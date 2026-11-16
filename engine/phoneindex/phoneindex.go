// Package phoneindex is the phone index (docs/spec/taskiem-for-everyone.md
// §13, migration 00454): which storefront orders and bookings were made
// with a phone, across businesses, so a person whose phone is verified on
// their account finds their own orders ("Chat with {business} about this
// order", and later the Orders list on You).
//
// Every query in a business stays inside it, and a phone's personal-data
// subject is made with each business's own key, so it cannot find a phone
// across businesses. The index keeps instead, per order, a hash of the
// customer's phone keyed with a platform key: TASKIEM_MESSAGE_HASH_KEY (the
// key reach_contacts uses) narrowed to this one use, so the hashes match
// nothing else. No number, name or amount is kept.
//
// It is written in the transaction that makes the order (WriteTx, called
// by the storefront's orders and by the app's orders), filled once for
// orders made before it (Backfill, business by business, opening each
// order's sealed phone in its own business), and emptied of an order when
// the order is deleted, its customer erased or forgotten (the database
// does that). It is read only by taskiem_customer_phone_orders, which is
// given a person, never a phone: it reads their verified phone itself.
package phoneindex

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/phone"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/sender"
)

// Opener opens a sealed envelope in a business's scope (secrets.Vault).
type Opener interface {
	OpenTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, env map[string]any) (any, error)
}

// Index is the phone index.
type Index struct {
	Pool *pgxpool.Pool
	// Key is TASKIEM_MESSAGE_HASH_KEY (32 bytes).
	Key []byte
	// Vault opens orders' sealed customers, for the backfill.
	Vault Opener
	// Countries are the calling codes a phone without one is read in
	// (default 234), as storefront SMS are.
	Countries []string
}

// Ready reports whether the index can be written and read.
func (x *Index) Ready() bool { return x != nil && x.Pool != nil && len(x.Key) == 32 }

// indexKey is the key narrowed to the index: the database is given this,
// never the platform key.
func (x *Index) indexKey() []byte {
	m := hmac.New(sha256.New, x.Key)
	m.Write([]byte("taskiem phone index"))
	return m.Sum(nil)
}

// Hash is a phone's (E.164) hash in the index. The database makes the
// same from a person's verified phone in taskiem_customer_phone_orders.
func (x *Index) Hash(e164 string) []byte {
	h := sha256.New()
	h.Write(x.indexKey())
	h.Write([]byte("phone-index:" + e164))
	return h.Sum(nil)
}

// Normalise reads a phone typed on an order as E.164: by people's own rule
// (engine/phone), else as storefront SMS read it.
func (x *Index) Normalise(typed string) (string, bool) {
	if n, err := phone.Normalise(typed); err == nil {
		return n, true
	}
	c := x.Countries
	if len(c) == 0 {
		c = []string{"234"}
	}
	n, err := sender.NormalisePhone(typed, c)
	return n, err == nil
}

