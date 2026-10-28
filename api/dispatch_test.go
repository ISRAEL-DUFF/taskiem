package api_test

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/iswallet"
	ridersconn "github.com/israel-duff/taskiem/connectors/riders"
	sfconn "github.com/israel-duff/taskiem/connectors/storefront"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/dispatch"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/riders"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/telegram"
	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
	"github.com/israel-duff/taskiem/templates"
)

// dispatchWorld is the reach world with a storefront taking orders for
// delivery, riders and their deliveries.
type dispatchWorld struct {
	*reachWorld
	biz    uuid.UUID
	orders *storefront.Orders
	sf     storefront.Storefront
	key    []byte
	proofs [][]byte
	n      int
}

func newDispatchWorld(t *testing.T, owner string) (*dispatchWorld, *client) {
	w := &dispatchWorld{reachWorld: newReachWorld(t)}
	ctx := context.Background()
	ada := w.tenant(t, owner, "ada@ada.test")
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id FROM tenants WHERE name = $1`, owner).Scan(&w.biz); err != nil {
		t.Fatal(err)
	}
	w.key, _ = storefront.InboxKey(bytes.Repeat([]byte{4}, 32))
	w.orders = &storefront.Orders{Pool: w.env.DB.App, Vault: w.env.Vault, InboxKey: w.key, PreviewDomain: "shops.test", Messages: w.srv.Messages,
		Start: w.env.Store.Start, Signal: w.env.Store.DeliverSignalOnce, MoneyLog: &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}}
	w.srv.Orders = w.orders
	w.srv.Riders = &riders.Riders{Pool: w.env.DB.App, Vault: w.env.Vault, Book: w.srv.Reach, Messages: w.srv.Messages, PageBase: "https://" + reachFormHost}
	w.srv.Dispatch = &dispatch.Dispatch{Pool: w.env.DB.App, Vault: w.env.Vault, Riders: w.srv.Riders, Orders: w.orders, Chat: []string{"whatsapp", "telegram"},
		MoneyLog: &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}, Delivered: dispatch.TellRun(w.env.Store), StatusLink: w.orders.StatusLink}
	w.orders.AfterPaid = w.srv.Dispatch.OrderPaid
	w.srv.ProofStore = func(_ context.Context, _ uuid.UUID, data []byte) (string, error) {
		w.proofs = append(w.proofs, data)
		return fmt.Sprintf("files/proof-%d.jpg", len(w.proofs)), nil
	}
	tpl, _ := templates.Default().Get("storefront-order")
	def, err := tpl.Instantiate(map[string]any{}, "Storefront order")
	if err != nil {
		t.Fatal(err)
	}
	wf := w.env.PublishIn(t, w.biz, string(def))
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.biz}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO triggers (id, tenant_id, workflow_id, version, environment, type, storefront_action) VALUES ($1, $2, $3, 1, 'prod', 'storefront', 'order')`,
			uuid.New(), w.biz, wf); err != nil {
			return err
		}
		if _, err := storefront.Save(ctx, tx, w.biz, "ada-stores", storefront.Card{Name: owner, Address: "4 Akoka Road, Yaba"}, nil, storefront.Columns{}, "user:ada"); err != nil {
			return err
		}
		if _, err := storefront.SaveAction(ctx, tx, w.biz, storefront.Action{Kind: "order", Enabled: true, Payment: "after_accept",
			Options: storefront.ActionOptions{Delivery: true, Zones: []storefront.Zone{{Name: "Lekki", FeeKobo: 150000}}}}); err != nil {
			return err
		}
		w.sf, err = storefront.Publish(ctx, tx, w.biz, "shops.test", "user:ada", false)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return w, ada
}

// order makes an accepted order to Lekki, paid as payment says.
func (w *dispatchWorld) order(t *testing.T, payment string) string {
	t.Helper()
	ref := w.submit(t, payment)
	if _, err := w.orders.Decide(context.Background(), w.biz, ref, "accept", "", "user:ada"); err != nil {
		t.Fatal(err)
	}
	return ref
}

