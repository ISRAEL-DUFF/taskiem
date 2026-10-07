package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/repair"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/effects"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// Two test connectors: shop (no credentials) with an output schema the
// drift monitor checks and a reconcilable write; crm, which needs a
// connection.
const shopManifest = `
manifest: connector/v1
id: shop
version: 1.0.0
name: Shop
category: other
auth: { type: none }
actions:
  get_order:
    title: Get order
    class: read
    input: { type: object, properties: { id: { type: string } } }
    output: { type: object, required: [status], properties: { status: { type: string } } }
  send:
    title: Send
    class: reconcilable_write
    idempotency: { field: reference, encoding: base32_lower, length: 32, prefix: "tsk_" }
    reconcile: lookup
    input: { type: object, properties: { id: { type: string } } }
  lookup:
    title: Look up by reference
    class: read
    input: { type: object, properties: { reference: { type: string } } }
`

const crmManifest = `
manifest: connector/v1
id: crm
version: 1.0.0
name: CRM
category: productivity
auth:
  type: api_key
  fields:
    - { key: api_key, label: API key, secret: true }
actions:
  get_contact:
    title: Get contact
    class: read
    input: { type: object, properties: { id: { type: string } } }
`

type shop struct {
	sends   atomic.Int64
	settled atomic.Bool // lookup finds the sent record
}

func (s *shop) connectors() []*connector.Connector {
	return []*connector.Connector{
		{Manifest: connector.MustParse([]byte(shopManifest)), Actions: map[string]connector.Action{
			"get_order": connector.ActionFunc(func(context.Context, connector.Request) (connector.Response, error) {
				// The provider renamed status to state.
				return connector.Response{Output: map[string]any{"state": "paid"}}, nil
			}),
			"send": connector.ActionFunc(func(context.Context, connector.Request) (connector.Response, error) {
				s.sends.Add(1)
				return connector.Response{}, effects.ErrUnknownOutcome
			}),
			"lookup": connector.ActionFunc(func(_ context.Context, req connector.Request) (connector.Response, error) {
				if !s.settled.Load() {
					return connector.Response{}, connector.ErrNotFound
				}
				return connector.Response{Output: map[string]any{"reference": req.Input["reference"], "status": "sent", "email": "ada@example.com"}}, nil
			}),
		}},
		{Manifest: connector.MustParse([]byte(crmManifest)), Actions: map[string]connector.Action{
			"get_contact": connector.ActionFunc(func(context.Context, connector.Request) (connector.Response, error) {
				return connector.Response{Output: map[string]any{}}, nil
			}),
		}},
	}
}

// patcher answers repair prompts by rewriting the workflow in the prompt.
type patcher struct {
	class   string
	replace [2]string
	test    string
}

func (p patcher) respond(req ai.Request) (*ai.Response, error) {
	doc := repair.WorkflowFrom(ai.PromptText(req))
	patched := strings.ReplaceAll(doc, p.replace[0], p.replace[1])
	raw, _ := json.Marshal(map[string]any{"class": p.class, "explanation": "Patched " + p.replace[0], "workflow": patched,
		"test": map[string]any{"name": "model: the failing case", "case": p.test}})
	return &ai.Response{Text: string(raw)}, nil
}

type repairWorld struct {
	*world
	fake  *ai.Fake
	shop  *shop
	owner *client
}

func newRepairWorld(t *testing.T, respond func(ai.Request) (*ai.Response, error)) *repairWorld {
	t.Helper()
	w := newWorld(t)
	w.env.VaultSecrets = true
	sh := &shop{}
	for _, c := range sh.connectors() {
		if err := w.env.Registry.Register(c); err != nil {
			t.Fatal(err)
		}
	}
	f := &ai.Fake{Respond: respond, Models: "claude-opus-5-5"}
	w.srv.AI = &api.AISettings{Provider: f}
	return &repairWorld{world: w, fake: f, shop: sh, owner: w.tenant(t, "Acme", "owner@acme.test")}
}

