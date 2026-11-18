package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ops"
	"github.com/israel-duff/taskiem/engine/peoplesafety"
	"github.com/israel-duff/taskiem/engine/telemetry"
)

// The moderators' side of people chats in the operator console
// (docs/spec/people-chatting.md §8.3, §8.4, "As built: stage 5a gaps,
// safety and moderation"; decision 0069 point 3): people's reports of
// people and of businesses, the messages the checks held and their
// verdicts, what moderators decide about a person and the appeals, and
// how much the kept chats take.
//
// No one at Taskiem reads a people chat except a moderator handling a
// report of it, or under a lawful request (lawful.go). Every opening of a
// report, a held message or a file in them says why (?why=) and is
// written to the platform audit chain in the same transaction that reads
// it, with who, what, why and when; nothing is shown if it cannot be
// recorded. Every write asks for the operator's passkey again.

func (s *Server) opsPeopleRoutes(r chi.Router) {
	r.Get("/people/overview", s.opsPeopleOverview)
	r.Get("/people/reports", s.opsPeopleReports)
	r.Get("/people/reports/{id}", s.opsPeopleReport)
	r.Get("/people/reports/{id}/files/{message}", s.opsPeopleReportFile)
	r.Post("/people/reports/{id}/review", s.opsReviewReport("people"))
	r.Get("/customer-reports", s.opsCustomerReports)
	r.Get("/customer-reports/{id}", s.opsCustomerReport)
	r.Post("/customer-reports/{id}/review", s.opsReviewReport("customer"))
	r.Get("/people/held", s.opsPeopleHeld)
	r.Get("/people/held/{id}", s.opsPeopleHeldOne)
	r.Get("/people/held/{id}/file", s.opsPeopleHeldFile)
	r.Post("/people/held/{id}/{decision}", s.opsDecideHeld)
	r.Get("/people/verdicts", s.opsPeopleVerdicts)
	r.Get("/people/persons/{id}", s.opsPeoplePerson)
	r.Post("/people/persons/{id}/actions", s.opsModeratePerson)
	r.Post("/people/actions/{id}/lift", s.opsLiftAction)
	r.Get("/people/appeals", s.opsPeopleAppeals)
	r.Post("/people/appeals/{id}/{decision}", s.opsDecideAppeal)
	r.Get("/people/storage", s.opsPeopleStorage)
	s.opsLawfulRoutes(r)
}

// opsWhy is the reason an operator gives for opening something of a
// person's: required, and recorded with the opening.
func opsWhy(w http.ResponseWriter, r *http.Request) (string, bool) {
	why := strings.TrimSpace(r.URL.Query().Get("why"))
	if utf8.RuneCountInString(why) < 3 || utf8.RuneCountInString(why) > 500 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Say why you are opening this (why, 3 to 500 characters): it is recorded with your name.", "why": true})
		return "", false
	}
	return why, true
}

func opsID(w http.ResponseWriter, r *http.Request, name, what string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such "+what)
		return uuid.Nil, false
	}
	return id, true
}

func statusParam(r *http.Request, def string, allowed ...string) string {
	st := r.URL.Query().Get("status")
	if st == "" {
		st = r.URL.Query().Get("state")
	}
	for _, a := range allowed {
		if st == a {
			return st
		}
	}
	return def
}

// --- the overview ---

