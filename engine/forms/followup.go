package forms

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// The follow-up of a form left half-filled (docs/spec/forms-that-sell.md
// §6.3 to §6.5): once a record's wait has passed since its last keep, the
// api role counts it as left and, within the caps, sends one message
// about the order the buyer started, by the channel they gave, with a
// link that restores their choice. A typed phone is reached by SMS
// whatever it matches (0039 point 3): never a linked chat. WhatsApp by an
// approved template on the business's own number is L4's, through
// FollowupChat; until then it is nil and a phone gets a text.

// The caps (§6.5; starting figures).
const (
	// FollowupsPerFormDay and FollowupsPerBusinessDay cap follow-ups in 24
	// hours, besides the plan's month (form_followups).
	FollowupsPerFormDay     = 50
	FollowupsPerBusinessDay = 100
	// HoldFactor: a form whose records kept today pass HoldFactor times
	// its orders a day (over four weeks, at least HoldFloor) has its
	// follow-ups held for the owner to release.
	HoldFactor = 10
	HoldFloor  = 3.0
	// StaleAfter: a record held longer than this past its wait is no
	// longer followed up, released or not.
	StaleAfter = 24 * time.Hour
)

// FollowupMessage is one follow-up as a channel sends it.
type FollowupMessage struct {
	Tenant   uuid.UUID
	Language lang.Tag
	// To is the contact: a phone as E.164 or an email.
	To       string
	Business string
	// Product and Detail name what was being bought ("Smart Ear Cleaner",
	// "2 items, ₦45,000"); Link restores it.
	Product, Detail, Link string
	// Key makes the message one per record.
	Key string
	// Token is the restore link's token (the end of Link), for a chat
	// template whose button opens the link under a fixed address.
	Token string
}

// FollowupChat reaches a typed phone in a chat app: on WhatsApp, by an
// approved template on the business's own number (L4, followup_chat.go).
// It answers sent false when it cannot (no own number, the template not
// approved there, held back past the business's allowance, refused by
// WhatsApp), and the phone gets a text; ref is the message's id, so a
// failure WhatsApp reports later sends the text then (FollowupFailed).
type FollowupChat interface {
	// OptIn: the template is a marketing one, so it reaches only a buyer
	// who asked for WhatsApp on the form (Choice.WhatsApp).
	OptIn() bool
	FollowUp(ctx context.Context, tx pgx.Tx, m FollowupMessage) (ref string, sent bool, err error)
}

type dueRow struct {
	id, tenant, action uuid.UUID
	front              *uuid.UUID
	site               *string
	contact            map[string]any
	kind               string
	hash               []byte
	choice             Choice
	lastKeep           time.Time
	on, held           bool
}

// errNotSent is a follow-up the outbox refused: its outcome is recorded
// in a transaction of its own.
type errNotSent struct{ outcome string }

func (e errNotSent) Error() string { return "forms: follow-up not sent: " + e.outcome }

// FollowUps takes the records past their wait and follows each up, or
// says why not. One that fails is tried again once its claim lapses.
func (s *Service) FollowUps(ctx context.Context) error { return s.followUps(ctx, 50) }