func (rw *repairWorld) publish(t *testing.T, steps string) string {
	t.Helper()
	return publishFlow(t, rw.owner, `{"schema":"wd/v1","id":"wf_orders","version":1,"name":"orders","trigger":{"type":"manual"},"steps":[`+steps+`]}`)
}

// failRun starts a run, lets it fail, and works the repair queue.
func (rw *repairWorld) failRun(t *testing.T, wf string, input map[string]any, want string) string {
	t.Helper()
	run := rw.owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": input})["run_id"].(string)
	rw.env.Drain(t)
	if st := rw.owner.must(200, "GET", "/v1/runs/"+run, nil)["run"].(map[string]any)["status"]; st != want {
		t.Fatalf("run: %v, want %s", st, want)
	}
	if _, err := rw.srv.RepairOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	return run
}

func (rw *repairWorld) repairs(t *testing.T, run string) []any {
	t.Helper()
	return rw.owner.must(200, "GET", "/v1/runs/"+run+"/repairs", nil)["repairs"].([]any)
}

func (rw *repairWorld) only(t *testing.T, run string) map[string]any {
	t.Helper()
	list := rw.repairs(t, run)
	if len(list) != 1 {
		t.Fatalf("repairs: %v", list)
	}
	return list[0].(map[string]any)
}

func (rw *repairWorld) prompts() string {
	var b strings.Builder
	for _, r := range rw.fake.Requests() {
		b.WriteString(ai.PromptText(r))
	}
	return b.String()
}

const (
	stepCharge = `{"id":"charge","type":"connector","connector":"fakepay@1","action":"transfer",
	  "input":{"amount":"=trigger.body.amount","logical_id":"=trigger.body.id","memo":"=secrets.charge_memo"}}`
	stepVerify = `{"id":"verify","type":"connector","needs":["charge"],"connector":"fakepay@1","action":"verify","input":{"reference":"=steps.charge.output.reference"}}`
	stepNotify = `{"id":"notify","type":"connector","needs":["verify"],"connector":"fakepay@1","action":"notify","retry":{"max":0},
	  "input":{"logical_id":"=trigger.body.id + '-' + trigger.body.customer.name"}}`
	missingCustomer = "trigger.body.customer.name"
	defaulted       = "(has(trigger.body.customer) ? trigger.body.customer.name : 'unknown')"
)

