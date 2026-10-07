package byok_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/byok"
	"github.com/israel-duff/taskiem/engine/byok/byoktest"
)

var ctx = context.Background()

func roundTrip(t *testing.T, p byok.Provider, prefix string) string {
	t.Helper()
	secret := []byte("local:v1:platform-wrapped tenant key")
	ct, err := p.Wrap(ctx, secret)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if !strings.HasPrefix(ct, prefix) || bytes.Contains([]byte(ct), secret) {
		t.Fatalf("ciphertext %q", ct)
	}
	pt, err := p.Unwrap(ctx, ct)
	if err != nil || !bytes.Equal(pt, secret) {
		t.Fatalf("unwrap: %q %v", pt, err)
	}
	return ct
}

func TestVaultTransit(t *testing.T) {
	fake := byoktest.NewTransit(t)
	f := byoktest.Factory(fake.Server)
	cfg := fake.Config("customer")

	p, err := f.New(cfg, map[string]string{"token": fake.Token}, byok.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ct := roundTrip(t, p, "vault:v1:")

	// AppRole logs in, and logs in again when its token is refused.
	ar, err := f.New(cfg, map[string]string{"role_id": fake.RoleID, "secret_id": fake.SecretID}, byok.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if pt, err := ar.Unwrap(ctx, ct); err != nil || len(pt) == 0 {
		t.Fatalf("approle unwrap: %v", err)
	}

	// Disabled, revoked or sealed: unavailable, never a wrong answer.
	fake.Disable("customer", true)
	if _, err := p.Unwrap(ctx, ct); !errors.Is(err, byok.ErrUnavailable) || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("disabled: %v", err)
	}
	fake.Disable("customer", false)
	fake.Revoke(true)
	if _, err := ar.Unwrap(ctx, ct); !errors.Is(err, byok.ErrUnavailable) {
		t.Errorf("revoked: %v", err)
	}
	fake.Revoke(false)
	fake.Down(true)
	if _, err := p.Wrap(ctx, []byte("x")); !errors.Is(err, byok.ErrUnavailable) {
		t.Errorf("sealed: %v", err)
	}
	fake.Down(false)
	if _, err := p.Unwrap(ctx, ct); err != nil {
		t.Errorf("after recovery: %v", err)
	}

	// Another key cannot open it.
	fake.AddKey("other")
	other, _ := f.New(fake.Config("other"), map[string]string{"token": fake.Token}, byok.Options{})
	if _, err := other.Unwrap(ctx, ct); !errors.Is(err, byok.ErrUnavailable) {
		t.Errorf("other key opened it: %v", err)
	}

	// Without the customer's CA the server is not trusted: verification
	// is never skipped.
	noCA := cfg
	noCA.CACert = ""
	plain := byoktest.Factory()
	if p2, err := plain.New(noCA, map[string]string{"token": fake.Token}, byok.Options{}); err != nil {
		t.Fatal(err)
	} else if _, err := p2.Unwrap(ctx, ct); !errors.Is(err, byok.ErrUnavailable) || !strings.Contains(err.Error(), "certificate") {
		t.Errorf("untrusted server: %v", err)
	}

	// The egress guard refuses loopback and private addresses by default.
	strict := &byok.Factory{CACert: byoktest.CACert(fake.Server)}
	if p3, err := strict.New(cfg, map[string]string{"token": fake.Token}, byok.Options{}); err != nil {
		t.Fatal(err)
	} else if _, err := p3.Unwrap(ctx, ct); !errors.Is(err, byok.ErrUnavailable) || !strings.Contains(err.Error(), "egress denied") {
		t.Errorf("loopback reached: %v", err)
	}
	// Allowing private ranges (dedicated deployments) still refuses loopback.
	private := &byok.Factory{CACert: byoktest.CACert(fake.Server), AllowPrivate: true}
	if p4, err := private.New(cfg, map[string]string{"token": fake.Token}, byok.Options{}); err != nil {
		t.Fatal(err)
	} else if _, err := p4.Unwrap(ctx, ct); !errors.Is(err, byok.ErrUnavailable) {
		t.Errorf("loopback reached with private ranges allowed: %v", err)
	}
}

func TestCloudProviders(t *testing.T) {
	cloud := byoktest.NewCloud(t)
	f := cloud.Factory()
	for _, tc := range []struct {
		name   string
		cfg    func() (byok.Config, map[string]string)
		prefix string
	}{
		{"aws", cloud.AWS, "aws:"},
		{"gcp", cloud.GCP, "gcp:"},
		{"azure", cloud.Azure, "azure:" + cloud.AzureKeyVersion + ":"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, creds := tc.cfg()
			p, err := f.New(cfg, creds, byok.Options{Tenant: "11111111-2222-3333-4444-555555555555"})
			if err != nil {
				t.Fatal(err)
			}
			ct := roundTrip(t, p, tc.prefix)
			if tc.name != "azure" {
				// The tenant is bound into the encryption context: another
				// tenant's provider cannot open it.
				q, _ := f.New(cfg, creds, byok.Options{Tenant: "99999999-2222-3333-4444-555555555555"})
				if _, err := q.Unwrap(ctx, ct); !errors.Is(err, byok.ErrUnavailable) {
					t.Errorf("opened under another tenant's context: %v", err)
				}
			}
			cloud.Disable(true)
			if _, err := p.Unwrap(ctx, ct); !errors.Is(err, byok.ErrUnavailable) {
				t.Errorf("disabled: %v", err)
			}
			cloud.Disable(false)
			cloud.Deny(true)
			if _, err := p.Unwrap(ctx, ct); !errors.Is(err, byok.ErrUnavailable) {
				t.Errorf("denied: %v", err)
			}
			cloud.Deny(false)
			if _, err := p.Unwrap(ctx, ct); err != nil {
				t.Errorf("after recovery: %v", err)
			}
		})
	}
	// Wrong AWS secret: the signature check fails at the fake.
	cfg, creds := cloud.AWS()
	creds["secret_access_key"] = "wrong"
	p, _ := f.New(cfg, creds, byok.Options{})
	if _, err := p.Wrap(ctx, []byte("x")); !errors.Is(err, byok.ErrUnavailable) || !strings.Contains(err.Error(), "InvalidSignature") {
		t.Errorf("bad signature: %v", err)
	}
}

