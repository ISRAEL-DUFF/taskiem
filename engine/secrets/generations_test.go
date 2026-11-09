package secrets_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// A person erased stays erased for SealTx; only RenewSubjectTx (a customer
// who writes again) gives them a new generation, which Erase, named by any
// generation, shreds with the rest (decision 0062 P2).
func TestSubjectGenerations(t *testing.T) {
	d := dbtest.New(t)
	v := localVault(t, d)
	tn := d.SeedTenant(t, nil).ID
	const ada = "2348031234567"
	in := func(fn func(tx pgx.Tx) error) {
		t.Helper()
		if err := db.InTenantTx(ctx, v.Pool, []uuid.UUID{tn}, fn); err != nil {
			t.Fatal(err)
		}
	}
	seal := func(value any) (map[string]any, error) {
		var env map[string]any
		err := db.InTenantTx(ctx, v.Pool, []uuid.UUID{tn}, func(tx pgx.Tx) error {
			var err error
			env, err = v.SealTx(ctx, tx, tn, "phone", value)
			return err
		})
		return env, err
	}
	open := func(env map[string]any) (any, error) {
		var out any
		err := db.InTenantTx(ctx, v.Pool, []uuid.UUID{tn}, func(tx pgx.Tx) error {
			var err error
			out, err = v.OpenTx(ctx, tx, tn, env)
			return err
		})
		return out, err
	}
	subjects := func() (live, renewed string) {
		in(func(tx pgx.Tx) error {
			var err error
			if live, err = v.LiveSubjectTx(ctx, tx, tn, "phone", ada); err != nil {
				return err
			}
			renewed, err = v.RenewSubjectTx(ctx, tx, tn, "phone", ada)
			return err
		})
		return live, renewed
	}

	base, err := v.SubjectFor(ctx, tn, "phone", ada)
	if err != nil {
		t.Fatal(err)
	}
	if live, renewed := subjects(); live != base || renewed != base {
		t.Fatalf("a person never erased: live %q, renewed %q, want %q", live, renewed, base)
	}
	before, err := seal(ada)
	if err != nil || before["subject"] != base {
		t.Fatalf("seal: %v %v", before, err)
	}
	if err := v.Erase(ctx, tn, base, "test"); err != nil {
		t.Fatal(err)
	}
	// Erased: SealTx refuses, SubjectForTx stays the base, and nothing
	// moves on to a new generation by itself.
	if _, err := seal(ada); !errors.Is(err, pii.ErrErased) {
		t.Fatalf("sealing an erased person: %v", err)
	}
	in(func(tx pgx.Tx) error {
		s, err := v.SubjectForTx(ctx, tx, tn, "phone", ada)
		if err != nil || s != base {
			t.Errorf("SubjectForTx after erasure: %q %v", s, err)
		}
		if live, err := v.LiveSubjectTx(ctx, tx, tn, "phone", ada); err != nil || live != base {
			t.Errorf("LiveSubjectTx before they came back: %q %v", live, err)
		}
		if _, err := v.SealCurrentTx(ctx, tx, tn, "phone", ada); !errors.Is(err, pii.ErrErased) {
			t.Errorf("SealCurrentTx before they came back: %v", err)
		}
		return nil
	})
	// They come back: a new generation, which SealCurrentTx follows.
	live, renewed := subjects()
	if renewed != base+"~2" || live != base {
		t.Fatalf("they came back: live (read before renewing) %q, renewed %q", live, renewed)
	}
	if again, _ := subjects(); again != base+"~2" {
		t.Fatalf("live after renewing: %q", again)
	}
	var current map[string]any
	in(func(tx pgx.Tx) error {
		var err error
		current, err = v.SealCurrentTx(ctx, tx, tn, "phone", ada)
		return err
	})
	if current["subject"] != base+"~2" {
		t.Fatalf("SealCurrentTx after they came back: %v", current)
	}
	if got, err := open(current); err != nil || got != ada {
		t.Fatalf("open the new generation: %v %v", got, err)
	}
	if _, err := open(before); !errors.Is(err, pii.ErrErased) {
		t.Fatalf("the first generation opened again: %v", err)
	}
	// SealTx still refuses: plaintext read before the erasure is never
	// sealed readable again by it.
	if _, err := seal(ada); !errors.Is(err, pii.ErrErased) {
		t.Fatalf("SealTx after they came back: %v", err)
	}
	// Erased again, named by their generation: every generation goes.
	if err := v.Erase(ctx, tn, base+"~2", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := open(current); !errors.Is(err, pii.ErrErased) {
		t.Fatalf("the second generation outlived its erasure: %v", err)
	}
	if err := v.Erase(ctx, tn, base, "test"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("erasing twice: %v", err)
	}
	if _, renewed := subjects(); renewed != base+"~3" {
		t.Fatalf("third time: %q", renewed)
	}
	// Erasing by the base reaches the generations.
	if err := v.Erase(ctx, tn, base, "test"); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM subject_keys WHERE tenant_id = $1 AND subject_id LIKE $2 AND shredded_at IS NULL`, tn, base+"%").Scan(&left); err != nil || left != 0 {
		t.Fatalf("keys left: %d %v", left, err)
	}
	// A tombstone opens as erased whatever its subject.
	if _, err := open(pii.Tombstone("phone", base+"~9")); !errors.Is(err, pii.ErrErased) {
		t.Fatalf("tombstone: %v", err)
	}
	// Categories are letters and underscores: no id collides with a
	// generation's prefix.
	if _, err := v.SubjectFor(ctx, tn, "phone:x~2", ada); err == nil || !strings.Contains(err.Error(), "category") {
		t.Fatalf("a category with ':' and '~': %v", err)
	}
}

// The generations' lookup is a range an index serves, not a scan.
func TestSubjectGenerationsUseTheIndex(t *testing.T) {
	d := dbtest.New(t)
	tx, err := d.Admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// A test database has few keys, where a scan is cheapest: with scans
	// priced out, the planner shows what serves the range.
	if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `EXPLAIN (COSTS OFF) SELECT subject_id FROM subject_keys
		WHERE tenant_id = '00000000-0000-0000-0000-000000000001' AND subject_id ~>=~ ('phone:ab' || '~') AND subject_id ~<~ ('phone:ab' || chr(127))`)
	if err != nil {
		t.Fatal(err)
	}
	var plan string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan += line + "\n"
	}
	if !strings.Contains(plan, "subject_keys_generations") || !strings.Contains(plan, "Index Cond: ((tenant_id") {
		t.Fatalf("the generations' range is not served by subject_keys_generations:\n%s", plan)
	}
}
