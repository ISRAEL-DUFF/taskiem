package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/connectors/riders"
	"github.com/israel-duff/taskiem/engine/dispatch"
	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// Device fields D1 (docs/spec/device-fields.md §14, as built D1): a form
// step put to a person in WhatsApp and Telegram, answered with the chat's
// own location and photo messages; require_source held as unverified; a
// form step to the person working a business link, answered in the app
// through the door; and the delivery proved both ways, on the Confirm card.

// doorJPEG is enough of a photo for the content check (the store is a fake).
var doorJPEG = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00photo of a parcel at a door")

// askInChat asks one member for where and a photo; the signature is
// optional and a chat cannot give one.
const askInChat = `{"schema":"wd/v1","id":"wf_handover","version":1,"name":"Handover","trigger":{"type":"manual"},
 "inputs":{"schema":{"type":"object","properties":{"rider":{"type":"string"}}}},
 "steps":[{"id":"handover","type":"form","config":{"title":"Delivered","to":{"member":"=trigger.body.rider"},
   "schema":{"type":"object","required":["where","proof"],"properties":{
    "where":{"type":"location","why":"so the delivery is recorded where it was handed over"},
    "proof":{"type":"photo","why":"a photo of the parcel at the door"},
    "signed":{"type":"signature","why":"if the customer will sign for it"}}}}},
  {"id":"done","type":"transform","needs":["handover"],"config":{"output":{
    "where":"=steps.handover.output.where.source","proof":"=steps.handover.output.proof.source",
    "state":"=steps.handover.output.where.state","signed":"=steps.handover.output.signed.state","app":"=steps.handover.output.device.app"}}}]}`

func (w *reachWorld) waSend(t *testing.T, from string, msg map[string]any) []whatsapptest.Sent {
	t.Helper()
	before := len(w.graph.Sent(from))
	body := whatsapptest.Delivery(waPNID, from, fmt.Sprintf("wamid.d1%d", w.seq.Add(1)), msg)
	w.post(t, w.wa, body, "X-Hub-Signature-256", whatsapp.Sign(waSecret, body))
	return w.graph.Sent(from)[before:]
}

func (w *reachWorld) linkTG(t *testing.T, c *client, account int64) {
	t.Helper()
	made := c.must(201, "POST", "/v1/me/telegram", map[string]any{"password": testPassword})
	u, _ := url.Parse(made["url"].(string))
	got := w.telegram(t, account, func(id int64) []byte { return telegramtest.Start(id, account, "rider", u.Query().Get("start")) })
	if len(got) != 1 || !strings.Contains(got[0].Text, "now linked") {
		t.Fatalf("linking Telegram: %+v", got)
	}
}

func deviceWorld(t *testing.T) *reachWorld {
	w := newReachWorld(t)
	w.forms.Files, w.forms.Scanner, w.forms.AppURL = noFiles{}, okScanner{}, "https://app.taskiem.test"
	w.forms.Ask = w.srv.FormAsk
	return w
}

func lastText(sent []whatsapptest.Sent) string {
	if len(sent) == 0 {
		return ""
	}
	return sent[len(sent)-1].Text
}

// doneOutput is the done step's output of a run.
func doneOutput(t *testing.T, owner *client, run string) map[string]any {
	t.Helper()
	got := owner.must(200, "GET", "/v1/runs/"+run, nil)
	if got["run"].(map[string]any)["status"] != "completed" {
		t.Fatalf("run: %v", got["run"])
	}
	for _, e := range got["events"].([]any) {
		ev := e.(map[string]any)
		if ev["step_id"] == "done" && ev["type"] == "StepCompleted" {
			return ev["payload"].(map[string]any)["output"].(map[string]any)
		}
	}
	t.Fatalf("no done step: %s", toJSON(got["events"]))
	return nil
}

