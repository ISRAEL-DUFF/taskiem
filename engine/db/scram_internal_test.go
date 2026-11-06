package db

import (
	"strings"
	"testing"
)

// TestScramVerifier: the verifier is the one Postgres stores for the
// password (RFC 5802/7677, as psql's \password computes it), here against
// a vector computed apart, with Python's hashlib.
func TestScramVerifier(t *testing.T) {
	salt := make([]byte, 16)
	for i := range salt {
		salt[i] = byte(i)
	}
	const want = "SCRAM-SHA-256$4096:AAECAwQFBgcICQoLDA0ODw==$2TQJOEc/xNMdBXdACh3RQYb4KLusaFHvONdbl1bGM7Y=:2eKDW8OHqNUG+FYHEV21ssc+WDnsWuxV2KL/Ldkf/Iw="
	if got, err := scramVerifier("pw-taskiem", salt); err != nil || got != want {
		t.Errorf("verifier\n got %s\nwant %s", got, want)
	}
	a, err := ScramVerifier("pw-taskiem")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ScramVerifier("pw-taskiem")
	if a == b || !strings.HasPrefix(a, "SCRAM-SHA-256$4096:") {
		t.Errorf("two verifiers with a fresh salt each: %s, %s", a, b)
	}
}

// TestLoginStatementHoldsNoPassword: the CREATE/ALTER ROLE migrate sends
// carries the password's SCRAM verifier, never the password, which
// Postgres would write to its log with a failing statement (or every DDL
// statement, under log_statement=ddl).
func TestLoginStatementHoldsNoPassword(t *testing.T) {
	const pw = "s3cret-it'is"
	stmt, err := loginStatement("ALTER", `taskiem "login"`, pw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stmt, pw) || strings.Contains(stmt, "s3cret") {
		t.Errorf("the statement holds the password: %s", stmt)
	}
	if !strings.HasPrefix(stmt, `ALTER ROLE "taskiem ""login""" LOGIN NOBYPASSRLS PASSWORD 'SCRAM-SHA-256$4096:`) {
		t.Errorf("statement %s", stmt)
	}
	if stmt, _ := loginStatement("CREATE", "taskiem_login", ""); stmt != `CREATE ROLE "taskiem_login" LOGIN NOBYPASSRLS` {
		t.Errorf("no password: %s", stmt)
	}
}
