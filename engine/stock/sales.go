package stock

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Sales (docs/spec/stock.md §5, K2): items sold at the counter, recorded by
// chat, voice note or the web. A sale moves stock and money together: the
// sale, a movement per item (reason sale) and, for cash, a takings entry
// in the money log, in one transaction. A sale paid by transfer or on the
// terminal waits for its money, which is matched to it once (MatchSales).

// How a sale was paid.
const (
	Cash     = "cash"
	Transfer = "transfer"
	POS      = "pos"
)

// SaleLine is one item of a sale.
type SaleLine struct {
	ItemID    string `json:"item_id"`
	Item      string `json:"item"`
	Quantity  int64  `json:"quantity"`
	Unit      string `json:"unit,omitempty"`
	PriceKobo int64  `json:"price_kobo"`
	LineKobo  int64  `json:"line_kobo"`
	// CostKobo is the item's cost price when sold, kept on its movement
	// (K4).
	CostKobo *int64 `json:"cost_kobo,omitempty"`
}

// SaleIn is a sale to record. AmountKobo nil prices it from its lines.
// Entry names a terminal's sale the owner gives its items to (POS only).
type SaleIn struct {
	Lines      []SaleLine
	AmountKobo *int64
	PaidHow    string
	Note       string
	Actor      string
	Key        string
	SoldAt     time.Time
	Entry      *uuid.UUID
}

// CounterSale is a recorded sale.
type CounterSale struct {
	ID         uuid.UUID  `json:"id"`
	Reference  string     `json:"reference"`
	Lines      []SaleLine `json:"lines"`
	AmountKobo int64      `json:"amount_kobo"`
	PaidHow    string     `json:"paid_how"`
	// Status is recorded (cash), waiting (its money not yet seen) or
	// matched.
	Status     string     `json:"status"`
	EntryID    *uuid.UUID `json:"entry_id,omitempty"`
	MatchedHow string     `json:"matched_how,omitempty"`
	Note       string     `json:"note,omitempty"`
	Actor      string     `json:"by"`
	SoldAt     time.Time  `json:"sold_at"`
	// Short is the items sold beyond the stock there was, once its
	// movements landed.
	Short []string `json:"short,omitempty"`
}

var (
	// ErrSaleInvalid refuses a sale that cannot be recorded, in plain words.
	ErrSaleInvalid = errors.New("stock: invalid sale")
	// ErrEntryTaken: the terminal's sale already counts for a sale, an
	// invoice or an order.
	ErrEntryTaken = errors.New("stock: that money counts for something else already")
)

// MaxSaleLines bounds a sale's lines.
const MaxSaleLines = 50

func (in *SaleIn) check() error {
	if len(in.Lines) == 0 || len(in.Lines) > MaxSaleLines {
		return fmt.Errorf("%w: a sale has 1 to %d items", ErrSaleInvalid, MaxSaleLines)
	}
	switch in.PaidHow {
	case Cash, Transfer, POS:
	default:
		return fmt.Errorf("%w: paid in cash, by transfer or on the terminal (pos)", ErrSaleInvalid)
	}
	if in.Entry != nil && in.PaidHow != POS {
		return fmt.Errorf("%w: only a terminal's sale is given its items", ErrSaleInvalid)
	}
	var total int64
	each := map[string]int64{}
	for i := range in.Lines {
		l := &in.Lines[i]
		l.Item = strings.TrimSpace(l.Item)
		if l.ItemID == "" || l.Item == "" || l.Quantity <= 0 || l.Quantity > MaxDelta || l.PriceKobo < 0 || l.PriceKobo > 1e10 {
			return fmt.Errorf("%w: each item is sold at least once, at a price of 0 or more", ErrSaleInvalid)
		}
		if each[l.ItemID] += l.Quantity; each[l.ItemID] > MaxDelta {
			return fmt.Errorf("%w: at most %d of an item in a sale", ErrSaleInvalid, MaxDelta)
		}
		l.LineKobo = l.PriceKobo * l.Quantity
		total += l.LineKobo
	}
	if in.AmountKobo == nil {
		in.AmountKobo = &total
	}
	if *in.AmountKobo < 0 || *in.AmountKobo > 1e11 {
		return fmt.Errorf("%w: the amount", ErrSaleInvalid)
	}
	if *in.AmountKobo == 0 {
		return fmt.Errorf("%w: a sale has an amount; something given away is an adjustment", ErrSaleInvalid)
	}
	in.Note = strings.TrimSpace(in.Note)
	if len([]rune(in.Note)) > 200 {
		return fmt.Errorf("%w: the note is up to 200 characters", ErrSaleInvalid)
	}
	if in.Actor == "" || in.Key == "" {
		return fmt.Errorf("%w: who sold it, and an idempotency key", ErrSaleInvalid)
	}
	in.Key = FitKey(in.Key)
	if in.SoldAt.IsZero() {
		in.SoldAt = time.Now()
	}
	return nil
}

