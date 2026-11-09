package assistant

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/pii"
)

// Mode is Taskiem's switch for every business's assistants (E4, L11).
type Mode string

// Modes: on for everyone; beta for businesses Taskiem has flagged only;
// off for no one (TASKIEM_ASSISTANTS).
const (
	ModeOn   Mode = "on"
	ModeBeta Mode = "beta"
	ModeOff  Mode = "off"
)

// ParseMode reads TASKIEM_ASSISTANTS (on when empty).
func ParseMode(s string) (Mode, error) {
	switch m := Mode(strings.ToLower(strings.TrimSpace(s))); m {
	case "":
		return ModeOn, nil
	case ModeOn, ModeBeta, ModeOff:
		return m, nil
	}
	return "", fmt.Errorf("TASKIEM_ASSISTANTS is on, beta or off, not %q", s)
}

// Message is a customer's message as a chat trigger's conversation block
// reads it.
type Message struct {
	Channel        string // <connector ref>/<connection>
	Sender         string
	SenderCategory string
	Private        bool
	Kind           string
	Text           string
}

// What a run started by the guard is asked to do (trigger.guard.outcome).
const (
	OutcomeAnswer = "answer" // answer the message (after notice, when set)
	OutcomeReply  = "reply"  // send reply, a fixed answer, and nothing else
	OutcomeQuiet  = "quiet"  // keep the message (the conversation's record) and answer nothing
)

// Why (trigger.guard.reason, or the reason no run started).
const (
	ReasonLimited  = "limited"  // over the per-customer limit: told once
	ReasonStopped  = "stopped"  // wrote STOP: confirmed
	ReasonStarted  = "started"  // wrote START after STOP: confirmed
	ReasonHandover = "handover" // asked for a person: confirmed, members told
	ReasonKind     = "kind"     // a kind of message the assistant does not read: a fixed answer
	ReasonPaused   = "paused"   // the owner paused the assistant for everyone
	ReasonWaiting  = "waiting"  // a person is answering this customer

	DropNotCustomer = "not_customer" // not a new message in a private chat
	DropOff         = "off"          // Taskiem's switch is off (or beta, and the business is not in it)
	DropStoppedByUs = "stopped_by_taskiem"
	DropStopped     = "stopped" // the customer wrote STOP
	DropLimited     = "limited" // over the limit, already told
	DropKind        = "kind"
	DropBusy        = "busy" // the workflow's queue is full
)

// Decision is what the guard decided for a message.
type Decision struct {
	Start    bool
	Outcome  string
	Reason   string
	Dropped  string // why no run, when Start is false
	Reply    string // the fixed answer to send (outcome reply)
	Notice   string // the notice to send first (a new customer)
	Language string
	First    bool
	Subject  string
}

// Map is the decision as a run sees it: trigger.guard. The sender and the
// text are personal data (sealed with the run, under the customer).
func (d Decision) Map(m Message, kind string) map[string]any {
	out := map[string]any{"outcome": d.Outcome, "reason": d.Reason, "reply": d.Reply, "notice": d.Notice, "language": d.Language,
		"first": d.First, "kind": kind, "channel": m.Channel, "sender": m.Sender, "text": m.Text}
	return out
}

// Sealer is what the guard needs of the vault: a sender's subject, and
// the sender sealed under it.
type Sealer interface {
	pii.Cipher
	pii.SubjectSealer
}

// Guard decides, for each customer's message, whether a run starts and
// what it is asked to do.
type Guard struct {
	Mode    Mode
	Catalog *lang.Catalog
	Now     func() time.Time
}

