package pii_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/pii"
)

// fakeVault is the vault's sealing without a database: a subject is the
// category and the value; its "ciphertext" is the JSON of the value
// (readable here, which is enough to follow the envelopes). Shredded
// subjects refuse to seal or open, as secrets.Vault does.
type fakeVault struct {
	keys     map[string]bool // subject -> shredded
	current  map[string]string
	sealings int
}

func newFakeVault() *fakeVault {
	return &fakeVault{keys: map[string]bool{}, current: map[string]string{}}
}

func (f *fakeVault) subject(category string, value any) string {
	return category + ":" + fmt.Sprint(value)
}

func (f *fakeVault) envelope(subject, category string, value any) (map[string]any, error) {
	if f.keys[subject] {
		return nil, pii.ErrErased
	}
	f.keys[subject] = false
	raw, _ := json.Marshal(value)
	f.sealings++
	return map[string]any{"$pii": category, "subject": subject, "ct": string(raw)}, nil
}

func (f *fakeVault) SealTx(_ context.Context, _ pgx.Tx, _ uuid.UUID, category string, value any) (map[string]any, error) {
	return f.envelope(f.subject(category, value), category, value)
}

func (f *fakeVault) SealUnderTx(_ context.Context, _ pgx.Tx, _ uuid.UUID, subject, category string, value any) (map[string]any, error) {
	if shredded, ok := f.keys[subject]; !ok || shredded {
		return nil, pii.ErrErased
	}
	return f.envelope(subject, category, value)
}

func (f *fakeVault) SealCurrentTx(ctx context.Context, tx pgx.Tx, t uuid.UUID, category string, value any) (map[string]any, error) {
	base := f.subject(category, value)
	if !f.keys[base] {
		return f.SealTx(ctx, tx, t, category, value)
	}
	if gen := f.current[base]; gen != "" {
		return f.SealUnderTx(ctx, tx, t, gen, category, value)
	}
	return nil, pii.ErrErased
}

func (f *fakeVault) SubjectForTx(_ context.Context, _ pgx.Tx, _ uuid.UUID, category string, value any) (string, error) {
	return f.subject(category, value), nil
}

func (f *fakeVault) OpenTx(_ context.Context, _ pgx.Tx, _ uuid.UUID, env map[string]any) (any, error) {
	subject, _ := env["subject"].(string)
	if shredded, ok := f.keys[subject]; !ok || shredded {
		return nil, pii.ErrErased
	}
	var v any
	if err := json.Unmarshal([]byte(env["ct"].(string)), &v); err != nil {
		return nil, err
	}
	return v, nil
}

// erase shreds a subject and its generations, as secrets.Vault.Erase.
func (f *fakeVault) erase(subject string) {
	base := pii.BaseSubject(subject)
	for s := range f.keys {
		if s == base || strings.HasPrefix(s, base+"~") {
			f.keys[s] = true
		}
	}
}

var bg = context.Background()

func open(t *testing.T, c pii.Cipher, v any) (any, pii.Taint) {
	t.Helper()
	taint := pii.Taint{}
	out, err := pii.Open(bg, c, nil, uuid.Nil, v, taint)
	if err != nil {
		t.Fatal(err)
	}
	return out, taint
}

// In a customer's run, free text and names are sealed under their own
// subject inside the customer's: erasing either erases them. The
// customer's own number is under the generation they are on.
func TestUnderSealsTwice(t *testing.T) {
	f := newFakeVault()
	const ada, bola = "2348031234567", "2348039876543"
	customer := "phone:" + ada
	f.keys[customer] = true // erased once; they came back
	f.keys[customer+"~2"] = false
	c := pii.Under(f, customer+"~2")
	if got := pii.CustomerOf(c); got != customer+"~2" {
		t.Fatalf("CustomerOf: %q", got)
	}

	words, err := c.SealTx(bg, nil, uuid.Nil, "other", "Call Bola on 0803")
	if err != nil {
		t.Fatal(err)
	}
	if words["subject"] != customer+"~2" {
		t.Fatalf("outer subject %v", words["subject"])
	}
	var inner map[string]any
	_ = json.Unmarshal([]byte(words["ct"].(string)), &inner)
	if inner["subject"] != "other:Call Bola on 0803" {
		t.Fatalf("inner envelope %v", inner)
	}
	out, taint := open(t, c, words)
	if out != "Call Bola on 0803" || taint["s:Call Bola on 0803"] != "other" {
		t.Fatalf("opened %v, taint %v", out, taint)
	}

	self, err := c.SealTx(bg, nil, uuid.Nil, "phone", ada)
	if err != nil || self["subject"] != customer+"~2" {
		t.Fatalf("the customer's own number: %v %v", self, err)
	}
	if s, _ := c.(pii.SubjectSealer).SubjectForTx(bg, nil, uuid.Nil, "phone", ada); s != customer+"~2" {
		t.Fatalf("SubjectForTx of the customer: %q", s)
	}
	other, err := c.SealTx(bg, nil, uuid.Nil, "phone", bola)
	if err != nil || other["subject"] != "phone:"+bola {
		t.Fatalf("another person's number keeps its own subject only: %v %v", other, err)
	}

	// Erasing the person the words are about (their own subject) erases
	// them inside the customer's envelope.
	f.erase("other:Call Bola on 0803")
	if out, _ := open(t, c, words); out != pii.Erased {
		t.Fatalf("after erasing the words' own subject: %v", out)
	}
	// Erasing the customer erases their words and number.
	name, _ := c.SealTx(bg, nil, uuid.Nil, "name", "Ada Obi")
	f.erase(customer)
	if out, _ := open(t, c, map[string]any{"a": name, "b": self}); fmt.Sprint(out) != "map[a:[erased] b:[erased]]" {
		t.Fatalf("after erasing the customer: %v", out)
	}
}

