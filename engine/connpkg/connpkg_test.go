package connpkg_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/connpkg"
	"github.com/israel-duff/taskiem/engine/conntest"
)

func sample(t *testing.T) (*connpkg.Package, string) {
	t.Helper()
	seed, pub, err := connpkg.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := connpkg.ParsePrivateKey(seed)
	if err != nil {
		t.Fatal(err)
	}
	p := &connpkg.Package{ID: "p_acme_ledger", Version: "1.0.0", Manifest: "manifest: connector/v1\n", Module: []byte("\x00asm"),
		Licence: "MIT", Conformance: &conntest.Suite{Cases: []conntest.Case{{Name: "a", Action: "get", Input: map[string]any{"n": 5000},
			Expect: conntest.Expect{Output: map[string]any{"ok": true}}}}},
		Attestation: connpkg.Attestation{OriginalWork: true, Contact: "dev@acme.test"}, CreatedAt: "2026-10-07T00:00:00Z"}
	p.Sign(key)
	return p, pub
}

func TestSignVerifyAndTamper(t *testing.T) {
	p, pub := sample(t)
	key, _ := connpkg.ParsePublicKey(pub)
	if err := p.Verify(key); err != nil {
		t.Fatal(err)
	}
	// Through the file format and back, it still verifies.
	var buf bytes.Buffer
	if err := connpkg.Write(&buf, p); err != nil {
		t.Fatal(err)
	}
	back, err := connpkg.Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := back.Verify(key); err != nil {
		t.Fatalf("round trip: %v", err)
	}

	// Any change to what was signed breaks the digest.
	for name, edit := range map[string]func(*connpkg.Package){
		"module":   func(p *connpkg.Package) { p.Module = []byte("\x00asn") },
		"manifest": func(p *connpkg.Package) { p.Manifest += "# more\n" },
		"licence":  func(p *connpkg.Package) { p.Licence = "Apache-2.0" },
		"cases":    func(p *connpkg.Package) { p.Conformance.Cases[0].Input["n"] = 1 },
	} {
		q, _ := sample(t)
		*q = *p
		q.Conformance = &conntest.Suite{Cases: []conntest.Case{{Name: "a", Action: "get", Input: map[string]any{"n": 5000}, Expect: p.Conformance.Cases[0].Expect}}}
		edit(q)
		if err := q.Verify(key); !errors.Is(err, connpkg.ErrDigest) {
			t.Errorf("%s changed: %v", name, err)
		}
	}
	// A recomputed digest without the key does not verify.
	forged := *p
	forged.Licence = "Apache-2.0"
	forged.Digest = forged.ComputeDigest()
	if err := forged.Verify(key); !errors.Is(err, connpkg.ErrSignature) {
		t.Errorf("forged digest: %v", err)
	}
	// Another publisher's key does not verify it.
	_, other := sample(t)
	otherKey, _ := connpkg.ParsePublicKey(other)
	if err := p.Verify(otherKey); !errors.Is(err, connpkg.ErrSignature) {
		t.Errorf("other key: %v", err)
	}
}

func TestReadRefusesOtherDocuments(t *testing.T) {
	for _, doc := range []string{`{"format":"something/v1"}`, `{"format":"taskiem-connector-package/v1","extra":1}`, `not json`} {
		if _, err := connpkg.Read(strings.NewReader(doc)); err == nil {
			t.Errorf("read %s", doc)
		}
	}
	if _, err := connpkg.ParsePrivateKey("short"); err == nil {
		t.Error("bad seed accepted")
	}
}
