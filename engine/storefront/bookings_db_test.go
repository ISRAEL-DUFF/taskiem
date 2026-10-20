package storefront_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/templates"
)

var (
	haircut = storefront.Item{ID: "7", Name: "Haircut", PriceKobo: kobo(300000), Available: true, DurationMinutes: 30}
	class   = storefront.Item{ID: "8", Name: "Braiding class", PriceKobo: kobo(500000), Available: true, DurationMinutes: 60, Capacity: 2}
)

func kobo(n int64) *int64 { return &n }

// bookingWorld is newWorld with services, availability (every day 09:00
// to 17:00) and the booking form, running the default booking workflow.
func bookingWorld(t *testing.T, auto bool, payment string) *world {
	w := newWorld(t)
	ctx := context.Background()
	w.orders.Cancel = w.env.Store.CancelRun
	tpl, _ := templates.Default().Get("storefront-booking")
	def, err := tpl.Instantiate(map[string]any{}, "Storefront bookings")
	if err != nil {
		t.Fatal(err)
	}
	wf := w.env.Publish(t, string(def))
	w.wfs["booking"] = wf
	table := uuid.New()
	var days [7][]storefront.Span
	for i := range days {
		days[i] = []storefront.Span{{Open: "09:00", Close: "17:00"}}
	}
	w.in(func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO triggers (id, tenant_id, workflow_id, version, environment, type, storefront_action) VALUES ($1, $2, $3, 1, 'prod', 'storefront', 'booking')`,
			uuid.New(), w.env.Tenant, wf); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO data_tables (id, tenant_id, name, created_by) VALUES ($1, $2, 'services', 'test')`, table, w.env.Tenant); err != nil {
			return err
		}
		if _, err := storefront.SaveBookings(ctx, tx, w.env.Tenant, &table, storefront.Columns{Name: "name", Price: "price", Duration: "minutes", Capacity: "seats"},
			storefront.Availability{Days: days, AutoConfirm: auto}); err != nil {
			return err
		}
		if err := storefront.SyncServices(ctx, tx, w.env.Tenant, []storefront.Item{haircut, class}); err != nil {
			return err
		}
		if _, err := storefront.SaveAction(ctx, tx, w.env.Tenant, storefront.Action{Kind: "booking", Enabled: true, Payment: payment}); err != nil {
			return err
		}
		w.sf, err = storefront.Publish(ctx, tx, w.env.Tenant, "shops.test", "user:ada", false)
		return err
	})
	return w
}

// slot is day d from today, at hh:mm in Lagos.
func slot(d, hh, mm int) time.Time {
	now := time.Now().In(storefront.Lagos)
	return time.Date(now.Year(), now.Month(), now.Day()+d, hh, mm, 0, 0, storefront.Lagos)
}

