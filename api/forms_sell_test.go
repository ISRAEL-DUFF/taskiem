package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/time/rate"

	ridersconn "github.com/israel-duff/taskiem/connectors/riders"
	sfconn "github.com/israel-duff/taskiem/connectors/storefront"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/publicweb"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

const (
	sellSite    = "https://adakitchen.ng"
	sellStoreAt = "ada-stores.shops.test"
)

// sellWorld is the dispatch world with a product on the page, deliveries
// to Lekki and Yaba paid at the door, riders, and the public host serving
// the storefront and the form host.
type sellWorld struct {
	*dispatchWorld
	ada  *client
	host http.Handler
}

func newSellWorld(t *testing.T) *sellWorld {
	t.Helper()
	dw, ada := newDispatchWorld(t, "Ada Stores")
	w := &sellWorld{dispatchWorld: dw, ada: ada}
	w.srv.PublicHosts = []string{"shops.test"}
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
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.biz}, func(tx pgx.Tx) error {
		if _, err := storefront.SaveAction(ctx, tx, w.biz, storefront.Action{Kind: "order", Enabled: true, Payment: "after_accept",
			Options: storefront.ActionOptions{Delivery: true, PayOnDelivery: true, Zones: []storefront.Zone{{Name: "Lekki", FeeKobo: 150000}, {Name: "Yaba", FeeKobo: 0}}}}); err != nil {
			return err
		}
		price := int64(2500000)
		if err := storefront.SyncCatalogue(ctx, tx, w.biz, []storefront.Item{{ID: "ear", Name: "Smart Ear Cleaner", PriceKobo: &price, Available: true,
			Description: "A camera on a soft tip: see what you clean."}}); err != nil {
			return err
		}
		var err error
		w.sf, err = storefront.Publish(ctx, tx, w.biz, "shops.test", "user:ada", false)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	w.forms.ShopDomain = "shops.test"
	w.orders.FormSent = w.forms.OrderSent
	w.newHost(t)
	return w
}

// newHost starts the public host afresh, nothing of it cached.
func (w *sellWorld) newHost(t *testing.T) {
	t.Helper()
	pp := w.env.DB.PublicPool(t, 2)
	formKey, _ := storefront.FormKey(bytes.Repeat([]byte{4}, 32))
	keys := w.forms.Keys
	host := &publicweb.Server{Lookup: publicweb.DBLookup(pp), Origins: publicweb.DBOrigins(pp), Storefronts: publicweb.DBStorefronts(pp),
		Submit: publicweb.DBSubmit(pp), OrderStatus: publicweb.DBOrderStatus(pp), Forms: publicweb.DBForms(pp), Reach: publicweb.DBReach(pp),
		FormHost: reachFormHost, FormKeys: &keys, FormKey: formKey, InboxKey: w.key, PreviewDomain: "shops.test", Logger: slog.New(slog.DiscardHandler),
		Views: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour), Submissions: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour),
		FormSubmissions: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour),
		// Forms left half-filled (L3): keeps and follow-ups' links.
		Keeps: publicweb.DBKeeps(pp), FormKeeps: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour)}
	w.host = host.Handler()
}

