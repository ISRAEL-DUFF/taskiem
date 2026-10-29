package stock_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
)

func (w *world) log() *moneylog.Log { return &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault} }

func (w *world) sale(in stock.SaleIn) stock.CounterSale {
	w.t.Helper()
	if in.Actor == "" {
		in.Actor = "user:owner"
	}
	if in.Key == "" {
		in.Key = uuid.NewString()
	}
	x, err := w.s.RecordSale(context.Background(), w.env.Tenant, in, w.log())
	if err != nil {
		w.t.Fatal(err)
	}
	return x
}

// money records an entry as a rail would report it.
func (w *world) money(kind, ref string, kobo int64, at time.Time) uuid.UUID {
	w.t.Helper()
	ch := "paystack/main"
	if kind == "pos_sale" {
		ch = "moniepointpos/shop"
	}
	id, _, err := w.log().Record(context.Background(), w.env.Tenant, moneylog.Entry{Direction: "in", Kind: kind, Status: "settled", Channel: ch,
		Reference: ref, AmountKobo: &kobo, OccurredAt: &at, Source: "event", Event: kind + ".ok"})
	if err != nil {
		w.t.Fatal(err)
	}
	return id
}

// A cash sale is the sale, its movements and a takings entry in the money
// log; the stock goes down; a sale beyond the stock is recorded, short.
func TestCashSale(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, map[string]int64{"rice": 0, "indomie": 0})
	w.apply(w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Received, Delta: n(5)}).ID)
	w.apply(w.record(stock.Move{ItemID: "indomie", Item: "Indomie", Reason: stock.Received, Delta: n(1)}).ID)

	x := w.sale(stock.SaleIn{PaidHow: stock.Cash, Key: "sale-1", Lines: []stock.SaleLine{
		{ItemID: "rice", Item: "Rice", Quantity: 3, Unit: "bag", PriceKobo: 1500000},
		{ItemID: "indomie", Item: "Indomie", Quantity: 2, PriceKobo: 50000},
	}})
	if x.Status != "recorded" || x.AmountKobo != 4600000 || x.EntryID == nil || len(x.Short) != 1 || x.Short[0] != "Indomie" || x.Reference[:2] != "S-" {
		t.Fatalf("sale: %+v", x)
	}
	if w.tb.count("rice") != 2 || w.tb.count("indomie") != 0 || w.ledger("rice") != 2 {
		t.Fatalf("stock: rice %d, indomie %d", w.tb.count("rice"), w.tb.count("indomie"))
	}
	var kind, desc string
	var amount int64
	w.in(func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT kind, amount_kobo, description FROM money_entries WHERE id = $1`, *x.EntryID).Scan(&kind, &amount, &desc)
	})
	if kind != "cash" || amount != 4600000 || desc != "Sale: 3 bags Rice, 2 Indomie" {
		t.Fatalf("takings: %s %d %q", kind, amount, desc)
	}
	// The same key is the same sale: nothing moves twice.
	if again := w.sale(stock.SaleIn{PaidHow: stock.Cash, Key: "sale-1", Lines: []stock.SaleLine{{ItemID: "rice", Item: "Rice", Quantity: 3, PriceKobo: 1500000}}}); again.ID != x.ID || w.tb.count("rice") != 2 {
		t.Fatalf("again: %+v, rice %d", again, w.tb.count("rice"))
	}
	if m := w.movements("rice")[0]; m.Reason != stock.Sale || m.Reference != x.Reference || *m.Change != -3 {
		t.Fatalf("movement: %+v", m)
	}
	// A sale is never changed.
	err := w.env.DB.Admin.QueryRow(ctx, `UPDATE stock_sales SET amount_kobo = 1 WHERE id = $1 RETURNING id`, x.ID).Scan(new(uuid.UUID))
	if err == nil {
		t.Fatal("a sale was changed")
	}

	// Refused: no amount; an unknown way of paying; a gift.
	for _, in := range []stock.SaleIn{
		{PaidHow: "cheque", Lines: []stock.SaleLine{{ItemID: "rice", Item: "Rice", Quantity: 1, PriceKobo: 100}}},
		{PaidHow: stock.Cash, Lines: []stock.SaleLine{{ItemID: "rice", Item: "Rice", Quantity: 1}}},
		{PaidHow: stock.Cash},
	} {
		in.Actor, in.Key = "user:owner", uuid.NewString()
		if _, err := w.s.RecordSale(ctx, w.env.Tenant, in, w.log()); !errors.Is(err, stock.ErrSaleInvalid) {
			t.Errorf("%+v: %v", in, err)
		}
	}
}

// A sale paid by transfer waits for its money; the transfer that comes
// is matched to it once, the one naming its reference first.
func TestTransferAndPOSSalesMatch(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, map[string]int64{"rice": 0})
	w.apply(w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Received, Delta: n(20)}).ID)
	now := time.Now()
	line := []stock.SaleLine{{ItemID: "rice", Item: "Rice", Quantity: 1, PriceKobo: 4500000}}

	a := w.sale(stock.SaleIn{PaidHow: stock.Transfer, Lines: line, SoldAt: now})
	b := w.sale(stock.SaleIn{PaidHow: stock.Transfer, Lines: line, SoldAt: now.Add(time.Minute)})
	if a.Status != "waiting" || b.Status != "waiting" || w.tb.count("rice") != 18 {
		t.Fatalf("waiting: %+v %+v", a, b)
	}
	// Two transfers of the amount: one naming b's reference, one plain.
	plain := w.money("payment", "PSK-1", 4500000, now.Add(2*time.Minute))
	named := w.money("payment", "PSK-2 "+b.Reference, 4500000, now.Add(3*time.Minute))
	_ = w.money("payment", "PSK-3", 4400000, now) // another amount: no match
	if got, err := w.s.MatchSales(ctx, w.env.Tenant); err != nil || got != 2 {
		t.Fatalf("matched %d, %v", got, err)
	}
	a, _ = w.s.GetSale(ctx, w.env.Tenant, a.ID)
	b, _ = w.s.GetSale(ctx, w.env.Tenant, b.ID)
	if *b.EntryID != named || b.MatchedHow != "reference" || *a.EntryID != plain || a.MatchedHow != "amount" {
		t.Fatalf("matches: a %+v, b %+v", a, b)
	}
	if got, _ := w.s.MatchSales(ctx, w.env.Tenant); got != 0 {
		t.Fatal("matched twice")
	}

	// A terminal's sale: matched when its money is already there.
	pos := w.money("pos_sale", "TRM-1", 900000, now)
	c := w.sale(stock.SaleIn{PaidHow: stock.POS, Lines: []stock.SaleLine{{ItemID: "rice", Item: "Rice", Quantity: 2, PriceKobo: 450000}}, SoldAt: now})
	if c.Status != "matched" || *c.EntryID != pos {
		t.Fatalf("pos: %+v", c)
	}
	// A terminal's sale with no sale: listed, then given its items.
	lone := w.money("pos_sale", "TRM-2", 450000, now.Add(-time.Hour))
	var unsold []stock.Unsold
	w.in(func(tx pgx.Tx) error {
		var err error
		unsold, err = stock.UnsoldTx(ctx, tx, w.env.Tenant, now.Add(-2*time.Hour), now.Add(time.Hour))
		return err
	})
	if len(unsold) != 1 || unsold[0].EntryID != lone || unsold[0].AmountKobo != 450000 {
		t.Fatalf("unsold: %+v", unsold)
	}
	given := w.sale(stock.SaleIn{PaidHow: stock.POS, Entry: &lone, Lines: []stock.SaleLine{{ItemID: "rice", Item: "Rice", Quantity: 1, PriceKobo: 450000}}})
	if given.Status != "matched" || given.MatchedHow != "owner" || *given.EntryID != lone {
		t.Fatalf("given: %+v", given)
	}
	// The same money for another sale: refused.
	if _, err := w.s.RecordSale(ctx, w.env.Tenant, stock.SaleIn{PaidHow: stock.POS, Entry: &lone, Actor: "user:owner", Key: uuid.NewString(),
		Lines: []stock.SaleLine{{ItemID: "rice", Item: "Rice", Quantity: 1, PriceKobo: 450000}}}, w.log()); !errors.Is(err, stock.ErrEntryTaken) {
		t.Fatalf("taken: %v", err)
	}
	// Matched money is not "money in with nothing to match".
	var state string
	w.in(func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT state FROM money_match_state WHERE entry_id = $1`, plain).Scan(&state)
	})
	if state != "matched" {
		t.Fatalf("state %q", state)
	}

	// The day: totals by how paid, the top items, nothing left unsold.
	var d stock.Day
	w.in(func(tx pgx.Tx) error {
		var err error
		d, err = stock.DayTx(ctx, tx, w.env.Tenant, now.In(storefront.Lagos))
		return err
	})
	if d.Count != 4 || d.TotalKobo != 9000000+900000+450000 || d.ByHow[stock.Transfer] != 9000000 || d.ByHow[stock.POS] != 1350000 || d.Waiting != 0 ||
		len(d.Top) != 1 || d.Top[0].Quantity != 5 || len(d.Unsold) != 0 {
		t.Fatalf("day: %+v", d)
	}
}
