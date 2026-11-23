package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// Customers' tools (made tools T5): an order status tool for customers,
// whose answer is a card of public outputs with a button to another
// customers' tool and one to a members' tool; a track tool for customers;
// a refund tool for members; and a public menu entry carried over.
const (
	custStatusDef = `{"schema":"wd/v1","id":"wf_status","version":1,"name":"Order status","trigger":{"type":"manual"},
	"inputs":{"schema":{"type":"object","required":["ref"],"properties":{"ref":{"type":"string","title":"Order number","maxLength":20}}}},
	"outputs":[
	  {"name":"state","type":"string","title":"State","description":"Where it is","value":"=steps.look.output.state","public":true},
	  {"name":"ref","type":"string","title":"Ref","description":"The order","value":"=steps.look.output.ref","public":true},
	  {"name":"cost","type":"string","title":"Cost","description":"What it cost us","value":"=steps.look.output.cost"},
	  {"name":"staff","type":"object","title":"Staff","description":"For staff","value":"=steps.look.output.staff"}],
	"tool":{"name":"Order status","command":"order-status","about":"Where your order is","for":"customers",
	  "answer":{"type":"card","title":"=outputs.state","ref":"=outputs.ref","lines":[{"label":"Order","value":"=outputs.ref"}],
	    "buttons":[{"label":"Track it","tool":"track","inputs":{"ref":"=outputs.ref"}},{"label":"Refund","tool":"give-back","inputs":{"ref":"=outputs.ref"}}]}},
	"steps":[{"id":"look","type":"transform","config":{"output":{"ref":"=trigger.body.ref","state":"On its way","cost":"SECRET-COST-991","staff":{"note":"SECRET-NOTE-17"}}}}]}`
	custTrackDef = `{"schema":"wd/v1","id":"wf_track","version":1,"name":"Track an order","trigger":{"type":"manual"},
	"inputs":{"schema":{"type":"object","required":["ref"],"properties":{"ref":{"type":"string","title":"Order number","maxLength":20}}}},
	"outputs":[{"name":"ref","type":"string","title":"Ref","description":"The order","value":"=steps.look.output.ref","public":true}],
	"tool":{"name":"Track an order","command":"track","about":"Where the rider is","for":"customers","answer":{"type":"text","text":"='Tracking ' + outputs.ref"}},
	"steps":[{"id":"look","type":"transform","config":{"output":{"ok":true,"ref":"=trigger.body.ref"}}}]}`
	custRefundDef = `{"schema":"wd/v1","id":"wf_refund","version":1,"name":"Refund","trigger":{"type":"manual"},
	"inputs":{"schema":{"type":"object","required":["ref"],"properties":{"ref":{"type":"string","title":"Order number"}}}},
	"outputs":[{"name":"ref","type":"string","title":"Ref","description":"The order","value":"=steps.look.output.ref"}],
	"tool":{"name":"Refund","command":"give-back","about":"Give the money back","answer":{"type":"text","text":"='Refunded ' + outputs.ref"}},
	"steps":[{"id":"look","type":"transform","config":{"output":{"ok":true,"ref":"=trigger.body.ref"}}}]}`
)

