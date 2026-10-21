package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/api"
	sfconn "github.com/israel-duff/taskiem/connectors/storefront"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

const visitorsFlow = `{"schema":"wd/v1","id":"wf_visitors","version":1,"name":"Visitors log","trigger":{"type":"manual"},
 "inputs":{"schema":{"type":"object","properties":{"name":{"type":"string"}}}},
 "steps":[{"id":"log","type":"connector","name":"Write the visitor down","connector":"xenibase@1","action":"insert_rows","connection":"data_tables",
   "input":{"table":"visitors","row":{"visitor":"=trigger.body.name"}}}]}`

// From WhatsApp alone (no-technical-steps N6): a trader makes a page,
// turns orders on, accepts an order, and builds and publishes a workflow
// that writes to a new table; "close shop today" reopens by itself.
func TestChatAllTheWay(t *testing.T) {
	w := shopWorld(t)
	ctx := context.Background()
	key, _ := storefront.InboxKey(bytes.Repeat([]byte{3}, 32))
	w.srv.Orders = &storefront.Orders{Pool: w.env.DB.App, Vault: w.env.Vault, InboxKey: key, PreviewDomain: shopDomain,
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)},
		Start:    w.env.Store.Start, Signal: w.env.Store.DeliverSignalOnce, Cancel: w.env.Store.CancelRun}
	if err := w.env.Registry.Register(sfconn.New(w.srv.Orders)); err != nil {
		t.Fatal(err)
	}
	w.srv.AI = &api.AISettings{Provider: &ai.Fake{Models: "claude-opus-5-5", Respond: func(req ai.Request) (*ai.Response, error) {
		env := map[string]any{"summary": "Logs visitors.", "assumptions": []string{}, "tests": []any{}, "template": "", "template_params": "", "workflow": visitorsFlow,
			"tables": []builder.TableSpec{{Name: "visitors", Title: "Visitors", Columns: []builder.ColumnSpec{{Name: "visitor", Title: "Visitor", Type: "text"}}}}}
		raw, _ := json.Marshal(env)
		return &ai.Response{Text: string(raw)}, nil
	}}}
	owner := w.tenant(t, "Ada's Kitchen", "ada@ada.test")
	verifyPhone(t, w.world, owner, "phone", "business")
	const on = "+2348010000081"
	w.bind(t, owner, on)

	// The page, as the owner sets it up in chat.
	w.say(t, on, "make my page")
	w.say(t, on, "Ada's Kitchen")
	w.say(t, on, "Home-cooked jollof")
	w.say(t, on, "Yaba, Lagos")
	w.say(t, on, "mon-sat 8-6, closed sunday")
	if got := w.send(t, on, whatsapptest.Text("skip")); len(got) != 1 || !strings.Contains(got[0].Text, "Publish it now?") {
		t.Fatalf("summary: %+v", got)
	}
	got := w.send(t, on, whatsapptest.ButtonReply("yes", "Yes, publish"))
	if len(got) != 4 || !strings.Contains(got[0].Text, "Your page is live") || !strings.Contains(got[3].Text, "Turn on orders now?") {
		t.Fatalf("published, then orders offered: %+v", got)
	}
	// Yes: the order workflow is made and published from chat, the form
	// switched on.
	if r := w.say(t, on, "yes"); !strings.Contains(r, "Orders are on") || !strings.Contains(r, "connect Paystack") {
		t.Fatalf("orders on: %q", r)
	}
	acts := owner.must(200, "GET", "/v1/storefront/actions", nil)["actions"].([]any)
	order := acts[0].(map[string]any)
	if order["kind"] != "order" || order["enabled"] != true || order["workflow_id"] == nil {
		t.Fatalf("the order form: %v", order)
	}

	// A customer orders on the page; the owner accepts in chat.
	var sfID uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id FROM storefronts WHERE slug = 'adas-kitchen'`).Scan(&sfID); err != nil {
		t.Fatal(err)
	}
	ref := storefront.NewReference("adas-kitchen")
	_, hash := storefront.NewStatusKey()
	ct, _ := storefront.SealSubmission(key, sfID, ref, storefront.Submission{Kind: "order", Payment: "none", Channel: "direct", Submitted: time.Now(),
		Basket: []storefront.Line{{ID: "1", Name: "Jollof rice", Quantity: 1, PriceKobo: 150000, LineKobo: 150000}}, TotalKobo: 150000, Fulfilment: "pickup",
		Customer: storefront.Customer{Name: "Bola Ade", Phone: "+2348031234567"}})
	var sub *string
	if err := w.env.DB.PublicPool(t, 1).QueryRow(ctx, `SELECT taskiem_public_submit($1, 'order', $2, $3, $4, $5)`,
		sfID, ref, bytes.Repeat([]byte{7}, 32), hash, ct).Scan(&sub); err != nil || sub == nil {
		t.Fatalf("submit: %v %v", sub, err)
	}
	if err := w.srv.Orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	w.env.Drain(t)
	w.tick(t)
	req := w.graph.Last(t, on)
	if !strings.Contains(req.Text, "Approval needed") || len(req.Buttons) != 2 {
		t.Fatalf("the order to accept: %+v", req)
	}
	if got := w.send(t, on, whatsapptest.ButtonReply(req.Buttons[0].ID, "Approve")); len(got) != 1 || !strings.Contains(got[0].Text, "request is approved") {
		t.Fatalf("accept: %+v", got)
	}
	w.env.Drain(t)
	var status string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status FROM storefront_orders WHERE reference = $1`, ref).Scan(&status); err != nil || status != "accepted" {
		t.Fatalf("the order: %s %v", status, err)
	}

	// A workflow built in chat, with its new table, published there.
	before := len(w.graph.Sent(on))
	w.say(t, on, "build when a visitor comes, write their name in a new visitors table")
	if got := w.lastTo(t, on, before+1); len(got) != 1 || !strings.Contains(got[0].Text, "Save this as a draft workflow?") {
		t.Fatalf("draft: %+v", got)
	}
	r := w.say(t, on, "yes")
	if !strings.Contains(r, "Saved *Visitors log* as a draft") || !strings.Contains(r, "I made the table it uses: visitors") || !strings.Contains(r, "Publish *Visitors log* now") {
		t.Fatalf("saved: %q", r)
	}
	if r := w.say(t, on, "yes"); !strings.Contains(r, "*Visitors log* is published") {
		t.Fatalf("publish: %q", r)
	}
	wfs := toJSON(owner.must(200, "GET", "/v1/workflows", nil))
	if !strings.Contains(wfs, `"name":"Visitors log"`) || !strings.Contains(toJSON(owner.must(200, "GET", "/v1/tables", nil)), `"name":"visitors"`) {
		t.Fatalf("workflows %s", wfs)
	}
	var published bool
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM workflows w JOIN workflow_versions v ON v.workflow_id = w.id
		WHERE w.name = 'Visitors log' AND v.state = 'published')`).Scan(&published); err != nil || !published {
		t.Fatalf("published: %v %v", published, err)
	}
	if !strings.Contains(toJSON(owner.must(200, "GET", "/v1/audit?limit=200", nil)), `"via"`) {
		t.Log("audit has the chat entries")
	}

	// "close shop today": closed, it says when it reopens, and opens again
	// by itself.
	if r := w.say(t, on, "close shop today"); !strings.Contains(r, "opens again by itself") {
		t.Fatalf("close today: %q", r)
	}
	if r := w.say(t, on, "yes"); !strings.Contains(r, "closed") || !strings.Contains(r, "opens again by itself") {
		t.Fatalf("closed: %q", r)
	}
	if st, _, _, _ := storefrontRow(t, w.world, "adas-kitchen"); st != "closed" {
		t.Fatalf("status: %s", st)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefronts SET reopen_at = now() - interval '1 minute' WHERE slug = 'adas-kitchen'`); err != nil {
		t.Fatal(err)
	}
	if n, err := (&storefront.Reopener{Pool: w.env.DB.App}).Tick(ctx); err != nil || n != 1 {
		t.Fatalf("reopen: %d %v", n, err)
	}
	if st, _, closed, _ := storefrontRow(t, w.world, "adas-kitchen"); st != "published" || closed != nil {
		t.Fatalf("reopened: %s %v", st, closed)
	}

	// Into the web app with no password: a one-time link.
	if r := w.say(t, on, "sign in"); !strings.Contains(r, waPublicURL+"/reset-password#token=") {
		t.Errorf("sign in: %q", r)
	}
}
