// Package stock is a shop's stock kept by chat (docs/spec/stock.md,
// decision 0057): the catalogue table in the business's Xenibase project
// holds the count, and every change to it is a movement Taskiem records
// first, in the transaction of what caused it (an order accepted, a sale,
// goods received, an adjustment, a count).
//
// The table is reached over Xenibase's API, so no transaction spans it and
// Taskiem's database. A movement is therefore pending until the table
// holds it: the count read and the count to send are recorded on the
// movement before the change is sent under a key of the movement and that
// count, and the change is made only while the table still holds the count
// read (the storefront's rule for orders, lifted here). A retried send
// replays the first answer, so a movement lands once; when the count moved
// first, it is read again. Done, the movement's change is what landed, and
// an item's count is the sum of its movements' changes.
//
// The table is the owner's: anyone with table.edit may change a count by
// hand. A count that differs from the sum of the item's movements while
// no movement of Taskiem's is on its way is recorded as an "outside"
// movement, so the count is always explained.
package stock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
)

// Reasons a movement is made for.
const (
	Order      = "order"
	Sale       = "sale"
	Received   = "received"
	Adjustment = "adjustment"
	Count      = "count"
	Outside    = "outside"
)

var (
	// ErrInvalid refuses a movement that cannot be recorded.
	ErrInvalid = errors.New("stock: invalid")
	// ErrNotTracked: the business's catalogue keeps no stock.
	ErrNotTracked = errors.New("stock: the catalogue keeps no stock")
)

// Table reads and changes the stock column of a business's catalogue table
// (storefront.XenibaseStock).
type Table interface {
	// Read returns a row's stock; tracked is false when the catalogue maps
	// no stock column, or the row is gone or holds no count.
	Read(ctx context.Context, tenant uuid.UUID, row string) (n int64, tracked bool, err error)
	// Change sets a row's stock to to only while it is still from; applied
	// is false when it had moved. The same key again replays the first
	// answer.
	Change(ctx context.Context, tenant uuid.UUID, row string, from, to int64, key string) (applied bool, err error)
}

// Stock records movements and sends them to the table. Times it compares
// are the database's (ReadMark), never the application's.
type Stock struct {
	Pool  *pgxpool.Pool
	Table Table
}

// Ready reports whether stock can be kept here.
func (s *Stock) Ready() bool { return s != nil && s.Pool != nil && s.Table != nil }

// Move is a movement asked for: a change (Delta) or a count (SetTo).
type Move struct {
	ItemID string
	Item   string
	Reason string
	Delta  *int64
	SetTo  *int64
	Note   string
	// Reference names what it came from: an order's reference, a sale, a
	// purchase order, a count.
	Reference string
	Actor     string
	// Key makes recording it once: the same key is the same movement.
	Key string
}

