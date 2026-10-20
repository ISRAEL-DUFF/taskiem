package sender

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/effects"
)

func TestNormalisePhone(t *testing.T) {
	ng := []string{"234"}
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"+2348031234567", "+2348031234567", true},
		{"2348031234567", "+2348031234567", true},
		{"08031234567", "+2348031234567", true},
		{"0803 123 4567", "+2348031234567", true},
		{"(0803)-123-4567", "+2348031234567", true},
		{"002348031234567", "+2348031234567", true},
		{"09012345678", "+2349012345678", true},
		{"07012345678", "+2347012345678", true},
		{"+2341234567890", "", false}, // a landline-shaped number
		{"+23480312345", "", false},   // too short
		{"+447700900123", "", false},  // not an allowed country
		{"+881612345678", "", false},  // satellite: premium routes
		{"8031234567", "", false},
		{"0803123456a", "", false},
		{"", "", false},
	} {
		got, err := NormalisePhone(c.in, ng)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("NormalisePhone(%q) = %q, %v", c.in, got, err)
		}
		if err != nil && !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v is not ErrInvalid", c.in, err)
		}
	}
	if got, err := NormalisePhone("+447700900123", []string{"234", "44"}); err != nil || got != "+447700900123" {
		t.Errorf("allowed country: %q %v", got, err)
	}
}

func TestNormaliseEmail(t *testing.T) {
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"Ada@Example.com", "ada@example.com", true},
		{"  ada@example.com ", "ada@example.com", true},
		{"Ada <ada@example.com>", "", false},
		{"ada@localhost", "", false},
		{"ada@example.com\r\nBcc: x@y.z", "", false},
		{"not an email", "", false},
	} {
		got, err := NormaliseEmail(c.in)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("NormaliseEmail(%q) = %q, %v", c.in, got, err)
		}
	}
}

