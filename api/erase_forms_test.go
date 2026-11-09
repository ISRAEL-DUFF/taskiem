package api_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

// Erasing a person by their number erases every way it was sealed: as
// WhatsApp gives it, with + or a leading 0, as "other" (a chat sender may
// be declared so), and a number given as a number reaches it sealed as a
// string (a Telegram chat id). A subject named by its generation erases
// the person; nothing found at all is 404 (decision 0062 P2).
func TestEraseForms(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	v := w.env.Vault
	n := 0
	// business is a tenant whose owner can erase once (a step-up each).
	business := func() (*client, uuid.UUID, string) {
		n++
		owner := w.tenant(t, fmt.Sprintf("Shop %d", n), fmt.Sprintf("owner%d@shop.test", n))
		tenant := uuid.MustParse(owner.must(200, "GET", "/v1/me", nil)["tenant_id"].(string))
		return owner, tenant, enrol(t, owner)
	}
	seal := func(tenant uuid.UUID, category string, value any) map[string]any {
		t.Helper()
		var env map[string]any
		if err := db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			var err error
			env, err = v.SealTx(ctx, tx, tenant, category, value)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return env
	}
	open := func(tenant uuid.UUID, env map[string]any) error {
		return db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			_, err := v.OpenTx(ctx, tx, tenant, env)
			return err
		})
	}

	// A number written any way.
	owner, tenant, secret := business()
	sealed := []map[string]any{
		seal(tenant, "phone", "2348031234567"),
		seal(tenant, "phone", "+2348031234567"),
		seal(tenant, "other", "2348031234567"),
	}
	bola := seal(tenant, "phone", "2348039876543")
	// A message from Ada buffered for a run that is not waiting yet.
	if err := db.InTenantTx(ctx, v.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO signals (id, tenant_id, environment, event, correlation, payload, expires_at) VALUES ($1, $2, 'prod', 'whatsapp@1:messages', '2348031234567', '{}', now() + interval '1 day')`,
			uuid.Must(uuid.NewV7()), tenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if st, _ := owner.do("POST", "/v1/pii/erase", map[string]any{"category": "Phone:x", "value": "1", "totp": codeAfter(secret, 1)}); st != 400 {
		t.Fatalf("a bad category: %d", st)
	}
	out := owner.must(200, "POST", "/v1/pii/erase", map[string]any{"category": "phone", "value": "0803 123 4567", "totp": codeAfter(secret, 1)})
	if out["erased"] != true {
		t.Fatalf("erased: %v", out)
	}
	for i, env := range sealed {
		if err := open(tenant, env); err == nil {
			t.Errorf("form %d outlived the erasure", i)
		}
	}
	if err := open(tenant, bola); err != nil {
		t.Fatalf("another person erased: %v", err)
	}
	var buffered int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM signals WHERE tenant_id = $1`, tenant).Scan(&buffered); err != nil || buffered != 0 {
		t.Fatalf("buffered messages left: %d %v", buffered, err)
	}

	// A chat id given as a number, sealed as a string.
	owner, tenant, secret = business()
	chat := seal(tenant, "other", "777001")
	if out := owner.must(200, "POST", "/v1/pii/erase", map[string]any{"category": "other", "value": 777001, "totp": codeAfter(secret, 1)}); out["erased"] != true || open(tenant, chat) == nil {
		t.Fatalf("a chat id given as a number: %v", out)
	}

	// Named by a generation: the person (base and generations) is erased.
	owner, tenant, secret = business()
	ada := seal(tenant, "phone", "2348031234567")
	subject := ada["subject"].(string)
	if out := owner.must(200, "POST", "/v1/pii/erase", map[string]any{"subject": subject + "~2", "totp": codeAfter(secret, 1)}); out["subject"] != subject || open(tenant, ada) == nil {
		t.Fatalf("named by a generation: %v", out)
	}

	// Nothing to erase: 404.
	owner, _, secret = business()
	if st, body := owner.do("POST", "/v1/pii/erase", map[string]any{"category": "phone", "value": "08031234567", "totp": codeAfter(secret, 1)}); st != 404 {
		t.Fatalf("nothing to erase: %d %v", st, body)
	}
}
