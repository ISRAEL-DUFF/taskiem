package expr

import (
	"fmt"
	"reflect"
	"testing"
)

// ResolveKeeping reports where it left secret expressions. A value that an
// expression evaluated to is not among them, whatever it reads like.
func TestResolveKeeping(t *testing.T) {
	e := MustNew()
	act := map[string]any{"trigger": map[string]any{"body": map[string]any{
		"n": "=secrets.api_token", "list": []any{"=secrets.api_token", "=1+1"}, "obj": map[string]any{"k": "=secrets.other"}}}}
	in := map[string]any{
		"auth":  "='Bearer ' + secrets.api_token",
		"n":     "=trigger.body.n",
		"whole": "=trigger.body",
		"list":  []any{"=trigger.body.n", "=secrets.api_token", map[string]any{"a/b~c": "=secrets.other"}},
		"plain": "text",
	}
	out, kept, err := e.ResolveKeeping(in, act)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/auth", "/list/1", "/list/2/a~1b~0c"}; !reflect.DeepEqual(kept, want) {
		t.Errorf("kept = %v, want %v", kept, want)
	}
	// The same value as Resolve gives.
	plain, err := e.Resolve(in, act, true)
	if err != nil || !reflect.DeepEqual(out, plain) {
		t.Errorf("out = %v, Resolve gives %v (%v)", out, plain, err)
	}
	if got := e.SecretPaths(in); !reflect.DeepEqual(got, kept) {
		t.Errorf("SecretPaths = %v, want %v", got, kept)
	}
	// SecretPaths over the resolved value would also name the data: that
	// is why the worker is given the places and never searches for them.
	if got := e.SecretPaths(out); len(got) <= len(kept) {
		t.Errorf("the resolved value's data does not read like expressions: %v", got)
	}

	// The worker's half: only the places are evaluated.
	secrets := map[string]any{"secrets": map[string]any{"api_token": "S1", "other": "S2"}}
	got, err := e.ResolveAt(out, kept, secrets)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"auth":  "Bearer S1",
		"n":     "=secrets.api_token",
		"whole": map[string]any{"n": "=secrets.api_token", "list": []any{"=secrets.api_token", "=1+1"}, "obj": map[string]any{"k": "=secrets.other"}},
		"list":  []any{"=secrets.api_token", "S1", map[string]any{"a/b~c": "S2"}},
		"plain": "text",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolved = %v\nwant %v", got, want)
	}
	// The value given is not changed: it is the recorded input.
	if out.(map[string]any)["auth"] != "='Bearer ' + secrets.api_token" || out.(map[string]any)["list"].([]any)[1] != "=secrets.api_token" {
		t.Errorf("ResolveAt changed its argument: %v", out)
	}
	// A whole value that is one expression has the empty pointer.
	if v, kept, err := e.ResolveKeeping("=secrets.api_token", nil); err != nil || v != "=secrets.api_token" || !reflect.DeepEqual(kept, []string{""}) {
		t.Errorf("whole value: %v %v %v", v, kept, err)
	}
	if v, err := e.ResolveAt("=secrets.api_token", []string{""}, secrets); err != nil || v != "S1" {
		t.Errorf("whole value resolved: %v %v", v, err)
	}
}

// A place that holds no secret expression is refused, not guessed at.
func TestResolveAtRefusesOtherPlaces(t *testing.T) {
	e := MustNew()
	secrets := map[string]any{"secrets": map[string]any{"k": "S"}}
	v := map[string]any{"a": "=secrets.k", "b": "=1+1", "c": "text", "d": []any{"=secrets.k"}, "n": 1}
	for _, at := range []string{"/b", "/c", "/missing", "/d/1", "/d/x", "/d/00", "/n", "/a/deeper", ""} {
		if got, err := e.ResolveAt(v, []string{at}, secrets); err == nil {
			t.Errorf("place %q: resolved to %v", at, got)
		}
	}
	if got, err := e.ResolveAt(v, nil, secrets); err != nil || !reflect.DeepEqual(got, v) {
		t.Errorf("no places: %v %v", got, err)
	}
}

// A body is JSON only when all of it is: text that starts like a JSON
// value is not that value with the rest dropped.
func TestDecodeJSONTakesOneWholeValue(t *testing.T) {
	for _, bad := range []string{"9 bags left", "true story", `{"a":1} and more`, `"quoted" tail`, "1 2", `{"a":1}{"b":2}`, "null x"} {
		if v, err := DecodeJSON([]byte(bad)); err == nil {
			t.Errorf("%q decoded as %v, want an error", bad, v)
		}
	}
	for in, want := range map[string]any{"9": int64(9), " 9\n": int64(9), "true": true, `"x"`: "x", "\t{\"a\":1}\r\n": map[string]any{"a": int64(1)}} {
		v, err := DecodeJSON([]byte(in))
		if err != nil || fmt.Sprint(v) != fmt.Sprint(want) {
			t.Errorf("%q decoded as %v (%v), want %v", in, v, err, want)
		}
	}
	if v, err := DecodeJSON([]byte("  \n")); v != nil || err != nil {
		t.Errorf("an empty body: %v %v", v, err)
	}
}
