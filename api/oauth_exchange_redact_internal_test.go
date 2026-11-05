package api

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/connectors/meta"
	"github.com/israel-duff/taskiem/engine/egress"
)

// Meta exchanges the code by GET (token_method: get), so the platform
// app's client_secret is in the token URL's query. A transport failure
// must not repeat that URL: not in the error, the log, or the redirect
// the person is sent to (F11).
func TestTokenExchangeFailureHidesTheClientSecret(t *testing.T) {
	c := meta.Messenger(meta.Options{})
	if c.Manifest.Auth.OAuth2.TokenMethod != "get" {
		t.Fatal("Messenger no longer exchanges by GET; this test needs a connector that does")
	}
	release := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer hang.Close()
	defer close(release)
	var logs bytes.Buffer
	s := &Server{PublicURL: "https://taskiem.example", Logger: slog.New(slog.NewTextHandler(&logs, nil)),
		Egress: &egress.Guard{Blocked: func(netip.Addr) bool { return false }}} // the fake is on loopback
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	prov := oauthProvider{token: hang.URL + "/v26.0/oauth/access_token", client: OAuthClient{ID: "123456789", Secret: "META_APP_SECRET"},
		redirect: "https://taskiem.example/v1/connections/oauth/callback"}
	_, err := s.oauthExchange(ctx, uuid.New(), c, prov, "the-code", "the-verifier")
	if !errors.Is(err, errOAuthProvider) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a hung exchange: %v", err)
	}
	if !strings.Contains(err.Error(), "/v26.0/oauth/access_token") {
		t.Errorf("the error should still say which endpoint failed: %v", err)
	}
	rec := httptest.NewRecorder()
	s.oauthDone(rec, httptest.NewRequest(http.MethodGet, oauthCallbackPath, nil), "", err)
	for what, got := range map[string]string{"error": err.Error(), "redirect": rec.Header().Get("Location"), "log": logs.String()} {
		for _, secret := range []string{"META_APP_SECRET", "client_secret", "the-code", "the-verifier"} {
			if strings.Contains(got, secret) {
				t.Errorf("the %s repeats %q: %s", what, secret, got)
			}
		}
	}
}