func (s *Server) opsPeopleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := map[string]any{}
	err := s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		var reports, customer, held, appeals, lawful int64
		if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM taskiem_ops_people_reports('open')), (SELECT count(*) FROM taskiem_ops_customer_reports('open')),
			(SELECT count(*) FROM taskiem_ops_people_held('held')), (SELECT count(*) FROM taskiem_ops_people_appeals('open')),
			(SELECT count(*) FROM taskiem_ops_lawful_requests(NULL) WHERE state IN ('recorded', 'approved'))`).Scan(&reports, &customer, &held, &appeals, &lawful); err != nil {
			return err
		}
		out["open"] = map[string]int64{"people_reports": reports, "customer_reports": customer, "held": held, "appeals": appeals, "lawful_requests": lawful}
		st, err := telemetry.PeopleStorage(ctx, tx)
		if err != nil {
			return err
		}
		out["storage"] = nonNil(st)
		out["storage_words"] = storageWords(st)
		return nil
	})
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) opsPeopleStorage(w http.ResponseWriter, r *http.Request) {
	st, err := telemetry.PeopleStorage(r.Context(), s.Store.Pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tables": nonNil(st), "words": storageWords(st)})
}

// --- reports ---

type opsPeopleReportRow struct {
	ID                uuid.UUID  `json:"id"`
	ReporterID        *uuid.UUID `json:"reporter_id"`
	Detached          bool       `json:"detached"`
	ReportedID        *uuid.UUID `json:"reported_id"`
	ReportedName      string     `json:"reported_name"`
	ReportedGone      bool       `json:"reported_gone"`
	ReportedSuspended bool       `json:"reported_suspended"`
	Reason            string     `json:"reason"`
	Note              string     `json:"note"`
	FromRequest       bool       `json:"from_request"`
	Files             int        `json:"files"`
	Sealed            bool       `json:"sealed"`
	Status            string     `json:"status"`
	CreatedAt         time.Time  `json:"created_at"`
	ReviewedBy        *string    `json:"reviewed_by"`
	ReviewedAt        *time.Time `json:"reviewed_at"`
	ReportsAgainst    int64      `json:"reports_against"`
}

func (s *Server) opsPeopleReports(w http.ResponseWriter, r *http.Request) {
	status := statusParam(r, "open", "open", "reviewed", "all")
	var out []opsPeopleReportRow
	err := s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		rows, err := tx.Query(r.Context(), `SELECT * FROM taskiem_ops_people_reports($1)`, status)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[opsPeopleReportRow])
		return err
	})
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reports": nonNil(out)})
}

// opsPeopleReport is GET /v1/ops/people/reports/{id}?why=: the report
// opened, its messages and which files it keeps. Recorded first.
func (s *Server) opsPeopleReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := opsID(w, r, "id", "report")
	if !ok {
		return
	}
	why, ok := opsWhy(w, r)
	if !ok {
		return
	}
	o := operatorFrom(ctx)
	var rep struct {
		ID           uuid.UUID  `json:"id"`
		ReporterID   *uuid.UUID `json:"reporter_id"`
		ReportedID   *uuid.UUID `json:"reported_id"`
		ReportedName string     `json:"reported_name"`
		Reason       string     `json:"reason"`
		Note         string     `json:"note"`
		FromRequest  bool       `json:"from_request"`
		Status       string     `json:"status"`
		CreatedAt    time.Time  `json:"created_at"`
		ReviewedBy   *string    `json:"reviewed_by"`
		ReviewedAt   *time.Time `json:"reviewed_at"`
		ReviewNote   *string    `json:"review_note"`
	}
	var plain []byte
	var sealed []byte
	var boxKey, kmsKey *string
	type file struct {
		MessageID uuid.UUID `json:"message_id"`
		Type      string    `json:"type"`
		Size      int64     `json:"size"`
	}
	var files []file
	err := s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		err := tx.QueryRow(ctx, `SELECT id, reporter_id, reported_id, reported_name, reason, note, from_request, messages, sealed, box_key, kms_key, status,
			created_at, reviewed_by, reviewed_at, review_note FROM taskiem_ops_people_report($1)`, id).Scan(&rep.ID, &rep.ReporterID, &rep.ReportedID,
			&rep.ReportedName, &rep.Reason, &rep.Note, &rep.FromRequest, &plain, &sealed, &boxKey, &kmsKey, &rep.Status, &rep.CreatedAt, &rep.ReviewedBy,
			&rep.ReviewedAt, &rep.ReviewNote)
		if errors.Is(err, pgx.ErrNoRows) {
			return errOpsNotFound
		}
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT message_id, type, size FROM taskiem_ops_people_report_files($1)`, id)
		if err != nil {
			return err
		}
		if files, err = pgx.CollectRows(rows, pgx.RowToStructByPos[file]); err != nil {
			return err
		}
		return ops.AuditTx(ctx, tx, o.Actor(), "ops.people.report.open", id.String(), map[string]any{"why": why, "reported": rep.ReportedID, "files": len(files)})
	})
	if errors.Is(err, errOpsNotFound) {
		writeErr(w, http.StatusNotFound, "no such report")
		return
	}
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	raw := plain
	if sealed != nil {
		box, err := peoplesafety.OpenBox(ctx, s.Vault, peoplesafety.ReportPurpose(id), id, *boxKey, *kmsKey)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if raw, err = box.Open(peoplesafety.MessagesRow, sealed); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	var msgs []reportMessage
	if err := json.Unmarshal(raw, &msgs); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"report": rep, "messages": nonNil(msgs), "files": nonNil(files), "sealed": sealed != nil})
}

