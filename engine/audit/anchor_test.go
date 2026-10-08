package audit_test

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/audit"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

func appendEntries(t *testing.T, d *dbtest.DB, tenant uuid.UUID, n int) {
	t.Helper()
	err := db.InTenantTx(ctx, d.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		for i := 0; i < n; i++ {
			if _, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', 'u1', 'test.action', 'x', '{"amount": 5000}'::jsonb)`, tenant); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// rewrite changes an entry's detail and recomputes every hash after it, as
// someone with write access to the database could.
func rewrite(t *testing.T, export *bytes.Buffer, seq int64) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	prev := make([]byte, 32)
	sc := bufio.NewScanner(export)
	for sc.Scan() {
		var l audit.Line
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatal(err)
		}
		if l.Entry != nil {
			if l.Entry.Seq == seq {
				l.Entry.Detail = strings.Replace(l.Entry.Detail, "5000", "500000", 1)
			}
			l.Entry.PrevHash = hex.EncodeToString(prev)
			sum := sha256.Sum256(append(append([]byte{}, prev...), audit.Canonical(*l.Entry)...))
			l.Entry.Hash = hex.EncodeToString(sum[:])
			prev = sum[:]
		}
		if l.Head != nil {
			l.Head.Hash = hex.EncodeToString(prev)
		}
		b, _ := json.Marshal(l)
		out.Write(append(b, '\n'))
	}
	return &out
}

func TestAnchorsCatchARewrittenChain(t *testing.T) {
	d := dbtest.New(t)
	tn := d.SeedTenant(t, nil)
	appendEntries(t, d, tn.ID, 3)
	signer, err := audit.NewSigner(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	x := &audit.Anchorer{Pool: d.App, Signer: signer, Dir: dir, Interval: time.Hour}
	n, err := x.Tick(ctx)
	if err != nil || n < 1 {
		t.Fatalf("tick: %d %v", n, err)
	}
	// Anchored within the interval: nothing more to do, even after appends.
	appendEntries(t, d, tn.ID, 2)
	if n, _ := x.Tick(ctx); n != 0 {
		t.Errorf("anchored %d again within the interval", n)
	}

	raw, err := os.ReadFile(filepath.Join(dir, tn.ID.String()+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	external, err := audit.ReadAnchors(bytes.NewReader(raw))
	if err != nil || len(external) != 1 {
		t.Fatalf("external anchors: %v %v", external, err)
	}
	var stored []audit.Anchor
	_ = db.InTenantTx(ctx, d.App, []uuid.UUID{tn.ID}, func(tx pgx.Tx) error {
		stored, err = audit.ListAnchors(ctx, tx)
		return err
	})
	if len(stored) != 1 || stored[0] != external[0] {
		t.Fatalf("stored %v, external %v", stored, external)
	}
	if !external[0].VerifySignature(signer.PublicKey()) {
		t.Fatal("signature")
	}

	// The real export agrees with the anchor.
	res, err := audit.VerifyWithAnchors(export(t, d, tn.ID), external, signer.PublicKey())
	if err != nil || res.FirstBroken != 0 || res.Anchors != 1 {
		t.Fatalf("intact: %+v %v", res, err)
	}
	// A rewrite with every hash recomputed passes the chain check alone...
	forged := rewrite(t, export(t, d, tn.ID), 2)
	if res, _ := audit.Verify(bytes.NewReader(forged.Bytes())); res.FirstBroken != 0 {
		t.Fatalf("the forged chain should be self-consistent: %+v", res)
	}
	// ...but not the anchor made before it.
	res, _ = audit.VerifyWithAnchors(bytes.NewReader(forged.Bytes()), external, signer.PublicKey())
	if res.FirstBroken != external[0].Seq || !strings.Contains(res.Reason, "rewritten") {
		t.Errorf("forged: %+v", res)
	}
	// A forged anchor is refused.
	other, _ := audit.NewSigner(bytes.Repeat([]byte{8}, 32))
	if res, _ := audit.VerifyWithAnchors(export(t, d, tn.ID), external, other.PublicKey()); !strings.Contains(res.Reason, "not signed by this key") {
		t.Errorf("wrong key: %+v", res)
	}
	// Anchors are append-only.
	if _, err := d.Admin.Exec(ctx, `UPDATE audit_anchors SET chain_seq = 99 WHERE tenant_id = $1`, tn.ID); err == nil {
		t.Error("an anchor was changed")
	}
}