// A customer on the business's own WhatsApp number: menu lists the
// customers' tools and the public menu's entry carried over (never a
// members' tool); a pick sends the form; then yes, always; then the
// answer, from public outputs only, with a button only to another
// customers' tool, pressable by that customer alone. The menu's entry
// still starts as it did. A tool whose care says it moves money is never
// offered or started.
func TestCustomerToolsWhatsApp(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.flows()
	w.graph.AddNumber(pnA, tokA, "+234 800 000 0001")
	w.graph.AddNumber(pnB, tokB, "+234 800 000 0002")
	alpha := w.tenant(t, "Alpha", "owner@custtools.test")
	beta := w.tenant(t, "Beta", "owner@custtools-b.test")
	publishFlow(t, alpha, custStatusDef)
	publishFlow(t, alpha, custTrackDef)
	publishFlow(t, alpha, custRefundDef)
	pay := alpha.must(201, "POST", "/v1/workflows", map[string]any{"name": "Pay supplier", "definition": json.RawMessage(waPayFlow)})["id"].(string)
	alpha.must(200, "POST", "/v1/workflows/"+pay+"/versions/1/publish", nil)
	alpha.must(200, "PUT", "/v1/whatsapp/number", map[string]any{"phone_number_id": pnA, "waba_id": "300000000000001", "access_token": tokA, "own_app": false})
	beta.must(200, "PUT", "/v1/whatsapp/number", map[string]any{"phone_number_id": pnB, "waba_id": "300000000000002", "access_token": tokB, "own_app": false})
	// The menu's entry is given a command from its label: a customers' tool.
	v := alpha.must(200, "PUT", "/v1/whatsapp/public-menu", map[string]any{"enabled": true, "workflows": []any{map[string]any{"workflow_id": pay, "label": "Pay a supplier"}}})
	if pw := v["public_workflows"].([]any); len(pw) != 1 || pw[0].(map[string]any)["command"] != "pay-a-supplier" {
		t.Fatalf("menu: %v", v)
	}
	// Members never see customers' tools as theirs.
	if got := toJSON(alpha.must(200, "GET", "/v1/tools", nil)); strings.Contains(got, "order-status") || !strings.Contains(got, "give-back") {
		t.Errorf("members' tools: %s", got)
	}
	alpha.must(404, "POST", "/v1/tools/order-status/start", map[string]any{"input": map[string]any{"ref": "A-1"}})

	const tolu, ada = "+2348099990101", "+2348099990102"
	say := func(from, text string) []whatsapptest.Sent {
		return w.sendTo(t, "/channels/whatsapp", pnA, waSecret, from, whatsapptest.Text(text))
	}
	got := say(tolu, "menu")
	if len(got) != 1 || !strings.Contains(got[0].Text, "1. Order status\n2. Pay a supplier\n3. Track an order") || strings.Contains(got[0].Text, "Refund") {
		t.Fatalf("menu: %+v", got)
	}
	// A members' tool's command is the customer's words to the business,
	// never a start.
	if got := say(tolu, "/give-back A-1"); len(got) != 0 {
		t.Errorf("members' command: %+v", got)
	}
	// A pick: the form, never questions in the chat.
	got = say(tolu, "1")
	if len(got) != 1 || got[0].Flow != whatsapp.FlowInputs {
		t.Fatalf("form: %+v", got)
	}
	tok := got[0].FlowToken
	if st, resp := w.flowCall(t, "/channels/whatsapp/flows", waSecret, "data_exchange", "INPUTS", tok, map[string]any{"t1": "A-100"}); st != 200 || flowToken(t, resp) != tok {
		t.Fatalf("answers: %d %v", st, resp)
	}
	got = w.sendTo(t, "/channels/whatsapp", pnA, waSecret, tolu, whatsapptest.FlowCompleted(tok))
	// Yes, always: this tool only reads, and a member would get it at once.
	if len(got) != 1 || len(got[0].Buttons) != 2 || !strings.Contains(got[0].Text, "A-100") || !strings.Contains(got[0].Text, "Reply *yes*") {
		t.Fatalf("summary: %+v", got)
	}
	got = say(tolu, "yes")
	if len(got) != 1 || !strings.Contains(got[0].Text, "The answer comes here") {
		t.Fatalf("started: %+v", got)
	}
	w.env.Drain(t)
	before := len(w.graph.Sent(tolu))
	w.tick(t)
	got = w.graph.Sent(tolu)[before:]
	if len(got) != 1 || got[0].From != pnA {
		t.Fatalf("answer: %+v", got)
	}
	ans := got[0]
	if !strings.Contains(ans.Text, "*On its way* · A-100") || strings.Contains(ans.Text, "SECRET") {
		t.Errorf("answer words: %q", ans.Text)
	}
	// Only the button to another customers' tool; never the members' refund.
	if len(ans.Buttons) != 1 || ans.Buttons[0].Title != "Track it" {
		t.Fatalf("answer buttons: %+v", ans.Buttons)
	}
	// Told once.
	w.tick(t)
	if n := len(w.graph.Sent(tolu)) - before; n != 1 {
		t.Errorf("told %d times", n)
	}
	// The run is the customer's, audited with the number masked.
	runs := toJSON(alpha.must(200, "GET", "/v1/runs", nil)["runs"])
	if !strings.Contains(runs, "whatsapp_public:"+tolu) {
		t.Errorf("runs: %s", runs)
	}
	var n int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'run.start' AND actor_id LIKE 'whatsapp_public:+234•%' AND detail->>'tool' = 'order-status' AND detail->>'for' = 'customers'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("audited: %d %v", n, err)
	}
	// A member who may read runs sees all of it; the customer did not.
	var run string
	// Told, the waiting row and its number are gone.
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM customer_tool_watches`).Scan(&n); err != nil || n != 0 {
		t.Errorf("watches kept: %d %v", n, err)
	}
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id::text FROM runs WHERE started_by = $1`, "whatsapp_public:"+tolu).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if a := alpha.must(200, "GET", "/v1/runs/"+run+"/answer", nil); a["title"] != "On its way" {
		t.Errorf("member's answer: %v", a)
	}

	// The button: another customer pressing it is refused; its customer
	// gets the track tool's form.
	btn := ans.Buttons[0].ID
	if got := w.sendTo(t, "/channels/whatsapp", pnA, waSecret, ada, whatsapptest.ButtonReply(btn, "Track it")); len(got) != 1 || !strings.Contains(got[0].Text, "not available any more") {
		t.Errorf("another's press: %+v", got)
	}
	// Nor by a member, as a member's button.
	if !strings.HasPrefix(btn, "tb:") {
		t.Fatalf("button id: %q", btn)
	}
	alpha.must(404, "POST", "/v1/tools/buttons/"+strings.TrimPrefix(btn, "tb:"), nil)
	// The button carries every value the track tool takes: straight to yes.
	got = w.sendTo(t, "/channels/whatsapp", pnA, waSecret, tolu, whatsapptest.ButtonReply(btn, "Track it"))
	if len(got) != 1 || !strings.Contains(got[0].Text, "Track an order") || !strings.Contains(got[0].Text, "A-100") || len(got[0].Buttons) != 2 {
		t.Fatalf("pressed: %+v", got)
	}

	// The public menu's entry still starts as it did, with its reference.
	say(ada, "menu")
	got = say(ada, "2")
	if len(got) != 1 || got[0].Flow != whatsapp.FlowInputs {
		t.Fatalf("menu entry's form: %+v", got)
	}
	tok = got[0].FlowToken
	if st, resp := w.flowCall(t, "/channels/whatsapp/flows", waSecret, "data_exchange", "INPUTS", tok, map[string]any{"n1": "700", "t2": "0123456789", "c3": "1"}); st != 200 || flowToken(t, resp) != tok {
		t.Fatalf("menu entry's answers: %d %v", st, resp)
	}
	w.sendTo(t, "/channels/whatsapp", pnA, waSecret, ada, whatsapptest.FlowCompleted(tok))
	if got := say(ada, "yes"); len(got) != 1 || !strings.Contains(got[0].Text, "has started. Your reference is") {
		t.Fatalf("menu entry started: %+v", got)
	}

	// A tool whose care says it moves money is neither offered nor started,
	// whatever was deployed.
	const kemi = "+2348099990103"
	say(kemi, "menu")
	got = say(kemi, "3")
	if len(got) != 1 || got[0].Flow != whatsapp.FlowInputs {
		t.Fatalf("track's form: %+v", got)
	}
	tok = got[0].FlowToken
	w.flowCall(t, "/channels/whatsapp/flows", waSecret, "data_exchange", "INPUTS", tok, map[string]any{"t1": "A-7"})
	w.sendTo(t, "/channels/whatsapp", pnA, waSecret, kemi, whatsapptest.FlowCompleted(tok))
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE workflow_tools SET care = '{"level":"step_up","because":["pay"]}' WHERE command = 'track'`); err != nil {
		t.Fatal(err)
	}
	if got := say(kemi, "yes"); len(got) != 1 || !strings.Contains(got[0].Text, "can't be done from here") {
		t.Errorf("money tool's yes: %+v", got)
	}
	if got := say(kemi, "menu"); len(got) != 1 || strings.Contains(got[0].Text, "Track an order") {
		t.Errorf("money tool listed: %+v", got)
	}

	// Beta's number offers none of Alpha's tools.
	if got := w.sendTo(t, "/channels/whatsapp", pnB, waSecret, tolu, whatsapptest.Text("menu")); len(got) != 0 {
		t.Errorf("beta: %+v", got)
	}
	// Nor does a deploy of Beta's take Alpha's tools.
	if got := w.sendTo(t, "/channels/whatsapp", pnB, waSecret, tolu, whatsapptest.ButtonReply(btn, "Track it")); len(got) != 0 && !strings.Contains(got[0].Text, "not available") {
		t.Errorf("alpha's button on beta's number: %+v", got)
	}
}

// A tool's command may not be a public menu entry's (made tools T5): the
// entry is a customers' tool by it.
func TestCustomerToolsMenuCommandTaken(t *testing.T) {
	w := newWAWorld(t)
	w.graph.AddNumber(pnA, tokA, "+234 800 000 0001")
	alpha := w.tenant(t, "Alpha", "owner@custmenu.test")
	pay := alpha.must(201, "POST", "/v1/workflows", map[string]any{"name": "Pay supplier", "definition": json.RawMessage(waPayFlow)})["id"].(string)
	alpha.must(200, "POST", "/v1/workflows/"+pay+"/versions/1/publish", nil)
	alpha.must(200, "PUT", "/v1/whatsapp/number", map[string]any{"phone_number_id": pnA, "waba_id": "300000000000001", "access_token": tokA, "own_app": false})
	alpha.must(200, "PUT", "/v1/whatsapp/public-menu", map[string]any{"enabled": true, "workflows": []any{map[string]any{"workflow_id": pay, "label": "Track"}}})
	wf := alpha.must(201, "POST", "/v1/workflows", map[string]any{"name": "Track an order", "definition": json.RawMessage(custTrackDef)})["id"].(string)
	if e := alpha.must(409, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil); !strings.Contains(e["error"].(string), "public menu's Track") {
		t.Errorf("taken: %v", e)
	}
	// A new entry on the menu skips a command a tool has.
	def := strings.Replace(custTrackDef, `"command":"track"`, `"command":"follow"`, 1)
	wf2 := alpha.must(201, "POST", "/v1/workflows", map[string]any{"name": "Follow", "definition": json.RawMessage(def)})["id"].(string)
	alpha.must(200, "POST", "/v1/workflows/"+wf2+"/versions/1/publish", nil)
	v := alpha.must(200, "PUT", "/v1/whatsapp/public-menu", map[string]any{"enabled": true, "workflows": []any{
		map[string]any{"workflow_id": pay, "label": "Track"}, map[string]any{"workflow_id": wf, "label": "Follow"}}})
	if got := toJSON(v["public_workflows"]); !strings.Contains(got, `"command":"track"`) || !strings.Contains(got, `"command":"follow-2"`) {
		t.Errorf("commands: %s", got)
	}
}

// A customer in the app (made tools T5): in their chat with the business,
// its customers' tools (never a members' tool), a start that always asks
// yes, a confirm that starts one run as the customer, the answer from
// public outputs only, with a button to another customers' tool that only
// they may press; nothing for another person's chat or run, or once the
// business blocks them.
func TestCustomerToolsInTheApp(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	owner := w.owner
	publishFlow(t, owner, custStatusDef)
	publishFlow(t, owner, custTrackDef)
	publishFlow(t, owner, custRefundDef)
	tolu, _ := w.personIn(t, "+2348031110101")
	toluID := personID(t, tolu)
	id := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz, "from": "shop"})["id"].(string)
	ada, _ := w.personIn(t, "+2348031110102")
	adaChat := ada.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz, "from": "shop"})["id"].(string)

	tools := tolu.must(200, "GET", "/v1/me/chats/"+id+"/tools", nil)["tools"].([]any)
	if got := toJSON(tools); len(tools) != 2 || !strings.Contains(got, `"command":"order-status"`) || !strings.Contains(got, `"command":"track"`) ||
		strings.Contains(got, "give-back") || strings.Contains(got, "care") || !strings.Contains(got, `"Order number"`) {
		t.Fatalf("tools: %s", got)
	}
	if w.audits(t, "customer.tools", toluID) != 1 {
		t.Error("the list is not audited in the business's chain")
	}
	// A members' tool is not reachable, here or by the members' routes.
	tolu.must(404, "POST", "/v1/me/chats/"+id+"/tools/give-back/start", map[string]any{"input": map[string]any{"ref": "A-1"}})
	if st, _ := tolu.do("POST", "/v1/tools/order-status/start", map[string]any{"input": map[string]any{"ref": "A-1"}}); st != 403 && st != 404 {
		t.Errorf("members' route for a person: %d", st)
	}

	// Yes, always: this tool only reads.
	tolu.must(422, "POST", "/v1/me/chats/"+id+"/tools/order-status/start", map[string]any{"input": map[string]any{}})
	st := tolu.must(200, "POST", "/v1/me/chats/"+id+"/tools/order-status/start", map[string]any{"input": map[string]any{"ref": "A-100"}})
	if st["care"] != "yes" || st["confirm"] == nil || st["run_id"] != nil || toJSON(st["values"]) != `[{"label":"Order number","name":"ref","value":"A-100"}]` {
		t.Fatalf("start: %v", st)
	}
	confirm := st["confirm"].(string)
	tolu.must(409, "POST", "/v1/me/chats/"+id+"/tools/order-status/confirm", map[string]any{"confirm": confirm, "input": map[string]any{"ref": "A-999"}})
	ada.must(404, "POST", "/v1/me/chats/"+adaChat+"/tools/order-status/confirm", map[string]any{"confirm": confirm, "input": map[string]any{"ref": "A-100"}})
	first := tolu.must(201, "POST", "/v1/me/chats/"+id+"/tools/order-status/confirm", map[string]any{"confirm": confirm, "input": map[string]any{"ref": "A-100"}})
	again := tolu.must(200, "POST", "/v1/me/chats/"+id+"/tools/order-status/confirm", map[string]any{"confirm": confirm, "input": map[string]any{"ref": "A-100"}})
	run := first["run_id"].(string)
	if again["run_id"] != run {
		t.Errorf("confirmed twice: %v %v", first, again)
	}
	var startedBy string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT started_by FROM runs WHERE id = $1`, run).Scan(&startedBy); err != nil || startedBy != "customer:"+toluID.String() {
		t.Errorf("started by %q %v", startedBy, err)
	}
	if w.audits(t, "run.start", toluID) != 1 {
		t.Error("the start is not audited as the person")
	}
	w.env.Drain(t)

	a := tolu.must(200, "GET", "/v1/me/chats/"+id+"/runs/"+run+"/answer", nil)
	if a["status"] != "completed" || a["type"] != "card" || a["title"] != "On its way" || strings.Contains(toJSON(a), "SECRET") || a["tool"] != nil {
		t.Fatalf("answer: %v", a)
	}
	// Reading an answer is checked by the door, not written to the chain.
	if n := w.audits(t, "customer.answer", toluID); n != 0 {
		t.Errorf("answer reads audited: %d", n)
	}
	bs := a["buttons"].([]any)
	if len(bs) != 1 || bs[0].(map[string]any)["tool"] != "track" {
		t.Fatalf("buttons: %v", bs)
	}
	token := bs[0].(map[string]any)["token"].(string)
	// Someone else's run, and its button, are not theirs.
	ada.must(404, "GET", "/v1/me/chats/"+adaChat+"/runs/"+run+"/answer", nil)
	ada.must(404, "POST", "/v1/me/chats/"+adaChat+"/tools/buttons/"+token, nil)
	// Nor another's chat.
	ada.must(404, "GET", "/v1/me/chats/"+id+"/tools", nil)
	// A member pressing it as a members' button: no such tool for members.
	owner.must(404, "POST", "/v1/tools/buttons/"+token, nil)
	p := tolu.must(200, "POST", "/v1/me/chats/"+id+"/tools/buttons/"+token, nil)
	if p["command"] != "track" || toJSON(p["inputs"]) != `{"ref":"A-100"}` {
		t.Errorf("pressed: %v", p)
	}

	// The business blocks them: its tools are shut to them.
	thread := ""
	for _, c := range owner.must(200, "GET", "/v1/customer-chats", nil)["chats"].([]any) {
		if c.(map[string]any)["phone"] == "+2348031110101" {
			thread = c.(map[string]any)["id"].(string)
		}
	}
	owner.must(204, "POST", "/v1/customer-chats/"+thread+"/block", nil)
	if e := tolu.must(403, "GET", "/v1/me/chats/"+id+"/tools", nil); e["blocked"] != true {
		t.Errorf("blocked: %v", e)
	}
}

