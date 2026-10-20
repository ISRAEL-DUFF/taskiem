package forms

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/internal/schemacheck"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/wd"
)

// FileStore keeps form files in the organisation's Xenibase storage
// (media.Service).
type FileStore interface {
	PutFile(ctx context.Context, tenant uuid.UUID, data []byte) (media.File, error)
	SignURL(ctx context.Context, tenant uuid.UUID, path string, ttl time.Duration) (string, time.Time, error)
}

// Service runs forms on the api side.
type Service struct {
	Pool     *pgxpool.Pool
	Vault    *secrets.Vault
	Keys     Keys
	Messages *sender.Queue
	Files    FileStore
	// Scanner checks files; nil refuses them (ErrNoScanner).
	Scanner Scanner
	// Start starts a form's run (runtime.Store.Start); Signal answers a
	// form step (runtime.Store.DeliverSignalOnce).
	Start  func(ctx context.Context, req runtime.StartRequest) (runtime.Started, error)
	Signal func(ctx context.Context, tenant uuid.UUID, env, event, correlation, dedupKey string, payload any) ([]uuid.UUID, bool, error)
	// FormHost is the form host's name (forms.<preview domain>); AppURL
	// the web app's, where members' forms open.
	FormHost string
	AppURL   string
	Logger   *slog.Logger
	Now      func() time.Time
}

// Errors a submission is refused with; the respondent was told before it
// reached the inbox, so these are rare (a form changed in between).
var (
	ErrRefused   = errors.New("forms: submission refused")
	ErrNoRequest = errors.New("forms: no such form request")
)

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// Upload is one file of a submission, opened.
type Upload struct {
	Meta FileMeta
	Data []byte
}

type inboxRow struct {
	id, tenant              uuid.UUID
	endpoint, request, link *uuid.UUID
	reference               string
	statusHash, payload     []byte
	created                 time.Time
}