func (s *Service) followUps(ctx context.Context, n int) error {
	if s.Messages == nil {
		return nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, tenant_id, action_id, front_id, site, contact, contact_kind, contact_hash, choice, last_keep_at, followup_on, held
		FROM taskiem_form_left_due($1)`, n)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (dueRow, error) {
		var x dueRow
		return x, r.Scan(&x.id, &x.tenant, &x.action, &x.front, &x.site, &x.contact, &x.kind, &x.hash, &x.choice, &x.lastKeep, &x.on, &x.held)
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, x := range list {
		if err := s.followUp(ctx, x); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) followUp(ctx context.Context, x dueRow) error {
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{x.tenant}, func(tx pgx.Tx) error {
		outcome, err := s.followUpTx(ctx, tx, x)
		if err != nil || outcome == "followed" || outcome == "gone" {
			return err
		}
		return s.mark(ctx, tx, x, outcome)
	})
	var ns errNotSent
	if errors.As(err, &ns) {
		return db.InTenantTx(ctx, s.Pool, []uuid.UUID{x.tenant}, func(tx pgx.Tx) error { return s.mark(ctx, tx, x, ns.outcome) })
	}
	return err
}

// mark records what became of a record past its wait; "" is held.
func (s *Service) mark(ctx context.Context, tx pgx.Tx, x dueRow, outcome string) error {
	var o *string
	if outcome != "" {
		o = &outcome
	}
	_, err := tx.Exec(ctx, `SELECT taskiem_form_left_mark($1, $2, $3)`, x.tenant, x.id, o)
	return err
}

// followUpTx decides and, when it may, sends: "followed", an outcome why
// not, "gone" when the record's page has its order (the record is
// forgotten), or "" when the form's follow-ups are held.
func (s *Service) followUpTx(ctx context.Context, tx pgx.Tx, x dueRow) (string, error) {
	if !x.on {
		return "off", nil
	}
	// The page's order was sent since (with another contact, a typo
	// corrected): the page is done, and its record forgotten.
	var done bool
	if err := tx.QueryRow(ctx, `SELECT taskiem_form_left_done($1, $2)`, x.tenant, x.id).Scan(&done); err != nil {
		return "", err
	}
	if done {
		return "gone", nil
	}
	var wait int
	var released *time.Time
	if err := tx.QueryRow(ctx, `SELECT followup_wait, followup_released_at FROM storefront_actions WHERE id = $1`, x.action).Scan(&wait, &released); err != nil {
		return "", err
	}
	now := s.now()
	if now.Sub(x.lastKeep) > time.Duration(wait)*time.Minute+StaleAfter {
		return "stale", nil
	}
	// One follow-up of a business at a time, across the api's replicas:
	// the day's and the month's caps are read and spent under this lock,
	// held until the follow-up is recorded.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 901))`, x.tenant.String()); err != nil {
		return "", err
	}
	var formDay, bizDay, bizMonth, keptToday int
	var ordersDaily float64
	var stopped bool
	if err := tx.QueryRow(ctx, `SELECT form_day, business_day, business_month, kept_today, orders_daily::float8, stopped FROM taskiem_form_left_caps($1, $2, $3)`,
		x.tenant, x.action, x.hash).Scan(&formDay, &bizDay, &bizMonth, &keptToday, &ordersDaily, &stopped); err != nil {
		return "", err
	}
	if s.afterCaps != nil {
		s.afterCaps()
	}
	if stopped {
		return "stopped", nil
	}
	if x.held {
		return "", nil
	}
	// A sudden run of keeps holds the form's follow-ups, unless the owner
	// released them today.
	today := now.UTC().Truncate(24 * time.Hour)
	if float64(keptToday) > HoldFactor*max(ordersDaily, HoldFloor) && (released == nil || released.Before(today)) {
		if _, err := tx.Exec(ctx, `UPDATE storefront_actions SET followup_held_at = now() WHERE id = $1 AND followup_held_at IS NULL`, x.action); err != nil {
			return "", err
		}
		s.log().Warn("forms: follow-ups held: a run of keeps on one form", "tenant", x.tenant, "action", x.action, "kept_today", keptToday)
		return "", nil
	}
	if formDay >= FollowupsPerFormDay || bizDay >= FollowupsPerBusinessDay {
		return "capped", nil
	}
	if s.FollowupLimit != nil {
		limit, err := s.FollowupLimit(ctx, x.tenant)
		if err != nil {
			return "", err
		}
		if limit > 0 && int64(bizMonth) >= limit {
			return "limit", nil
		}
	}
	m, ok, err := s.followupMessage(ctx, tx, x)
	if err != nil {
		return "", err
	}
	if !ok {
		return "stale", nil // the form or its product is no longer on the page
	}
	// The person's allowance across businesses, taken only for a message
	// that is sent: a message the outbox then refuses rolls it back.
	var took bool
	if err := tx.QueryRow(ctx, `SELECT taskiem_form_followup_take($1)`, x.hash).Scan(&took); err != nil {
		return "", err
	}
	if !took {
		return "capped", nil
	}
	channel := sender.SMS
	if x.kind == "email" {
		channel = sender.Email
	}
	if channel == sender.SMS && s.FollowupChat != nil {
		// L4: WhatsApp by an approved template, where there is one; else
		// the text below (followup_chat.go).
		if sent, err := s.followUpChat(ctx, tx, x, m); err != nil || sent {
			return "followed", err
		}
	}
	token, ok, err := s.FollowedUp(ctx, tx, x.tenant, x.id, channel)
	if err != nil || !ok {
		return "", err
	}
	m.Link = s.backLink(ctx, tx, x.tenant, token)
	if err := s.sendFollowup(ctx, tx, channel, m); err != nil {
		switch {
		case errors.Is(err, sender.ErrRecipientLimit):
			return "", errNotSent{"capped"}
		case errors.Is(err, sender.ErrAllowance):
			return "", errNotSent{"limit"}
		case errors.Is(err, sender.ErrInvalid):
			return "", errNotSent{"failed"}
		}
		return "", err
	}
	return "followed", nil
}