// custOrdersDef is a customers' tool that answers with a list of twelve
// orders, each row with a button to the track tool.
const custOrdersDef = `{"schema":"wd/v1","id":"wf_orders","version":1,"name":"My orders","trigger":{"type":"manual"},
	"inputs":{"schema":{"type":"object","properties":{}}},
	"outputs":[{"name":"orders","type":"list","title":"Orders","description":"Open orders","value":"=steps.look.output.orders","public":true},
	  {"name":"cost","type":"string","title":"Cost","description":"For staff","value":"=steps.look.output.cost"}],
	"tool":{"name":"My orders","command":"my-orders","about":"Your open orders","for":"customers",
	  "answer":{"type":"list","from":"orders","title":"Your orders","item":{"title":"=item.ref","sub":"=item.state"},
	    "actions":[{"label":"Track","tool":"track","inputs":{"ref":"=item.ref"}}]}},
	"steps":[{"id":"look","type":"transform","config":{"output":{"cost":"SECRET-COST","orders":[
	  {"ref":"A-1","state":"Packed"},{"ref":"A-2","state":"Packed"},{"ref":"A-3","state":"Packed"},{"ref":"A-4","state":"Packed"},
	  {"ref":"A-5","state":"Packed"},{"ref":"A-6","state":"Packed"},{"ref":"A-7","state":"Packed"},{"ref":"A-8","state":"Packed"},
	  {"ref":"A-9","state":"Packed"},{"ref":"A-10","state":"Packed"},{"ref":"A-11","state":"Packed"},{"ref":"A-12","state":"Packed"}]}}}]}`

