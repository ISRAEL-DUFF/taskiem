package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/connectors/riders"
	"github.com/israel-duff/taskiem/engine/dispatch"
	"github.com/israel-duff/taskiem/engine/media"
)

// Device fields D2 (docs/spec/device-fields.md §16): a customer signs for
// a delivery on the rider's phone. The §6 step asks the signature with the
// signer's name; the app sends the pad's PNG with the answer; record_proof
// keeps it beside the photo; the Confirm card says it was signed; the
// signer's name is sealed in the run, and the audit keeps how each field
// was answered, never what.

// signPNG is a pad's drawing, enough of a PNG for the content check.
var signPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x01\x00\x00\x00\x00\x80\x08\x06\x00\x00\x00 strokes")

// sniffedFiles is the fake store, keeping each file's type as its content
// says (as the real one does), so a signature is a PNG and a photo a JPEG.
type sniffedFiles struct{ noFiles }

func (sniffedFiles) PutFile(_ context.Context, _ uuid.UUID, data []byte) (media.File, error) {
	t := http.DetectContentType(data)
	return media.File{Path: "files/" + strings.ReplaceAll(t, "/", "."), Type: t, Size: int64(len(data))}, nil
}

const proveSigned = `{"schema":"wd/v1","id":"wf_signed","version":1,"name":"Prove a delivery, signed","trigger":{"type":"manual"},
 "inputs":{"schema":{"type":"object","required":["reference","rider"],"properties":{"reference":{"type":"string"},"rider":{"type":"string"}}}},
 "steps":[{"id":"handover","type":"form","config":{"title":"Delivered","to":{"link":"=trigger.body.rider"},
   "schema":{"type":"object","required":["where","proof"],"properties":{
    "where":{"type":"location","why":"so the delivery is recorded where it was handed over","max_accuracy_m":100},
    "proof":{"type":"photo","why":"a photo of the parcel at the door, for the shop's records"},
    "signed":{"type":"signature","why":"if the customer will sign for it","ask_name":true}}},
   "timeout":"2h","on_timeout":"continue"}},
  {"id":"record","type":"connector","connector":"riders@1","action":"record_proof","needs":["handover"],
   "input":{"reference":"=trigger.body.reference","where":"=steps.handover.output.where","photo":"=steps.handover.output.proof","signature":"=steps.handover.output.signed"}},
  {"id":"done","type":"transform","needs":["record"],"config":{"output":{"mark":"=steps.record.output.mark",
    "signed":"=steps.handover.output.signed.state","source":"=steps.handover.output.signed.source"}}}]}`

