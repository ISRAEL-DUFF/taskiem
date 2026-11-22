package forms

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/pii"
)

// A form step answered in WhatsApp or Telegram (device fields §3, D1): a
// location message and a photo message answer the question open for the
// person, field by field, kept (form_chat_answers, a location sealed)
// until the question is whole; then it is sent as any answer is, its
// channel the chat's, so each field's source is `sent`.

// ChatQuestion is a form step a chat message may answer.
type ChatQuestion struct {
	// Tenant is the asking business; Request its form request.
	Tenant  uuid.UUID
	Request uuid.UUID
	Title   string
	Schema  map[string]any
	// From is the asking business's name, Through that it was put to the
	// person's business through a link (its note is in their scope).
	From    string
	Through bool
	// Held is what the chat has sent towards it so far, by field (a
	// location opened, a photo as {media: {channel, id, mime}}), and
	// Waiting that it is whole but weaker than a field's require_source.
	Held    map[string]any
	Waiting bool
	created time.Time
}

// ChatAsker is the person a chat message comes from, in the business the
// conversation is with (their own).
type ChatAsker struct {
	Tenant uuid.UUID
	User   uuid.UUID
	Roles  []string
	Owner  bool
}

// ChatQuestions are the form steps waiting for the person, newest first:
// their business's own (to their role or to them) and those put to them
// through a link, each with what the chat has sent towards it.
func (s *Service) ChatQuestions(ctx context.Context, who ChatAsker) ([]ChatQuestion, error) {
	var out []ChatQuestion
	var doors []RequestDoor
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{who.Tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT r.id, r.title, r.schema, r.created_at, t.name FROM form_requests r JOIN tenants t ON t.id = r.tenant_id
			WHERE r.status = 'pending' AND r.expires_at > now() AND r.sent_at IS NOT NULL
			  AND ((r.recipient ? 'role' AND ($1 OR r.recipient->>'role' = ANY($2))) OR r.recipient->>'member' = $3)
			ORDER BY r.created_at DESC LIMIT 20`, who.Owner, who.Roles, who.User.String())
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ChatQuestion, error) {
			q := ChatQuestion{Tenant: who.Tenant}
			return q, r.Scan(&q.Request, &q.Title, &q.Schema, &q.created, &q.From)
		})
		if err != nil {
			return err
		}
		for i := range out {
			if out[i].Held, out[i].Waiting, err = s.heldTx(ctx, tx, who.Tenant, out[i].Request, out[i].Schema); err != nil {
				return err
			}
		}
		rows, err = tx.Query(ctx, `SELECT id, from_tenant, user_id, business, title, status, expires_at, created_at FROM form_request_doors
			WHERE user_id = $1 AND status = 'pending' AND expires_at > now() ORDER BY created_at DESC LIMIT 20`, who.User)
		if err != nil {
			return err
		}
		doors, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (RequestDoor, error) {
			var d RequestDoor
			return d, r.Scan(&d.ID, &d.From, &d.User, &d.Business, &d.Title, &d.Status, &d.ExpiresAt, &d.CreatedAt)
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	for _, d := range doors {
		// The request is the other business's: read in its scope, trusted
		// by the note the door left in the person's.
		q := ChatQuestion{Tenant: d.From, Request: d.ID, Title: d.Title, From: d.Business, Through: true, created: d.CreatedAt}
		var status string
		err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{d.From}, func(tx pgx.Tx) error {
			if err := tx.QueryRow(ctx, `SELECT schema, status FROM form_requests WHERE id = $1`, d.ID).Scan(&q.Schema, &status); err != nil {
				return err
			}
			var err error
			q.Held, q.Waiting, err = s.heldTx(ctx, tx, d.From, d.ID, q.Schema)
			return err
		})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if status == "pending" {
			out = append(out, q)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].created.After(out[j].created) })
	return out, nil
}

func (s *Service) heldTx(ctx context.Context, tx pgx.Tx, tenant, request uuid.UUID, schema map[string]any) (map[string]any, bool, error) {
	var raw []byte
	var waiting bool
	err := tx.QueryRow(ctx, `SELECT answers, held FROM form_chat_answers WHERE request_id = $1`, request).Scan(&raw, &waiting)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[string]any{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	v, err := expr.DecodeJSON(raw)
	if err != nil {
		return nil, false, err
	}
	if s.Vault != nil {
		if v, err = pii.Open(ctx, s.Vault, tx, tenant, v, pii.Taint{}); err != nil {
			return nil, false, err
		}
	}
	m, _ := v.(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	return m, waiting, nil
}

// ChatHold keeps what a chat sent for a field of q (a location sealed as
// the question's schema seals it), and whether the question now waits on
// a stronger answer; it returns q with what is held.
func (s *Service) ChatHold(ctx context.Context, q ChatQuestion, user uuid.UUID, field string, value map[string]any, waiting bool) (ChatQuestion, error) {
	held := map[string]any{}
	for k, v := range q.Held {
		held[k] = v
	}
	if field != "" {
		held[field] = value
	}
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{q.Tenant}, func(tx pgx.Tx) error {
		plain, err := roundTrip(held)
		if err != nil {
			return err
		}
		sealed, err := pii.Seal(ctx, s.Vault, tx, q.Tenant, plain, pii.SchemaPaths(q.Schema, nil), pii.Taint{})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO form_chat_answers (request_id, tenant_id, user_id, answers, held) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (request_id) DO UPDATE SET answers = $4, held = $5, user_id = $3, updated_at = now()`, q.Request, q.Tenant, user, sealed, waiting)
		return err
	})
	q.Held, q.Waiting = held, waiting
	return q, err
}

