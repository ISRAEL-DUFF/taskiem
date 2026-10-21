package inmail

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
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Opener opens what the mail role sealed (forms.Keys).
type Opener interface {
	OpenBytes(what string, ct []byte) ([]byte, error)
}

// Service runs inbound email on the api side: it drains the inbox the
// mail role fills and starts each message's run.
type Service struct {
	Pool *pgxpool.Pool
	Keys Opener
	// Files keeps attachments in the organisation's storage; Scanner
	// checks them first. Without either, attachments are listed as not
	// kept.
	Files   forms.FileStore
	Scanner forms.Scanner
	// Start starts a run (runtime.Store.Start).
	Start func(ctx context.Context, req runtime.StartRequest) (runtime.Started, error)
	// Domain is the mail domain; OwnSenders are Taskiem's own sending
	// addresses, whose mail never starts a run.
	Domain     string
	OwnSenders []string
	// AppURL is the web app's, for links in owners' notices.
	AppURL string
	Logger *slog.Logger
	Now    func() time.Time
}

// Limits on what a run gets of a message: the text and HTML are cut
// (truncated is set), attachments past the count are listed as skipped.
const (
	maxText        = 100_000
	maxHTML        = 200_000
	maxAttachments = 10
	maxFileBytes   = 25 << 20
)

// keptTypes are the attachments kept: the types forms take, which the
// media service re-encodes (images) or checks (PDFs).
var keptTypes = map[string]bool{"image/jpeg": true, "image/png": true, "application/pdf": true}

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

// Address is an endpoint's address.
func (s *Service) Address(publicID string) string {
	if s.Domain == "" {
		return ""
	}
	return publicID + "@" + s.Domain
}

type inboxRow struct {
	id, tenant, endpoint uuid.UUID
	idem, meta, raw      []byte
	created              time.Time
}

