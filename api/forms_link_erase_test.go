package api_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/decide"
	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/runtime"
)

// A location answered on the one-time page, sent by text to a phone, is
// the phone's (build-plan-gaps H3, review fix 1): the page says nothing of
// who answered, so the server takes the person from the request it sent,
// and erasing that phone leaves no readable place in the run, through the
// real page, its inbox and the run.
const placeByText = `{"schema":"wd/v1","id":"wf_placebytext","version":1,"name":"Where","trigger":{"type":"manual"},
 "inputs":{"schema":{"type":"object","properties":{"phone":{"type":"string","x-pii":"phone"}}}},
 "steps":[{"id":"where","type":"form","config":{"title":"Where are you","to":{"phone":"=trigger.body.phone"},
   "schema":{"type":"object","required":["here"],"properties":{"here":{"type":"location","why":"so the delivery goes to the right place"}}}}},
  {"id":"done","type":"transform","needs":["where"],"config":{"output":{"lat":"=steps.where.output.here.lat","source":"=steps.where.output.here.source"}}}]}`

func TestOneTimePageLocationIsTheRecipients(t *testing.T) {
	w := deviceWorld(t)
	ctx := context.Background()
	owner := w.tenant(t, "Acme", "owner@bytext.test")
	wf := publishFlow(t, owner, placeByText)
	const phone = "+2348031239911"
	run := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"phone": phone}})["run_id"].(string)
	w.env.Drain(t)
	if err := w.forms.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	req := decide.FormRequestID(run, "where")
	path := "/q/" + w.forms.Keys.RequestToken(req)
	first := w.page(t, path, nil)
	tm, sm := reachTokenRe.FindStringSubmatch(first.Body.String()), reachStateRe.FindStringSubmatch(first.Body.String())
	if first.Code != 200 || tm == nil || sm == nil || !strings.Contains(first.Body.String(), "Use my location") {
		t.Fatalf("the page: %d %s", first.Code, first.Body.String())
	}
	time.Sleep(formguard.DefaultMinFill + 100*time.Millisecond)
	done := w.page(t, path, url.Values{"token": {tm[1]}, "state": {sm[1]}, "website": {""}, "do": {"send"},
		"a.here.lat": {"6.611111"}, "a.here.lng": {"3.311111"}, "a.here.acc": {"10"}})
	if done.Code != 200 {
		t.Fatalf("sending: %d %s", done.Code, done.Body.String())
	}
	w.started(t)
	tenant := uuid.MustParse(tenantOf(t, owner))
	ref := runtime.RunRef{ID: uuid.MustParse(run), TenantID: tenant}
	opened, err := w.env.Store.OpenedHistory(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if out := stepOut(t, opened, "done"); out["lat"] != 6.611111 || out["source"] != "device" {
		t.Fatalf("before: %v", out)
	}
	// Erasing the phone the link went to erases the place, and its copy.
	subjects, err := w.env.Vault.SubjectsFor(ctx, tenant, "phone", phone)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range subjects {
		_ = w.env.Vault.Erase(ctx, tenant, s, "test")
	}
	opened, err = w.env.Store.OpenedHistory(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if out := stepOut(t, opened, "done"); out["lat"] != pii.Erased {
		t.Fatalf("after erasing the phone: %v", out)
	}
	for _, e := range opened {
		if strings.Contains(string(e.Payload), "6.611") {
			t.Fatalf("a place still readable in %s: %s", e.Type, e.Payload)
		}
	}
}

// stepOut is a completed step's output in an opened history.
func stepOut(t *testing.T, events []history.Event, step string) map[string]any {
	t.Helper()
	for _, e := range events {
		if e.Type == history.StepCompleted && e.StepID == step {
			var m map[string]any
			if err := json.Unmarshal(e.Payload, &m); err != nil {
				t.Fatal(err)
			}
			out, _ := m["output"].(map[string]any)
			return out
		}
	}
	t.Fatalf("%s did not complete", step)
	return nil
}