// A customer's list answer on WhatsApp is a list message: its first ten
// rows, how many more, and a row chosen offers its buttons, to the
// customer it went to only.
func TestCustomerToolsWhatsAppList(t *testing.T) {
	w := newWAWorld(t)
	w.flows()
	w.graph.AddNumber(pnA, tokA, "+234 800 000 0001")
	alpha := w.tenant(t, "Alpha", "owner@custlist.test")
	publishFlow(t, alpha, custOrdersDef)
	publishFlow(t, alpha, custTrackDef)
	alpha.must(200, "PUT", "/v1/whatsapp/number", map[string]any{"phone_number_id": pnA, "waba_id": "300000000000001", "access_token": tokA, "own_app": false})
	alpha.must(200, "PUT", "/v1/whatsapp/public-menu", map[string]any{"enabled": true, "workflows": []any{}})
	const tolu, ada = "+2348099990201", "+2348099990202"
	say := func(from, text string) []whatsapptest.Sent {
		return w.sendTo(t, "/channels/whatsapp", pnA, waSecret, from, whatsapptest.Text(text))
	}
	if got := say(tolu, "menu"); len(got) != 1 || !strings.Contains(got[0].Text, "1. My orders") {
		t.Fatalf("menu: %+v", got)
	}
	// No inputs: straight to yes.
	if got := say(tolu, "1"); len(got) != 1 || !strings.Contains(got[0].Text, "Reply *yes*") {
		t.Fatalf("summary: %+v", got)
	}
	say(tolu, "yes")
	w.env.Drain(t)
	// Another customer's answer waits; their number is erased: its row
	// goes, and nothing is sent to it.
	say(ada, "menu")
	say(ada, "1")
	say(ada, "yes")
	w.env.Drain(t)
	secret := enrol(t, alpha)
	if out := alpha.must(200, "POST", "/v1/pii/erase", map[string]any{"category": "phone", "value": ada, "totp": codeAfter(secret, 1)}); out["saved"] != float64(1) {
		t.Errorf("erased: %v", out)
	}
	adaBefore := len(w.graph.Sent(ada))
	before := len(w.graph.Sent(tolu))
	w.tick(t)
	if n := len(w.graph.Sent(ada)) - adaBefore; n != 0 {
		t.Errorf("an erased number was told: %d", n)
	}
	got := w.graph.Sent(tolu)[before:]
	if len(got) != 1 || len(got[0].List) != 10 || !strings.Contains(got[0].Text, "*Your orders*") || !strings.Contains(got[0].Text, "…and 2 more.") ||
		strings.Contains(got[0].Text, "app") || strings.Contains(toJSON(got[0]), "SECRET") {
		t.Fatalf("list: %+v", got)
	}
	row := got[0].List[2]
	if row.Title != "A-3" || row.Description != "Packed" || !strings.HasPrefix(row.ID, "tr:") {
		t.Fatalf("row: %+v", row)
	}
	// Another customer choosing it is told it is not theirs.
	if got := w.sendTo(t, "/channels/whatsapp", pnA, waSecret, ada, whatsapptest.ListReply(row.ID, row.Title)); len(got) != 1 || !strings.Contains(got[0].Text, "sent to someone else") {
		t.Errorf("another's row: %+v", got)
	}
	// Its customer: the row's button.
	got = w.sendTo(t, "/channels/whatsapp", pnA, waSecret, tolu, whatsapptest.ListReply(row.ID, row.Title))
	if len(got) != 1 || !strings.Contains(got[0].Text, "*A-3* · Packed") || len(got[0].Buttons) != 1 || got[0].Buttons[0].Title != "Track" {
		t.Fatalf("row chosen: %+v", got)
	}
	// Pressed: the track tool, filled in, to yes.
	got = w.sendTo(t, "/channels/whatsapp", pnA, waSecret, tolu, whatsapptest.ButtonReply(got[0].Buttons[0].ID, "Track"))
	if len(got) != 1 || !strings.Contains(got[0].Text, "A-3") || len(got[0].Buttons) != 2 {
		t.Fatalf("pressed: %+v", got)
	}
}