// book holds a slot and submits a booking as the public host does.
func (w *world) book(it storefront.Item, start time.Time) (string, string, error) {
	w.t.Helper()
	svc, _ := storefront.ServiceOf(it)
	ref := storefront.NewReference("ada-kitchen")
	key, hash := storefront.NewStatusKey()
	sub := storefront.Submission{Kind: "booking", Customer: storefront.Customer{Name: "Bola", Phone: "+2348031234567"}, TotalKobo: *it.PriceKobo,
		Payment: "none", Channel: "direct", Submitted: time.Now(), StatusKey: key,
		Booking: &storefront.Booked{ServiceID: it.ID, ServiceName: it.Name, Start: start, End: start.Add(svc.Duration), Until: start.Add(svc.Duration + svc.Buffer), Capacity: svc.Capacity}}
	ct, err := storefront.SealSubmission(w.key, w.sf.ID, ref, sub)
	if err != nil {
		w.t.Fatal(err)
	}
	idem := make([]byte, 32)
	copy(idem, ref)
	var got *string
	err = w.env.DB.PublicPool(w.t, 4).QueryRow(context.Background(), `SELECT taskiem_public_book($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		w.sf.ID, ref, idem, hash, ct, it.ID, start, sub.Booking.Until, svc.Capacity).Scan(&got)
	return ref, key, err
}

func taken(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23P01"
}

// texts are the SMS texts queued so far, oldest first.
func (w *world) texts() []string {
	w.t.Helper()
	var out []string
	w.in(func(tx pgx.Tx) error {
		rows, err := w.env.DB.Admin.Query(context.Background(), `SELECT body FROM platform_messages WHERE dedup_key LIKE '%:sms' ORDER BY created_at, id`)
		if err != nil {
			return err
		}
		envs, err := pgx.CollectRows(rows, pgx.RowTo[map[string]any])
		if err != nil {
			return err
		}
		for _, env := range envs {
			v, err := w.env.Vault.OpenTx(context.Background(), tx, w.env.Tenant, env)
			if err != nil {
				return err
			}
			m, _ := v.(map[string]any)
			s, _ := m["text"].(string)
			out = append(out, s)
		}
		return nil
	})
	return out
}

// fire brings the run's waiting timers to now and runs what follows.
func (w *world) fire(ref string) {
	w.t.Helper()
	o := w.order(ref)
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE timers SET fire_at = now() - interval '1 second' WHERE run_id = $1 AND kind = 'wait' AND fired_at IS NULL`, *o.RunID); err != nil {
		w.t.Fatal(err)
	}
	w.env.Drain(w.t)
}

func (w *world) drain() {
	w.t.Helper()
	if err := w.orders.Drain(context.Background()); err != nil {
		w.t.Fatal(err)
	}
	w.env.Drain(w.t)
}

func (w *world) changes() {
	w.t.Helper()
	if err := w.orders.ProcessChanges(context.Background()); err != nil {
		w.t.Fatal(err)
	}
	w.env.Drain(w.t)
}

// change is a customer's change from their status link.
func (w *world) change(ref, key, action string, cutoff int, it storefront.Item, start time.Time) string {
	w.t.Helper()
	svc, _ := storefront.ServiceOf(it)
	var got string
	if err := w.env.DB.PublicPool(w.t, 1).QueryRow(context.Background(), `SELECT taskiem_public_change($1, $2, $3, $4, $5, $6, $7, $8)`,
		ref, storefront.StatusHash(key), action, cutoff, start, start.Add(svc.Duration), start.Add(svc.Duration+svc.Buffer), svc.Capacity).Scan(&got); err != nil {
		w.t.Fatal(err)
	}
	return got
}

func TestNoDoubleBooking(t *testing.T) {
	w := bookingWorld(t, false, "none")
	at := slot(3, 10, 0)
	if _, _, err := w.book(haircut, at); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name  string
		it    storefront.Item
		start time.Time
	}{{"the same time", haircut, at}, {"overlapping", haircut, at.Add(15 * time.Minute)}, {"another service over it", class, at.Add(-30 * time.Minute)}} {
		if _, _, err := w.book(c.it, c.start); !taken(err) {
			t.Errorf("%s: %v, want the slot taken", c.name, err)
		}
	}
	if _, _, err := w.book(haircut, at.Add(30*time.Minute)); err != nil {
		t.Fatalf("right after: %v", err)
	}

	// A class takes two at once, and no third, however many try together.
	start := slot(4, 12, 0)
	var mu sync.Mutex
	ok := 0
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := w.book(class, start)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case !taken(err):
				t.Errorf("book: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok != 2 {
		t.Fatalf("%d bookings of a class for two", ok)
	}
	if _, _, err := w.book(class, slot(-1, 12, 0)); !taken(err) {
		t.Fatalf("a time past: %v", err)
	}
}

