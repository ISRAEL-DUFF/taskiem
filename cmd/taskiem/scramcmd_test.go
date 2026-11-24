package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestScramVerifierCmd: taskiem-vps hands a login's password to this
// command on stdin and puts what it prints in CREATE/ALTER ROLE, so the
// password never reaches a statement Postgres could log.
func TestScramVerifierCmd(t *testing.T) {
	var out bytes.Buffer
	if err := scramVerifierCmd(strings.NewReader("0123abcd\n"), &out); err != nil {
		t.Fatal(err)
	}
	v := strings.TrimSpace(out.String())
	if !strings.HasPrefix(v, "SCRAM-SHA-256$4096:") || strings.Contains(v, "0123abcd") || strings.ContainsAny(v, "'\n") {
		t.Errorf("verifier %q", v)
	}
	if err := scramVerifierCmd(strings.NewReader(""), &out); err == nil {
		t.Error("an empty password was accepted")
	}
}
