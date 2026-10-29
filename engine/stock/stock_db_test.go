package stock_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// table is a catalogue's stock column with Xenibase's Idempotency-Key
// replay: a key sent again answers as it first did.
type table struct {
	mu      sync.Mutex
	n       map[string]int64
	keys    map[string]bool
	changes int
	// before runs once, between a read and the next change, as another
	// writer would.
	before func(n map[string]int64)
	fail   error
	tracks bool
}

func newTable(counts map[string]int64) *table {
	return &table{n: counts, keys: map[string]bool{}, tracks: true}
}

func (f *table) Read(_ context.Context, _ uuid.UUID, row string) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.n[row]
	return n, ok, nil
}

func (f *table) Change(_ context.Context, _ uuid.UUID, row string, from, to int64, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return false, f.fail
	}
	if applied, ok := f.keys[key]; ok {
		return applied, nil
	}
	if f.before != nil {
		f.before(f.n)
		f.before = nil
	}
	applied := f.n[row] == from
	if applied {
		f.n[row] = to
		f.changes++
	}
	f.keys[key] = applied
	return applied, nil
}

func (f *table) Tracks(context.Context, uuid.UUID) (bool, error) { return f.tracks, nil }

func (f *table) count(row string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n[row]
}

type world struct {
	t   *testing.T
	env *rt.Env
	tb  *table
	s   *stock.Stock
}

func newWorld(t *testing.T, counts map[string]int64) *world {
	w := &world{t: t, env: rt.New(t), tb: newTable(counts)}
	w.s = &stock.Stock{Pool: w.env.DB.App, Table: w.tb}
	return w
}

func (w *world) in(fn func(pgx.Tx) error) {
	w.t.Helper()
	if err := db.InTenantTx(context.Background(), w.env.DB.App, []uuid.UUID{w.env.Tenant}, fn); err != nil {
		w.t.Fatal(err)
	}
}

func n(v int64) *int64 { return &v }

func (w *world) record(mv stock.Move) stock.Movement {
	w.t.Helper()
	if mv.Actor == "" {
		mv.Actor = "user:owner"
	}
	if mv.Key == "" {
		mv.Key = uuid.NewString()
	}
	var m stock.Movement
	w.in(func(tx pgx.Tx) error {
		var err error
		m, err = stock.RecordTx(context.Background(), tx, w.env.Tenant, mv)
		return err
	})
	return m
}

func (w *world) apply(id uuid.UUID) stock.Movement {
	w.t.Helper()
	m, err := w.s.Apply(context.Background(), w.env.Tenant, id)
	if err != nil {
		w.t.Fatal(err)
	}
	return m
}

func (w *world) ledger(item string) int64 {
	w.t.Helper()
	var sum int64
	w.in(func(tx pgx.Tx) error {
		var err error
		sum, err = stock.LedgerTx(context.Background(), tx, w.env.Tenant, item)
		return err
	})
	return sum
}

func (w *world) movements(item string) []stock.Movement {
	w.t.Helper()
	var list []stock.Movement
	w.in(func(tx pgx.Tx) error {
		var err error
		list, err = stock.ListTx(context.Background(), tx, w.env.Tenant, stock.Filter{ItemID: item})
		return err
	})
	return list
}

