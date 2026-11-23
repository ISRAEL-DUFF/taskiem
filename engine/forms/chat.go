package forms

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
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
// channel the chat's, so each field's source is `sent`. What each person
// sends is their own draft: two members of a role asked the same question
// do not mix their answers.

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
	// Held is what the person has sent towards it so far in a chat, by
	// field (a location opened, a photo as {media: {channel, id, mime}}),
	// and Waiting that it is whole but weaker than a field's
	// require_source.
	// Starter: put to the person who started its run (to: "starter"),
	// asked in Channel, the chat they started it from.
	Starter bool
	Channel string
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
}

// ErrNotWaiting is a chat message for a question no longer waiting: it
// was answered (in the app, or by another message), withdrawn or expired.
var ErrNotWaiting = errors.New("forms: the question is no longer waiting")

// ChatQuestions are the form steps waiting for the person, newest first:
// their business's own, to a role they hold or to them by name, and those
// put to them through a link still active, each with what they have sent
// towards it. An owner sees every role's requests under To fill in, but a
// photo or a location they send in a chat answers only what is theirs:
// the owner's own photo of a product is not a cashier's answer.
func (s *Service) ChatQuestions(ctx context.Context, who ChatAsker) ([]ChatQuestion, error) {
	var out []ChatQuestion
	var doors []RequestDoor
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{who.Tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT r.id, r.title, r.schema, r.created_at, t.name, COALESCE((r.recipient->>'starter')::boolean, false), COALESCE(r.recipient->>'channel', '') FROM form_requests r JOIN tenants t ON t.id = r.tenant_id
			WHERE r.status = 'pending' AND r.expires_at > now() AND r.sent_at IS NOT NULL
			  AND ((r.recipient ? 'role' AND r.recipient->>'role' = ANY($1)) OR r.recipient->>'member' = $2)
			ORDER BY r.created_at DESC LIMIT 20`, who.Roles, who.User.String())
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ChatQuestion, error) {
			q := ChatQuestion{Tenant: who.Tenant}
			return q, r.Scan(&q.Request, &q.Title, &q.Schema, &q.created, &q.From, &q.Starter, &q.Channel)
		})
		if err != nil {
			return err
		}
		for i := range out {
			if out[i].Held, out[i].Waiting, err = s.heldTx(ctx, tx, who.Tenant, out[i].Request, who.User); err != nil {
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
		if err != nil || len(doors) == 0 {
			return err
		}
		// Through a link still active only: once it has ended, the other
		// business's questions are no longer the person's to answer.
		linked, err := LinkedTx(ctx, tx)
		if err != nil {
			return err
		}
		doors = slices.DeleteFunc(doors, func(d RequestDoor) bool { return !linked[d.From] })
		return nil
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
			q.Held, q.Waiting, err = s.heldTx(ctx, tx, d.From, d.ID, who.User)
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

// heldTx is what user has sent in a chat towards a request, opened.
func (s *Service) heldTx(ctx context.Context, tx pgx.Tx, tenant, request, user uuid.UUID) (map[string]any, bool, error) {
	var raw []byte
	var waiting bool
	err := tx.QueryRow(ctx, `SELECT answers, held FROM form_chat_answers WHERE request_id = $1 AND user_id = $2`, request, user).Scan(&raw, &waiting)
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

// ChatAdded is what a chat message made of a question: the question with
// what the person now holds towards it, whether that is whole, and
// whether a field it holds is weaker than the field's require_source (a
// whole question so is held until the person sends it as it is).
type ChatAdded struct {
	Question    ChatQuestion
	Whole, Weak bool
}

// ChatAdd keeps what a chat sent for a field of q, as user's (a location
// sealed as the question's schema seals it). The request is locked while
// the person's draft is read again, merged and decided: of two messages
// arriving together, the second sees the first's field, so a question of
// a location and a photo is whole once, with both. ErrNotWaiting when the
// request no longer waits.
func (s *Service) ChatAdd(ctx context.Context, q ChatQuestion, user uuid.UUID, field string, value map[string]any) (ChatAdded, error) {
	var out ChatAdded
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{q.Tenant}, func(tx pgx.Tx) error {
		var status string
		var expires time.Time
		err := tx.QueryRow(ctx, `SELECT status, expires_at FROM form_requests WHERE id = $1 FOR UPDATE`, q.Request).Scan(&status, &expires)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotWaiting
		}
		if err != nil {
			return err
		}
		if status != "pending" || !s.now().Before(expires) {
			return ErrNotWaiting
		}
		held, _, err := s.heldTx(ctx, tx, q.Tenant, q.Request, user)
		if err != nil {
			return err
		}
		held[field] = value
		have := map[string]bool{}
		for k := range held {
			have[k] = true
		}
		out.Whole = ChatWhole(q.Schema, have)
		out.Weak = slices.ContainsFunc(WeakerInChat(q.Schema), func(f string) bool { return have[f] })
		plain, err := roundTrip(map[string]any{field: value})
		if err != nil {
			return err
		}
		sealed, err := pii.Seal(ctx, s.Vault, tx, q.Tenant, plain, pii.SchemaPaths(q.Schema, nil), pii.Taint{})
		if err != nil {
			return err
		}
		waiting := out.Whole && out.Weak
		_, err = tx.Exec(ctx, `INSERT INTO form_chat_answers (request_id, tenant_id, user_id, answers, held) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (request_id, user_id) DO UPDATE SET answers = form_chat_answers.answers || $4, held = $5, updated_at = now()`,
			q.Request, q.Tenant, user, sealed, waiting)
		if err != nil {
			return err
		}
		q.Held, q.Waiting = held, waiting
		out.Question = q
		return nil
	})
	return out, err
}

// ChatClear forgets what user has sent towards q in a chat (a photo that
// did not come through, an answer refused), to be sent again.
func (s *Service) ChatClear(ctx context.Context, q ChatQuestion, user uuid.UUID) error {
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{q.Tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM form_chat_answers WHERE request_id = $1 AND user_id = $2`, q.Request, user)
		return err
	})
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
// what every chat held towards it goes (Process) and a link's note is
// marked answered.
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
		return tx.QueryRow(ctx, `SELECT status, note FROM form_submissions WHERE reference = $1`, ref).Scan(&status, &n)
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
