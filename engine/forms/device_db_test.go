package forms_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/decide"
	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// deliverFlow asks one member where a parcel was handed over (device
// fields §6), and reads the answer in the next step.
const deliverFlow = `{"schema":"wd/v1","id":"wf_deliver","version":1,"name":"Deliver","trigger":{"type":"manual"},
 "inputs":{"schema":{"type":"object","properties":{"rider":{"type":"string"},"phone":{"type":"string","x-pii":"phone"}}}},
 "steps":[{"id":"handover","type":"form","config":{"title":"Delivered","to":{"member":"=trigger.body.rider"},
   "schema":{"type":"object","required":["where"],"properties":{
    "where":{"type":"location","why":"so the delivery is recorded where it was handed over","max_accuracy_m":100},
    "proof":{"type":"photo","why":"a photo of the parcel at the door"},
    "note":{"type":"string"}}}}},
  {"id":"after","type":"transform","needs":["handover"],"config":{"output":{"source":"=steps.handover.output.where.source","state":"=steps.handover.output.where.state",
   "lat":"=steps.handover.output.where.lat","app":"=steps.handover.output.device.app","online":"=steps.handover.output.device.online"}}}]}`

const lat, lng = 6.452131, 3.395812

func (w *world) answer(req uuid.UUID, sub forms.Submission) {
	w.t.Helper()
	ref := forms.NewReference()
	h := sha256.Sum256([]byte(ref))
	if err := w.svc.Process(context.Background(), forms.Incoming{Tenant: w.env.Tenant, Request: &req, Reference: ref, StatusHash: h[:], Submission: sub}); err != nil {
		w.t.Fatal(err)
	}
	var status string
	var note *string
	_ = w.env.DB.Admin.QueryRow(context.Background(), `SELECT status, note FROM form_submissions WHERE reference = $1`, ref).Scan(&status, &note)
	if status == "refused" {
		w.t.Fatalf("refused: %s", *note)
	}
	w.env.Drain(w.t)
}

func stepPayload(t *testing.T, events []history.Event, step string) map[string]any {
	t.Helper()
	for _, e := range events {
		if e.Type == history.StepCompleted && e.StepID == step {
			var m map[string]any
			if err := json.Unmarshal(e.Payload, &m); err != nil {
				t.Fatal(err)
			}
			return m
		}
	}
	t.Fatalf("%s did not complete", step)
	return nil
}

// A location answered in the app is sealed in the run's history, read by
// the run, masked to about 100 m where masked values are shown, and
// shown exactly only when the history is opened. The answer carries the
// device it came from, with the server's time, and the source the server
// set from the channel.
func TestLocationSealedInHistoryAndMasked(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	wf := w.env.Publish(t, deliverFlow)
	rider := uuid.New()
	run := w.env.Start(t, wf, map[string]any{"body": map[string]any{"rider": rider.String()}})
	w.env.Drain(t)
	req := uuid.MustParse(decide.FormRequestID(run.ID.String(), "handover"))
	var recipient string
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT recipient::text FROM form_requests WHERE id = $1`, req).Scan(&recipient)
	if !strings.Contains(recipient, rider.String()) {
		t.Fatalf("recipient: %s", recipient)
	}

	made := time.Now().Add(-2 * time.Hour)
	off := false
	w.answer(req, forms.Submission{Channel: "app", Submitted: time.Now(), Respondent: forms.Respondent{Kind: "member", MemberID: rider.String()},
		Device:  &forms.Device{App: "app", Version: "2.1.0", Online: &off, At: &made},
		Answers: map[string]any{"note": "left with the gateman", "where": map[string]any{"lat": lat, "lng": lng, "accuracy_m": 250.0, "at": made.UTC().Format(time.RFC3339), "source": "sent"}}})
	if st := w.env.Status(t, run); st != "completed" {
		t.Fatalf("run: %s", st)
	}

	// Sealed: no coordinate in the stored history, the answer's or the
	// next step's that copied it.
	var raw []byte
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT string_agg(payload::text, '') FROM run_events WHERE run_id = $1`, run.ID).Scan(&raw)
	if bytes.Contains(raw, []byte("6.452131")) || bytes.Contains(raw, []byte("3.395812")) || !bytes.Contains(raw, []byte(`"$pii": "location"`)) {
		t.Fatalf("history: %s", raw)
	}
	var sealed []history.Event
	w.in(func(tx pgx.Tx) error {
		var err error
		sealed, err = runtime.History(ctx, tx, run.ID)
		return err
	})
	out := stepPayload(t, sealed, "handover")["output"].(map[string]any)
	where := out["where"].(map[string]any)
	// Source from the channel, not the body; coarse past max_accuracy_m;
	// a photo the app can send (D1) is not made up when left out.
	if where["source"] != "device" || where["state"] != "answered" || where["coarse"] != true || out["proof"] != nil {
		t.Fatalf("answer: %v", out)
	}
	dev := out["device"].(map[string]any)
	if dev["app"] != "app" || dev["version"] != "2.1.0" || dev["online"] != false || dev["received_at"] == dev["at"] || dev["clock_differs"] != nil {
		t.Fatalf("device: %v", dev)
	}

	// Masked to about 100 m wherever a masked value is shown.
	var lines []string
	w.in(func(tx pgx.Tx) error {
		lines = whatsapp.SummaryLines(out, func(env map[string]any) (any, error) { return w.env.Vault.OpenTx(ctx, tx, w.env.Tenant, env) }, 20)
		return nil
	})
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "where.lat: ≈6.452") || !strings.Contains(joined, "where.lng: ≈3.396") || strings.Contains(joined, "6.452131") {
		t.Fatalf("masked: %s", joined)
	}

	// Exactly, to someone who opens the run with reveal; and the run
	// read it.
	opened, err := w.env.Store.OpenedHistory(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	if got := stepPayload(t, opened, "handover")["output"].(map[string]any)["where"].(map[string]any)["lat"]; got != lat {
		t.Fatalf("opened: %v", got)
	}
	after := stepPayload(t, opened, "after")["output"].(map[string]any)
	if after["source"] != "device" || after["state"] != "answered" || after["lat"] != lat || after["app"] != "app" || after["online"] != false {
		t.Fatalf("the run read: %v", after)
	}
}

