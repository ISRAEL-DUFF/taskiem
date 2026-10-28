package riders_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/reach"
	"github.com/israel-duff/taskiem/engine/riders"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/storefront"
)

type world struct {
	t   *testing.T
	env *rt.Env
	r   *riders.Riders
}

func newWorld(t *testing.T) *world {
	env := rt.New(t)
	key := bytes.Repeat([]byte{8}, 32)
	book := &reach.Book{Pool: env.DB.App, Vault: env.Vault, HashKey: key}
	return &world{t: t, env: env, r: &riders.Riders{Pool: env.DB.App, Vault: env.Vault, Book: book,
		Messages: &sender.Queue{Vault: env.Vault, HashKey: key}, PageBase: "https://forms.test"}}
}

func (w *world) in(tenant uuid.UUID, fn func(pgx.Tx) error) {
	w.t.Helper()
	if err := db.InTenantTx(context.Background(), w.env.DB.App, []uuid.UUID{tenant}, fn); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) list(tenant uuid.UUID, f riders.Filter) []riders.Rider {
	w.t.Helper()
	var out []riders.Rider
	w.in(tenant, func(tx pgx.Tx) error {
		var err error
		out, err = w.r.ListTx(context.Background(), tx, tenant, f)
		return err
	})
	return out
}

func (w *world) add(tenant uuid.UUID, name, phone string, zones ...string) riders.Rider {
	w.t.Helper()
	x, err := w.r.Add(context.Background(), tenant, riders.In{Name: &name, Phone: &phone, Zones: &zones}, "user:owner")
	if err != nil {
		w.t.Fatal(err)
	}
	return x
}

func (w *world) invites(tenant uuid.UUID) int {
	w.t.Helper()
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM platform_messages WHERE tenant_id = $1 AND dedup_key LIKE 'rider-invite:%'`,
		tenant).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

// The same phone added by two businesses is two riders, each seeing only
// its own, and one presence: off said once stops both; on brings both
// back; busy till a time shows the time. Retired riders keep their row and
// can come back.
func TestRiderSharedByTwoBusinesses(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	a, b, c := w.env.Tenant, w.env.AddTenant(t), w.env.AddTenant(t)
	musa := w.add(a, " Musa  Ibrahim ", "0803 123 4567", "Lekki", "Ajah")
	if musa.Name != "Musa Ibrahim" || musa.Phone != "+2348031234567" || len(musa.Zones) != 2 || musa.Reached != "sms" || musa.Presence.State != "free" {
		t.Fatalf("added %+v", musa)
	}
	if musa.InvitedAt == nil || w.invites(a) != 1 {
		t.Fatalf("the tap-in link was not sent: %v, %d", musa.InvitedAt, w.invites(a))
	}
	w.add(b, "Musa", "+2348031234567")
	w.add(b, "Tunde", "08091112222")
	if got := w.list(a, riders.Filter{}); len(got) != 1 || got[0].ID != musa.ID {
		t.Fatalf("A sees %+v", got)
	}
	if got := w.list(b, riders.Filter{}); len(got) != 2 || got[0].Name != "Musa" || got[1].Name != "Tunde" {
		t.Fatalf("B sees %+v", got)
	}
	if got := w.list(a, riders.Filter{Zone: "lekki"}); len(got) != 1 {
		t.Fatalf("by area: %+v", got)
	}
	if got := w.list(a, riders.Filter{Zone: "Yaba"}); len(got) != 0 {
		t.Fatalf("by another area: %+v", got)
	}
	// Twice: the second says so.
	if _, err := w.r.Add(ctx, a, riders.In{Name: ptr("M"), Phone: ptr("08031234567")}, "user:owner"); !errors.Is(err, riders.ErrExists) {
		t.Fatalf("added twice: %v", err)
	}

	// Off, from the rider's WhatsApp, for both businesses.
	n, err := w.r.Say(ctx, "+2348031234567", reach.WhatsApp, "+2348031234567", riders.Said{State: "off"})
	if err != nil || n != 2 {
		t.Fatalf("off: %d %v", n, err)
	}
	for _, tenant := range []uuid.UUID{a, b} {
		for _, x := range w.list(tenant, riders.Filter{}) {
			want := "free"
			if x.Name != "Tunde" {
				want = "off"
			}
			if x.Presence.State != want {
				t.Errorf("%s at %s: %s, want %s", x.Name, tenant, x.Presence.State, want)
			}
		}
	}
	// A business without that rider learns nothing of them.
	w.in(c, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM taskiem_rider_presence($1)`, [][]byte{w.r.Book.PhoneHash("+2348031234567")}).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("another business read the presence of a rider it does not have")
		}
		return nil
	})
	// Nor reads the table.
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{a}, func(tx pgx.Tx) error {
		var n int
		return tx.QueryRow(ctx, `SELECT count(*) FROM rider_presence`).Scan(&n)
	}); err == nil {
		t.Error("rider_presence is readable by the app role")
	}
	// Busy till a time; then on.
	until := time.Now().Add(2 * time.Hour).Truncate(time.Minute)
	if n, err := w.r.Say(ctx, "+2348031234567", reach.WhatsApp, "+2348031234567", riders.Said{State: "busy", Until: until}); err != nil || n != 2 {
		t.Fatalf("busy: %d %v", n, err)
	}
	if x := w.list(a, riders.Filter{})[0]; x.Presence.State != "busy" || x.Presence.Until == nil || !x.Presence.Until.Equal(until) {
		t.Errorf("busy: %+v", x.Presence)
	}
	if _, err := w.r.Say(ctx, "+2348031234567", reach.WhatsApp, "+2348031234567", riders.Said{State: "on"}); err != nil {
		t.Fatal(err)
	}
	if x := w.list(a, riders.Filter{})[0]; x.Presence.State != "free" {
		t.Errorf("on: %+v", x.Presence)
	}
	// Someone who rides for no one records nothing.
	if n, err := w.r.Say(ctx, "+2348039999999", reach.WhatsApp, "+2348039999999", riders.Said{State: "off"}); err != nil || n != 0 {
		t.Fatalf("a stranger: %d %v", n, err)
	}

	// Retired at A: gone from A's list, kept as retired; B still has them.
	if x, err := w.r.Retire(ctx, a, musa.ID, "user:owner"); err != nil || x.RetiredAt == nil || x.Active {
		t.Fatalf("retired: %+v %v", x, err)
	}
	if got := w.list(a, riders.Filter{}); len(got) != 0 {
		t.Errorf("a retired rider is listed: %+v", got)
	}
	if got := w.list(a, riders.Filter{Retired: true}); len(got) != 1 || got[0].ID != musa.ID {
		t.Errorf("retired: %+v", got)
	}
	if n, _ := w.r.Say(ctx, "+2348031234567", reach.WhatsApp, "+2348031234567", riders.Said{State: "off"}); n != 1 {
		t.Errorf("after retiring at A, rides for %d", n)
	}
	// Added again: the same rider back, with what is given now.
	back := w.add(a, "Musa I.", "08031234567", "lekki")
	if back.ID != musa.ID || back.RetiredAt != nil || !back.Active || back.Name != "Musa I." || len(back.Zones) != 1 {
		t.Errorf("back: %+v", back)
	}
}

