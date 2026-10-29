package stock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// Counts and shrinkage (docs/spec/stock.md §7, K4): what was on the shelf,
// item by item, against what Taskiem counted; each difference a count
// movement naming the count, valued at the item's cost price. A count of
// every item closes a counting period. Shrinkage is what came in, what was
// sold, and what was lost to counts and adjustments, at cost.

// ErrCount: a count Taskiem cannot take.
var ErrCount = errors.New("stock: that count cannot be taken")

// CountLine is one item counted.
type CountLine struct {
	ItemID string `json:"item_id"`
	Item   string `json:"item"`
	Unit   string `json:"unit,omitempty"`
	// Expected is the count Taskiem had; nil when the row held none.
	Expected *int64 `json:"expected"`
	Counted  int64  `json:"counted"`
	CostKobo *int64 `json:"cost_kobo,omitempty"`
}

// Lost is how many fewer were counted than expected (0 when none).
func (l CountLine) Lost() int64 {
	if l.Expected == nil || l.Counted >= *l.Expected {
		return 0
	}
	return *l.Expected - l.Counted
}

// StockCount is a count taken.
type StockCount struct {
	ID        uuid.UUID   `json:"id"`
	Reference string      `json:"reference"`
	Lines     []CountLine `json:"lines"`
	// Full: every item was counted, closing a counting period.
	Full      bool      `json:"full"`
	Reason    string    `json:"reason,omitempty"`
	LostKobo  int64     `json:"lost_kobo"`
	Actor     string    `json:"by"`
	CountedAt time.Time `json:"counted_at"`
	// Changed is how many items' counts it changed.
	Changed int `json:"changed"`
}

// CountIn is a count to record.
type CountIn struct {
	Lines  []CountLine
	Full   bool
	Reason string
	Actor  string
	Key    string
}

// MaxCountLines is the most items one count holds (the catalogue's most).
const MaxCountLines = 1000

