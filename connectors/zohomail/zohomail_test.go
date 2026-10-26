package zohomail

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/connector"
)

func TestRegisters(t *testing.T) {
	c := New(Options{})
	if err := connector.NewRegistry().Register(c); err != nil {
		t.Fatal(err)
	}
	m := c.Manifest
	if m.ClientName() != "zoho" || len(m.Auth.OAuth2.LinkedFields) == 0 || m.Triggers["new_mail"].Poll == nil {
		t.Errorf("manifest %+v", m.Auth.OAuth2)
	}
	creds := map[string]string{"mail_server": "https://mail.zoho.eu", "accounts_server": "https://accounts.zoho.eu"}
	if h := m.HostsFor(creds); !slices.Equal(h, []string{"mail.zoho.eu", "accounts.zoho.eu"}) {
		t.Errorf("hosts %v", h)
	}
	for in, want := range map[string]string{"https://accounts.zoho.eu": "https://mail.zoho.eu", "https://accounts.zoho.com.au": "https://mail.zoho.com.au", "https://accounts.zohocloud.ca": "https://mail.zohocloud.ca"} {
		if got, ok := mailServer(in); !ok || got != want {
			t.Errorf("mailServer(%s) = %s", in, got)
		}
	}
	if _, ok := mailServer("https://evil.example"); ok {
		t.Error("a server that is no accounts server")
	}
}

// Connecting picks the user's own Zoho mailbox, not an IMAP account it
// reads, and keeps the granted token.
func TestLinkPicksTheZohoMailbox(t *testing.T) {
	accounts := `{"status":{"code":200,"description":"success"},"data":[{"accountId":"2560636000000084002","accountDisplayName":"paula","type":"IMAP_ACCOUNT","enabled":true},{"accountId":"2560636000000008002","primaryEmailAddress":"orders@adacakes.ng","type":"ZOHO_ACCOUNT","enabled":true}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/accounts" || r.Header.Get("Authorization") != "Zoho-oauthtoken 1000.granted" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, accounts)
	}))
	defer srv.Close()
	c := New(Options{BaseURL: srv.URL})
	creds, err := c.Link(context.Background(), connector.LinkCall{Credentials: map[string]string{"accounts_server": "https://accounts.zoho.com", "refresh_token": "1000.r"}, Token: "1000.granted", HTTP: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if creds["account_id"] != "2560636000000008002" || creds["email_address"] != "orders@adacakes.ng" || creds["access_token"] != "1000.granted" || creds["access_expires_at"] == "" || creds["refresh_token"] != "1000.r" {
		t.Errorf("creds %v", creds)
	}
	accounts = `{"status":{"code":200,"description":"success"},"data":[{"accountId":"1","type":"IMAP_ACCOUNT"}]}`
	if _, err := c.Link(context.Background(), connector.LinkCall{Credentials: map[string]string{}, Token: "1000.granted", HTTP: srv.Client()}); !errors.Is(err, connector.ErrLinkRefused) || !strings.Contains(err.Error(), "no Zoho Mail mailbox") {
		t.Errorf("no Zoho mailbox: %v", err)
	}
}

func TestText(t *testing.T) {
	if got := text(`<div>Hello,<br>40 &amp; more</div><script>x()</script><p>Bye</p>`); got != "Hello,\n40 & more\nBye" {
		t.Errorf("text %q", got)
	}
}
