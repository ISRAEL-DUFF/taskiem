package main

import (
	"context"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

func TestLoadPublicConfig(t *testing.T) {
	// Only its own login: no TASKIEM_DATABASE_URL, no KMS, nothing else.
	t.Setenv("TASKIEM_DATABASE_URL", "")
	t.Setenv("TASKIEM_PUBLIC_DATABASE_URL", "postgres://pub@db/taskiem")
	c, err := loadPublicConfig()
	if err != nil {
		t.Fatal(err)
	}
	if c.Public.DSN != "postgres://pub@db/taskiem" || c.Public.Role != "taskiem_public" || c.Public.PoolSize != 5 || c.Public.Listen != ":8082" {
		t.Fatalf("%+v", c.Public)
	}

	t.Setenv("TASKIEM_PUBLIC_DATABASE_URL", "")
	if _, err := loadPublicConfig(); err == nil {
		t.Fatal("no database at all must be refused")
	}
	// Without its own, the public host falls back to the platform's DSN.
	t.Setenv("TASKIEM_DATABASE_URL", "postgres://app@db/taskiem")
	if c, err := loadPublicConfig(); err != nil || c.Public.DSN != "postgres://app@db/taskiem" {
		t.Fatalf("%v %+v", err, c.Public)
	}

	for _, v := range []string{"0", "101", "lots"} {
		t.Setenv("TASKIEM_PUBLIC_DATABASE_POOL", v)
		if _, err := loadPublicConfig(); err == nil {
			t.Errorf("pool %q accepted", v)
		}
	}
}

func TestPublicKey(t *testing.T) {
	t.Setenv("TASKIEM_PUBLIC_DATABASE_URL", "postgres://pub@db/taskiem")
	t.Setenv("TASKIEM_PUBLIC_KEY", "")
	if c, err := loadPublicConfig(); err != nil || c.Public.Key != nil {
		t.Fatalf("unset: %v %v", err, c.Public.Key)
	}
	key := strings.Repeat("k", 32)
	t.Setenv("TASKIEM_PUBLIC_KEY", base64.StdEncoding.EncodeToString([]byte(key)))
	if c, err := loadPublicConfig(); err != nil || string(c.Public.Key) != key {
		t.Fatalf("set: %v %q", err, c.Public.Key)
	}
	for _, v := range []string{"not base64!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		t.Setenv("TASKIEM_PUBLIC_KEY", v)
		if _, err := loadPublicConfig(); err == nil {
			t.Errorf("key %q accepted", v)
		}
	}
}

func TestFormHost(t *testing.T) {
	t.Setenv("TASKIEM_PUBLIC_DATABASE_URL", "postgres://pub@db/taskiem")
	t.Setenv("TASKIEM_PUBLIC_URL", "https://app.taskiem.test/")
	t.Setenv("TASKIEM_PUBLIC_DOMAINS", "Shops.Example.com, other.example.com")
	t.Setenv("TASKIEM_PUBLIC_FORM_HOST", "")
	if c, err := loadPublicConfig(); err != nil || c.Public.FormHost != "forms.shops.example.com" || c.Public.AppURL != "https://app.taskiem.test" {
		t.Fatalf("default: %v %+v", err, c.Public)
	}
	t.Setenv("TASKIEM_PUBLIC_FORM_HOST", "Forms.Example.org")
	if c, err := loadPublicConfig(); err != nil || c.Public.FormHost != "forms.example.org" {
		t.Fatalf("set: %v %+v", err, c.Public)
	}
	t.Setenv("TASKIEM_PUBLIC_FORM_HOST", "")
	t.Setenv("TASKIEM_PUBLIC_DOMAINS", "")
	if c, err := loadPublicConfig(); err != nil || c.Public.FormHost != "" {
		t.Fatalf("none: %v %+v", err, c.Public)
	}
}

func TestDomainTarget(t *testing.T) {
	t.Setenv("TASKIEM_PUBLIC_DATABASE_URL", "postgres://pub@db/taskiem")
	t.Setenv("TASKIEM_PUBLIC_DOMAINS", "shops.example.com")
	t.Setenv("TASKIEM_PUBLIC_CNAME", "")
	t.Setenv("TASKIEM_PUBLIC_ADDRESSES", " 203.0.113.7, 2001:db8::7 ")
	c, err := loadPublicConfig()
	if err != nil || c.Public.CNAME != "pages.shops.example.com" || len(c.Public.Addresses) != 2 || c.Public.Addresses[1] != "2001:db8::7" {
		t.Fatalf("default: %v %+v", err, c.Public)
	}
	t.Setenv("TASKIEM_PUBLIC_CNAME", "Edge.Example.net.")
	if c, err := loadPublicConfig(); err != nil || c.Public.CNAME != "edge.example.net" {
		t.Fatalf("set: %v %+v", err, c.Public)
	}
	t.Setenv("TASKIEM_PUBLIC_ADDRESSES", "not-an-ip")
	if _, err := loadPublicConfig(); err == nil {
		t.Fatal("a bad address was accepted")
	}
}

func TestSameLogin(t *testing.T) {
	if !sameLogin("postgres://app:x@db:5432/taskiem", "postgres://app:y@db:5432/taskiem") {
		t.Error("same user, host and database")
	}
	if sameLogin("postgres://pub@db/taskiem", "postgres://app@db/taskiem") {
		t.Error("different users")
	}
}

func TestHealthcheckAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" {
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	if err := healthcheck([]string{strings.TrimPrefix(srv.URL, "http://")}); err != nil {
		t.Fatalf("a loopback address: %v", err)
	}
	for _, addr := range []string{"10.0.0.5:9090", "example.com:80", "169.254.169.254:80", "nonsense"} {
		if err := healthcheck([]string{addr}); err == nil {
			t.Errorf("%s accepted", addr)
		}
	}
}

// TestPublicReadinessPingsTheDatabase: the public host is not ready while
// its database is unreachable, so the proxy in front stops sending it
// pages it can only answer with errors.
func TestPublicReadinessPingsTheDatabase(t *testing.T) {
	d := dbtest.New(t)
	proxy := d.NewProxy(t)
	metrics := freeAddr(t)
	t.Setenv("TASKIEM_DATABASE_URL", "")
	t.Setenv("TASKIEM_PUBLIC_DATABASE_URL", proxy.DSN(d))
	t.Setenv("TASKIEM_PUBLIC_LISTEN", freeAddr(t))
	t.Setenv("TASKIEM_METRICS_LISTEN", metrics)
	cfg, err := loadPublicConfig()
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- servePublic(ctx, cfg, slog.New(slog.DiscardHandler)) }()
	t.Cleanup(func() { stop(); <-done })

	waitFor := func(want int) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			code, body, _ := get(t, "http://"+metrics+"/readyz")
			if code == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("readyz %d %q, want %d", code, body, want)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	waitFor(http.StatusOK)
	proxy.SetDown(true)
	waitFor(http.StatusServiceUnavailable)
	proxy.SetDown(false)
	waitFor(http.StatusOK)
}