// followupMessage is the message for a record: the business, the product
// and the package from the page as published, and the contact opened.
func (s *Service) followupMessage(ctx context.Context, tx pgx.Tx, x dueRow) (FollowupMessage, bool, error) {
	m, err := storefront.PublishedModel(ctx, tx, x.tenant)
	if errors.Is(err, storefront.ErrNotFound) {
		return FollowupMessage{}, false, nil
	}
	if err != nil {
		return FollowupMessage{}, false, err
	}
	var act *storefront.PublicAction
	for _, f := range m.Fronts {
		for i := range f.Actions {
			if a := f.Actions[i]; a.ID != nil && *a.ID == x.action {
				act = &f.Actions[i]
			}
		}
	}
	if act == nil {
		return FollowupMessage{}, false, nil
	}
	out := FollowupMessage{Tenant: x.tenant, Language: lang.Tag(m.Card.Language), Business: m.Card.Name, Key: "followup:" + x.id.String()}
	if out.Language == "" {
		out.Language = lang.EN
	}
	name := func(row string) string {
		var n string
		_ = tx.QueryRow(ctx, `SELECT COALESCE(c.item->>'name', '') FROM storefront_catalogue c JOIN storefronts s ON s.id = c.storefront_id
			WHERE s.tenant_id = $1 AND c.row_id = $2 LIMIT 1`, x.tenant, row).Scan(&n)
		return n
	}
	if pf, ok := storefront.FormPackages(act.Form); ok {
		out.Product = name(pf.Item)
		for _, p := range pf.Packages {
			if p.Qty == x.choice.Package {
				out.Detail = itemsWord(p.Qty) + ", " + storefront.FormatNaira(p.PriceKobo)
			}
		}
	} else if len(x.choice.Lines) > 0 {
		out.Product = name(x.choice.Lines[0].ID)
		n := 0
		for _, l := range x.choice.Lines {
			n += l.Qty
		}
		out.Detail = itemsWord(n)
	}
	if out.Product == "" {
		out.Product = act.Label
	}
	if r := []rune(out.Product); len(r) > 40 {
		out.Product = string(r[:39]) + "…"
	}
	v, err := s.Vault.OpenTx(ctx, tx, x.tenant, x.contact)
	if err != nil {
		return FollowupMessage{}, false, err
	}
	out.To, _ = v.(string)
	return out, out.To != "", nil
}

func itemsWord(n int) string {
	if n == 1 {
		return "1 item"
	}
	return fmt.Sprintf("%d items", n)
}

// backLink is the restore link on the storefront's own address.
func (s *Service) backLink(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, token string) string {
	var slug string
	_ = tx.QueryRow(ctx, `SELECT COALESCE(published_slug, slug) FROM storefronts WHERE tenant_id = $1`, tenant).Scan(&slug)
	return "https://" + slug + "." + s.ShopDomain + "/back/" + token
}