func TestFormStepAnsweredInWhatsAppAndTelegram(t *testing.T) {
	w := deviceWorld(t)
	ctx := context.Background()
	owner := w.tenant(t, "Acme", "owner@acme.test")
	rider := addMember(t, w.world, owner, "rider", "viewer")
	const number = "+2348031112233"
	w.bindWA(t, rider, number)
	wf := publishFlow(t, owner, askInChat)

	// Asked by name: under To fill in, and in their WhatsApp with how to answer.
	run := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"rider": userID(t, rider).String()}})["run_id"].(string)
	w.env.Drain(t)
	if err := w.forms.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	ask := w.graph.Last(t, number).Text
	if !strings.Contains(ask, "To fill in: Delivered") || !strings.Contains(ask, "Send your location") || !strings.Contains(ask, "A signature cannot be given in a chat") {
		t.Fatalf("the ask: %q", ask)
	}
	// Words that are not an answer go on to the chat as before.
	// The location first: kept, the photo asked for.
	if got := lastText(w.waSend(t, number, whatsapptest.Location(6.452131, 3.395812))); !strings.Contains(got, "Now send the photo") {
		t.Fatalf("after the location: %q", got)
	}
	w.graph.AddMedia("m-door", "image/jpeg", doorJPEG, int64(len(doorJPEG)))
	if got := lastText(w.waSend(t, number, whatsapptest.Image("m-door", "image/jpeg", ""))); !strings.Contains(got, "Sent to Acme: Delivered") {
		t.Fatalf("after the photo: %q", got)
	}
	w.env.Drain(t)
	out := doneOutput(t, owner, run)
	if out["where"] != "sent" || out["proof"] != "sent" || out["state"] != "answered" || out["signed"] != "unavailable" || out["app"] != "whatsapp" {
		t.Fatalf("whatsapp answer: %v", out)
	}
	// The pin is sealed in the history; nothing of it is kept for the chat.
	var raw []byte
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT string_agg(payload::text, '') FROM run_events WHERE run_id = $1`, run).Scan(&raw)
	if bytes.Contains(raw, []byte("6.452131")) {
		t.Fatalf("the pin in the history: %s", raw)
	}
	var held int
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM form_chat_answers`).Scan(&held)
	if held != 0 {
		t.Fatalf("%d chat answers left", held)
	}

	// Telegram: the same question answered with Telegram's location and photo.
	const account = 7001
	w.linkTG(t, rider, account)
	run = owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"rider": userID(t, rider).String()}})["run_id"].(string)
	w.env.Drain(t)
	if err := w.forms.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	w.bot.SetFile("tg-door", "photos/door.jpg", doorJPEG, int64(len(doorJPEG)))
	w.telegram(t, account, func(id int64) []byte { return telegramtest.PhotoMessage(id, account, "tg-door", "") })
	sent := w.telegram(t, account, func(id int64) []byte { return telegramtest.LocationMessage(id, account, 6.4521, 3.3958) })
	if len(sent) == 0 || !strings.Contains(sent[len(sent)-1].Text, "Sent to Acme: Delivered") {
		t.Fatalf("telegram: %+v", sent)
	}
	w.env.Drain(t)
	if out := doneOutput(t, owner, run); out["where"] != "sent" || out["proof"] != "sent" || out["app"] != "telegram" {
		t.Fatalf("telegram answer: %v", out)
	}

	// require_source: a pin sent is held, the person offered the app, then
	// sent as it is, unverified.
	strict := strings.Replace(strings.Replace(askInChat, `"why":"so the delivery is recorded where it was handed over"`, `"why":"so the delivery is recorded where it was handed over","require_source":"device"`, 1), "wf_handover", "wf_strict", 1)
	wf2 := publishFlow(t, owner, strict)
	run = owner.must(201, "POST", "/v1/workflows/"+wf2+"/runs", map[string]any{"input": map[string]any{"rider": userID(t, rider).String()}})["run_id"].(string)
	w.env.Drain(t)
	if err := w.forms.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if ask := w.graph.Last(t, number).Text; !strings.Contains(ask, "marked unverified unless you answer in the app") {
		t.Fatalf("the strict ask: %q", ask)
	}
	w.waSend(t, number, whatsapptest.Image("m-door", "image/jpeg", ""))
	got := w.waSend(t, number, whatsapptest.Location(6.45, 3.39))
	if len(got) != 1 || !strings.Contains(got[0].Text, "wants your phone's own reading") || !strings.Contains(got[0].Text, "https://app.taskiem.test/forms/requests/") ||
		len(got[0].Buttons) != 1 || !strings.HasPrefix(got[0].Buttons[0].ID, "fq:send:") {
		t.Fatalf("held: %+v", got)
	}
	// Not sent yet: the request still waits for the app.
	if reqs := rider.must(200, "GET", "/v1/form-requests", nil)["requests"].([]any); len(reqs) != 1 {
		t.Fatalf("waiting: %v", reqs)
	}
	if got := lastText(w.press(t, number, got[0].Buttons[0].ID)); !strings.Contains(got, "Sent to Acme") {
		t.Fatalf("sent as it is: %q", got)
	}
	w.env.Drain(t)
	if out := doneOutput(t, owner, run); out["where"] != "sent" || out["state"] != "unverified" {
		t.Fatalf("unverified: %v", out)
	}
}

