package forms_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/decide"
	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/pii"
)

// Erasing the person who answered a location erases where they were
// (device fields §5, build-plan-gaps H3 g-scan-erase): the answer, the
// copy a later step made of it, and the submission, though the run's other
// data stays readable.
func TestErasingTheRespondentErasesTheirLocation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		to    string
		who   func(rider uuid.UUID) forms.Respondent
		owner func(rider uuid.UUID) (string, any)
	}{
		{"by a link to their phone", `"to":{"phone":"=trigger.body.phone"}`,
			func(uuid.UUID) forms.Respondent { return forms.Respondent{Kind: "request", Phone: "+2348031234522"} },
			func(uuid.UUID) (string, any) { return "phone", "08031234522" }},
		{"a member in the app", `"to":{"member":"=trigger.body.rider"}`,
			func(r uuid.UUID) forms.Respondent { return forms.Respondent{Kind: "member", MemberID: r.String()} },
			func(r uuid.UUID) (string, any) { return "member", r.String() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			ctx := context.Background()
			flow := strings.Replace(deliverFlow, `"to":{"member":"=trigger.body.rider"}`, tc.to, 1)
			// The next step copies the place twice: as numbers, and in words.
			flow = strings.Replace(flow, `"lat":"=steps.handover.output.where.lat"`,
				`"lat":"=steps.handover.output.where.lat","lng":"=steps.handover.output.where.lng","said":"=\"at \" + string(steps.handover.output.where.lat) + \",\" + string(steps.handover.output.where.lng)"`, 1)
			wf := w.env.Publish(t, flow)
			rider := uuid.New()
			run := w.env.Start(t, wf, map[string]any{"body": map[string]any{"rider": rider.String(), "phone": "+2348031234522"}})
			w.env.Drain(t)
			req := uuid.MustParse(decide.FormRequestID(run.ID.String(), "handover"))
			w.answer(req, forms.Submission{Channel: "app", Submitted: time.Now(), Respondent: tc.who(rider),
				Answers: map[string]any{"note": "by the gate", "where": map[string]any{"lat": lat, "lng": lng, "accuracy_m": 8.0, "at": time.Now().UTC().Format(time.RFC3339)}}})
			if st := w.env.Status(t, run); st != "completed" {
				t.Fatalf("run: %s", st)
			}
			// Before: opened exactly, the copy too, each under the
			// respondent (marked owned).
			opened, err := w.env.Store.OpenedHistory(ctx, run)
			if err != nil {
				t.Fatal(err)
			}
			after := stepPayload(t, opened, "after")["output"].(map[string]any)
			if after["lat"] != lat || after["lng"] != lng || !strings.Contains(after["said"].(string), "6.452131") {
				t.Fatalf("before erasure: %v", after)
			}
			var raw []byte
			_ = w.env.DB.Admin.QueryRow(ctx, `SELECT string_agg(payload::text, '') FROM run_events WHERE run_id = $1`, run.ID).Scan(&raw)
			if bytes.Contains(raw, []byte("6.452131")) || !bytes.Contains(raw, []byte(`"owned": true`)) {
				t.Fatalf("history: %s", raw)
			}

			cat, value := tc.owner(rider)
			subjects, err := w.env.Vault.SubjectsFor(ctx, w.env.Tenant, cat, value)
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range subjects {
				_ = w.env.Vault.Erase(ctx, w.env.Tenant, s, "test")
			}

			// After: no readable location anywhere in the run, nor the
			// copies of it; the note, which is not the respondent's place,
			// stays.
			opened, err = w.env.Store.OpenedHistory(ctx, run)
			if err != nil {
				t.Fatal(err)
			}
			all, _ := json.Marshal(opened)
			for _, e := range opened {
				if strings.Contains(string(e.Payload), "6.452") || strings.Contains(string(e.Payload), "3.395") || strings.Contains(string(e.Payload), "3.396") {
					t.Fatalf("a location still readable after erasure in %s: %s", e.Type, e.Payload)
				}
			}
			out := stepPayload(t, opened, "handover")["output"].(map[string]any)
			where := out["where"].(map[string]any)
			if where["lat"] != pii.Erased || where["lng"] != pii.Erased || out["note"] != "by the gate" || where["state"] != "answered" {
				t.Fatalf("answer after erasure: %v", out)
			}
			after = stepPayload(t, opened, "after")["output"].(map[string]any)
			if after["lat"] != pii.Erased || after["lng"] != pii.Erased || after["said"] != pii.Erased || after["state"] != "answered" {
				t.Fatalf("the copy after erasure: %v (%s)", after, all)
			}
			var subs int
			_ = w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM form_submissions WHERE request_id = $1`, req).Scan(&subs)
			if subs != 0 {
				t.Fatalf("%d submissions kept after the respondent was erased", subs)
			}
		})
	}
}
