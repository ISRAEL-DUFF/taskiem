package matching

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/pii"
)

// Payer memory (decision 0046 point 6; docs/spec/schools.md 7.3;
// docs/matching.md#payer-memory). When a person confirms or matches money
// to a customer's invoice, the payer is remembered for that customer: the
// payer's name as the rail sent it and, where the rail gave it, the paying
// account. The next payment from them suggests that customer's invoices
// first ("this payer paid for this customer before"), strongly enough to
// be matched on its own when the business has automatic matching on.
//
// Payers are personal data. A payer is found by the pseudonym sealing
// derives from their normalised name or account (payer_key: keyed by the
// business's own key, so it says nothing without it), and what the rail
// sent is kept sealed. A remembered payer can be forgotten.

// Payer is a payer remembered for a customer.
type Payer struct {
	ID   uuid.UUID `json:"id"`
	Kind string    `json:"kind"` // name or account
	// Payer is the name or account as the rail sent it (masked by the API
	// unless revealed).
	Payer     string    `json:"payer"`
	Times     int       `json:"times"`
	FirstBy   string    `json:"first_by"`
	CreatedAt time.Time `json:"created_at"`
	LastAt    time.Time `json:"last_at"`
}

// normalName is a payer's name as remembered: its words, lower case, in
// order ("OBI ADA C" and "Ada Obi C." are one payer).
func normalName(name string) string {
	words := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return !unicode.IsLetter(r) })
	sort.Strings(words)
	return strings.Join(words, " ")
}

// normalAccount is a paying account as remembered: its digits.
func normalAccount(account string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, account)
}

// payerKeys are the keys a payer is remembered under, by kind, with what
// the rail sent sealed: none for a payer with no name or account, or
// without a cipher.
func (m *Matcher) payerKeys(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, name, account string, seal bool) (map[string]string, map[string]map[string]any, error) {
	keys, sealed := map[string]string{}, map[string]map[string]any{}
	if m.PII == nil {
		return keys, sealed, nil
	}
	for kind, v := range map[string][2]string{"name": {normalName(name), name}, "account": {normalAccount(account), account}} {
		if len(v[0]) < 3 {
			continue
		}
		env, err := m.PII.SealTx(ctx, tx, tenant, "payer_"+kind, v[0])
		if err != nil {
			return nil, nil, err
		}
		key, _ := env["subject"].(string)
		if key == "" {
			continue
		}
		keys[kind] = key
		if !seal {
			continue
		}
		if sealed[kind], err = m.PII.SealTx(ctx, tx, tenant, "name", v[1]); err != nil {
			return nil, nil, err
		}
	}
	return keys, sealed, nil
}

// remembered says which customers this payer paid for before: by their
// paying account (surer) or their name.
func (m *Matcher) remembered(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, name, account string) (map[string]string, error) {
	out := map[string]string{}
	keys, _, err := m.payerKeys(ctx, tx, tenant, name, account, false)
	if err != nil || len(keys) == 0 {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT kind, customer_id FROM money_payers WHERE tenant_id = $1 AND ((kind = 'name' AND payer_key = $2) OR (kind = 'account' AND payer_key = $3))`,
		tenant, keys["name"], keys["account"])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, customer string
		if err := rows.Scan(&kind, &customer); err != nil {
			return nil, err
		}
		if out[customer] != "account" {
			out[customer] = kind
		}
	}
	return out, rows.Err()
}

// remember remembers an entry's payer for customers a person matched it
// to.
func (m *Matcher) remember(ctx context.Context, tx pgx.Tx, tenant, entryID uuid.UUID, customers []string, by string) error {
	if m.PII == nil || len(customers) == 0 {
		return nil
	}
	var env map[string]any
	if err := tx.QueryRow(ctx, `SELECT payer FROM money_entries WHERE tenant_id = $1 AND id = $2`, tenant, entryID).Scan(&env); err != nil || env == nil {
		return err
	}
	v, err := m.PII.OpenTx(ctx, tx, tenant, env)
	if errors.Is(err, pii.ErrErased) {
		return nil
	}
	if err != nil {
		return err
	}
	p, _ := v.(map[string]any)
	name, _ := p["name"].(string)
	account, _ := p["account"].(string)
	keys, sealed, err := m.payerKeys(ctx, tx, tenant, name, account, true)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, c := range customers {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		for kind, key := range keys {
			raw, _ := json.Marshal(sealed[kind])
			if _, err := tx.Exec(ctx, `INSERT INTO money_payers (id, tenant_id, kind, payer_key, payer, customer_id, first_by, created_at, last_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
				ON CONFLICT (tenant_id, kind, payer_key, customer_id) DO UPDATE SET payer = EXCLUDED.payer, times = money_payers.times + 1, last_at = EXCLUDED.last_at`,
				uuid.New(), tenant, kind, key, raw, c, by, m.now()); err != nil {
				return err
			}
		}
	}
	return nil
}

// PayersOf lists the payers remembered for a customer (Invoice.CustomerID),
// most recent first, opened.
func PayersOf(ctx context.Context, tx pgx.Tx, c pii.Cipher, tenant uuid.UUID, customer string) ([]Payer, error) {
	rows, err := tx.Query(ctx, `SELECT id, kind, payer, times, first_by, created_at, last_at FROM money_payers WHERE tenant_id = $1 AND customer_id = $2
		ORDER BY last_at DESC, id LIMIT 50`, tenant, customer)
	if err != nil {
		return nil, err
	}
	type sealed struct {
		p   Payer
		env map[string]any
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (sealed, error) {
		var x sealed
		return x, r.Scan(&x.p.ID, &x.p.Kind, &x.env, &x.p.Times, &x.p.FirstBy, &x.p.CreatedAt, &x.p.LastAt)
	})
	if err != nil {
		return nil, err
	}
	out := make([]Payer, 0, len(list))
	for _, x := range list {
		x.p.Payer = pii.Erased
		if c != nil {
			v, err := c.OpenTx(ctx, tx, tenant, x.env)
			if err != nil && !errors.Is(err, pii.ErrErased) {
				return nil, err
			}
			if s, ok := v.(string); ok {
				x.p.Payer = s
			}
		}
		out = append(out, x.p)
	}
	return out, nil
}

// Forget forgets a remembered payer: their next payment is suggested as
// anyone's would be.
func Forget(ctx context.Context, tx pgx.Tx, tenant, payer uuid.UUID) error {
	tag, err := tx.Exec(ctx, `DELETE FROM money_payers WHERE tenant_id = $1 AND id = $2`, tenant, payer)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return invoicing.ErrNotFound
	}
	return nil
}
