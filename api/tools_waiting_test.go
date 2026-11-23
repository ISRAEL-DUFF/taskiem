package api_test

import (
	"context"
	"testing"
)

// "Waiting for Ada to approve" (made tools §7): a run on its way says who
// it waits for, in names and roles only: an open approval's approvers (the
// one who holds its role by name, several as a role, nobody as an owner),
// an open form's recipient (you, a member by name, a role's holders, a
// linked business, someone outside by a link).
func TestToolAnswerWaitingFor(t *testing.T) {
	w := deviceWorld(t)
	ctx := context.Background()
	owner := w.tenant(t, "Acme", "owner@acme.test")
	name := func(c *client, n string) {
		t.Helper()
		if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE users SET name = $2 WHERE id = $1`, personID(t, c), n); err != nil {
			t.Fatal(err)
		}
	}
	name(owner, "Tunde")
	name(addMember(t, w.world, owner, "ada", "manager"), "Ada")
	op := addMember(t, w.world, owner, "op", "operator")
	name(op, "Musa")
	c1 := addMember(t, w.world, owner, "c1", "cashier")
	c2 := addMember(t, w.world, owner, "c2", "cashier")
	name(c1, "Bisi")
	name(c2, "Chidi")

	flow := func(id, step string) string {
		return publishFlow(t, owner, `{"schema":"wd/v1","id":"`+id+`","version":1,"name":"`+id+`","trigger":{"type":"manual"},
			"inputs":{"schema":{"type":"object","properties":{"who":{"type":"string"}}}},
			"steps":[`+step+`]}`)
	}
	waits := func(c *client, wf string, input map[string]any) []any {
		t.Helper()
		run := c.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": input})["run_id"].(string)
		w.env.Drain(t)
		a := c.must(200, "GET", "/v1/runs/"+run+"/answer", nil)
		if a["status"] != "waiting" && a["status"] != "running" {
			t.Fatalf("not waiting: %v", a)
		}
		got, _ := a["waiting_for"].([]any)
		if len(got) != 1 {
			t.Fatalf("waiting for: %v", a)
		}
		return got
	}
	one := func(got []any) map[string]any { return got[0].(map[string]any) }
	approval := func(role string) string {
		return `{"id":"ok","type":"approval","config":{"role":"` + role + `","subject":{"what":"a refund"}}}`
	}

	// An approval whose role one person holds: by name; to them, "you".
	wf := flow("wf_mgr", approval("manager"))
	if got := one(waits(owner, wf, map[string]any{})); got["kind"] != "approval" || got["who"] != "Ada" || got["you"] != nil {
		t.Errorf("one manager: %v", got)
	}
	if got := one(waits(owner, flow("wf_own", approval("owner")), map[string]any{})); got["who"] != "you" || got["you"] != true {
		t.Errorf("to the one owner: %v", got)
	}
	// Two hold it: the role.
	name(addMember(t, w.world, owner, "mgr2", "manager"), "Bola")
	if got := one(waits(owner, wf, map[string]any{})); got["who"] != "someone with the role manager" {
		t.Errorf("two managers: %v", got)
	}
	// Nobody holds it: an owner.
	if got := one(waits(owner, flow("wf_aud", approval("auditor")), map[string]any{})); got["who"] != "an owner" {
		t.Errorf("nobody: %v", got)
	}

	form := func(to string) string {
		return `{"id":"ask","type":"form","config":{"title":"Check it","to":` + to + `,
			"schema":{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}}}}}`
	}
	// A form to a member: "you" to them, with the request to open; their
	// name to anyone else.
	wf = flow("wf_member", form(`{"member":"=trigger.body.who"}`))
	got := one(waits(op, wf, map[string]any{"who": personID(t, op).String()}))
	if got["kind"] != "form" || got["who"] != "you" || got["you"] != true || got["request"] == nil {
		t.Errorf("a form to me: %v", got)
	}
	if got := one(waits(owner, wf, map[string]any{"who": personID(t, op).String()})); got["who"] != "Musa" || got["request"] != nil {
		t.Errorf("a form to Musa: %v", got)
	}
	// To a role: its holders.
	if got := one(waits(owner, flow("wf_role", form(`{"role":"cashier"}`)), map[string]any{})); got["who"] != "the cashiers" {
		t.Errorf("a form to cashiers: %v", got)
	}
	// To a phone: someone outside, the number never said.
	if got := one(waits(owner, flow("wf_phone", form(`{"phone":"+2348031230000"}`)), map[string]any{})); got["who"] != "someone outside the business" {
		t.Errorf("a form to a phone: %v", got)
	}
	// To a linked business: its name.
	bayo := w.tenant(t, "Bayo Stores", "bayo@stores.test")
	bayo.must(200, "PUT", "/v1/link-settings", map[string]any{"findable": true})
	owner.must(201, "POST", "/v1/links", map[string]any{"business": tenantOf(t, bayo), "role": "supplier"})
	l := bayo.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)
	bayo.must(200, "POST", "/v1/links/"+l["id"].(string)+"/accept", nil)
	w.srv.WaitBackground()
	link := owner.must(200, "GET", "/v1/links", nil)["links"].([]any)[0].(map[string]any)["id"].(string)
	if got := one(waits(owner, flow("wf_link", form(`{"link":"=trigger.body.who"}`)), map[string]any{"who": link})); got["who"] != "Bayo Stores" {
		t.Errorf("a form to a link: %v", got)
	}

	// Ended: nobody.
	run := owner.must(201, "POST", "/v1/workflows/"+flow("wf_done", `{"id":"n","type":"transform","config":{"output":{"x":1}}}`)+"/runs", map[string]any{"input": map[string]any{}})["run_id"].(string)
	w.env.Drain(t)
	if a := owner.must(200, "GET", "/v1/runs/"+run+"/answer", nil); a["waiting_for"] != nil {
		t.Errorf("done: %v", a)
	}
}