// Drain takes messages from the inbox and processes each; one that fails
// stays and is tried again once its claim lapses.
func (s *Service) Drain(ctx context.Context) error {
	rows, err := s.Pool.Query(ctx, `SELECT id, tenant_id, endpoint_id, idem, meta, raw, created_at FROM taskiem_email_inbox_claim(5)`)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (inboxRow, error) {
		var x inboxRow
		return x, r.Scan(&x.id, &x.tenant, &x.endpoint, &x.idem, &x.meta, &x.raw, &x.created)
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, x := range list {
		if err := s.process(ctx, x); err != nil {
			errs = append(errs, fmt.Errorf("email %s: %w", x.id, err))
			continue
		}
		if _, err := s.Pool.Exec(ctx, `SELECT taskiem_email_inbox_done($1)`, x.id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Run drains the inbox every interval until ctx ends.
func (s *Service) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := s.Drain(ctx); err != nil && ctx.Err() == nil {
			s.log().Error("email: draining the inbox", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type endpoint struct {
	workflow uuid.UUID
	env      string
	version  int
	publicID string
	def      *wd.Definition
}

// Config is an email trigger's config.
type Config struct {
	Senders     []string `json:"senders"`
	Attachments string   `json:"attachments"`
}

// TriggerConfig reads an email trigger's config.
func TriggerConfig(def *wd.Definition) Config {
	var c Config
	raw, _ := json.Marshal(def.Trigger.Config)
	_ = json.Unmarshal(raw, &c)
	return c
}

// record is what an email_messages row says.
type record struct {
	status      string
	note        string
	run         *uuid.UUID
	attachments int
}

func (s *Service) process(ctx context.Context, x inboxRow) error {
	metaRaw, err := s.Keys.OpenBytes(MetaAAD(x.id), x.meta)
	if err != nil {
		s.log().Error("email: an inbox row that does not open; dropped", "id", x.id)
		return nil //nolint:nilerr // dropped on purpose: retrying cannot open it
	}
	raw, err := s.Keys.OpenBytes(RawAAD(x.id), x.raw)
	if err != nil {
		s.log().Error("email: an inbox message that does not open; dropped", "id", x.id)
		return nil //nolint:nilerr // as above
	}
	var meta Meta
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		return nil //nolint:nilerr // as above
	}
	return s.handle(ctx, x, meta, raw)
}

// handle processes an opened message: it records it and starts its run,
// or says why not.
func (s *Service) handle(ctx context.Context, x inboxRow, meta Meta, raw []byte) error {
	var ep endpoint
	var done bool
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{x.tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM email_messages WHERE endpoint_id = $1 AND idem = $2)`, x.endpoint, x.idem).Scan(&done); err != nil || done {
			return err
		}
		var def []byte
		err := tx.QueryRow(ctx, `SELECT e.workflow_id, e.environment, e.version, e.public_id, v.definition FROM email_endpoints e
			JOIN workflow_versions v ON v.workflow_id = e.workflow_id AND v.version = e.version WHERE e.id = $1`, x.endpoint).
			Scan(&ep.workflow, &ep.env, &ep.version, &ep.publicID, &def)
		if err != nil {
			return err
		}
		ep.def, err = wd.Load(def)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // the workflow is gone
	}
	if err != nil || done {
		return err
	}
	msg, perr := Parse(raw)
	rec := record{status: "refused"}
	var files []keptFile
	var conf Confirmation
	switch {
	case perr != nil:
		rec.note = "The message could not be read."
	default:
		if c, ok := ForwardingConfirmation(msg, meta.Auth, meta.ReceivedAt); ok {
			conf, rec.status, rec.note = c, "confirmation", c.Provider+" asked to confirm forwarding to this address."
			break
		}
		cfg := TriggerConfig(ep.def)
		if why := Automatic(msg, meta.MailFrom, s.OwnSenders); why != "" {
			rec.note = "Not started: " + why + "."
		} else if !sample(meta) && !SenderAllowed(cfg.Senders, msg.From.Address, meta.Auth) {
			rec.note = "Not started: the sender is not one this workflow takes mail from"
			if len(cfg.Senders) > 0 && !meta.Auth.Aligned {
				rec.note += " (the sender could not be proven, so the list does not trust it)"
			}
			rec.note += "."
		} else {
			rec, files, err = s.startRun(ctx, x, meta, msg, ep, cfg)
			if err != nil {
				return err
			}
		}
	}
	size := len(raw)
	domain := ""
	if msg != nil {
		domain = Domain(msg.From.Address)
	}
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{x.tenant}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO email_messages (id, tenant_id, endpoint_id, workflow_id, environment, idem, sender_domain, size, attachments, status, note, run_id, received_at)
			VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8, $9, $10, NULLIF($11, ''), $12, $13) ON CONFLICT (endpoint_id, idem) DO NOTHING`,
			x.id, x.tenant, x.endpoint, ep.workflow, ep.env, x.idem, domain, size, rec.attachments, rec.status, rec.note, rec.run, meta.ReceivedAt); err != nil {
			return err
		}
		for _, f := range files {
			sum, _ := hex.DecodeString(f.ref.SHA256)
			if _, err := tx.Exec(ctx, `INSERT INTO email_files (id, tenant_id, message_id, name, type, size, sha256, storage_key)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (id) DO NOTHING`,
				uuid.MustParse(f.ref.ID), x.tenant, x.id, f.ref.Name, f.ref.Type, f.ref.Size, sum, f.path); err != nil {
				return err
			}
		}
		if rec.status != "confirmation" {
			return nil
		}
		c, _ := json.Marshal(conf)
		if _, err := tx.Exec(ctx, `UPDATE email_endpoints SET confirmation = $2, updated_at = now() WHERE id = $1`, x.endpoint, c); err != nil {
			return err
		}
		return s.tellConfirmation(ctx, tx, x.tenant, ep, conf)
	})
}

// tellConfirmation tells the owners, in chat, what their mail provider
// needs to finish setting up forwarding.
func (s *Service) tellConfirmation(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ep endpoint, c Confirmation) error {
	var name string
	if err := tx.QueryRow(ctx, `SELECT name FROM workflows WHERE id = $1`, ep.workflow).Scan(&name); err != nil {
		return err
	}
	body := c.Provider + " wants to confirm that mail can be forwarded to " + name + "'s address."
	if c.Code != "" {
		body += " The confirmation code is " + c.Code + ": enter it in " + c.Provider + "'s forwarding settings."
	} else {
		body += " Open the link in the workflow's \"When it runs\" tab in Taskiem to confirm."
	}
	_, err := alerts.TellOwners(ctx, tx, tenant, alerts.Alert{Kind: "email_forwarding_confirmation", Dedup: ep.publicID + ":" + c.ReceivedAt.UTC().Format(time.RFC3339),
		Title: "Confirm forwarding for " + name, Body: body, Link: link(s.AppURL, "/workflows/"+ep.workflow.String()+"?tab=settings"),
		Detail: map[string]any{"workflow": ep.workflow.String(), "provider": c.Provider}})
	return err
}

func link(base, path string) string {
	if base == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + path
}

// startRun keeps the message's attachments and starts the workflow with
// the message as its trigger.
func (s *Service) startRun(ctx context.Context, x inboxRow, meta Meta, msg *Message, ep endpoint, cfg Config) (record, []keptFile, error) {
	files, refs, skipped, err := s.attachments(ctx, x.tenant, msg, cfg)
	if err != nil {
		return record{}, nil, err
	}
	body := Body(msg, refs, skipped)
	trig := map[string]any{"type": "email", "body": body, "email": map[string]any{
		"address": meta.Address, "received_at": meta.ReceivedAt.UTC().Format(time.RFC3339),
		"auth": map[string]any{"spf": meta.Auth.SPF, "dkim": stringsAny(meta.Auth.DKIM), "dmarc": meta.Auth.DMARC, "aligned": meta.Auth.Aligned}}}
	// Plain JSON values, as runs take them.
	raw, _ := json.Marshal(trig)
	var plain map[string]any
	_ = json.Unmarshal(raw, &plain)
	st, err := s.Start(ctx, runtime.StartRequest{TenantID: x.tenant, WorkflowID: ep.workflow, Version: ep.version, Environment: ep.env,
		Trigger: plain, StartedBy: "email:" + Domain(msg.From.Address), TriggerID: "email/" + ep.workflow.String() + "/" + ep.env, DedupKey: hex.EncodeToString(x.idem)})
	if le, ok := runtime.IsLimit(err); ok {
		return record{status: "refused", note: "Not started: the plan's limit " + le.Limit + " was reached."}, nil, nil
	}
	if err != nil {
		return record{}, nil, err
	}
	return record{status: "started", run: &st.Ref.ID, attachments: len(files)}, files, nil
}

func stringsAny(s []string) []any {
	out := make([]any, 0, len(s))
	for _, v := range s {
		out = append(out, v)
	}
	return out
}

type keptFile struct {
	path string
	ref  forms.FileRef
}

// Skipped is an attachment not kept, and why.
type Skipped struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Size   int    `json:"size"`
	Reason string `json:"reason"`
}

// attachments scans and stores what is kept, and says why the rest is
// not. Images inside the body (a signature's logo) are neither.
func (s *Service) attachments(ctx context.Context, tenant uuid.UUID, msg *Message, cfg Config) ([]keptFile, []forms.FileRef, []Skipped, error) {
	var kept []keptFile
	refs := []forms.FileRef{}
	skipped := []Skipped{}
	for _, a := range msg.Attachments {
		if a.Inline && strings.HasPrefix(a.Type, "image/") {
			continue
		}
		name := clipName(a.Name)
		skip := func(why string) {
			skipped = append(skipped, Skipped{Name: name, Type: a.Type, Size: len(a.Data), Reason: why})
		}
		switch {
		case cfg.Attachments != "keep":
			skip("this workflow does not keep attachments")
			continue
		case s.Scanner == nil || s.Files == nil:
			skip("attachments cannot be kept on this Taskiem yet")
			continue
		case !keptTypes[a.Type]:
			skip("only JPEG and PNG pictures and PDFs are kept")
			continue
		case len(a.Data) > maxFileBytes:
			skip("larger than 25 MB")
			continue
		case len(kept) >= maxAttachments:
			skip("only the first 10 attachments are kept")
			continue
		}
		threat, err := s.Scanner.Scan(ctx, a.Data)
		if err != nil {
			return nil, nil, nil, err // tried again
		}
		if threat != "" {
			s.log().Warn("email: an attachment failed the malware scan", "tenant", tenant, "threat", threat)
			skip("did not pass the malware check")
			continue
		}
		f, err := s.Files.PutFile(ctx, tenant, a.Data)
		if errors.Is(err, media.ErrFile) {
			skip("could not be read as a picture or PDF")
			continue
		}
		if err != nil {
			return nil, nil, nil, err
		}
		sum := sha256.Sum256(a.Data)
		ref := forms.FileRef{ID: uuid.Must(uuid.NewV7()).String(), Name: name, Type: f.Type, Size: f.Size, SHA256: hex.EncodeToString(sum[:])}
		kept = append(kept, keptFile{path: f.Path, ref: ref})
		refs = append(refs, ref)
	}
	return kept, refs, skipped, nil
}

func clipName(n string) string {
	n = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, n))
	if utf8.RuneCountInString(n) > 120 {
		n = string([]rune(n)[:120])
	}
	if n == "" {
		n = "attachment"
	}
	return n
}

