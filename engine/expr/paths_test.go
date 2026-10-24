package expr

import (
	"reflect"
	"testing"
)

func TestPaths(t *testing.T) {
	e := MustNewWithRoots("input", "output", "steps")
	for src, want := range map[string][][]string{
		"=input.customer.email": {{"input", "customer", "email"}},
		"=input.?source":        {{"input", "source"}},
		`=input["account"]`:     {{"input", "account"}},
		"=output.balances.filter(b, b.currency == input.currency)[0].balance": {{"output", "balances"}, {"input", "currency"}},
		"=steps.recipient.output.recipient_code":                              {{"steps", "recipient", "output", "recipient_code"}},
		"=has(input.source) ? input.source : 'x'":                             {{"input", "source"}},
		"='NGN'": nil,
	} {
		got, err := e.Paths(src, "input", "output", "steps")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v, want %v", src, got, want)
		}
	}
}

func TestStepReads(t *testing.T) {
	e := MustNew()
	got, err := e.StepReads(`=steps.?summary.output.paid.orValue(0) + size(steps.lines.output.rows) + (has(steps.paid) ? 1 : 0) + steps["check"].error.size() + (steps.?x.hasValue() ? 1 : 0)`)
	if err != nil {
		t.Fatal(err)
	}
	want := []StepRead{
		{Step: "summary", Field: "output", Optional: true},
		{Step: "lines", Field: "output"},
		{Step: "paid", Test: true},
		{Step: "check", Field: "error"},
		{Step: "x", Optional: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}