var errOpsNotFound = errors.New("not found")

// opsPeopleReportFile is GET /v1/ops/people/reports/{id}/files/{message}
// ?why=: one photo or file a report keeps, opened. Recorded first.
func (s *Server) opsPeopleReportFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := opsID(w, r, "id", "report")
	if !ok {
		return
	}
	msg, ok := opsID(w, r, "message", "file")
	if !ok {
		return
	}
	why, ok := opsWhy(w, r)
	if !ok {
		return
	}
	o := operatorFrom(ctx)
	var ctype, boxKey, kmsKey string
	var sealed []byte
	err := s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		err := tx.QueryRow(ctx, `SELECT type, sealed, box_key, kms_key FROM taskiem_ops_people_report_file($1, $2)`, id, msg).Scan(&ctype, &sealed, &boxKey, &kmsKey)
		if errors.Is(err, pgx.ErrNoRows) {
			return errOpsNotFound
		}
		if err != nil {
			return err
		}
		return ops.AuditTx(ctx, tx, o.Actor(), "ops.people.report.file", id.String()+"/"+msg.String(), map[string]any{"why": why})
	})
	if errors.Is(err, errOpsNotFound) {
		writeErr(w, http.StatusNotFound, "no such file")
		return
	}
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	box, err := peoplesafety.OpenBox(ctx, s.Vault, peoplesafety.ReportPurpose(id), id, boxKey, kmsKey)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data, err := box.Open(peoplesafety.FileRow(msg), sealed)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeOpsFile(w, ctype, data)
}

// writeOpsFile answers a person's file to an operator: never cached,
// never sniffed, downloaded unless it is a photo.
func writeOpsFile(w http.ResponseWriter, ctype string, data []byte) {
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if !strings.HasPrefix(ctype, "image/") {
		w.Header().Set("Content-Disposition", "attachment")
	}
	_, _ = w.Write(data)
}

