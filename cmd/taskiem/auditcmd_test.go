package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/audit"
)

// auditFixture writes an export of two entries and an anchors file holding
// one anchor per entry, the first signed by a retired key and the second
// by the current one; it returns the paths and the two public keys.
func auditFixture(t *testing.T) (export, anchors, oldKey, newKey string) {
	t.Helper()
	dir := t.TempDir()
	var out, anch bytes.Buffer
	prev := make([]byte, 32)
	signers := make([]*audit.Signer, 2)
	for i := range signers {
		seed := bytes.Repeat([]byte{byte(i + 1)}, 32)
		s, err := audit.NewSigner(seed)
		if err != nil {
			t.Fatal(err)
		}
		signers[i] = s
	}
	for seq := int64(1); seq <= 2; seq++ {
		e := audit.Entry{TenantID: "t1", Seq: seq, ActorType: "user", ActorID: "u1", Action: "test.action", Target: "x", Detail: `{"n": 1}`,
			At: "2026-01-02T03:04:05Z", PrevHash: hex.EncodeToString(prev)}
		sum := sha256.Sum256(append(append([]byte{}, prev...), audit.Canonical(e)...))
		e.Hash, prev = hex.EncodeToString(sum[:]), sum[:]
		line, _ := json.Marshal(audit.Line{Entry: &e})
		out.Write(append(line, '\n'))
		a := audit.Anchor{TenantID: "t1", Seq: seq, Hash: e.Hash, At: "2026-01-03T00:00:00Z"}
		signers[seq-1].Sign(&a)
		line, _ = json.Marshal(a)
		anch.Write(append(line, '\n'))
	}
	line, _ := json.Marshal(audit.Line{Head: &audit.Head{TenantID: "t1", Seq: 2, Hash: hex.EncodeToString(prev)}})
	out.Write(append(line, '\n'))
	export, anchors = filepath.Join(dir, "audit.jsonl"), filepath.Join(dir, "anchors.jsonl")
	for p, b := range map[string][]byte{export: out.Bytes(), anchors: anch.Bytes()} {
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return export, anchors, base64.StdEncoding.EncodeToString(signers[0].PublicKey()), base64.StdEncoding.EncodeToString(signers[1].PublicKey())
}

// With anchoring off, the scheduler says so and names what is unset.
func TestAnchoringOffIsSaid(t *testing.T) {
	if off := (config{AnchorKey: "k", AnchorDir: "/anchors"}).anchoringOff(); off != "" {
		t.Errorf("anchoring on: %q", off)
	}
	off := (config{AnchorKey: "k"}).anchoringOff()
	if !strings.Contains(off, "audit anchoring: off") || !strings.Contains(off, "TASKIEM_ANCHOR_DIR") || strings.Contains(off, "TASKIEM_ANCHOR_KEY") {
		t.Errorf("no directory: %q", off)
	}
}

// Verifying against anchors without a key checks no signature, so it does
// not pass unless the person says that is what they want; an anchors file
// with nothing in it proves nothing; and anchors signed before a key was
// changed verify with the retired key.
func TestAuditVerifyAnchorsNeedsKeys(t *testing.T) {
	export, anchors, oldKey, newKey := auditFixture(t)
	t.Setenv("TASKIEM_ANCHOR_PUBLIC_KEYS", "")
	verify := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := auditCmd(append(append([]string{"verify"}, args...), export), &out)
		return out.String(), err
	}
	if out, err := verify("--anchors", anchors); err == nil || !strings.Contains(err.Error(), "--key") {
		t.Errorf("no key: passed with %q, %v", out, err)
	}
	if out, err := verify("--anchors", anchors, "--unsigned-ok"); err != nil || !strings.Contains(out, "NOT checked") {
		t.Errorf("--unsigned-ok: %q %v", out, err)
	}
	if _, err := verify("--anchors", anchors, "--key", newKey); err == nil || !strings.Contains(err.Error(), "entry 1") {
		t.Errorf("only the current key: %v", err)
	}
	if out, err := verify("--anchors", anchors, "--key", newKey, "--key", oldKey); err != nil || !strings.Contains(out, "all 2 anchors") {
		t.Errorf("both keys: %q %v", out, err)
	}
	// Retired keys may come from the environment, as the server reads them.
	t.Setenv("TASKIEM_ANCHOR_PUBLIC_KEYS", oldKey)
	if _, err := verify("--anchors", anchors, "--key", newKey); err != nil {
		t.Errorf("retired key from the environment: %v", err)
	}
	empty := filepath.Join(t.TempDir(), "none.jsonl")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := verify("--anchors", empty, "--key", newKey); err == nil || !strings.Contains(err.Error(), "no anchors") {
		t.Errorf("an empty anchors file: passed with %q, %v", out, err)
	}
}