// A rider who taps in on a chat app is reached there; their Telegram
// account speaks for them though it carries no phone.
func TestRiderTappedIn(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	a := w.env.Tenant
	musa := w.add(a, "Musa", "08031234567")
	w.in(a, func(tx pgx.Tx) error {
		_, err := w.r.Book.LinkTx(ctx, tx, a, "+2348031234567", reach.Telegram, "tg:4242")
		return err
	})
	x, err := w.r.Get(ctx, a, musa.ID)
	if err != nil || x.Reached != "telegram" {
		t.Fatalf("reached: %+v %v", x, err)
	}
	w.in(a, func(tx pgx.Tx) error {
		ok, err := w.r.IsRiderTx(ctx, tx, a, "08031234567")
		if !ok {
			t.Error("not a rider")
		}
		return err
	})
	if n, err := w.r.Say(ctx, "", reach.Telegram, "tg:4242", riders.Said{State: "off"}); err != nil || n != 1 {
		t.Fatalf("off on Telegram: %d %v", n, err)
	}
	if x, _ := w.r.Get(ctx, a, musa.ID); x.Presence.State != "off" {
		t.Errorf("presence %+v", x.Presence)
	}
	// Sent again only after a while.
	w.in(a, func(tx pgx.Tx) error {
		if _, err := w.r.InviteTx(ctx, tx, a, musa.ID, "user:owner"); !errors.Is(err, riders.ErrTooSoon) {
			t.Errorf("invited again at once: %v", err)
		}
		return nil
	})
}

// With delivery areas on the order form, a rider's areas are among them,
// spelt as there; changing a rider keeps their phone.
func TestRiderAreas(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	a := w.env.Tenant
	w.in(a, func(tx pgx.Tx) error {
		if _, err := storefront.Save(ctx, tx, a, "ada-stores", storefront.Card{Name: "Ada Stores"}, nil, storefront.Columns{}, "user:ada"); err != nil {
			return err
		}
		_, err := storefront.SaveAction(ctx, tx, a, storefront.Action{Kind: "order", Payment: "after_accept", Options: storefront.ActionOptions{Delivery: true,
			Zones: []storefront.Zone{{Name: "Lekki Phase 1", FeeKobo: 150000}, {Name: "Yaba", FeeKobo: 200000}}}})
		return err
	})
	musa := w.add(a, "Musa", "08031234567", "lekki phase 1")
	if len(musa.Zones) != 1 || musa.Zones[0] != "Lekki Phase 1" {
		t.Fatalf("areas %v", musa.Zones)
	}
	name, zones := "Tunde", []string{"Ikeja"}
	if _, err := w.r.Add(ctx, a, riders.In{Name: &name, Phone: ptr("08091112222"), Zones: &zones}, "user:owner"); !errors.Is(err, riders.ErrInvalid) {
		t.Errorf("an area not on the form: %v", err)
	}
	w.in(a, func(tx pgx.Tx) error {
		zones := []string{"Yaba", "LEKKI PHASE 1"}
		fee, off := int64(80000), false
		x, err := w.r.UpdateTx(ctx, tx, a, musa.ID, riders.In{Name: ptr("Musa B"), Zones: &zones, FeeKobo: &fee, Active: &off}, "user:owner")
		if err != nil {
			return err
		}
		if x.Name != "Musa B" || len(x.Zones) != 2 || x.Zones[1] != "Lekki Phase 1" || x.FeeKobo != 80000 || x.Active {
			t.Errorf("changed %+v", x)
		}
		if _, err := w.r.UpdateTx(ctx, tx, a, musa.ID, riders.In{Phone: ptr("08090000000")}, "user:owner"); !errors.Is(err, riders.ErrInvalid) {
			t.Errorf("a new phone was taken: %v", err)
		}
		return nil
	})
	if got := w.list(a, riders.Filter{ActiveOnly: true}); len(got) != 0 {
		t.Errorf("a rider not active is offered: %+v", got)
	}
}

func ptr[T any](v T) *T { return &v }
