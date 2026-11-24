package publicweb

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The one-time page (by text or email) asks the device (build-plan-gaps
// H3, g-dev-link): each device field with its why, a button for the
// browser's own location prompt, a photo by the camera or a file, a pad,
// a code typed (scanned with the camera where the browser can), and I
// can't answer this. The script and its styles are the page's own; the
// camera is allowed only where a code is asked.
func TestHostedFormRequestAsksTheDevice(t *testing.T) {
	e := newFormHost(t)
	id := uuid.New()
	schema := map[string]any{"type": "object", "required": []any{"where"}, "properties": map[string]any{
		"where":  map[string]any{"type": "location", "why": "so the delivery is recorded where it was handed over"},
		"proof":  map[string]any{"type": "photo", "why": "a photo of the parcel"},
		"signed": map[string]any{"type": "signature", "why": "to say you received it", "ask_name": true},
		"item":   map[string]any{"type": "scan", "why": "the code on the parcel", "formats": []any{"ean_13"}},
		"note":   map[string]any{"type": "string"}}}
	e.f.requests["tok-dev"] = FormRequest{ID: id, Title: "Received", Business: "Ada Stores", Schema: schema, Status: "pending"}
	token := e.keys.RequestToken(id.String())
	e.f.requests[token] = e.f.requests["tok-dev"]

	w := e.get("/q/" + token)
	body := must(t, w, 200, `data-device="location"`, "Why: so the delivery is recorded where it was handed over", `data-locate`, "Use my location",
		`name="a.where.lat"`, `name="a.where.address"`, `capture="environment"`, `data-device="signature"`, `name="a.signed.signed_by"`,
		`data-device="scan"`, `data-formats="ean_13"`, "Scan with the camera", `name="a.item"`, "I can't answer this", `enctype="multipart/form-data"`,
		"/form-device.")
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
		t.Fatalf("the page's policy: %s", csp)
	}
	if pp := w.Header().Get("Permissions-Policy"); !strings.Contains(pp, "camera=(self)") || !strings.Contains(pp, "geolocation=(self)") || !strings.Contains(pp, "microphone=()") {
		t.Fatalf("permissions: %s", pp)
	}
	if strings.Contains(body, "<script>") || strings.Contains(body, " onclick=") || strings.Contains(body, " style=") {
		t.Fatal("an inline script or style on the page")
	}

	// Nothing given: the location is asked for.
	e.tick(10 * time.Second)
	must(t, e.next("/q/"+token, body, url.Values{"do": {"send"}}), 422, "Please tap Use my location for Where, type the address, or tick I can&#39;t answer this.")

	// Read by the browser, signed on the pad, a code scanned, a photo taken.
	e.tick(10 * time.Second)
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")
	jpeg := []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00parcel")
	must(t, e.next("/q/"+token, body, url.Values{"do": {"send"}, "a.where.lat": {"6.452131"}, "a.where.lng": {"3.395812"}, "a.where.acc": {"14.6"},
		"a.where.at": {"2026-11-24T10:00:00Z"}, "a.where.fix": {"gps"}, "a.signed.signed_by": {"Ngozi Eze"}, "a.item": {"6151100093217"}, "a.item.format": {"ean_13"}},
		upload{"a.signed", "signature.png", png}, upload{"a.proof", "door.jpg", jpeg}), 200, "Your answers have been sent to Ada Stores.")
	sub := e.submission(t, e.f.last(t))
	where, _ := sub.Answers["where"].(map[string]any)
	item, _ := sub.Answers["item"].(map[string]any)
	signed, _ := sub.Answers["signed"].(map[string]any)
	if where["lat"] != 6.452131 || where["accuracy_m"] != 15.0 || where["fix"] != "gps" || where["source"] != nil || item["text"] != "6151100093217" || item["format"] != "ean_13" || signed["signed_by"] != "Ngozi Eze" {
		t.Fatalf("answers: %v", sub.Answers)
	}
	files := map[string]string{}
	for _, f := range sub.Files {
		files[f.Field] = f.Type
	}
	if files["signed"] != "image/png" || files["proof"] != "image/jpeg" {
		t.Fatalf("files: %+v", sub.Files)
	}

	// The browser refused, the pad left: refusals are answers; a typed
	// code of a format the question does not take is refused.
	id2 := uuid.New()
	token2 := e.keys.RequestToken(id2.String())
	e.f.requests[token2] = FormRequest{ID: id2, Title: "Received", Business: "Ada Stores", Schema: schema, Status: "pending"}
	body = must(t, e.get("/q/"+token2), 200)
	e.tick(10 * time.Second)
	must(t, e.next("/q/"+token2, body, url.Values{"do": {"send"}, "a.where.state": {"refused"}, "a.item": {"ABC"}, "a.item.format": {"qr"}}), 422, "does not take QR codes")
	e.tick(10 * time.Second)
	must(t, e.next("/q/"+token2, body, url.Values{"do": {"send"}, "a.where.state": {"refused"}, "a.signed.cant": {"true"}, "a.item": {"ABC-1"}}), 200, "Your answers have been sent")
	sub = e.submission(t, e.f.last(t))
	if sub.Answers["where"].(map[string]any)["state"] != "refused" || sub.Answers["signed"].(map[string]any)["state"] != "refused" || sub.Answers["item"].(map[string]any)["format"] != nil || len(sub.Files) != 0 {
		t.Fatalf("refusals: %v %+v", sub.Answers, sub.Files)
	}

	// No code asked: the camera stays off.
	id3 := uuid.New()
	token3 := e.keys.RequestToken(id3.String())
	e.f.requests[token3] = FormRequest{ID: id3, Title: "Where", Business: "Ada Stores", Status: "pending", Schema: map[string]any{"type": "object", "properties": map[string]any{
		"where": map[string]any{"type": "location", "why": "w"}}}}
	if pp := e.get("/q/" + token3).Header().Get("Permissions-Policy"); !strings.Contains(pp, "camera=()") {
		t.Fatalf("no code, the camera: %s", pp)
	}
}