type opsCustomerReportRow struct {
	ID         uuid.UUID  `json:"id"`
	ReporterID *uuid.UUID `json:"reporter_id"`
	Detached   bool       `json:"detached"`
	TenantID   uuid.UUID  `json:"tenant_id"`
	Business   string     `json:"business"`
	Reason     string     `json:"reason"`
	Note       string     `json:"note"`
	Messages   int        `json:"messages"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	ReviewedBy *string    `json:"reviewed_by"`
	ReviewedAt *time.Time `json:"reviewed_at"`
}

func (s *Server) opsCustomerReports(w http.ResponseWriter, r *http.Request) {
	status := statusParam(r, "open", "open", "reviewed", "all")
	var out []opsCustomerReportRow
	err := s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		rows, err := tx.Query(r.Context(), `SELECT * FROM taskiem_ops_customer_reports($1)`, status)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[opsCustomerReportRow])
		return err
	})
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reports": nonNil(out)})
}

// opsCustomerReport is GET /v1/ops/customer-reports/{id}?why=: a person's
// report of a business, with the messages they sent. Recorded first.
func (s *Server) opsCustomerReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := opsID(w, r, "id", "report")
	if !ok {
		return
	}
	why, ok := opsWhy(w, r)
	if !ok {
		return
	}
	o := operatorFrom(ctx)
	var rep struct {
		ID         uuid.UUID       `json:"id"`
		ReporterID *uuid.UUID      `json:"reporter_id"`
		TenantID   uuid.UUID       `json:"tenant_id"`
		Business   string          `json:"business"`
		Reason     string          `json:"reason"`
		Note       string          `json:"note"`
		Messages   json.RawMessage `json:"messages"`
		Status     string          `json:"status"`
		CreatedAt  time.Time       `json:"created_at"`
		ReviewedBy *string         `json:"reviewed_by"`
		ReviewedAt *time.Time      `json:"reviewed_at"`
		ReviewNote *string         `json:"review_note"`
	}
	err := s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		err := tx.QueryRow(ctx, `SELECT * FROM taskiem_ops_customer_report($1)`, id).Scan(&rep.ID, &rep.ReporterID, &rep.TenantID, &rep.Business, &rep.Reason,
			&rep.Note, &rep.Messages, &rep.Status, &rep.CreatedAt, &rep.ReviewedBy, &rep.ReviewedAt, &rep.ReviewNote)
		if errors.Is(err, pgx.ErrNoRows) {
			return errOpsNotFound
		}
		if err != nil {
			return err
		}
		return ops.AuditTx(ctx, tx, o.Actor(), "ops.customer.report.open", id.String(), map[string]any{"why": why, "business": rep.TenantID})
	})
	if errors.Is(err, errOpsNotFound) {
		writeErr(w, http.StatusNotFound, "no such report")
		return
	}
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"report": rep})
}

// opsReviewReport is POST …/{id}/review {note?, step_up}: the report is
// marked reviewed by the operator (kept a year from now, then purged).
func (s *Server) opsReviewReport(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := opsID(w, r, "id", "report")
		if !ok {
			return
		}
		var req struct {
			Note   string     `json:"note"`
			StepUp *opsStepUp `json:"step_up,omitempty"`
		}
		if err := decodeBody(r, &req); err != nil {
			s.fail(w, r, err)
			return
		}
		if utf8.RuneCountInString(req.Note) > 1000 {
			writeErr(w, http.StatusUnprocessableEntity, "a note is up to 1,000 characters")
			return
		}
		op := map[string]string{"people": "ops.people.report.review", "customer": "ops.customer_report.review"}[kind]
		if !s.checkOpsStepUp(w, r, req.StepUp, op, id.String()) {
			return
		}
		o := operatorFrom(r.Context())
		err := s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
			if _, err := tx.Exec(r.Context(), `SELECT taskiem_ops_report_review($1, $2, $3, $4)`, kind, id, o.Actor(), req.Note); err != nil {
				return err
			}
			return ops.AuditTx(r.Context(), tx, o.Actor(), op, id.String(), map[string]any{"note": req.Note})
		})
		if err != nil {
			s.opsDBFail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "reviewed"})
	}
}

// --- held messages and verdicts ---

type opsHeldRow struct {
	ID            uuid.UUID  `json:"id"`
	SenderID      *uuid.UUID `json:"sender_id"`
	SenderName    string     `json:"sender_name"`
	RecipientID   *uuid.UUID `json:"recipient_id"`
	RecipientName string     `json:"recipient_name"`
	Kind          string     `json:"kind"`
	Reasons       []string   `json:"reasons"`
	HasFile       bool       `json:"has_file"`
	State         string     `json:"state"`
	CreatedAt     time.Time  `json:"created_at"`
	DecidedBy     *string    `json:"decided_by"`
	DecidedAt     *time.Time `json:"decided_at"`
}

func (s *Server) opsPeopleHeld(w http.ResponseWriter, r *http.Request) {
	state := statusParam(r, "held", "held", "released", "confirmed", "all")
	var out []opsHeldRow
	err := s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		rows, err := tx.Query(r.Context(), `SELECT * FROM taskiem_ops_people_held($1)`, state)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[opsHeldRow])
		return err
	})
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"held": nonNil(out)})
}

// heldRow is a held message as kept.
type heldRow struct {
	opsHeldRow
	ChatID          uuid.UUID  `json:"-"`
	ReplyTo         *uuid.UUID `json:"-"`
	Forwards        int        `json:"-"`
	sealed          []byte
	fileSealed      []byte
	FileType        *string `json:"file_type"`
	FileSize        *int64  `json:"file_size"`
	boxKey, kmsKey  string
	DecisionNote    *string    `json:"decision_note"`
	ReleasedMessage *uuid.UUID `json:"released_message"`
}

func heldTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (heldRow, error) {
	var h heldRow
	err := tx.QueryRow(ctx, `SELECT id, sender_id, sender_name, recipient_id, recipient_name, chat_id, kind, reply_to, forwards, reasons, sealed, file_sealed,
		file_type, file_size, box_key, kms_key, state, created_at, decided_by, decided_at, decision_note, released_message FROM taskiem_ops_people_held_one($1)`, id).
		Scan(&h.ID, &h.SenderID, &h.SenderName, &h.RecipientID, &h.RecipientName, &h.ChatID, &h.Kind, &h.ReplyTo, &h.Forwards, &h.Reasons, &h.sealed,
			&h.fileSealed, &h.FileType, &h.FileSize, &h.boxKey, &h.kmsKey, &h.State, &h.CreatedAt, &h.DecidedBy, &h.DecidedAt, &h.DecisionNote,
			&h.ReleasedMessage)
	if errors.Is(err, pgx.ErrNoRows) {
		return h, errOpsNotFound
	}
	h.HasFile = h.fileSealed != nil
	return h, err
}

func (s *Server) openHeldMessage(ctx context.Context, h heldRow) (heldContent, peoplesafety.Box, error) {
	var c heldContent
	box, err := peoplesafety.OpenBox(ctx, s.Vault, peoplesafety.HeldPurpose(h.ID), h.ID, h.boxKey, h.kmsKey)
	if err != nil {
		return c, box, err
	}
	raw, err := box.Open(peoplesafety.MessagesRow, h.sealed)
	if err != nil {
		return c, box, err
	}
	return c, box, json.Unmarshal(raw, &c)
}

// opsPeopleHeldOne is GET /v1/ops/people/held/{id}?why=: a held message
// opened, with its verdict. Recorded first.
func (s *Server) opsPeopleHeldOne(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := opsID(w, r, "id", "held message")
	if !ok {
		return
	}
	why, ok := opsWhy(w, r)
	if !ok {
		return
	}
	o := operatorFrom(ctx)
	var h heldRow
	err := s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		var err error
		if h, err = heldTx(ctx, tx, id); err != nil {
			return err
		}
		return ops.AuditTx(ctx, tx, o.Actor(), "ops.people.held.open", id.String(), map[string]any{"why": why, "sender": h.SenderID})
	})
	if errors.Is(err, errOpsNotFound) {
		writeErr(w, http.StatusNotFound, "no such held message")
		return
	}
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	c, _, err := s.openHeldMessage(ctx, h)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"held": h, "message": c})
}

// opsPeopleHeldFile is GET /v1/ops/people/held/{id}/file?why=: a held
// message's photo or file. Recorded first.
func (s *Server) opsPeopleHeldFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := opsID(w, r, "id", "held message")
	if !ok {
		return
	}
	why, ok := opsWhy(w, r)
	if !ok {
		return
	}
	o := operatorFrom(ctx)
	var h heldRow
	err := s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		var err error
		if h, err = heldTx(ctx, tx, id); err != nil {
			return err
		}
		if h.fileSealed == nil {
			return errOpsNotFound
		}
		return ops.AuditTx(ctx, tx, o.Actor(), "ops.people.held.file", id.String(), map[string]any{"why": why})
	})
	if errors.Is(err, errOpsNotFound) {
		writeErr(w, http.StatusNotFound, "no such file")
		return
	}
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	box, err := peoplesafety.OpenBox(ctx, s.Vault, peoplesafety.HeldPurpose(h.ID), h.ID, h.boxKey, h.kmsKey)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data, err := box.Open(peoplesafety.FileRow(h.ID), h.fileSealed)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeOpsFile(w, *h.FileType, data)
}

// opsDecideHeld is POST /v1/ops/people/held/{id}/{release|confirm}
// {note?, step_up}: release a wrongly held message, which is delivered
// then as the sender meant it, or confirm the hold.
func (s *Server) opsDecideHeld(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := opsID(w, r, "id", "held message")
	if !ok {
		return
	}
	decision := chi.URLParam(r, "decision")
	state := map[string]string{"release": "released", "confirm": "confirmed"}[decision]
	if state == "" {
		writeErr(w, http.StatusNotFound, "the decision is release or confirm")
		return
	}
	var req struct {
		Note   string     `json:"note"`
		StepUp *opsStepUp `json:"step_up,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if utf8.RuneCountInString(req.Note) > 1000 {
		writeErr(w, http.StatusUnprocessableEntity, "a note is up to 1,000 characters")
		return
	}
	op := "ops.people.held." + decision
	if !s.checkOpsStepUp(w, r, req.StepUp, op, id.String()) {
		return
	}
	o := operatorFrom(ctx)
	var h heldRow
	err := s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		var err error
		h, err = heldTx(ctx, tx, id)
		return err
	})
	if errors.Is(err, errOpsNotFound) {
		writeErr(w, http.StatusNotFound, "no such held message")
		return
	}
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	if h.State != "held" {
		writeErr(w, http.StatusConflict, "this message was already decided")
		return
	}
	var released *uuid.UUID
	if decision == "release" {
		if h.SenderID == nil {
			writeErr(w, http.StatusConflict, "the sender deleted their account: the message cannot be delivered")
			return
		}
		c, box, err := s.openHeldMessage(ctx, h)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		// The held message's id as its client id: a release retried after
		// a failure below delivers it once.
		d := peopleDraft{kind: h.Kind, body: c.Body, card: c.Card, location: c.Location, replyTo: h.ReplyTo, forwards: h.Forwards, released: true,
			clientID: "held:" + h.ID.String()}
		if h.fileSealed != nil {
			data, err := box.Open(peoplesafety.FileRow(h.ID), h.fileSealed)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			d.file = &sentFile{Type: *h.FileType, Name: c.Name, Data: data}
		}
		m, _, err := s.peopleSend(ctx, *h.SenderID, h.ChatID.String(), d)
		if err != nil {
			var se customerSendErr
			if errors.As(err, &se) {
				writeErr(w, http.StatusConflict, "The message could not be delivered: "+se.msg)
				return
			}
			s.fail(w, r, err)
			return
		}
		released = &m.ID
	}
	err = s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		if _, err := tx.Exec(ctx, `SELECT taskiem_ops_people_held_decide($1, $2, $3, $4, $5)`, id, state, o.Actor(), req.Note, released); err != nil {
			return err
		}
		return ops.AuditTx(ctx, tx, o.Actor(), op, id.String(), map[string]any{"note": req.Note, "sender": h.SenderID, "message": released})
	})
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "state": state, "message_id": released})
}