// A run whose customer was erased while it was still open carries on: what
// it is given is sealed as erased rather than failing (a message they send
// later wakes it, a timer fires, a step finishes).
func TestUnderAfterErasure(t *testing.T) {
	f := newFakeVault()
	const ada = "2348031234567"
	customer := "phone:" + ada
	f.keys[customer] = false
	c := pii.Under(f, customer)
	f.erase(customer)
	sealed, err := pii.Seal(bg, c, nil, uuid.Nil, map[string]any{"from": ada, "text": "Hello again", "amount": 5},
		[]pii.Path{{Segments: []string{"from"}, Category: "phone"}, {Segments: []string{"text"}, Category: "other"}}, pii.Taint{})
	if err != nil {
		t.Fatalf("sealing in an erased customer's run: %v", err)
	}
	m := sealed.(map[string]any)
	for _, k := range []string{"from", "text"} {
		env, ok := m[k].(map[string]any)
		if !ok || !pii.IsTombstone(env) || env["subject"] != customer {
			t.Fatalf("%s: %v", k, m[k])
		}
	}
	if out, _ := open(t, c, sealed); fmt.Sprint(out) != "map[amount:5 from:[erased] text:[erased]]" {
		t.Fatalf("opened: %v", out)
	}
	// Another person erased: a tombstone under theirs.
	f.keys["phone:2348039876543"] = true
	env, err := c.SealTx(bg, nil, uuid.Nil, "phone", "2348039876543")
	if err != nil || !pii.IsTombstone(env) || env["subject"] != "phone:2348039876543" {
		t.Fatalf("an erased person's number: %v %v", env, err)
	}
	// A string made from a known value of the erased customer is erased.
	taint := pii.Taint{}
	taint.Add("Hello again", "other")
	got, err := pii.Seal(bg, c, nil, uuid.Nil, map[string]any{"reply": "You said: Hello again"}, nil, taint)
	if err != nil || got.(map[string]any)["reply"] != pii.Erased {
		t.Fatalf("a derived string: %v %v", got, err)
	}
}

// A string made from known values in a customer's run is sealed under
// each person it names and the customer: erasing any of them erases it.
func TestUnderDerived(t *testing.T) {
	for _, erase := range []string{"name:Bola Tinubu", "phone:2348031234567"} {
		f := newFakeVault()
		customer := "phone:2348031234567"
		f.keys[customer] = false
		c := pii.Under(f, customer)
		taint := pii.Taint{}
		taint.Add("Bola Tinubu", "name")
		sealed, err := pii.Seal(bg, c, nil, uuid.Nil, map[string]any{"note": "Ask Bola Tinubu to call"}, nil, taint)
		if err != nil {
			t.Fatal(err)
		}
		if out, _ := open(t, c, sealed); out.(map[string]any)["note"] != "Ask Bola Tinubu to call" {
			t.Fatalf("opened: %v", out)
		}
		f.erase(erase)
		if out, _ := open(t, c, sealed); out.(map[string]any)["note"] != pii.Erased {
			t.Fatalf("after erasing %s: %v", erase, out)
		}
	}
}

// Data just received (a buffered signal) is sealed under its person's key
// now: the generation they were given when they came back, or, erased and
// not back, kept as erased rather than refused.
func TestReceived(t *testing.T) {
	f := newFakeVault()
	c := pii.Received(f)
	f.keys["phone:1"] = true
	env, err := c.SealTx(bg, nil, uuid.Nil, "phone", "1")
	if err != nil || !pii.IsTombstone(env) {
		t.Fatalf("erased, not back: %v %v", env, err)
	}
	if _, err := pii.Current(f).SealTx(bg, nil, uuid.Nil, "phone", "1"); err != pii.ErrErased {
		t.Fatalf("Current, erased, not back: %v", err)
	}
	f.keys["phone:1~2"] = false
	f.current["phone:1"] = "phone:1~2"
	if env, err := c.SealTx(bg, nil, uuid.Nil, "phone", "1"); err != nil || env["subject"] != "phone:1~2" {
		t.Fatalf("back: %v %v", env, err)
	}
	if env, err := pii.Current(f).SealTx(bg, nil, uuid.Nil, "phone", "1"); err != nil || env["subject"] != "phone:1~2" {
		t.Fatalf("Current, back: %v %v", env, err)
	}
	if env, err := c.SealTx(bg, nil, uuid.Nil, "other", "hi"); err != nil || env["subject"] != "other:hi" {
		t.Fatalf("never erased: %v %v", env, err)
	}
}

func TestBaseSubject(t *testing.T) {
	for in, want := range map[string]string{
		"phone:ab12":     "phone:ab12",
		"phone:ab12~2":   "phone:ab12",
		"phone:ab12~13":  "phone:ab12",
		"phone:ab12~":    "phone:ab12~",
		"phone:ab12~x2":  "phone:ab12~x2",
		"":               "",
		"other:1~2~3":    "other:1~2",
		"plain-no-tilde": "plain-no-tilde",
	} {
		if got := pii.BaseSubject(in); got != want {
			t.Errorf("BaseSubject(%q) = %q, want %q", in, got, want)
		}
	}
}
