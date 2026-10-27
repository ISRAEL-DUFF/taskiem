package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/connectors/slack"
	"github.com/israel-duff/taskiem/engine/egress"
)

// Add to Slack (K2): Slack's token answer nests the workspace (team.id),
// and refuses an exchange with 200 and {"ok": false}.
func TestSlackTokenAnswer(t *testing.T) {
	c := slack.New(slack.Options{})
	answer := map[string]any{"ok": true, "access_token": "xoxb-example-bot-token", "token_type": "bot",
		"scope": "chat:write,users:read", "bot_user_id": "U0KRQLJ9H", "app_id": "A0KRD7HC3",
		"team": map[string]any{"name": "Slack Pickleball Team", "id": "T9TK3CUKW"}, "authed_user": map[string]any{"id": "U1234"}}
	creds, _, err := oauthCredentials(c.Manifest, answer, "")
	if err != nil || creds["bot_token"] != "xoxb-example-bot-token" || creds["team_id"] != "T9TK3CUKW" || creds["team_name"] != "Slack Pickleball Team" {
		t.Fatalf("credentials %v %v", creds, err)
	}

	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok": false, "error": "invalid_code"}`))
	}))
	defer refusing.Close()
	s := &Server{Egress: &egress.Guard{Blocked: func(netip.Addr) bool { return false }}} // the fake is on loopback
	_, err = s.oauthExchange(context.Background(), uuid.New(), c, oauthProvider{token: refusing.URL, client: OAuthClient{ID: "id", Secret: "secret"}}, "code", "verifier")
	if !errors.Is(err, errOAuthProvider) || !strings.Contains(err.Error(), "invalid_code") {
		t.Errorf("a refused exchange: %v", err)
	}
	if got := strings.Join(c.Manifest.Auth.OAuth2.Scopes, c.Manifest.Auth.OAuth2.ScopeSeparator()); !strings.HasPrefix(got, "app_mentions:read,channels:history,") {
		t.Errorf("scopes on the consent URL: %s", got)
	}
}
