package secrets

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// A tenant key or pseudonym key made in a transaction that rolled back is
// never served from the cache once another process made the real one.
func TestCacheIgnoresRolledBackKeys(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	kms, _ := NewLocalKMS(map[string][]byte{"root": bytes.Repeat([]byte{5}, 32)})
	a := &Vault{Pool: d.App, KMS: kms, RootKey: "root"}
	b := &Vault{Pool: d.App, KMS: kms, RootKey: "root"}
	tn := d.SeedTenant(t, nil)
	rollback := errors.New("rollback")
	err := db.InTenantTx(ctx, d.App, []uuid.UUID{tn.ID}, func(tx pgx.Tx) error {
		if _, err := a.pseudonymKey(ctx, tx, tn.ID); err != nil { // creates and caches version 1 and the pseudonym key
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if _, err := b.Put(ctx, tn.ID, "prod", "k", []byte("v"), "u"); err != nil {
		t.Fatal(err)
	}
	if got, err := a.Get(ctx, tn.ID, "prod", "k"); err != nil || got != "v" {
		t.Fatalf("stale tenant key served: %q %v", got, err)
	}
	sa, err := a.SubjectFor(ctx, tn.ID, "bvn", "22212345678")
	if err != nil {
		t.Fatal(err)
	}
	sb, err := b.SubjectFor(ctx, tn.ID, "bvn", "22212345678")
	if err != nil || sa != sb {
		t.Fatalf("stale pseudonym key served: %s vs %s (%v)", sa, sb, err)
	}
}
