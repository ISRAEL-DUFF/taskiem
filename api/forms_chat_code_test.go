package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// A stock code typed on WhatsApp (build-plan-gaps H3, g-scan-wa): a form
// step's scan is asked there as words to type, and a message in that chat
// within ten minutes that can be a code is taken as it: never a command
// word, never a menu's number, never once the ten minutes are up. With a location, the code is asked
// once the location is in.
const countInChat = `{"schema":"wd/v1","id":"wf_countchat","version":1,"name":"Count","trigger":{"type":"manual"},
 "inputs":{"schema":{"type":"object","properties":{"keeper":{"type":"string"}}}},
 "steps":[{"id":"count","type":"form","config":{"title":"Count the shelf","to":{"member":"=trigger.body.keeper"},
   "schema":{"type":"object","required":["item"],"properties":{
    "item":{"type":"scan","why":"the code on the item's pack","formats":["ean_13","upc_a"]}}}}},
  {"id":"done","type":"transform","needs":["count"],"config":{"output":{
    "source":"=steps.count.output.item.source","state":"=steps.count.output.item.state","text":"=steps.count.output.item.text","app":"=steps.count.output.device.app"}}}]}`

func TestStockCodeTypedOnWhatsApp(t *testing.T) {
	w := deviceWorld(t)
	ctx := context.Background()
	owner := w.tenant(t, "Acme", "owner@codes.test")
	keeper := addMember(t, w.world, owner, "keeper", "viewer")
	const number = "+2348031114455"
	w.bindWA(t, keeper, number)
	wf := publishFlow(t, owner, countInChat)
	ask := func(flow string) string {
		t.Helper()
		run := owner.must(201, "POST", "/v1/workflows/"+flow+"/runs", map[string]any{"input": map[string]any{"keeper": userID(t, keeper).String()}})["run_id"].(string)
		w.env.Drain(t)
		if err := w.forms.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		return run
	}
	pending := func(run string) bool {
		var n int
		_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM form_requests WHERE run_id = $1 AND status = 'pending'`, run).Scan(&n)
		return n == 1
	}

	// Asked as words to type, with why.
	run := ask(wf)
	if got := w.graph.Last(t, number).Text; !strings.Contains(got, "To fill in: Count the shelf") || !strings.Contains(got, "Type the code as your next message here (the code on the item's pack)") {
		t.Fatalf("the ask: %q", got)
	}
	// A command word is not the code; the asking stays, so the digits
	// after it, still within its ten minutes, are (H3 review).
	w.waSend(t, number, whatsapptest.Text("status"))
	if !pending(run) {
		t.Fatal("a command word was taken as the code")
	}
	if got := lastText(w.waSend(t, number, whatsapptest.Text("6151100093217"))); !strings.Contains(got, "Sent to Acme: Count the shelf") {
		t.Fatalf("the code: %q", got)
	}
	w.env.Drain(t)
	out := doneOutput(t, owner, run)
	if out["source"] != "typed" || out["state"] != "answered" || out["text"] != "6151100093217" || out["app"] != "whatsapp" {
		t.Fatalf("the answer: %v", out)
	}

	// With a location: the location first, then the code asked, then taken.
	withPlace := strings.Replace(countInChat, `"required":["item"],"properties":{`, `"required":["item","where"],"properties":{"where":{"type":"location","why":"which shop"},`, 1)
	withPlace = strings.Replace(withPlace, `"id":"wf_countchat"`, `"id":"wf_countplace"`, 1)
	run = ask(publishFlow(t, owner, withPlace))
	if got := w.graph.Last(t, number).Text; !strings.Contains(got, "Send your location") || !strings.Contains(got, "Then type the code, when asked") {
		t.Fatalf("the ask with a place: %q", got)
	}
	if got := lastText(w.waSend(t, number, whatsapptest.Location(6.45, 3.39))); !strings.Contains(got, "Now type the code as your next message") {
		t.Fatalf("after the location: %q", got)
	}
	if got := lastText(w.waSend(t, number, whatsapptest.Text("036000291452"))); !strings.Contains(got, "Sent to Acme") {
		t.Fatalf("the code after the place: %q", got)
	}
	w.env.Drain(t)
	if out := doneOutput(t, owner, run); out["text"] != "036000291452" || out["source"] != "typed" {
		t.Fatalf("with a place: %v", out)
	}

	// Past its ten minutes, a message is not the code: the person moved
	// on, or is talking to another business on the same number.
	late := ask(wf)
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE form_chat_answers SET asked_at = now() - interval '11 minutes' WHERE asking IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	w.waSend(t, number, whatsapptest.Text("6151100093217"))
	if !pending(late) {
		t.Fatal("a code typed after the asking ended was taken")
	}

	// A menu waiting for a number keeps its number: digits are not the code.
	w.waSend(t, number, whatsapptest.Text("help"))
	run = ask(wf)
	w.waSend(t, number, whatsapptest.Text("2"))
	if !pending(run) {
		t.Fatal("a menu's number was taken as the code")
	}
}
