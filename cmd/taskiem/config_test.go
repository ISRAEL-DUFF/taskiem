package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

// TestConfigBooleans: on/off and yes/no are understood, in any case, and
// a value that is none of them stops the process naming the variable
// (TASKIEM_SECURE_COOKIES=yes used to mean insecure cookies).
func TestConfigBooleans(t *testing.T) {
	t.Setenv("TASKIEM_DATABASE_URL", "postgres://localhost/taskiem")
	for _, c := range []struct {
		v    string
		want bool
	}{{"yes", true}, {"on", true}, {"ON", true}, {"True", true}, {"1", true}, {"no", false}, {"off", false}, {"0", false}, {"", true}} {
		t.Setenv("TASKIEM_SECURE_COOKIES", c.v)
		t.Setenv("TASKIEM_REQUIRE_ADMIN_PASSKEYS", c.v)
		cfg, err := loadConfig()
		if err != nil {
			t.Fatalf("%q: %v", c.v, err)
		}
		// Empty is the default: secure cookies on, passkeys off without a public URL.
		wantPasskeys := c.want && c.v != ""
		if cfg.SecureCookies != c.want || cfg.RequireAdminPasskeys != wantPasskeys {
			t.Errorf("%q: secure cookies %v, admin passkeys %v", c.v, cfg.SecureCookies, cfg.RequireAdminPasskeys)
		}
	}
	t.Setenv("TASKIEM_SECURE_COOKIES", "true")
	t.Setenv("TASKIEM_REQUIRE_ADMIN_PASSKEYS", "true")
	for _, name := range []string{"TASKIEM_SECURE_COOKIES", "TASKIEM_REQUIRE_ADMIN_PASSKEYS", "TASKIEM_ALLOW_SIGNUP", "TASKIEM_TRUST_PROXY", "TASKIEM_STATUS_PAGE", "TASKIEM_MAIL_ENABLED", "TASKIEM_PPROF"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "enabled")
			if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), name) {
				t.Errorf("%s=enabled: %v", name, err)
			}
		})
	}
}

// TestConfigNumbersAndKeys: a pool size or anchor key that does not parse
// stops the process instead of falling back (to 20 connections, to no
// anchoring).
func TestConfigNumbersAndKeys(t *testing.T) {
	t.Setenv("TASKIEM_DATABASE_URL", "postgres://localhost/taskiem")
	t.Setenv("TASKIEM_DATABASE_POOL", "40")
	if cfg, err := loadConfig(); err != nil || cfg.PoolSize != 40 {
		t.Fatalf("pool 40: %d %v", cfg.PoolSize, err)
	}
	for _, bad := range []string{"forty", "0", "-3"} {
		t.Setenv("TASKIEM_DATABASE_POOL", bad)
		if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "TASKIEM_DATABASE_POOL") {
			t.Errorf("pool %q: %v", bad, err)
		}
	}
	t.Setenv("TASKIEM_DATABASE_POOL", "")
	t.Setenv("TASKIEM_LOGIN_BURST", "lots")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "TASKIEM_LOGIN_BURST") {
		t.Errorf("login burst: %v", err)
	}
	t.Setenv("TASKIEM_LOGIN_BURST", "")
	t.Setenv("TASKIEM_ANCHOR_KEY", "not-a-key")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "TASKIEM_ANCHOR_KEY") {
		t.Errorf("anchor key: %v", err)
	}
	t.Setenv("TASKIEM_ANCHOR_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if _, err := loadConfig(); err != nil {
		t.Errorf("good anchor key: %v", err)
	}
}

// A dedicated pool's worker cannot serve the container queue: container
// steps run in the shared pool whatever the tenant's routing.
func TestConfigRefusesContainerInADedicatedPool(t *testing.T) {
	t.Setenv("TASKIEM_DATABASE_URL", "postgres://localhost/taskiem")
	t.Setenv("TASKIEM_WORKER_POOL", "acme")
	t.Setenv("TASKIEM_WORKER_QUEUES", "connector,container")
	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "TASKIEM_WORKER_QUEUES") {
		t.Errorf("container in a dedicated pool: %v", err)
	}
	t.Setenv("TASKIEM_WORKER_QUEUES", "connector,sandbox,ai")
	if _, err := loadConfig(); err != nil {
		t.Errorf("a dedicated pool without container: %v", err)
	}
	t.Setenv("TASKIEM_WORKER_POOL", "shared")
	t.Setenv("TASKIEM_WORKER_QUEUES", "container")
	if _, err := loadConfig(); err != nil {
		t.Errorf("container in the shared pool: %v", err)
	}
}