const saleCols = `id, reference, lines, amount_kobo, paid_how, status, entry_id, COALESCE(matched_how, ''), COALESCE(note, ''), actor, sold_at`

func scanSale(r pgx.Row) (CounterSale, error) {
	var x CounterSale
	var lines []byte
	err := r.Scan(&x.ID, &x.Reference, &lines, &x.AmountKobo, &x.PaidHow, &x.Status, &x.EntryID, &x.MatchedHow, &x.Note, &x.Actor, &x.SoldAt)
	if err == nil {
		err = json.Unmarshal(lines, &x.Lines)
	}
	return x, err
}

// saleReference is a new sale's reference: S- and six of the
// storefront's unambiguous letters and digits.
func saleReference() string { return reference("S") }

// reference is a fresh reference with a prefix: S-7QX4KD, P-K2M9PA.
func reference(prefix string) string {
	const alphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return prefix + "-" + string(b)
}

// SaleWords says a sale's items in a few words: "3 bags Rice, 1 Indomie".
func SaleWords(lines []SaleLine) string {
	parts := make([]string, len(lines))
	for i, l := range lines {
		parts[i] = (Item{Unit: l.Unit}).Quantity(l.Quantity) + " " + l.Item
	}
	return strings.Join(parts, ", ")
}

// RecordSale records a sale, its movements and, for cash, its takings
// entry in log, in one transaction; then sends the movements and, for a
// sale waiting for its money, looks for it. The same key again answers the
// sale it made.
func (s *Stock) RecordSale(ctx context.Context, tenant uuid.UUID, in SaleIn, log *moneylog.Log) (CounterSale, error) {
	if err := in.check(); err != nil {
		return CounterSale{}, err
	}
	var x CounterSale
	var moves []uuid.UUID
	fresh := false
	err := s.inTx(ctx, tenant, func(tx pgx.Tx) error {
		moves, fresh = nil, false
		var err error
		x, err = scanSale(tx.QueryRow(ctx, `SELECT `+saleCols+` FROM stock_sales WHERE tenant_id = $1 AND key = $2`, tenant, in.Key))
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		fresh = true
		lines, _ := json.Marshal(in.Lines)
		status := "waiting"
		var entry *uuid.UUID
		matchedHow := ""
		if in.PaidHow == Cash {
			id, _, err := log.RecordTx(ctx, tx, tenant, moneylog.Entry{Direction: "in", Kind: "cash", Status: "settled", Channel: "cash",
				Reference: "sale:" + in.Key, AmountKobo: in.AmountKobo, OccurredAt: &in.SoldAt, Source: "entered",
				Description: cut("Sale: "+SaleWords(in.Lines), 300), EnteredBy: in.Actor})
			if err != nil {
				return err
			}
			// The takings are the sale's: the invoice matcher never offers
			// them for an invoice or order (taskiem_money_entry_counted
			// counts them too, 00265).
			if _, err := tx.Exec(ctx, `INSERT INTO money_match_state (entry_id, tenant_id, state) VALUES ($1, $2, 'matched')
				ON CONFLICT (entry_id) DO UPDATE SET state = 'matched', considered_at = now()`, id, tenant); err != nil {
				return err
			}
			status, entry = "recorded", &id
		}
		if in.Entry != nil {
			if err := takeEntry(ctx, tx, tenant, *in.Entry, *in.AmountKobo, POS); err != nil {
				return err
			}
			entry, status, matchedHow = in.Entry, "matched", "owner"
		}
		for attempt := 0; ; attempt++ {
			sp, err := tx.Begin(ctx)
			if err != nil {
				return err
			}
			ref := saleReference()
			x, err = scanSale(sp.QueryRow(ctx, `INSERT INTO stock_sales (id, tenant_id, reference, lines, amount_kobo, paid_how, status, entry_id, matched_how, matched_at,
					note, actor, key, sold_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, ''), CASE WHEN $9 = '' THEN NULL ELSE now() END, NULLIF($10, ''), $11, $12, $13)
				RETURNING `+saleCols, uuid.Must(uuid.NewV7()), tenant, ref, lines, *in.AmountKobo, in.PaidHow, status, entry, matchedHow, in.Note, in.Actor,
				in.Key, in.SoldAt))
			var pe *pgconn.PgError
			if errors.As(err, &pe) && pe.Code == "23505" && strings.Contains(pe.ConstraintName, "reference") && attempt < 5 {
				_ = sp.Rollback(ctx)
				continue
			}
			if err != nil {
				_ = sp.Rollback(ctx)
				return err
			}
			if err := sp.Commit(ctx); err != nil {
				return err
			}
			break
		}
		// One movement per item: an item on two lines ("2 rice, 3 rice")
		// moves by both.
		var order []string
		each := map[string]*SaleLine{}
		for _, l := range x.Lines {
			if e := each[l.ItemID]; e != nil {
				e.Quantity += l.Quantity
				continue
			}
			l := l
			each[l.ItemID] = &l
			order = append(order, l.ItemID)
		}
		for _, id := range order {
			l := each[id]
			d := -l.Quantity
			m, err := RecordTx(ctx, tx, tenant, Move{ItemID: l.ItemID, Item: l.Item, Reason: Sale, Delta: &d, Reference: x.Reference, Actor: in.Actor,
				Key: "sale:" + x.ID.String() + ":" + l.ItemID, CostKobo: l.CostKobo})
			if err != nil {
				return err
			}
			moves = append(moves, m.ID)
		}
		return nil
	})
	if err != nil {
		return CounterSale{}, err
	}
	if !fresh {
		return s.withShort(ctx, tenant, x)
	}
	for _, id := range moves {
		if _, err := s.Apply(ctx, tenant, id); err != nil {
			break // recorded: the sweep sends it again
		}
	}
	if x.Status == "waiting" {
		if _, err := s.MatchSales(ctx, tenant); err != nil {
			return x, err
		}
		if got, err := s.GetSale(ctx, tenant, x.ID); err == nil {
			x = got
		}
	}
	return s.withShort(ctx, tenant, x)
}