// WriteTx records that order (of tenant, in tx: the business's scope) was
// made with phone. A phone that cannot be read as a number records
// nothing.
func (x *Index) WriteTx(ctx context.Context, tx pgx.Tx, tenant, order uuid.UUID, typed string) error {
	if !x.Ready() {
		return nil
	}
	n, ok := x.Normalise(typed)
	if !ok {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO customer_phone_index (order_id, tenant_id, phone_hash) VALUES ($1, $2, $3)
		ON CONFLICT (order_id) DO UPDATE SET phone_hash = EXCLUDED.phone_hash`, order, tenant, x.Hash(n))
	return err
}

// backfillBatch is how many orders one transaction of the backfill reads.
const backfillBatch = 500

// Backfill indexes the orders made before the index, business by
// business, up to limit businesses a call: in each, every order's sealed
// phone is opened in that business's own scope and its hash written; the
// business is then marked done. Orders of the kind trade (a business's
// order from another business) and forgotten or erased customers are left
// out. It answers how many businesses it finished.
func (x *Index) Backfill(ctx context.Context, limit int) (int, error) {
	if !x.Ready() || x.Vault == nil {
		return 0, nil
	}
	rows, err := x.Pool.Query(ctx, `SELECT tenant_id FROM taskiem_phone_index_backfill_due($1)`, limit)
	if err != nil {
		return 0, err
	}
	due, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, err
	}
	done := 0
	var errs []error
	for _, t := range due {
		if err := x.backfillOne(ctx, t); err != nil {
			errs = append(errs, err)
			continue
		}
		done++
	}
	return done, errors.Join(errs...)
}

func (x *Index) backfillOne(ctx context.Context, tenant uuid.UUID) error {
	last := uuid.Nil
	indexed := 0
	for {
		n, next, wrote := 0, last, 0
		err := db.InTenantTx(ctx, x.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT o.id, o.customer FROM storefront_orders o
				WHERE o.tenant_id = $1 AND o.id > $2 AND o.kind <> 'trade' AND o.forgotten_at IS NULL
				  AND NOT EXISTS (SELECT 1 FROM customer_phone_index i WHERE i.order_id = o.id)
				ORDER BY o.id LIMIT $3`, tenant, last, backfillBatch)
			if err != nil {
				return err
			}
			type row struct {
				id  uuid.UUID
				env []byte
			}
			list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
				var w row
				return w, r.Scan(&w.id, &w.env)
			})
			if err != nil {
				return err
			}
			n = len(list)
			for _, w := range list {
				next = w.id
				var env map[string]any
				if json.Unmarshal(w.env, &env) != nil {
					continue
				}
				v, err := x.Vault.OpenTx(ctx, tx, tenant, env)
				if errors.Is(err, pii.ErrErased) {
					continue
				}
				if err != nil {
					return err
				}
				m, _ := v.(map[string]any)
				p, _ := m["phone"].(string)
				if p == "" {
					continue
				}
				if err := x.WriteTx(ctx, tx, tenant, w.id, p); err != nil {
					return err
				}
				wrote++
			}
			if n < backfillBatch {
				_, err = tx.Exec(ctx, `INSERT INTO phone_index_backfill (tenant_id, indexed) VALUES ($1, $2)
					ON CONFLICT (tenant_id) DO UPDATE SET indexed = phone_index_backfill.indexed + EXCLUDED.indexed, done_at = now()`, tenant, indexed+wrote)
			}
			return err
		})
		if err != nil {
			return err
		}
		indexed += wrote
		if n < backfillBatch {
			return nil
		}
		last = next
	}
}

// Order is one of a person's own orders, as the index lets them see it:
// nothing more than this.
type Order struct {
	Reference  string    `json:"reference"`
	State      string    `json:"state"`
	TotalKobo  int64     `json:"total_kobo"`
	MadeAt     time.Time `json:"made_at"`
	Business   string    `json:"business"`
	BusinessID uuid.UUID `json:"business_id"`
	// Waiting: made with the phone before it was verified on the account,
	// and not yet confirmed as the person's ("These are mine").
	Waiting bool `json:"waiting"`
}

// Orders are the person's own orders across businesses, newest first, up
// to limit (at most 200): those made with their verified phone.
func (x *Index) Orders(ctx context.Context, person uuid.UUID, limit int) ([]Order, error) {
	if !x.Ready() {
		return []Order{}, nil
	}
	rows, err := x.Pool.Query(ctx, `SELECT reference, state, total_kobo, made_at, business, business_id, waiting
		FROM taskiem_customer_phone_orders($1, $2, $3)`, person, x.indexKey(), limit)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Order])
	if out == nil {
		out = []Order{}
	}
	return out, err
}

// Confirm records that the orders made with the person's phone before it
// was on their account are theirs; false when they have no phone.
func (x *Index) Confirm(ctx context.Context, person uuid.UUID) (bool, error) {
	var ok bool
	err := x.Pool.QueryRow(ctx, `SELECT taskiem_customer_phone_orders_confirm($1)`, person).Scan(&ok)
	return ok, err
}
