package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// A workflow's roles hold on every way a person starts it (decision 0070):
// the API (the web's and the app's Run) and run in the chat, for members,
// with owners always let through and keys acting as their owner.
func TestStartRoles(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	owner := w.tenant(t, "Acme", "owner@roles.test")
	def := json.RawMessage(`{"schema":"wd/v1","id":"wf_count","version":1,"name":"Count stock","trigger":{"type":"manual"},"roles":["operator"],
		"steps":[{"id":"a","type":"transform","config":{"output":{"ok":true}}}]}`)
	wf := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Count stock", "definition": def})["id"].(string)
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	if got := owner.must(200, "GET", "/v1/workflows/"+wf+"/versions/1", nil); !strings.Contains(toJSON(got), `"roles":["operator"]`) {
		t.Fatalf("roles kept: %v", got)
	}
	start := map[string]any{"input": map[string]any{}}
	// The editor offers the roles that can start runs, owners left out.
	if got := toJSON(owner.must(200, "GET", "/v1/approver-roles?for=start", nil)); !strings.Contains(got, `"name":"operator"`) || !strings.Contains(got, `"name":"builder"`) ||
		strings.Contains(got, `"name":"owner"`) || strings.Contains(got, `"name":"viewer"`) {
		t.Errorf("roles offered: %s", got)
	}

	// The API: a builder (run.start, not operator) is refused in words; an
	// operator and the owner start it.
	builder := w.member(t, owner, "bo@roles.test", "builder")
	if e := builder.must(403, "POST", "/v1/workflows/"+wf+"/runs", start); !strings.Contains(e["error"].(string), "only operator can start Count stock") {
		t.Errorf("builder: %v", e)
	}
	op := w.member(t, owner, "op@roles.test", "operator")
	op.must(201, "POST", "/v1/workflows/"+wf+"/runs", start)
	owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", start)
	// A key acts as its owner.
	key := &client{t: t, base: w.base, token: owner.must(201, "POST", "/v1/api-keys", map[string]any{"name": "ci", "permissions": []string{"run.start"}})["key"].(string)}
	key.must(201, "POST", "/v1/workflows/"+wf+"/runs", start)

	// The chat: the builder is told who may; the operator gets to yes.
	w.bind(t, builder, "+2348010000031")
	if r := w.say(t, "+2348010000031", "run count stock"); !strings.Contains(r, "Only operator can start Count stock") {
		t.Errorf("builder in chat: %q", r)
	}
	w.bind(t, op, "+2348010000032")
	if r := w.say(t, "+2348010000032", "run count stock"); strings.Contains(r, "Only operator") || !strings.Contains(r, "Count stock") {
		t.Errorf("operator in chat: %q", r)
	}
	// Their role taken away before yes: refused at yes.
	opID := userID(t, op)
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE memberships SET role = 'builder' WHERE user_id = $1`, opID); err != nil {
		t.Fatal(err)
	}
	if r := w.say(t, "+2348010000032", "yes"); !strings.Contains(r, "Only operator can start Count stock") {
		t.Errorf("roles changed before yes: %q", r)
	}

	var refused int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'run.start.refused'`).Scan(&refused); err != nil || refused != 3 {
		t.Errorf("refusals audited: %d %v", refused, err)
	}
	if runs := owner.must(200, "GET", "/v1/runs", nil)["runs"].([]any); len(runs) != 3 {
		t.Errorf("runs started: %d", len(runs))
	}
}
