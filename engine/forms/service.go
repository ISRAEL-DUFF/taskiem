package forms

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
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
	// Delete removes stored files (of submissions retention or erasure
	// deleted, DrainDeletions).
	Delete(ctx context.Context, tenant uuid.UUID, bucket string, paths []string) error
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
	// Retention is how long a run of def would be kept (runtime.Store.Retention):
	// a submission that starts no run, refused or held at a plan limit, is
	// kept as long, then deleted with its files (ExpireRunless). nil keeps
	// such submissions runtime.DefaultRetention.
	Retention func(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, def *wd.Definition) (time.Duration, error)
	// FormHost is the form host's name (forms.<preview domain>); AppURL
	// the web app's, where members' forms open.
	FormHost string
	AppURL   string
	// Ask puts a form step to people in their chat apps once its request
	// is recorded as sent (device fields §2); nil asks in the app alone.
	Ask func(ctx context.Context, a ChatAsk)
	// Forms left half-filled (forms-that-sell §6): ShopDomain is where
	// storefronts are served (<slug>.<domain>), for the restore link;
	// FollowupLimit the plan's form_followups a month (0: none; nil:
	// none); FollowupChat reaches a typed phone on WhatsApp by template
	// (L4; nil: a text).
	ShopDomain    string
	FollowupLimit func(ctx context.Context, tenant uuid.UUID) (int64, error)
	FollowupChat  FollowupChat
	// afterCaps, in tests, runs once a follow-up's caps are read.
	afterCaps func()
	Logger    *slog.Logger
	Now       func() time.Time
}

// Errors a submission is refused with; the respondent was told before it
// reached the inbox, so these are rare (a form changed in between).
var (
	ErrRefused   = errors.New("forms: submission refused")
	ErrNoRequest = errors.New("forms: no such form request")
)

// NoLongerTaking is the note of a submission refused because its form,
// or its form step, takes no more answers: answered by another meanwhile,
// withdrawn or expired.
const NoLongerTaking = "the form is no longer taking answers"

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

