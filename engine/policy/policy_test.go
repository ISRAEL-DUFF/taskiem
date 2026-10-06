package policy_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/policy"
)

const highValue = `{
  "rules": [
    {"when": "=subject.amount_kobo < 50000000", "levels": [{"role": "credit_officer"}]},
    {"when": "=subject.amount_kobo >= 50000000",
     "levels": [{"role": "credit_officer"}, {"role": "head_of_credit", "count": 2}], "step_up": "totp"}
  ],
  "constraints": {"forbid_self_approval": true},
  "timeout": "24h",
  "on_timeout": "escalate:head_of_operations"
}`

func TestRulesRouteBySubject(t *testing.T) {
	p, err := policy.Parse([]byte(highValue))
	if err != nil {
		t.Fatal(err)
	}
	small, err := p.Match(map[string]any{"amount_kobo": int64(1000)})
	if err != nil || len(small.Levels) != 1 || small.StepUp != "" {
		t.Fatalf("small: %+v %v", small, err)
	}
	big, _ := p.Match(map[string]any{"amount_kobo": int64(60000000)})
	levels := big.Normalised()
	if len(levels) != 2 || levels[0].Count != 1 || levels[1].Count != 2 || big.StepUp != "totp" {
		t.Fatalf("big: %+v", levels)
	}
	if _, err := p.Match(map[string]any{}); err == nil {
		t.Error("a subject without the field should not silently match")
	}
	if !p.Constraints.SelfApprovalForbidden() || !p.Constraints.Distinct() {
		t.Error("constraints default to the strict choice")
	}
}

func TestNoMatchingRule(t *testing.T) {
	p, _ := policy.Parse([]byte(`{"rules":[{"when":"=subject.kind == 'loan'","levels":[{"role":"a"}]}]}`))
	if _, err := p.Match(map[string]any{"kind": "refund"}); !errors.Is(err, policy.ErrNoRule) {
		t.Errorf("got %v", err)
	}
}

func TestInvalidPolicies(t *testing.T) {
	for doc, want := range map[string]string{
		`{"rules":[]}`: "at least one rule",
		`{"rules":[{"when":"subject.x","levels":[{"role":"a"}]}]}`:           "starting with =",
		`{"rules":[{"when":"=trigger.x","levels":[{"role":"a"}]}]}`:          "trigger",
		`{"rules":[{"levels":[]}]}`:                                          "at least one level",
		`{"rules":[{"levels":[{"role":"a"}],"step_up":"sms"}]}`:              "totp or passkey",
		`{"rules":[{"levels":[{"role":"a"}]}],"on_timeout":"escalate:boss"}`: "needs a timeout",
		`{"rules":[{"levels":[{"role":"a"}]}],"surprise":1}`:                 "unknown field",
		`{"rules":[{"levels":[{"role":"a b"}]}]}`:                            "not a role name",
		`{"rules":[{"levels":[{"role":"a"}]}],"timeout":"soon"}`:             "timeout",
	} {
		if _, err := policy.Parse([]byte(doc)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", doc, err, want)
		}
	}
}
