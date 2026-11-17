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

// DoorKey is the narrowed index key, for the customer door's own check
// that the person has a confirmed order with a business (00462): the door
// makes the hash of the person's verified phone itself, so a wrong key
// matches nothing. Nil when the index is not set up.
func (x *Index) DoorKey() []byte {
	if !x.Ready() {
		return nil
	}
	return x.indexKey()
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
// out, and an order whose customer is erased or forgotten while the fill
// reads it is not indexed: the order and its customer's key are held
// (FOR SHARE) until the hash is written, so an erasure waits for it and
// then takes it out, or comes first and is seen. An order that cannot be
// opened is skipped and counted, not a failure; a business that fails is
// marked and tried again later, after those not yet tried. One replica at
// a time fills a business (an advisory lock). It answers how many
// businesses it finished.
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
		ok, err := x.backfillLocked(ctx, t)
		if err != nil {
			errs = append(errs, err)
			// Marked, so it waits its turn behind the others.
			if merr := db.InTenantTx(context.WithoutCancel(ctx), x.Pool, []uuid.UUID{t}, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `INSERT INTO phone_index_backfill (tenant_id, done_at, tries, failed_at) VALUES ($1, NULL, 1, now())
					ON CONFLICT (tenant_id) DO UPDATE SET tries = phone_index_backfill.tries + 1, failed_at = now()
					WHERE phone_index_backfill.done_at IS NULL`, t)
				return err
			}); merr != nil {
				errs = append(errs, merr)
			}
			continue
		}
		if ok {
			done++
		}
	}
	return done, errors.Join(errs...)
}

// backfillLocked fills one business while holding its advisory lock (a
// session lock on a connection of its own); false when another replica
// holds it.
func (x *Index) backfillLocked(ctx context.Context, tenant uuid.UUID) (bool, error) {
	conn, err := x.Pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended('phone-index-backfill:' || $1::text, 0))`, tenant).Scan(&got); err != nil {
		return false, err
	}
	if !got {
		return false, nil
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtextextended('phone-index-backfill:' || $1::text, 0))`, tenant)
	}()
	return true, x.backfillOne(ctx, tenant)
}

func (x *Index) backfillOne(ctx context.Context, tenant uuid.UUID) error {
	last := uuid.Nil
	indexed, skipped := 0, 0
	for {
		n, next, wrote, missed := 0, last, 0, 0
		err := db.InTenantTx(ctx, x.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			// Done meanwhile (another replica, before this one took the lock).
			var finished bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM phone_index_backfill WHERE tenant_id = $1 AND done_at IS NOT NULL)`, tenant).Scan(&finished); err != nil || finished {
				n = -1
				return err
			}
			// The orders, held until this commits, and only those whose
			// customer's key is there and held too: an erasure (shredding
			// the key) or retention (forgotten_at) waits for this, and its
			// trigger then removes what this wrote.
			rows, err := tx.Query(ctx, `SELECT o.id, o.customer FROM storefront_orders o
				JOIN subject_keys k ON k.tenant_id = o.tenant_id AND k.subject_id = o.subject_id AND k.shredded_at IS NULL
				WHERE o.tenant_id = $1 AND o.id > $2 AND o.kind <> 'trade' AND o.forgotten_at IS NULL
				  AND NOT EXISTS (SELECT 1 FROM customer_phone_index i WHERE i.order_id = o.id)
				ORDER BY o.id LIMIT $3 FOR SHARE OF o, k`, tenant, last, backfillBatch)
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
					missed++
					continue
				}
				// Opened in a savepoint: an order that cannot be opened is
				// skipped, and the batch goes on.
				var v any
				err := pgx.BeginFunc(ctx, tx, func(sp pgx.Tx) error {
					var err error
					v, err = x.Vault.OpenTx(ctx, sp, tenant, env)
					return err
				})
				if errors.Is(err, pii.ErrErased) {
					continue
				}
				if err != nil {
					if ctx.Err() != nil {
						return err
					}
					missed++
					continue
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
				_, err = tx.Exec(ctx, `INSERT INTO phone_index_backfill (tenant_id, indexed, skipped, done_at) VALUES ($1, $2, $3, now())
					ON CONFLICT (tenant_id) DO UPDATE SET indexed = phone_index_backfill.indexed + EXCLUDED.indexed,
					  skipped = phone_index_backfill.skipped + EXCLUDED.skipped, done_at = now(), failed_at = NULL`, tenant, indexed+wrote, skipped+missed)
			}
			return err
		})
		if err != nil {
			return err
		}
		if n < 0 {
			return nil
		}
		indexed += wrote
		skipped += missed
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
}

// Orders are the person's own confirmed orders across businesses (or of
// one business, when business is not uuid.Nil), newest first, up to limit
// (at most 200): those made with their verified phone. Orders made before
// the phone was on their account are left out until they say "These are
// mine" (Waiting counts them). Read in the person's own scope.
func (x *Index) Orders(ctx context.Context, person, business uuid.UUID, limit int) ([]Order, error) {
	out := []Order{}
	if !x.Ready() {
		return out, nil
	}
	var only *uuid.UUID
	if business != uuid.Nil {
		only = &business
	}
	err := db.InPersonTx(ctx, x.Pool, person, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT reference, state, total_kobo, made_at, business, business_id
			FROM taskiem_customer_phone_orders($1, $2, $3, $4)`, person, x.indexKey(), only, limit)
		if err != nil {
			return err
		}
		list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Order])
		if list != nil {
			out = list
		}
		return err
	})
	return out, err
}

// Waiting is how many orders made with the person's phone before it was
// on their account wait for "These are mine": a count, nothing of them.
func (x *Index) Waiting(ctx context.Context, person uuid.UUID) (int, error) {
	if !x.Ready() {
		return 0, nil
	}
	var n int
	err := db.InPersonTx(ctx, x.Pool, person, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT taskiem_customer_phone_orders_waiting($1, $2)`, person, x.indexKey()).Scan(&n)
	})
	return n, err
}

// Confirm records that the orders made with the person's phone before it
// was on their account are theirs; false when they have no phone.
func (x *Index) Confirm(ctx context.Context, person uuid.UUID) (bool, error) {
	var ok bool
	err := db.InPersonTx(ctx, x.Pool, person, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT taskiem_customer_phone_orders_confirm($1)`, person).Scan(&ok)
	})
	return ok, err
}