// DrainDeletions deletes the submissions that started no run once their
// retain_until passes (00394), then removes from storage the files of
// submissions that retention or erasure deleted (taskiem_purge_run and the
// erasure trigger queue them, 00380) and of refused ones. A file whose removal fails stays queued and is
// tried again once its lease lapses.
func (s *Service) DrainDeletions(ctx context.Context) error {
	// Submissions that started no run, past their own retention, go first
	// (their files join the queue).
	if _, err := s.Pool.Exec(ctx, `SELECT taskiem_form_submissions_expire(500)`); err != nil {
		return err
	}
	if s.Files == nil {
		return nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT tenant_id, storage_key FROM taskiem_form_file_deletions_claim(100)`)
	if err != nil {
		return err
	}
	type queued struct {
		tenant uuid.UUID
		key    string
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (queued, error) {
		var q queued
		return q, r.Scan(&q.tenant, &q.key)
	})
	if err != nil {
		return err
	}
	byTenant := map[uuid.UUID][]string{}
	var order []uuid.UUID
	for _, q := range list {
		if _, ok := byTenant[q.tenant]; !ok {
			order = append(order, q.tenant)
		}
		byTenant[q.tenant] = append(byTenant[q.tenant], q.key)
	}
	var errs []error
	for _, t := range order {
		keys := byTenant[t]
		if err := s.Files.Delete(ctx, t, media.PrivateBucket, keys); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", t, err))
			continue
		}
		for _, k := range keys {
			if _, err := s.Pool.Exec(ctx, `SELECT taskiem_form_file_deletions_done($1, $2)`, t, k); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (s *Service) processInbox(ctx context.Context, x inboxRow) error {
	var sub Submission
	if err := s.Keys.Open(SubmissionAAD(x.reference), x.payload, &sub); err != nil {
		s.log().Error("forms: an inbox payload that does not open; dropped", "reference", x.reference)
		return nil //nolint:nilerr // dropped on purpose: retrying cannot open it
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
			return s.refuse(ctx, x.tenant, x.endpoint, x.request, x.reference, x.statusHash, sub, nil, nil, "a file did not arrive")
		}
		data, err := s.Keys.OpenBytes(UploadAAD(x.reference, meta.Field, count[meta.Field]), raw[i].data)
		if err != nil {
			return s.refuse(ctx, x.tenant, x.endpoint, x.request, x.reference, x.statusHash, sub, nil, nil, "a file did not open")
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
	// recipient is a form step's request's own `to`, as stored (sealed
	// where personal): who a one-time link was sent to.
	recipient map[string]any
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
			return s.refuse(ctx, in.Tenant, in.Endpoint, in.Request, in.Reference, in.StatusHash, in.Submission, nil, nil, NoLongerTaking)
		}
		return err
	}
	answers, files, why, err := s.files(ctx, in, tg)
	if err != nil {
		// Files stored before the error are stored again on the retry.
		return errors.Join(err, s.discard(ctx, in.Tenant, files))
	}
	received := in.Submission.Submitted
	if received.IsZero() {
		received = s.now()
	}
	device := deviceMeta(in.Submission, received)
	if why == "" {
		deviceAt, _ := time.Parse(time.RFC3339, device["at"].(string))
		why = deviceAnswers(tg.schema, answers, in.Submission, deviceAt)
	}
	if why == "" {
		why = s.check(tg, answers, files)
	}
	if why != "" {
		return s.refuse(ctx, in.Tenant, in.Endpoint, in.Request, in.Reference, in.StatusHash, in.Submission, tg.def, files, why)
	}
	id := uuid.Must(uuid.NewV7())
	r := in.Submission.Respondent
	var signal map[string]any
	err = db.InTenantTx(ctx, s.Pool, []uuid.UUID{in.Tenant}, func(tx pgx.Tx) error {
		plainAnswers, err := roundTrip(answers)
		if err != nil {
			return err
		}
		// Fields the schema marks personal, and anything else that looks
		// personal (an email, a phone number, a BVN), as runs seal them.
		// A location is sealed under the person who answered it, so
		// erasing them erases where they were, here and in every copy the
		// run makes of it (device fields §5).
		owner := RespondentOwner(r)
		if r.Kind == "request" {
			// The one-time page says nothing of who answered: the person is
			// the one the link was sent to, from the request itself, never
			// from the page.
			if owner, err = s.recipientOwner(ctx, tx, in.Tenant, tg.recipient); err != nil {
				return err
			}
		}
		paths := pii.LocationsOwnedBy(pii.SchemaPaths(tg.schema, tg.types), owner)
		sealedAnswers, err := pii.Seal(ctx, s.Vault, tx, in.Tenant, plainAnswers, paths, pii.Taint{})
		if err != nil {
			return err
		}
		if sealedAnswers, err = pii.SealDetected(ctx, s.Vault, tx, in.Tenant, sealedAnswers, pii.Taint{}); err != nil {
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
		if tg.request != nil {
			// Answered now, in this transaction: the step's run may end as
			// soon as the signal arrives, which withdraws pending requests.
			// Taken once: of two answers arriving together, the second
			// waits on the row and finds it answered; only this same
			// submission, whose signal did not get through, goes on.
			tag, err := tx.Exec(ctx, `UPDATE form_requests SET status = 'answered', answered_at = now() WHERE id = $1 AND status = 'pending'`, *tg.request)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				var mine bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM form_submissions WHERE reference = $1 AND request_id = $2 AND status <> 'refused')`,
					in.Reference, *tg.request).Scan(&mine); err != nil {
					return err
				}
				if !mine {
					return ErrNoRequest
				}
			}
			// What a chat held towards it, anyone's, goes with the answer.
			if _, err := tx.Exec(ctx, `DELETE FROM form_chat_answers WHERE request_id = $1`, *tg.request); err != nil {
				return err
			}
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
			payload := map[string]any{}
			for k, v := range sealedAnswers.(map[string]any) {
				payload[k] = v
			}
			payload["respondent"] = resp
			payload["device"] = device
			signal = payload
		}
		return audit(ctx, tx, in.Tenant, "form.submitted", in.Reference, map[string]any{"files": len(files), "channel": ch, "device": deviceAudit(tg.schema, answers)})
	})
	if errors.Is(err, ErrNoRequest) {
		// Answered by another submission meanwhile: refused, its files
		// discarded.
		return s.refuse(ctx, in.Tenant, in.Endpoint, in.Request, in.Reference, in.StatusHash, in.Submission, tg.def, files, NoLongerTaking)
	}
	if err != nil {
		// Not recorded: the retry stores its files again.
		return errors.Join(err, s.discard(ctx, in.Tenant, files))
	}
	if tg.request != nil {
		return s.answerStep(ctx, in, tg, id, signal)
	}
	return s.startRun(ctx, in, tg, id, answers, device)
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
	var to []byte
	if err := tx.QueryRow(ctx, `SELECT run_id, step_id, environment, schema, status, recipient FROM form_requests WHERE id = $1`, *in.Request).
		Scan(&t.run, &t.step, &t.env, &raw, &status, &to); err != nil {
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
	if rv, err := expr.DecodeJSON(to); err == nil {
		t.recipient, _ = rv.(map[string]any)
	}
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
		kind := wd.DeviceKind(p)
		if f, _ := p["format"].(string); f != "file" && kind != wd.DevicePhoto && kind != wd.DeviceSignature {
			return nil, out, fmt.Sprintf("%q takes no file", u.Meta.Field), nil
		}
		switch kind {
		case wd.DevicePhoto:
			p = map[string]any{"x-form": map[string]any{"accept": []any{"image/jpeg", "image/png"}}}
		case wd.DeviceSignature:
			// A pad's drawing: a PNG by its content, not only its name,
			// and small (device fields §5, D2).
			if len(u.Data) > MaxSignatureBytes {
				return nil, out, fmt.Sprintf("the signature %q is larger than 1 MB", clipName(u.Meta.Name)), nil
			}
			if http.DetectContentType(u.Data) != "image/png" {
				return nil, out, fmt.Sprintf("a signature is a PNG drawing; %q is not one", clipName(u.Meta.Name)), nil
			}
			p = map[string]any{"x-form": map[string]any{"accept": []any{"image/png"}}}
		}
		if why := fileRule(p, u); why != "" {
			return nil, out, why, nil
		}
		threat, err := s.Scanner.Scan(ctx, u.Data)
		if err != nil {
			return nil, out, "", err // tried again
		}
		if threat != "" {
			s.log().Warn("forms: a file failed the malware scan", "reference", in.Reference, "field", u.Meta.Field, "threat", threat)
			return nil, out, fmt.Sprintf("the file %q did not pass the malware check", u.Meta.Name), nil
		}
		f, err := s.Files.PutFile(ctx, in.Tenant, u.Data)
		if errors.Is(err, media.ErrFile) {
			return nil, out, plain(err), nil
		}
		if err != nil {
			return nil, out, "", err
		}
		sum := sha256.Sum256(u.Data)
		ref := FileRef{ID: uuid.Must(uuid.NewV7()).String(), Name: clipName(u.Meta.Name), Type: f.Type, Size: f.Size, SHA256: hex.EncodeToString(sum[:])}
		out = append(out, storedFile{field: u.Meta.Field, path: f.Path, ref: ref})
		answers[u.Meta.Field] = ref
		if kind == wd.DeviceSignature {
			// The signer's name, given beside the pad in the answers.
			if m, ok := in.Submission.Answers[u.Meta.Field].(map[string]any); ok {
				if who, ok := m["signed_by"].(string); ok {
					answers[u.Meta.Field] = signedFile{FileRef: ref, SignedBy: who}
				}
			}
		}
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

// formEngine evaluates pages' show_if over the answers, as the form
// host does.
var formEngine = expr.MustNewWithRoots("answers")

// withoutHidden drops the fields of pages the answers hide (a show_if
// false, or one that cannot be evaluated, as on the form host) from the
// answers and from the schema's required list.
func withoutHidden(tg target, probe, answers map[string]any) map[string]any {
	f, err := tg.def.FormTriggerConfig()
	if err != nil {
		return tg.schema
	}
	hidden := map[string]bool{}
	for _, pg := range wd.FormPages(f.Pages, tg.schema) {
		c := wd.FormCondition(pg.ShowIf)
		if c == "" {
			continue
		}
		if ok, err := formEngine.EvalBool(c, map[string]any{"answers": answers}); err == nil && ok {
			continue
		}
		for _, name := range pg.Fields {
			hidden[name] = true
		}
	}
	if len(hidden) == 0 {
		return tg.schema
	}
	out := map[string]any{}
	for k, v := range tg.schema {
		out[k] = v
	}
	if req, ok := tg.schema["required"].([]any); ok {
		var keep []any
		for _, r := range req {
			if name, _ := r.(string); !hidden[name] {
				keep = append(keep, r)
			}
		}
		out["required"] = keep
		if keep == nil {
			delete(out, "required")
		}
	}
	for name := range hidden {
		delete(probe, name)
		delete(answers, name)
	}
	return out
}

// check validates the answers against the schema again: the form host
// checked them, but a form changed in between must not start a run with
// answers it does not take. File fields are checked holding their id.
func (s *Service) check(tg target, answers map[string]any, files []storedFile) string {
	probe := map[string]any{}
	for k, v := range answers {
		probe[k] = v
	}
	props, _ := tg.schema["properties"].(map[string]any)
	for _, f := range files {
		if wd.DeviceKind(props[f.field]) == "" {
			probe[f.field] = f.ref.ID
		}
	}
	var msgs []string
	if tg.def != nil {
		schema := withoutHidden(tg, probe, answers)
		root := map[string]any{}
		for k, v := range schema {
			root[k] = v
		}
		if len(tg.types) > 0 {
			root["types"] = tg.types
		}
		raw, _ := json.Marshal(root)
		body, _ := json.Marshal(probe)
		msgs = schemacheck.New("https://schemas.taskiem.dev/forms/"+tg.def.ID+".json", raw).Validate(body)
	} else {
		body, _ := json.Marshal(probe)
		// Device fields as the answers the server made of them.
		raw, _ := json.Marshal(wd.AnswerSchema(tg.schema))
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
	if r.Business != "" {
		out["business"] = r.Business
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

func (s *Service) startRun(ctx context.Context, in Incoming, tg target, id uuid.UUID, answers, device map[string]any) error {
	sub := in.Submission
	form := map[string]any{"submission_id": id.String(), "reference": in.Reference, "submitted_at": sub.Submitted.UTC().Format(time.RFC3339),
		"respondent": respondentMap(sub.Respondent), "channel": channelOf(sub), "consents": consentsOf(sub.Consents), "device": device}
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
			// No run will delete it: it is kept as long as its run would be.
			keep, err := s.retention(ctx, tx, in.Tenant, tg.def)
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE form_submissions SET note = $2, retain_until = now() + $3::interval, updated_at = now() WHERE id = $1`, id, note, keep)
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE form_submissions SET run_id = $2, status = 'started', retain_until = NULL, updated_at = now() WHERE id = $1`, id, st.Ref.ID)
		return err
	})
}

func (s *Service) answerStep(ctx context.Context, in Incoming, tg target, id uuid.UUID, payload map[string]any) error {
	if _, _, err := s.Signal(ctx, in.Tenant, tg.env, history.FormEvent, tg.request.String(), "form:"+in.Reference, payload); err != nil {
		return err
	}
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{in.Tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE form_submissions SET run_id = $2, status = 'answered', retain_until = NULL, updated_at = now() WHERE id = $1`, id, tg.run)
		return err
	})
}

// refuse records a submission that cannot go ahead, so its status link
// says so, and drops its inbox row. No run will delete the record, so it
// is kept as long as a run of def would be (nil: the tenant's default);
// files already stored for it are discarded.
func (s *Service) refuse(ctx context.Context, tenant uuid.UUID, endpoint, request *uuid.UUID, reference string, statusHash []byte, sub Submission, def *wd.Definition, stored []storedFile, why string) error {
	s.log().Warn("forms: submission refused", "reference", reference, "why", why)
	if err := s.discard(ctx, tenant, stored); err != nil {
		return err
	}
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		ch := sub.Channel
		if ch == "" {
			ch = "direct"
		}
		keep, err := s.retention(ctx, tx, tenant, def)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO form_submissions (id, tenant_id, endpoint_id, request_id, reference, status_hash, answers, respondent, channel, status, note, retain_until)
			VALUES ($1, $2, $3, $4, $5, $6, '{}', $7, $8, 'refused', $9, now() + $10::interval) ON CONFLICT (reference) DO NOTHING`,
			uuid.Must(uuid.NewV7()), tenant, endpoint, request, reference, statusHash, map[string]any{"kind": sub.Respondent.Kind}, ch, why, keep)
		return err
	})
}