// The count is always the sum of its movements: a count Taskiem first
// sees in the table is recorded as an outside movement before the first
// change lands on it.
func TestMovementLandsOnceAndExplainsTheCount(t *testing.T) {
	w := newWorld(t, map[string]int64{"rice": 10})
	m := w.record(stock.Move{ItemID: "rice", Item: "Rice (bag)", Reason: stock.Received, Delta: n(50), Key: "receive-1"})
	if m.Status != "pending" || m.Before != nil {
		t.Fatalf("recorded: %+v", m)
	}
	// The same key is the same movement.
	if again := w.record(stock.Move{ItemID: "rice", Item: "Rice (bag)", Reason: stock.Received, Delta: n(50), Key: "receive-1"}); again.ID != m.ID {
		t.Fatalf("a second movement for one key: %s, %s", again.ID, m.ID)
	}
	m = w.apply(m.ID)
	if m.Status != "done" || *m.Before != 10 || *m.After != 60 || *m.Change != 50 {
		t.Fatalf("applied: %+v", m)
	}
	// Applying again sends nothing more.
	w.apply(m.ID)
	if w.tb.count("rice") != 60 || w.tb.changes != 1 || w.ledger("rice") != 60 {
		t.Fatalf("table %d after %d changes, ledger %d", w.tb.count("rice"), w.tb.changes, w.ledger("rice"))
	}
	list := w.movements("rice")
	if len(list) != 2 || list[1].Reason != stock.Outside || *list[1].Change != 10 || list[1].Note != "First count seen in the table" {
		t.Fatalf("movements: %+v", list)
	}
}

// Another writer moves the count between the read and the change: the
// change is refused, read again, and lands once.
func TestMovementReadsAgainWhenTheCountMoved(t *testing.T) {
	w := newWorld(t, map[string]int64{"rice": 0})
	seed := w.apply(w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Received, Delta: n(6)}).ID)
	if *seed.After != 6 {
		t.Fatalf("seed: %+v", seed)
	}
	sale := w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Sale, Delta: n(-2)})
	// A change of Taskiem's own (another movement) lands first.
	other := w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Sale, Delta: n(-1)})
	w.tb.before = func(c map[string]int64) {
		w.tb.before = nil
		w.tb.mu.Unlock()
		w.apply(other.ID)
		w.tb.mu.Lock()
	}
	sale = w.apply(sale.ID)
	if sale.Status != "done" || *sale.Before != 5 || *sale.After != 3 {
		t.Fatalf("sale: %+v", sale)
	}
	if w.tb.count("rice") != 3 || w.ledger("rice") != 3 {
		t.Fatalf("table %d, ledger %d", w.tb.count("rice"), w.ledger("rice"))
	}
	for _, m := range w.movements("rice") {
		if m.Reason == stock.Outside {
			t.Fatalf("a change of Taskiem's taken for an outside one: %+v", m)
		}
	}
}

// Two sales of the last bag at the same moment: one takes it, the other
// is recorded short, and the count never goes below zero.
func TestTwoSalesOfTheLastBag(t *testing.T) {
	w := newWorld(t, map[string]int64{"rice": 0})
	w.apply(w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Received, Delta: n(1)}).ID)
	a := w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Sale, Delta: n(-1)})
	b := w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Sale, Delta: n(-1)})
	var wg sync.WaitGroup
	got := make([]stock.Movement, 2)
	for i, id := range []uuid.UUID{a.ID, b.ID} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, err := w.s.Apply(context.Background(), w.env.Tenant, id)
			if err != nil {
				t.Error(err)
			}
			got[i] = m
		}()
	}
	wg.Wait()
	shorts := got[0].Short + got[1].Short
	if got[0].Status != "done" || got[1].Status != "done" || shorts != 1 {
		t.Fatalf("sales: %+v %+v", got[0], got[1])
	}
	if w.tb.count("rice") != 0 || w.ledger("rice") != 0 {
		t.Fatalf("table %d, ledger %d", w.tb.count("rice"), w.ledger("rice"))
	}
}

