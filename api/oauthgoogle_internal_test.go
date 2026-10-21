package api

import (
	"testing"

	"github.com/israel-duff/taskiem/connectors/gmail"
	"github.com/israel-duff/taskiem/connectors/googlesheets"
	"github.com/israel-duff/taskiem/engine/connector"
)

// Google (no-technical-steps N2): the grant keeps a refresh token and
// Taskiem's client, which the connector refreshes access tokens with, so
// the connection does not expire with Google's hour-long access token.
func TestGoogleOAuthCredentials(t *testing.T) {
	for name, conn := range map[string]*connector.Connector{"sheets": googlesheets.New(googlesheets.Options{}), "gmail": gmail.New(gmail.Options{})} {
		m := conn.Manifest
		answer := map[string]any{"access_token": "ya29.short", "refresh_token": "1//refresh", "expires_in": float64(3599), "scope": "x", "token_type": "Bearer"}
		creds, expires, err := oauthCredentials(m, answer, "")
		if err != nil {
			t.Fatal(err)
		}
		applyPlatformClient(m, creds, OAuthClient{ID: "app.apps.googleusercontent.com", Secret: "GOCSPX-secret"})
		if expires != nil {
			t.Errorf("%s: the connection expires with the access token: %v", name, expires)
		}
		if creds["refresh_token"] != "1//refresh" || creds["client_id"] != "app.apps.googleusercontent.com" || creds["client_secret"] != "GOCSPX-secret" || creds["access_token"] != "" {
			t.Errorf("%s: credentials %v", name, creds)
		}
		if err := checkCredentials(conn, creds); err != nil {
			t.Errorf("%s: the grant's fields are refused: %v", name, err)
		}
		// Revoking gives back the refresh token, which ends the grant.
		if oauthToken(conn, creds) != "1//refresh" {
			t.Errorf("%s: revokes %q", name, oauthToken(conn, creds))
		}
		o := m.Auth.OAuth2
		if o.AuthorizeParams["access_type"] != "offline" || o.AuthorizeParams["prompt"] != "consent" {
			t.Errorf("%s: no refresh token would be issued: %v", name, o.AuthorizeParams)
		}
	}
}