// at asks the public host for path on host (a GET, or a POST of v).
func (w *sellWorld) at(t *testing.T, host, path string, v url.Values) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if v == nil {
		r = httptest.NewRequest("GET", path, nil)
	} else {
		r = httptest.NewRequest("POST", path, strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	r.Host = host
	rec := httptest.NewRecorder()
	w.host.ServeHTTP(rec, r)
	return rec
}

func (w *sellWorld) homeFront(t *testing.T) string {
	t.Helper()
	for _, f := range w.ada.must(200, "GET", "/v1/storefront/fronts", nil)["fronts"].([]any) {
		if m := f.(map[string]any); m["slug"] == "" {
			return m["id"].(string)
		}
	}
	t.Fatal("no home front")
	return ""
}

func (w *sellWorld) publishPage(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.biz}, func(tx pgx.Tx) error {
		var err error
		w.sf, err = storefront.Publish(ctx, tx, w.biz, "shops.test", "user:ada", false)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

var (
	sfTokenRe  = regexp.MustCompile(`name="token" value="([A-Za-z0-9_-]+)"`)
	statusLoc  = regexp.MustCompile(`^/embed/o/([A-Z]{2}-[A-Z2-9]{6})\?k=[a-z2-9]+&sent=([a-z0-9-]+)$`)
	threePacks = []map[string]any{{"qty": 1, "price_kobo": 2500000, "label": "1 item + free delivery"}, {"qty": 2, "price_kobo": 4500000}, {"qty": 3, "price_kobo": 6500000}}
)

// Forms that sell L1, done when (docs/spec/forms-that-sell.md §11): a
// seller embeds a form selling one product in three packages on their
// site, a buyer orders two for pay at the door, the order reaches Orders,
// the owner accepts in chat, and a rider is offered it.
func TestFormThatSellsEndToEnd(t *testing.T) {
	w := newSellWorld(t)
	ada := w.ada
	ctx := context.Background()

	// The business's orders go to its riders; Musa rides for Lekki.
	old := ada.must(200, "GET", "/v1/dispatch/workflow", nil)["workflow_id"].(string)
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE workflow_versions SET template_id = 'storefront-order' WHERE workflow_id = $1`, old); err != nil {
		t.Fatal(err)
	}
	ada.must(200, "POST", "/v1/dispatch/workflow", map[string]any{})
	const musa = "+2348031110001"
	path := strings.TrimPrefix(ada.must(200, "GET", "/v1/riders", nil)["tap_in_url"].(string), "https://"+reachFormHost)
	loc, _ := url.Parse(w.page(t, path+"/whatsapp", nil).Header().Get("Location"))
	w.whatsapp(t, musa, loc.Query().Get("text"))
	ada.must(201, "POST", "/v1/riders", map[string]any{"name": "Musa Bello", "phone": musa, "zones": []string{"Lekki"}, "fee_kobo": 100000})
	const adaWA = "+2348020000003"
	ada.must(202, "POST", "/v1/me/whatsapp", map[string]any{"number": adaWA, "password": testPassword})
	ada.must(200, "POST", "/v1/me/whatsapp/verify", map[string]any{"code": codeRe.FindString(w.graph.Last(t, adaWA).Text)})

	// Fronts > Add an order form: the ear cleaner in three packages. A
	// product not on the list, or seven packages, is refused.
	home := w.homeFront(t)
	if items := ada.must(200, "GET", "/v1/storefront/fronts/"+home+"/sellable", nil)["items"].([]any); len(items) != 1 ||
		items[0].(map[string]any)["id"] != "ear" || items[0].(map[string]any)["price_kobo"] != float64(2500000) {
		t.Fatalf("sellable: %v", items)
	}
	if e := ada.must(400, "POST", "/v1/storefront/fronts/"+home+"/actions", map[string]any{"kind": "order", "sells": map[string]any{"item": "nope", "packages": threePacks}}); !strings.Contains(e["error"].(string), "not on this front's list") {
		t.Fatalf("not on the list: %v", e)
	}
	seven := []map[string]any{}
	for q := 1; q <= 7; q++ {
		seven = append(seven, map[string]any{"qty": q, "price_kobo": 100000 * q})
	}
	if e := ada.must(400, "POST", "/v1/storefront/fronts/"+home+"/actions", map[string]any{"kind": "order", "sells": map[string]any{"item": "ear", "packages": seven}}); !strings.Contains(e["error"].(string), "one to 6 packages") {
		t.Fatalf("seven: %v", e)
	}
	made := ada.must(201, "POST", "/v1/storefront/fronts/"+home+"/actions", map[string]any{"kind": "order", "label": "Order the ear cleaner",
		"sells": map[string]any{"item": "ear", "packages": threePacks}, "notice": "Do not order if you are not ready to receive it in 2 to 4 days."})
	act := made["action"].(map[string]any)
	actionID, slug := act["id"].(string), act["slug"].(string)
	if slug != "order-the-ear-cleaner" || len(made["warnings"].([]any)) != 0 {
		t.Fatalf("made: %v", made)
	}
	// A package dearer per item than the catalogue: a warning, not a
	// refusal; then back to the three.
	dear := ada.must(200, "PUT", "/v1/storefront/actions/"+actionID+"/sells", map[string]any{"sells": map[string]any{"item": "ear",
		"packages": []map[string]any{{"qty": 2, "price_kobo": 5200000}}}})
	if ws := dear["warnings"].([]any); len(ws) != 1 || !strings.Contains(ws[0].(string), "more than its own price") {
		t.Fatalf("dearer: %v", dear)
	}
	ada.must(200, "PUT", "/v1/storefront/actions/"+actionID+"/sells", map[string]any{"sells": map[string]any{"item": "ear", "packages": threePacks}})

	// Its workflow is the business's order workflow, with the riders.
	set := ada.must(201, "POST", "/v1/storefront/actions/"+actionID+"/setup", nil)
	wf := set["workflow_id"].(string)
	if st := ada.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)["state"]; st != "published" {
		t.Fatalf("publish: %v", st)
	}
	var tpl string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT COALESCE(template_id, '') FROM workflow_versions WHERE workflow_id = $1 AND version = 1`, wf).Scan(&tpl); err != nil || tpl != "dispatch-orders" {
		t.Fatalf("the order form's workflow: %q %v", tpl, err)
	}
	ada.must(200, "PUT", "/v1/storefront/actions/"+actionID, map[string]any{"enabled": true, "label": "Order the ear cleaner", "payment": "after_accept", "options": map[string]any{
		"delivery": true, "pay_on_delivery": true, "zones": []map[string]any{{"name": "Lekki", "fee_kobo": 150000}, {"name": "Yaba", "fee_kobo": 0}},
		"notice": "Do not order if you are not ready to receive it in 2 to 4 days."}})
	w.publishPage(t)

	// On the seller's website: proven, and the code for this one form.
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO embed_sites (id, tenant_id, origin, method, token, verified_at, created_by)
		VALUES (gen_random_uuid(), $1, $2, 'dns', 'taskiem-site-verify=abcdefghijklmnopqrstuvwxyz012345', now(), 'user:ada')`, w.biz, sellSite); err != nil {
		t.Fatal(err)
	}
	code := ada.must(200, "GET", "/v1/storefront/embed?action="+actionID, nil)
	frame := "https://" + sellStoreAt + "/embed/a/" + slug
	if code["embed_url"] != frame || !strings.Contains(code["frame"].(string), `src="`+frame+`"`) || !strings.Contains(code["script"].(string), `"storefront:height"`) ||
		!strings.Contains(code["script"].(string), `e.origin==="https://`+sellStoreAt+`"`) || !strings.Contains(code["plain"].(string), frame) {
		t.Fatalf("the code: %v", code)
	}

	// The buyer opens it in the frame on the website.
	page := w.at(t, sellStoreAt, "/embed/a/"+slug, nil)
	body := page.Body.String()
	if page.Code != 200 || !strings.Contains(page.Header().Get("Content-Security-Policy"), "frame-ancestors "+sellSite) {
		t.Fatalf("the frame: %d %v", page.Code, page.Header())
	}
	for _, want := range []string{"Smart Ear Cleaner", "1 item &#43; free delivery", "<strong>2 items</strong> <span>₦45,000", "<strong>3 items</strong> <span>₦65,000",
		"ready to receive it in 2 to 4 days", `data-kind="storefront" data-origins="` + sellSite + `"`, "Lekki, ₦1,500 delivery", `value="on_delivery"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the form: no %q", want)
		}
	}
	tok := sfTokenRe.FindStringSubmatch(body)
	if tok == nil {
		t.Fatal("no token")
	}
	time.Sleep(formguard.DefaultMinFill + 100*time.Millisecond)
	// Two for pay at the door, to Lekki; what the browser says of prices
	// is never read.
	sent := w.at(t, sellStoreAt, "/embed/a/"+slug, url.Values{"token": {tok[1]}, "website": {""}, "name": {"Bola Ade"}, "phone": {"0803 555 1234"},
		"address": {"12 Admiralty Way"}, "zone": {"Lekki"}, "pay": {"on_delivery"}, "pkg": {"2"}, "total": {"100"}, "price": {"1"}})
	m := statusLoc.FindStringSubmatch(sent.Header().Get("Location"))
	if sent.Code != http.StatusSeeOther || m == nil || m[2] != slug {
		t.Fatalf("sent: %d %q %s", sent.Code, sent.Header().Get("Location"), sent.Body.String())
	}
	ref := m[1]
	// The frame tells the website the action and the reference only.
	done := w.at(t, sellStoreAt, sent.Header().Get("Location"), nil).Body.String()
	if !strings.Contains(done, `data-submitted="`+ref+`" data-action="`+slug+`"`) || strings.Contains(done, "Bola") {
		t.Fatalf("the status page: %s", done)
	}
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	w.env.Drain(t)

	// In Orders: the package as one line, the area's fee in the total,
	// paid at the door, from the website.
	o := ada.must(200, "GET", "/v1/storefront/orders/"+ref, nil)["order"].(map[string]any)
	lines, _ := o["basket"].([]any)
	if o["total_kobo"] != float64(4650000) || o["payment"] != "on_delivery" || o["zone"] != "Lekki" || len(lines) != 1 {
		t.Fatalf("the order: %v", o)
	}
	if l := lines[0].(map[string]any); l["quantity"] != float64(2) || l["line_kobo"] != float64(4500000) || l["package_qty"] != float64(2) || l["package_kobo"] != float64(4500000) {
		t.Fatalf("the package's line: %v", l)
	}
	found := false
	for _, x := range ada.must(200, "GET", "/v1/storefront/orders?status=open", nil)["orders"].([]any) {
		found = found || x.(map[string]any)["reference"] == ref
	}
	if !found {
		t.Fatal("not in Orders")
	}
	var trig []byte
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT payload->'trigger' FROM run_events WHERE run_id = $1 AND type = 'RunStarted'`, o["run_id"]).Scan(&trig); err != nil {
		t.Fatal(err)
	}
	var run struct {
		Body struct {
			Lines []map[string]any `json:"lines"`
			Total int64            `json:"total_kobo"`
		} `json:"body"`
		Submission struct {
			Lines  []map[string]any `json:"lines"`
			Action map[string]any   `json:"action"`
		} `json:"submission"`
	}
	if err := json.Unmarshal(trig, &run); err != nil || len(run.Body.Lines) != 1 || run.Body.Lines[0]["package_kobo"] != float64(4500000) || run.Body.Total != 4650000 ||
		len(run.Submission.Lines) != 1 || run.Submission.Action["slug"] != slug {
		t.Fatalf("the run's trigger: %s %v", trig, err)
	}

	// The owner accepts in chat.
	w.tick(t)
	req := w.graph.Last(t, adaWA)
	if !strings.Contains(req.Text, "Accept or decline the order") {
		t.Fatalf("the request: %+v", req)
	}
	var got []whatsapptest.Sent
	if len(req.Buttons) > 0 {
		got = w.press(t, adaWA, req.Buttons[0].ID)
	} else {
		before := len(w.graph.Sent(adaWA))
		body := whatsapptest.Delivery(waPNID, adaWA, "wamid.sell-approve", whatsapptest.QuickReply(req.Payloads[0], "Approve"))
		w.post(t, w.wa, body, "X-Hub-Signature-256", whatsapp.Sign(waSecret, body))
		got = w.graph.Sent(adaWA)[before:]
	}
	if len(got) == 0 || !strings.Contains(got[0].Text, "approved") {
		t.Fatalf("accepted in chat: %+v", got)
	}
	w.env.Drain(t)
	// A rider is offered it.
	ds := ada.must(200, "GET", "/v1/storefront/orders/"+ref+"/deliveries", nil)["deliveries"].([]any)
	if len(ds) != 1 || ds[0].(map[string]any)["status"] != "offered" || ds[0].(map[string]any)["rider"] != "Musa Bello" {
		t.Fatalf("offered: %v", ds)
	}
	w.tick(t)
	if offer := w.graph.Last(t, musa); !strings.Contains(offer.Text, "A delivery for you: "+ref) {
		t.Fatalf("musa: %+v", offer)
	}
}

const sellHostedForm = `{"schema":"wd/v1","id":"wf_earform","version":1,"name":"Ear cleaner form",
 "trigger":{"type":"form","config":{"title":"Ear cleaner","access":"public"}},
 "inputs":{"schema":{"type":"object","required":["full_name","phone"],"properties":{
  "full_name":{"type":"string","maxLength":100},"phone":{"type":"string","format":"phone-ng","x-pii":"phone"},
  "best_day":{"type":"string","title":"Best day to deliver","maxLength":40}}}},
 "steps":[{"id":"note","type":"transform","config":{"output":{"who":"=trigger.body.full_name","day":"=trigger.body.best_day"}}}]}`

// Sell something with this form (§5.1): refused while the Storefront area
// is off, with the area named; then the hosted form becomes an order
// action of the home page with its own questions, its workflow the
// action's (reading the buyer and the answers where an order has them),
// and its address leads to the action, framed or not.
func TestSellSomethingWithThisForm(t *testing.T) {
	w := newSellWorld(t)
	ada := w.ada
	ctx := context.Background()
	wfID := publishFlow(t, ada, sellHostedForm)
	if st := ada.must(200, "POST", "/v1/workflows/"+wfID+"/versions/1/publish", nil)["state"]; st != "published" {
		t.Fatalf("publish: %v", st)
	}
	var form map[string]any
	for _, f := range ada.must(200, "GET", "/v1/forms", nil)["forms"].([]any) {
		if m := f.(map[string]any); m["workflow_id"] == wfID && m["environment"] == "prod" {
			form = m
		}
	}
	if form == nil || form["sells_as"] != nil {
		t.Fatalf("the form: %v", form)
	}
	id, public := form["id"].(string), form["public_id"].(string)
	sell := map[string]any{"sells": map[string]any{"item": "ear", "packages": threePacks}}

	// The Storefront area off: refused, naming it.
	ada.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]any{"storefront": false}, "confirm": true})
	if e := ada.must(409, "POST", "/v1/forms/"+id+"/sell", sell); e["code"] != "area_off" || e["area_off"] != "storefront" {
		t.Fatalf("area off: %v", e)
	}
	ada.must(200, "PUT", "/v1/settings/areas", map[string]any{"areas": map[string]any{"storefront": true}, "confirm": true})

	out := ada.must(201, "POST", "/v1/forms/"+id+"/sell", sell)
	act := out["action"].(map[string]any)
	slug := act["slug"].(string)
	if slug != "ear-cleaner" || act["enabled"] != true || out["version"] != float64(2) || out["url"] != "https://"+sellStoreAt+"/a/"+slug ||
		out["form_url"] != "https://"+reachFormHost+"/f/"+public {
		t.Fatalf("sold: %v", out)
	}
	if again := ada.must(409, "POST", "/v1/forms/"+id+"/sell", sell); !strings.Contains(again["error"].(string), "sells already") {
		t.Fatalf("twice: %v", again)
	}
	// The workflow runs on the action, reading the buyer's name where an
	// order has it and the form's own question as an answer.
	var def string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT definition::text FROM workflow_versions WHERE workflow_id = $1 AND version = 2`, wfID).Scan(&def); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"type": "storefront"`, `"action_id": "` + act["id"].(string) + `"`, "trigger.body.customer.name", "trigger.answers.best_day"} {
		if !strings.Contains(def, want) {
			t.Errorf("the definition: no %q in %s", want, def)
		}
	}
	var form2 []byte
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT form FROM storefront_actions WHERE id = $1`, act["id"]).Scan(&form2); err != nil {
		t.Fatal(err)
	}
	if q := storefront.FormFields(form2); len(q) != 1 || q[0] != "best_day" || !storefront.HasWidget(form2, "packages") {
		t.Fatalf("the action's form: %s", form2)
	}
	// The Forms page shows it sells.
	for _, f := range ada.must(200, "GET", "/v1/forms", nil)["forms"].([]any) {
		if m := f.(map[string]any); m["id"] == id && (m["sells_as"] != act["id"] || m["status"] != "closed") {
			t.Fatalf("listed: %v", m)
		}
	}
	// The form's address leads to the action, framed or not, keeping ?item=.
	if r := w.at(t, reachFormHost, "/f/"+public, nil); r.Code != http.StatusSeeOther || r.Header().Get("Location") != "https://"+sellStoreAt+"/a/"+slug {
		t.Fatalf("the address: %d %q", r.Code, r.Header().Get("Location"))
	}
	if r := w.at(t, reachFormHost, "/embed/f/"+public+"?item=ear", nil); r.Header().Get("Location") != "https://"+sellStoreAt+"/embed/a/"+slug+"?item=ear" {
		t.Fatalf("framed: %d %q", r.Code, r.Header().Get("Location"))
	}
	// And the action's page asks the form's own question beside the order.
	page := w.at(t, sellStoreAt, "/a/"+slug, nil).Body.String()
	if !strings.Contains(page, "Best day to deliver") || !strings.Contains(page, `name="pkg"`) || strings.Contains(page, "full_name") {
		t.Fatalf("the action's page: %s", page)
	}
	// The form's own version deployed again: the form is back, open.
	v := ada.must(201, "POST", "/v1/workflows/"+wfID+"/versions", map[string]any{"definition": json.RawMessage(sellHostedForm)})["version"]
	ada.must(200, "POST", fmt.Sprintf("/v1/workflows/%s/versions/%v/publish", wfID, v), nil)
	for _, f := range ada.must(200, "GET", "/v1/forms", nil)["forms"].([]any) {
		if m := f.(map[string]any); m["id"] == id && (m["sells_as"] != nil || m["status"] != "live") {
			t.Fatalf("deployed again: %v", m)
		}
	}
}

