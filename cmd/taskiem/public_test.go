package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

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
	t.Cleanup(func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("public host: %v", err)
		}
	})

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

// Forms that sell L4, on the founder's direction of 25 November 2026:
// WhatsApp follow-ups are not switched by the deployment's environment.
// The public host reads only the template language
// (TASKIEM_WHATSAPP_TEMPLATE_LANGUAGE, "en"), none of the platform
// number's settings, and the old follow-up variables, even ones that
// would not have read, change nothing: a form names WhatsApp only on what
// a business's own number's account holds (taskiem_public_form_followup_whatsapp).
func TestPublicWAFollowup(t *testing.T) {
	t.Setenv("TASKIEM_DATABASE_URL", "")
	t.Setenv("TASKIEM_PUBLIC_DATABASE_URL", "postgres://pub@db/taskiem")
	t.Setenv("TASKIEM_WHATSAPP_PHONE_NUMBER_ID", "")
	t.Setenv("TASKIEM_WHATSAPP_TEMPLATE_LANGUAGE", "")
	c, err := loadPublicConfig()
	if err != nil {
		t.Fatal(err)
	}
	if c.Public.WALanguage != "en" || c.WhatsApp != nil {
		t.Fatalf("with nothing set: %q %+v", c.Public.WALanguage, c.WhatsApp)
	}
	// The removed variables, even unreadable ones, are not read.
	t.Setenv("TASKIEM_WA_FOLLOWUP_TEMPLATE", "Not A Name")
	t.Setenv("TASKIEM_WA_FOLLOWUP_TEMPLATE_CATEGORY", "authentication")
	t.Setenv("TASKIEM_WA_FOLLOWUP_TEMPLATE_LANGUAGE", "english")
	t.Setenv("TASKIEM_WHATSAPP_TEMPLATE_LANGUAGE", "en_US")
	if c, err = loadPublicConfig(); err != nil || c.Public.WALanguage != "en_US" || c.WhatsApp != nil {
		t.Fatalf("the template language alone: %+v %v", c.Public, err)
	}
}

// The api side of the same direction: the server starts with the old
// follow-up variables set to values that once stopped it, and follow-ups
// reach WhatsApp through nothing but a business's own number. With no
// WhatsApp at all there is no chat for them (every follow-up is a text or
// an email); with the platform's WhatsApp set and no own number, a
// business's follow-ups have no WhatsApp either.
func TestFollowupsNeedNoEnv(t *testing.T) {
	d := dbtest.New(t)
	key := make([]byte, 32)
	t.Setenv("TASKIEM_DATABASE_URL", d.DSN)
	t.Setenv("TASKIEM_LOCAL_KMS_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("TASKIEM_PUBLIC_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	t.Setenv("TASKIEM_ARCHIVE_DIR", t.TempDir())
	t.Setenv("TASKIEM_WA_FOLLOWUP_TEMPLATE", "Not A Name")
	t.Setenv("TASKIEM_WA_FOLLOWUP_TEMPLATE_CATEGORY", "authentication")
	t.Setenv("TASKIEM_WA_FOLLOWUP_TEMPLATE_LANGUAGE", "english")
	for _, k := range []string{"TASKIEM_WHATSAPP_PHONE_NUMBER_ID", "TASKIEM_WHATSAPP_ACCESS_TOKEN", "TASKIEM_WHATSAPP_APP_SECRET", "TASKIEM_WHATSAPP_VERIFY_TOKEN", "TASKIEM_WHATSAPP_TOKEN_KEY"} {
		t.Setenv(k, "")
	}
	engineFor := func() *engine {
		t.Helper()
		cfg, err := loadConfig()
		if err != nil {
			t.Fatalf("the server does not start: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		e, err := newEngine(ctx, cfg, slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatalf("the server does not start: %v", err)
		}
		t.Cleanup(func() { e.pool.Close(); e.connectors.Pool.Close() })
		if e.forms == nil {
			t.Fatal("no follow-ups at all")
		}
		return e
	}
	if e := engineFor(); e.forms.FollowupChat != nil {
		t.Fatal("a WhatsApp follow-up with no WhatsApp")
	}
	t.Setenv("TASKIEM_WHATSAPP_PHONE_NUMBER_ID", "106540352242922")
	t.Setenv("TASKIEM_WHATSAPP_ACCESS_TOKEN", "token")
	t.Setenv("TASKIEM_WHATSAPP_APP_SECRET", "secret")
	t.Setenv("TASKIEM_WHATSAPP_VERIFY_TOKEN", "verify")
	t.Setenv("TASKIEM_WHATSAPP_TOKEN_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)))
	e := engineFor()
	if e.forms.FollowupChat == nil {
		t.Fatal("no WhatsApp follow-ups for own numbers")
	}
	if c, err := e.forms.FollowupChat.Ready(context.Background(), uuid.New()); c != "" || err != nil {
		t.Fatalf("WhatsApp for a business with no own number: %q %v", c, err)
	}
}
