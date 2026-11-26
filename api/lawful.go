package api

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ops"
	"github.com/israel-duff/taskiem/engine/peoplechats"
	"github.com/israel-duff/taskiem/engine/peoplesafety"
)

// Lawful requests (decision 0069 point 11; docs/spec/people-chatting.md
// §8.5; docs/runbooks/lawful-requests.md): Taskiem answers only proper
// legal process, gives the least that is asked, tells the person where
// the law allows, and publishes how many requests it received.
//
// In the operator console one operator records a request (who asked,
// their reference, the legal basis, whose chats and which dates); a
// second operator approves it; then the chats asked for, and nothing
// else, are exported opened, once per step-up, each export written to the
// platform audit chain. Whether and when the person was told is recorded,
// or why the law forbids it. Every step asks for the operator's passkey.
// GET /v1/transparency/lawful-requests publishes the counts.

func (s *Server) opsLawfulRoutes(r chi.Router) {
	r.Get("/lawful-requests", s.opsLawfulRequests)
	r.Post("/lawful-requests", s.opsRecordLawful)
	r.Get("/lawful-requests/{id}", s.opsLawfulRequest)
	r.Post("/lawful-requests/{id}/approve", s.opsLawfulStep("approve"))
	r.Post("/lawful-requests/{id}/refuse", s.opsLawfulStep("refuse"))
	r.Post("/lawful-requests/{id}/told", s.opsLawfulStep("told"))
	r.Post("/lawful-requests/{id}/export", s.opsLawfulExport)
}

type lawfulRequest struct {
	ID            uuid.UUID  `json:"id"`
	Authority     string     `json:"authority"`
	Reference     string     `json:"reference"`
	LegalBasis    string     `json:"legal_basis"`
	PersonID      *uuid.UUID `json:"person_id"`
	PersonName    string     `json:"person_name"`
	From          time.Time  `json:"from"`
	To            time.Time  `json:"to"`
	WithPersonID  *uuid.UUID `json:"with_person_id"`
	Files         bool       `json:"files"`
	ScopeNote     string     `json:"scope_note"`
	ReceivedAt    time.Time  `json:"received_at"`
	RecordedBy    string     `json:"recorded_by"`
	RecordedAt    time.Time  `json:"recorded_at"`
	ApprovedBy    *string    `json:"approved_by"`
	ApprovedAt    *time.Time `json:"approved_at"`
	State         string     `json:"state"`
	AnsweredBy    *string    `json:"answered_by"`
	AnsweredAt    *time.Time `json:"answered_at"`
	MessagesGiven *int       `json:"messages_given"`
	RefusedReason *string    `json:"refused_reason"`
	ToldAt        *time.Time `json:"told_at"`
	NotToldReason *string    `json:"not_told_reason"`
	// Groups: whether the request asks for the person's group messages:
	// none; sent (their own messages in groups); seen (every group message
	// they could see, within their own time in each group).
	Groups string `json:"groups"`
}

func (s *Server) lawfulTx(r *http.Request, id *uuid.UUID) ([]lawfulRequest, error) {
	var out []lawfulRequest
	err := s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		rows, err := tx.Query(r.Context(), `SELECT * FROM taskiem_ops_lawful_requests($1)`, id)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[lawfulRequest])
		return err
	})
	return out, err
}

func (s *Server) opsLawfulRequests(w http.ResponseWriter, r *http.Request) {
	out, err := s.lawfulTx(r, nil)
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": nonNil(out)})
}

