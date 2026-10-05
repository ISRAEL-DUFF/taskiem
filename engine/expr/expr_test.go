package expr

import (
	"reflect"
	"strings"
	"testing"
)

var e = MustNew()

func act() map[string]any {
	trig, _ := DecodeJSON([]byte(`{"body":{"amount_kobo":5000000,"bvn":"22212345678","items":[1,2,3],"rate":1.5}}`))
	return map[string]any{
		"trigger": trig,
		"steps":   map[string]any{"verify": map[string]any{"output": map[string]any{"status": "success", "amount": int64(5000000)}}},
		"run":     map[string]any{"id": "r1"},
		"env":     map[string]any{"api": "https://x"},
	}
}

func TestEval(t *testing.T) {
	cases := map[string]any{
		"=trigger.body.amount_kobo":                                  int64(5000000),
		"=trigger.body.amount_kobo >= 50000000":                      false,
		"=steps.verify.output.amount == trigger.body.amount_kobo":    true,
		"=size(trigger.body.items)":                                  int64(3),
		"=env.api + '/topups/' + run.id":                             "https://x/topups/r1",
		"=trigger.body.items.map(i, i * 2)":                          []any{int64(2), int64(4), int64(6)},
		"={'a': 1, 'b': [true, null]}":                               map[string]any{"a": int64(1), "b": []any{true, nil}},
		"=trigger.body.rate * 2.0":                                   3.0,
		"=has(trigger.body.missing) ? 'yes' : 'no'":                  "no",
		"=steps.verify.output.status in ['success', 'reversed']":     true,
		"='Salary ' + 'Oct'.upperAscii()":                            "Salary OCT",
		"=string(double(trigger.body.amount_kobo) / 100.0) + ' NGN'": "50000 NGN",
	}
	for src, want := range cases {
		got, err := e.Eval(src, act())
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", src, got, want)
		}
	}
}

func TestEvalErrors(t *testing.T) {
	for _, src := range []string{
		"=trigger.body.nope",          // missing key
		"=unknown_root.x",             // undeclared
		"=trigger.body.amount_kobo +", // syntax
		"=[1,2,3].map(x, [1,2,3].map(y, [1,2,3].map(z, x*y*z)))[0][0][0] / 0", // division by zero
	} {
		if _, err := e.Eval(src, act()); err == nil {
			t.Errorf("%s: expected error", src)
		}
	}
}

func TestCostLimit(t *testing.T) {
	// 2^20 elements: exceeds the cost limit instead of hanging the worker.
	src := "=[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20].map(a, [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20].map(b, [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20].map(c, [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20].map(d, [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20].map(f, a*b*c*d*f)))))"
	_, err := e.Eval(src, nil)
	if err == nil || !strings.Contains(err.Error(), "cost") {
		t.Errorf("want cost limit error, got %v", err)
	}
}

func TestResolve(t *testing.T) {
	in := map[string]any{
		"amount":  "=trigger.body.amount_kobo",
		"literal": "plain",
		"nested":  []any{"=run.id", map[string]any{"auth": "='Bearer ' + secrets.token"}},
	}
	got, err := e.Resolve(in, act(), true)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"amount":  int64(5000000),
		"literal": "plain",
		"nested":  []any{"r1", map[string]any{"auth": "='Bearer ' + secrets.token"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v", got)
	}
	a := act()
	a["secrets"] = map[string]any{"token": "s3cr3t"}
	got, err = e.Resolve(in, a, false)
	if err != nil {
		t.Fatal(err)
	}
	if v := got.(map[string]any)["nested"].([]any)[1].(map[string]any)["auth"]; v != "Bearer s3cr3t" {
		t.Errorf("secret not resolved: %v", v)
	}
}

func TestReferences(t *testing.T) {
	r, err := e.References("=steps.a.output.x + steps['b'].output.y + trigger.body.list.filter(v, v > index).size() + env.k")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Steps["a"] || !r.Steps["b"] || len(r.Steps) != 2 {
		t.Errorf("steps = %v", r.Steps)
	}
	for _, root := range []string{"steps", "trigger", "index", "env"} {
		if !r.Roots[root] {
			t.Errorf("missing root %s in %v", root, r.Roots)
		}
	}
	if r.Roots["v"] {
		t.Error("comprehension variable reported as a root")
	}
	if _, err := e.References("=nope.x"); err == nil {
		t.Error("undeclared root accepted")
	}
}