// Drain takes submissions from the inbox and processes each; one that
// fails stays and is tried again once its claim lapses.
func (s *Service) Drain(ctx context.Context) error {
	rows, err := s.Pool.Query(ctx, `SELECT id, tenant_id, endpoint_id, request_id, link_id, reference, status_hash, payload, created_at FROM taskiem_form_inbox_claim(10)`)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (inboxRow, error) {
		var x inboxRow
		return x, r.Scan(&x.id, &x.tenant, &x.endpoint, &x.request, &x.link, &x.reference, &x.statusHash, &x.payload, &x.created)
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, x := range list {
		if err := s.processInbox(ctx, x); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", x.reference, err))
			continue
		}
		if _, err := s.Pool.Exec(ctx, `SELECT taskiem_form_inbox_done($1)`, x.id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) processInbox(ctx context.Context, x inboxRow) error {
	var sub Submission
	if err := s.Keys.Open(SubmissionAAD(x.reference), x.payload, &sub); err != nil {
		s.log().Error("forms: an inbox payload that does not open; dropped", "reference", x.reference)
		return nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT field, data FROM taskiem_form_uploads($1)`, x.id)
	if err != nil {
		return err
	}
	type up struct {
		field string
		data  []byte
	}
	raw, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (up, error) {
		var u up
		return u, r.Scan(&u.field, &u.data)
	})
	if err != nil {
		return err
	}
	var uploads []Upload
	count := map[string]int{}
	for i, meta := range sub.Files {
		if i >= len(raw) || raw[i].field != meta.Field {
			return s.refuse(ctx, x.tenant, x.endpoint, x.request, x.reference, x.statusHash, sub, "a file did not arrive")
		}
		data, err := s.Keys.OpenBytes(UploadAAD(x.reference, meta.Field, count[meta.Field]), raw[i].data)
		if err != nil {
			return s.refuse(ctx, x.tenant, x.endpoint, x.request, x.reference, x.statusHash, sub, "a file did not open")
		}
		count[meta.Field]++
		uploads = append(uploads, Upload{Meta: meta, Data: data})
	}
	return s.Process(ctx, Incoming{Tenant: x.tenant, Endpoint: x.endpoint, Request: x.request, Reference: x.reference, StatusHash: x.statusHash,
		Submission: sub, Uploads: uploads})
}

// Incoming is a submission to process: from the inbox, or from a member
// in the app (the API's, which needs no inbox).
type Incoming struct {
	Tenant            uuid.UUID
	Endpoint, Request *uuid.UUID
	Reference         string
	StatusHash        []byte
	Submission        Submission
	Uploads           []Upload
	// StartedBy is a member's user for runs they start; empty is the form.
	StartedBy string
}

type target struct {
	env      string
	workflow uuid.UUID
	version  int
	run      uuid.UUID
	step     string
	schema   map[string]any
	types    map[string]any
	def      *wd.Definition
	endpoint *uuid.UUID
	request  *uuid.UUID
}

// Process records a submission and starts its run, or answers its form
// step. A repeat (the same reference) does nothing more.
func (s *Service) Process(ctx context.Context, in Incoming) error {
	var done bool
	var tg target
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{in.Tenant}, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM form_submissions WHERE reference = $1 AND status <> 'received'`, in.Reference).Scan(&n); err != nil {
			return err
		}
		if done = n > 0; done {
			return nil
		}
		var err error
		tg, err = s.target(ctx, tx, in)
		return err
	})
	if err != nil || done {
		if errors.Is(err, ErrNoRequest) {
			return s.refuse(ctx, in.Tenant, in.Endpoint, in.Request, in.Reference, in.StatusHash, in.Submission, "the form is no longer taking answers")
		}
		return err
	}
	answers, files, why, err := s.files(ctx, in, tg)
	if err != nil {
		return err
	}
	if why == "" {
		why = s.check(tg, answers, files)
	}
	if why != "" {
		return s.refuse(ctx, in.Tenant, in.Endpoint, in.Request, in.Reference, in.StatusHash, in.Submission, why)
	}
	id := uuid.Must(uuid.NewV7())
	r := in.Submission.Respondent
	var signal map[string]any
	err = db.InTenantTx(ctx, s.Pool, []uuid.UUID{in.Tenant}, func(tx pgx.Tx) error {
		plainAnswers, err := roundTrip(answers)
		if err != nil {
			return err
		}
		sealedAnswers, err := pii.Seal(ctx, s.Vault, tx, in.Tenant, plainAnswers, pii.SchemaPaths(tg.schema, tg.types), pii.Taint{})
		if err != nil {
			return err
		}
		resp, err := s.sealRespondent(ctx, tx, in.Tenant, r)
		if err != nil {
			return err
		}
		a, _ := json.Marshal(sealedAnswers)
		rr, _ := json.Marshal(resp)
		ch := in.Submission.Channel
		if ch == "" {
			ch = "direct"
		}
		if _, err := tx.Exec(ctx, `INSERT INTO form_submissions (id, tenant_id, endpoint_id, request_id, reference, status_hash, answers, respondent, channel)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) ON CONFLICT (reference) DO NOTHING`,
			id, in.Tenant, tg.endpoint, tg.request, in.Reference, in.StatusHash, a, rr, ch); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT id FROM form_submissions WHERE reference = $1`, in.Reference).Scan(&id); err != nil {
			return err
		}
		for _, f := range files {
			sum, _ := hex.DecodeString(f.ref.SHA256)
			if _, err := tx.Exec(ctx, `INSERT INTO form_files (id, tenant_id, submission_id, field, name, type, size, sha256, storage_key)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) ON CONFLICT (id) DO NOTHING`,
				uuid.MustParse(f.ref.ID), in.Tenant, id, f.field, f.ref.Name, f.ref.Type, f.ref.Size, sum, f.path); err != nil {
				return err
			}
		}
		if tg.request != nil {
			// Answered now, in this transaction: the step's run may end as
			// soon as the signal arrives, which withdraws pending requests.
			if _, err := tx.Exec(ctx, `UPDATE form_requests SET status = 'answered', answered_at = now() WHERE id = $1 AND status = 'pending'`, *tg.request); err != nil {
				return err
			}
			payload := map[string]any{}
			for k, v := range sealedAnswers.(map[string]any) {
				payload[k] = v
			}
			payload["respondent"] = resp
			signal = payload
		}
		return audit(ctx, tx, in.Tenant, "form.submitted", in.Reference, map[string]any{"files": len(files), "channel": ch})
	})
	if err != nil {
		return err
	}
	if tg.request != nil {
		return s.answerStep(ctx, in, tg, id, signal)
	}
	return s.startRun(ctx, in, tg, id, answers)
}

// roundTrip makes v plain JSON values (file references become maps).
func roundTrip(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return expr.DecodeJSON(raw)
}

// target finds what a submission is for: the endpoint's deployed version
// (its inputs schema), or the waiting request (the step's schema).
func (s *Service) target(ctx context.Context, tx pgx.Tx, in Incoming) (target, error) {
	var t target
	if in.Endpoint != nil {
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT e.workflow_id, e.environment, e.version, v.definition FROM form_endpoints e
			JOIN workflow_versions v ON v.workflow_id = e.workflow_id AND v.version = e.version WHERE e.id = $1`, *in.Endpoint).Scan(&t.workflow, &t.env, &t.version, &raw); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return t, ErrNoRequest
			}
			return t, err
		}
		def, err := wd.Load(raw)
		if err != nil {
			return t, err
		}
		schema, types := def.InputsSchema()
		t.schema, _ = schema.(map[string]any)
		t.types, t.def, t.endpoint = types, def, in.Endpoint
		return t, nil
	}
	if in.Request == nil {
		return t, ErrNoRequest
	}
	var raw []byte
	var status string
	if err := tx.QueryRow(ctx, `SELECT run_id, step_id, environment, schema, status FROM form_requests WHERE id = $1`, *in.Request).
		Scan(&t.run, &t.step, &t.env, &raw, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return t, ErrNoRequest
		}
		return t, err
	}
	if status != "pending" {
		// Answered by this submission, whose signal did not get through:
		// it is sent again.
		var mine bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM form_submissions WHERE reference = $1 AND request_id = $2)`, in.Reference, *in.Request).Scan(&mine); err != nil {
			return t, err
		}
		if status != "answered" || !mine {
			return t, ErrNoRequest
		}
	}
	v, err := expr.DecodeJSON(raw)
	if err != nil {
		return t, err
	}
	t.schema, _ = v.(map[string]any)
	t.request = in.Request
	return t, nil
}

type storedFile struct {
	field string
	path  string
	ref   FileRef
}

// files scans and stores the uploads, and puts each file field's
// reference (one, or a list when the field takes several) in the answers.
func (s *Service) files(ctx context.Context, in Incoming, tg target) (map[string]any, []storedFile, string, error) {
	answers := map[string]any{}
	for k, v := range in.Submission.Answers {
		answers[k] = v
	}
	if len(in.Uploads) == 0 {
		return answers, nil, "", nil
	}
	props, _ := tg.schema["properties"].(map[string]any)
	if s.Scanner == nil {
		return nil, nil, "files cannot be taken: no malware scanner is configured", nil
	}
	if s.Files == nil {
		return nil, nil, "files cannot be taken: the organisation's storage is not set up", nil
	}
	if len(in.Uploads) > 10 {
		return nil, nil, "at most 10 files", nil
	}
	var out []storedFile
	for _, u := range in.Uploads {
		p, _ := props[u.Meta.Field].(map[string]any)
		if f, _ := p["format"].(string); f != "file" {
			return nil, nil, fmt.Sprintf("%q takes no file", u.Meta.Field), nil
		}
		if why := fileRule(p, u); why != "" {
			return nil, nil, why, nil
		}
		threat, err := s.Scanner.Scan(ctx, u.Data)
		if err != nil {
			return nil, nil, "", err // tried again
		}
		if threat != "" {
			s.log().Warn("forms: a file failed the malware scan", "reference", in.Reference, "field", u.Meta.Field, "threat", threat)
			return nil, nil, fmt.Sprintf("the file %q did not pass the malware check", u.Meta.Name), nil
		}
		f, err := s.Files.PutFile(ctx, in.Tenant, u.Data)
		if errors.Is(err, media.ErrFile) {
			return nil, nil, plain(err), nil
		}
		if err != nil {
			return nil, nil, "", err
		}
		sum := sha256.Sum256(u.Data)
		ref := FileRef{ID: uuid.Must(uuid.NewV7()).String(), Name: clipName(u.Meta.Name), Type: f.Type, Size: f.Size, SHA256: hex.EncodeToString(sum[:])}
		out = append(out, storedFile{field: u.Meta.Field, path: f.Path, ref: ref})
		answers[u.Meta.Field] = ref
	}
	return answers, out, "", nil
}

// fileRule checks a file against its field's accept and max_mb (forms 6.5).
func fileRule(p map[string]any, u Upload) string {
	h, _ := p["x-form"].(map[string]any)
	limit := 10.0
	if mb, ok := h["max_mb"].(float64); ok {
		limit = mb
	} else if mb, ok := h["max_mb"].(int64); ok {
		limit = float64(mb)
	}
	if float64(len(u.Data)) > min(limit, 25)*(1<<20) {
		return fmt.Sprintf("%q is larger than %.0f MB", u.Meta.Name, min(limit, 25))
	}
	if acc, ok := h["accept"].([]any); ok && len(acc) > 0 {
		ok := false
		for _, a := range acc {
			pat, _ := a.(string)
			if pat == u.Meta.Type || (strings.HasSuffix(pat, "/*") && strings.HasPrefix(u.Meta.Type, strings.TrimSuffix(pat, "*"))) {
				ok = true
			}
		}
		if !ok {
			return fmt.Sprintf("%q is not a type this question takes", u.Meta.Name)
		}
	}
	return ""
}

func clipName(n string) string {
	n = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, n))
	if len([]rune(n)) > 120 {
		n = string([]rune(n)[:120])
	}
	if n == "" {
		n = "file"
	}
	return n
}

// check validates the answers against the schema again: the form host
// checked them, but a form changed in between must not start a run with
// answers it does not take. File fields are checked holding their id.
func (s *Service) check(tg target, answers map[string]any, files []storedFile) string {
	probe := map[string]any{}
	for k, v := range answers {
		probe[k] = v
	}
	for _, f := range files {
		probe[f.field] = f.ref.ID
	}
	body, _ := json.Marshal(probe)
	var msgs []string
	if tg.def != nil {
		msgs = tg.def.ValidateInputs(body)
	} else {
		raw, _ := json.Marshal(tg.schema)
		msgs = schemacheck.New("https://schemas.taskiem.dev/forms/request.json", raw).Validate(body)
	}
	if len(msgs) > 0 {
		return "the answers do not fit the form: " + strings.Join(msgs, "; ")
	}
	return ""
}

func (s *Service) sealRespondent(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, r Respondent) (map[string]any, error) {
	out := map[string]any{"kind": r.Kind}
	if r.MemberID != "" {
		out["member_id"] = r.MemberID
	}
	for k, v := range map[string]string{"phone": r.Phone, "email": r.Email} {
		if v == "" {
			continue
		}
		env, err := s.Vault.SealTx(ctx, tx, tenant, k, v)
		if err != nil {
			return nil, err
		}
		out[k] = env
	}
	return out, nil
}

func (s *Service) startRun(ctx context.Context, in Incoming, tg target, id uuid.UUID, answers map[string]any) error {
	sub := in.Submission
	form := map[string]any{"submission_id": id.String(), "reference": in.Reference, "submitted_at": sub.Submitted.UTC().Format(time.RFC3339),
		"respondent": respondentMap(sub.Respondent), "channel": channelOf(sub), "consents": consentsOf(sub.Consents)}
	if sub.Site != "" {
		form["site"] = sub.Site
	}
	startedBy := in.StartedBy
	if startedBy == "" {
		startedBy = "form:" + in.Reference
	}
	raw, _ := json.Marshal(map[string]any{"type": "form", "body": answers, "form": form})
	var trig map[string]any
	_ = json.Unmarshal(raw, &trig)
	st, err := s.Start(ctx, runtime.StartRequest{TenantID: in.Tenant, WorkflowID: tg.workflow, Version: tg.version, Environment: tg.env,
		Trigger: trig, StartedBy: startedBy, TriggerID: "form/" + tg.endpoint.String(), DedupKey: id.String()})
	note := ""
	if le, ok := runtime.IsLimit(err); ok {
		note, err = "Not started: the plan's limit "+le.Limit+" was reached.", nil
	}
	if err != nil {
		return err
	}
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{in.Tenant}, func(tx pgx.Tx) error {
		if note != "" {
			_, err := tx.Exec(ctx, `UPDATE form_submissions SET note = $2, updated_at = now() WHERE id = $1`, id, note)
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE form_submissions SET run_id = $2, status = 'started', updated_at = now() WHERE id = $1`, id, st.Ref.ID)
		return err
	})
}