// The data class end to end: classified by rules, patched by the model,
// proven in the shadow sandbox, accepted (publish and resume), and the
// resumed run completes without sending the completed transfer again.
// Secrets and personal data in the failed run never reach the model.
func TestRepairDataPatchPublishAndResume(t *testing.T) {
	rw := newRepairWorld(t, patcher{class: "data", replace: [2]string{missingCustomer, defaulted},
		test: `{"trigger":{"type":"manual","body":{"id":"x","amount":1}},"mocks":{"charge":{"output":{"reference":"r"}},"verify":{"output":{}},"notify":{"output":{"sent":true}}},"expect":{"status":"completed"}}`}.respond)
	const secret = "sk_live_SEEDED_MEMO_9911"
	rw.owner.must(204, "PUT", "/v1/secrets/prod/charge_memo", map[string]any{"value": secret})
	wf := rw.publish(t, stepCharge+","+stepVerify+","+stepNotify)
	run := rw.failRun(t, wf, map[string]any{"amount": 500, "id": "o-1", "email": "ada@example.com", "bvn": "22198765432"}, "failed")
	if n := rw.env.Provider.Executions("o-1"); n != 1 {
		t.Fatalf("charge executed %d times", n)
	}

	p := rw.only(t, run)
	if p["status"] != "proposed" || p["class"] != "data" || p["classified_by"] != "rules" || p["step"] != "notify" || p["created_by"] != "system:ai-repair" {
		t.Fatalf("proposal: %v", p)
	}
	if !strings.Contains(toJSON(p["diff"]), "notify") {
		t.Errorf("diff: %v", p["diff"])
	}
	ev := p["evidence"].(map[string]any)
	sh := ev["shadow"].(map[string]any)
	if sh["passed"] != true || ev["resumable"] != true || ev["reproduces_failure"] != true || ev["regression"].(map[string]any)["passed"] != true ||
		ev["model_test"].(map[string]any)["passed"] != true || !strings.Contains(toJSON(sh["replayed"]), "charge") {
		t.Fatalf("evidence: %v", toJSON(ev))
	}
	all := rw.prompts() + toJSON(p)
	for _, leak := range []string{secret, "ada@example.com", "22198765432"} {
		if strings.Contains(all, leak) {
			t.Errorf("%q reached the model or the proposal", leak)
		}
	}
	if len(rw.fake.Requests()) != 1 {
		t.Errorf("model calls: %d", len(rw.fake.Requests()))
	}
	its := rw.owner.must(200, "GET", "/v1/repairs/"+p["id"].(string)+"/interactions", nil)["interactions"].([]any)
	if len(its) != 1 || its[0].(map[string]any)["outcome"] != "valid" {
		t.Errorf("interactions: %v", its)
	}

	// Accept: publish (no four-eyes here) and resume.
	acc := rw.owner.must(200, "POST", "/v1/repairs/"+p["id"].(string)+"/accept", nil)
	if acc["status"] != "resumed" || acc["draft_version"] != float64(2) || acc["resumed_run_id"] == nil {
		t.Fatalf("accept: %v", acc)
	}
	got := rw.owner.must(200, "GET", "/v1/workflows/"+wf, nil)
	if got["workflow"].(map[string]any)["active_version"] != float64(2) {
		t.Errorf("not published: %v", got["workflow"])
	}
	for _, v := range got["versions"].([]any) {
		if vm := v.(map[string]any); vm["version"] == float64(2) && vm["created_by"] != "system:ai-repair" {
			t.Errorf("version 2: %v", vm)
		}
	}
	rw.env.Drain(t)
	resumed := acc["resumed_run_id"].(string)
	if st := rw.owner.must(200, "GET", "/v1/runs/"+resumed, nil)["run"].(map[string]any)["status"]; st != "completed" {
		t.Fatalf("resumed run: %v", st)
	}
	if n := rw.env.Provider.Executions("o-1"); n != 1 {
		t.Errorf("the completed charge was sent again: %d executions", n)
	}
	if n := rw.env.Provider.Executions("o-1-unknown"); n != 1 {
		t.Errorf("notify ran %d times", n)
	}
	rw.owner.must(409, "POST", "/v1/repairs/"+p["id"].(string)+"/accept", nil)
	audit := toJSON(rw.owner.must(200, "GET", "/v1/audit", nil))
	for _, a := range []string{"ai.repair.propose", "ai.repair.accept", "run.resume", "workflow.publish"} {
		if !strings.Contains(audit, a) {
			t.Errorf("audit lacks %s", a)
		}
	}
	// The regression test is now one of the workflow's tests: the next
	// repair of this workflow must keep passing it.
	run2 := rw.failRun(t, wf, map[string]any{"amount": 700, "id": "o-2", "customer": map[string]any{"name": 7}}, "failed")
	if p2 := rw.only(t, run2); len(p2["evidence"].(map[string]any)["existing_tests"].([]any)) < 1 {
		t.Errorf("existing tests not run: %v", toJSON(p2["evidence"]))
	}
	// Retention still purges the repaired run: its proposal and replays go
	// with it; the version keeps its place, the audit chain the co-author.
	bg := context.Background()
	if _, err := rw.env.DB.Admin.Exec(bg, `UPDATE runs SET retain_until = now() - interval '1 second' WHERE id = ANY ($1)`,
		[]uuid.UUID{uuid.MustParse(run), uuid.MustParse(resumed)}); err != nil {
		t.Fatal(err)
	}
	tenant := uuid.MustParse(rw.owner.must(200, "GET", "/v1/me", nil)["tenant_id"].(string))
	for _, id := range []string{run, resumed} {
		var ok bool
		if err := db.InTenantTx(bg, rw.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			return tx.QueryRow(bg, `SELECT taskiem_purge_run($1)`, uuid.MustParse(id)).Scan(&ok)
		}); err != nil || !ok {
			t.Fatalf("purge %s: %v %v", id, ok, err)
		}
	}
	rw.owner.must(404, "GET", "/v1/repairs/"+p["id"].(string), nil)
}