// submit sends an order for delivery to Lekki from the page, as a
// customer does; its run starts.
func (w *dispatchWorld) submit(t *testing.T, payment string) string {
	t.Helper()
	ctx := context.Background()
	w.n++
	ref := storefront.NewReference("ada-stores")
	key, hash := storefront.NewStatusKey()
	sub := storefront.Submission{Kind: "order", Customer: storefront.Customer{Name: "Bola Ade", Phone: fmt.Sprintf("+2348039%06d", time.Now().UnixNano()%1000000), Address: "12 Admiralty Way"},
		Basket: []storefront.Line{{ID: "1", Name: "Jollof rice", Quantity: 2, PriceKobo: 250000, LineKobo: 500000}}, TotalKobo: 650000, Zone: "Lekki", DeliveryKobo: 150000,
		Fulfilment: "delivery", Payment: payment, Channel: "direct", Submitted: time.Now(), StatusKey: key}
	ct, err := storefront.SealSubmission(w.key, w.sf.ID, ref, sub)
	if err != nil {
		t.Fatal(err)
	}
	idem := make([]byte, 32)
	copy(idem, ref)
	var got *string
	if err := w.env.DB.PublicPool(t, 1).QueryRow(ctx, `SELECT taskiem_public_submit($1, 'order', $2, $3, $4, $5)`, w.sf.ID, ref, idem, hash, ct).Scan(&got); err != nil || got == nil {
		t.Fatalf("submit: %v %v", got, err)
	}
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	return ref
}