func TestBookingConfirmedRemindedAndMarked(t *testing.T) {
	w := bookingWorld(t, false, "none")
	ref, key, err := w.book(haircut, slot(3, 10, 0))
	if err != nil {
		t.Fatal(err)
	}
	w.drain()
	if o := w.order(ref); o.Status != "received" || o.Kind != "booking" || o.ServiceName != "Haircut" || o.RunID == nil {
		t.Fatalf("recorded: %+v", o)
	}
	w.decide(ref, "ask", "approved")
	if o := w.order(ref); o.Status != "accepted" {
		t.Fatalf("after accepting: %+v", o)
	}
	texts := w.texts()
	if len(texts) != 1 || !strings.Contains(texts[0], "Booked: Haircut, ") || !strings.Contains(texts[0], "https://ada-kitchen.shops.test/o/"+ref+"?k="+key) {
		t.Fatalf("confirmation: %q", texts)
	}
	w.fire(ref) // a day before
	w.fire(ref) // two hours before
	texts = w.texts()
	if len(texts) != 3 || !strings.Contains(texts[1], "Reminder: Haircut tomorrow") || !strings.Contains(texts[2], "See you soon: Haircut") {
		t.Fatalf("reminders: %q", texts)
	}
	w.fire(ref) // the end: did they come?
	w.decide(ref, "outcome", "rejected")
	if o := w.order(ref); o.Status != "no_show" {
		t.Fatalf("after the appointment: %+v", o)
	}
}

func TestCustomerMovesAndCancels(t *testing.T) {
	w := bookingWorld(t, false, "none")
	ctx := context.Background()
	ref, key, err := w.book(haircut, slot(3, 10, 0))
	if err != nil {
		t.Fatal(err)
	}
	w.drain()
	w.decide(ref, "ask", "approved")
	first := *w.order(ref).RunID
	if _, _, err := w.book(haircut, slot(3, 14, 0)); err != nil {
		t.Fatal(err)
	}

	// Onto a taken time: refused; past the cut-off: refused; free: moved.
	if got := w.change(ref, key, "move", 120, haircut, slot(3, 14, 0)); got != "taken" {
		t.Fatalf("onto a taken time: %s", got)
	}
	if got := w.change(ref, key, "move", 5*24*60, haircut, slot(3, 15, 0)); got != "too_late" {
		t.Fatalf("past the cut-off: %s", got)
	}
	if got := w.change(ref, "wrong-key", "cancel", 120, haircut, time.Time{}); got != "not_allowed" {
		t.Fatalf("a wrong key: %s", got)
	}
	if got := w.change(ref, key, "move", 120, haircut, slot(3, 15, 0)); got != "ok" {
		t.Fatalf("move: %s", got)
	}
	w.changes()
	o := w.order(ref)
	if o.RunID == nil || *o.RunID == first || o.Status != "accepted" || !o.StartsAt.Equal(slot(3, 15, 0)) || o.Version != 1 {
		t.Fatalf("after moving: %+v", o)
	}
	if st := w.env.Status(t, runRef(w, first)); st != "cancelled" {
		t.Fatalf("the first run is %s", st)
	}
	if texts := w.texts(); len(texts) != 2 || !strings.Contains(texts[1], "Your booking "+ref+" is moved to ") {
		t.Fatalf("moved: %q", texts)
	}
	var alerts int
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE kind = $1`, storefront.AlertBookingChanged).Scan(&alerts)
	if alerts != 1 {
		t.Fatalf("the owner got %d alerts", alerts)
	}
	// The old time is free again.
	if _, _, err := w.book(haircut, slot(3, 10, 0)); err != nil {
		t.Fatalf("the old time: %v", err)
	}

	if got := w.change(ref, key, "cancel", 120, haircut, time.Time{}); got != "ok" {
		t.Fatalf("cancel: %s", got)
	}
	w.changes()
	if o := w.order(ref); o.Status != "cancelled" || w.env.Status(t, runRef(w, *o.RunID)) != "cancelled" {
		t.Fatalf("after cancelling: %+v", o)
	}
	if _, _, err := w.book(haircut, slot(3, 15, 0)); err != nil {
		t.Fatalf("the cancelled time: %v", err)
	}
	if got := w.change(ref, key, "cancel", 120, haircut, time.Time{}); got != "not_allowed" {
		t.Fatalf("cancelling twice: %s", got)
	}
}

func TestOwnerChangesAndAutoConfirm(t *testing.T) {
	w := bookingWorld(t, true, "none")
	ctx := context.Background()
	ref, _, err := w.book(class, slot(2, 9, 0))
	if err != nil {
		t.Fatal(err)
	}
	w.drain()
	if o := w.order(ref); o.Status != "accepted" {
		t.Fatalf("confirmed at once: %+v", o)
	}
	other, _, err := w.book(haircut, slot(2, 11, 0))
	if err != nil {
		t.Fatal(err)
	}
	w.drain()
	if _, err := w.orders.OwnerMove(ctx, w.env.Tenant, ref, slot(2, 10, 30), "user:ada"); !errors.Is(err, storefront.ErrSlotTaken) {
		t.Fatalf("onto the haircut: %v", err)
	}
	if _, err := w.orders.OwnerMove(ctx, w.env.Tenant, ref, slot(2, 18, 0), "user:ada"); err != nil {
		t.Fatalf("after hours, for the owner: %v", err)
	}
	w.changes()
	if o := w.order(ref); o.Version != 1 || o.Status != "accepted" || !o.StartsAt.Equal(slot(2, 18, 0)) {
		t.Fatalf("moved: %+v", o)
	}
	if _, err := w.orders.OwnerCancel(ctx, w.env.Tenant, other, "user:ada"); err != nil {
		t.Fatal(err)
	}
	w.changes()
	texts := w.texts()
	if last := texts[len(texts)-1]; !strings.Contains(last, other) || !strings.Contains(last, "cancelled by the business") {
		t.Fatalf("the customer was not told: %q", texts)
	}
	var alerts int
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE kind = $1`, storefront.AlertBookingChanged).Scan(&alerts)
	if alerts != 0 {
		t.Fatalf("the owner was alerted of their own changes: %d", alerts)
	}
}

