package api_test

import (
	"bytes"
	"context"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// A members' tool takes a location, a photo and a signature among its
// inputs (build-plan-gaps H3, g-scan-tool): "Add a site visit" takes the
// place from the app's sheet, a photo taken with the camera and the site
// manager's signature, each read as a form step's answers are (a source
// from the channel, never the body; a file only as the one this person
// sent for this input, once); on WhatsApp the location and the photo are
// the chat's own messages and the signature unavailable. The place is
// sealed in the run under the member, and never shown in words.
const siteVisitTool = `{"schema":"wd/v1","id":"wf_visit","version":1,"name":"Add a site visit","trigger":{"type":"manual"},"roles":["operator"],
 "inputs":{"schema":{"type":"object","required":["where","proof"],"properties":{
  "where":{"type":"location","why":"so the visit is recorded where it was","max_accuracy_m":200},
  "proof":{"type":"photo","why":"a photo of the work"},
  "signed":{"type":"signature","why":"the site manager signs it off","ask_name":true},
  "note":{"type":"string"}}}},
 "outputs":[{"name":"said","type":"string","title":"Said","description":"How each was given","value":"=steps.done.output.said"}],
 "tool":{"name":"Add a site visit","command":"visit","about":"Record a visit to a site","answer":{"type":"text","text":"=outputs.said"}},
 "steps":[{"id":"done","type":"transform","config":{"output":{
   "where":"=trigger.body.where.source","state":"=trigger.body.where.state","coarse":"=has(trigger.body.where.coarse)",
   "proof":"=trigger.body.proof.source","signed":"=trigger.body.signed.state","signer":"=has(trigger.body.signed.source) ? trigger.body.signed.source : \"none\"",
   "said":"=\"visit \" + trigger.body.where.source + \" \" + trigger.body.proof.source + \" \" + trigger.body.signed.state"}}}]}`

func toolFile(t *testing.T, c *client, field, name string, data []byte, app bool) (int, map[string]any) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	if app {
		_ = mw.WriteField("app", "app")
	}
	fw, _ := mw.CreateFormFile(field, name)
	_, _ = fw.Write(data)
	_ = mw.Close()
	return c.rawPost(t, "/v1/tools/visit/files", mw.FormDataContentType(), b.Bytes())
}