func TestCustomerSignsOnTheRidersPhone(t *testing.T) {
	ctx := context.Background()
	w, ada := newDispatchWorld(t, "Ada Stores")
	w.forms.Files, w.forms.Scanner, w.forms.AppURL = sniffedFiles{}, okScanner{}, "https://app.taskiem.test"
	w.forms.Ask = w.srv.FormAsk
	ops := &riders.Late{}
	ops.Set(w.srv.Riders)
	ops.SetDispatch(w.srv.Dispatch)
	if err := w.env.Registry.Register(riders.New(ops)); err != nil {
		t.Fatal(err)
	}
	musaBiz := w.tenant(t, "Musa Rides", "musa@signs.test")
	musaID := tenantOf(t, musaBiz)
	musaBiz.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	ada.must(201, "POST", "/v1/links", map[string]any{"business": musaID, "role": "supplier"})
	l := musaBiz.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)
	musaBiz.must(200, "POST", "/v1/links/"+l["id"].(string)+"/accept", nil)
	w.srv.WaitBackground()
	link := l["id"].(string)
	const musa = "+2348031110072"
	path := strings.TrimPrefix(ada.must(200, "GET", "/v1/riders", nil)["tap_in_url"].(string), "https://"+reachFormHost)
	loc, _ := url.Parse(w.page(t, path+"/whatsapp", nil).Header().Get("Location"))
	w.whatsapp(t, musa, loc.Query().Get("text"))
	m := ada.must(201, "POST", "/v1/riders", map[string]any{"name": "Musa Bello", "phone": musa, "zones": []string{"Lekki"}, "fee_kobo": 100000})["rider"].(map[string]any)
	ada.must(200, "PATCH", "/v1/riders/"+m["id"].(string), map[string]any{"link_id": link})
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
	var rid string
	ask := func(wf, ref string) string {
		t.Helper()
		run := ada.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"reference": ref, "rider": link}})["run_id"].(string)
		w.env.Drain(t)
		if err := w.forms.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		rid = musaBiz.must(200, "GET", "/v1/form-requests", nil)["requests"].([]any)[0].(map[string]any)["id"].(string)
		return run
	}
	ref := deliver()
	run := ask(publishFlow(t, ada, proveSigned), ref)

	answer := func(signed map[string]any, sig []byte) (int, map[string]any) {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		answers, _ := json.Marshal(map[string]any{"where": map[string]any{"lat": 6.4281, "lng": 3.4219, "accuracy_m": 8}, "signed": signed})
		_ = mw.WriteField("answers", string(answers))
		_ = mw.WriteField("device", `{"app":"app","version":"3.1.0","online":true}`)
		fw, _ := mw.CreateFormFile("proof", "door.jpg")
		_, _ = fw.Write(doorJPEG)
		fw, _ = mw.CreateFormFile("signed", "signature.png")
		_, _ = fw.Write(sig)
		_ = mw.Close()
		return musaBiz.rawPost(t, "/v1/form-requests/"+rid+"/answers", mw.FormDataContentType(), b.Bytes())
	}
	// A JPEG sent as the signature, and a signature with no name, are refused.
	if code, res := answer(map[string]any{"signed_by": "Ngozi Eze"}, doorJPEG); code != 422 || !strings.Contains(toJSON(res), "a signature is a PNG drawing") {
		t.Fatalf("a JPEG as the signature: %d %v", code, res)
	}
	if code, res := answer(map[string]any{}, signPNG); code != 422 || !strings.Contains(toJSON(res), "say who signed") {
		t.Fatalf("no name: %d %v", code, res)
	}
	if code, res := answer(map[string]any{"signed_by": "Ngozi Eze"}, signPNG); code != 201 {
		t.Fatalf("signed: %d %v", code, res)
	}
	w.env.Drain(t)
	if out := doneOutput(t, ada, run); out["signed"] != "answered" || out["source"] != "drawn" {
		t.Fatalf("the run: %v", out)
	}
	// The signer's name is sealed in the run's history.
	if got := toJSON(ada.must(200, "GET", "/v1/runs/"+run, nil)); strings.Contains(got, "Ngozi") {
		t.Fatalf("the signer's name in the run's history: %s", got)
	}
	// The Confirm card: signed by the customer, with a link to it.
	var card map[string]any
	for _, a := range ada.must(200, "GET", "/v1/chat-alerts", nil)["alerts"].([]any) {
		if x := a.(map[string]any); x["kind"] == "confirm" && x["reference"] == ref {
			card = x
		}
	}
	proof, _ := card["proof"].(map[string]any)
	if proof == nil || proof["signed"] != true || !strings.HasPrefix(toJSON(proof["signature_url"]), `"https://`) || !strings.Contains(card["line"].(string), "signed by the customer") {
		t.Fatalf("the Confirm card: %v", card)
	}
	// Audited as photos are: kinds, states and sources, nothing more.
	var detail string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT detail::text FROM audit_log WHERE action = 'form.submitted' ORDER BY id DESC LIMIT 1`).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, `"signed": "signature answered drawn"`) || !strings.Contains(detail, `"proof": "photo answered camera"`) || strings.Contains(detail, "Ngozi") {
		t.Fatalf("audited: %s", detail)
	}

	// The rider's photo, answered to the same run, is never recorded as the
	// customer's signature: only a signature field's own PNG is.
	swapped := strings.NewReplacer(`"id":"wf_signed"`, `"id":"wf_swapped"`, `"signature":"=steps.handover.output.signed"`, `"signature":"=steps.handover.output.proof"`).Replace(proveSigned)
	run = ask(publishFlow(t, ada, swapped), deliver())
	if code, res := answer(map[string]any{"signed_by": "Ngozi Eze"}, signPNG); code != 201 {
		t.Fatalf("answered: %d %v", code, res)
	}
	w.env.Drain(t)
	if got := ada.must(200, "GET", "/v1/runs/"+run, nil); got["run"].(map[string]any)["status"] != "failed" || !strings.Contains(toJSON(got["events"]), "no such signature answered to this run") {
		t.Fatalf("the photo recorded as the signature: %s", toJSON(got["run"]))
	}
}