// A form that sells is closed as a form (review of L1): its address leads
// to the action while the action is there, it cannot be reopened as the
// old form, and once the action is deleted it says it is closed, never
// serving or running the superseded version.
func TestSoldFormClosedWhenItsActionGoes(t *testing.T) {
	w := newSellWorld(t)
	ada := w.ada
	ctx := context.Background()
	wfID := publishFlow(t, ada, sellHostedForm)
	ada.must(200, "POST", "/v1/workflows/"+wfID+"/versions/1/publish", nil)
	var id, public string
	for _, f := range ada.must(200, "GET", "/v1/forms", nil)["forms"].([]any) {
		if m := f.(map[string]any); m["workflow_id"] == wfID && m["environment"] == "prod" {
			id, public = m["id"].(string), m["public_id"].(string)
		}
	}
	act := ada.must(201, "POST", "/v1/forms/"+id+"/sell", map[string]any{"sells": map[string]any{"item": "ear", "packages": threePacks}})["action"].(map[string]any)
	if r := w.at(t, reachFormHost, "/f/"+public, nil); r.Code != http.StatusSeeOther {
		t.Fatalf("while the action is there: %d", r.Code)
	}
	if e := ada.must(409, "PATCH", "/v1/forms/"+id, map[string]any{"status": "live"}); !strings.Contains(e["error"].(string), "sells on your page") {
		t.Fatalf("reopened: %v", e)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `DELETE FROM storefront_actions WHERE id = $1`, act["id"]); err != nil {
		t.Fatal(err)
	}
	w.newHost(t)
	for _, path := range []string{"/f/" + public, "/embed/f/" + public} {
		r := w.at(t, reachFormHost, path, nil)
		if body := r.Body.String(); r.Code == http.StatusSeeOther || strings.Contains(body, "Best day to deliver") || !strings.Contains(body, "not taking answers") {
			t.Fatalf("%s after the action went: %d %s", path, r.Code, body)
		}
	}
}