// A patch that does not fix the failure never reaches a person: the shadow
// run fails, the model gets three attempts, and the proposal is withheld.
func TestRepairShadowWithholdsBadPatch(t *testing.T) {
	rw := newRepairWorld(t, patcher{class: "data", replace: [2]string{missingCustomer, "trigger.body.customer.name + ''"}}.respond)
	rw.owner.must(204, "PUT", "/v1/secrets/prod/charge_memo", map[string]any{"value": "m"})
	wf := rw.publish(t, stepCharge+","+stepVerify+","+stepNotify)
	run := rw.failRun(t, wf, map[string]any{"amount": 500, "id": "o-3"}, "failed")
	p := rw.only(t, run)
	if p["status"] != "withheld" || p["attempts"] != float64(3) || len(rw.fake.Requests()) != 3 {
		t.Fatalf("proposal: %v (%d calls)", p, len(rw.fake.Requests()))
	}
	if !strings.Contains(ai.PromptText(rw.fake.Requests()[2]), "shadow run") {
		t.Errorf("the shadow failure was not fed back")
	}
	rw.owner.must(409, "POST", "/v1/repairs/"+p["id"].(string)+"/accept", nil)
	rw.owner.must(200, "POST", "/v1/repairs/"+p["id"].(string)+"/dismiss", nil)
}

// With four-eyes publishing, accepting asks to publish; the run resumes
// when a second person approves.
func TestRepairFourEyes(t *testing.T) {
	rw := newRepairWorld(t, patcher{class: "data", replace: [2]string{missingCustomer, defaulted}}.respond)
	bob := addMember(t, rw.world, rw.owner, "bob", "admin")
	rw.owner.must(204, "PUT", "/v1/secrets/prod/charge_memo", map[string]any{"value": "m"})
	wf := rw.publish(t, stepCharge+","+stepVerify+","+stepNotify)
	rw.owner.must(200, "PUT", "/v1/governance", map[string]any{"four_eyes_publish": true, "four_eyes_policies": false})
	run := rw.failRun(t, wf, map[string]any{"amount": 500, "id": "o-4"}, "failed")
	p := rw.only(t, run)
	acc := rw.owner.must(200, "POST", "/v1/repairs/"+p["id"].(string)+"/accept", nil)
	if acc["status"] != "awaiting_publish" || acc["resumed_run_id"] != nil {
		t.Fatalf("accept: %v", acc)
	}
	// The person who accepted cannot approve it; a second person does.
	rw.owner.must(403, "POST", "/v1/workflows/"+wf+"/versions/2/publish/approve", map[string]any{})
	bob.must(200, "POST", "/v1/workflows/"+wf+"/versions/2/publish/approve", map[string]any{})
	after := rw.owner.must(200, "GET", "/v1/repairs/"+p["id"].(string), nil)
	if after["status"] != "resumed" || after["resumed_run_id"] == nil {
		t.Fatalf("after approval: %v", after)
	}
	rw.env.Drain(t)
	if st := rw.owner.must(200, "GET", "/v1/runs/"+after["resumed_run_id"].(string), nil)["run"].(map[string]any)["status"]; st != "completed" {
		t.Fatalf("resumed run: %v", st)
	}
	if n := rw.env.Provider.Executions("o-4"); n != 1 {
		t.Errorf("charge executed %d times", n)
	}
}

