package api_test

import (
	"context"
	"strings"
	"testing"
	"time"
)

// askRider asks one member where a parcel was handed over (device fields
// §6, D0): the member finds it under To fill in and answers from the app.
const askRider = `{"schema":"wd/v1","id":"wf_deliver","version":1,"name":"Deliver","trigger":{"type":"manual"},
 "inputs":{"schema":{"type":"object","properties":{"rider":{"type":"string"}}}},
 "steps":[{"id":"handover","type":"form","config":{"title":"Delivered","to":{"member":"=trigger.body.rider"},
   "schema":{"type":"object","required":["where"],"properties":{
    "where":{"type":"location","why":"so the delivery is recorded where it was handed over","max_accuracy_m":100},
    "proof":{"type":"photo","why":"a photo of the parcel at the door"}}}}},
  {"id":"done","type":"transform","needs":["handover"],"config":{"output":{"source":"=steps.handover.output.where.source","app":"=steps.handover.output.device.app"}}}]}`

func TestFormStepToAMemberWithALocation(t *testing.T) {
	w := formsWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	rider := addMember(t, w, owner, "rider", "viewer")
	other := addMember(t, w, owner, "other", "viewer")
	w.srv.Forms.Scanner = okScanner{} // a photo is a file
	wf := publishFlow(t, owner, askRider)
	run := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"rider": userID(t, rider).String()}})
	w.env.Drain(t)

	// Only the member asked sees it.
	reqs := rider.must(200, "GET", "/v1/form-requests", nil)["requests"].([]any)
	if len(reqs) != 1 || reqs[0].(map[string]any)["member"] != userID(t, rider).String() {
		t.Fatalf("requests: %v", reqs)
	}
	rid := reqs[0].(map[string]any)["id"].(string)
	if len(other.must(200, "GET", "/v1/form-requests", nil)["requests"].([]any)) != 0 {
		t.Fatal("another member sees it")
	}
	other.must(404, "GET", "/v1/form-requests/"+rid, nil)
	// They are alerted by name, with a link to it in the app.
	if err := w.srv.Forms.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	var body, detail string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT body, detail::text FROM alerts WHERE kind = 'form.request' AND dedup_key = $1`, rid).Scan(&body, &detail); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(body, "For ") || !strings.Contains(detail, userID(t, rider).String()) {
		t.Fatalf("alert: %s %s", body, detail)
	}

	// A location without its lat is refused in plain words; then answered
	// from the phone app, offline, the source claimed in the body ignored.
	if r := rider.must(422, "POST", "/v1/form-requests/"+rid+"/answers", map[string]any{"answers": map[string]any{"where": map[string]any{"lng": 3.39}}}); !strings.Contains(toJSON(r), "a location is its lat and lng") {
		t.Fatalf("refused: %v", r)
	}
	// Refused submissions use up nothing: the request still waits.
	at := time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)
	rider.must(201, "POST", "/v1/form-requests/"+rid+"/answers", map[string]any{
		"answers": map[string]any{"where": map[string]any{"lat": 6.452131, "lng": 3.395812, "accuracy_m": 9, "at": at, "source": "sent"}},
		"device":  map[string]any{"app": "app", "version": "2.1.0", "online": false, "at": at}})
	w.env.Drain(t)
	got := owner.must(200, "GET", "/v1/runs/"+run["run_id"].(string), nil)
	if got["run"].(map[string]any)["status"] != "completed" {
		t.Fatalf("run: %v", got["run"])
	}
	// Sealed in the run's history; the run read the server's source.
	hist := toJSON(got["events"])
	if strings.Contains(hist, "6.452131") || !strings.Contains(hist, `"$pii":"location"`) || !strings.Contains(hist, `"source":"device"`) || !strings.Contains(hist, `"received_at"`) {
		t.Fatalf("history: %s", hist)
	}
}