func (s *Server) opsLawfulRequest(w http.ResponseWriter, r *http.Request) {
	id, ok := opsID(w, r, "id", "request")
	if !ok {
		return
	}
	out, err := s.lawfulTx(r, &id)
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	if len(out) == 0 {
		writeErr(w, http.StatusNotFound, "no such request")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request": out[0]})
}

// opsRecordLawful is POST /v1/ops/lawful-requests: records a request as
// it was received. A step-up for the person it names.
func (s *Server) opsRecordLawful(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Authority    string     `json:"authority"`
		Reference    string     `json:"reference"`
		LegalBasis   string     `json:"legal_basis"`
		PersonID     uuid.UUID  `json:"person_id"`
		From         time.Time  `json:"from"`
		To           time.Time  `json:"to"`
		WithPersonID *uuid.UUID `json:"with_person_id,omitempty"`
		Files        bool       `json:"files"`
		ScopeNote    string     `json:"scope_note"`
		ReceivedAt   time.Time  `json:"received_at"`
		Groups       string     `json:"groups"`
		StepUp       *opsStepUp `json:"step_up,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	for _, f := range []struct {
		v    *string
		max  int
		name string
	}{{&req.Authority, 200, "authority"}, {&req.Reference, 200, "reference"}, {&req.LegalBasis, 500, "legal_basis"}} {
		*f.v = strings.TrimSpace(*f.v)
		if *f.v == "" || utf8.RuneCountInString(*f.v) > f.max {
			writeErr(w, http.StatusUnprocessableEntity, f.name+" is needed (who asked, their reference, and the law they asked under)")
			return
		}
	}
	if req.PersonID == uuid.Nil || req.From.IsZero() || !req.To.After(req.From) || req.ReceivedAt.IsZero() {
		writeErr(w, http.StatusUnprocessableEntity, "name the person (person_id), the dates asked for (from before to) and when it was received (received_at)")
		return
	}
	switch req.Groups {
	case "":
		req.Groups = "none"
	case "none", "sent", "seen":
	default:
		writeErr(w, http.StatusUnprocessableEntity, "groups is none, sent (the person's own messages in groups) or seen (every group message they could see)")
		return
	}
	if utf8.RuneCountInString(req.ScopeNote) > 1000 {
		writeErr(w, http.StatusUnprocessableEntity, "scope_note is up to 1,000 characters")
		return
	}
	if !s.checkOpsStepUp(w, r, req.StepUp, "ops.lawful.record", req.PersonID.String()) {
		return
	}
	o := operatorFrom(ctx)
	id := uuid.Must(uuid.NewV7())
	err := s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		if _, err := tx.Exec(ctx, `SELECT taskiem_ops_lawful_record($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`, id, req.Authority, req.Reference,
			req.LegalBasis, req.PersonID, req.From, req.To, req.WithPersonID, req.Files, req.ScopeNote, req.ReceivedAt, o.Actor(), req.Groups); err != nil {
			return err
		}
		return ops.AuditTx(ctx, tx, o.Actor(), "ops.lawful.record", id.String(), map[string]any{"authority": req.Authority, "reference": req.Reference,
			"person": req.PersonID, "from": req.From, "to": req.To, "with": req.WithPersonID, "files": req.Files, "groups": req.Groups})
	})
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	out, err := s.lawfulTx(r, &id)
	if err != nil || len(out) == 0 {
		s.opsDBFail(w, r, errors.Join(err, errOpsNotFound))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"request": out[0]})
}

// opsLawfulStep is POST /v1/ops/lawful-requests/{id}/{approve|refuse|told}:
// approve (a second operator: the one who recorded it is refused, 403),
// refuse {reason}, told {told_at} or {reason} (why the law forbids it).
func (s *Server) opsLawfulStep(step string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		id, ok := opsID(w, r, "id", "request")
		if !ok {
			return
		}
		var req struct {
			Reason string     `json:"reason"`
			ToldAt *time.Time `json:"told_at,omitempty"`
			StepUp *opsStepUp `json:"step_up,omitempty"`
		}
		if err := decodeBody(r, &req); err != nil {
			s.fail(w, r, err)
			return
		}
		req.Reason = strings.TrimSpace(req.Reason)
		if utf8.RuneCountInString(req.Reason) > 1000 {
			writeErr(w, http.StatusUnprocessableEntity, "a reason is up to 1,000 characters")
			return
		}
		op := "ops.lawful." + step
		if !s.checkOpsStepUp(w, r, req.StepUp, op, id.String()) {
			return
		}
		o := operatorFrom(ctx)
		err := s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
			if _, err := tx.Exec(ctx, `SELECT taskiem_ops_lawful_step($1, $2, $3, NULL, $4, $5)`, id, step, o.Actor(), req.Reason, req.ToldAt); err != nil {
				return err
			}
			return ops.AuditTx(ctx, tx, o.Actor(), op, id.String(), map[string]any{"reason": req.Reason, "told_at": req.ToldAt})
		})
		if err != nil {
			s.opsDBFail(w, r, err)
			return
		}
		out, err := s.lawfulTx(r, &id)
		if err != nil || len(out) == 0 {
			s.opsDBFail(w, r, errors.Join(err, errOpsNotFound))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"request": out[0]})
	}
}

// lawfulMessage is one message of an export, opened.
type lawfulMessage struct {
	ID         uuid.UUID       `json:"id"`
	FromPerson bool            `json:"from_person"`
	Kind       string          `json:"kind"`
	Body       string          `json:"body"`
	Card       *peopleCard     `json:"card,omitempty"`
	Location   *peopleLocation `json:"location,omitempty"`
	Attachment map[string]any  `json:"attachment,omitempty"`
	// File is the photo or file, base64, when the request asked for files.
	File    *string   `json:"file,omitempty"`
	Deleted bool      `json:"deleted,omitempty"`
	At      time.Time `json:"at"`
}

type lawfulChat struct {
	ChatID   uuid.UUID       `json:"chat_id"`
	With     *uuid.UUID      `json:"with"`
	WithName string          `json:"with_name"`
	Messages []lawfulMessage `json:"messages"`
}

// opsLawfulExport is POST /v1/ops/lawful-requests/{id}/export {step_up}:
// once a second operator approved it, exactly the scope asked (the
// person's own copies of their people chats, between the dates, with one
// person when it names one, with files only when it asks for them),
// opened. Recorded in the platform chain with the count given, and the
// request marked answered.
func (s *Server) opsLawfulExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := opsID(w, r, "id", "request")
	if !ok {
		return
	}
	var req struct {
		StepUp *opsStepUp `json:"step_up,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.checkOpsStepUp(w, r, req.StepUp, "ops.lawful.export", id.String()) {
		return
	}
	o := operatorFrom(ctx)
	list, err := s.lawfulTx(r, &id)
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	if len(list) == 0 {
		writeErr(w, http.StatusNotFound, "no such request")
		return
	}
	lr := list[0]
	if lr.State != "approved" && lr.State != "answered" {
		writeErr(w, http.StatusConflict, "a second operator approves a request before anything is exported")
		return
	}
	if lr.PersonID == nil {
		writeErr(w, http.StatusConflict, "the account was deleted: nothing is kept to export")
		return
	}
	person := *lr.PersonID
	var chats []lawfulChat
	var groups []lawfulGroup
	given, files := 0, 0
	// One transaction: what is read and its record go together (an export
	// that cannot be recorded is not given, and what is recorded is what
	// was read). The reads are the person's own (their scope set, as
	// their own transaction would be); the record is the operator's
	// (their scope cleared again).
	err = s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		chats, given, files = nil, 0, 0
		groups = nil
		if err := personScopeTx(ctx, tx, person); err != nil {
			return err
		}
		var err error
		if chats, given, files, err = s.lawfulChatsTx(ctx, tx, person, lr); err != nil {
			return err
		}
		if lr.Groups != "none" {
			var n, f int
			if groups, n, f, err = s.lawfulGroupsTx(ctx, tx, person, lr); err != nil {
				return err
			}
			given, files = given+n, files+f
		}
		if err := personScopeTx(ctx, tx, uuid.Nil); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT taskiem_ops_lawful_step($1, 'answered', $2, $3, NULL, NULL)`, id, o.Actor(), given); err != nil {
			return err
		}
		return ops.AuditTx(ctx, tx, o.Actor(), "ops.lawful.export", id.String(), map[string]any{"why": "lawful request " + lr.Reference,
			"authority": lr.Authority, "person": person, "from": lr.From, "to": lr.To, "with": lr.WithPersonID, "groups": lr.Groups, "group_count": len(groups),
			"messages": given, "files": files})
	})
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="lawful-request-`+id.String()+`.json"`)
	writeJSON(w, http.StatusOK, map[string]any{"request": lr, "person_id": person, "chats": nonNil(chats), "groups": nonNil(groups), "messages_given": given,
		"files_given": files,
		"exported_by": o.Actor(), "exported_at": time.Now().UTC()})
}