// proveDelivery is the delivery fixture of device fields §6: the rider
// asked through the link, the proof recorded on the delivery.
const proveDelivery = `{"schema":"wd/v1","id":"wf_prove","version":1,"name":"Prove a delivery","trigger":{"type":"manual"},
 "inputs":{"schema":{"type":"object","required":["reference","rider"],"properties":{"reference":{"type":"string"},"rider":{"type":"string"}}}},
 "steps":[{"id":"handover","type":"form","config":{"title":"Delivered","to":{"link":"=trigger.body.rider"},
   "schema":{"type":"object","required":["where","proof"],"properties":{
    "where":{"type":"location","why":"so the delivery is recorded where it was handed over","max_accuracy_m":100},
    "proof":{"type":"photo","why":"a photo of the parcel at the door, for the shop's records"},
    "signed":{"type":"signature","why":"if the customer will sign for it"}}},
   "timeout":"2h","on_timeout":"continue"}},
  {"id":"record","type":"connector","connector":"riders@1","action":"record_proof","needs":["handover"],
   "input":{"reference":"=trigger.body.reference","where":"=steps.handover.output.where","photo":"=steps.handover.output.proof"}},
  {"id":"done","type":"transform","needs":["record"],"config":{"output":{"mark":"=steps.record.output.mark",
    "kind":"=steps.handover.output.respondent.kind","app":"=steps.handover.output.device.app"}}}]}`

