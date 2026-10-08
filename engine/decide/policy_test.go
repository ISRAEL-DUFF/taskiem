package decide

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/wd"
)

const loanPolicy = `{"rules":[
  {"when":"=subject.amount_kobo < 50000000","levels":[{"role":"credit_officer"}]},
  {"when":"=subject.amount_kobo >= 50000000","levels":[{"role":"credit_officer"},{"role":"head_of_credit","count":2}],"step_up":"totp"}],
 "constraints":{"distinct_approvers":false},
 "timeout":"24h","on_timeout":"escalate:head_of_operations"}`

// newPolicySim starts a run with policies snapshotted, as the store does.
func newPolicySim(t *testing.T, wdJSON string, trigger any, policies map[string]string) *sim {
	t.Helper()
	def, err := wd.Load([]byte(wdJSON))
	if err != nil {
		t.Fatal(err)
	}
	s := &sim{t: t, def: def, now: time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)}
	snap := map[string]history.PolicySnapshot{}
	for name, doc := range policies {
		snap[name] = history.PolicySnapshot{Version: 3, Document: json.RawMessage(doc)}
	}
	s.append(history.RunStarted, "", 0, history.RunStartedPayload{Run: history.RunInfo{ID: "run-1"}, Trigger: trigger, Policies: snap}, history.OriginIngest)
	s.decide()
	return s
}

const approveLoan = `{"id":"ok","type":"approval","config":{"policy":"high_value","subject":{"amount_kobo":"=trigger.amount"}}}`

func TestPolicyRoutesBySubject(t *testing.T) {
	big := newPolicySim(t, wdDoc(approveLoan, ""), map[string]any{"amount": 60000000}, map[string]string{"high_value": loanPolicy})
	var p history.ApprovalRequestedPayload
	if err := json.Unmarshal(big.h[len(big.h)-1].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Levels) != 2 || p.Levels[1] != (history.ApprovalLevel{Role: "head_of_credit", Count: 2}) || p.Role != "credit_officer" || p.Count != 1 ||
		p.StepUp != "totp" || p.PolicyVersion != 3 || p.Constraints == nil || !p.Constraints.ForbidSelfApproval || p.Constraints.DistinctApprovers {
		t.Fatalf("payload: %+v", p)
	}
	// The policy's timeout applies when the step sets none.
	if p.TimeoutAt != "2026-10-06T09:00:00Z" {
		t.Errorf("timeout: %s", p.TimeoutAt)
	}
	big.advance(25 * time.Hour)
	big.fire("ok", "approval_timeout")
	var esc history.ApprovalRequestedPayload
	_ = json.Unmarshal(big.h[len(big.h)-1].Payload, &esc)
	if !esc.Escalated || esc.Role != "head_of_operations" || len(esc.Levels) != 1 || esc.Levels[0].Role != "head_of_operations" {
		t.Errorf("escalation: %+v", esc)
	}

	small := newPolicySim(t, wdDoc(approveLoan, ""), map[string]any{"amount": 1000}, map[string]string{"high_value": loanPolicy})
	var sp history.ApprovalRequestedPayload
	_ = json.Unmarshal(small.h[len(small.h)-1].Payload, &sp)
	if len(sp.Levels) != 1 || sp.StepUp != "" {
		t.Errorf("small: %+v", sp)
	}
}

func TestApprovalWithoutRoutingFails(t *testing.T) {
	missing := newPolicySim(t, wdDoc(approveLoan, ""), map[string]any{"amount": 1}, nil)
	if e := missing.payload(history.StepFailed, "ok")["error"].(map[string]any); e["kind"] != "policy" {
		t.Errorf("missing policy: %v", e)
	}
	narrow := `{"rules":[{"when":"=subject.amount_kobo > 100","levels":[{"role":"a"}]}]}`
	unmatched := newPolicySim(t, wdDoc(approveLoan, ""), map[string]any{"amount": 1}, map[string]string{"high_value": narrow})
	if e := unmatched.payload(history.StepFailed, "ok")["error"].(map[string]any); e["kind"] != "policy" || unmatched.last().Type != history.RunFailed {
		t.Errorf("no matching rule: %v", e)
	}
}
