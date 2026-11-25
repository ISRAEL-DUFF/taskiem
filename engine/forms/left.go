package forms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/sender"
)

// A form left half-filled (docs/spec/forms-that-sell.md §6, §10): the
// public host sends a keep when a buyer has typed a contact into an order
// form that follows up and not sent it, carrying only the contact and the
// product choice, sealed with the public key for the api role. Here the
// api role takes keeps from their inbox, seals the contact under the
// person in the vault, keys it as message's per-person limits are, and
// keeps one record per form and contact (form_left), through narrow
// functions only. A submission from the same contact within the wait
// forgets the record; one after the follow-up marks it came back. Records
// last 7 days after their last keep, or after their follow-up. The
// follow-up itself is followup.go's.

// Keep is what the public host sends: the contact as typed (a Nigerian
// phone or an email) and what was being bought, never another answer.
type Keep struct {
	Contact string `json:"contact"`
	Choice  Choice `json:"choice"`
	// Front is the action's front; Site the website the form was framed
	// on, when it was.
	Front *uuid.UUID `json:"front,omitempty"`
	Site  string     `json:"site,omitempty"`
	At    time.Time  `json:"at"`
	// Page is the page's own key: the status hash the order sent from it
	// has (storefront.StatusHash of its status key). Two contacts typed on
	// one page make one record, the phone's, and a keep from a page whose
	// order is recorded already is dropped.
	Page []byte `json:"page,omitempty"`
}

// Choice is what the buyer was buying: a package's quantity, or the
// basket's lines, and the delivery area chosen.
type Choice struct {
	Package int          `json:"package,omitempty"`
	Size    string       `json:"size,omitempty"`
	Lines   []ChoiceLine `json:"lines,omitempty"`
	Zone    string       `json:"zone,omitempty"`
}

// ChoiceLine is one basket line chosen: the row and how many.
type ChoiceLine struct {
	ID  string `json:"id"`
	Qty int    `json:"qty"`
}

// Limits on a keep.
const (
	maxChoiceLines = 50
	maxKeepField   = 120
	maxSite        = 300
)

// LeftFor is how long a record lasts after its last keep, or after its
// follow-up; a restore link lasts as long.
const LeftFor = 7 * 24 * time.Hour

// KeepAAD binds a sealed keep to its form or action.
func KeepAAD(scope uuid.UUID) string { return "form keep\x00" + scope.String() }

// ErrBadKeep is a keep that does not read: a contact that is neither a
// Nigerian phone nor an email, or a choice out of bounds.
var ErrBadKeep = errors.New("forms: a keep is a contact and a product choice")

// ContactOf reads a keep's contact: a Nigerian mobile number as E.164, or
// an email, and which it is.
func (k Keep) ContactOf() (kind, value string, err error) {
	c := strings.TrimSpace(k.Contact)
	if strings.Contains(c, "@") {
		e, err := sender.NormaliseEmail(c)
		if err != nil {
			return "", "", ErrBadKeep
		}
		return "email", e, nil
	}
	p, err := sender.NormalisePhone(c, []string{"234"})
	if err != nil {
		return "", "", ErrBadKeep
	}
	return "phone", p, nil
}

// Check refuses a keep whose choice is out of bounds.
func (k Keep) Check() error {
	ch := k.Choice
	if ch.Package < 0 || ch.Package > 100 || len(ch.Lines) > maxChoiceLines || len(ch.Zone) > maxKeepField || len(ch.Size) > maxKeepField || len(k.Site) > maxSite ||
		(k.Page != nil && len(k.Page) != 32) {
		return ErrBadKeep
	}
	for _, l := range ch.Lines {
		if l.ID == "" || len(l.ID) > maxKeepField || l.Qty < 1 || l.Qty > 100 {
			return ErrBadKeep
		}
	}
	if _, _, err := k.ContactOf(); err != nil {
		return err
	}
	return nil
}

// Mask is a contact as the owner's list shows it: a phone's last four
// digits, an email's domain; never more.
func Mask(kind, value string) string {
	if kind == "email" {
		if _, d, ok := strings.Cut(value, "@"); ok {
			return "…@" + d
		}
		return "…"
	}
	if len(value) < 4 {
		return "…"
	}
	return "… " + value[len(value)-4:]
}

// ContactHash is a contact keyed as message's cross-tenant per-person
// limits key it.
func (s *Service) ContactHash(kind, value string) []byte {
	ch := "sms"
	if kind == "email" {
		ch = "email"
	}
	return s.Messages.ContactHash(ch, value)
}

type keepRow struct {
	id, tenant       uuid.UUID
	endpoint, action *uuid.UUID
	payload          []byte
	created          time.Time
}

