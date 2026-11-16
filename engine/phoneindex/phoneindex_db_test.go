package phoneindex_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/phoneindex"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/storefront"
)

type fixture struct {
	t      *testing.T
	env    *rt.Env
	orders *storefront.Orders
	idx    *phoneindex.Index
}

func newFixture(t *testing.T) *fixture {
	env := rt.New(t)
	env.NoReplay = true
	idx := &phoneindex.Index{Pool: env.DB.App, Key: bytes.Repeat([]byte{8}, 32), Vault: env.Vault}
	return &fixture{t: t, env: env, idx: idx, orders: &storefront.Orders{Pool: env.DB.App, Vault: env.Vault}}
}

// business makes another business, named.
func (f *fixture) business(name string) uuid.UUID {
	f.t.Helper()
	tn := f.env.DB.SeedTenant(f.t, nil)
	if _, err := f.env.DB.Admin.Exec(context.Background(), `UPDATE tenants SET name = $2 WHERE id = $1`, tn.ID, name); err != nil {
		f.t.Fatal(err)
	}
	return tn.ID
}

// order places a chat order for phone in tenant, totalling kobo.
func (f *fixture) order(tenant uuid.UUID, phone string, kobo int64) storefront.Order {
	f.t.Helper()
	var o storefront.Order
	if err := db.InTenantTx(context.Background(), f.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		o, err = f.orders.PlaceChatTx(context.Background(), tx, tenant, storefront.ChatOrder{Customer: storefront.Customer{Name: "Bola", Phone: phone, Address: "12 Herbert Macaulay"},
			Lines: []storefront.Line{{ID: "1", Name: "Jollof rice", Quantity: 1, PriceKobo: kobo}}, Fulfilment: "pickup", Pay: storefront.ChatPayLater, By: "user:ada"})
		return err
	}); err != nil {
		f.t.Fatal(err)
	}
	return o
}