// FollowupText is the follow-up's words in a language: an SMS, or an
// email's subject and text.
func FollowupText(l lang.Tag, channel string, m FollowupMessage) (subject, text string) {
	c := lang.Default()
	what := m.Product
	if m.Detail != "" {
		what += " (" + m.Detail + ")"
	}
	if channel == sender.Email {
		return c.Text(l, "form.followup.email_subject", "business", m.Business),
			c.Text(l, "form.followup.email", "business", m.Business, "what", what, "link", m.Link)
	}
	return "", c.Text(l, "form.followup.sms", "what", what, "link", m.Link)
}

func (s *Service) sendFollowup(ctx context.Context, tx pgx.Tx, channel string, m FollowupMessage) error {
	if channel == sender.SMS {
		// "N45,000": the naira sign makes a text of 70 characters a part,
		// not 160.
		m.Detail = strings.ReplaceAll(m.Detail, "₦", "N")
	}
	subject, text := FollowupText(m.Language, channel, m)
	if channel == sender.SMS && sender.Segments(sender.CleanName(m.Business)+": "+text) > sender.MaxSegments {
		// Too long for three parts: without the package.
		m.Detail = ""
		_, text = FollowupText(m.Language, channel, m)
	}
	_, err := s.Messages.Enqueue(ctx, tx, sender.Message{Tenant: m.Tenant, Channel: channel, Purpose: sender.PurposeLink, To: m.To, SenderName: m.Business,
		Subject: subject, Text: text, Key: m.Key, ExpiresAt: s.now().Add(6 * time.Hour)})
	return err
}

// FollowupsOn reports whether an action follows up, for the public host's
// consent line and the owner's screens.
func FollowupsOn(ctx context.Context, tx pgx.Tx, action uuid.UUID) (on bool, wait int, held *time.Time, err error) {
	err = tx.QueryRow(ctx, `SELECT followup_on, followup_wait, followup_held_at FROM storefront_actions WHERE id = $1`, action).Scan(&on, &wait, &held)
	return on, wait, held, err
}

// The owner's view (§6.6).

// LeftSummary is a form's counts since a day.
type LeftSummary struct {
	Left     int `json:"left"`
	Followed int `json:"followed"`
	CameBack int `json:"came_back"`
}

// LeftRow is one record that left, as the owner sees it: never the
// contact, only its mask.
type LeftRow struct {
	ID         uuid.UUID  `json:"id"`
	Kind       string     `json:"contact_kind"`
	Contact    string     `json:"contact"`
	Product    string     `json:"product"`
	Package    int        `json:"package,omitempty"`
	Zone       string     `json:"zone,omitempty"`
	At         time.Time  `json:"at"`
	FollowedAt *time.Time `json:"followed_up_at,omitempty"`
	CameBackAt *time.Time `json:"came_back_at,omitempty"`
	Channel    string     `json:"channel,omitempty"`
	// Outcome is why it was not followed up: capped, stopped, limit, off,
	// stale or failed; empty while waiting or held, or once followed up.
	Outcome string `json:"outcome,omitempty"`
}

// Left reads a form's counts since since and its records that left.
func Left(ctx context.Context, tx pgx.Tx, tenant, scope uuid.UUID, since time.Time) (LeftSummary, []LeftRow, error) {
	var sum LeftSummary
	var kept int
	if err := tx.QueryRow(ctx, `SELECT kept, left_n, followed, came_back FROM taskiem_form_left_summary($1, $2, $3)`, tenant, scope, since.UTC().Format("2006-01-02")).
		Scan(&kept, &sum.Left, &sum.Followed, &sum.CameBack); err != nil {
		return sum, nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id, contact_kind, contact_mask, choice, last_keep_at, followed_up_at, came_back_at, COALESCE(channel, ''), COALESCE(outcome, '')
		FROM taskiem_form_left_list($1, $2)`, tenant, scope)
	if err != nil {
		return sum, nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (LeftRow, error) {
		var x LeftRow
		var ch Choice
		err := r.Scan(&x.ID, &x.Kind, &x.Contact, &ch, &x.At, &x.FollowedAt, &x.CameBackAt, &x.Channel, &x.Outcome)
		x.Package, x.Zone = ch.Package, ch.Zone
		if x.Outcome == "followed" {
			x.Outcome = ""
		}
		return x, err
	})
	return sum, list, err
}
