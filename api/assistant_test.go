package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/assistant"
	"github.com/israel-duff/taskiem/engine/db"
)

// The owner pauses the assistant for everyone and resumes a customer
// handed to a person; Taskiem's operators stop a business's assistants
// and put it in the beta, with a reason and a passkey, in both audits
// (decision 0062 V6).
func TestAssistantSwitches(t *testing.T) {
	ctx := context.Background()
	w := opsWorld(t)
	owner := w.tenant(t, "Mama Put", "owner@mamaput.test")
	tenant := uuid.MustParse(tenantOf(t, owner))

	got := owner.must(200, "GET", "/v1/assistant", nil)
	if got["paused"] != false || got["taskiem"].(map[string]any)["on"] != true || len(got["handovers"].([]any)) != 0 {
		t.Fatalf("assistant: %v", got)
	}
	if got := owner.must(200, "POST", "/v1/assistant/pause", map[string]any{"paused": true}); got["paused"] != true || got["paused_by"] == nil {
		t.Fatalf("pause: %v", got)
	}
	if got := owner.must(200, "POST", "/v1/assistant/pause", map[string]any{"paused": false}); got["paused"] != false {
		t.Fatalf("resume: %v", got)
	}

	// A customer handed to a person: listed with their number's last four
	// digits only, and resumed.
	const channel, number = "whatsapp@1/main", "2348031234567"
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		g := &assistant.Guard{}
		d, err := g.Decide(ctx, tx, w.env.Vault, tenant, assistant.Defaults(),
			assistant.Message{Channel: channel, Dedup: "wamid.T1", Sender: number, SenderCategory: "phone", Private: true, Kind: "text", Text: "agent"})
		if err == nil && d.Reason != assistant.ReasonHandover {
			t.Errorf("decision %+v", d)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	got = owner.must(200, "GET", "/v1/assistant", nil)
	hs := got["handovers"].([]any)
	if len(hs) != 1 || hs[0].(map[string]any)["hint"] != "Number ending 4567" || strings.Contains(toJSON(got), number) {
		t.Fatalf("handovers: %v", got)
	}
	customer := hs[0].(map[string]any)["customer"].(string)
	owner.must(404, "POST", "/v1/assistant/handovers/resume", map[string]any{"channel": channel, "customer": "phone:nobody"})
	if got := owner.must(200, "POST", "/v1/assistant/handovers/resume", map[string]any{"channel": channel, "customer": customer}); len(got["handovers"].([]any)) != 0 {
		t.Fatalf("after resume: %v", got)
	}
	viewer := addMember(t, w, owner, "vic", "viewer")
	viewer.must(403, "GET", "/v1/assistant", nil)

	// Taskiem's switches: operators only, with a reason and a passkey.
	owner.must(401, "POST", "/v1/ops/tenants/"+tenant.String()+"/assistants-stop", map[string]any{"reason": "x"})
	c, a := enrolOperator(t, w, "ops@taskiem.test")
	c.must(422, "POST", "/v1/ops/tenants/"+tenant.String()+"/assistants-stop", map[string]any{"step_up": c.stepUp(a, "ops.tenant.assistants-stop", tenant.String())})
	c.must(200, "POST", "/v1/ops/tenants/"+tenant.String()+"/assistants-stop", map[string]any{"reason": "spam complaints",
		"step_up": c.stepUp(a, "ops.tenant.assistants-stop", tenant.String())})
	if tk := owner.must(200, "GET", "/v1/assistant", nil)["taskiem"].(map[string]any); tk["on"] != false || tk["stopped_reason"] != "spam complaints" {
		t.Fatalf("stopped: %v", tk)
	}
	c.must(200, "POST", "/v1/ops/tenants/"+tenant.String()+"/assistants-start", map[string]any{"reason": "resolved",
		"step_up": c.stepUp(a, "ops.tenant.assistants-start", tenant.String())})
	c.must(200, "POST", "/v1/ops/tenants/"+tenant.String()+"/assistants-beta-on", map[string]any{"reason": "pilot shop",
		"step_up": c.stepUp(a, "ops.tenant.assistants-beta-on", tenant.String())})
	if tk := owner.must(200, "GET", "/v1/assistant", nil)["taskiem"].(map[string]any); tk["on"] != true || tk["beta"] != true {
		t.Fatalf("beta: %v", tk)
	}
	// A member cannot write Taskiem's switches, even through the database
	// role the API uses.
	err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE assistant_controls SET stopped_at = NULL, beta = false WHERE tenant_id = $1`, tenant)
		return err
	})
	var beta bool
	if qerr := w.env.DB.Admin.QueryRow(ctx, `SELECT beta FROM assistant_controls WHERE tenant_id = $1`, tenant).Scan(&beta); qerr != nil || (err == nil && !beta) {
		t.Fatalf("the business changed Taskiem's switch: %v %v", err, qerr)
	}
	var actions []string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT array_agg(action ORDER BY chain_seq) FROM audit_log WHERE tenant_id = $1 AND action LIKE '%assistant%'`, tenant).Scan(&actions); err != nil {
		t.Fatal(err)
	}
	if strings.Join(actions, ",") != "assistant.pause,assistant.resume,assistant.handover.resume,tenant.assistants-stop,tenant.assistants-start,tenant.assistants-beta-on" {
		t.Fatalf("audit: %v", actions)
	}
	_ = time.Now
}