// person makes an account with a verified phone, verified at.
func (f *fixture) person(phone string, at time.Time) uuid.UUID {
	f.t.Helper()
	id := uuid.Must(uuid.NewV7())
	ctx := context.Background()
	if _, err := f.env.DB.Admin.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, 'Bola')`, id, id.String()+"@phone.taskiem.invalid"); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.env.DB.Admin.Exec(ctx, `INSERT INTO user_phones (user_id, number, verified_at) VALUES ($1, $2, $3)`, id, phone, at); err != nil {
		f.t.Fatal(err)
	}
	return id
}

// Orders made before the index are filled in business by business, each
// order's sealed phone opened in its own business; the job marks each
// business done and does not come back to it.
func TestBackfill(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a, b := f.business("Mama's Kitchen"), f.business("Ada Stores")
	oa := f.order(a, "+2348031234567", 450000)
	ob := f.order(b, "08031234567", 300000)
	oc := f.order(b, "+2348039999999", 100000)
	// One erased customer: nothing to read, nothing indexed.
	od := f.order(b, "+2348037777777", 100000)
	if err := f.env.Vault.Erase(ctx, b, od.CustomerID(), "test"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM customer_phone_index`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("before: %d %v", n, err)
	}
	done, err := f.idx.Backfill(ctx, 100)
	if err != nil || done < 2 {
		t.Fatalf("backfill: %d %v", done, err)
	}
	want := map[uuid.UUID][]byte{oa.ID: f.idx.Hash("+2348031234567"), ob.ID: f.idx.Hash("+2348031234567"), oc.ID: f.idx.Hash("+2348039999999")}
	rows, err := f.env.DB.Admin.Query(ctx, `SELECT order_id, tenant_id, phone_hash FROM customer_phone_index`)
	if err != nil {
		t.Fatal(err)
	}
	got := 0
	for rows.Next() {
		var id, tenant uuid.UUID
		var h []byte
		if err := rows.Scan(&id, &tenant, &h); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(want[id], h) {
			t.Errorf("order %s: hash %x", id, h)
		}
		got++
	}
	rows.Close()
	if got != 3 {
		t.Fatalf("indexed %d orders, want 3", got)
	}
	var marked int
	if err := f.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM phone_index_backfill WHERE tenant_id IN ($1, $2)`, a, b).Scan(&marked); err != nil || marked != 2 {
		t.Fatalf("marked: %d %v", marked, err)
	}
	if again, err := f.idx.Backfill(ctx, 100); err != nil || again != 0 {
		t.Fatalf("a second run: %d %v", again, err)
	}
}

// The one read across businesses: a person's own verified phone's orders,
// only the listed columns; nobody else's phone, and not a disabled
// account's.
func TestOrdersOfAPerson(t *testing.T) {
	f := newFixture(t)
	f.orders.PhoneIndex = f.idx
	ctx := context.Background()
	a, b := f.business("Mama's Kitchen"), f.business("Ada Stores")
	oa := f.order(a, "+2348031234567", 450000)
	ob := f.order(b, "08031234567", 300000)
	f.order(b, "+2348039999999", 100000) // someone else's
	bola := f.person("+2348031234567", time.Now().Add(-time.Hour))
	other := f.person("+2348035555555", time.Now().Add(-time.Hour))

	list, err := f.idx.Orders(ctx, bola, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Reference != ob.Reference || list[1].Reference != oa.Reference {
		t.Fatalf("orders: %+v", list)
	}
	if list[0].Business != "Ada Stores" || list[0].BusinessID != b || list[0].TotalKobo != 300000 || list[0].State != "accepted" || list[0].MadeAt.IsZero() {
		t.Fatalf("the newest: %+v", list[0])
	}
	// Made after the phone was verified: not waiting for "These are mine".
	if list[0].Waiting || list[1].Waiting {
		t.Fatalf("waiting: %+v", list)
	}
	if list, err := f.idx.Orders(ctx, other, 50); err != nil || len(list) != 0 {
		t.Fatalf("another person's orders: %+v %v", list, err)
	}
	if list, err := f.idx.Orders(ctx, uuid.New(), 50); err != nil || len(list) != 0 {
		t.Fatalf("no one's orders: %+v %v", list, err)
	}

	// The function answers only these columns, and only by person.
	var cols []string
	if err := f.env.DB.Admin.QueryRow(ctx, `SELECT array_agg(a.name ORDER BY a.ord) FROM pg_proc p,
		unnest(p.proargnames, p.proargmodes::text[]) WITH ORDINALITY a(name, mode, ord) WHERE p.proname = 'taskiem_customer_phone_orders' AND a.mode = 't'`).Scan(&cols); err != nil {
		t.Fatal(err)
	}
	if want := []string{"reference", "state", "total_kobo", "made_at", "business", "business_id", "waiting"}; len(cols) != len(want) {
		t.Fatalf("columns: %v", cols)
	} else {
		for i := range want {
			if cols[i] != want[i] {
				t.Fatalf("columns: %v", cols)
			}
		}
	}
	// The app reaches the index only in a business's own scope.
	var seen int
	if err := db.InTenantTx(ctx, f.env.DB.App, []uuid.UUID{a}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM customer_phone_index`).Scan(&seen)
	}); err != nil || seen != 1 {
		t.Fatalf("the index in one business's scope: %d %v", seen, err)
	}
	// A key other than the index's finds nothing.
	var wrong int
	if err := f.env.DB.App.QueryRow(ctx, `SELECT count(*) FROM taskiem_customer_phone_orders($1, $2, 50)`, bola, bytes.Repeat([]byte{9}, 32)).Scan(&wrong); err != nil || wrong != 0 {
		t.Fatalf("another key: %d %v", wrong, err)
	}

	// A disabled account sees nothing.
	if _, err := f.env.DB.Admin.Exec(ctx, `UPDATE users SET status = 'disabled' WHERE id = $1`, bola); err != nil {
		t.Fatal(err)
	}
	if list, err := f.idx.Orders(ctx, bola, 50); err != nil || len(list) != 0 {
		t.Fatalf("a disabled account: %+v %v", list, err)
	}
}

// Orders made with the phone before it was on the account wait for "These
// are mine"; a new number forgets the confirmation.
func TestTheseAreMine(t *testing.T) {
	f := newFixture(t)
	f.orders.PhoneIndex = f.idx
	ctx := context.Background()
	a := f.business("Mama's Kitchen")
	f.order(a, "+2348031234567", 450000)
	bola := f.person("+2348031234567", time.Now().Add(time.Minute))
	list, err := f.idx.Orders(ctx, bola, 50)
	if err != nil || len(list) != 1 || !list[0].Waiting {
		t.Fatalf("before confirming: %+v %v", list, err)
	}
	if ok, err := f.idx.Confirm(ctx, bola); err != nil || !ok {
		t.Fatalf("confirm: %v %v", ok, err)
	}
	if list, err = f.idx.Orders(ctx, bola, 50); err != nil || len(list) != 1 || list[0].Waiting {
		t.Fatalf("after confirming: %+v %v", list, err)
	}
	if _, err := f.env.DB.Admin.Exec(ctx, `UPDATE user_phones SET number = '+2348031234568' WHERE user_id = $1`, bola); err != nil {
		t.Fatal(err)
	}
	var confirmed *time.Time
	if err := f.env.DB.Admin.QueryRow(ctx, `SELECT orders_confirmed_at FROM user_phones WHERE user_id = $1`, bola).Scan(&confirmed); err != nil || confirmed != nil {
		t.Fatalf("a new number kept the confirmation: %v %v", confirmed, err)
	}
	if ok, err := f.idx.Confirm(ctx, uuid.New()); err != nil || ok {
		t.Fatalf("no phone: %v %v", ok, err)
	}
}
