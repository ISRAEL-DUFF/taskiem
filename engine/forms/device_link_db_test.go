package forms_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/decide"
	"github.com/israel-duff/taskiem/engine/forms"
)

// signFlow asks a customer, by a one-time link to their phone, where the
// parcel was handed over and to sign for it (build-plan-gaps H3,
// g-dev-link): the one-time page now asks the browser.
const signFlow = `{"schema":"wd/v1","id":"wf_signlink","version":1,"name":"Sign","trigger":{"type":"manual"},
 "inputs":{"schema":{"type":"object","properties":{"phone":{"type":"string","x-pii":"phone"}}}},
 "steps":[{"id":"handover","type":"form","config":{"title":"Received","to":{"phone":"=trigger.body.phone"},
   "schema":{"type":"object","required":["where"],"properties":{
    "where":{"type":"location","why":"so the delivery is recorded where it was handed over"},
    "signed":{"type":"signature","why":"to say you received it","ask_name":true},
    "item":{"type":"scan","why":"the code on the parcel"}}}}},
  {"id":"done","type":"transform","needs":["handover"],"config":{"output":{
    "where":"=steps.handover.output.where.source","signed":"=steps.handover.output.signed.source",
    "item":"=steps.handover.output.item.source","app":"=steps.handover.output.device.app"}}}]}`

// A customer answers a location and a signature on the one-time page
// from a text: the page's answer reads as the app's does, its sources
// set by the server (the browser's reading is the device's; the pad's
// drawing drawn; a code with a format the camera's), the signer sealed.
func TestOneTimePageAnswersTheDevice(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	wf := w.env.Publish(t, signFlow)
	const phone = "+2348031234588"
	run := w.env.Start(t, wf, map[string]any{"body": map[string]any{"phone": phone}})
	w.env.Drain(t)
	req := uuid.MustParse(decide.FormRequestID(run.ID.String(), "handover"))
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")
	ref := forms.NewReference()
	h := sha256.Sum256([]byte(ref))
	in := forms.Incoming{Tenant: w.env.Tenant, Request: &req, Reference: ref, StatusHash: h[:],
		Uploads: []forms.Upload{{Meta: forms.FileMeta{Field: "signed", Name: "signature.png", Type: "image/png", Size: int64(len(png))}, Data: png}},
		Submission: forms.Submission{Channel: "direct", Submitted: time.Now(), Respondent: forms.Respondent{Kind: "request", Phone: phone},
			Answers: map[string]any{"where": map[string]any{"lat": lat, "lng": lng, "accuracy_m": 15.0, "at": time.Now().UTC().Format(time.RFC3339), "fix": "gps"},
				"signed": map[string]any{"signed_by": "Ngozi Eze"}, "item": map[string]any{"text": "6151100093217", "format": "ean_13"}}}}
	in.Submission.Files = []forms.FileMeta{in.Uploads[0].Meta}
	if err := w.svc.Process(ctx, in); err != nil {
		t.Fatal(err)
	}
	var status string
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT status FROM form_submissions WHERE reference = $1`, ref).Scan(&status)
	if status == "refused" {
		t.Fatal("refused")
	}
	w.env.Drain(t)
	opened, err := w.env.Store.OpenedHistory(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	out := stepPayload(t, opened, "done")["output"].(map[string]any)
	// The page's code is typed whatever the page says of its camera: a
	// browser's word is not a scan (device fields §17, H3 review).
	if out["where"] != "device" || out["signed"] != "drawn" || out["item"] != "typed" || out["app"] != "link" {
		t.Fatalf("the one-time page's answer: %v", out)
	}
	var raw []byte
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT string_agg(payload::text, '') FROM run_events WHERE run_id = $1`, run.ID).Scan(&raw)
	if bytes.Contains(raw, []byte("Ngozi Eze")) || bytes.Contains(raw, []byte("6.452131")) {
		t.Fatalf("the signer or the place in the clear: %s", raw)
	}
	// A refusal on the page is an answer.
	run = w.env.Start(t, wf, map[string]any{"body": map[string]any{"phone": phone}})
	w.env.Drain(t)
	req = uuid.MustParse(decide.FormRequestID(run.ID.String(), "handover"))
	w.answer(req, forms.Submission{Channel: "direct", Submitted: time.Now(), Respondent: forms.Respondent{Kind: "request", Phone: phone},
		Answers: map[string]any{"where": map[string]any{"state": "refused", "reason": "the browser was not allowed to read where I am"},
			"signed": map[string]any{"state": "refused", "reason": "I could not answer this on the page"}}})
	opened, _ = w.env.Store.OpenedHistory(ctx, run)
	got := stepPayload(t, opened, "handover")["output"].(map[string]any)
	if got["where"].(map[string]any)["state"] != "refused" || got["signed"].(map[string]any)["state"] != "refused" || !strings.Contains(got["where"].(map[string]any)["reason"].(string), "browser") {
		t.Fatalf("refused on the page: %v", got)
	}
}