// Body is trigger.body for a message (decision 0038).
func Body(m *Message, files []forms.FileRef, skipped []Skipped) map[string]any {
	addrs := func(list []Address) []any {
		out := []any{}
		for _, a := range list {
			out = append(out, addr(a))
		}
		return out
	}
	text, textCut := clip(m.Text, maxText)
	html, htmlCut := clip(m.HTML, maxHTML)
	latest, _ := clip(Latest(m.Text), maxText)
	body := map[string]any{
		"from": addr(m.From), "to": addrs(m.To), "cc": addrs(m.Cc), "reply_to": addrs(m.ReplyTo),
		"subject": m.Subject, "message_id": m.MessageID,
		"text": text, "latest": latest, "html": html,
		"attachments": files, "skipped": skipped,
	}
	if !m.Date.IsZero() {
		body["date"] = m.Date.UTC().Format(time.RFC3339)
	}
	if by := ForwardedBy(m); by != "" {
		body["forwarded_by"] = by
	}
	if textCut || htmlCut {
		body["truncated"] = true
	}
	return body
}

func addr(a Address) map[string]any {
	out := map[string]any{"address": a.Address}
	if a.Name != "" {
		out["name"] = a.Name
	}
	return out
}

// clip cuts s to at most n bytes on a character boundary.
func clip(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s, true
}

