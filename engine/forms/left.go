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
// public host sends a keep when a buyer has typed a contact into a form
// that follows up and not sent it, carrying only the contact and the
// product choice, sealed with the public key for the api role. Here the
// api role takes keeps from their inbox, seals the contact under the
// person in the vault, keys it as message's per-person limits are, and
// keeps one record per form and contact (form_left), through narrow
// functions only. A submission from the same contact within the wait
// forgets the record; one after the follow-up marks it came back. Records
// last 7 days after their last keep, or after their follow-up.

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
	if ch.Package < 0 || ch.Package > 100 || len(ch.Lines) > maxChoiceLines || len(ch.Zone) > maxKeepField || len(ch.Size) > maxKeepField || len(k.Site) > maxSite {
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
		env, err := s.Vault.SealTx(ctx, tx, x.tenant, kind, value)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `SELECT taskiem_form_left_put($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, x.tenant, x.endpoint, x.action, k.Front, site, env,
			s.ContactHash(kind, value), kind, choice, at)
		return err
	})
}

// Sent tells the records that a contact sent the form (scope: its
// endpoint or action): within the wait the record is forgotten; after
// the follow-up it came back. It answers "deleted", "came_back" or "".
func (s *Service) Sent(ctx context.Context, tx pgx.Tx, tenant, scope uuid.UUID, contact string) (string, error) {
	kind, value, err := Keep{Contact: contact}.ContactOf()
	if err != nil {
		return "", nil //nolint:nilerr // a contact that keys nothing matches no record
	}
	var out string
	err = tx.QueryRow(ctx, `SELECT taskiem_form_left_sent($1, $2, $3)`, tenant, scope, s.ContactHash(kind, value)).Scan(&out)
	return out, err
}

// FollowedUp records a record's follow-up and makes its restore link's
// row: the returned token (shown once, in the message) restores the
// choice, once, for 7 days. ok is false when it was followed up already.
func (s *Service) FollowedUp(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (token string, ok bool, err error) {
	token = NewLinkToken()
	err = tx.QueryRow(ctx, `SELECT taskiem_form_left_followed($1, $2, $3, $4)`, tenant, id, TokenHash(token), s.now()).Scan(&ok)
	if err != nil || !ok {
		return "", false, err
	}
	return token, true, nil
}

// PurgeLeft forgets records and restore links past their 7 days, and
// keeps no api role took within a day.
func (s *Service) PurgeLeft(ctx context.Context) (int, error) {
	var n int
	if err := s.Pool.QueryRow(ctx, `SELECT taskiem_form_left_purge()`).Scan(&n); err != nil {
		return 0, fmt.Errorf("forms: purging forms left half-filled: %w", err)
	}
	return n, nil
}