// TestDispatchInChat (dispatch D2, D3): an order offered to a rider in
// their WhatsApp with Accept and Can't, the rider's taps and words run it
// to completed, their buttons work only for them, their photo and
// location are kept as proof; the owner offers, sees and releases from
// the API and from chat.
func TestDispatchInChat(t *testing.T) {
	w, ada := newDispatchWorld(t, "Ada Stores")
	ctx := context.Background()

	// Musa taps in and is added.
	tapIn := func(phone string) {
		path := strings.TrimPrefix(ada.must(200, "GET", "/v1/riders", nil)["tap_in_url"].(string), "https://"+reachFormHost)
		loc, _ := url.Parse(w.page(t, path+"/whatsapp", nil).Header().Get("Location"))
		w.whatsapp(t, phone, loc.Query().Get("text"))
	}
	const musa = "+2348031110001"
	tapIn(musa)
	m := ada.must(201, "POST", "/v1/riders", map[string]any{"name": "Musa Bello", "phone": musa, "zones": []string{"Lekki"}, "fee_kobo": 100000})["rider"].(map[string]any)
	if m["reached"] != "whatsapp" {
		t.Fatalf("musa %v", m)
	}

	// An order offered by rotation reaches Musa with Accept and Can't.
	ref := w.order(t, "none")
	out := ada.must(200, "POST", "/v1/storefront/orders/"+ref+"/offer", map[string]any{})
	if out["status"] != "offered" || out["rider"] != "Musa Bello" {
		t.Fatalf("offer %v", out)
	}
	w.tick(t)
	msg := w.graph.Last(t, musa)
	if msg.Type != "interactive" || !strings.Contains(msg.Text, "A delivery for you: "+ref) || !strings.Contains(msg.Text, "Drop: Lekki") ||
		!strings.Contains(msg.Text, "Your fee: ₦1,000") || strings.Contains(msg.Text, "Admiralty") || len(msg.Buttons) != 2 || msg.Buttons[0].Title != "Accept" {
		t.Fatalf("the offer: %+v", msg)
	}
	accept := msg.Buttons[0].ID

	// Someone else pressing it gets nothing.
	if got := w.press(t, "+2348030000009", accept); len(got) != 1 || !strings.Contains(got[0].Text, "no longer works") {
		t.Fatalf("another number: %+v", got)
	}
	// Musa accepts: the address and the customer's phone, and Picked up.
	got := w.press(t, musa, accept)
	if len(got) != 1 || !strings.Contains(got[0].Text, ref+" is yours") || !strings.Contains(got[0].Text, "12 Admiralty Way") || len(got[0].Buttons) != 1 ||
		got[0].Buttons[0].Title != "Picked up" {
		t.Fatalf("accepted: %+v", got)
	}
	// Accept again: nothing more.
	if again := w.press(t, musa, accept); len(again) != 0 {
		t.Fatalf("accepted twice: %+v", again)
	}
	// Words work as the buttons do.
	got = w.whatsapp(t, musa, "picked up")
	if len(got) != 1 || !strings.Contains(got[0].Text, "On your way with "+ref) || got[0].Buttons[0].Title != "Arrived" {
		t.Fatalf("picked up: %+v", got)
	}
	got = w.press(t, musa, got[0].Buttons[0].ID)
	if len(got) != 1 || !strings.Contains(got[0].Text, "At the door with "+ref) || got[0].Buttons[0].Title != "Delivered" {
		t.Fatalf("arrived: %+v", got)
	}
	delivered := got[0].Buttons[0].ID
	// A location at the door is kept.
	locMsg := map[string]any{"type": "location", "location": map[string]any{"latitude": 6.4281, "longitude": 3.4219}}
	body := whatsapptest.Delivery(waPNID, musa, fmt.Sprintf("wamid.loc%d", w.seq.Add(1)), locMsg)
	before := len(w.graph.Sent(musa))
	w.post(t, w.wa, body, "X-Hub-Signature-256", whatsapp.Sign(waSecret, body))
	if after := w.graph.Sent(musa)[before:]; len(after) != 1 || !strings.Contains(after[0].Text, "Got your location") {
		t.Fatalf("location: %+v", after)
	}
	got = w.press(t, musa, delivered)
	if len(got) != 1 || !strings.Contains(got[0].Text, ref+" is delivered") {
		t.Fatalf("delivered: %+v", got)
	}
	// A photo after delivering is kept as proof.
	w.graph.AddMedia("media-proof", "image/jpeg", []byte("\xff\xd8\xff photo"), 0)
	before = len(w.graph.Sent(musa))
	body = whatsapptest.Delivery(waPNID, musa, fmt.Sprintf("wamid.img%d", w.seq.Add(1)), whatsapptest.Image("media-proof", "image/jpeg", ""))
	w.post(t, w.wa, body, "X-Hub-Signature-256", whatsapp.Sign(waSecret, body))
	if after := w.graph.Sent(musa)[before:]; len(after) != 1 || !strings.Contains(after[0].Text, "Photo kept") || len(w.proofs) != 1 {
		t.Fatalf("photo: %+v, %d proofs", after, len(w.proofs))
	}
	o := ada.must(200, "GET", "/v1/storefront/orders/"+ref, nil)
	order := o
	if v, ok := o["order"].(map[string]any); ok {
		order = v
	}
	if order["status"] != "completed" {
		t.Fatalf("order %v", o)
	}
	ds := ada.must(200, "GET", "/v1/storefront/orders/"+ref+"/deliveries", nil)["deliveries"].([]any)
	d := ds[0].(map[string]any)
	if d["status"] != "delivered" || d["proof"] != true || !strings.Contains(d["location"].(string), "6.428100,3.421900") || d["rider"] != "Musa Bello" {
		t.Fatalf("delivery %v", d)
	}
	if list := ada.must(200, "GET", "/v1/deliveries", nil)["deliveries"].([]any); len(list) != 1 {
		t.Fatalf("today %v", list)
	}

	// From chat: Ada offers the next order to Musa, sees it, takes it back.
	const adaWA = "+2348020000001"
	ada.must(202, "POST", "/v1/me/whatsapp", map[string]any{"number": adaWA, "password": testPassword})
	ada.must(200, "POST", "/v1/me/whatsapp/verify", map[string]any{"code": codeRe.FindString(w.graph.Last(t, adaWA).Text)})
	ref2 := w.order(t, "none")
	if got := w.whatsapp(t, adaWA, "order "+ref2+" to musa"); len(got) != 1 || !strings.Contains(got[0].Text, ref2+" is offered to Musa Bello") {
		t.Fatalf("order to: %+v", got)
	}
	if got := w.whatsapp(t, adaWA, "order "+ref2+" to nobody"); len(got) != 1 || !strings.Contains(got[0].Text, "no rider called nobody") {
		t.Fatalf("no such rider: %+v", got)
	}
	if got := w.whatsapp(t, adaWA, "deliveries"); len(got) != 1 || !strings.Contains(got[0].Text, ref2+" · Musa Bello: offered") ||
		!strings.Contains(got[0].Text, ref+" · Musa Bello: delivered") {
		t.Fatalf("deliveries: %+v", got)
	}
	if got := w.whatsapp(t, adaWA, "release "+ref2); len(got) != 1 || !strings.Contains(got[0].Text, ref2+" is taken back from Musa Bello") {
		t.Fatalf("release: %+v", got)
	}
	// Musa's words for it now do nothing.
	if got := w.whatsapp(t, musa, "accept"); len(got) != 1 || !strings.Contains(got[0].Text, "no delivery that fits") {
		t.Fatalf("accept after release: %+v", got)
	}
	_ = ctx
}