// ChatAnswer is a whole question sent from a chat.
type ChatAnswer struct {
	Question ChatQuestion
	// Channel is whatsapp or telegram.
	Channel string
	// User is who answered; Business their own business, when the
	// question came through a link.
	User     uuid.UUID
	Business uuid.UUID
	// Answers are the held locations; Uploads the photos, fetched.
	Answers map[string]any
	Uploads []Upload
}

// ChatSubmit sends a question answered in a chat, as any answer is sent:
// it is refused in plain words (the second value) or answered, and then
// what the chat held goes, and a link's note is marked answered.
func (s *Service) ChatSubmit(ctx context.Context, a ChatAnswer) (status, note string, err error) {
	q := a.Question
	r := Respondent{Kind: "member", MemberID: a.User.String()}
	if q.Through {
		r = Respondent{Kind: "linked", MemberID: a.User.String(), Business: a.Business.String()}
	}
	key := make([]byte, 18)
	_, _ = rand.Read(key)
	h := sha256.Sum256([]byte(hex.EncodeToString(key)))
	ref := NewReference()
	in := Incoming{Tenant: q.Tenant, Request: &q.Request, Reference: ref, StatusHash: h[:], Uploads: a.Uploads,
		Submission: Submission{Answers: a.Answers, Respondent: r, Channel: a.Channel, Submitted: s.now()}}
	for _, u := range a.Uploads {
		in.Submission.Files = append(in.Submission.Files, u.Meta)
	}
	if err := s.Process(ctx, in); err != nil {
		return "", "", err
	}
	var n *string
	err = db.InTenantTx(ctx, s.Pool, []uuid.UUID{q.Tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT status, note FROM form_submissions WHERE reference = $1`, ref).Scan(&status, &n); err != nil {
			return err
		}
		if status == "refused" {
			return nil
		}
		_, err := tx.Exec(ctx, `DELETE FROM form_chat_answers WHERE request_id = $1`, q.Request)
		return err
	})
	if err != nil {
		return "", "", err
	}
	if n != nil {
		note = *n
	}
	if status != "refused" && q.Through && a.Business != uuid.Nil {
		err = db.InTenantTx(ctx, s.Pool, []uuid.UUID{a.Business}, func(tx pgx.Tx) error { return AnsweredDoorTx(ctx, tx, q.Request) })
	}
	return status, note, err
}