// withShort adds the items sold beyond the stock there was.
func (s *Stock) withShort(ctx context.Context, tenant uuid.UUID, x CounterSale) (CounterSale, error) {
	err := s.inTx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT item FROM stock_movements WHERE tenant_id = $1 AND key LIKE $2 AND short > 0 ORDER BY item`,
			tenant, "sale:"+x.ID.String()+":%")
		if err != nil {
			return err
		}
		x.Short, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	return x, err
}

// GetSale reads one sale.
func (s *Stock) GetSale(ctx context.Context, tenant, id uuid.UUID) (CounterSale, error) {
	var x CounterSale
	err := s.inTx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		x, err = scanSale(tx.QueryRow(ctx, `SELECT `+saleCols+` FROM stock_sales WHERE tenant_id = $1 AND id = $2`, tenant, id))
		return err
	})
	return x, err
}

// SalesTx lists a business's sales between from and to, newest first.
func SalesTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, from, to time.Time) ([]CounterSale, error) {
	rows, err := tx.Query(ctx, `SELECT `+saleCols+` FROM stock_sales WHERE tenant_id = $1 AND sold_at >= $2 AND sold_at < $3 ORDER BY sold_at DESC, id DESC LIMIT 500`,
		tenant, from, to)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (CounterSale, error) { return scanSale(r) })
}

// countableSQL is what an entry counts for: what the payer sent
// (moneylog.CountableKobo).
const countableSQL = `(COALESCE(e.amount_kobo, 0) + CASE WHEN e.net THEN COALESCE(e.fee_kobo, 0) ELSE 0 END)`

// kindsFor is the entry kinds a sale's money comes as.
func kindsFor(paidHow string) []string {
	if paidHow == POS {
		return []string{"pos_sale"}
	}
	return []string{"payment", "account_credit", "statement"}
}

// freeSQL says an entry counts for nothing yet: no sale, invoice or order.
const freeSQL = `NOT EXISTS (SELECT 1 FROM stock_sales x WHERE x.entry_id = e.id)
	AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments p WHERE p.entry_id = e.id AND p.undoes IS NULL
	                AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id))
	AND NOT EXISTS (SELECT 1 FROM storefront_order_payments p WHERE p.entry_id = e.id AND p.undoes IS NULL
	                AND NOT EXISTS (SELECT 1 FROM storefront_order_payments u WHERE u.undoes = p.id))`

// takeEntry checks a terminal's sale may be given to a sale: money in,
// settled, of the kind, the amount, and counting for nothing yet.
func takeEntry(ctx context.Context, tx pgx.Tx, tenant, entry uuid.UUID, amount int64, paidHow string) error {
	if err := moneylog.LockEntry(ctx, tx, entry); err != nil {
		return err
	}
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM money_entries e WHERE e.tenant_id = $1 AND e.id = $2 AND e.direction = 'in' AND e.status = 'settled'
		AND e.kind = ANY ($3) AND `+countableSQL+` = $4 AND `+freeSQL+`)`, tenant, entry, kindsFor(paidHow), amount).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return ErrEntryTaken
	}
	return matchedTx(ctx, tx, tenant, entry)
}