func (s *Server) opsPeopleVerdicts(w http.ResponseWriter, r *http.Request) {
	type verdict struct {
		FromPerson uuid.UUID  `json:"from_person"`
		FromName   string     `json:"from_name"`
		ToPerson   *uuid.UUID `json:"to_person"`
		ToName     string     `json:"to_name"`
		Verdict    string     `json:"verdict"`
		Reasons    []string   `json:"reasons"`
		At         time.Time  `json:"at"`
	}
	var out []verdict
	err := s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		rows, err := tx.Query(r.Context(), `SELECT * FROM taskiem_ops_people_verdicts(200)`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[verdict])
		return err
	})
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"verdicts": nonNil(out)})
}

// --- a person's standing, actions and appeals ---

type opsAction struct {
	ID         uuid.UUID  `json:"id"`
	Action     string     `json:"action"`
	Reason     string     `json:"reason"`
	Operator   string     `json:"operator"`
	ReportID   *uuid.UUID `json:"report_id"`
	Until      *time.Time `json:"until"`
	LiftedAt   *time.Time `json:"lifted_at"`
	LiftedBy   *string    `json:"lifted_by"`
	LiftReason *string    `json:"lift_reason"`
	ClosedAt   *time.Time `json:"closed_at"`
	CloseNote  *string    `json:"close_note"`
	CreatedAt  time.Time  `json:"created_at"`
	AppealID   *uuid.UUID `json:"appeal_id"`
	AppealSt   *string    `json:"appeal_state"`
	AppealText *string    `json:"appeal_text"`
	AppealAt   *time.Time `json:"appeal_at"`
}

