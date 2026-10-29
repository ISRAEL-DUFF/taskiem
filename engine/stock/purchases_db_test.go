package stock_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/reach"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/stock"
)

func (w *world) purchases() *stock.Purchases {
	key := bytes.Repeat([]byte{8}, 32)
	return &stock.Purchases{Pool: w.env.DB.App, Vault: w.env.Vault, Book: &reach.Book{Pool: w.env.DB.App, Vault: w.env.Vault, HashKey: key},
		Chat: []string{"whatsapp"}, Messages: &sender.Queue{Vault: w.env.Vault, HashKey: key}, PageBase: "https://forms.test", Stock: w.s}
}

// A supplier is added once, with an SMS tap-in link; a low item becomes a
// drafted order to its supplier; above the limit a second person
// approves; sent, it goes by SMS until the supplier taps in; received in
// part then in full, the stock rises with movements naming the order and
// the cost is set; paid, it is an expense naming the supplier, and what is
// owed falls.
func TestPurchaseOrders(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, map[string]int64{"rice": 0, "indomie": 0, "garri": 0})
	p := w.purchases()
	var costs []stock.Cost
	p.Cost = func(_ context.Context, _ uuid.UUID, row string, kobo int64, _ string) error {
		costs = append(costs, stock.Cost{ItemID: row, Kobo: kobo})
		return nil
	}
	w.apply(w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Received, Delta: n(3)}).ID)
	w.apply(w.record(stock.Move{ItemID: "indomie", Item: "Indomie", Reason: stock.Received, Delta: n(4)}).ID)

	var dangote stock.Supplier
	w.in(func(tx pgx.Tx) error {
		var err error
		dangote, err = p.AddSupplierTx(ctx, tx, w.env.Tenant, " Dangote  Depot ", "0803 123 4567", "user:owner")
		return err
	})
	if dangote.Name != "Dangote Depot" || dangote.Phone != "+2348031234567" || dangote.Reached != "sms" || dangote.InvitedAt == nil {
		t.Fatalf("supplier: %+v", dangote)
	}
	var sms []string
	w.in(func(tx pgx.Tx) error {
		_, err := p.AddSupplierTx(ctx, tx, w.env.Tenant, "dangote depot", "08099999999", "user:owner")
		if !errors.Is(err, stock.ErrSupplierExists) {
			t.Errorf("the same name: %v", err)
		}
		return nil
	})
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT array_agg(purpose ORDER BY created_at) FROM platform_messages WHERE tenant_id = $1`, w.env.Tenant).Scan(&sms); err != nil || len(sms) != 1 {
		t.Fatalf("invite: %v %v", sms, err)
	}

	items := []stock.Item{
		{ID: "rice", Name: "Rice", Unit: "bag", Stock: n(3), Reorder: n(5), Target: n(20), CostKobo: n(4000000), Supplier: "Dangote Depot"},
		{ID: "indomie", Name: "Indomie", Unit: "carton", Stock: n(4), Reorder: n(10), Target: n(50), CostKobo: n(850000), Supplier: "dangote depot"},
		{ID: "garri", Name: "Garri", Stock: n(0), Reorder: n(2), Supplier: "Mama Garri"},
	}
	var drafted []stock.Purchase
	var skipped []string
	w.in(func(tx pgx.Tx) error {
		var err error
		drafted, skipped, err = p.DraftLowTx(ctx, tx, w.env.Tenant, items, "user:owner", "low-1")
		return err
	})
	if len(drafted) != 1 || len(skipped) != 1 || skipped[0] != "Garri" {
		t.Fatalf("drafted %+v, skipped %v", drafted, skipped)
	}
	po := drafted[0]
	if po.Status != stock.Drafted || po.Supplier != "Dangote Depot" || po.TotalKobo != 17*4000000+46*850000 || po.Reference[:2] != "P-" ||
		po.Words() != "46 cartons Indomie, 17 bags Rice" {
		t.Fatalf("order: %+v", po)
	}
	// Drafting again: the same key, the same order; a fresh key finds the
	// items already on an open order.
	w.in(func(tx pgx.Tx) error {
		again, _, err := p.DraftLowTx(ctx, tx, w.env.Tenant, items, "user:owner", "low-1")
		if err != nil || len(again) != 1 || again[0].ID != po.ID {
			t.Errorf("again: %+v %v", again, err)
		}
		fresh, _, err := p.DraftLowTx(ctx, tx, w.env.Tenant, items, "user:owner", "low-2")
		if err != nil || len(fresh) != 0 {
			t.Errorf("on order already: %+v %v", fresh, err)
		}
		// A key's _ is no wildcard: "low_1" is not "low-1".
		if wild, _, err := p.DraftLowTx(ctx, tx, w.env.Tenant, items, "user:owner", "low_1"); err != nil || len(wild) != 0 {
			t.Errorf("low_1: %+v %v", wild, err)
		}
		return nil
	})

	// Above the limit, the drafter may not approve, nor may a workflow.
	limit := int64(50000000)
	w.in(func(tx pgx.Tx) error {
		return stock.PutSettingsTx(ctx, tx, w.env.Tenant, stock.Settings{PurchaseLimitKobo: &limit}, "user:owner")
	})
	for _, who := range []string{"user:owner", "workflow:" + uuid.NewString()} {
		err := txErr(w, func(tx pgx.Tx) error { _, err := p.SendTx(ctx, tx, w.env.Tenant, po.ID, who); return err })
		if !errors.Is(err, stock.ErrSecondApproval) {
			t.Errorf("%s approving: %v", who, err)
		}
	}
	w.in(func(tx pgx.Tx) error {
		var err error
		po, err = p.SendTx(ctx, tx, w.env.Tenant, po.ID, "user:bisi")
		return err
	})
	if po.Status != stock.Sent || po.ApprovedBy == nil || *po.ApprovedBy != "user:bisi" || po.SentAt == nil {
		t.Fatalf("sent: %+v", po)
	}
	var text string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*)::text FROM platform_messages WHERE tenant_id = $1`, w.env.Tenant).Scan(&text); err != nil || text != "2" {
		t.Fatalf("the order by SMS: %s %v", text, err)
	}

	// The supplier asks for a change; the owner is told.
	w.in(func(tx pgx.Tx) error {
		var err error
		po, err = stock.CounterTx(ctx, tx, w.env.Tenant, po.ID, "only 10 bags of rice this week")
		return err
	})
	var alerts int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE tenant_id = $1 AND kind = $2`, w.env.Tenant, stock.AlertSupplier).Scan(&alerts); err != nil || alerts != 1 ||
		po.Counter == nil || *po.Counter != "only 10 bags of rice this week" {
		t.Fatalf("change: %+v, %d alerts, %v", po, alerts, err)
	}
	w.in(func(tx pgx.Tx) error {
		var err error
		po, err = stock.ConfirmTx(ctx, tx, w.env.Tenant, po.ID, "owner", "user:owner")
		return err
	})
	if po.Status != stock.Confirmed || po.ConfirmedHow == nil || *po.ConfirmedHow != "owner" {
		t.Fatalf("confirmed: %+v", po)
	}

	// Received in part, then the rest; a receipt retried moves stock once.
	po, err := p.Receive(ctx, w.env.Tenant, po.ID, map[string]int64{"rice": 10}, "user:owner", "r1")
	if err != nil || po.Status != stock.PartReceived || po.ReceivedKobo != 40000000 || w.tb.count("rice") != 13 {
		t.Fatalf("part: %+v %v, rice %d", po, err, w.tb.count("rice"))
	}
	if _, err := p.Receive(ctx, w.env.Tenant, po.ID, map[string]int64{"rice": 10}, "user:owner", "r1"); err != nil || w.tb.count("rice") != 13 {
		t.Fatalf("retried: %v, rice %d", err, w.tb.count("rice"))
	}
	if m := w.movements("rice")[0]; m.Reason != stock.Received || m.Reference != po.Reference || *m.Change != 10 || !strings.Contains(m.Note, "Dangote") {
		t.Fatalf("movement: %+v", m)
	}
	po, err = p.Receive(ctx, w.env.Tenant, po.ID, map[string]int64{"rice": 7, "indomie": 46}, "user:owner", "r2")
	if err != nil || po.Status != stock.FullReceived || po.ReceivedAt == nil || po.ReceivedKobo != po.TotalKobo || w.tb.count("indomie") != 50 {
		t.Fatalf("all: %+v %v", po, err)
	}
	if len(costs) != 3 || costs[1] != (stock.Cost{ItemID: "indomie", Kobo: 850000}) {
		t.Fatalf("costs: %+v", costs)
	}
	// The receipt that completed it, retried (its answer lost): the same
	// answer, not "received already"; a key's _ is no wildcard.
	if again, err := p.Receive(ctx, w.env.Tenant, po.ID, map[string]int64{"rice": 7, "indomie": 46}, "user:owner", "r2"); err != nil ||
		again.Status != stock.FullReceived || w.tb.count("indomie") != 50 {
		t.Fatalf("retried: %+v %v", again, err)
	}
	if _, err := p.Receive(ctx, w.env.Tenant, po.ID, map[string]int64{"rice": 1}, "user:owner", "r_"); !errors.Is(err, stock.ErrPurchase) {
		t.Fatalf("r_ taken for r1: %v", err)
	}
	if _, err := p.Receive(ctx, w.env.Tenant, po.ID, map[string]int64{"rice": 1}, "user:owner", "r3"); !errors.Is(err, stock.ErrPurchase) {
		t.Fatalf("received already: %v", err)
	}

	// Paid in part by transfer: an expense naming the supplier; owed falls.
	w.in(func(tx pgx.Tx) error {
		var err error
		po, err = stock.PayTx(ctx, tx, w.env.Tenant, po.ID, 50000000, stock.Transfer, "user:owner", "pay-1", w.log())
		return err
	})
	w.in(func(tx pgx.Tx) error {
		again, err := stock.PayTx(ctx, tx, w.env.Tenant, po.ID, 50000000, stock.Transfer, "user:owner", "pay-1", w.log())
		if err != nil || again.PaidKobo != 50000000 {
			t.Errorf("paid again: %+v %v", again, err)
		}
		return nil
	})
	if po.PaidKobo != 50000000 || po.OwedKobo != po.TotalKobo-50000000 {
		t.Fatalf("paid: %+v", po)
	}
	var kind, vendor, channel, desc string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT e.kind, e.vendor, e.channel, e.description FROM purchase_payments p JOIN money_entries e ON e.id = p.entry_id
		WHERE p.purchase_id = $1`, po.ID).Scan(&kind, &vendor, &channel, &desc); err != nil || kind != "expense" || vendor != "Dangote Depot" || channel != "bank" ||
		!strings.HasPrefix(desc, "Order "+po.Reference) {
		t.Fatalf("expense: %s %s %s %q %v", kind, vendor, channel, desc, err)
	}
	w.in(func(tx pgx.Tx) error {
		list, err := p.SuppliersTx(ctx, tx, w.env.Tenant, false)
		if err != nil || len(list) != 1 || list[0].OwedKobo != po.OwedKobo || list[0].Open != 0 {
			t.Errorf("suppliers: %+v %v", list, err)
		}
		return nil
	})

	// Cancelled: only before anything came.
	w.in(func(tx pgx.Tx) error {
		other, err := stock.DraftTx(ctx, tx, w.env.Tenant, stock.DraftIn{Supplier: dangote.ID, Lines: []stock.PurchaseLine{{ItemID: "garri", Item: "Garri", Quantity: 5, CostKobo: 100}},
			Actor: "user:owner", Key: "d-2"})
		if err != nil {
			return err
		}
		if x, err := stock.CancelTx(ctx, tx, w.env.Tenant, other.ID, "user:owner"); err != nil || x.Status != stock.Cancelled {
			t.Errorf("cancel: %+v %v", x, err)
		}
		if _, err := stock.CancelTx(ctx, tx, w.env.Tenant, po.ID, "user:owner"); !errors.Is(err, stock.ErrPurchase) {
			t.Errorf("cancel a received order: %v", err)
		}
		for _, bad := range [][]stock.PurchaseLine{nil, {{ItemID: "rice", Item: "Rice", Quantity: 0}}, {{ItemID: "rice", Item: "Rice", Quantity: 1}, {ItemID: "rice", Item: "Rice", Quantity: 1}}} {
			if _, err := stock.DraftTx(ctx, tx, w.env.Tenant, stock.DraftIn{Supplier: dangote.ID, Lines: bad, Actor: "user:owner", Key: uuid.NewString()}); !errors.Is(err, stock.ErrPurchase) {
				t.Errorf("draft %+v: %v", bad, err)
			}
		}
		return nil
	})
}

// txErr runs fn in the tenant's transaction and answers its error.
func txErr(w *world, fn func(pgx.Tx) error) error {
	var out error
	w.in(func(tx pgx.Tx) error {
		out = fn(tx)
		return nil
	})
	return out
}

func TestFindSupplier(t *testing.T) {
	list := []stock.Supplier{{Name: "Dangote Depot"}, {Name: "Mama Rice"}, {Name: "Mama Garri"}}
	if s, _, err := stock.FindSupplier(list, "dangote"); err != nil || s.Name != "Dangote Depot" {
		t.Errorf("dangote: %+v %v", s, err)
	}
	if _, names, err := stock.FindSupplier(list, "mama"); !errors.Is(err, stock.ErrNoSupplier) || len(names) != 2 {
		t.Errorf("mama: %v %v", names, err)
	}
	if s, _, err := stock.FindSupplier(list, "rice"); err != nil || s.Name != "Mama Rice" {
		t.Errorf("rice: %+v %v", s, err)
	}
}