// Movement is a recorded movement.
type Movement struct {
	ID     uuid.UUID `json:"id"`
	ItemID string    `json:"item_id"`
	Item   string    `json:"item"`
	Reason string    `json:"reason"`
	// Status is pending (on its way to the table), done, or void (the item
	// is gone, or the catalogue keeps no stock).
	Status string `json:"status"`
	Delta  *int64 `json:"delta,omitempty"`
	SetTo  *int64 `json:"set_to,omitempty"`
	// Before and After are the count the table held and the count it was
	// set to; Change is what landed (After - Before).
	Before *int64 `json:"before"`
	After  *int64 `json:"after"`
	Change *int64 `json:"change"`
	// Short is what a decrease asked for beyond the stock there was.
	Short     int64      `json:"short,omitempty"`
	Note      string     `json:"note,omitempty"`
	Reference string     `json:"reference,omitempty"`
	Actor     string     `json:"by"`
	Error     string     `json:"error,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	DoneAt    *time.Time `json:"done_at"`
}

// MaxDelta bounds a change asked for at once.
const MaxDelta = 1_000_000

func (m Move) check() error {
	switch m.Reason {
	case Order, Sale, Received, Adjustment, Count:
	default:
		return fmt.Errorf("%w: a movement is for an order, a sale, goods received, an adjustment or a count", ErrInvalid)
	}
	if (m.Delta == nil) == (m.SetTo == nil) {
		return fmt.Errorf("%w: a movement is a change or a count", ErrInvalid)
	}
	if m.Reason == Count && m.SetTo == nil || m.Reason != Count && m.SetTo != nil {
		return fmt.Errorf("%w: only a count sets the stock", ErrInvalid)
	}
	if m.Delta != nil && (*m.Delta == 0 || *m.Delta > MaxDelta || *m.Delta < -MaxDelta) {
		return fmt.Errorf("%w: a change is between -%d and %d, and not 0", ErrInvalid, MaxDelta, MaxDelta)
	}
	if m.SetTo != nil && (*m.SetTo < 0 || *m.SetTo > MaxDelta) {
		return fmt.Errorf("%w: a count is between 0 and %d", ErrInvalid, MaxDelta)
	}
	if m.ItemID == "" || len(m.ItemID) > 100 || strings.TrimSpace(m.Item) == "" || len(m.Item) > 200 {
		return fmt.Errorf("%w: the item", ErrInvalid)
	}
	if len([]rune(m.Note)) > 200 || len(m.Reference) > 100 || m.Actor == "" || m.Key == "" || len(m.Key) > 200 {
		return fmt.Errorf("%w: the note is up to 200 characters", ErrInvalid)
	}
	return nil
}

const movementCols = `id, item_id, item, reason, status, delta, set_to, count_before, count_after, change, short, COALESCE(note, ''),
	COALESCE(reference, ''), actor, COALESCE(error, ''), created_at, done_at`

func scanMovement(r pgx.Row) (Movement, error) {
	var m Movement
	err := r.Scan(&m.ID, &m.ItemID, &m.Item, &m.Reason, &m.Status, &m.Delta, &m.SetTo, &m.Before, &m.After, &m.Change, &m.Short, &m.Note,
		&m.Reference, &m.Actor, &m.Error, &m.CreatedAt, &m.DoneAt)
	return m, err
}

// RecordTx records a movement in tx, the transaction of what caused it;
// the same key again answers the movement it made. Apply sends it.
func RecordTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, mv Move) (Movement, error) {
	mv.Item = strings.TrimSpace(mv.Item)
	mv.Note = strings.TrimSpace(mv.Note)
	if err := mv.check(); err != nil {
		return Movement{}, err
	}
	m, err := scanMovement(tx.QueryRow(ctx, `INSERT INTO stock_movements (id, tenant_id, item_id, item, reason, delta, set_to, status, note, reference, actor, key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', NULLIF($8, ''), NULLIF($9, ''), $10, $11)
		ON CONFLICT (tenant_id, key) DO NOTHING RETURNING `+movementCols,
		uuid.Must(uuid.NewV7()), tenant, mv.ItemID, mv.Item, mv.Reason, mv.Delta, mv.SetTo, mv.Note, mv.Reference, mv.Actor, mv.Key))
	if errors.Is(err, pgx.ErrNoRows) {
		return scanMovement(tx.QueryRow(ctx, `SELECT `+movementCols+` FROM stock_movements WHERE tenant_id = $1 AND key = $2`, tenant, mv.Key))
	}
	return m, err
}

// GetTx reads one movement.
func GetTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (Movement, error) {
	return scanMovement(tx.QueryRow(ctx, `SELECT `+movementCols+` FROM stock_movements WHERE tenant_id = $1 AND id = $2`, tenant, id))
}

// Attempts bounds how often Apply reads the count again when other
// changes keep moving it; the sweep tries again later.
const Attempts = 5

// ErrMoving: the count kept changing while a movement was sent.
var ErrMoving = errors.New("stock: the count kept changing; it is sent again shortly")

// Apply sends a movement to the table until it lands (or is void), and
// answers it. An error leaves it pending for the sweep, which sends the
// same change again.
func (s *Stock) Apply(ctx context.Context, tenant, id uuid.UUID) (Movement, error) {
	for attempt := 0; attempt < Attempts; attempt++ {
		m, err := s.get(ctx, tenant, id)
		if err != nil || m.Status != "pending" {
			return m, err
		}
		from, to := m.Before, m.After
		if from == nil {
			if m, err = s.claim(ctx, tenant, m); err != nil || m.Status != "pending" {
				return m, err
			}
			from, to = m.Before, m.After
			if from == nil { // claimed by another sender meanwhile: theirs is read next time round
				continue
			}
		}
		applied, err := s.Table.Change(ctx, tenant, m.ItemID, *from, *to, changeKey(m.ID, *from))
		if err != nil {
			s.failed(ctx, tenant, m.ID, err)
			return m, err
		}
		if applied {
			return s.done(ctx, tenant, m.ID, *from)
		}
		// The count moved first: read it again.
		if err := s.inTx(ctx, tenant, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE stock_movements SET count_before = NULL, count_after = NULL, short = 0
				WHERE tenant_id = $1 AND id = $2 AND status = 'pending' AND count_before = $3`, tenant, m.ID, *from)
			return err
		}); err != nil {
			return m, err
		}
	}
	return s.get(ctx, tenant, id)
}

func (s *Stock) inTx(ctx context.Context, tenant uuid.UUID, fn func(pgx.Tx) error) error {
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, fn)
}

func (s *Stock) get(ctx context.Context, tenant, id uuid.UUID) (Movement, error) {
	var m Movement
	err := s.inTx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		m, err = GetTx(ctx, tx, tenant, id)
		return err
	})
	return m, err
}