func TestBookingSweeper(t *testing.T) {
	w := bookingWorld(t, false, "none")
	ctx := context.Background()
	at := slot(2, 10, 0)
	ref, _, err := w.book(haircut, at)
	if err != nil {
		t.Fatal(err)
	}
	w.drain()

	// On the morning of the day, the owner gets the day's bookings, once.
	morning := time.Date(at.Year(), at.Month(), at.Day(), 7, 30, 0, 0, storefront.Lagos)
	w.orders.Now = func() time.Time { return morning }
	for range 2 {
		if err := w.orders.CheckBookings(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var title, body string
	var n int
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*), max(title), max(body) FROM alerts WHERE kind = $1`, storefront.AlertBookingsToday).Scan(&n, &title, &body)
	if n != 1 || title != "Today's bookings: 1" || body != "10:00 Haircut ("+ref+"), not confirmed yet" {
		t.Fatalf("the day's list: %d %q %q", n, title, body)
	}

	// Unanswered at its time: expired, its run ended, the customer told.
	w.orders.Now = func() time.Time { return at.Add(time.Minute) }
	if err := w.orders.CheckBookings(ctx); err != nil {
		t.Fatal(err)
	}
	o := w.order(ref)
	if o.Status != "expired" || w.env.Status(t, runRef(w, *o.RunID)) != "cancelled" {
		t.Fatalf("unanswered: %+v", o)
	}
	if texts := w.texts(); len(texts) != 1 || !strings.Contains(texts[0], "could not be confirmed in time") {
		t.Fatalf("told: %q", texts)
	}
}

// fakeLive is the table's rows as they are now.
type fakeLive struct {
	items map[string]storefront.Item
	err   error
}

func (f *fakeLive) Items(_ context.Context, _ uuid.UUID, _ bool, ids []string) (map[string]storefront.Item, error) {
	out := map[string]storefront.Item{}
	for _, id := range ids {
		if it, ok := f.items[id]; ok {
			out[id] = it
		}
	}
	return out, f.err
}

func TestPricesCheckedBeforeTheRun(t *testing.T) {
	w := bookingWorld(t, false, "none")
	ctx := context.Background()
	live := &fakeLive{items: map[string]storefront.Item{"1": {ID: "1", Name: "Jollof rice", PriceKobo: kobo(180000), Available: true}}}
	w.orders.Live = live

	// The table does not answer: the order waits, then goes ahead with a note.
	live.err = errors.New("project_resuming")
	ref := w.submit("order", "after_accept", 300000)
	if err := w.orders.Drain(ctx); err == nil {
		t.Fatal("drained without the price check")
	}
	w.orders.Now = func() time.Time { return time.Now().Add(storefront.PriceCheckGrace + time.Minute) }
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_inbox SET claimed_until = NULL`); err != nil {
		t.Fatal(err)
	}
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if o := w.order(ref); o.Status != "received" || o.RunID == nil || !strings.HasPrefix(o.Note, "Prices were not checked") {
		t.Fatalf("unchecked: %+v", o)
	}
	w.orders.Now = nil
	live.err = nil

	// The price went up (₦1,500 to ₦1,800): the customer confirms first.
	ref = w.submit("order", "after_accept", 300000)
	w.drain()
	o := w.order(ref)
	if o.Status != "price_changed" || o.RunID != nil || o.PriceChange == nil || o.PriceChange.TotalKobo != 360000 {
		t.Fatalf("changed: %+v", o)
	}
	key := w.statusKey(ref)
	texts := w.texts()
	if last := texts[len(texts)-1]; !strings.Contains(last, "The total is now ₦3,600") || !strings.Contains(last, "?k="+key) {
		t.Fatalf("told: %q", texts)
	}
	if got := w.change(ref, key, "price_accept", 0, haircut, time.Time{}); got != "ok" {
		t.Fatalf("accept: %s", got)
	}
	w.changes()
	if o = w.order(ref); o.Status != "received" || o.TotalKobo != 360000 || o.RunID == nil || o.Basket[0].PriceKobo != 180000 {
		t.Fatalf("after confirming: %+v", o)
	}

	// A service gone from the table: the booking cannot go ahead.
	bref, _, err := w.book(haircut, slot(3, 10, 0))
	if err != nil {
		t.Fatal(err)
	}
	w.drain()
	if o := w.order(bref); o.Status != "cancelled" || !strings.Contains(o.Note, "Haircut") {
		t.Fatalf("gone: %+v", o)
	}
	if _, _, err := w.book(haircut, slot(3, 10, 0)); err != nil {
		t.Fatalf("its time was not freed: %v", err)
	}
}