func TestCleanName(t *testing.T) {
	for in, want := range map[string]string{
		"Ada's Kitchen":            "Ada's Kitchen",
		"  Ada \n Kitchen\r":       "Ada Kitchen",
		`"Bank" <admin@x.com>`:     "Bank admin@x.com",
		"Ada\u202eKitchen":         "AdaKitchen",
		strings.Repeat("a", 70):    strings.Repeat("a", 60),
		"Mama Put ₦ Best in Lekki": "Mama Put ₦ Best in Lekki",
	} {
		if got := CleanName(in); got != want {
			t.Errorf("CleanName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSegments(t *testing.T) {
	for _, c := range []struct {
		text string
		want int
	}{
		{"", 1},
		{strings.Repeat("a", 160), 1},
		{strings.Repeat("a", 161), 2},
		{strings.Repeat("a", 306), 2},
		{strings.Repeat("a", 307), 3},
		{strings.Repeat("€", 80), 1}, // two septets each
		{strings.Repeat("€", 81), 2}, // 162 septets
		{"Total: ₦5,000", 1},         // the naira sign is not GSM-7
		{strings.Repeat("₦", 70), 1},
		{strings.Repeat("₦", 71), 2},
		{strings.Repeat("😀", 35), 1}, // two UTF-16 units each
		{strings.Repeat("😀", 36), 2},
	} {
		if got := Segments(c.text); got != c.want {
			t.Errorf("Segments(%d runes of %q) = %d, want %d", len([]rune(c.text)), []rune(c.text)[0:min(1, len([]rune(c.text)))], got, c.want)
		}
	}
}

func TestCheck(t *testing.T) {
	q := &Queue{}
	base := func() Message {
		return Message{Tenant: uuid.New(), Channel: SMS, Purpose: PurposeConfirmation, To: "08031234567", SenderName: "Ada's Kitchen", Text: "Order AK-1 confirmed.", Key: "k"}
	}
	m := base()
	to, cat, err := q.check(&m)
	if err != nil || to != "+2348031234567" || cat != "phone" || m.Text != "Ada's Kitchen: Order AK-1 confirmed." {
		t.Fatalf("%q %q %v %q", to, cat, err, m.Text)
	}
	m = base()
	m.Text = "Ada's Kitchen here: hi"
	if _, _, err := q.check(&m); err != nil || m.Text != "Ada's Kitchen here: hi" {
		t.Fatalf("a text already naming the business is not prefixed again: %q %v", m.Text, err)
	}
	m = base()
	m.Purpose = PurposeCode
	if _, _, err := q.check(&m); err != nil || m.ExpiresAt.IsZero() || time.Until(m.ExpiresAt) > CodeTTL {
		t.Fatalf("a code expires: %v %v", m.ExpiresAt, err)
	}
	for name, mut := range map[string]func(*Message){
		"no key":         func(m *Message) { m.Key = "" },
		"no tenant":      func(m *Message) { m.Tenant = uuid.Nil },
		"no sender":      func(m *Message) { m.SenderName = " \n" },
		"no text":        func(m *Message) { m.Text = "  " },
		"purpose":        func(m *Message) { m.Purpose = "marketing" },
		"channel":        func(m *Message) { m.Channel = "fax" },
		"too long":       func(m *Message) { m.Text = strings.Repeat("a", 460) },
		"sms subject":    func(m *Message) { m.Subject = "Hi" },
		"foreign number": func(m *Message) { m.To = "+447700900123" },
		"email no subj":  func(m *Message) { m.Channel, m.To = Email, "a@b.co" },
		"email reply-to": func(m *Message) { m.Channel, m.To, m.Subject, m.ReplyTo = Email, "a@b.co", "Hi", "nope" },
	} {
		m := base()
		mut(&m)
		if _, _, err := q.check(&m); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	m = base()
	m.Channel, m.To, m.Subject, m.ReplyTo = Email, "Customer@Example.com", "Your order\r\nBcc: x@y.z", "Owner@Ada.ng"
	to, cat, err = q.check(&m)
	if err != nil || to != "customer@example.com" || cat != "email" || m.ReplyTo != "owner@ada.ng" || strings.ContainsAny(m.Subject, "\r\n") {
		t.Fatalf("email: %q %q %q %q %v", to, cat, m.ReplyTo, m.Subject, err)
	}
}

func TestBuildEmail(t *testing.T) {
	raw := string(buildEmail("no-reply@taskiem.test", `Ada "Kitchen"`, "c@example.com", "owner@ada.ng", "Order\r\nBcc: evil@x.com", "Hello\nthere", "id1", time.Unix(0, 0)))
	head, body, _ := strings.Cut(raw, "\r\n\r\n")
	for _, line := range strings.Split(head, "\r\n") {
		if strings.HasPrefix(line, "Bcc:") {
			t.Fatalf("a header was injected: %q", raw)
		}
	}
	if !strings.Contains(head, "Reply-To: owner@ada.ng") ||
		!strings.Contains(head, `From: "Ada Kitchen via Taskiem" <no-reply@taskiem.test>`) || body != "Hello\r\nthere\r\n" {
		t.Fatalf("%q", raw)
	}
	// The platform's sender written with a name of its own: the business's
	// name replaces it, around the bare address.
	raw = string(buildEmail("Taskiem <no-reply@taskiem.test>", "Ada", "c@example.com", "", "Order", "Hi", "id2", time.Unix(0, 0)))
	if !strings.Contains(raw, "From: \"Ada via Taskiem\" <no-reply@taskiem.test>\r\n") {
		t.Fatalf("named sender: %q", raw)
	}
}

func TestTermii(t *testing.T) {
	var got map[string]string
	answer := func(status int, body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/sms/send" || r.Method != "POST" {
				t.Errorf("%s %s", r.Method, r.URL.Path)
			}
			_ = json.NewDecoder(r.Body).Decode(&got)
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
		}))
	}
	for _, c := range []struct {
		status int
		body   string
		want   error
	}{
		{200, `{"code":"ok","message_id":"9122","message":"Successfully Sent"}`, nil},
		{429, `{}`, ErrNotSent},
		{401, `{"message":"Invalid API key"}`, ErrProvider},
		{500, `{}`, ErrProvider},
		{400, `{"message":"Insufficient balance"}`, ErrProvider},
		{400, `{"message":"Invalid phone number"}`, ErrRecipient},
		{200, `{"message":"odd"}`, nil}, // no message id: ambiguous
	} {
		srv := answer(c.status, c.body)
		p := &Termii{BaseURL: srv.URL, APIKey: "tk", SenderID: "Taskiem", HTTP: srv.Client()}
		ref, err := p.Send(context.Background(), "+2348031234567", "Hi")
		srv.Close()
		switch {
		case c.want == nil && c.status == 200 && strings.Contains(c.body, "message_id"):
			if err != nil || ref != "9122" {
				t.Errorf("%d: %q %v", c.status, ref, err)
			}
		case c.want == nil:
			if err == nil || errors.Is(err, ErrNotSent) || errors.Is(err, ErrProvider) || errors.Is(err, ErrRecipient) {
				t.Errorf("%d %s must be ambiguous: %v", c.status, c.body, err)
			}
		case !errors.Is(err, c.want):
			t.Errorf("%d %s: %v, want %v", c.status, c.body, err, c.want)
		}
	}
	if got["to"] != "2348031234567" || got["channel"] != "dnd" || got["from"] != "Taskiem" || got["api_key"] != "tk" || got["type"] != "plain" {
		t.Errorf("request %v", got)
	}
}