func TestValidate(t *testing.T) {
	cloud := byoktest.NewCloud(t)
	_, gcpCreds := cloud.GCP()
	_, azCreds := cloud.Azure()
	for _, tc := range []struct {
		name  string
		cfg   byok.Config
		creds map[string]string
		want  string
	}{
		{"http address", byok.Config{Provider: byok.VaultTransit, Address: "http://bao:8200", Key: "k"}, map[string]string{"token": "t"}, "https"},
		{"address with path", byok.Config{Provider: byok.VaultTransit, Address: "https://bao:8200/v1/x", Key: "k"}, map[string]string{"token": "t"}, "no path"},
		{"bad key", byok.Config{Provider: byok.VaultTransit, Address: "https://bao", Key: "../sys"}, map[string]string{"token": "t"}, "key"},
		{"mount escape", byok.Config{Provider: byok.VaultTransit, Address: "https://bao", Key: "k", Mount: "a/../sys"}, map[string]string{"token": "t"}, "mount"},
		{"both creds", byok.Config{Provider: byok.VaultTransit, Address: "https://bao", Key: "k"}, map[string]string{"token": "t", "role_id": "r", "secret_id": "s"}, "exactly one"},
		{"no creds", byok.Config{Provider: byok.VaultTransit, Address: "https://bao", Key: "k"}, map[string]string{}, "exactly one"},
		{"bad ca", byok.Config{Provider: byok.VaultTransit, Address: "https://bao", Key: "k", CACert: "nope"}, map[string]string{"token": "t"}, "ca_cert"},
		{"aws region", byok.Config{Provider: byok.AWSKMS, Region: "mars", Key: byoktest.AWSKey}, map[string]string{"access_key_id": "a", "secret_access_key": "b"}, "region"},
		{"aws address", byok.Config{Provider: byok.AWSKMS, Region: "eu-west-1", Key: "alias/x", Address: "https://evil.example"}, map[string]string{"access_key_id": "a", "secret_access_key": "b"}, "address"},
		{"aws extra field", byok.Config{Provider: byok.AWSKMS, Region: "eu-west-1", Key: "alias/x"}, map[string]string{"access_key_id": "a", "secret_access_key": "b", "token": "c"}, "does not apply"},
		{"gcp name", byok.Config{Provider: byok.GCPKMS, Key: "projects/x"}, gcpCreds, "crypto key"},
		{"azure host", byok.Config{Provider: byok.AzureKeyVault, Address: "https://evil.example", Key: "k", KeyVersion: cloud.AzureKeyVersion}, azCreds, "vault URL"},
		{"azure version", byok.Config{Provider: byok.AzureKeyVault, Address: "https://acme.vault.azure.net", Key: "k"}, azCreds, "key_version"},
		{"unknown", byok.Config{Provider: "rot13"}, nil, "provider must be"},
	} {
		err := byok.Validate(tc.cfg.Normalise(), tc.creds)
		if !errors.Is(err, byok.ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	if err := byok.Validate(byok.Config{Provider: byok.VaultTransit, Address: "https://bao.example.com:8200", Key: "customer"}.Normalise(), map[string]string{"token": "t"}); err != nil {
		t.Errorf("valid transit: %v", err)
	}
	if _, err := (&byok.Factory{}).New(byok.Config{Provider: byok.GCPKMS, Key: byoktest.GCPKey}, map[string]string{"service_account_json": "{}"}, byok.Options{}); !errors.Is(err, byok.ErrInvalid) {
		t.Errorf("empty service account: %v", err)
	}
	d1 := byok.CredentialDigest(map[string]string{"token": "a"})
	if d1 == byok.CredentialDigest(map[string]string{"token": "b"}) || len(d1) != 12 {
		t.Errorf("digest %q", d1)
	}
}