// entryFree takes an entry's lock (the one invoices and orders count it
// under, moneylog.LockEntry) and says nothing counts it yet: no sale,
// invoice or order.
func entryFree(ctx context.Context, tx pgx.Tx, entry uuid.UUID) (bool, error) {
	if err := moneylog.LockEntry(ctx, tx, entry); err != nil {
		return false, err
	}
	var free bool
	err := tx.QueryRow(ctx, `SELECT taskiem_money_entry_counted($1) = 0`, entry).Scan(&free)
	return free, err
}

// matchedTx marks an entry a sale took as matched, and closes what the
// invoice matcher offered it for, so it is neither offered again nor
// confirmed from an old offer.
func matchedTx(ctx context.Context, tx pgx.Tx, tenant, entry uuid.UUID) error {
	if _, err := tx.Exec(ctx, `INSERT INTO money_match_state (entry_id, tenant_id, state) VALUES ($1, $2, 'matched')
		ON CONFLICT (entry_id) DO UPDATE SET state = 'matched', considered_at = now()`, entry, tenant); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE money_suggestions SET status = 'closed', decided_at = now()
		WHERE tenant_id = $1 AND entry_id = $2 AND status = 'open'`, tenant, entry)
	return err
}

// MatchWindow is how far from the sale its money may be: the money a rail
// reports for a counter sale comes within hours.
const MatchWindow = 12 * time.Hour

// MatchSales matches a business's sales waiting for their money to the
// money the rails reported: a transfer in, or a terminal's sale, of the
// sale's amount, counting for nothing yet, within MatchWindow of the sale;
// the one naming the sale's reference first, else the nearest in time.
// Each entry counts for one sale. It answers how many it matched.
func (s *Stock) MatchSales(ctx context.Context, tenant uuid.UUID) (int, error) {
	n := 0
	err := s.inTx(ctx, tenant, func(tx pgx.Tx) error {
		n = 0
		rows, err := tx.Query(ctx, `SELECT `+saleCols+` FROM stock_sales WHERE tenant_id = $1 AND status = 'waiting' ORDER BY sold_at, id FOR UPDATE SKIP LOCKED`, tenant)
		if err != nil {
			return err
		}
		waiting, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (CounterSale, error) { return scanSale(r) })
		if err != nil {
			return err
		}
		for _, x := range waiting {
			var entry uuid.UUID
			var how string
			err := tx.QueryRow(ctx, `SELECT e.id, CASE WHEN position(lower($6) IN lower(e.reference || ' ' || COALESCE(e.description, '') || ' ' || COALESCE(e.note, ''))) > 0
					THEN 'reference' ELSE 'amount' END
				FROM money_entries e WHERE e.tenant_id = $1 AND e.direction = 'in' AND e.status = 'settled' AND e.kind = ANY ($2)
				  AND `+countableSQL+` = $3 AND COALESCE(e.occurred_at, e.recorded_at) BETWEEN $4 AND $5 AND `+freeSQL+`
				ORDER BY (position(lower($6) IN lower(e.reference || ' ' || COALESCE(e.description, '') || ' ' || COALESCE(e.note, ''))) > 0) DESC,
				  abs(extract(epoch FROM COALESCE(e.occurred_at, e.recorded_at) - $7)), e.id
				LIMIT 1 FOR UPDATE OF e SKIP LOCKED`,
				tenant, kindsFor(x.PaidHow), x.AmountKobo, x.SoldAt.Add(-MatchWindow), x.SoldAt.Add(MatchWindow), x.Reference, x.SoldAt).Scan(&entry, &how)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			sp, err := tx.Begin(ctx)
			if err != nil {
				return err
			}
			// Under the lock invoices and orders count it under: nothing
			// counts it yet.
			free, err := entryFree(ctx, sp, entry)
			if err != nil || !free {
				_ = sp.Rollback(ctx)
				if err != nil {
					return err
				}
				continue
			}
			_, err = sp.Exec(ctx, `UPDATE stock_sales SET status = 'matched', entry_id = $3, matched_how = $4, matched_at = now() WHERE tenant_id = $1 AND id = $2`,
				tenant, x.ID, entry, how)
			var pe *pgconn.PgError
			if errors.As(err, &pe) && pe.Code == "23505" {
				_ = sp.Rollback(ctx) // the entry went to another sale meanwhile
				continue
			}
			if err != nil {
				_ = sp.Rollback(ctx)
				return err
			}
			if err := matchedTx(ctx, sp, tenant, entry); err != nil {
				_ = sp.Rollback(ctx)
				return err
			}
			if err := sp.Commit(ctx); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

// Unsold is a terminal's sale with no sale recorded for it: money with no
// items, listed in the evening for the owner to say what was sold.
type Unsold struct {
	EntryID    uuid.UUID `json:"entry_id"`
	AmountKobo int64     `json:"amount_kobo"`
	Channel    string    `json:"channel"`
	At         time.Time `json:"at"`
}

// UnsoldTx lists the terminal's sales between from and to that count for
// no sale (nor an invoice or order).
func UnsoldTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, from, to time.Time) ([]Unsold, error) {
	rows, err := tx.Query(ctx, `SELECT e.id, `+countableSQL+`, e.channel, COALESCE(e.occurred_at, e.recorded_at) FROM money_entries e
		WHERE e.tenant_id = $1 AND e.direction = 'in' AND e.status = 'settled' AND e.kind = 'pos_sale'
		  AND COALESCE(e.occurred_at, e.recorded_at) >= $2 AND COALESCE(e.occurred_at, e.recorded_at) < $3 AND `+freeSQL+`
		ORDER BY COALESCE(e.occurred_at, e.recorded_at), e.id LIMIT 200`, tenant, from, to)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Unsold, error) {
		var u Unsold
		return u, r.Scan(&u.EntryID, &u.AmountKobo, &u.Channel, &u.At)
	})
}

// Day is a day's sales added up: the total, by how paid, how many sales,
// the top items, and the terminal's sales with no items.
type Day struct {
	Day       string           `json:"day"`
	TotalKobo int64            `json:"total_kobo"`
	ByHow     map[string]int64 `json:"by_how"`
	Count     int              `json:"count"`
	// Waiting is the sales by transfer or on the terminal whose money is
	// not yet seen.
	Waiting int           `json:"waiting"`
	Top     []TopItem     `json:"top"`
	Unsold  []Unsold      `json:"unsold"`
	Sales   []CounterSale `json:"sales"`
	Low     []LowSince    `json:"low,omitempty"`
}

// TopItem is an item's sales on a day.
type TopItem struct {
	Item      string `json:"item"`
	Unit      string `json:"unit,omitempty"`
	Quantity  int64  `json:"quantity"`
	TotalKobo int64  `json:"total_kobo"`
}

// LowSince is an item that went at or below its reorder level that day.
type LowSince struct {
	Item  string `json:"item"`
	Count int64  `json:"count"`
}

// DayTx adds up a business's sales on the Lagos day day.
func DayTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, day time.Time) (Day, error) {
	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, storefront.Lagos)
	to := from.AddDate(0, 0, 1)
	d := Day{Day: from.Format("2006-01-02"), ByHow: map[string]int64{Cash: 0, Transfer: 0, POS: 0}, Top: []TopItem{}}
	var err error
	if d.Sales, err = SalesTx(ctx, tx, tenant, from, to); err != nil {
		return d, err
	}
	top := map[string]*TopItem{}
	var order []string
	for _, x := range d.Sales {
		d.TotalKobo += x.AmountKobo
		d.ByHow[x.PaidHow] += x.AmountKobo
		d.Count++
		if x.Status == "waiting" {
			d.Waiting++
		}
		for _, l := range x.Lines {
			t := top[l.ItemID]
			if t == nil {
				t = &TopItem{Item: l.Item, Unit: l.Unit}
				top[l.ItemID] = t
				order = append(order, l.ItemID)
			}
			t.Quantity += l.Quantity
			t.TotalKobo += l.LineKobo
		}
	}
	for _, id := range order {
		d.Top = append(d.Top, *top[id])
	}
	sortTop(d.Top)
	if len(d.Top) > 5 {
		d.Top = d.Top[:5]
	}
	if d.Unsold, err = UnsoldTx(ctx, tx, tenant, from, to); err != nil {
		return d, err
	}
	if d.Sales == nil {
		d.Sales = []CounterSale{}
	}
	if d.Unsold == nil {
		d.Unsold = []Unsold{}
	}
	return d, nil
}

func sortTop(t []TopItem) {
	for i := 1; i < len(t); i++ {
		for j := i; j > 0 && (t[j].TotalKobo > t[j-1].TotalKobo || t[j].TotalKobo == t[j-1].TotalKobo && t[j].Quantity > t[j-1].Quantity); j-- {
			t[j], t[j-1] = t[j-1], t[j]
		}
	}
}