func TestAfricasTalking(t *testing.T) {
	var form url.Values
	for _, c := range []struct {
		code int
		want error
	}{{101, nil}, {102, nil}, {403, ErrRecipient}, {409, ErrRecipient}, {405, ErrProvider}, {402, ErrProvider}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/version1/messaging" || r.Header.Get("apiKey") != "atk" {
				t.Errorf("%s %v", r.URL.Path, r.Header)
			}
			_ = r.ParseForm()
			form = r.PostForm
			fmt.Fprintf(w, `{"SMSMessageData":{"Message":"Sent","Recipients":[{"statusCode":%d,"number":"+2348031234567","status":"S","messageId":"ATXid_1"}]}}`, c.code)
		}))
		p := &AfricasTalking{BaseURL: srv.URL, Username: "taskiem", APIKey: "atk", SenderID: "Taskiem", HTTP: srv.Client()}
		ref, err := p.Send(context.Background(), "+2348031234567", "Hi")
		srv.Close()
		if c.want == nil && (err != nil || ref != "ATXid_1") || c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("status %d: %q %v", c.code, ref, err)
		}
	}
	if form.Get("username") != "taskiem" || form.Get("to") != "+2348031234567" || form.Get("from") != "Taskiem" || form.Get("message") != "Hi" {
		t.Errorf("form %v", form)
	}
}

type failTransport struct{ err error }

func (f failTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, f.err }

// A connection the egress guard never made is ErrNotSent; a failure after
// the request went out is ambiguous.
func TestTransportFailures(t *testing.T) {
	notSent := &http.Client{Transport: failTransport{fmt.Errorf("dial: %w", effects.ErrNotSent)}}
	p := &Termii{BaseURL: "https://api.ng.termii.com", HTTP: notSent}
	if _, err := p.Send(context.Background(), "+2348031234567", "Hi"); !errors.Is(err, ErrNotSent) {
		t.Fatalf("dial failure: %v", err)
	}
	later := &http.Client{Transport: failTransport{errors.New("read: connection reset")}}
	p.HTTP = later
	if _, err := p.Send(context.Background(), "+2348031234567", "Hi"); err == nil || errors.Is(err, ErrNotSent) || errors.Is(err, ErrProvider) {
		t.Fatalf("a failure after sending must be ambiguous: %v", err)
	}
}
