package inmail

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/sender"
)

// Vault seals personal data and names its pseudonyms (secrets.Vault).
type Vault interface {
	pii.Cipher
	SubjectForTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, category string, value any) (string, error)
}

// errHeld leaves a message in the inbox: its ticket's run is still going
// but not waiting for a reply yet.
var errHeld = errors.New("inmail: held for the ticket's run")

// HoldFor is how long a reply waits in the inbox for its ticket's run to
// wait for it; after that it starts a run of its own.
const HoldFor = 7 * 24 * time.Hour

// threadMatch is the thread a message was found to belong to, and how.
type threadMatch struct {
	thread string
	by     string // tag, headers or subject
}

// replyPaths are a reply's personal parts, as an email trigger's
// (runtime's emailPaths) under body: who sent it and to whom, and what it
// says, sealed under its sender.
var replyPaths = func() []pii.Path {
	var out []pii.Path
	add := func(cat string, segs ...string) {
		out = append(out, pii.Path{Segments: append([]string{"body"}, segs...), Category: cat})
	}
	add("email", "from", "address")
	add("name", "from", "name")
	for _, list := range []string{"to", "cc", "reply_to"} {
		add("email", list, "*", "address")
		add("name", list, "*", "name")
	}
	add("email", "forwarded_by")
	sender := []string{"body", "from", "address"}
	for _, segs := range [][]string{{"subject"}, {"text"}, {"latest"}, {"html"}, {"attachments", "*", "name"}, {"skipped", "*", "name"}} {
		out = append(out, pii.Path{Segments: append([]string{"body"}, segs...), Category: "other", Owner: sender, OwnerCategory: "email"})
	}
	return out
}()

