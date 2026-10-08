package alerts

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/byok/byoktest"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// A key_health rule alerts once when a customer key becomes unavailable
// and once when it recovers.
func TestKeyHealthAlerts(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	kms, err := secrets.NewLocalKMS(map[string][]byte{"root": bytes.Repeat([]byte{3}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	fake := byoktest.NewTransit(t)
	v := &secrets.Vault{Pool: d.App, KMS: kms, RootKey: "root", BYOK: byoktest.Factory(fake.Server)}
	tn := d.SeedTenant(t, nil)
	if _, _, err := v.EnableBYOK(ctx, tn.ID, fake.Config("customer"), map[string]string{"token": fake.Token}, "owner"); err != nil {
		t.Fatal(err)
	}
	ch := uuid.New()
	if _, err := d.Admin.Exec(ctx, `INSERT INTO alert_channels (id, tenant_id, kind, name, config, created_by) VALUES ($1, $2, 'email', 'ops', '{"to":["ops@example.com"]}', 'u')`, ch, tn.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Admin.Exec(ctx, `INSERT INTO alert_rules (id, tenant_id, name, kind, channel_ids, created_by) VALUES ($1, $2, 'keys', 'key_health', $3, 'u')`, uuid.New(), tn.ID, []uuid.UUID{ch}); err != nil {
		t.Fatal(err)
	}
	a := &Alerter{Pool: d.App, PublicURL: "https://app.example"}
	count := func() (n int, titles []string) {
		rows, err := d.Admin.Query(ctx, `SELECT title FROM alerts WHERE tenant_id = $1 AND kind = 'key_health' ORDER BY created_at`, tn.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var s string
			_ = rows.Scan(&s)
			titles = append(titles, s)
		}
		return len(titles), titles
	}
	if err := a.Evaluate(ctx, tn.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := count(); n != 0 {
		t.Fatalf("alert while healthy: %d", n)
	}
	fake.Revoke(true)
	for i := 0; i < 3; i++ {
		if _, err := v.CheckKey(ctx, tn.ID, "system"); err != nil {
			t.Fatal(err)
		}
		if err := a.Evaluate(ctx, tn.ID); err != nil {
			t.Fatal(err)
		}
	}
	if n, titles := count(); n != 1 || titles[0] != "Your encryption key is unavailable" {
		t.Fatalf("unavailable alerts: %v", titles)
	}
	fake.Revoke(false)
	if _, err := v.CheckKey(ctx, tn.ID, "system"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := a.Evaluate(ctx, tn.ID); err != nil {
			t.Fatal(err)
		}
	}
	if n, titles := count(); n != 2 || titles[1] != "Your encryption key works again" {
		t.Fatalf("recovery alerts: %v", titles)
	}
	var deliveries int
	_ = d.Admin.QueryRow(ctx, `SELECT count(*) FROM alert_deliveries WHERE tenant_id = $1`, tn.ID).Scan(&deliveries)
	if deliveries != 2 {
		t.Errorf("%d deliveries", deliveries)
	}
}