// Selling builds the order form's version from the live one: a newer
// version not yet published is refused, never set aside (review of L1).
func TestSellRefusesOverANewerVersion(t *testing.T) {
	w := newSellWorld(t)
	ada := w.ada
	wfID := publishFlow(t, ada, sellHostedForm)
	ada.must(201, "POST", "/v1/workflows/"+wfID+"/versions", map[string]any{"definition": json.RawMessage(sellHostedForm)})
	var id string
	for _, f := range ada.must(200, "GET", "/v1/forms", nil)["forms"].([]any) {
		if m := f.(map[string]any); m["workflow_id"] == wfID && m["environment"] == "prod" {
			id = m["id"].(string)
		}
	}
	e := ada.must(409, "POST", "/v1/forms/"+id+"/sell", map[string]any{"sells": map[string]any{"item": "ear", "packages": threePacks}})
	if e["code"] != "newer_version" || e["newer_version"] != float64(2) || !strings.Contains(e["error"].(string), "publish it first") {
		t.Fatalf("a newer version: %v", e)
	}
	ada.must(200, "POST", "/v1/workflows/"+wfID+"/versions/2/publish", nil)
	if out := ada.must(201, "POST", "/v1/forms/"+id+"/sell", map[string]any{"sells": map[string]any{"item": "ear", "packages": threePacks}}); out["version"] != float64(3) {
		t.Fatalf("sold after publishing: %v", out)
	}
}