// findThread finds the thread a message to a threaded workflow belongs
// to: its verified reply tag; else a Message-ID Taskiem sent from this
// address in In-Reply-To or References (newest first); else a [#T-1042]
// subject tag from a proven sender Taskiem emailed in that thread.
func (s *Service) findThread(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ep endpoint, meta Meta, msg *Message) (threadMatch, error) {
	if meta.Tag != "" {
		if t, ok := VerifyTag(ep.replyKey, ep.id, meta.Tag); ok {
			return threadMatch{thread: t, by: "tag"}, nil
		}
	}
	var ids []string
	if msg.InReplyTo != "" {
		ids = append(ids, msg.InReplyTo)
	}
	for i := len(msg.References) - 1; i >= 0; i-- {
		ids = append(ids, msg.References[i])
	}
	if len(ids) > 0 {
		var t string
		err := tx.QueryRow(ctx, `SELECT thread FROM email_thread_messages WHERE endpoint_id = $1 AND message_id = ANY ($2)
			ORDER BY array_position($2, message_id) LIMIT 1`, ep.id, ids).Scan(&t)
		if err == nil {
			return threadMatch{thread: t, by: "headers"}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return threadMatch{}, err
		}
	}
	// The subject is free text anyone can write: it counts only from a
	// sender the message proves, whom Taskiem emailed in that thread.
	t := SubjectThread(msg.Subject)
	if t == "" || !meta.Auth.Aligned || msg.From.Address == "" || s.PII == nil {
		return threadMatch{}, nil
	}
	subject, err := s.PII.SubjectForTx(ctx, tx, tenant, "email", msg.From.Address)
	if errors.Is(err, pii.ErrErased) {
		return threadMatch{}, nil
	}
	if err != nil {
		return threadMatch{}, err
	}
	var in bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM email_thread_messages WHERE endpoint_id = $1 AND thread = $2 AND recipient_subject = $3)`,
		ep.id, t, subject).Scan(&in); err != nil || !in {
		return threadMatch{}, err
	}
	return threadMatch{thread: t, by: "subject"}, nil
}

// threaded handles a message to a threaded workflow: a reply in a known
// thread goes to the workflow's run waiting for it, waits in the inbox
// while the thread's run is going but not waiting, and otherwise starts a
// run, with the thread when one was found.
func (s *Service) threaded(ctx context.Context, x inboxRow, meta Meta, msg *Message, ep endpoint, cfg Config) (record, []keptFile, error) {
	var th threadMatch
	var waiting, live bool
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{x.tenant}, func(tx pgx.Tx) error {
		var err error
		if th, err = s.findThread(ctx, tx, x.tenant, ep, meta, msg); err != nil || th.thread == "" {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM signal_waits w JOIN runs r ON r.id = w.run_id
			WHERE w.tenant_id = $1 AND w.environment = $2 AND w.event = $3 AND w.correlation = $4 AND r.workflow_id = $5)`,
			x.tenant, ep.env, ReplyEvent, th.thread, ep.workflow).Scan(&waiting); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM email_thread_messages m JOIN runs r ON r.id = m.run_id
			WHERE m.endpoint_id = $1 AND m.thread = $2 AND r.workflow_id = $3 AND r.status NOT IN ('completed', 'failed', 'cancelled'))`,
			ep.id, th.thread, ep.workflow).Scan(&live)
	})
	if err != nil {
		return record{}, nil, err
	}
	hold := live && s.now().Sub(meta.ReceivedAt) < HoldFor
	if th.thread == "" || s.Wake == nil || (!waiting && !hold) {
		return s.startRun(ctx, x, meta, msg, ep, cfg, th)
	}
	if !waiting {
		return record{}, nil, errHeld
	}
	files, refs, skipped, err := s.attachments(ctx, x.tenant, msg, cfg)
	if err != nil {
		return record{}, nil, err
	}
	discard := func() {
		if len(files) == 0 || s.Files == nil {
			return
		}
		paths := make([]string, 0, len(files))
		for _, f := range files {
			paths = append(paths, f.path)
		}
		if err := s.Files.Delete(ctx, x.tenant, media.PrivateBucket, paths); err != nil {
			s.log().Warn("email: removing a reply's files", "err", err)
		}
	}
	raw, _ := json.Marshal(map[string]any{"body": Body(msg, refs, skipped), "email": emailOf(meta, th)})
	var payload any
	_ = json.Unmarshal(raw, &payload)
	if s.PII != nil {
		err = db.InTenantTx(ctx, s.Pool, []uuid.UUID{x.tenant}, func(tx pgx.Tx) error {
			sealed, err := pii.Seal(ctx, s.PII, tx, x.tenant, payload, replyPaths, pii.Taint{})
			if err != nil {
				return err
			}
			payload, err = pii.SealDetected(ctx, s.PII, tx, x.tenant, sealed, pii.Taint{})
			return err
		})
		if err != nil {
			discard()
			return record{}, nil, err
		}
	}
	woke, fresh, err := s.Wake(ctx, x.tenant, ep.env, ep.workflow, ReplyEvent, th.thread, hex.EncodeToString(x.idem), payload)
	if err != nil {
		discard()
		return record{}, nil, err
	}
	switch {
	case !fresh:
		// Delivered before, and not recorded: recorded now.
		discard()
		return record{status: "replied", note: "Delivered to the run waiting for " + th.thread + "."}, nil, nil
	case len(woke) == 0:
		// It stopped waiting since: looked at again shortly.
		discard()
		return record{}, nil, errHeld
	}
	return record{status: "replied", run: &woke[0], attachments: len(files)}, files, nil
}

// ThreadEmail is one email to a customer in a thread (email@1
// send_in_thread).
type ThreadEmail struct {
	Tenant, Run uuid.UUID
	Environment string
	To          string
	Subject     string
	Message     string
	Thread      string
	InReplyTo   string
	References  []string
	// Address names the threaded email workflow whose address the email
	// is sent from and replies come back to: its Taskiem address
	// (trigger.email.address, with or without a tag) or its workflow id.
	// Empty is the run's own workflow. Another workflow must be of the
	// same business and environment, with threads on.
	Address string
	// Key is the step's idempotency key: a retry sends nothing new and
	// answers the same.
	Key string
}

func fatalf(format string, a ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, a...), effects.ErrFatal)
}

// SendInThread emails a customer in a thread from the platform's sending
// address in the business's name: Reply-To the thread's reply address, a
// Message-ID Taskiem makes, In-Reply-To and References so mail apps
// thread it, and the subject tagged [#KEY] once (decision 0063).
func (s *Service) SendInThread(ctx context.Context, e ThreadEmail) (map[string]any, error) {
	thread, ok := ThreadKey(e.Thread)
	switch {
	case !ok:
		return nil, fatalf("thread %q is not a ticket key: letters, digits and single hyphens, at most %d characters (T-1042)", e.Thread, MaxThreadKey)
	case strings.TrimSpace(e.Message) == "":
		return nil, fatalf("a message is required")
	case s.Messages == nil:
		return nil, fatalf("this Taskiem does not send email to customers")
	case s.Domain == "":
		return nil, fatalf("this Taskiem receives no email (TASKIEM_MAIL_DOMAIN), so replies could not come back")
	}
	to, err := sender.NormaliseEmail(e.To)
	if err != nil {
		return nil, fatalf("to: %q is not an email address", e.To)
	}
	clean := func(id string) string { return strings.Trim(strings.TrimSpace(id), "<>") }
	inReplyTo := clean(e.InReplyTo)
	if inReplyTo != "" && !ValidMessageID(inReplyTo) {
		return nil, fatalf("in_reply_to %q is not a Message-ID", e.InReplyTo)
	}
	var given []string
	for _, r := range e.References {
		r = clean(r)
		if !ValidMessageID(r) {
			return nil, fatalf("references: %q is not a Message-ID", r)
		}
		given = append(given, r)
	}
	var out map[string]any
	err = db.InTenantTx(ctx, s.Pool, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		ep, err := s.threadEndpoint(ctx, tx, e)
		if err != nil {
			return err
		}
		if len(ep.replyKey) == 0 {
			key := make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `UPDATE email_endpoints SET reply_key = COALESCE(reply_key, $2) WHERE id = $1 RETURNING reply_key`, ep.id, key).Scan(&ep.replyKey); err != nil {
				return err
			}
		}
		var business string
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, e.Tenant).Scan(&business); err != nil {
			return err
		}
		id := ThreadMessageID(ep.replyKey, ep.id, e.Key, s.Domain)
		// What Taskiem sent in the thread before, newest first.
		rows, err := tx.Query(ctx, `SELECT message_id FROM email_thread_messages WHERE endpoint_id = $1 AND thread = $2 AND message_id <> $3
			ORDER BY created_at DESC, id DESC LIMIT $4`, ep.id, thread, id, maxReferences)
		if err != nil {
			return err
		}
		earlier, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		if inReplyTo == "" && len(earlier) > 0 {
			inReplyTo = earlier[0]
		}
		refs := threadReferences(given, earlier, inReplyTo)
		subject := TagSubject(e.Subject, thread)
		replyTo := ep.publicID + "+" + ReplyTag(ep.replyKey, ep.id, thread) + "@" + s.Domain
		if _, err := s.Messages.Enqueue(ctx, tx, sender.Message{Tenant: e.Tenant, Channel: sender.Email, Purpose: sender.PurposeNotice, To: to,
			SenderName: business, ReplyTo: replyTo, Subject: subject, Text: e.Message, Key: "thread:" + e.Key,
			MessageID: id, InReplyTo: inReplyTo, References: refs}); err != nil {
			return err
		}
		var recipient *string
		if s.PII != nil {
			sub, err := s.PII.SubjectForTx(ctx, tx, e.Tenant, "email", to)
			if err != nil && !errors.Is(err, pii.ErrErased) {
				return err
			}
			if sub != "" {
				recipient = &sub
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO email_thread_messages (id, tenant_id, endpoint_id, thread, message_id, run_id, recipient_subject)
			VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (endpoint_id, message_id) DO NOTHING`,
			uuid.Must(uuid.NewV7()), e.Tenant, ep.id, thread, id, e.Run, recipient); err != nil {
			return err
		}
		out = map[string]any{"message_id": id, "thread": thread, "reply_to": replyTo, "subject": subject,
			"in_reply_to": inReplyTo, "references": stringsAny(refs)}
		return nil
	})
	switch {
	case errors.Is(err, effects.ErrFatal):
		return nil, err
	case errors.Is(err, sender.ErrInvalid):
		return nil, fmt.Errorf("%w: %w", effects.ErrFatal, err)
	case err != nil:
		return nil, fmt.Errorf("%w: %w", effects.ErrRetryable, err)
	}
	return out, nil
}