// opsPeoplePerson is GET /v1/ops/people/persons/{id}: a person's name,
// state and signals, and what moderators decided; never their chats.
func (s *Server) opsPeoplePerson(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := opsID(w, r, "id", "person")
	if !ok {
		return
	}
	var p struct {
		ID               uuid.UUID `json:"id"`
		Name             string    `json:"name"`
		Active           bool      `json:"active"`
		Deleted          bool      `json:"deleted"`
		Suspended        bool      `json:"suspended"`
		CreatedAt        time.Time `json:"created_at"`
		ReportsAgainst   int64     `json:"reports_against"`
		Held             int64     `json:"held"`
		VerdictsWeek     int64     `json:"verdicts_week"`
		DeviceAccounts   int       `json:"device_accounts"`
		RequestsWaiting  int64     `json:"requests_waiting"`
		RequestsAccepted int64     `json:"requests_accepted"`
		RequestsRefused  int64     `json:"requests_refused"`
	}
	var acts []opsAction
	err := s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		err := tx.QueryRow(ctx, `SELECT * FROM taskiem_ops_people_person($1)`, id).Scan(&p.ID, &p.Name, &p.Active, &p.Deleted, &p.Suspended, &p.CreatedAt,
			&p.ReportsAgainst, &p.Held, &p.VerdictsWeek, &p.DeviceAccounts, &p.RequestsWaiting, &p.RequestsAccepted, &p.RequestsRefused)
		if errors.Is(err, pgx.ErrNoRows) {
			return errOpsNotFound
		}
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT * FROM taskiem_ops_people_actions($1)`, id)
		if err != nil {
			return err
		}
		acts, err = pgx.CollectRows(rows, pgx.RowToStructByPos[opsAction])
		return err
	})
	if errors.Is(err, errOpsNotFound) {
		writeErr(w, http.StatusNotFound, "no such person")
		return
	}
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"person": p, "actions": nonNil(acts)})
}

// opsModeratePerson is POST /v1/ops/people/persons/{id}/actions {action:
// warn|limit|suspend|close, reason, days?, report_id?, step_up}: recorded
// with the reason, the operator and the time; the person is told in plain
// words (a push on You, and Your standing) and may appeal. A limit is
// for days (7 by default), a suspension for days or until lifted (0), a
// closure 30 days after, for an appeal.
func (s *Server) opsModeratePerson(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := opsID(w, r, "id", "person")
	if !ok {
		return
	}
	var req struct {
		Action   string     `json:"action"`
		Reason   string     `json:"reason"`
		Days     *int       `json:"days,omitempty"`
		ReportID *uuid.UUID `json:"report_id,omitempty"`
		StepUp   *opsStepUp `json:"step_up,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	switch req.Action {
	case peoplesafety.ActionWarn, peoplesafety.ActionLimit, peoplesafety.ActionSuspend, peoplesafety.ActionClose:
	default:
		writeErr(w, http.StatusUnprocessableEntity, "the action is warn, limit, suspend or close")
		return
	}
	if req.Reason == "" || utf8.RuneCountInString(req.Reason) > 1000 {
		writeErr(w, http.StatusUnprocessableEntity, "a reason is needed (up to 1,000 characters): the person is told it")
		return
	}
	if req.Days != nil && (*req.Days < 0 || *req.Days > 365) {
		writeErr(w, http.StatusUnprocessableEntity, "days is 0 to 365")
		return
	}
	now := time.Now()
	var until *time.Time
	switch req.Action {
	case peoplesafety.ActionLimit:
		d := 7
		if req.Days != nil && *req.Days > 0 {
			d = *req.Days
		}
		u := now.Add(time.Duration(d) * 24 * time.Hour)
		until = &u
	case peoplesafety.ActionSuspend:
		if req.Days != nil && *req.Days > 0 {
			u := now.Add(time.Duration(*req.Days) * 24 * time.Hour)
			until = &u
		}
	case peoplesafety.ActionClose:
		u := now.Add(peoplesafety.AppealWindow)
		until = &u
	}
	op := "ops.people." + req.Action
	if !s.checkOpsStepUp(w, r, req.StepUp, op, id.String()) {
		return
	}
	o := operatorFrom(ctx)
	var action uuid.UUID
	err := s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		if err := tx.QueryRow(ctx, `SELECT taskiem_ops_people_moderate($1, $2, $3, $4, $5, $6)`, id, req.Action, req.Reason, o.Actor(), req.ReportID, until).
			Scan(&action); err != nil {
			return err
		}
		return ops.AuditTx(ctx, tx, o.Actor(), op, id.String(), map[string]any{"reason": req.Reason, "until": until, "report": req.ReportID, "action": action})
	})
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	title := map[string]string{peoplesafety.ActionWarn: "A warning from Taskiem", peoplesafety.ActionLimit: "Your message requests are limited",
		peoplesafety.ActionSuspend: "Your account can't message people for now", peoplesafety.ActionClose: "Taskiem is closing your account"}[req.Action]
	s.tellStanding(ctx, id, title, req.Reason+" "+moderationWords(req.Action, until))
	writeJSON(w, http.StatusCreated, map[string]any{"id": action, "action": req.Action, "until": until})
}

