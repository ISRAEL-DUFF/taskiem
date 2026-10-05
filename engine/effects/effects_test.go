package effects

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"testing"
)

func TestParseClass(t *testing.T) {
	for _, s := range []string{"read", "idempotent_write", "reconcilable_write", "unsafe_write"} {
		if _, err := ParseClass(s); err != nil {
			t.Errorf("ParseClass(%q): %v", s, err)
		}
	}
	for _, s := range []string{"", "write", "READ"} {
		if _, err := ParseClass(s); err == nil {
			t.Errorf("ParseClass(%q) accepted", s)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		err  error
		want ErrorKind
	}{
		{fmt.Errorf("503: %w", ErrRetryable), KindRetryable},
		{fmt.Errorf("401: %w", ErrFatal), KindFatal},
		{fmt.Errorf("reset after send: %w", ErrUnknownOutcome), KindUnknownOutcome},
		{fmt.Errorf("dns: %w", ErrNotSent), KindNotSent},
		{errors.New("something odd"), KindUnknownOutcome},
	}
	for _, c := range cases {
		if got := Classify(c.err); got != c.want {
			t.Errorf("Classify(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestAfterError(t *testing.T) {
	cases := []struct {
		c    Class
		k    ErrorKind
		want Next
	}{
		{Read, KindRetryable, Retry},
		{Read, KindUnknownOutcome, Retry},
		{Read, KindFatal, Fail},
		{IdempotentWrite, KindUnknownOutcome, Retry},
		{IdempotentWrite, KindRetryable, Retry},
		{ReconcilableWrite, KindUnknownOutcome, Reconcile},
		{ReconcilableWrite, KindRetryable, Retry},
		{UnsafeWrite, KindUnknownOutcome, Park},
		{UnsafeWrite, KindRetryable, Park},
		{UnsafeWrite, KindNotSent, Retry},
		{UnsafeWrite, KindFatal, Fail},
	}
	for _, c := range cases {
		if got := AfterError(c.c, c.k); got != c.want {
			t.Errorf("AfterError(%s, %s) = %s, want %s", c.c, c.k, got, c.want)
		}
	}
}

func TestOnOpenIntent(t *testing.T) {
	want := map[Class]Next{Read: Retry, IdempotentWrite: Retry, ReconcilableWrite: Reconcile, UnsafeWrite: Park}
	for c, n := range want {
		if got := OnOpenIntent(c); got != n {
			t.Errorf("OnOpenIntent(%s) = %s, want %s", c, got, n)
		}
	}
}

func TestAfterReconcile(t *testing.T) {
	if n, fresh := AfterReconcile(Found); n != Complete || fresh {
		t.Errorf("found: %s %v", n, fresh)
	}
	if n, fresh := AfterReconcile(NotFound); n != Retry || !fresh {
		t.Errorf("not_found: %s %v", n, fresh)
	}
	if n, _ := AfterReconcile(Indeterminate); n != Park {
		t.Errorf("indeterminate: %s", n)
	}
}

var paystack = Spec{
	Field: "reference", Encoding: Base32Lower, Length: 32, Prefix: "tsk_",
	Limits: &Limits{MinLength: 16, MaxLength: 50, Charset: "a-z0-9_-"},
}

func TestPaystackKeyFitsProviderRules(t *testing.T) {
	k, err := paystack.Key(KeyInput{TenantID: "t", Seed: "s", StepID: "pay"})
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[a-z0-9_-]{16,50}$`).MatchString(k) || len(k) != 36 {
		t.Fatalf("key %q does not fit Paystack's reference rules", k)
	}
}

func TestKeyDependsOnEveryInput(t *testing.T) {
	base := KeyInput{TenantID: "t1", Seed: "s1", StepID: "pay", AttemptGroup: 0}
	k0, _ := paystack.Key(base)
	if again, _ := paystack.Key(base); again != k0 {
		t.Fatal("key is not deterministic")
	}
	variants := []KeyInput{
		{TenantID: "t2", Seed: "s1", StepID: "pay"},
		{TenantID: "t1", Seed: "s2", StepID: "pay"},
		{TenantID: "t1", Seed: "s1", StepID: "pay2"},
		{TenantID: "t1", Seed: "s1", StepID: "pay", AttemptGroup: 1},
		// Separator ambiguity: moving a boundary must change the key.
		{TenantID: "t1s", Seed: "1", StepID: "pay"},
	}
	for _, v := range variants {
		if k, _ := paystack.Key(v); k == k0 {
			t.Errorf("%+v collides with base", v)
		}
	}
}

func TestDigestRejectsBadInput(t *testing.T) {
	bad := []KeyInput{
		{Seed: "s", StepID: "x"},
		{TenantID: "t", StepID: "x"},
		{TenantID: "t", Seed: "s"},
		{TenantID: "t", Seed: "a\x00b", StepID: "x"},
		{TenantID: "t", Seed: "s", StepID: "x", AttemptGroup: -1},
	}
	for _, in := range bad {
		if _, err := in.Digest(); err == nil {
			t.Errorf("Digest(%+v) accepted", in)
		}
	}
}

func TestSpecValidate(t *testing.T) {
	bad := map[string]Spec{
		"unknown encoding":       {Encoding: "rot13", Length: 32},
		"too few bits":           {Encoding: HexLower, Length: 31},
		"longer than digest":     {Encoding: HexLower, Length: 65},
		"over provider max":      {Encoding: HexLower, Length: 64, Limits: &Limits{MaxLength: 50}},
		"under provider min":     {Encoding: Base64URL, Length: 22, Limits: &Limits{MinLength: 30}},
		"prefix outside chars":   {Encoding: Base32Lower, Length: 32, Prefix: "TSK_", Limits: &Limits{Charset: "a-z0-9_-"}},
		"alphabet outside chars": {Encoding: Base64URL, Length: 22, Limits: &Limits{Charset: "a-z0-9"}},
		"bad charset regex":      {Encoding: HexLower, Length: 32, Limits: &Limits{Charset: "z-a"}},
	}
	for name, s := range bad {
		if err := s.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := paystack.Validate(); err != nil {
		t.Errorf("paystack spec rejected: %v", err)
	}
}

func TestVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct {
			Name         string `json:"name"`
			TenantID     string `json:"tenant_id"`
			Seed         string `json:"seed"`
			StepID       string `json:"step_id"`
			AttemptGroup int    `json:"attempt_group"`
			Encoding     string `json:"encoding"`
			Length       int    `json:"length"`
			Prefix       string `json:"prefix"`
			Expected     string `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) == 0 {
		t.Fatal("no vectors")
	}
	for _, c := range file.Cases {
		s := Spec{Encoding: Encoding(c.Encoding), Length: c.Length, Prefix: c.Prefix}
		got, err := s.Key(KeyInput{TenantID: c.TenantID, Seed: c.Seed, StepID: c.StepID, AttemptGroup: c.AttemptGroup})
		if err != nil {
			t.Errorf("%s: %v", c.Name, err)
			continue
		}
		if got != c.Expected {
			t.Errorf("%s: got %s, want %s", c.Name, got, c.Expected)
		}
	}
}
