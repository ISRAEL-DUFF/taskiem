package api

import (
	"errors"
	"testing"

	"github.com/israel-duff/taskiem/connectors/slack"
	"github.com/israel-duff/taskiem/engine/connector"
)

// Taskiem's platform OAuth apps are for its own connectors (F1): a
// tenant's (x_) or a publisher's (p_) connector that names one is never
// handed it, so the app's secret never goes to the token URL that
// connector chose, nor into its connection's credentials.
func TestPlatformOAuthAppsAreForBuiltIns(t *testing.T) {
	s := &Server{PublicURL: "https://taskiem.example", OAuthClients: map[string]OAuthClient{
		"slack":              {ID: "slack_platform_id", Secret: "SLACK_PLATFORM_SECRET"},
		"quickbooks_sandbox": {ID: "qb_id", Secret: "QB_SECRET"},
	}}
	for _, id := range []string{"x_evil", "p_acme_evil"} {
		c := &connector.Connector{Manifest: &connector.Manifest{ID: id, Name: "Evil", Auth: connector.Auth{
			OAuth2:    &connector.OAuth2{Client: "slack", AuthorizeURL: "https://evil.example/authorize", TokenURL: "https://evil.example/token", PKCE: true},
			HostModes: []connector.HostMode{{Mode: "test", BaseURL: "https://evil.example", Client: "quickbooks_sandbox"}},
		}}}
		for _, mode := range []string{"", "test"} {
			prov, err := s.oauthFor(c, mode)
			if !errors.Is(err, errOAuthUnavailable) || prov.client.Secret != "" {
				t.Errorf("%s in mode %q was given a platform app: %+v %v", id, mode, prov.client, err)
			}
		}
		if ok := s.oauthLinkable(c); ok == nil || *ok {
			t.Errorf("%s is offered for linking", id)
		}
	}
	// The built-in keeps its app.
	if prov, err := s.oauthFor(slack.New(slack.Options{}), ""); err != nil || prov.client.Secret != "SLACK_PLATFORM_SECRET" {
		t.Errorf("built-in Slack: %+v %v", prov.client, err)
	}
}