// The roles rule holds for customers (made tools T5): a menu entry whose
// workflow names roles is never listed or started for a customer; a menu
// entry whose workflow declares a members' tool is that tool's members'
// alone; a menu entry never takes a tool's command, nor Taskiem's own
// words or the routes' "buttons"; answers are read without a chain row,
// and not without a limit.
func TestCustomerToolsRolesAndCommands(t *testing.T) {
	w := newDoorShopWorld(t)
	owner := w.owner
	ctx := context.Background()
	roled := strings.Replace(waPayFlow, `"trigger":{"type":"manual"},`, `"trigger":{"type":"manual"},"roles":["manager"],`, 1)
	roled = strings.Replace(roled, `"wf_pay"`, `"wf_roled"`, 1)
	staffOnly := publishFlow(t, owner, roled)
	members := publishFlow(t, owner, custRefundDef) // a members' tool, /give-back
	open := publishFlow(t, owner, strings.Replace(waPayFlow, `"wf_pay"`, `"wf_open"`, 1))
	track := publishFlow(t, owner, custTrackDef)
	w.graph.AddNumber(pnA, tokA, "+234 800 000 0001")
	owner.must(200, "PUT", "/v1/whatsapp/number", map[string]any{"phone_number_id": pnA, "waba_id": "300000000000001", "access_token": tokA, "own_app": false})
	v := owner.must(200, "PUT", "/v1/whatsapp/public-menu", map[string]any{"enabled": true, "workflows": []any{
		map[string]any{"workflow_id": staffOnly, "label": "Managers"},
		map[string]any{"workflow_id": members, "label": "Give back"},
		map[string]any{"workflow_id": open, "label": "Buttons"},
	}})
	got := toJSON(v["public_workflows"])
	// "give-back" is the members' tool's: the entry takes the next; Buttons
	// is a route's word.
	if !strings.Contains(got, `"command":"give-back-2"`) || !strings.Contains(got, `"command":"buttons-2"`) || !strings.Contains(got, `"command":"managers"`) {
		t.Fatalf("commands: %s", got)
	}
	// And a menu entry never takes one of Taskiem's own words.
	owner.must(200, "PUT", "/v1/whatsapp/public-menu", map[string]any{"enabled": true, "workflows": []any{
		map[string]any{"workflow_id": staffOnly, "label": "Managers"},
		map[string]any{"workflow_id": members, "label": "Give back"},
		map[string]any{"workflow_id": open, "label": "Buttons"},
		map[string]any{"workflow_id": track, "label": "Order"},
	}})
	var cmd string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT command FROM whatsapp_public_workflows WHERE label = 'Order'`).Scan(&cmd); err != nil || cmd != "order-2" {
		t.Errorf("a reserved word: %q %v", cmd, err)
	}
	// A tool may not be called buttons either.
	if v := owner.must(200, "POST", "/v1/validate", map[string]any{"definition": json.RawMessage(strings.Replace(custTrackDef, `"command":"track"`, `"command":"buttons"`, 1))}); v["valid"] != false {
		t.Errorf("a tool called buttons: %v", v)
	}

	tolu, _ := w.personIn(t, "+2348031110201")
	toluID := personID(t, tolu)
	id := tolu.must(201, "POST", "/v1/me/chats", map[string]any{"business_id": w.biz, "from": "shop"})["id"].(string)
	tools := toJSON(tolu.must(200, "GET", "/v1/me/chats/"+id+"/tools", nil)["tools"])
	// Only the open entry, and the customers' tool; never the one with
	// roles, nor the one whose workflow is a members' tool.
	if !strings.Contains(tools, `"command":"buttons-2"`) || !strings.Contains(tools, `"command":"track"`) || strings.Contains(tools, "managers") || strings.Contains(tools, "give-back") {
		t.Fatalf("tools: %s", tools)
	}
	if e := tolu.must(409, "POST", "/v1/me/chats/"+id+"/tools/managers/start", map[string]any{"input": map[string]any{"amount": 5, "account_number": "0123456789", "currency": "NGN"}}); e["not_offered"] != true {
		t.Errorf("roles at start: %v", e)
	}
	tolu.must(404, "POST", "/v1/me/chats/"+id+"/tools/give-back-2/start", map[string]any{"input": map[string]any{}})
	tolu.must(404, "POST", "/v1/me/chats/"+id+"/tools/give-back/start", map[string]any{"input": map[string]any{}})
	// The menu entry called buttons-2 starts as itself, not as a button.
	if st := tolu.must(200, "POST", "/v1/me/chats/"+id+"/tools/buttons-2/start", map[string]any{"input": map[string]any{"amount": 5, "account_number": "0123456789", "currency": "NGN"}}); st["confirm"] == nil {
		t.Errorf("buttons-2: %v", st)
	}

	// Reading an answer: no chain row, and a limit.
	run := uuidLike()
	limited := false
	for range 30 {
		if st, _ := tolu.do("GET", "/v1/me/chats/"+id+"/runs/"+run+"/answer", nil); st == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Error("answer reads are not limited")
	}
	if n := w.audits(t, "customer.answer", toluID); n != 0 {
		t.Errorf("answer reads audited: %d", n)
	}
}

func uuidLike() string { return "0193aaaa-0000-7000-8000-000000000001" }