// A count changed in the table by hand is found when the catalogue is
// read, once; not while a change of Taskiem's is on its way.
func TestOutsideChangesAreRecorded(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, map[string]int64{"rice": 0, "beans": 0})
	w.apply(w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Received, Delta: n(20)}).ID)
	w.apply(w.record(stock.Move{ItemID: "beans", Item: "Beans", Reason: stock.Received, Delta: n(4)}).ID)

	w.tb.n["rice"] = 17 // the owner edits the table
	read, err := w.s.ReadMark(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := w.s.Outside(ctx, w.env.Tenant, []stock.Seen{{ItemID: "rice", Item: "Rice", Count: 17}, {ItemID: "beans", Item: "Beans", Count: 4}}, read); err != nil || got != 1 {
		t.Fatalf("outside: %d, %v", got, err)
	}
	again, _ := w.s.ReadMark(ctx)
	if got, _ := w.s.Outside(ctx, w.env.Tenant, []stock.Seen{{ItemID: "rice", Item: "Rice", Count: 17}}, again); got != 0 {
		t.Fatal("the same change recorded twice")
	}
	list := w.movements("rice")
	if list[0].Reason != stock.Outside || *list[0].Change != -3 || list[0].Note != "Changed in the table" || list[0].Actor != "table" {
		t.Fatalf("outside: %+v", list[0])
	}
	if w.ledger("rice") != 17 {
		t.Fatalf("ledger %d", w.ledger("rice"))
	}

	// A sale sent but not yet marked done explains the table's count: no
	// outside movement for it.
	sale := w.record(stock.Move{ItemID: "beans", Item: "Beans", Reason: stock.Sale, Delta: n(-1)})
	w.tb.fail = errors.New("cut off")
	if _, err := w.s.Apply(ctx, w.env.Tenant, sale.ID); err == nil {
		t.Fatal("a failed send answered no error")
	}
	w.tb.fail = nil
	w.tb.n["beans"] = 3 // as if the send had landed before it was cut off
	later, _ := w.s.ReadMark(ctx)
	if got, _ := w.s.Outside(ctx, w.env.Tenant, []stock.Seen{{ItemID: "beans", Item: "Beans", Count: 3}}, later); got != 0 {
		t.Fatal("a change on its way taken for an outside one")
	}
	// The sweep sends it again a minute later; it lands on 3 (the table
	// moved: read again) and the count is explained.
	w.in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE stock_movements SET claimed_at = now() - interval '1 hour' WHERE id = $1`, sale.ID)
		return err
	})
	if err := w.s.ApplyPending(ctx, w.env.Tenant); err != nil {
		t.Fatal(err)
	}
	if w.tb.count("beans") != 2 || w.ledger("beans") != 2 {
		t.Fatalf("beans: table %d, ledger %d", w.tb.count("beans"), w.ledger("beans"))
	}
}

// A count sets the stock; a decrease never takes it below zero.
func TestCountsAndFloors(t *testing.T) {
	w := newWorld(t, map[string]int64{"rice": 0})
	w.apply(w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Received, Delta: n(5)}).ID)
	adj := w.apply(w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Adjustment, Delta: n(-8), Note: "spoilt"}).ID)
	if *adj.After != 0 || *adj.Change != -5 || adj.Short != 3 || adj.Note != "spoilt" {
		t.Fatalf("adjustment: %+v", adj)
	}
	c := w.apply(w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Count, SetTo: n(12)}).ID)
	if *c.After != 12 || *c.Change != 12 || w.ledger("rice") != 12 {
		t.Fatalf("count: %+v", c)
	}
	// A table holding a negative count (typed by hand) is not taken
	// further below it.
	w.tb.n["rice"] = -2
	sale := w.apply(w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Sale, Delta: n(-1)}).ID)
	if *sale.Before != -2 || *sale.After != -2 || sale.Short != 1 || w.ledger("rice") != -2 {
		t.Fatalf("sale on a negative count: %+v, ledger %d", sale, w.ledger("rice"))
	}
}

func TestMovementsRefused(t *testing.T) {
	w := newWorld(t, map[string]int64{})
	for _, mv := range []stock.Move{
		{ItemID: "rice", Item: "Rice", Reason: "stolen", Delta: n(1)},
		{ItemID: "rice", Item: "Rice", Reason: stock.Sale},
		{ItemID: "rice", Item: "Rice", Reason: stock.Sale, Delta: n(0)},
		{ItemID: "rice", Item: "Rice", Reason: stock.Sale, SetTo: n(3)},
		{ItemID: "rice", Item: "Rice", Reason: stock.Count, Delta: n(3)},
		{ItemID: "rice", Item: "Rice", Reason: stock.Count, SetTo: n(-1)},
		{ItemID: "", Item: "Rice", Reason: stock.Sale, Delta: n(-1)},
	} {
		mv.Actor, mv.Key = "user:owner", uuid.NewString()
		err := db.InTenantTx(context.Background(), w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
			_, err := stock.RecordTx(context.Background(), tx, w.env.Tenant, mv)
			return err
		})
		if !errors.Is(err, stock.ErrInvalid) {
			t.Errorf("%+v: %v", mv, err)
		}
	}
	// An item that is gone: the movement is void and moves nothing.
	m := w.apply(w.record(stock.Move{ItemID: "gone", Item: "Gone", Reason: stock.Sale, Delta: n(-1)}).ID)
	if m.Status != "void" || m.Change != nil {
		t.Fatalf("gone: %+v", m)
	}
	// What a movement asked and why are never changed once recorded.
	err := db.InTenantTx(context.Background(), w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `UPDATE stock_movements SET reason = 'count' WHERE id = $1`, m.ID)
		return err
	})
	if err == nil {
		t.Fatal("a movement's reason was changed")
	}
}

// An order's lines lower the stock once each row, answering what was
// short; a catalogue keeping no stock records nothing.
func TestOrderLines(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, map[string]int64{"1": 0, "2": 0})
	w.apply(w.record(stock.Move{ItemID: "1", Item: "Jollof rice", Reason: stock.Received, Delta: n(3)}).ID)
	w.apply(w.record(stock.Move{ItemID: "2", Item: "Chapman", Reason: stock.Received, Delta: n(1)}).ID)
	order := uuid.New()
	lines := []storefront.Line{{ID: "1", Name: "Jollof rice", Quantity: 1}, {ID: "2", Name: "Chapman", Quantity: 2}, {ID: "1", Name: "Jollof rice", Quantity: 1}}
	short, err := w.s.OrderLines(ctx, w.env.Tenant, order, "AK-7QX4KD", lines)
	if err != nil || len(short) != 1 || short[0] != "Chapman" {
		t.Fatalf("short %v, %v", short, err)
	}
	// Retried: nothing moves twice.
	if short, err = w.s.OrderLines(ctx, w.env.Tenant, order, "AK-7QX4KD", lines); err != nil || len(short) != 1 {
		t.Fatalf("retried: %v, %v", short, err)
	}
	if w.tb.count("1") != 1 || w.tb.count("2") != 0 || w.ledger("1") != 1 {
		t.Fatalf("table %d %d", w.tb.count("1"), w.tb.count("2"))
	}
	if m := w.movements("1")[0]; m.Reason != stock.Order || m.Reference != "AK-7QX4KD" || *m.Change != -2 {
		t.Fatalf("order movement: %+v", m)
	}
	w.tb.tracks = false
	if short, err := w.s.OrderLines(ctx, w.env.Tenant, uuid.New(), "AK-2", lines); err != nil || len(short) != 0 || len(w.movements("1")) != 2 {
		t.Fatalf("untracked: %v %v", short, err)
	}
}

func TestKeptRow(t *testing.T) {
	at := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	r := stock.KeptRow(stock.Movement{ID: uuid.Nil, ItemID: "1", Item: "Rice", Reason: stock.Sale, Before: n(5), After: n(3), Change: n(-2),
		Reference: "sale", Actor: "user:owner", CreatedAt: at, DoneAt: &at})
	if r["change"] != int64(-2) || r["count_after"] != int64(3) || r["at"] != "2026-10-29T09:00:00Z" || r["short"] != nil {
		t.Fatalf("row: %v", r)
	}
}
