package gmail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/israel-duff/taskiem/connectors/internal/fixture"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

var refreshCreds = map[string]string{"client_id": "cid.apps.googleusercontent.com", "client_secret": "shh", "refresh_token": "1//rt"}

// tokenServer mints "ya29.test-token" and records each grant.
type tokenServer struct {
	*httptest.Server
	mints  atomic.Int32
	grants []url.Values
	status int
}

func newTokenServer(t *testing.T) *tokenServer {
	ts := &tokenServer{status: http.StatusOK}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		ts.grants = append(ts.grants, r.PostForm)
		ts.mints.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(ts.status)
		if ts.status != http.StatusOK {
			_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"Bad Request"}`)
			return
		}
		_, _ = io.WriteString(w, `{"access_token":"ya29.test-token","expires_in":3599,"token_type":"Bearer"}`)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func execute(t *testing.T, base, tokenURL string, hc *http.Client, action string, creds map[string]string, input map[string]any) (map[string]any, error) {
	t.Helper()
	c := New(Options{BaseURL: base, TokenURL: tokenURL})
	if creds == nil {
		creds = refreshCreds
	}
	r, err := c.Actions[action].Execute(context.Background(), connector.Request{Input: input, Credentials: creds, HTTP: hc, Attempt: 1})
	out, _ := r.Output.(map[string]any)
	return out, err
}

// call runs an action against recorded exchanges.
func call(t *testing.T, action string, input map[string]any, exchanges ...string) (map[string]any, error) {
	t.Helper()
	var exs []fixture.Exchange
	for _, n := range exchanges {
		exs = append(exs, fixture.Load(t, n))
	}
	srv := fixture.Serve(t, exs...)
	ts := newTokenServer(t)
	return execute(t, srv.URL, ts.URL, srv.Client(), action, nil, input)
}

// capture answers send/drafts with a recorded response and keeps the body.
func capture(t *testing.T, action string, input map[string]any) (map[string]any, map[string]any, error) {
	t.Helper()
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/drafts") {
			_, _ = io.WriteString(w, `{"id":"r-1","message":{"id":"m9","threadId":"t9"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"m9","threadId":"t9","labelIds":["SENT"]}`)
	}))
	defer srv.Close()
	ts := newTokenServer(t)
	out, err := execute(t, srv.URL, ts.URL, srv.Client(), action, nil, input)
	return out, body, err
}

// parsed decodes the raw field Gmail receives.
func parsed(t *testing.T, body map[string]any) *mail.Message {
	t.Helper()
	raw, err := base64.URLEncoding.DecodeString(body["raw"].(string))
	if err != nil {
		t.Fatalf("raw is not base64url: %v", err)
	}
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("not RFC 5322: %v\n%s", err, raw)
	}
	return m
}

func partText(t *testing.T, p *multipart.Part) string {
	t.Helper()
	b, _ := io.ReadAll(p)
	if p.Header.Get("Content-Transfer-Encoding") == "base64" {
		d, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(b), "\r\n", ""))
		if err != nil {
			t.Fatal(err)
		}
		return string(d)
	}
	return string(b)
}

func TestRegistersHosts(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	c, _ := reg.Get("gmail@1")
	if h := c.Manifest.Hosts(); len(h) != 2 || h[0] != "gmail.googleapis.com" || h[1] != "oauth2.googleapis.com" {
		t.Errorf("hosts %v", h)
	}
	// An overridden API keeps the token endpoint reachable.
	if h := New(Options{BaseURL: "https://gmail.proxy.example/gmail/v1"}).Manifest.Hosts(); len(h) != 2 || h[1] != "oauth2.googleapis.com" {
		t.Errorf("override hosts %v", h)
	}
	if h := New(Options{BaseURL: "https://gmail.proxy.example", TokenURL: "https://token.proxy.example/token"}).Manifest.Hosts(); len(h) != 2 || h[1] != "token.proxy.example" {
		t.Errorf("token override hosts %v", h)
	}
}

func TestProfileWithRefreshToken(t *testing.T) {
	srv := fixture.Serve(t, fixture.Load(t, "profile"), fixture.Load(t, "labels"))
	ts := newTokenServer(t)
	c := New(Options{BaseURL: srv.URL, TokenURL: ts.URL})
	req := connector.Request{Credentials: refreshCreds, HTTP: srv.Client()}
	r, err := c.Actions["get_profile"].Execute(context.Background(), req)
	if err != nil || r.Output.(map[string]any)["email_address"] != "ops@example.com" {
		t.Fatalf("%v %v", r.Output, err)
	}
	// The token is cached across actions of the connection.
	if _, err := c.Actions["list_labels"].Execute(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if ts.mints.Load() != 1 || ts.grants[0].Get("grant_type") != "refresh_token" || ts.grants[0].Get("refresh_token") != "1//rt" {
		t.Errorf("%d mints, %v", ts.mints.Load(), ts.grants)
	}
}

func TestProfileWithServiceAccount(t *testing.T) {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	keyJSON, _ := json.Marshal(map[string]string{"type": "service_account", "client_email": "robot@p.iam.gserviceaccount.com", "private_key_id": "k1",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))})
	srv := fixture.Serve(t, fixture.Load(t, "profile_subject_path"))
	ts := newTokenServer(t)
	creds := map[string]string{"service_account_json": string(keyJSON), "subject": "ops@example.com", "user_id": "ops@example.com"}
	out, err := execute(t, srv.URL, ts.URL, srv.Client(), "get_profile", creds, nil)
	if err != nil || out["email_address"] != "ops@example.com" {
		t.Fatalf("%v %v", out, err)
	}
	g := ts.grants[0]
	if g.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		t.Errorf("grant %v", g)
	}
	parts := strings.Split(g.Get("assertion"), ".")
	claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var cl map[string]any
	_ = json.Unmarshal(claims, &cl)
	if cl["sub"] != "ops@example.com" || cl["scope"] != "https://www.googleapis.com/auth/gmail.modify" || cl["iss"] != "robot@p.iam.gserviceaccount.com" {
		t.Errorf("claims %v", cl)
	}
}

func TestSendPlainMessage(t *testing.T) {
	out, body, err := capture(t, "send_message", map[string]any{
		"to": []any{"Ada Lovelace <ada@example.com>", "grace@example.com, Linus <linus@example.org>"}, "bcc": "audit@example.com",
		"subject": "Payroll for September – approved", "text": "Hello Ada,\nPayroll is approved.\n", "thread_id": "t1",
		"in_reply_to": "<inv42@example.com>", "references": "<a@x> <inv42@example.com>",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["id"] != "m9" || out["thread_id"] != "t9" {
		t.Errorf("output %v", out)
	}
	if body["threadId"] != "t1" {
		t.Errorf("threadId %v", body)
	}
	m := parsed(t, body)
	to, err := m.Header.AddressList("To")
	if err != nil || len(to) != 3 || to[0].Name != "Ada Lovelace" || to[2].Address != "linus@example.org" {
		t.Errorf("to %v %v", to, err)
	}
	if bcc, _ := m.Header.AddressList("Bcc"); len(bcc) != 1 || bcc[0].Address != "audit@example.com" {
		t.Errorf("bcc %v", bcc)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(m.Header.Get("Subject"))
	if err != nil || subject != "Payroll for September – approved" || !strings.HasPrefix(m.Header.Get("Subject"), "=?utf-8?") {
		t.Errorf("subject %q %q %v", m.Header.Get("Subject"), subject, err)
	}
	if m.Header.Get("In-Reply-To") != "<inv42@example.com>" || m.Header.Get("References") != "<a@x> <inv42@example.com>" {
		t.Errorf("threading headers %q %q", m.Header.Get("In-Reply-To"), m.Header.Get("References"))
	}
	if m.Header.Get("Content-Type") != `text/plain; charset=UTF-8` || m.Header.Get("Content-Transfer-Encoding") != "base64" || m.Header.Get("Mime-Version") != "1.0" {
		t.Errorf("content headers %v", m.Header)
	}
	b, _ := io.ReadAll(m.Body)
	text, _ := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(b), "\r\n", ""))
	if string(text) != "Hello Ada,\nPayroll is approved.\n" {
		t.Errorf("body %q", text)
	}
}

func TestSendWithAlternativesAndAttachment(t *testing.T) {
	pdf := []byte("%PDF-1.4 fake\x00\x01")
	_, body, err := capture(t, "send_message", map[string]any{
		"to": []any{"ada@example.com"}, "subject": "Invoice", "text": "See attached.", "html": "<p>See attached.</p>",
		"attachments": []any{
			map[string]any{"filename": "invoice 42 – Sept.pdf", "content_type": "application/pdf", "content_base64": base64.URLEncoding.EncodeToString(pdf)},
			map[string]any{"filename": "notes.txt", "content": "line one\n--boundary-looking line\n"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := parsed(t, body)
	mt, params, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/mixed" {
		t.Fatalf("content type %q", m.Header.Get("Content-Type"))
	}
	mr := multipart.NewReader(m.Body, params["boundary"])
	alt, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	amt, aparams, _ := mime.ParseMediaType(alt.Header.Get("Content-Type"))
	if amt != "multipart/alternative" {
		t.Fatalf("first part %q", amt)
	}
	ar := multipart.NewReader(alt, aparams["boundary"])
	for _, want := range [][2]string{{"text/plain", "See attached."}, {"text/html", "<p>See attached.</p>"}} {
		p, err := ar.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		if ct, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type")); ct != want[0] || partText(t, p) != want[1] {
			t.Errorf("alternative %s", ct)
		}
	}
	att, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if att.FileName() != "invoice 42 – Sept.pdf" || att.Header.Get("Content-Type") != "application/pdf" || partText(t, att) != string(pdf) {
		t.Errorf("attachment %v %q", att.Header, att.FileName())
	}
	notes, err := mr.NextPart()
	if err != nil || notes.FileName() != "notes.txt" || notes.Header.Get("Content-Type") != "application/octet-stream" || partText(t, notes) != "line one\n--boundary-looking line\n" {
		t.Errorf("notes %v %v", notes, err)
	}
	if _, err := mr.NextPart(); err != io.EOF {
		t.Errorf("extra parts: %v", err)
	}
}

// Header injection: no input may add a header or end the header block.
func TestHeaderInjectionIsRefused(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{"to": []any{"ada@example.com"}, "subject": "Hi", "text": "x"}
	}
	for name, mutate := range map[string]func(map[string]any){
		"subject CRLF":     func(m map[string]any) { m["subject"] = "Hi\r\nBcc: victim@example.com" },
		"subject LF":       func(m map[string]any) { m["subject"] = "Hi\nBcc: victim@example.com" },
		"subject CR":       func(m map[string]any) { m["subject"] = "Hi\rBcc: victim@example.com" },
		"to newline":       func(m map[string]any) { m["to"] = []any{"ada@example.com\r\nBcc: victim@example.com"} },
		"to name newline":  func(m map[string]any) { m["to"] = []any{"\"Ada\r\nBcc: v@example.com\" <ada@example.com>"} },
		"cc garbage":       func(m map[string]any) { m["cc"] = []any{"not an address"} },
		"from two":         func(m map[string]any) { m["from"] = "a@example.com, b@example.com" },
		"reply_to newline": func(m map[string]any) { m["reply_to"] = "a@example.com\nX-Evil: 1" },
		"in_reply_to":      func(m map[string]any) { m["in_reply_to"] = "<a@x>\r\nBcc: v@example.com" },
		"in_reply_to bare": func(m map[string]any) { m["in_reply_to"] = "a@x" },
		"references":       func(m map[string]any) { m["references"] = "<a@x> <b@y>\nX: 1" },
		"filename": func(m map[string]any) {
			m["attachments"] = []any{map[string]any{"filename": "a.txt\r\nContent-Type: text/html", "content": "x"}}
		},
		"content_type": func(m map[string]any) {
			m["attachments"] = []any{map[string]any{"filename": "a", "content_type": "text/plain\r\nX: 1", "content": "x"}}
		},
		"multipart ct": func(m map[string]any) {
			m["attachments"] = []any{map[string]any{"filename": "a", "content_type": "multipart/mixed; boundary=x", "content": "x"}}
		},
		"no recipients": func(m map[string]any) { delete(m, "to") },
		"bad base64": func(m map[string]any) {
			m["attachments"] = []any{map[string]any{"filename": "a", "content_base64": "@@@"}}
		},
	} {
		in := base()
		mutate(in)
		// No exchanges: a request reaching Gmail fails the test.
		_, err := call(t, "send_message", in)
		if effects.Classify(err) != effects.KindFatal {
			t.Errorf("%s: %v", name, err)
		}
	}

	// Names that need quoting or encoding are re-encoded, not passed raw.
	_, body, err := capture(t, "send_message", map[string]any{"to": []any{`"Doe, Jane (Ops)" <jane@example.com>`, "Zoë Ade <zoe@example.com>"}, "subject": "x", "text": "y"})
	if err != nil {
		t.Fatal(err)
	}
	m := parsed(t, body)
	to, err := m.Header.AddressList("To")
	if err != nil || len(to) != 2 || to[0].Name != "Doe, Jane (Ops)" || to[1].Name != "Zoë Ade" {
		t.Errorf("to %v %v (%q)", to, err, m.Header.Get("To"))
	}
	if len(m.Header) != 5 { // To, Subject, MIME-Version, Content-Type, Content-Transfer-Encoding
		t.Errorf("headers %v", m.Header)
	}
}

func TestSendErrors(t *testing.T) {
	in := map[string]any{"to": []any{"ada@example.com"}, "subject": "Hi", "text": "x"}
	for fx, kind := range map[string]effects.ErrorKind{
		"send_500":      effects.KindUnknownOutcome, // Gmail may have sent it: park
		"send_429":      effects.KindNotSent,        // refused: send again later
		"send_403_rate": effects.KindNotSent,
		"send_400":      effects.KindFatal,
	} {
		if _, err := call(t, "send_message", in, fx); effects.Classify(err) != kind {
			t.Errorf("%s: %v (%s)", fx, err, effects.Classify(err))
		}
	}
	// A refused token never reaches Gmail.
	ts := newTokenServer(t)
	ts.status = http.StatusBadRequest
	_, err := execute(t, "http://127.0.0.1:1", ts.URL, http.DefaultClient, "send_message", nil, in)
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("token refused: %v", err)
	}
	// No credentials at all.
	_, err = execute(t, "http://127.0.0.1:1", ts.URL, http.DefaultClient, "send_message", map[string]string{}, in)
	if effects.Classify(err) != effects.KindFatal {
		t.Errorf("no credentials: %v", err)
	}
}

func TestCreateDraft(t *testing.T) {
	out, body, err := capture(t, "create_draft", map[string]any{"to": []any{"ada@example.com"}, "subject": "Draft", "html": "<b>hi</b>", "thread_id": "t1"})
	if err != nil || out["draft_id"] != "r-1" || out["message_id"] != "m9" {
		t.Fatalf("%v %v", out, err)
	}
	msg, _ := body["message"].(map[string]any)
	if msg["threadId"] != "t1" {
		t.Errorf("draft body %v", body)
	}
	m := parsed(t, msg)
	if !strings.HasPrefix(m.Header.Get("Content-Type"), "text/html") {
		t.Errorf("content type %q", m.Header.Get("Content-Type"))
	}
	out, err = call(t, "create_draft", map[string]any{"to": []any{"ada@example.com"}, "text": "x"}, "draft_ok")
	if err != nil || out["draft_id"] != "r-5133318546283736587" {
		t.Errorf("fixture draft %v %v", out, err)
	}
}

func TestListAndGet(t *testing.T) {
	out, err := call(t, "list_messages", map[string]any{"q": "from:billing@example.com is:unread", "max_results": int64(10), "page_token": "p1", "label_ids": []any{"INBOX"}}, "list")
	if err != nil || len(out["messages"].([]any)) != 2 || out["next_page_token"] != "p2" || out["result_size_estimate"] != int64(2) {
		t.Fatalf("list %v %v", out, err)
	}
	out, err = call(t, "list_messages", nil, "list_empty")
	if err != nil || len(out["messages"].([]any)) != 0 || out["next_page_token"] != "" {
		t.Errorf("empty list %v %v", out, err)
	}

	out, err = call(t, "get_message", map[string]any{"id": "m1"}, "get_full")
	if err != nil {
		t.Fatal(err)
	}
	if out["text"] != "Invoice 42 is attached. – Ada\r\n" || out["html"] != "<p>Invoice 42 is attached. – Ada</p>" {
		t.Errorf("bodies %q %q", out["text"], out["html"])
	}
	if out["subject"] != "Invoice 42" || out["from"] != "Billing <billing@example.com>" || out["message_id"] != "<inv42@example.com>" || out["headers"].(map[string]any)["received"] != "first" {
		t.Errorf("headers %v", out)
	}
	atts := out["attachments"].([]any)
	if len(atts) != 1 || atts[0].(map[string]any)["attachment_id"] != "ANGjdJ8" || atts[0].(map[string]any)["filename"] != "invoice-42.pdf" {
		t.Errorf("attachments %v", atts)
	}
	if l := out["label_ids"].([]any); len(l) != 2 || l[1] != "UNREAD" {
		t.Errorf("labels %v", l)
	}

	out, err = call(t, "get_message", map[string]any{"id": "m1", "format": "metadata", "metadata_headers": []any{"Subject"}}, "get_metadata")
	if err != nil || out["subject"] != "Invoice 42" || out["text"] != "" {
		t.Errorf("metadata %v %v", out, err)
	}
	_, err = call(t, "get_message", map[string]any{"id": "nope"}, "get_404")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "NOT_FOUND") {
		t.Errorf("404: %v", err)
	}
	if _, err := call(t, "get_message", map[string]any{"id": "m1", "format": "raw"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("raw format: %v", err)
	}
}

func TestModifyLabels(t *testing.T) {
	out, err := call(t, "modify_labels", map[string]any{"id": "m1", "add_label_ids": []any{"Label_12"}, "remove_label_ids": []any{"UNREAD", "INBOX"}, "request_key": "k"}, "modify")
	if err != nil || out["label_ids"].([]any)[0] != "Label_12" {
		t.Fatalf("%v %v", out, err)
	}
	if _, err := call(t, "modify_labels", map[string]any{"id": "m1"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("nothing to change: %v", err)
	}
	out, err = call(t, "list_labels", nil, "labels")
	if err != nil || len(out["labels"].([]any)) != 2 {
		t.Errorf("labels %v %v", out, err)
	}
}