func TestDeliveryProvedOnWhatsAppAndInTheApp(t *testing.T) {
	ctx := context.Background()
	w, ada := newDispatchWorld(t, "Ada Stores")
	w.forms.Files, w.forms.Scanner, w.forms.AppURL = noFiles{}, okScanner{}, "https://app.taskiem.test"
	w.forms.Ask = w.srv.FormAsk
	ops := &riders.Late{}
	ops.Set(w.srv.Riders)
	ops.SetDispatch(w.srv.Dispatch)
	if err := w.env.Registry.Register(riders.New(ops)); err != nil {
		t.Fatal(err)
	}
	musaBiz := w.tenant(t, "Musa Rides", "musa@rides.test")
	musaID := tenantOf(t, musaBiz)
	musaBiz.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	ada.must(201, "POST", "/v1/links", map[string]any{"business": musaID, "role": "supplier"})
	l := musaBiz.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)
	musaBiz.must(200, "POST", "/v1/links/"+l["id"].(string)+"/accept", nil)
	w.srv.WaitBackground()
	link := l["id"].(string)
	const musa = "+2348031110071"
	path := strings.TrimPrefix(ada.must(200, "GET", "/v1/riders", nil)["tap_in_url"].(string), "https://"+reachFormHost)
	loc, _ := url.Parse(w.page(t, path+"/whatsapp", nil).Header().Get("Location"))
	w.whatsapp(t, musa, loc.Query().Get("text"))
	m := ada.must(201, "POST", "/v1/riders", map[string]any{"name": "Musa Bello", "phone": musa, "zones": []string{"Lekki"}, "fee_kobo": 100000})["rider"].(map[string]any)
	ada.must(200, "PATCH", "/v1/riders/"+m["id"].(string), map[string]any{"link_id": link})
	// Musa is on Taskiem: his own business's chat is his WhatsApp too.
	w.bindWA(t, musaBiz, musa)
	ada.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	deliver := func() string {
		t.Helper()
		ref := w.order(t, "none")
		ada.must(200, "POST", "/v1/storefront/orders/"+ref+"/offer", map[string]any{})
		d := ada.must(200, "GET", "/v1/storefront/orders/"+ref+"/deliveries", nil)["deliveries"].([]any)[0].(map[string]any)
		for _, a := range []string{dispatch.Accept, dispatch.PickedUp, dispatch.Arrived, dispatch.Delivered} {
			if _, err := w.srv.Dispatch.Press(ctx, w.biz, uuid.MustParse(d["id"].(string)), a); err != nil {
				t.Fatalf("%s: %v", a, err)
			}
		}
		return ref
	}
	confirmCard := func(ref string) map[string]any {
		t.Helper()
		for _, a := range ada.must(200, "GET", "/v1/chat-alerts", nil)["alerts"].([]any) {
			if x := a.(map[string]any); x["kind"] == "confirm" && x["reference"] == ref {
				return x
			}
		}
		t.Fatalf("no Confirm card for %s", ref)
		return nil
	}
	wf := publishFlow(t, ada, proveDelivery)
	ask := func(ref string) string {
		t.Helper()
		run := ada.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"reference": ref, "rider": link}})["run_id"].(string)
		w.env.Drain(t)
		if err := w.forms.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		return run
	}

	// 1. On WhatsApp: asked in Musa's own chat, answered with a pin and a photo.
	ref := deliver()
	run := ask(ref)
	if got := w.graph.Last(t, musa).Text; !strings.Contains(got, "Ada Stores asks: Delivered") || !strings.Contains(got, "Send a photo here") {
		var note *string
		var sent *string
		_ = w.env.DB.Admin.QueryRow(ctx, `SELECT note, sent_at::text FROM form_requests WHERE run_id = $1`, run).Scan(&note, &sent)
		t.Fatalf("Musa's ask: %q (note %v, sent %v) %s", got, fmtPtr(note), fmtPtr(sent), toJSON(ada.must(200, "GET", "/v1/runs/"+run, nil)))
	}
	w.waSend(t, musa, whatsapptest.Location(6.4281, 3.4219))
	w.graph.AddMedia("m-parcel", "image/jpeg", doorJPEG, int64(len(doorJPEG)))
	if got := lastText(w.waSend(t, musa, whatsapptest.Image("m-parcel", "image/jpeg", ""))); !strings.Contains(got, "Sent to Ada Stores: Delivered") {
		t.Fatalf("after the photo: %q", got)
	}
	w.env.Drain(t)
	if out := doneOutput(t, ada, run); out["mark"] != dispatch.MarkSaidByRider || out["kind"] != "linked" || out["app"] != "whatsapp" {
		t.Fatalf("on WhatsApp: %v", out)
	}
	card := confirmCard(ref)
	proof, _ := card["proof"].(map[string]any)
	where, _ := proof["where"].(map[string]any)
	if proof == nil || proof["mark"] != "said_by_rider" || proof["photo_source"] != "sent" || where["source"] != "sent" || where["lat"] != 6.428 ||
		!strings.HasPrefix(fmt.Sprint(proof["photo_url"]), "https://") || !strings.Contains(card["line"].(string), "location sent by the rider · photo sent") {
		t.Fatalf("the Confirm card: %v", card)
	}

	// 2. In the app: the same question listed for Musa from Ada, answered
	// with the phone's reading and the camera's photo, through the link.
	ref = deliver()
	run = ask(ref)
	reqs := musaBiz.must(200, "GET", "/v1/form-requests", nil)["requests"].([]any)
	if len(reqs) != 1 || reqs[0].(map[string]any)["from"] != "Ada Stores" {
		t.Fatalf("Musa's To fill in: %v", reqs)
	}
	rid := reqs[0].(map[string]any)["id"].(string)
	if one := musaBiz.must(200, "GET", "/v1/form-requests/"+rid, nil); one["title"] != "Delivered" || one["from"] != "Ada Stores" {
		t.Fatalf("the request: %v", one)
	}
	ada.must(404, "GET", "/v1/form-requests/"+rid, nil) // the asker's owner is not the one asked
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	answers, _ := json.Marshal(map[string]any{"where": map[string]any{"lat": 6.4281, "lng": 3.4219, "accuracy_m": 8}})
	device, _ := json.Marshal(map[string]any{"app": "app", "version": "3.0.0", "online": true})
	_ = mw.WriteField("answers", string(answers))
	_ = mw.WriteField("device", string(device))
	fw, _ := mw.CreateFormFile("proof", "door.jpg")
	_, _ = fw.Write(doorJPEG)
	_ = mw.Close()
	if code, res := musaBiz.rawPost(t, "/v1/form-requests/"+rid+"/answers", mw.FormDataContentType(), b.Bytes()); code != 201 {
		t.Fatalf("answered in the app: %d %v", code, res)
	}
	w.env.Drain(t)
	if out := doneOutput(t, ada, run); out["mark"] != dispatch.MarkReadByPhone || out["app"] != "app" {
		t.Fatalf("in the app: %v", out)
	}
	if n := len(musaBiz.must(200, "GET", "/v1/form-requests", nil)["requests"].([]any)); n != 0 {
		t.Fatalf("%d still to fill in", n)
	}
	card = confirmCard(ref)
	proof = card["proof"].(map[string]any)
	if proof["photo_source"] != "camera" || proof["where"].(map[string]any)["source"] != "device" ||
		!strings.Contains(card["line"].(string), "location read by the phone · photo taken in the app") {
		t.Fatalf("the Confirm card from the app: %v", card)
	}
	// Answered once: a second answer is refused.
	musaBiz.must(409, "POST", "/v1/form-requests/"+rid+"/answers", map[string]any{"answers": map[string]any{}})

	// 3. A photo is a file sent with the answer: the earlier photo's id,
	// named in the answer's body, is refused, not taken as "taken in the app".
	var oldPhoto string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id::text FROM form_files ORDER BY created_at DESC LIMIT 1`).Scan(&oldPhoto); err != nil {
		t.Fatal(err)
	}
	ref = deliver()
	ask(ref)
	reqs = musaBiz.must(200, "GET", "/v1/form-requests", nil)["requests"].([]any)
	if len(reqs) != 1 || reqs[0].(map[string]any)["run_id"] != uuid.Nil.String() {
		t.Fatalf("the asker's run is not the linked side's to know: %v", reqs)
	}
	rid = reqs[0].(map[string]any)["id"].(string)
	code, res := musaBiz.do("POST", "/v1/form-requests/"+rid+"/answers", map[string]any{"device": map[string]any{"app": "app"},
		"answers": map[string]any{"where": map[string]any{"lat": 6.4281, "lng": 3.4219, "accuracy_m": 8},
			"proof": map[string]any{"id": oldPhoto, "name": "door.jpg", "type": "image/jpeg", "sha256": strings.Repeat("ab", 32), "size": len(doorJPEG)}}})
	if code != 422 || !strings.Contains(fmt.Sprint(res["error"]), "is sent as a file with the answer") {
		t.Fatalf("a reused photo: %d %v", code, res)
	}
	// Nor can a workflow record another run's photo as this delivery's.
	forge := publishFlow(t, ada, `{"schema":"wd/v1","id":"wf_forge","version":1,"name":"Forge","trigger":{"type":"manual"},
	 "inputs":{"schema":{"type":"object","properties":{"reference":{"type":"string"},"photo":{"type":"string"}}}},
	 "steps":[{"id":"record","type":"connector","connector":"riders@1","action":"record_proof",
	   "input":{"reference":"=trigger.body.reference","photo":{"state":"answered","id":"=trigger.body.photo","source":"camera"}}}]}`)
	frun := ada.must(201, "POST", "/v1/workflows/"+forge+"/runs", map[string]any{"input": map[string]any{"reference": ref, "photo": oldPhoto}})["run_id"].(string)
	w.env.Drain(t)
	if got := ada.must(200, "GET", "/v1/runs/"+frun, nil); got["run"].(map[string]any)["status"] != "failed" || !strings.Contains(toJSON(got["events"]), "no such photo answered to this run") {
		t.Fatalf("recording another run's photo: %s", toJSON(got))
	}

	// 4. The link ends: the request is no longer Musa's to list, read or
	// answer, in the app or in his chat.
	musaBiz.must(200, "POST", "/v1/links/"+link+"/end", nil)
	w.srv.WaitBackground()
	if n := len(musaBiz.must(200, "GET", "/v1/form-requests", nil)["requests"].([]any)); n != 0 {
		t.Fatalf("listed after the link ended: %d", n)
	}
	musaBiz.must(404, "GET", "/v1/form-requests/"+rid, nil)
	musaBiz.must(404, "POST", "/v1/form-requests/"+rid+"/answers", map[string]any{"answers": map[string]any{"where": map[string]any{"lat": 6.4281, "lng": 3.4219}}})
	if got := lastText(w.waSend(t, musa, whatsapptest.Location(6.4281, 3.4219))); strings.Contains(got, "Delivered") {
		t.Fatalf("answered in chat after the link ended: %q", got)
	}
	var drafts int
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM form_chat_answers WHERE request_id = $1`, rid).Scan(&drafts)
	if drafts != 0 {
		t.Fatalf("%d drafts kept after the link ended", drafts)
	}
}

func fmtPtr(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