// threadEndpoint is the address an email in a thread is sent from: the
// run's own workflow's, or the one e.Address names, which must be an
// email workflow of the run's business in the run's environment, live and
// with threads on.
func (s *Service) threadEndpoint(ctx context.Context, tx pgx.Tx, e ThreadEmail) (endpoint, error) {
	const cols = `SELECT e.id, e.public_id, e.status, e.environment, e.reply_key, v.definition->'trigger'->'config'->>'threads'
			FROM email_endpoints e JOIN workflow_versions v ON v.workflow_id = e.workflow_id AND v.version = e.version `
	var ep endpoint
	var status, env string
	var threads *string
	scan := func(row pgx.Row) error {
		return row.Scan(&ep.id, &ep.publicID, &status, &env, &ep.replyKey, &threads)
	}
	name := strings.TrimSpace(e.Address)
	var err error
	switch {
	case name == "":
		err = scan(tx.QueryRow(ctx, cols+`JOIN runs r ON r.workflow_id = e.workflow_id WHERE r.id = $1 AND e.environment = $2`, e.Run, e.Environment))
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ep, fatalf("sending in a thread needs this workflow to start when an email comes in, with threads on, or the address of one that does")
		case err != nil:
			return ep, err
		}
	default:
		if wf, perr := uuid.Parse(name); perr == nil {
			// One address per environment: this environment's first.
			err = scan(tx.QueryRow(ctx, cols+`WHERE e.workflow_id = $1 AND e.tenant_id = $2 ORDER BY e.environment = $3 DESC LIMIT 1`, wf, e.Tenant, e.Environment))
		} else {
			public, ok := s.addressID(name)
			if !ok {
				return ep, fatalf("address %q is not a Taskiem email address of this business (k3x9q2m7a1bz@%s) or a workflow id", name, s.Domain)
			}
			err = scan(tx.QueryRow(ctx, cols+`WHERE e.public_id = $1 AND e.tenant_id = $2`, public, e.Tenant))
		}
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ep, fatalf("address %q is not the email address of a workflow of this business", name)
		case err != nil:
			return ep, err
		case env != e.Environment:
			return ep, fatalf("address %q belongs to the workflow's %s environment, not %s, where this run is", name, env, e.Environment)
		}
	}
	switch {
	case threads == nil || *threads != "true":
		if name == "" {
			return ep, fatalf("sending in a thread needs threads on in this workflow's email trigger, so replies come back to the ticket")
		}
		return ep, fatalf("sending in a thread needs threads on in the email trigger of the workflow at %q, so replies come back to the ticket", name)
	case status != "live":
		return ep, fatalf("the email address is %s, so replies could not come back", status)
	}
	return ep, nil
}

// addressID is the public id of a Taskiem address given as
// <id>[+tag]@<domain>, or as the bare id.
func (s *Service) addressID(addr string) (string, bool) {
	addr = strings.ToLower(strings.Trim(addr, "<>"))
	local := addr
	if at := strings.LastIndexByte(addr, '@'); at >= 0 {
		if addr[at+1:] != strings.ToLower(s.Domain) {
			return "", false
		}
		local = addr[:at]
	}
	if i := strings.IndexByte(local, '+'); i >= 0 {
		local = local[:i]
	}
	return local, localPart.MatchString(local)
}

// threadReferences is an email's References: those given (the
// customer's thread), then what Taskiem sent before (oldest first), then
// the message it answers, each once; past maxReferences, the first (the
// thread's start) and the newest are kept.
func threadReferences(given, earlierNewestFirst []string, inReplyTo string) []string {
	out := []string{}
	add := func(id string) {
		if id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	for _, r := range given {
		add(r)
	}
	for i := len(earlierNewestFirst) - 1; i >= 0; i-- {
		add(earlierNewestFirst[i])
	}
	add(inReplyTo)
	if len(out) > maxReferences {
		out = append(out[:1], out[len(out)-maxReferences+1:]...)
	}
	return out
}