// retention is how long a submission that starts no run is kept, as an
// interval for Postgres.
func (s *Service) retention(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, def *wd.Definition) (string, error) {
	d := runtime.DefaultRetention
	if s.Retention != nil {
		var err error
		if d, err = s.Retention(ctx, tx, tenant, def); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%d seconds", int64(d.Seconds())), nil
}

// discard queues files stored for a submission that was not recorded with
// them (refused, or failed and tried again), for DrainDeletions to remove.
func (s *Service) discard(ctx context.Context, tenant uuid.UUID, stored []storedFile) error {
	if len(stored) == 0 {
		return nil
	}
	keys := make([]string, len(stored))
	for i, f := range stored {
		keys[i] = f.path
	}
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_form_files_discard($1, $2)`, tenant, keys)
		return err
	})
}

// RespondentOwner is the person who answered, as what their answers are
// sealed under: their phone, else their email, else the member (or a
// linked business's person) by their user id. Nil for an anonymous
// answer, whose location is sealed as its own.
func RespondentOwner(r Respondent) *pii.Owner {
	switch {
	case r.Phone != "":
		return &pii.Owner{Category: "phone", Value: r.Phone}
	case r.Email != "":
		return &pii.Owner{Category: "email", Value: r.Email}
	case r.MemberID != "":
		return &pii.Owner{Category: "member", Value: r.MemberID}
	}
	return nil
}

// recipientOwner is who a form step's one-time link went to, as what a
// location answered through it is sealed under: the request's own `to`
// (its phone, else its email), opened. Nil when it names neither.
func (s *Service) recipientOwner(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, to map[string]any) (*pii.Owner, error) {
	if to == nil {
		return nil, nil
	}
	m, err := s.open(ctx, tx, tenant, to)
	if err != nil {
		return nil, err
	}
	if p, _ := m["phone"].(string); p != "" {
		return &pii.Owner{Category: "phone", Value: p}, nil
	}
	if e, _ := m["email"].(string); e != "" {
		return &pii.Owner{Category: "email", Value: e}, nil
	}
	return nil, nil
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
	if r.Business != "" {
		m["business"] = r.Business
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