// TestRidersPay (dispatch D5): Riders > Pay shows what each rider is due,
// keeps their bank account sealed and shown by its last digits, sets up
// the weekly payout from its template and starts it now.
func TestRidersPay(t *testing.T) {
	w, ada := newDispatchWorld(t, "Ada Stores")
	ctx := context.Background()
	// Riders are paid from the Taskiem wallet.
	f := newFakeWallets(t)
	ops := &ridersconn.Late{}
	ops.Set(w.srv.Riders)
	ops.SetDispatch(w.srv.Dispatch)
	for _, c := range []*connector.Connector{iswallet.New(iswallet.Options{BaseURL: f.srv.URL, TestURL: f.srv.URL}), ridersconn.New(ops)} {
		if err := w.env.Registry.Register(c); err != nil {
			t.Fatal(err)
		}
	}
	w.env.Vault.Platform = secrets.PlatformClients{"iswallet": {"test": {"api_key": "isw_platform_test", "shared_client": "true"}}}
	ada.must(201, "POST", "/v1/settings/receive-money", map[string]any{"business_email": "ada@ada.test", "bvn": "22212345678"})
	if v := ada.must(200, "GET", "/v1/riders/pay", nil); len(v["lines"].([]any)) != 0 || v["workflow_id"] != nil {
		t.Fatalf("nothing yet: %v", v)
	}
	const musa = "+2348031110001"
	path := strings.TrimPrefix(ada.must(200, "GET", "/v1/riders", nil)["tap_in_url"].(string), "https://"+reachFormHost)
	loc, _ := url.Parse(w.page(t, path+"/whatsapp", nil).Header().Get("Location"))
	w.whatsapp(t, musa, loc.Query().Get("text"))
	m := ada.must(201, "POST", "/v1/riders", map[string]any{"name": "Musa Bello", "phone": musa, "zones": []string{"Lekki"}, "fee_kobo": 100000})["rider"].(map[string]any)

	ref := w.order(t, "none")
	ada.must(200, "POST", "/v1/storefront/orders/"+ref+"/offer", map[string]any{})
	d := ada.must(200, "GET", "/v1/storefront/orders/"+ref+"/deliveries", nil)["deliveries"].([]any)[0].(map[string]any)
	id := uuid.MustParse(d["id"].(string))
	for _, a := range []string{dispatch.Accept, dispatch.PickedUp, dispatch.Arrived, dispatch.Delivered} {
		if _, err := w.srv.Dispatch.Press(ctx, w.biz, id, a); err != nil {
			t.Fatalf("%s: %v", a, err)
		}
	}
	v := ada.must(200, "GET", "/v1/riders/pay", nil)
	lines := v["lines"].([]any)
	if len(lines) != 1 || lines[0].(map[string]any)["net_kobo"] != float64(100000) || lines[0].(map[string]any)["rider"] != "Musa Bello" {
		t.Fatalf("lines %v", v)
	}

	// The day's money shows Musa's.
	sum := ada.must(200, "GET", "/v1/money/summary", nil)
	if rs, _ := sum["riders"].([]any); len(rs) != 1 || rs[0].(map[string]any)["delivered"] != float64(1) || rs[0].(map[string]any)["fees_kobo"] != float64(100000) {
		t.Fatalf("money summary riders: %v", sum["riders"])
	}
	ada.must(400, "PUT", "/v1/riders/"+m["id"].(string)+"/bank", map[string]any{"bank_code": "058", "account_number": "12"})
	ada.must(404, "PUT", "/v1/riders/"+uuid.NewString()+"/bank", map[string]any{"bank_code": "058", "account_number": "0123456789"})
	if b := ada.must(200, "PUT", "/v1/riders/"+m["id"].(string)+"/bank", map[string]any{"bank_code": "058", "bank_name": "GTBank", "account_number": "0123456789"}); b["bank"] != "GTBank, ending 6789" {
		t.Fatalf("bank %v", b)
	}
	if r := ada.must(200, "GET", "/v1/riders", nil)["riders"].([]any)[0].(map[string]any); r["bank"] != "GTBank, ending 6789" {
		t.Fatalf("rider %v", r)
	}

	// Paying now waits for the setup.
	ada.must(409, "POST", "/v1/riders/pay", nil)
	ada.must(400, "PUT", "/v1/riders/pay/setup", map[string]any{"pay_day": 9, "pay_time": "17:00"})
	ada.must(400, "PUT", "/v1/riders/pay/setup", map[string]any{"pay_day": 5, "pay_time": "5pm"})
	st := ada.must(200, "PUT", "/v1/riders/pay/setup", map[string]any{"pay_day": 5, "pay_time": "17:30"})
	wf := st["workflow_id"].(string)
	var cron string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT cron FROM triggers WHERE workflow_id = $1 AND environment = 'prod'`, wf).Scan(&cron); err != nil || cron != "30 17 * * 5" {
		t.Fatalf("the weekly payout: %q %v", cron, err)
	}
	// Changing it is a new version of the same workflow.
	if again := ada.must(200, "PUT", "/v1/riders/pay/setup", map[string]any{"pay_day": 1, "pay_time": "09:00"}); again["workflow_id"] != wf || again["version"] != float64(2) {
		t.Fatalf("again %v", again)
	}
	if v := ada.must(200, "GET", "/v1/riders/pay", nil); v["workflow_id"] != wf {
		t.Fatalf("view %v", v)
	}
	run := ada.must(202, "POST", "/v1/riders/pay", nil)
	defer func() {
		// With a second person needed for prod alone, a change to weekly
		// pay waits for them: prod keeps the version it has.
		ada.must(200, "PUT", "/v1/governance", map[string]any{"four_eyes_publish_environments": []string{"prod"}})
		st := ada.must(200, "PUT", "/v1/riders/pay/setup", map[string]any{"pay_day": 3, "pay_time": "12:00"})
		if st["waiting"] != true || st["version"] != float64(3) {
			t.Errorf("under four-eyes for prod: %v", st)
		}
		var cron string
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT cron FROM triggers WHERE workflow_id = $1 AND environment = 'prod'`, wf).Scan(&cron); err != nil || cron != "0 9 * * 1" {
			t.Errorf("prod changed before the second person: %q %v", cron, err)
		}
	}()
	var n int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM runs r JOIN trigger_receipts t ON t.run_id = r.id
		WHERE r.id = $1 AND r.workflow_id = $2 AND t.dedup_key = $3`, run["run_id"], wf, "riders-pay:"+run["batch_id"].(string)).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the run: %d %v", n, err)
	}
}

// TestTurnOnDeliveries (dispatch D6): turning deliveries on puts a
// workflow from dispatch-orders in place of the order form's, once; an
// order then runs from the owner's accept to the rider's offer, and the
// rider's Delivered completes the run.
func TestTurnOnDeliveries(t *testing.T) {
	w, ada := newDispatchWorld(t, "Ada Stores")
	ctx := context.Background()
	ops := &ridersconn.Late{}
	ops.Set(w.srv.Riders)
	ops.SetDispatch(w.srv.Dispatch)
	for _, c := range []*connector.Connector{sfconn.New(w.orders), ridersconn.New(ops)} {
		if err := w.env.Registry.Register(c); err != nil {
			t.Fatal(err)
		}
	}
	verifyPhone(t, w.world, ada, "phone", "business")
	if v := ada.must(200, "GET", "/v1/dispatch/workflow", nil); v["on"] != false || v["workflow_id"] == nil {
		t.Fatalf("before: %v", v)
	}
	old := ada.must(200, "GET", "/v1/dispatch/workflow", nil)["workflow_id"].(string)
	// From chat: asked first, then on.
	const adaWA = "+2348020000001"
	ada.must(202, "POST", "/v1/me/whatsapp", map[string]any{"number": adaWA, "password": testPassword})
	ada.must(200, "POST", "/v1/me/whatsapp/verify", map[string]any{"code": codeRe.FindString(w.graph.Last(t, adaWA).Text)})
	turnOn := func() []whatsapptest.Sent {
		t.Helper()
		got := w.whatsapp(t, adaWA, "turn on deliveries")
		if len(got) != 1 || !strings.Contains(got[0].Text, "Turn on deliveries now?") || len(got[0].Buttons) != 2 {
			t.Fatalf("asked: %+v", got)
		}
		return w.press(t, adaWA, got[0].Buttons[0].ID)
	}
	// The form's workflow here was written by the business, not made from
	// the plain template: it is not replaced without its say-so.
	if got := turnOn(); len(got) != 1 || !strings.Contains(got[0].Text, "starts your own workflow") {
		t.Fatalf("own workflow in chat: %+v", got)
	}
	if e := ada.must(409, "POST", "/v1/dispatch/workflow", map[string]any{}); e["code"] != "replaces_workflow" || e["workflow"] == "" {
		t.Fatalf("own workflow: %v", e)
	}
	// Made from the plain template, it is replaced.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE workflow_versions SET template_id = 'storefront-order' WHERE workflow_id = $1`, old); err != nil {
		t.Fatal(err)
	}
	if got := turnOn(); len(got) != 1 || !strings.Contains(got[0].Text, "Deliveries are on") {
		t.Fatalf("yes: %+v", got)
	}
	on := ada.must(200, "POST", "/v1/dispatch/workflow", map[string]any{"replace": true})
	wf, _ := on["workflow_id"].(string)
	if on["on"] != true || wf == "" || wf == old {
		t.Fatalf("on: %v", on)
	}
	var triggers int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM triggers WHERE type = 'storefront' AND environment = 'prod' AND tenant_id = $1`, w.biz).Scan(&triggers); err != nil || triggers != 1 {
		t.Fatalf("the form starts %d workflows: %v", triggers, err)
	}
	if again := ada.must(200, "POST", "/v1/dispatch/workflow", nil); again["workflow_id"] != wf || again["on"] != true {
		t.Fatalf("again: %v", again)
	}

	// Musa taps in; an order is accepted in its run and offered to him.
	const musa = "+2348031110001"
	path := strings.TrimPrefix(ada.must(200, "GET", "/v1/riders", nil)["tap_in_url"].(string), "https://"+reachFormHost)
	loc, _ := url.Parse(w.page(t, path+"/whatsapp", nil).Header().Get("Location"))
	w.whatsapp(t, musa, loc.Query().Get("text"))
	ada.must(201, "POST", "/v1/riders", map[string]any{"name": "Musa Bello", "phone": musa, "zones": []string{"Lekki"}, "fee_kobo": 100000})
	ref := w.submit(t, "none")
	w.env.Drain(t)
	o := ada.must(200, "GET", "/v1/storefront/orders/"+ref, nil)["order"].(map[string]any)
	runID := uuid.MustParse(o["run_id"].(string))
	if err := w.env.Store.DecideApproval(ctx, runtime.RunRef{ID: runID, TenantID: w.biz}, "decide", "approved", "user:ada", "web"); err != nil {
		t.Fatal(err)
	}
	w.env.Drain(t)
	ds := ada.must(200, "GET", "/v1/storefront/orders/"+ref+"/deliveries", nil)["deliveries"].([]any)
	if len(ds) != 1 || ds[0].(map[string]any)["status"] != "offered" || ds[0].(map[string]any)["rider"] != "Musa Bello" {
		t.Fatalf("offered by the run: %v", ds)
	}
	id := uuid.MustParse(ds[0].(map[string]any)["id"].(string))
	for _, a := range []string{dispatch.Accept, dispatch.PickedUp, dispatch.Arrived, dispatch.Delivered} {
		if _, err := w.srv.Dispatch.Press(ctx, w.biz, id, a); err != nil {
			t.Fatalf("%s: %v", a, err)
		}
	}
	w.env.Drain(t)
	var status string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status FROM runs WHERE id = $1`, runID).Scan(&status); err != nil || status != "completed" {
		t.Fatalf("the run after Delivered: %q %v", status, err)
	}
	if o := ada.must(200, "GET", "/v1/storefront/orders/"+ref, nil)["order"].(map[string]any); o["status"] != "completed" {
		t.Fatalf("order %v", o["status"])
	}
}