// FileURL is a short-lived download link for one of the organisation's
// email attachments (email@1 file_url): the content never goes into the
// run.
func (s *Service) FileURL(ctx context.Context, tenant uuid.UUID, file string, ttl time.Duration) (map[string]any, error) {
	id, err := uuid.Parse(file)
	if err != nil {
		return nil, fmt.Errorf("%w: no such file", forms.ErrNoRequest)
	}
	var path, name, ctype string
	var size int64
	if err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT storage_key, name, type, size FROM email_files WHERE id = $1`, id).Scan(&path, &name, &ctype, &size)
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: no such file", forms.ErrNoRequest)
		}
		return nil, err
	}
	if s.Files == nil {
		return nil, errors.New("email: the organisation's storage is not set up")
	}
	if ttl <= 0 || ttl > time.Hour {
		ttl = 15 * time.Minute
	}
	url, exp, err := s.Files.SignURL(ctx, tenant, path, ttl)
	if err != nil {
		return nil, err
	}
	return map[string]any{"url": url, "expires_at": exp.UTC().Format(time.RFC3339), "name": name, "type": ctype, "size": size}, nil
}

// sample is a sample message (Sample): no mail server sent it.
func sample(m Meta) bool { return m.MailFrom == SampleSender && m.RemoteIP == "" }

// SampleSender is who a sample message comes from.
const SampleSender = "sample@taskiem.app"

// Sample runs an endpoint's workflow with a sample message, as if one had
// arrived, so an owner sees what a run gets before setting up forwarding.
// It returns the run started, if one was.
func (s *Service) Sample(ctx context.Context, tenant, endpointID uuid.UUID, workflowName string) (*uuid.UUID, string, error) {
	var publicID string
	if err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT public_id FROM email_endpoints WHERE id = $1`, endpointID).Scan(&publicID)
	}); err != nil {
		return nil, "", err
	}
	now := s.now().UTC()
	id := uuid.Must(uuid.NewV7())
	raw := []byte("From: Taskiem <" + SampleSender + ">\r\nTo: " + s.Address(publicID) + "\r\nSubject: A sample email for " +
		strings.NewReplacer("\r", " ", "\n", " ").Replace(workflowName) + "\r\nDate: " + now.Format(time.RFC1123Z) +
		"\r\nMessage-ID: <sample-" + id.String() + "@taskiem.app>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" +
		"This is a sample email, sent from Taskiem to show what your workflow gets when an email arrives.\r\n")
	idem := sha256.Sum256([]byte("sample " + id.String()))
	meta := Meta{Reference: id.String(), Address: s.Address(publicID), MailFrom: SampleSender, ReceivedAt: now,
		Auth: Auth{SPF: "pass", DKIM: []string{"taskiem.app"}, Aligned: true}}
	x := inboxRow{id: id, tenant: tenant, endpoint: endpointID, idem: idem[:], raw: raw, created: now}
	if err := s.handle(ctx, x, meta, raw); err != nil {
		return nil, "", err
	}
	var run *uuid.UUID
	var note *string
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT run_id, note FROM email_messages WHERE id = $1`, id).Scan(&run, &note)
	})
	if note != nil {
		return run, *note, err
	}
	return run, "", err
}
