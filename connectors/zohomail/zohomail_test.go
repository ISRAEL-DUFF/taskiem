package zohomail

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

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

// A busy inbox (connector/v1 rule 22): 250 emails since the last poll
// are all answered, the oldest 200 first and the rest on the next poll,
// each once, oldest first, the poller giving back what it read (seen).
func TestNewMessagesBusyInbox(t *testing.T) {
	base := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)
	const n = 250
	// Zoho lists newest first: index 0 is the newest.
	at := func(i int) time.Time { return base.Add(time.Duration(i) * time.Second) }
	var listings int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/accounts/1/messages/view" || r.URL.Query().Get("sortorder") != "false" {
			http.NotFound(w, r)
			return
		}
		listings++
		start, _ := strconv.Atoi(r.URL.Query().Get("start"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		var rows []string
		// n new emails, then 30 older than the first poll's since.
		for k := start - 1; k < start-1+limit && k < n+30; k++ {
			i := n - 1 - k
			rows = append(rows, fmt.Sprintf(`{"messageId":"m%03d","folderId":"f","threadId":"m%03d","subject":"s","fromAddress":"a@example.com","toAddress":"b@example.com","receivedTime":"%d","status":"0","hasAttachment":"0"}`, i+30, i+30, at(i).UnixMilli()))
		}
		_, _ = io.WriteString(w, `{"status":{"code":200},"data":[`+strings.Join(rows, ",")+`]}`)
	}))
	defer srv.Close()
	c := New(Options{BaseURL: srv.URL})
	creds := map[string]string{"access_token": "1000.t", "access_expires_at": "2099-01-01T00:00:00Z", "account_id": "1", "refresh_token": "1000.r"}
	poll := func(since string, seen []any) map[string]any {
		t.Helper()
		resp, err := c.Actions["new_messages"].Execute(context.Background(), connector.Request{Input: map[string]any{"since": since, "seen": seen}, Credentials: creds, HTTP: srv.Client()})
		if err != nil {
			t.Fatal(err)
		}
		return resp.Output.(map[string]any)
	}
	ids := func(out map[string]any) []string {
		var got []string
		for _, m := range out["messages"].([]map[string]any) {
			got = append(got, m["message_id"].(string))
		}
		return got
	}
	first := poll(base.Format(time.RFC3339), nil)
	got := ids(first)
	if len(got) != maxNew || got[0] != "m030" || got[maxNew-1] != "m229" {
		t.Fatalf("first poll: %d, from %s to %s", len(got), got[0], got[len(got)-1])
	}
	if want := at(maxNew - 1).Format(time.RFC3339); first["until"] != want || len(first["keys"].([]any)) != maxNew {
		t.Errorf("first poll until %v (want %s), %d keys", first["until"], want, len(first["keys"].([]any)))
	}
	if listings != 2 {
		t.Errorf("the first poll listed %d pages, want 2 (back to since)", listings)
	}
	// The poller starts the next poll where the first got to, and gives
	// back what it read: the rest come, none twice.
	second := poll(first["until"].(string), first["keys"].([]any))
	rest := ids(second)
	if len(rest) != n-maxNew || rest[0] != "m230" || rest[len(rest)-1] != "m279" || second["until"] != "" {
		t.Fatalf("second poll: %d, %v ... until %v", len(rest), rest[:1], second["until"])
	}
	all := map[string]bool{}
	for _, id := range append(got, rest...) {
		if all[id] {
			t.Errorf("%s answered twice", id)
		}
		all[id] = true
	}
	if len(all) != n {
		t.Errorf("%d of %d answered", len(all), n)
	}
}