// personScopeTx sets (or, with uuid.Nil, clears) the person whose rows
// tx reads, as db.InPersonTx does, for the rest of tx.
func personScopeTx(ctx context.Context, tx pgx.Tx, person uuid.UUID) error {
	v := ""
	if person != uuid.Nil {
		v = person.String()
	}
	_, err := tx.Exec(ctx, "SELECT set_config('app.person_scope', $1, true)", v)
	return err
}

// lawfulChatsTx reads the person's one-to-one messages within the
// request's scope, in tx scoped to the person: the chats with messages,
// how many messages and how many files.
func (s *Server) lawfulChatsTx(ctx context.Context, tx pgx.Tx, person uuid.UUID, lr lawfulRequest) ([]lawfulChat, int, int, error) {
	var chats []lawfulChat
	given, files := 0, 0
	key, err := s.keys().KeyTx(ctx, tx, person, false)
	if errors.Is(err, peoplechats.ErrNoKey) {
		return nil, 0, 0, nil
	}
	if err != nil {
		return nil, 0, 0, err
	}
	rows, err := tx.Query(ctx, `SELECT id, peer_id FROM people_chats WHERE person_id = $1 AND ($2::uuid IS NULL OR peer_id = $2) ORDER BY created_at`,
		person, lr.WithPersonID)
	if err != nil {
		return nil, 0, 0, err
	}
	type ref struct {
		id   uuid.UUID
		peer *uuid.UUID
	}
	refs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ref, error) {
		var x ref
		return x, r.Scan(&x.id, &x.peer)
	})
	if err != nil {
		return nil, 0, 0, err
	}
	var peers []uuid.UUID
	for _, c := range refs {
		if c.peer != nil {
			peers = append(peers, *c.peer)
		}
	}
	names, err := peopleNamesTx(ctx, tx, peers)
	if err != nil {
		return nil, 0, 0, err
	}
	for _, c := range refs {
		rows, err := tx.Query(ctx, `SELECT m.id, m.mine, m.kind, m.sealed, m.attachment, m.deleted_at IS NOT NULL, m.created_at, f.sealed
			FROM people_messages m LEFT JOIN people_files f ON $4 AND f.person_id = m.person_id AND f.message_id = m.id AND f.kind = 'file'
			WHERE m.person_id = $1 AND m.chat_id = $2 AND m.created_at >= $3 AND m.created_at < $5 ORDER BY m.created_at, m.id`,
			person, c.id, lr.From, lr.Files, lr.To)
		if err != nil {
			return nil, 0, 0, err
		}
		msgs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (lawfulMessage, error) {
			var m lawfulMessage
			var sealed, file []byte
			if err := r.Scan(&m.ID, &m.FromPerson, &m.Kind, &sealed, &m.Attachment, &m.Deleted, &m.At, &file); err != nil {
				return m, err
			}
			if v, err := openSealed(key, person, m.ID, sealed); err == nil {
				m.Body, m.Card, m.Location = v.Body, v.Card, v.Location
				if m.Attachment != nil && v.Name != "" {
					m.Attachment["name"] = v.Name
				}
			}
			if file != nil {
				if data, err := peoplechats.Open(key, person, fileRow(m.ID), file); err == nil {
					b := base64.StdEncoding.EncodeToString(data)
					m.File = &b
					files++
				}
			}
			return m, nil
		})
		if err != nil {
			return nil, 0, 0, err
		}
		if len(msgs) == 0 {
			continue
		}
		given += len(msgs)
		lc := lawfulChat{ChatID: c.id, With: c.peer, Messages: msgs}
		if c.peer != nil {
			lc.WithName = names.name[*c.peer]
			if names.gone[*c.peer] {
				lc.WithName = peopleGoneName
			}
		}
		chats = append(chats, lc)
	}
	return chats, given, files, nil
}

// lawfulCounts is GET /v1/transparency/lawful-requests, for anyone: how
// many lawful requests Taskiem received, answered and refused, each
// half-year that has ended (decision 0069 point 11). The current half-year
// is left out until it ends, and nothing says what is still being looked
// at, so the count never moves when one person's request comes in.
func (s *Server) lawfulCounts(w http.ResponseWriter, r *http.Request) {
	type period struct {
		peoplesafety.Half
		Label    string `json:"label"`
		Received int64  `json:"received"`
		Answered int64  `json:"answered"`
		Refused  int64  `json:"refused"`
	}
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT year, half, received, answered, refused FROM taskiem_lawful_request_counts()`)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (period, error) {
		var p period
		err := r.Scan(&p.Year, &p.Half.Half, &p.Received, &p.Answered, &p.Refused)
		p.Label = p.Half.Label()
		return p, err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, map[string]any{"periods": nonNil(out),
		"words": "Taskiem answers only proper legal process, gives the least that is asked, and tells the person where the law allows. These are the requests for people's data Taskiem received from authorities, by the half-year they were received in. A half-year is shown once it has ended."})
}