// opsLiftAction is POST /v1/ops/people/actions/{id}/lift {reason?,
// step_up}: ends a limit, suspension or closure in force.
func (s *Server) opsLiftAction(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := opsID(w, r, "id", "action")
	if !ok {
		return
	}
	var req struct {
		Reason string     `json:"reason"`
		StepUp *opsStepUp `json:"step_up,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if utf8.RuneCountInString(req.Reason) > 1000 {
		writeErr(w, http.StatusUnprocessableEntity, "a reason is up to 1,000 characters")
		return
	}
	if !s.checkOpsStepUp(w, r, req.StepUp, "ops.people.lift", id.String()) {
		return
	}
	o := operatorFrom(ctx)
	var person uuid.UUID
	err := s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		if err := tx.QueryRow(ctx, `SELECT taskiem_ops_people_lift($1, $2, $3)`, id, o.Actor(), req.Reason).Scan(&person); err != nil {
			return err
		}
		return ops.AuditTx(ctx, tx, o.Actor(), "ops.people.lift", id.String(), map[string]any{"reason": req.Reason, "person": person})
	})
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	s.tellStanding(ctx, person, "Your account is back to normal", "Taskiem lifted a decision on your account. You can message people as before.")
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "person_id": person, "lifted": true})
}

func (s *Server) opsPeopleAppeals(w http.ResponseWriter, r *http.Request) {
	state := statusParam(r, "open", "open", "upheld", "overturned", "all")
	type appeal struct {
		ID           uuid.UUID  `json:"id"`
		ActionID     uuid.UUID  `json:"action_id"`
		PersonID     uuid.UUID  `json:"person_id"`
		Name         string     `json:"name"`
		Action       string     `json:"action"`
		ActionReason string     `json:"action_reason"`
		Operator     string     `json:"operator"`
		Until        *time.Time `json:"until"`
		Text         string     `json:"text"`
		State        string     `json:"state"`
		CreatedAt    time.Time  `json:"created_at"`
		DecidedBy    *string    `json:"decided_by"`
		DecidedAt    *time.Time `json:"decided_at"`
		DecisionNote *string    `json:"decision_note"`
	}
	var out []appeal
	err := s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		rows, err := tx.Query(r.Context(), `SELECT * FROM taskiem_ops_people_appeals($1)`, state)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[appeal])
		return err
	})
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"appeals": nonNil(out)})
}

// opsDecideAppeal is POST /v1/ops/people/appeals/{id}/{uphold|overturn}
// {note, step_up}: the person is told either way, with the note.
// Overturning lifts the decision.
func (s *Server) opsDecideAppeal(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := opsID(w, r, "id", "appeal")
	if !ok {
		return
	}
	decision := chi.URLParam(r, "decision")
	state := map[string]string{"uphold": "upheld", "overturn": "overturned"}[decision]
	if state == "" {
		writeErr(w, http.StatusNotFound, "the decision is uphold or overturn")
		return
	}
	var req struct {
		Note   string     `json:"note"`
		StepUp *opsStepUp `json:"step_up,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	req.Note = strings.TrimSpace(req.Note)
	if req.Note == "" || utf8.RuneCountInString(req.Note) > 1000 {
		writeErr(w, http.StatusUnprocessableEntity, "a note to the person is needed (up to 1,000 characters): they are told it")
		return
	}
	op := "ops.people.appeal." + decision
	if !s.checkOpsStepUp(w, r, req.StepUp, op, id.String()) {
		return
	}
	o := operatorFrom(ctx)
	var person uuid.UUID
	err := s.opsTx(r, func(tx pgx.Tx, _ func(uuid.UUID) error) error {
		if err := tx.QueryRow(ctx, `SELECT taskiem_ops_people_appeal_decide($1, $2, $3, $4)`, id, state, o.Actor(), req.Note).Scan(&person); err != nil {
			return err
		}
		return ops.AuditTx(ctx, tx, o.Actor(), op, id.String(), map[string]any{"note": req.Note, "person": person})
	})
	if err != nil {
		s.opsDBFail(w, r, err)
		return
	}
	if state == "overturned" {
		s.tellStanding(ctx, person, "Your appeal was accepted", req.Note+" The decision is lifted.")
	} else {
		s.tellStanding(ctx, person, "Your appeal was turned down", req.Note)
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "state": state, "person_id": person})
}

// opsStepUpPeople are the step-up operations of this file and lawful.go.
var opsStepUpPeople = []string{
	"ops.people.report.review", "ops.customer_report.review", "ops.people.held.release", "ops.people.held.confirm",
	"ops.people.warn", "ops.people.limit", "ops.people.suspend", "ops.people.close", "ops.people.lift",
	"ops.people.appeal.uphold", "ops.people.appeal.overturn",
	"ops.lawful.record", "ops.lawful.approve", "ops.lawful.export", "ops.lawful.refuse", "ops.lawful.told",
}

func init() {
	for _, op := range opsStepUpPeople {
		opsStepUpOps[op] = true
	}
}