// A customer cancelling a paid booking is owed a refund, unless the
// booking form keeps the deposit.
func TestCancelPaidBooking(t *testing.T) {
	w := bookingWorld(t, true, "none")
	ctx := context.Background()
	for _, c := range []struct{ policy, want string }{{"", "refund_due"}, {"keep_deposit", "cancelled"}} {
		w.in(func(tx pgx.Tx) error {
			_, err := storefront.SaveAction(ctx, tx, w.env.Tenant, storefront.Action{Kind: "booking", Enabled: true, Payment: "at_order",
				Options: storefront.ActionOptions{CancelRefund: c.policy}})
			return err
		})
		ref, key, err := w.book(haircut, slot(4, 9, 0))
		if err != nil {
			t.Fatal(err)
		}
		w.drain()
		if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_orders SET status = 'paid', paid_at = now() WHERE reference = $1`, ref); err != nil {
			t.Fatal(err)
		}
		if got := w.change(ref, key, "cancel", 120, haircut, time.Time{}); got != "ok" {
			t.Fatalf("%q: cancel: %s", c.policy, got)
		}
		if o := w.order(ref); o.Status != c.want {
			t.Fatalf("%q: %s, want %s", c.policy, o.Status, c.want)
		}
	}
	w.in(func(tx pgx.Tx) error {
		_, err := storefront.SaveAction(ctx, tx, w.env.Tenant, storefront.Action{Kind: "booking", Enabled: true, Options: storefront.ActionOptions{CancelRefund: "half"}})
		if !errors.Is(err, storefront.ErrInvalid) {
			t.Errorf("a made-up policy: %v", err)
		}
		return nil
	})
}