// Transient: no model call, no change; retry resumes through the normal
// path on the same version.
func TestRepairTransientRetry(t *testing.T) {
	rw := newRepairWorld(t, nil)
	var down atomic.Bool
	down.Store(true)
	rw.env.Provider.Faults = func(action string) rt.Fault {
		if action == "notify" && down.Load() {
			return rt.RefuseBefore
		}
		return rt.NoFault
	}
	rw.owner.must(204, "PUT", "/v1/secrets/prod/charge_memo", map[string]any{"value": "m"})
	notify := `{"id":"notify","type":"connector","needs":["verify"],"connector":"fakepay@1","action":"notify","retry":{"max":0},"input":{"logical_id":"=trigger.body.id + '-n'"}}`
	wf := rw.publish(t, stepCharge+","+stepVerify+","+notify)
	run := rw.failRun(t, wf, map[string]any{"amount": 500, "id": "o-5"}, "failed")
	p := rw.only(t, run)
	if p["status"] != "action" || p["class"] != "transient" || p["action"].(map[string]any)["kind"] != "retry" || len(rw.fake.Requests()) != 0 {
		t.Fatalf("proposal: %v", p)
	}
	viewer := addMember(t, rw.world, rw.owner, "vic", "viewer")
	viewer.must(403, "POST", "/v1/repairs/"+p["id"].(string)+"/accept", nil)
	down.Store(false)
	acc := rw.owner.must(200, "POST", "/v1/repairs/"+p["id"].(string)+"/accept", nil)
	if acc["status"] != "resumed" {
		t.Fatalf("accept: %v", acc)
	}
	rw.env.Drain(t)
	if st := rw.owner.must(200, "GET", "/v1/runs/"+acc["resumed_run_id"].(string), nil)["run"].(map[string]any)["status"]; st != "completed" {
		t.Fatalf("resumed: %v", st)
	}
	if rw.env.Provider.Executions("o-5") != 1 || rw.env.Provider.Executions("o-5-n") != 1 {
		t.Errorf("executions: %v", rw.env.Provider.Snapshot())
	}
}

// Credential: prompt to reconnect, without asking the model.
func TestRepairCredential(t *testing.T) {
	rw := newRepairWorld(t, nil)
	wf := rw.publish(t, `{"id":"contact","type":"connector","connector":"crm@1","action":"get_contact","input":{"id":"=trigger.body.id"}}`)
	run := rw.failRun(t, wf, map[string]any{"id": "c-1"}, "failed")
	p := rw.only(t, run)
	act := p["action"].(map[string]any)
	if p["status"] != "action" || p["class"] != "credential" || act["kind"] != "reconnect" || act["connector"] != "crm@1" || act["environment"] != "prod" ||
		len(rw.fake.Requests()) != 0 {
		t.Fatalf("proposal: %v", p)
	}
}

// Unknown outcome: the reconcile action is run read-only and its answer
// shown; the AI never resolves the step, and nothing can be accepted.
func TestRepairUnknownOutcome(t *testing.T) {
	rw := newRepairWorld(t, nil)
	wf := rw.publish(t, `{"id":"send","type":"connector","connector":"shop@1","action":"send","retry":{"max":0},"input":{"id":"=trigger.body.id"}}`)
	rw.shop.settled.Store(true)
	run := rw.failRun(t, wf, map[string]any{"id": "s-1"}, "needs_reconciliation")
	p := rw.only(t, run)
	rec := p["action"].(map[string]any)["reconcile"].(map[string]any)
	res := rec["result"].(map[string]any)
	if p["status"] != "action" || p["class"] != "unknown_outcome" || rec["available"] != true || res["found"] != true || len(rw.fake.Requests()) != 0 {
		t.Fatalf("proposal: %v", toJSON(p))
	}
	if strings.Contains(toJSON(p), "ada@example.com") {
		t.Errorf("the provider's record is not masked: %v", res)
	}
	if rw.shop.sends.Load() != 1 {
		t.Errorf("send ran %d times", rw.shop.sends.Load())
	}
	rw.owner.must(409, "POST", "/v1/repairs/"+p["id"].(string)+"/accept", nil)
	if st := rw.owner.must(200, "GET", "/v1/runs/"+run, nil)["run"].(map[string]any)["status"]; st != "needs_reconciliation" {
		t.Errorf("the step was resolved: %v", st)
	}
}

