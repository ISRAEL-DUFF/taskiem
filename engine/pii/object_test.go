package pii

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// plainCipher "seals" by wrapping the value, so a test can see what was
// sealed, as what, and open it again.
type plainCipher struct{ sealed int }

func (c *plainCipher) SealTx(_ context.Context, _ pgx.Tx, _ uuid.UUID, category string, value any) (map[string]any, error) {
	c.sealed++
	raw, _ := json.Marshal(value)
	return map[string]any{"$pii": category, "subject": category + ":x", "ct": string(raw)}, nil
}

func (c *plainCipher) OpenTx(_ context.Context, _ pgx.Tx, _ uuid.UUID, env map[string]any) (any, error) {
	var v any
	err := json.Unmarshal([]byte(env["ct"].(string)), &v)
	return v, err
}

func unsealed(v any, at string, out *[]string) {
	switch t := v.(type) {
	case map[string]any:
		if IsEnvelope(t) {
			return
		}
		for k, x := range t {
			unsealed(x, at+"."+k, out)
		}
	case []any:
		for _, x := range t {
			unsealed(x, at+".*", out)
		}
	case nil:
	case string:
		if t != "" {
			*out = append(*out, at)
		}
	default:
		*out = append(*out, at)
	}
}

// A declared place holding an object seals every value under it; it used
// to seal nothing ("objects keep their shape"), so a provider's whole
// identity record was stored readable.
func TestADeclaredObjectSealsEveryLeaf(t *testing.T) {
	record := map[string]any{
		"firstName": "Ngozi", "dateOfBirth": "1988-04-04", "age": float64(38), "watchListed": false, "middleName": nil, "empty": "",
		"address":  map[string]any{"street": "13B Fake Street", "lga": "Ilupeju", "geo": map[string]any{"lat": 6.55}},
		"phones":   []any{"08031234567", "08090000000"},
		"accounts": []any{map[string]any{"number": "0123456789", "bank": map[string]any{"name": "Kuda"}}},
	}
	in := map[string]any{"status": "found", "record": record, "list": []any{map[string]any{"name": "Bola Ade"}}}
	before, _ := json.Marshal(in)
	c := &plainCipher{}
	taint := Taint{}
	out, err := Seal(context.Background(), c, nil, uuid.Nil, in, []Path{
		{Segments: []string{"record"}, Category: "other"},
		{Segments: []string{"list"}, Category: "name"},
	}, taint)
	if err != nil {
		t.Fatal(err)
	}
	if after, _ := json.Marshal(in); string(after) != string(before) {
		t.Error("Seal changed its input")
	}
	o := out.(map[string]any)
	var open []string
	unsealed(o["record"], "record", &open)
	unsealed(o["list"], "list", &open)
	if len(open) > 0 {
		t.Errorf("left readable: %v", open)
	}
	if o["status"] != "found" {
		t.Errorf("an undeclared field was touched: %v", o["status"])
	}
	// The shape and the keys stay, so a field is read where it was.
	rec := o["record"].(map[string]any)
	if !IsEnvelope(rec["address"].(map[string]any)["geo"].(map[string]any)["lat"]) || !IsEnvelope(rec["phones"].([]any)[1]) ||
		!IsEnvelope(rec["accounts"].([]any)[0].(map[string]any)["bank"].(map[string]any)["name"]) || rec["middleName"] != nil || rec["empty"] != "" {
		t.Errorf("record %v", rec)
	}
	if env := o["list"].([]any)[0].(map[string]any)["name"].(map[string]any); env["$pii"] != "name" {
		t.Errorf("a leaf is sealed as the declared category: %v", env)
	}
	// Strings are remembered, so a copy elsewhere is sealed too; a
	// record's numbers and booleans are not (38 or false would seal every
	// equal value in the run).
	if _, ok := taint["s:Ngozi"]; !ok {
		t.Error("a string leaf is not remembered")
	}
	for k := range taint {
		if k[0] == 'n' {
			t.Errorf("a number leaf is remembered: %s", k)
		}
	}
	// Opening remembers exactly what sealing did, so what is followed does
	// not depend on when the history is read.
	again := Taint{}
	if _, err := Open(context.Background(), c, nil, uuid.Nil, out, again); err != nil || !reflect.DeepEqual(again, taint) {
		t.Errorf("opening remembers %v, sealing %v (%v)", again, taint, err)
	}
	// Opened leaf by leaf, it is the record again.
	var opened func(v any) any
	opened = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			if IsEnvelope(x) {
				p, _ := c.OpenTx(context.Background(), nil, uuid.Nil, x)
				return p
			}
			m := map[string]any{}
			for k, y := range x {
				m[k] = opened(y)
			}
			return m
		case []any:
			l := make([]any, len(x))
			for i, y := range x {
				l[i] = opened(y)
			}
			return l
		}
		return v
	}
	if got := opened(o["record"]); !reflect.DeepEqual(got, record) {
		t.Errorf("opened %v, want %v", got, record)
	}
}

// A declared list of plain values is sealed as before: each value, and
// numbers remembered like any declared scalar.
func TestADeclaredListOfValuesIsUnchanged(t *testing.T) {
	c := &plainCipher{}
	taint := Taint{}
	out, err := Seal(context.Background(), c, nil, uuid.Nil, map[string]any{"to": []any{"08031234567", float64(8090000000)}},
		[]Path{{Segments: []string{"to"}, Category: "phone"}}, taint)
	if err != nil {
		t.Fatal(err)
	}
	to := out.(map[string]any)["to"].([]any)
	if !IsEnvelope(to[0]) || !IsEnvelope(to[1]) || c.sealed != 2 || len(taint) != 2 {
		t.Errorf("%v, %d sealed, taint %v", to, c.sealed, taint)
	}
}

// Past MaxSealedLeaves a declared place is sealed whole: one envelope, as
// unreadable, opened to the same value.
func TestALargeDeclaredListIsSealedWhole(t *testing.T) {
	var list []any
	for i := 0; i < MaxSealedLeaves; i++ {
		list = append(list, map[string]any{"reference": "P-" + string(rune('a'+i%26)), "amount": float64(i)})
	}
	for _, in := range []map[string]any{{"data": list}, {"data": map[string]any{"rows": list}}} {
		c := &plainCipher{}
		taint := Taint{}
		out, err := Seal(context.Background(), c, nil, uuid.Nil, in, []Path{{Segments: []string{"data"}, Category: "other"}}, taint)
		if err != nil {
			t.Fatal(err)
		}
		env, _ := out.(map[string]any)["data"].(map[string]any)
		// Its text values are remembered (26 references), its numbers not.
		if _, ok := taint["s:P-a"]; !IsEnvelope(env) || c.sealed != 1 || len(taint) != 26 || !ok {
			t.Fatalf("sealed %d times, taint %d: %v", c.sealed, len(taint), out)
		}
		// Opened, it is the value again, and the same values are remembered.
		again := Taint{}
		got, err := Open(context.Background(), c, nil, uuid.Nil, out, again)
		if err != nil || !reflect.DeepEqual(got, in) {
			t.Errorf("opened, it is not the value sealed (%v)", err)
		}
		if !reflect.DeepEqual(again, taint) {
			t.Errorf("opening remembers %d values, sealing %d", len(again), len(taint))
		}
	}
}