// claim reads the table's count and records what the movement sends: the
// count read and the count it becomes. A difference from the item's
// movements that no change of Taskiem's on its way explains is recorded
// first as an outside movement.
func (s *Stock) claim(ctx context.Context, tenant uuid.UUID, m Movement) (Movement, error) {
	readAt, err := s.ReadMark(ctx)
	if err != nil {
		return m, err
	}
	n, tracked, err := s.Table.Read(ctx, tenant, m.ItemID)
	if err != nil {
		s.failed(ctx, tenant, m.ID, err)
		return m, err
	}
	err = s.inTx(ctx, tenant, func(tx pgx.Tx) error {
		if err := lockItem(ctx, tx, tenant, m.ItemID); err != nil {
			return err
		}
		cur, err := scanMovement(tx.QueryRow(ctx, `SELECT `+movementCols+` FROM stock_movements WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenant, m.ID))
		if err != nil {
			return err
		}
		if cur.Status != "pending" || cur.Before != nil {
			m = cur
			return nil
		}
		if !tracked {
			m, err = scanMovement(tx.QueryRow(ctx, `UPDATE stock_movements SET status = 'void', error = 'The item is gone, or the catalogue keeps no stock'
				WHERE tenant_id = $1 AND id = $2 RETURNING `+movementCols, tenant, m.ID))
			return err
		}
		if _, err := outsideTx(ctx, tx, tenant, m.ItemID, m.Item, n, readAt, m.ID); err != nil {
			return err
		}
		to, short := target(n, cur)
		m, err = scanMovement(tx.QueryRow(ctx, `UPDATE stock_movements SET count_before = $3, count_after = $4, short = $5, claimed_at = now(),
			attempts = attempts + 1, error = NULL WHERE tenant_id = $1 AND id = $2 RETURNING `+movementCols, tenant, m.ID, n, to, short))
		return err
	})
	return m, err
}

// target is the count a movement sets n to, and what a decrease asked
// for beyond the stock there was. A decrease never takes the count below
// zero, nor further below it when the table already held less.
func target(n int64, m Movement) (to, short int64) {
	if m.SetTo != nil {
		return *m.SetTo, 0
	}
	d := *m.Delta
	switch {
	case d >= 0 || n+d >= 0:
		return n + d, 0
	case n <= 0:
		return n, -d
	}
	return 0, -(n + d)
}

func (s *Stock) done(ctx context.Context, tenant, id uuid.UUID, from int64) (Movement, error) {
	var m Movement
	err := s.inTx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		m, err = scanMovement(tx.QueryRow(ctx, `UPDATE stock_movements SET status = 'done', change = count_after - count_before, done_at = now(), error = NULL
			WHERE tenant_id = $1 AND id = $2 AND status = 'pending' AND count_before = $3 RETURNING `+movementCols, tenant, id, from))
		if errors.Is(err, pgx.ErrNoRows) { // another sender marked it
			m, err = GetTx(ctx, tx, tenant, id)
			return err
		}
		if err != nil {
			return err
		}
		return copyCount(ctx, tx, tenant, m.ItemID, *m.After)
	})
	return m, err
}

// copyCount puts an item's new count into the storefront's catalogue copy,
// so the page and forms show it at once rather than at the next refresh.
func copyCount(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, item string, n int64) error {
	_, err := tx.Exec(ctx, `UPDATE storefront_catalogue SET item = jsonb_set(item, '{stock}', to_jsonb(GREATEST($3::bigint, 0)))
		WHERE tenant_id = $1 AND row_id = $2 AND item ? 'stock'`, tenant, item, n)
	return err
}

func (s *Stock) failed(ctx context.Context, tenant, id uuid.UUID, cause error) {
	msg := cause.Error()
	if len(msg) > 300 {
		msg = msg[:300]
	}
	_ = s.inTx(ctx, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE stock_movements SET error = $3, claimed_at = now(), attempts = attempts + 1
			WHERE tenant_id = $1 AND id = $2 AND status = 'pending'`, tenant, id, msg)
		return err
	})
}

// changeKey is the Idempotency-Key of one change: the movement and the
// count it changes from.
func changeKey(id uuid.UUID, from int64) string {
	h := sha256.Sum256([]byte(id.String() + "\x00" + strconv.FormatInt(from, 10)))
	return "tsk-st-" + hex.EncodeToString(h[:16])
}

// lockItem serialises, per item, the claims and the outside checks.
func lockItem(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, item string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('stock ' || $1::text || ' ' || $2, 0))`, tenant, item)
	return err
}

// outsideTx records an outside movement when the table's count n, read at
// readAt, is not the sum of the item's movements and nothing of Taskiem's
// explains it: no other movement on its way to the table, and none landed
// or claimed since the read. Call it under lockItem. except is a movement
// not counted as on its way (the one being claimed).
func outsideTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, itemID, item string, n int64, readAt time.Time, except uuid.UUID) (bool, error) {
	var sum int64
	var busy bool
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(change) FILTER (WHERE status = 'done'), 0),
			COALESCE(bool_or(id <> $4 AND (status = 'pending' AND count_before IS NOT NULL OR done_at >= $3 OR claimed_at >= $3)), false)
		FROM stock_movements WHERE tenant_id = $1 AND item_id = $2`, tenant, itemID, readAt, except).Scan(&sum, &busy); err != nil {
		return false, err
	}
	if busy || sum == n {
		return false, nil
	}
	note := "Changed in the table"
	var any bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM stock_movements WHERE tenant_id = $1 AND item_id = $2 AND status = 'done')`,
		tenant, itemID).Scan(&any); err != nil {
		return false, err
	}
	if !any {
		note = "First count seen in the table"
	}
	id := uuid.Must(uuid.NewV7())
	_, err := tx.Exec(ctx, `INSERT INTO stock_movements (id, tenant_id, item_id, item, reason, delta, status, count_before, count_after, change,
			note, actor, key, done_at)
		VALUES ($1, $2, $3, $4, 'outside', $5, 'done', $6, $7, $5, $8, 'table', $9, now())`,
		id, tenant, itemID, cut(item, 200), n-sum, sum, n, note, "outside:"+id.String())
	return err == nil, err
}

// ReadMark is the database's clock, taken just before the table is read:
// what Outside and the claims compare movements' times with, so the
// application's clock never enters it.
func (s *Stock) ReadMark(ctx context.Context) (time.Time, error) {
	var t time.Time
	err := s.Pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&t)
	return t, err
}

// Seen is an item's count as a refresh of the catalogue read it.
type Seen struct {
	ItemID string
	Item   string
	Count  int64
}

// Outside records, for each item a refresh read at readAt (ReadMark), an outside
// movement where its count is not explained by its movements (outsideTx).
// It answers how many it recorded.
func (s *Stock) Outside(ctx context.Context, tenant uuid.UUID, seen []Seen, readAt time.Time) (int, error) {
	n := 0
	err := s.inTx(ctx, tenant, func(tx pgx.Tx) error {
		n = 0
		for _, it := range seen {
			if err := lockItem(ctx, tx, tenant, it.ItemID); err != nil {
				return err
			}
			ok, err := outsideTx(ctx, tx, tenant, it.ItemID, it.Item, it.Count, readAt, uuid.Nil)
			if err != nil {
				return err
			}
			if ok {
				n++
			}
		}
		return nil
	})
	return n, err
}

// LedgerTx is the sum of an item's done movements: its count, as Taskiem
// knows it.
func LedgerTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, item string) (int64, error) {
	var n int64
	err := tx.QueryRow(ctx, `SELECT COALESCE(sum(change), 0) FROM stock_movements WHERE tenant_id = $1 AND item_id = $2 AND status = 'done'`,
		tenant, item).Scan(&n)
	return n, err
}

// ApplyPending sends a business's movements still on their way, oldest
// first: those never sent, and those whose send failed or was cut off,
// waiting a minute more after each attempt (up to an hour).
func (s *Stock) ApplyPending(ctx context.Context, tenant uuid.UUID) error {
	var ids []uuid.UUID
	err := s.inTx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM stock_movements WHERE tenant_id = $1 AND status = 'pending'
			AND COALESCE(claimed_at, '-infinity') <= now() - make_interval(mins => LEAST(GREATEST(attempts, 1), 60))
			ORDER BY created_at, id LIMIT 200`, tenant)
		if err != nil {
			return err
		}
		ids, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		return err
	})
	if err != nil {
		return err
	}
	var first error
	for _, id := range ids {
		if _, err := s.Apply(ctx, tenant, id); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Filter narrows a list of movements.
type Filter struct {
	ItemID string
	Reason string
	Since  *time.Time
	Until  *time.Time
	Limit  int
}

// ListTx lists movements, the latest to land first (pending ones by when
// they were made).
func ListTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, f Filter) ([]Movement, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	rows, err := tx.Query(ctx, `SELECT `+movementCols+` FROM stock_movements WHERE tenant_id = $1
		AND ($2 = '' OR item_id = $2) AND ($3 = '' OR reason = $3) AND status <> 'void'
		AND ($4::timestamptz IS NULL OR created_at >= $4) AND ($5::timestamptz IS NULL OR created_at < $5)
		ORDER BY COALESCE(done_at, created_at) DESC, id DESC LIMIT $6`, tenant, f.ItemID, f.Reason, f.Since, f.Until, f.Limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Movement, error) { return scanMovement(r) })
}

func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