// A provider's changed response (contract drift) starts a repair too; the
// patch maps the field the drift record names; one proposal per run.
func TestRepairSchemaDrift(t *testing.T) {
	rw := newRepairWorld(t, patcher{class: "schema_drift", replace: [2]string{"steps.get.output.status", "steps.get.output.state"}}.respond)
	wf := rw.publish(t, `{"id":"get","type":"connector","connector":"shop@1","action":"get_order","input":{"id":"=trigger.body.id"}},
	  {"id":"paid","type":"transform","needs":["get"],"config":{"output":{"paid":"=steps.get.output.status == 'paid'"}}}`)
	run := rw.failRun(t, wf, map[string]any{"id": "d-1"}, "failed")
	var reasons []string
	if err := rw.env.DB.Admin.QueryRow(context.Background(), `SELECT array_agg(reason ORDER BY reason) FROM repair_jobs WHERE run_id = $1`, uuid.MustParse(run)).Scan(&reasons); err != nil {
		t.Fatal(err)
	}
	if strings.Join(reasons, ",") != "drift,run_failed" {
		t.Errorf("jobs: %v", reasons)
	}
	p := rw.only(t, run)
	if p["status"] != "proposed" || p["class"] != "schema_drift" || p["classified_by"] != "rules" {
		t.Fatalf("proposal: %v", toJSON(p))
	}
	if !strings.Contains(rw.prompts(), `"/status"`) {
		t.Errorf("the drift finding is not in the prompt")
	}
	// Queue dedup: nothing more to do, and no second proposal.
	if n, err := rw.srv.RepairOnce(context.Background()); err != nil || n != 0 {
		t.Errorf("repair once again: %d %v", n, err)
	}
	list := rw.owner.must(200, "GET", "/v1/workflows/"+wf+"/repairs", nil)["repairs"].([]any)
	if len(list) != 1 {
		t.Errorf("workflow repairs: %d", len(list))
	}
}

// Budget exhaustion skips repairs that need the model, cleanly; the run
// is untouched and nothing is proposed.
func TestRepairBudgetExhausted(t *testing.T) {
	rw := newRepairWorld(t, patcher{class: "data", replace: [2]string{missingCustomer, defaulted}}.respond)
	tenant := uuid.MustParse(rw.owner.must(200, "GET", "/v1/me", nil)["tenant_id"].(string))
	if err := rw.env.Store.SetLimits(context.Background(), tenant, map[string]any{"ai_monthly_tokens": int64(10)}, "operator"); err != nil {
		t.Fatal(err)
	}
	if _, err := rw.env.DB.Admin.Exec(context.Background(), `INSERT INTO ai_interactions (id, tenant_id, round, kind, actor, provider, model, system_digest, messages, outcome, total_tokens)
		VALUES (gen_random_uuid(), $1, 1, 'draft', 'x', 'fake', 'fake', 'd', '[]', 'valid', 100)`, tenant); err != nil {
		t.Fatal(err)
	}
	rw.owner.must(204, "PUT", "/v1/secrets/prod/charge_memo", map[string]any{"value": "m"})
	wf := rw.publish(t, stepCharge+","+stepVerify+","+stepNotify)
	run := rw.failRun(t, wf, map[string]any{"amount": 500, "id": "o-6"}, "failed")
	if list := rw.repairs(t, run); len(list) != 0 || len(rw.fake.Requests()) != 0 {
		t.Fatalf("repairs: %v, model calls %d", list, len(rw.fake.Requests()))
	}
	var status, detail string
	if err := rw.env.DB.Admin.QueryRow(context.Background(), `SELECT status, detail FROM repair_jobs WHERE run_id = $1`, uuid.MustParse(run)).Scan(&status, &detail); err != nil {
		t.Fatal(err)
	}
	if status != "skipped" || !strings.Contains(detail, "budget") {
		t.Errorf("job: %s %s", status, detail)
	}
}

