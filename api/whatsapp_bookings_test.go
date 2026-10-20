package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	sfconn "github.com/israel-duff/taskiem/connectors/storefront"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
	"github.com/israel-duff/taskiem/templates"
)

func TestWhatsAppBookings(t *testing.T) {
	w := newWAWorld(t)
	ctx := context.Background()
	key, _ := storefront.InboxKey(bytes.Repeat([]byte{3}, 32))
	w.srv.PublicHosts = []string{"shops.taskiem.test"}
	w.srv.Orders = &storefront.Orders{Pool: w.env.DB.App, Vault: w.env.Vault, InboxKey: key, PreviewDomain: "shops.taskiem.test",
		Start: w.env.Store.Start, Signal: w.env.Store.DeliverSignalOnce, Cancel: w.env.Store.CancelRun}
	if err := w.env.Registry.Register(sfconn.New(w.srv.Orders)); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada's Salon", "ada@ada.test")
	const on, vn = "+2348010000009", "+2348010000005"
	w.bind(t, owner, on)
	viewer := w.member(t, owner, "vic@ada.test", "viewer")
	w.bind(t, viewer, vn)
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-salon", "draft": map[string]any{"name": "Ada's Salon"}})
	tpl, _ := templates.Default().Get("storefront-booking")
	def, err := tpl.Instantiate(map[string]any{}, "Bookings")
	if err != nil {
		t.Fatal(err)
	}
	publishFlow(t, owner, string(def))

	// Services and every day 09:00 to 17:00, set as the API would.
	var tenant, sfID uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT tenant_id, id FROM storefronts WHERE slug = 'ada-salon'`).Scan(&tenant, &sfID); err != nil {
		t.Fatal(err)
	}
	haircut := storefront.Item{ID: "7", Name: "Haircut", PriceKobo: new(int64), Available: true, DurationMinutes: 30}
	var days [7][]storefront.Span
	for i := range days {
		days[i] = []storefront.Span{{Open: "09:00", Close: "17:00"}}
	}
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		table := uuid.New()
		if _, err := tx.Exec(ctx, `INSERT INTO data_tables (id, tenant_id, name, created_by) VALUES ($1, $2, 'services', 'test')`, table, tenant); err != nil {
			return err
		}
		if _, err := storefront.SaveBookings(ctx, tx, tenant, &table, storefront.Columns{Name: "name", Duration: "minutes"}, storefront.Availability{Days: days}); err != nil {
			return err
		}
		return storefront.SyncServices(ctx, tx, tenant, []storefront.Item{haircut})
	}); err != nil {
		t.Fatal(err)
	}
	owner.must(200, "PUT", "/v1/storefront/actions/booking", map[string]any{"enabled": true, "payment": "none"})
	// Published as the API does, without reading the (absent) Xenibase table.
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := storefront.Publish(ctx, tx, tenant, "shops.taskiem.test", "user:ada")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().In(storefront.Lagos)
	day := time.Date(now.Year(), now.Month(), now.Day()+3, 0, 0, 0, 0, storefront.Lagos)
	book := func(start time.Time) string {
		t.Helper()
		ref := storefront.NewReference("ada-salon")
		_, hash := storefront.NewStatusKey()
		b := &storefront.Booked{ServiceID: "7", ServiceName: "Haircut", Start: start, End: start.Add(30 * time.Minute), Until: start.Add(30 * time.Minute), Capacity: 1}
		ct, _ := storefront.SealSubmission(key, sfID, ref, storefront.Submission{Kind: "booking", Payment: "none", Channel: "direct", Submitted: time.Now(),
			Booking: b, Customer: storefront.Customer{Name: "Bola Ade", Phone: "+2348031234567"}})
		idem := make([]byte, 32)
		copy(idem, ref)
		var got *string
		if err := w.env.DB.PublicPool(t, 1).QueryRow(ctx, `SELECT taskiem_public_book($1, $2, $3, $4, $5, '7', $6, $7, 1)`, sfID, ref, idem, hash, ct, b.Start, b.Until).Scan(&got); err != nil || got == nil {
			t.Fatalf("book: %v %v", got, err)
		}
		return ref
	}
	first, second := book(day.Add(10*time.Hour)), book(day.Add(11*time.Hour))
	if err := w.srv.Orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}

	if r := w.say(t, on, "help"); !strings.Contains(r, "*bookings*") {
		t.Errorf("help: %q", r)
	}
	if r := w.say(t, vn, "bookings"); !strings.Contains(r, "storefront.manage") {
		t.Errorf("viewer: %q", r)
	}
	if r := w.say(t, vn, "booking "+first+" cancel"); !strings.Contains(r, "storefront.manage") {
		t.Errorf("viewer cancel: %q", r)
	}
	r := w.say(t, on, "bookings")
	if !strings.Contains(r, "Bookings in the next 7 days") || !strings.Contains(r, "*"+day.Format("Mon 2 Jan")+"*") ||
		!strings.Contains(r, "10:00 Haircut, B••• •••4567 ("+first+") (not confirmed yet)") || strings.Contains(r, "Bola") {
		t.Errorf("list: %q", r)
	}
	if r := w.say(t, on, "today"); !strings.Contains(r, "No bookings today") {
		t.Errorf("today: %q", r)
	}

	// Cancel: shown, confirmed with yes, done; the customer hears from the sweeper.
	if r := w.say(t, on, "booking "+strings.ToLower(second)+" cancel"); !strings.Contains(r, "Cancel booking "+second+": Haircut, ") {
		t.Fatalf("cancel asked: %q", r)
	}
	if got := w.send(t, on, whatsapptest.ButtonReply("yes", "Yes, save")); len(got) != 1 || !strings.Contains(got[0].Text, "Booking "+second+" is cancelled") {
		t.Fatalf("cancelled: %+v", got)
	}
	if r := w.say(t, on, "booking "+second+" cancel"); !strings.Contains(r, "Cancel booking") {
		t.Fatalf("asked again: %q", r)
	}
	if got := w.send(t, on, whatsapptest.ButtonReply("yes", "Yes, save")); len(got) != 1 || !strings.Contains(got[0].Text, "no booking "+second+" that can be changed") {
		t.Fatalf("twice: %+v", got)
	}

	// Move: onto a free time; an unread time is refused before asking.
	if r := w.say(t, on, "move "+first+" to someday"); !strings.Contains(r, "could not read that time") {
		t.Errorf("bad time: %q", r)
	}
	wd := strings.ToLower(day.Format("Mon"))
	if r := w.say(t, on, "move "+first+" to "+wd+" 3pm"); !strings.Contains(r, "Move booking "+first+" (Haircut) from ") || !strings.Contains(r, day.Format("Mon 2 Jan")+", 15:00") {
		t.Fatalf("move asked: %q", r)
	}
	if r := w.say(t, on, "maybe"); !strings.Contains(r, "Reply *yes*") {
		t.Errorf("reminder: %q", r)
	}
	if got := w.send(t, on, whatsapptest.ButtonReply("yes", "Yes, save")); len(got) != 1 || !strings.Contains(got[0].Text, "is moved to "+day.Format("Mon 2 Jan")+", 15:00") {
		t.Fatalf("moved: %+v", got)
	}
	if o := owner.must(200, "GET", "/v1/storefront/orders/"+first, nil)["order"].(map[string]any); o["version"] != float64(1) {
		t.Fatalf("not moved: %v", o)
	}

	// A day off: shown with what is already booked on it, then saved once.
	if r := w.say(t, on, "off "+day.Format("2 Jan")); !strings.Contains(r, "Take "+day.Format("Mon 2 Jan")+" off?") || !strings.Contains(r, "Bookings already on it: 1") {
		t.Fatalf("off asked: %q", r)
	}
	if got := w.send(t, on, whatsapptest.Text("yes")); len(got) != 1 || !strings.Contains(got[0].Text, "is off") {
		t.Fatalf("off: %+v", got)
	}
	w.say(t, on, "off "+day.Format("2006-01-02"))
	if got := w.send(t, on, whatsapptest.Text("yes")); len(got) != 1 || !strings.Contains(got[0].Text, "already off") {
		t.Fatalf("off twice: %+v", got)
	}
	if r := w.say(t, on, "off "+day.Format("2 Jan")); !strings.Contains(r, "Take") {
		t.Fatal(r)
	}
	if r := w.say(t, on, "no"); !strings.Contains(r, "Nothing was changed") {
		t.Errorf("no: %q", r)
	}
	// A paid booking cancelled: the owner refunds it from chat, once.
	pay := &refundPay{}
	w.srv.Orders.Payments = pay
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_orders SET status = 'refund_due', payment_ref = 'pay-9', paid_at = now() WHERE reference = $1`, second); err != nil {
		t.Fatal(err)
	}
	if r := w.say(t, on, "refund "+first); !strings.Contains(r, "no payment to refund") {
		t.Errorf("nothing due: %q", r)
	}
	if r := w.say(t, on, "refund "+strings.ToLower(second)); !strings.Contains(r, "Refund ₦0 to the customer for "+second) && !strings.Contains(r, "Refund") {
		t.Fatalf("refund asked: %q", r)
	}
	if got := w.send(t, on, whatsapptest.ButtonReply("yes", "Yes, save")); len(got) != 1 || !strings.Contains(got[0].Text, "has been started") || pay.n != 1 {
		t.Fatalf("refunded: %+v %d", got, pay.n)
	}
	o := owner.must(200, "GET", "/v1/storefront/orders/"+second, nil)["order"].(map[string]any)
	if o["status"] != "refunded" {
		t.Fatalf("after the refund: %v", o)
	}
	owner.must(200, "POST", "/v1/storefront/orders/"+second+"/refund", nil) // done already: nothing more
	owner.must(409, "POST", "/v1/storefront/orders/"+first+"/refund", nil)
	owner.must(404, "POST", "/v1/storefront/orders/AD-NOPE00/refund", nil)
	if pay.n != 1 {
		t.Fatalf("refunds sent: %d", pay.n)
	}
	viewer.must(403, "POST", "/v1/storefront/orders/"+second+"/refund", nil)

	sf := owner.must(200, "GET", "/v1/storefront", nil)
	if off := sf["availability"].(map[string]any)["off"].([]any); len(off) != 1 || off[0] != day.Format("2006-01-02") {
		t.Fatalf("days off: %v", off)
	}
	if !strings.Contains(toJSON(owner.must(200, "GET", "/v1/audit", nil)), "storefront.day_off") {
		t.Error("the day off was not audited")
	}
}

// refundPay is a payment provider that only refunds.
type refundPay struct{ n int }

func (p *refundPay) Create(context.Context, uuid.UUID, string, storefront.PaymentRequest) (string, string, error) {
	return "", "", nil
}
func (p *refundPay) Verify(context.Context, uuid.UUID, string, string) (string, int64, error) {
	return "pending", 0, nil
}
func (p *refundPay) Refund(context.Context, uuid.UUID, string, string, string) (string, error) {
	p.n++
	return "pending", nil
}
