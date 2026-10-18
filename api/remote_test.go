package api_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/xenibase"
	"github.com/israel-duff/taskiem/connectors/xenibase/xenibasetest"
	"github.com/israel-duff/taskiem/engine/remote"
)

const xenibaseProject = "7d2c9a10-5b4e-4f00-9c11-0000000000a1"

const rowFlow = `{"schema":"wd/v1","id":"wf_rows","version":1,"name":"rows",
  "trigger":{"type":"connector_event","config":{"connector":"xenibase@1","trigger":"row_changed","events":["INSERT"],"options":{"tables":["orders"]}}},
  "steps":[{"id":"x","type":"transform","config":{"output":"=trigger.body.record"}}]}`

// Remote registration through the API (decision 0021): publishing creates
// the Xenibase webhook and answers with its state; the workflow's triggers
// and the connection show it, broken or not; publishing again repairs it;
// undeploying deletes it.
func TestPublishRegistersRemoteSubscriptions(t *testing.T) {
	w := newWorld(t)
	pg := xenibasetest.New(t)
	pg.AddProject(xenibaseProject, "shop")
	pg.AddTable(xenibaseProject, "orders", []string{"id"}, "id", "status")
	pg.AddToken(xenibasetest.Token{Secret: "pgd_shop", Scopes: []string{"read", "write"}, ProjectIDs: []string{xenibaseProject}})
	if err := w.env.Registry.Register(xenibase.New(xenibase.Options{BaseURL: pg.URL})); err != nil {
		t.Fatal(err)
	}
	rec := &remote.Reconciler{Pool: w.env.DB.App, Vault: w.env.Vault, Registry: w.env.Registry, Egress: w.env.Egress,
		HooksURL: "https://hooks.taskiem.example/hooks", Check: time.Nanosecond}
	w.srv.Remote = rec
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "xenibase@1", "name": "shop",
		"credentials": map[string]string{"token": "pgd_shop", "project_id": xenibaseProject}})

	out := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "rows", "definition": json.RawMessage(rowFlow)})
	wf := out["id"].(string)
	pub := owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	subs, _ := pub["remote_subscriptions"].([]any)
	health := map[string]string{}
	for _, s := range subs {
		m := s.(map[string]any)
		health[m["environment"].(string)] = m["health"].(string)
	}
	// Published to dev and prod; only prod has a Xenibase connection.
	if health["prod"] != "ok" || health["dev"] != "failed" {
		t.Fatalf("publish answered %v", pub)
	}
	hooks := pg.Webhooks(xenibaseProject)
	if len(hooks) != 1 {
		t.Fatalf("webhooks %+v", hooks)
	}

	trig := owner.must(200, "GET", "/v1/workflows/"+wf+"/triggers", nil)
	for _, x := range trig["triggers"].([]any) {
		m := x.(map[string]any)
		r, _ := m["remote"].(map[string]any)
		if r == nil || r["health"] != map[string]string{"prod": "ok", "dev": "failed"}[m["environment"].(string)] {
			t.Errorf("trigger %v", m)
		}
	}
	conn := func() map[string]any {
		for _, c := range owner.must(200, "GET", "/v1/connections", nil)["connections"].([]any) {
			if m := c.(map[string]any); m["connector"] == "xenibase" {
				r, _ := m["remote"].(map[string]any)
				return r
			}
		}
		return nil
	}
	if r := conn(); r == nil || r["health"] != "ok" || r["subscriptions"].(float64) != 1 {
		t.Fatalf("connection %v", r)
	}

	// Broken at Xenibase: shown on the connection, repaired by publishing again.
	pg.SetStatus(xenibaseProject, hooks[0].ID, "broken", "triggers dropped")
	if _, err := rec.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := conn(); r["health"] != "broken" || len(r["problems"].([]any)) != 1 {
		t.Fatalf("broken connection %v", r)
	}
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	if r := conn(); r["health"] != "ok" || pg.Webhooks(xenibaseProject)[0].Status != "healthy" {
		t.Fatalf("after republishing %v", r)
	}

	// Undeployed from prod: the webhook goes.
	und := owner.must(200, "DELETE", "/v1/workflows/"+wf+"/deployments/prod", nil)
	if len(pg.Webhooks(xenibaseProject)) != 0 {
		t.Fatalf("webhook left after undeploy: %v", und)
	}
	if r := conn(); r != nil {
		t.Errorf("connection still shows %v", r)
	}
	owner.must(409, "DELETE", "/v1/workflows/"+wf+"/deployments/prod", nil)
	owner.must(409, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"environment": "prod"})
}