// Permissions, tenant isolation and the tenant's off switch.
func TestRepairPermissionsAndIsolation(t *testing.T) {
	rw := newRepairWorld(t, patcher{class: "data", replace: [2]string{missingCustomer, defaulted}}.respond)
	rw.owner.must(204, "PUT", "/v1/secrets/prod/charge_memo", map[string]any{"value": "m"})
	wf := rw.publish(t, stepCharge+","+stepVerify+","+stepNotify)
	run := rw.failRun(t, wf, map[string]any{"amount": 500, "id": "o-7"}, "failed")
	p := rw.only(t, run)
	id := p["id"].(string)

	viewer := addMember(t, rw.world, rw.owner, "vic", "viewer")
	operator := addMember(t, rw.world, rw.owner, "otto", "operator")
	viewer.must(200, "GET", "/v1/repairs/"+id, nil)
	viewer.must(403, "POST", "/v1/repairs/"+id+"/accept", nil)
	viewer.must(403, "POST", "/v1/repairs/"+id+"/dismiss", nil)
	viewer.must(403, "PUT", "/v1/repairs/settings", map[string]any{"enabled": false})
	// Resolving runs is not publishing: an operator cannot accept a patch.
	operator.must(403, "POST", "/v1/repairs/"+id+"/accept", nil)

	other := rw.tenant(t, "Other", "owner@other.test")
	other.must(404, "GET", "/v1/repairs/"+id, nil)
	other.must(404, "POST", "/v1/repairs/"+id+"/accept", nil)
	if list := other.must(200, "GET", "/v1/workflows/"+wf+"/repairs", nil)["repairs"].([]any); len(list) != 0 {
		t.Errorf("another tenant sees repairs: %v", list)
	}

	// Off: new failures are not analysed.
	s := rw.owner.must(200, "PUT", "/v1/repairs/settings", map[string]any{"enabled": false})
	if s["enabled"] != false || s["available"] != true {
		t.Fatalf("settings: %v", s)
	}
	calls := len(rw.fake.Requests())
	run2 := rw.failRun(t, wf, map[string]any{"amount": 500, "id": "o-8"}, "failed")
	if len(rw.repairs(t, run2)) != 0 || len(rw.fake.Requests()) != calls {
		t.Errorf("repair ran while off")
	}
}

// TestRepairRoutes pins the repair API: reading proposals, the tenant's
// switch, and the two decisions a person makes. Nothing here lets the
// repair system itself publish, approve or resume.
func TestRepairRoutes(t *testing.T) {
	rw := newRepairWorld(t, nil)
	var got []string
	err := chi.Walk(rw.srv.Handler().(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.Contains(route, "repair") {
			got = append(got, method+" "+route)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{
		"GET /v1/repairs/settings",
		"GET /v1/repairs/{id}",
		"GET /v1/repairs/{id}/interactions",
		"GET /v1/runs/{run}/repairs",
		"GET /v1/workflows/{wf}/repairs",
		"POST /v1/repairs/{id}/accept",
		"POST /v1/repairs/{id}/dismiss",
		"PUT /v1/repairs/settings",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("repair routes changed; every new one needs a safety review (docs/ai.md):\n%s", strings.Join(got, "\n"))
	}
}