// Deliveries are not turned on for an order form that does not deliver,
// nor under four-eyes publishing.
func TestTurnOnDeliveriesRefused(t *testing.T) {
	w, ada := newDispatchWorld(t, "Ada Stores")
	ctx := context.Background()
	// A business with no page yet is told what to do first.
	other := w.tenant(t, "Bola Foods", "bola@bola.test")
	if e := other.must(409, "POST", "/v1/dispatch/workflow", nil); !strings.Contains(fmt.Sprint(e["error"]), "turn on delivery on your order form first") {
		t.Fatalf("no page: %v", e)
	}
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.biz}, func(tx pgx.Tx) error {
		_, err := storefront.SaveAction(ctx, tx, w.biz, storefront.Action{Kind: "order", Enabled: true, Payment: "after_accept",
			Options: storefront.ActionOptions{Pickup: true}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if e := ada.must(409, "POST", "/v1/dispatch/workflow", nil); !strings.Contains(fmt.Sprint(e["error"]), "turn on delivery on your order form") {
		t.Fatalf("no delivery: %v", e)
	}
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.biz}, func(tx pgx.Tx) error {
		_, err := storefront.SaveAction(ctx, tx, w.biz, storefront.Action{Kind: "order", Enabled: true, Payment: "after_accept",
			Options: storefront.ActionOptions{Delivery: true}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// A second person for prod alone is four-eyes too (per environment).
	ada.must(200, "PUT", "/v1/governance", map[string]any{"four_eyes_publish": false, "four_eyes_publish_environments": []string{"prod"}})
	if e := ada.must(409, "POST", "/v1/dispatch/workflow", map[string]any{"replace": true}); !strings.Contains(fmt.Sprint(e["error"]), "four-eyes") {
		t.Fatalf("four-eyes for prod: %v", e)
	}
	ada.must(200, "PUT", "/v1/governance", map[string]any{"four_eyes_publish": true})
	if e := ada.must(409, "POST", "/v1/dispatch/workflow", map[string]any{"replace": true}); !strings.Contains(fmt.Sprint(e["error"]), "four-eyes") {
		t.Fatalf("four-eyes: %v", e)
	}
}

// A rider tapped in on Telegram gets the offer there with inline buttons,
// and their taps work as on WhatsApp (dispatch D2, D3).
func TestDispatchOnTelegram(t *testing.T) {
	w, ada := newDispatchWorld(t, "Ada Stores")
	const tunde = int64(7101)
	path := strings.TrimPrefix(ada.must(200, "GET", "/v1/riders", nil)["tap_in_url"].(string), "https://"+reachFormHost)
	loc, _ := url.Parse(w.page(t, path+"/telegram", nil).Header().Get("Location"))
	start := loc.Query().Get("start")
	if start == "" {
		t.Fatalf("telegram link %v", loc)
	}
	w.telegram(t, tunde, func(id int64) []byte { return telegramtest.Start(id, tunde, "", start) })
	w.telegram(t, tunde, func(id int64) []byte { return telegramtest.ContactMessage(id, tunde, "+2348031110002", tunde) })
	r := ada.must(201, "POST", "/v1/riders", map[string]any{"name": "Tunde Ade", "phone": "+2348031110002", "zones": []string{"Lekki"}})["rider"].(map[string]any)
	if r["reached"] != "telegram" {
		t.Fatalf("tunde %v", r)
	}
	ref := w.order(t, "none")
	ada.must(200, "POST", "/v1/storefront/orders/"+ref+"/offer", map[string]any{})
	w.tick(t)
	offer := w.bot.Last(t, tunde)
	if !strings.Contains(offer.Text, "A delivery for you: "+ref) || len(offer.Keyboard) == 0 || offer.Keyboard[0][0].Text != "Accept" {
		t.Fatalf("the offer on Telegram: %+v", offer)
	}
	tap := func(b telegram.InlineButton) []telegramtest.Call {
		return w.telegram(t, tunde, func(id int64) []byte {
			return telegramtest.Callback(id, tunde, offer.MessageID, fmt.Sprintf("q%d", id), b.Data)
		})
	}
	got := tap(offer.Keyboard[0][0])
	var held telegramtest.Call
	for _, c := range got {
		if c.Method == "sendMessage" {
			held = c
		}
	}
	if !strings.Contains(held.Text, ref+" is yours") || len(held.Keyboard) == 0 || held.Keyboard[0][0].Text != "Picked up" {
		t.Fatalf("accepted on Telegram: %+v", got)
	}
	// Words work there too.
	got = w.telegram(t, tunde, func(id int64) []byte { return telegramtest.Message(id, tunde, "", "picked up") })
	if len(got) == 0 || !strings.Contains(got[len(got)-1].Text, "On your way with "+ref) {
		t.Fatalf("picked up on Telegram: %+v", got)
	}
	if o := ada.must(200, "GET", "/v1/storefront/orders/"+ref, nil)["order"].(map[string]any); o["status"] != "on_the_way" {
		t.Fatalf("order %v", o["status"])
	}
}
