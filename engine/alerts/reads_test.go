package alerts

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// Delivering to a channel decrypts its secret: the read is recorded.
func TestDeliveryRecordsSecretRead(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	kms, err := secrets.NewLocalKMS(map[string][]byte{"root": bytes.Repeat([]byte{3}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	v := &secrets.Vault{Pool: d.App, KMS: kms, RootKey: "root"}
	tn := d.SeedTenant(t, nil)
	ch := uuid.New()
	if _, err := v.Put(ctx, tn.ID, VaultEnv, SecretName(ch), []byte("https://hooks.slack.com/services/T0/B0/x"), "test"); err != nil {
		t.Fatal(err)
	}
	a := &Alerter{Pool: d.App, Secrets: v, Client: &http.Client{Transport: failing{}}}
	if err := a.SendTest(ctx, tn.ID, ch, "slack", []byte(`{}`)); err == nil {
		t.Fatal("delivery through a failing transport succeeded")
	}
	var kind, env, name, purpose string
	if err := d.Admin.QueryRow(ctx, `SELECT kind, environment, name, purpose FROM secret_reads WHERE tenant_id = $1`, tn.ID).Scan(&kind, &env, &name, &purpose); err != nil {
		t.Fatal(err)
	}
	if kind != "alert_channel" || env != VaultEnv || name != SecretName(ch) || purpose != "alert.deliver" {
		t.Errorf("read: %s %s %s %s", kind, env, name, purpose)
	}
}