func (g *Guard) now() time.Time {
	if g != nil && g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func (g *Guard) text(t, id string, kv ...string) string {
	c := lang.Default()
	if g != nil && g.Catalog != nil {
		c = g.Catalog
	}
	tag := lang.Tag(t)
	if !lang.Valid(tag) {
		tag = lang.EN
	}
	return c.Text(tag, id, kv...)
}

// Text is a fixed answer in a language (English when the language is not
// one Taskiem has), with its placeholders filled.
func (g *Guard) Text(language, id string, kv ...string) string { return g.text(language, id, kv...) }

// tags are the languages a customer may write in.
func tags() []lang.Tag {
	var out []lang.Tag
	for _, i := range lang.All() {
		out = append(out, i.Tag)
	}
	return out
}

// Decide is the guard for one message, in the business's transaction: it
// records the customer (when they first and last wrote, their counts, the
// words they used) and answers what the workflow's run should do, or why
// none starts. workflow is the subscribed workflow, for its queue.
func (g *Guard) Decide(ctx context.Context, tx pgx.Tx, sealer Sealer, tenant, workflow uuid.UUID, cfg Config, m Message) (Decision, error) {
	if !m.Private || strings.TrimSpace(m.Sender) == "" {
		return Decision{Dropped: DropNotCustomer}, nil
	}
	mode := ModeOn
	if g != nil && g.Mode != "" {
		mode = g.Mode
	}
	if mode == ModeOff {
		return Decision{Dropped: DropOff}, nil
	}
	var beta bool
	var stoppedByUs *time.Time
	err := tx.QueryRow(ctx, `SELECT beta, stopped_at FROM assistant_controls WHERE tenant_id = $1`, tenant).Scan(&beta, &stoppedByUs)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Decision{}, err
	}
	if stoppedByUs != nil {
		return Decision{Dropped: DropStoppedByUs}, nil
	}
	if mode == ModeBeta && !beta {
		return Decision{Dropped: DropOff}, nil
	}
	cat := m.SenderCategory
	if cat == "" {
		cat = "other"
	}
	subject, err := sealer.SubjectForTx(ctx, tx, tenant, cat, m.Sender)
	if err != nil {
		return Decision{}, err
	}
	// Sealing the sender makes their key (a new one for a person erased
	// who came back), so the run can seal under it.
	sealed, err := sealer.SealTx(ctx, tx, tenant, cat, m.Sender)
	if err != nil {
		return Decision{}, err
	}
	now := g.now().UTC()
	language := ""
	if m.Text != "" {
		if t, sure := lang.DetectAny(m.Text, tags()); sure {
			language = string(t)
		}
	}
	c, first, err := touch(ctx, tx, tenant, m.Channel, subject, sealed, language, now)
	if err != nil {
		return Decision{}, err
	}
	if language == "" {
		language = c.language
	}
	d := Decision{Language: language, First: first, Subject: subject}
	start := func(outcome, reason, reply string) (Decision, error) {
		// The workflow's queue: a full one drops the message, so one busy
		// assistant never fills the business's backlog (L8).
		var queued int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM runs WHERE workflow_id = $1 AND status = 'queued'`, workflow).Scan(&queued); err != nil {
			return Decision{}, err
		}
		if queued >= cfg.MaxQueued {
			_, err := alerts.Notify(ctx, tx, tenant, alerts.Alert{Kind: AlertBusy, Dedup: workflow.String() + ":" + now.Format("2006-01-02"),
				Title: "Your assistant is too busy", Body: fmt.Sprintf("%d messages are waiting for your assistant, so new ones are not answered. "+
					"They will be again as it catches up. If this goes on, ask whoever runs your Taskiem about your plan's limits.", queued),
				Link: "/workflows/" + workflow.String(), Detail: map[string]any{"workflow": workflow, "queued": queued}})
			return Decision{Dropped: DropBusy}, err
		}
		d.Start, d.Outcome, d.Reason, d.Reply = true, outcome, reason, reply
		if first && cfg.Notice {
			d.Notice = g.text(language, "assistant.notice", "business", businessName(ctx, tx, tenant))
		}
		return d, nil
	}
	word := WordNone
	if m.Kind == "text" || m.Kind == "button" {
		word = WordOf(m.Text)
	}
	// STOP and START (P3): STOP ends the assistant for this customer, on
	// this channel, until they write START.
	if c.stoppedAt != nil {
		if word != WordStart {
			return Decision{Dropped: DropStopped, Subject: subject}, nil
		}
		if _, err := tx.Exec(ctx, `UPDATE assistant_contacts SET stopped_at = NULL WHERE tenant_id = $1 AND channel = $2 AND subject = $3`, tenant, m.Channel, subject); err != nil {
			return Decision{}, err
		}
		return start(OutcomeReply, ReasonStarted, g.text(language, "assistant.started"))
	}
	if word == WordStop {
		if _, err := tx.Exec(ctx, `UPDATE assistant_contacts SET stopped_at = $4 WHERE tenant_id = $1 AND channel = $2 AND subject = $3`, tenant, m.Channel, subject, now); err != nil {
			return Decision{}, err
		}
		d.First = false // no notice before a goodbye
		first = false
		return start(OutcomeReply, ReasonStopped, g.text(language, "assistant.stopped"))
	}
	// Over the limit (L1): told once per minute or day over it, then quiet.
	if over, since := c.over(cfg, now); over {
		if c.limitedAt != nil && !c.limitedAt.Before(since) {
			return Decision{Dropped: DropLimited, Subject: subject}, nil
		}
		if _, err := tx.Exec(ctx, `UPDATE assistant_contacts SET limited_at = $4 WHERE tenant_id = $1 AND channel = $2 AND subject = $3`, tenant, m.Channel, subject, now); err != nil {
			return Decision{}, err
		}
		return start(OutcomeReply, ReasonLimited, g.text(language, "assistant.limited"))
	}
	// The owner paused the assistant for everyone (L11): messages are kept,
	// nothing is answered.
	var paused *time.Time
	if err := tx.QueryRow(ctx, `SELECT assistant_paused_at FROM tenant_channel_settings`).Scan(&paused); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Decision{}, err
	}
	if paused != nil {
		return start(OutcomeQuiet, ReasonPaused, "")
	}
	// A person is answering them (D13).
	if c.handoverUntil != nil && c.handoverUntil.After(now) {
		return start(OutcomeQuiet, ReasonWaiting, "")
	}
	if word == WordPerson {
		if err := HandOver(ctx, tx, tenant, m.Channel, subject, m.Sender, cat, cfg.HandoverHours, now, "customer"); err != nil {
			return Decision{}, err
		}
		return start(OutcomeReply, ReasonHandover, g.text(language, "assistant.handover", "business", businessName(ctx, tx, tenant)))
	}
	switch cfg.KindAction(m.Kind) {
	case KindDrop:
		return Decision{Dropped: DropKind, Subject: subject}, nil
	case KindReply:
		return start(OutcomeReply, ReasonKind, g.text(language, "assistant.kind"))
	}
	return start(OutcomeAnswer, "", "")
}

// Alert kinds.
const (
	AlertHandover = "assistant_handover"
	AlertBusy     = "assistant_busy"
)

type contact struct {
	language                  string
	minuteAt                  time.Time
	minuteCount               int
	day                       time.Time
	dayCount                  int
	limitedAt, stoppedAt      *time.Time
	handoverAt, handoverUntil *time.Time
}

// over reports whether the customer is over a limit, and since when the
// window they are over began (to tell them once per window).
func (c contact) over(cfg Config, now time.Time) (bool, time.Time) {
	if c.minuteCount > cfg.PerMinute {
		return true, c.minuteAt
	}
	if c.dayCount > cfg.PerDay {
		return true, c.day
	}
	return false, time.Time{}
}

// touch records a message from a customer: their window, their language,
// this minute's and today's counts. It answers the row as it is now, and
// whether they are new.
func touch(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, channel, subject string, sealed map[string]any, language string, now time.Time) (contact, bool, error) {
	var c contact
	var inserted bool
	err := tx.QueryRow(ctx, `INSERT INTO assistant_contacts AS a (tenant_id, channel, subject, sender, language, first_at, last_inbound_at, minute_at, minute_count, day, day_count)
		VALUES ($1, $2, $3, $4, COALESCE(NULLIF($5, ''), 'en'), $6, $6, $6, 1, ($6 AT TIME ZONE 'Africa/Lagos')::date, 1)
		ON CONFLICT (tenant_id, channel, subject) DO UPDATE SET
		  sender = EXCLUDED.sender,
		  language = COALESCE(NULLIF($5, ''), a.language),
		  last_inbound_at = $6,
		  minute_count = CASE WHEN a.minute_at > $6 - interval '1 minute' THEN a.minute_count + 1 ELSE 1 END,
		  minute_at = CASE WHEN a.minute_at > $6 - interval '1 minute' THEN a.minute_at ELSE $6 END,
		  day_count = CASE WHEN a.day = ($6 AT TIME ZONE 'Africa/Lagos')::date THEN a.day_count + 1 ELSE 1 END,
		  day = ($6 AT TIME ZONE 'Africa/Lagos')::date
		RETURNING language, minute_at, minute_count, day, day_count, limited_at, stopped_at, handover_at, handover_until, (xmax = 0)`,
		tenant, channel, subject, sealed, language, now).Scan(&c.language, &c.minuteAt, &c.minuteCount, &c.day, &c.dayCount,
		&c.limitedAt, &c.stoppedAt, &c.handoverAt, &c.handoverUntil, &inserted)
	return c, inserted, err
}

// HandOver hands a customer to a person (D13): the assistant stays quiet
// for them for hours (it keeps their messages), and the owners are told.
// by is who asked: the customer (their word) or a workflow.
func HandOver(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, channel, subject, sender, category string, hours int, now time.Time, by string) error {
	if hours <= 0 {
		hours = Defaults().HandoverHours
	}
	until := now.Add(time.Duration(hours) * time.Hour)
	tag, err := tx.Exec(ctx, `UPDATE assistant_contacts SET handover_at = $4, handover_until = $5 WHERE tenant_id = $1 AND channel = $2 AND subject = $3`,
		tenant, channel, subject, now, until)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNoContact
	}
	where := channelName(channel)
	_, err = alerts.TellOwners(ctx, tx, tenant, alerts.Alert{Kind: AlertHandover, Dedup: subject + ":" + now.Format(time.RFC3339Nano),
		Title: "A customer wants to talk to a person",
		Body: fmt.Sprintf("A customer%s on %s asked to talk to a person. Answer them there: your assistant stays quiet for them for %d hours.",
			maskSender(sender, category), where, hours),
		Detail: map[string]any{"channel": channel, "customer": subject, "by": by, "until": until}})
	return err
}

// ErrNoContact: no such customer on the channel.
var ErrNoContact = errors.New("assistant: no such customer on this channel")

// Resume ends a hand-over: the assistant answers the customer again.
func Resume(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, channel, subject string) error {
	tag, err := tx.Exec(ctx, `UPDATE assistant_contacts SET handover_at = NULL, handover_until = NULL WHERE tenant_id = $1 AND channel = $2 AND subject = $3`,
		tenant, channel, subject)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNoContact
	}
	return err
}

// Window is WhatsApp's 24-hour customer service window (C13): a business
// may answer freely within 24 hours of the customer's last message, and
// only with an approved template after.
const Window = 24 * time.Hour

// WindowOf answers when a customer last wrote on a channel and whether the
// window is open now.
func WindowOf(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, channel, subject string, now time.Time) (open bool, last *time.Time, err error) {
	err = tx.QueryRow(ctx, `SELECT last_inbound_at FROM assistant_contacts WHERE tenant_id = $1 AND channel = $2 AND subject = $3`, tenant, channel, subject).Scan(&last)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	return now.Sub(*last) < Window, last, nil
}

// EraseContacts deletes what the guard keeps about a customer (their
// counts, STOP, a hand-over), on every channel.
func EraseContacts(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, subject string) (int, error) {
	tag, err := tx.Exec(ctx, `DELETE FROM assistant_contacts WHERE tenant_id = $1 AND (subject = $2 OR left(subject, length($2) + 1) = $2 || '~')`, tenant, subject)
	return int(tag.RowsAffected()), err
}

func businessName(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) string {
	var name string
	if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&name); err != nil || strings.TrimSpace(name) == "" {
		return "this business"
	}
	return name
}

func channelName(channel string) string {
	switch {
	case strings.HasPrefix(channel, "whatsapp@"):
		return "WhatsApp"
	case strings.HasPrefix(channel, "telegram@"):
		return "Telegram"
	}
	return "chat"
}

// maskSender shows a customer to members without their number in an
// alert: the last four digits of a number, or nothing.
func maskSender(sender, category string) string {
	if category == "phone" && utf8.RuneCountInString(sender) > 4 {
		return " (number ending " + sender[len(sender)-4:] + ")"
	}
	return ""
}