func TestToolTakesLocationPhotoAndSignature(t *testing.T) {
	w := deviceWorld(t)
	w.forms.Files = sniffedFiles{}
	ctx := context.Background()
	owner := w.tenant(t, "Acme", "owner@visit.test")
	tunde := addMember(t, w.world, owner, "tunde", "operator")
	ngozi := addMember(t, w.world, owner, "ngozi", "operator")
	publishFlow(t, owner, siteVisitTool)

	// The app: the photo taken with its camera, the pad's PNG, then the
	// start naming them, with the place as the phone read it.
	code, photo := toolFile(t, tunde, "proof", "photo.jpg", doorJPEG, true)
	if code != 201 {
		t.Fatalf("the photo: %d %v", code, photo)
	}
	_, sig := toolFile(t, tunde, "signed", "signature.png", signPNG, true)
	photoID := photo["file"].(map[string]any)["id"].(string)
	sigID := sig["file"].(map[string]any)["id"].(string)
	// A JPEG is no signature; a file for an input that takes none is refused.
	if code, res := toolFile(t, tunde, "signed", "x.jpg", doorJPEG, true); code != 422 || !strings.Contains(toJSON(res), "a signature is a PNG drawing") {
		t.Fatalf("a JPEG signature: %d %v", code, res)
	}
	if code, _ := toolFile(t, tunde, "note", "x.jpg", doorJPEG, true); code != 422 {
		t.Fatalf("a file for a typed input: %d", code)
	}
	// Ngozi cannot name Tunde's photo.
	ngozi.must(422, "POST", "/v1/tools/visit/start", map[string]any{"input": map[string]any{"where": map[string]any{"lat": 6.5243, "lng": 3.3792}, "proof": map[string]any{"file": photoID}}})
	// A photo claimed as answered, without the file, is not taken.
	tunde.must(422, "POST", "/v1/tools/visit/start", map[string]any{"input": map[string]any{"where": map[string]any{"lat": 6.5243, "lng": 3.3792}, "proof": map[string]any{"state": "answered", "id": photoID, "source": "camera"}}})

	run := tunde.must(201, "POST", "/v1/tools/visit/start", map[string]any{"input": map[string]any{
		"where":  map[string]any{"lat": 6.524379, "lng": 3.379206, "accuracy_m": 9, "source": "sent"},
		"proof":  map[string]any{"file": photoID},
		"signed": map[string]any{"file": sigID, "signed_by": "Emeka Obi"},
		"note":   "Roof done"}})["run_id"].(string)
	w.env.Drain(t)
	out := doneOutput(t, owner, run)
	if out["where"] != "device" || out["state"] != "answered" || out["coarse"] != false || out["proof"] != "camera" || out["signed"] != "answered" || out["signer"] != "drawn" {
		t.Fatalf("the app's start: %v", out)
	}
	// The place and the signer are sealed in the run; the photo is the run's.
	var raw []byte
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT string_agg(payload::text, '') FROM run_events WHERE run_id = $1`, run).Scan(&raw)
	if bytes.Contains(raw, []byte("6.524379")) || bytes.Contains(raw, []byte("Emeka Obi")) || !bytes.Contains(raw, []byte(`"owned": true`)) {
		t.Fatalf("history: %s", raw)
	}
	var kept int
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM form_submissions WHERE run_id = $1 AND retain_until IS NULL`, run).Scan(&kept)
	if kept != 2 {
		t.Fatalf("%d of the files kept with the run", kept)
	}
	// Used once: the same photo again is refused.
	tunde.must(422, "POST", "/v1/tools/visit/start", map[string]any{"input": map[string]any{"where": map[string]any{"lat": 6.5, "lng": 3.3}, "proof": map[string]any{"file": photoID}}})

	// The web: a file chosen is sent, a place typed is typed.
	_, photo = toolFile(t, ngozi, "proof", "site.jpg", doorJPEG, false)
	run = ngozi.must(201, "POST", "/v1/tools/visit/start", map[string]any{"input": map[string]any{
		"where": map[string]any{"address": "4 Akoka Road, Yaba"}, "proof": map[string]any{"file": photo["file"].(map[string]any)["id"]},
		"signed": map[string]any{"state": "refused", "reason": "the manager was out"}}})["run_id"].(string)
	w.env.Drain(t)
	if out := doneOutput(t, owner, run); out["where"] != "typed" || out["proof"] != "sent" || out["signed"] != "refused" {
		t.Fatalf("the web's start: %v", out)
	}

	// WhatsApp: asked one by one, the location and the photo sent as
	// messages, the signature unavailable, never a place in words.
	const number = "+2348031117788"
	w.bindWA(t, tunde, number)
	owner.must(200, "PUT", "/v1/settings/chat-tools", map[string]any{"chat_tools": true})
	if got := lastText(w.waSend(t, number, whatsapptest.Text("/visit"))); !strings.Contains(got, "Send your location here for") || !strings.Contains(got, "so the visit is recorded where it was") {
		t.Fatalf("asked: %q", got)
	}
	if got := lastText(w.waSend(t, number, whatsapptest.Text("Allen Avenue"))); !strings.Contains(got, "Send your location") {
		t.Fatalf("words for a location: %q", got)
	}
	if got := lastText(w.waSend(t, number, whatsapptest.Location(6.601234, 3.351234))); !strings.Contains(got, "Send a photo here for") {
		t.Fatalf("after the location: %q", got)
	}
	w.graph.AddMedia("m-site", "image/jpeg", doorJPEG, int64(len(doorJPEG)))
	sent := w.waSend(t, number, whatsapptest.Image("m-site", "image/jpeg", ""))
	all := ""
	for _, m := range sent {
		all += m.Text + "\n"
	}
	if strings.Contains(all, "6.601") || !strings.Contains(all, "a location sent") || !strings.Contains(all, "a photo sent") {
		t.Fatalf("the echo: %q", all)
	}
	w.env.Drain(t)
	w.tick(t)
	w.tick(t)
	if got := w.graph.Last(t, number).Text; !strings.Contains(got, "visit sent sent unavailable") {
		t.Fatalf("the answer in WhatsApp: %q", got)
	}
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT string_agg(payload::text, '') FROM run_events e JOIN runs r ON r.id = e.run_id WHERE r.started_by = $1`, userID(t, tunde).String()).Scan(&raw)
	if bytes.Contains(raw, []byte("6.601234")) {
		t.Fatalf("the pin in the history: %s", raw)
	}
}