// DrainKeeps takes keeps from their inbox and records each. One that
// does not open or read is dropped; one that fails is tried again once
// its claim lapses.
func (s *Service) DrainKeeps(ctx context.Context) error {
	if s.Messages == nil {
		return nil // no keyed hashes without the message hash key
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, tenant_id, endpoint_id, action_id, payload, created_at FROM taskiem_form_left_inbox_claim(50)`)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (keepRow, error) {
		var x keepRow
		return x, r.Scan(&x.id, &x.tenant, &x.endpoint, &x.action, &x.payload, &x.created)
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, x := range list {
		if err := s.recordKeep(ctx, x); err != nil {
			errs = append(errs, err)
			continue
		}
		if _, err := s.Pool.Exec(ctx, `SELECT taskiem_form_left_inbox_done($1)`, x.id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) recordKeep(ctx context.Context, x keepRow) error {
	scope := x.action
	if x.endpoint != nil {
		scope = x.endpoint
	}
	var k Keep
	if err := s.Keys.Open(KeepAAD(*scope), x.payload, &k); err != nil {
		s.log().Warn("forms: a keep that does not open; dropped")
		return nil //nolint:nilerr // dropped: it is taken off the inbox, never retried
	}
	if k.Check() != nil {
		return nil //nolint:nilerr // a keep that does not read is dropped
	}
	kind, value, _ := k.ContactOf()
	at := k.At
	if at.IsZero() || at.After(x.created.Add(time.Minute)) || at.Before(x.created.Add(-time.Hour)) {
		at = x.created
	}
	choice, err := json.Marshal(k.Choice)
	if err != nil {
		return err
	}
	var site *string
	if k.Site != "" {
		site = &k.Site
	}
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{x.tenant}, func(tx pgx.Tx) error {
		if k.Page != nil {
			// The page's order is recorded already: it was sent, not left.
			var sent bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM storefront_orders WHERE tenant_id = $1 AND status_hash = $2)`, x.tenant, k.Page).Scan(&sent); err != nil {
				return err
			}
			if sent {
				return nil
			}
		}
		env, err := s.Vault.SealTx(ctx, tx, x.tenant, kind, value)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `SELECT taskiem_form_left_put($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`, x.tenant, x.endpoint, x.action, k.Front, site, env,
			s.ContactHash(kind, value), kind, choice, at, k.Page, Mask(kind, value))
		return err
	})
}

// Sent tells the records that a contact sent the form (scope: its
// endpoint or action): within the wait the record is forgotten; after
// the follow-up it came back. It answers "deleted", "came_back" or "".
func (s *Service) Sent(ctx context.Context, tx pgx.Tx, tenant, scope uuid.UUID, contact string) (string, error) {
	if s.Messages == nil {
		return "", nil
	}
	kind, value, err := Keep{Contact: contact}.ContactOf()
	if err != nil {
		return "", nil //nolint:nilerr // a contact that keys nothing matches no record
	}
	var out string
	err = tx.QueryRow(ctx, `SELECT taskiem_form_left_sent($1, $2, $3)`, tenant, scope, s.ContactHash(kind, value)).Scan(&out)
	return out, err
}

// OrderSent is Sent for an order's buyer, by their phone and their email:
// storefront.Orders' FormSent, in the transaction that records the order.
func (s *Service) OrderSent(ctx context.Context, tx pgx.Tx, tenant, action uuid.UUID, phone, email string) error {
	for _, c := range []string{phone, email} {
		if strings.TrimSpace(c) == "" {
			continue
		}
		if _, err := s.Sent(ctx, tx, tenant, action, c); err != nil {
			return err
		}
	}
	return nil
}

// FollowedUp records a record's follow-up by channel and makes its
// restore link's row: the returned token (shown once, in the message)
// restores the choice, once, for 7 days. ok is false when it was followed
// up already.
func (s *Service) FollowedUp(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, channel string) (token string, ok bool, err error) {
	token = NewLinkToken()
	err = tx.QueryRow(ctx, `SELECT taskiem_form_left_followed($1, $2, $3, $4, $5)`, tenant, id, TokenHash(token), s.now(), channel).Scan(&ok)
	if err != nil || !ok {
		return "", false, err
	}
	return token, true, nil
}

// StopContact stops every follow-up to a contact (a phone or an email)
// from a business: an inbound STOP, where a channel brings one.
func (s *Service) StopContact(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, contact string) error {
	if s.Messages == nil {
		return nil
	}
	kind, value, err := Keep{Contact: contact}.ContactOf()
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `SELECT taskiem_form_followup_stop($1, $2)`, tenant, s.ContactHash(kind, value))
	return err
}

// PurgeLeft forgets records and restore links past their 7 days, keeps
// no api role took within a day, a contact's follow-ups after their week
// and the counts after 90 days.
func (s *Service) PurgeLeft(ctx context.Context) (int, error) {
	var n int
	if err := s.Pool.QueryRow(ctx, `SELECT taskiem_form_left_purge()`).Scan(&n); err != nil {
		return 0, fmt.Errorf("forms: purging forms left half-filled: %w", err)
	}
	return n, nil
}