// A location answered by a link goes with the run's data when retention
// purges it: the history and the submission.
func TestLocationGoesWithTheRunsData(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	flow := strings.Replace(deliverFlow, `"to":{"member":"=trigger.body.rider"}`, `"to":{"phone":"=trigger.body.phone"}`, 1)
	wf := w.env.Publish(t, flow)
	phone := "+2348031234511"
	run := w.env.Start(t, wf, map[string]any{"body": map[string]any{"phone": phone}})
	w.env.Drain(t)
	req := uuid.MustParse(decide.FormRequestID(run.ID.String(), "handover"))
	w.answer(req, forms.Submission{Channel: "app", Submitted: time.Now(), Respondent: forms.Respondent{Kind: "request", Phone: phone},
		Answers: map[string]any{"where": map[string]any{"lat": lat, "lng": lng, "accuracy_m": 10.0}}})
	var n int
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM run_events WHERE run_id = $1 AND payload::text LIKE '%"$pii": "location"%'`, run.ID).Scan(&n)
	if n == 0 {
		t.Fatal("no sealed location in the history")
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE runs SET retain_until = now() - interval '1 second' WHERE id = $1`, run.ID); err != nil {
		t.Fatal(err)
	}
	var purged bool
	w.in(func(tx pgx.Tx) error { return tx.QueryRow(ctx, `SELECT taskiem_purge_run($1)`, run.ID).Scan(&purged) })
	var events, subs int
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM run_events WHERE run_id = $1`, run.ID).Scan(&events)
	_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM form_submissions WHERE request_id = $1`, req).Scan(&subs)
	if !purged || events != 0 || subs != 0 {
		t.Fatalf("purged %v: %d events, %d submissions left", purged, events, subs)
	}
}

// A model is never given a location: an ai step reading the answer, even
// through a field it lets personal data through and in its prompt, sends
// only that it was answered, its state and its source.
func TestNoLocationReachesAModel(t *testing.T) {
	w := newWorld(t)
	flow := strings.Replace(deliverFlow, `{"id":"after"`, `{"id":"judge","type":"ai","needs":["handover"],
     "config":{"prompt":"=\"Was the parcel left near \" + string(steps.handover.output.where.lat) + \", \" + string(steps.handover.output.where.lng) + \"?\"",
       "output_schema":{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}}},"personal_data":["where"]},
     "input":{"where":"=steps.handover.output.where","all":"=steps.handover.output"}},
    {"id":"after"`, 1)
	fake := &ai.Fake{Script: []ai.Response{{Text: `{"ok":true}`, JSON: json.RawMessage(`{"ok":true}`)}}}
	w.env.AI = fake
	wf := w.env.Publish(t, flow)
	rider := uuid.New()
	run := w.env.Start(t, wf, map[string]any{"body": map[string]any{"rider": rider.String()}})
	w.env.Drain(t)
	req := uuid.MustParse(decide.FormRequestID(run.ID.String(), "handover"))
	w.answer(req, forms.Submission{Channel: "app", Submitted: time.Now(), Respondent: forms.Respondent{Kind: "member", MemberID: rider.String()},
		Answers: map[string]any{"where": map[string]any{"lat": lat, "lng": lng, "accuracy_m": 12.0, "at": time.Now().UTC().Format(time.RFC3339)}}})
	if st := w.env.Status(t, run); st != "completed" {
		t.Fatalf("run: %s", st)
	}
	reqs := fake.Requests()
	if len(reqs) != 1 {
		t.Fatalf("asked %d times", len(reqs))
	}
	raw, _ := json.Marshal(reqs[0])
	sent := string(raw)
	for _, leak := range []string{"6.452", "3.395", "3.396", "accuracy_m"} {
		if strings.Contains(sent, leak) {
			t.Errorf("%q reached the model: %s", leak, sent)
		}
	}
	if !strings.Contains(sent, `location\": \"answered`) || !strings.Contains(sent, `source\": \"device`) {
		t.Errorf("what the model was told: %s", sent)
	}
}