func scanCount(r pgx.Row) (StockCount, error) {
	var c StockCount
	var lines []byte
	err := r.Scan(&c.ID, &c.Reference, &lines, &c.Full, &c.Reason, &c.LostKobo, &c.Actor, &c.CountedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrCount
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(lines, &c.Lines); err != nil {
		return c, err
	}
	for _, l := range c.Lines {
		if l.Expected == nil || *l.Expected != l.Counted {
			c.Changed++
		}
	}
	return c, nil
}

const countCols = `id, reference, lines, full_count, reason, lost_kobo, actor, counted_at`

// CountTx records a count: the count, and a count movement (setting the
// count) for each item whose count differs, naming the count, with the
// reason as its note and the item's cost. The same key is the same count.
func CountTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, in CountIn) (StockCount, []Movement, error) {
	if in.Key == "" {
		return StockCount{}, nil, fmt.Errorf("%w: an idempotency key", ErrCount)
	}
	in.Key = FitKey(in.Key)
	if c, err := scanCount(tx.QueryRow(ctx, `SELECT `+countCols+` FROM stock_counts WHERE tenant_id = $1 AND key = $2`, tenant, in.Key)); err == nil {
		return c, nil, nil
	} else if !errors.Is(err, ErrCount) {
		return StockCount{}, nil, err
	}
	if len(in.Lines) == 0 || len(in.Lines) > MaxCountLines {
		return StockCount{}, nil, fmt.Errorf("%w: a count has 1 to %d items", ErrCount, MaxCountLines)
	}
	in.Reason = cut(strings.TrimSpace(in.Reason), 200)
	seen := map[string]bool{}
	var lost int64
	for i := range in.Lines {
		l := &in.Lines[i]
		l.Item = cut(strings.TrimSpace(l.Item), 200)
		if l.ItemID == "" || l.Item == "" {
			return StockCount{}, nil, fmt.Errorf("%w: each line names an item", ErrCount)
		}
		if seen[l.ItemID] {
			return StockCount{}, nil, fmt.Errorf("%w: %s is counted twice", ErrCount, l.Item)
		}
		seen[l.ItemID] = true
		if l.Counted < 0 || l.Counted > MaxDelta {
			return StockCount{}, nil, fmt.Errorf("%w: how many %s is a whole number, 0 or more", ErrCount, l.Item)
		}
		if l.CostKobo != nil {
			lost += l.Lost() * *l.CostKobo
		}
	}
	ref := reference("C")
	id := uuid.Must(uuid.NewV7())
	raw, _ := json.Marshal(in.Lines)
	if _, err := tx.Exec(ctx, `INSERT INTO stock_counts (id, tenant_id, reference, lines, full_count, reason, lost_kobo, actor, key) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, tenant, ref, raw, in.Full, in.Reason, lost, in.Actor, in.Key); err != nil {
		return StockCount{}, nil, err
	}
	note := in.Reason
	if note == "" {
		note = "counted"
	}
	var moves []Movement
	for _, l := range in.Lines {
		if l.Expected != nil && *l.Expected == l.Counted {
			continue
		}
		n := l.Counted
		m, err := RecordTx(ctx, tx, tenant, Move{ItemID: l.ItemID, Item: l.Item, Reason: Count, SetTo: &n, Note: note, Reference: ref, Actor: in.Actor,
			Key: "count:" + in.Key + ":" + l.ItemID, CostKobo: l.CostKobo})
		if err != nil {
			return StockCount{}, nil, err
		}
		moves = append(moves, m)
	}
	if err := stockAudit(ctx, tx, tenant, "stock.count", ref, map[string]any{"count_id": id, "items": len(in.Lines), "changed": len(moves),
		"full": in.Full, "lost_kobo": lost, "by": in.Actor}); err != nil {
		return StockCount{}, nil, err
	}
	c, err := scanCount(tx.QueryRow(ctx, `SELECT `+countCols+` FROM stock_counts WHERE id = $1`, id))
	return c, moves, err
}

// RecordCount records a count, then sends its movements to the table.
func (s *Stock) RecordCount(ctx context.Context, tenant uuid.UUID, in CountIn) (StockCount, error) {
	var c StockCount
	var moves []Movement
	if err := s.inTx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		c, moves, err = CountTx(ctx, tx, tenant, in)
		return err
	}); err != nil {
		return c, err
	}
	for _, m := range moves {
		_, _ = s.Apply(ctx, tenant, m.ID)
	}
	return c, nil
}

// CountsTx lists the latest counts.
func CountsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, limit int) ([]StockCount, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := tx.Query(ctx, `SELECT `+countCols+` FROM stock_counts WHERE tenant_id = $1 ORDER BY counted_at DESC, id DESC LIMIT $2`, tenant, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (StockCount, error) { return scanCount(r) })
}

// --- shrinkage ---

// CostSQL is a movement m's item's cost in SQL: when it moved, else the
// last one known; 0 when none was ever given.
const CostSQL = `COALESCE(m.cost_kobo, (SELECT c.cost_kobo FROM stock_movements c WHERE c.tenant_id = m.tenant_id AND c.item_id = m.item_id
	AND c.cost_kobo IS NOT NULL ORDER BY c.created_at DESC LIMIT 1), 0)`

// ShrinkItem is one item's movement over a period.
type ShrinkItem struct {
	ItemID   string `json:"item_id"`
	Item     string `json:"item"`
	Received int64  `json:"received"`
	Sold     int64  `json:"sold"`
	// Lost is what counts and adjustments took off; LostKobo its worth at
	// the cost it had (items with no cost: units only).
	Lost     int64 `json:"lost"`
	LostKobo int64 `json:"lost_kobo"`
}

// Shrink is a period's stock: received against sold and lost, at cost.
type Shrink struct {
	From     time.Time    `json:"from"`
	To       time.Time    `json:"to"`
	Received int64        `json:"received"`
	Sold     int64        `json:"sold"`
	Lost     int64        `json:"lost"`
	LostKobo int64        `json:"lost_kobo"`
	Items    []ShrinkItem `json:"items"`
}

// ShrinkTx adds up the movements that landed in [from, to), each item's,
// the largest loss first.
func ShrinkTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, from, to time.Time) (Shrink, error) {
	out := Shrink{From: from, To: to, Items: []ShrinkItem{}}
	rows, err := tx.Query(ctx, `SELECT m.item_id, (array_agg(m.item ORDER BY m.done_at DESC))[1],
			COALESCE(sum(m.change) FILTER (WHERE m.reason = 'received' AND m.change > 0), 0),
			COALESCE(-sum(m.change) FILTER (WHERE m.reason IN ('sale', 'order') AND m.change < 0), 0),
			COALESCE(-sum(m.change) FILTER (WHERE m.reason IN ('count', 'adjustment') AND m.change < 0), 0),
			COALESCE(-sum(m.change * `+CostSQL+`) FILTER (WHERE m.reason IN ('count', 'adjustment') AND m.change < 0), 0)
		FROM stock_movements m WHERE m.tenant_id = $1 AND m.status = 'done' AND m.done_at >= $2 AND m.done_at < $3 AND m.change IS NOT NULL
		GROUP BY m.item_id`, tenant, from, to)
	if err != nil {
		return out, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ShrinkItem, error) {
		var x ShrinkItem
		return x, r.Scan(&x.ItemID, &x.Item, &x.Received, &x.Sold, &x.Lost, &x.LostKobo)
	})
	if err != nil {
		return out, err
	}
	for _, x := range list {
		out.Received += x.Received
		out.Sold += x.Sold
		out.Lost += x.Lost
		out.LostKobo += x.LostKobo
		out.Items = append(out.Items, x)
	}
	sortShrink(out.Items)
	return out, nil
}

// Worst is the item with the largest loss, nil when nothing was lost.
func (s Shrink) Worst() *ShrinkItem {
	if len(s.Items) == 0 || s.Items[0].Lost == 0 {
		return nil
	}
	return &s.Items[0]
}

func sortShrink(items []ShrinkItem) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && worse(items[j], items[j-1]); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

func worse(a, b ShrinkItem) bool {
	if a.LostKobo != b.LostKobo {
		return a.LostKobo > b.LostKobo
	}
	if a.Lost != b.Lost {
		return a.Lost > b.Lost
	}
	return a.Item < b.Item
}

// MonthlyText is the monthly email's stock section: what came in, was
// sold and was lost, and the item lost most; "" when nothing moved.
func MonthlyText(sh Shrink) string {
	if sh.Received == 0 && sh.Sold == 0 && sh.Lost == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Stock\n")
	fmt.Fprintf(&b, "Received %d, sold %d, lost %d to counts and adjustments", sh.Received, sh.Sold, sh.Lost)
	if sh.LostKobo > 0 {
		fmt.Fprintf(&b, " (%s at cost)", storefront.FormatNaira(sh.LostKobo))
	}
	b.WriteString(".\n")
	if w := sh.Worst(); w != nil {
		fmt.Fprintf(&b, "Lost most: %s, %d", w.Item, w.Lost)
		if w.LostKobo > 0 {
			fmt.Fprintf(&b, " (%s)", storefront.FormatNaira(w.LostKobo))
		}
		b.WriteString(".\n")
	}
	return b.String()
}

// MonthlyMore is moneydigest's MonthlyMore: the month's stock section.
func MonthlyMore(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, from, to time.Time) (string, error) {
	sh, err := ShrinkTx(ctx, tx, tenant, from, to)
	if err != nil {
		return "", err
	}
	return MonthlyText(sh), nil
}