func (s *Service) answerStep(ctx context.Context, in Incoming, tg target, id uuid.UUID, payload map[string]any) error {
	if _, _, err := s.Signal(ctx, in.Tenant, tg.env, history.FormEvent, tg.request.String(), "form:"+in.Reference, payload); err != nil {
		return err
	}
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{in.Tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE form_submissions SET run_id = $2, status = 'answered', updated_at = now() WHERE id = $1`, id, tg.run)
		return err
	})
}

// refuse records a submission that cannot go ahead, so its status link
// says so, and drops its inbox row.
func (s *Service) refuse(ctx context.Context, tenant uuid.UUID, endpoint, request *uuid.UUID, reference string, statusHash []byte, sub Submission, why string) error {
	s.log().Warn("forms: submission refused", "reference", reference, "why", why)
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		ch := sub.Channel
		if ch == "" {
			ch = "direct"
		}
		_, err := tx.Exec(ctx, `INSERT INTO form_submissions (id, tenant_id, endpoint_id, request_id, reference, status_hash, answers, respondent, channel, status, note)
			VALUES ($1, $2, $3, $4, $5, $6, '{}', $7, $8, 'refused', $9) ON CONFLICT (reference) DO NOTHING`,
			uuid.Must(uuid.NewV7()), tenant, endpoint, request, reference, statusHash, map[string]any{"kind": sub.Respondent.Kind}, ch, why)
		return err
	})
}

func respondentMap(r Respondent) map[string]any {
	m := map[string]any{"kind": r.Kind}
	if r.Phone != "" {
		m["phone"] = r.Phone
	}
	if r.Email != "" {
		m["email"] = r.Email
	}
	if r.MemberID != "" {
		m["member_id"] = r.MemberID
	}
	return m
}

func channelOf(s Submission) string {
	if s.Channel == "" {
		return "direct"
	}
	return s.Channel
}

func consentsOf(cs []Consent) []any {
	out := []any{}
	for _, c := range cs {
		out = append(out, map[string]any{"field": c.Field, "text_hash": c.TextHash, "at": c.At.UTC().Format(time.RFC3339)})
	}
	return out
}

func audit(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, action, target string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	raw, _ := json.Marshal(detail)
	_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'forms', $2, $3, $4)`, tenant, action, target, raw)
	return err
}

func plain(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, ": "); i > 0 && strings.HasPrefix(msg, "media") {
		msg = msg[i+2:]
	}
	return msg
}
