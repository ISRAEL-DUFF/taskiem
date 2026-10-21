package inmail

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// crlf makes a test message's line endings what SMTP delivers.
func crlf(s string) []byte {
	return []byte(strings.ReplaceAll(strings.TrimLeft(s, "\n"), "\n", "\r\n"))
}

var pdf = base64.StdEncoding.EncodeToString([]byte("%PDF-1.4\n1 0 obj << >> endobj\ntrailer << >>\n%%EOF\n"))

// A bank alert forwarded by a Gmail filter: the original From is kept,
// Gmail names the forwarding mailbox, and a statement is attached.
var gmailForwarded = crlf(`
X-Forwarded-For: owner@gmail.com k3x9q2m7a1bz@in.taskiem.test
X-Forwarded-To: k3x9q2m7a1bz@in.taskiem.test
From: "First Bank Alerts" <alerts@firstbank.example>
To: Owner <owner@gmail.com>
Subject: =?UTF-8?B?Q3JlZGl0IEFsZXJ0OiDigqY1MCwwMDAuMDA=?=
Date: Tue, 20 Oct 2026 09:15:00 +0100
Message-ID: <alert-123@firstbank.example>
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="outer"

--outer
Content-Type: multipart/alternative; boundary="alt"

--alt
Content-Type: text/plain; charset=utf-8
Content-Transfer-Encoding: quoted-printable

Amount: =E2=82=A650,000.00
Narration: Payment from Ada Stores
--alt
Content-Type: text/html; charset=utf-8

<html><body><p>Amount: &#8358;50,000.00</p></body></html>
--alt--
--outer
Content-Type: application/pdf; name="statement.pdf"
Content-Disposition: attachment; filename="statement.pdf"
Content-Transfer-Encoding: base64

` + pdf + `
--outer--
`)

func TestParseForwardedAlert(t *testing.T) {
	m, err := Parse(gmailForwarded)
	if err != nil {
		t.Fatal(err)
	}
	if m.From != (Address{Name: "First Bank Alerts", Address: "alerts@firstbank.example"}) {
		t.Errorf("from = %+v", m.From)
	}
	if m.Subject != "Credit Alert: ₦50,000.00" {
		t.Errorf("subject = %q", m.Subject)
	}
	if m.MessageID != "alert-123@firstbank.example" {
		t.Errorf("message id = %q", m.MessageID)
	}
	if !strings.Contains(m.Text, "Amount: ₦50,000.00") || !strings.Contains(m.Text, "Ada Stores") {
		t.Errorf("text = %q", m.Text)
	}
	if !strings.Contains(m.HTML, "<p>Amount") {
		t.Errorf("html = %q", m.HTML)
	}
	if len(m.Attachments) != 1 || m.Attachments[0].Name != "statement.pdf" || m.Attachments[0].Type != "application/pdf" ||
		!strings.HasPrefix(string(m.Attachments[0].Data), "%PDF") {
		t.Errorf("attachments = %+v", m.Attachments)
	}
	if want := time.Date(2026, 10, 20, 8, 15, 0, 0, time.UTC); !m.Date.Equal(want) {
		t.Errorf("date = %v", m.Date)
	}
	if by := ForwardedBy(m); by != "owner@gmail.com" {
		t.Errorf("forwarded by = %q", by)
	}
	b := Body(m, nil, nil)
	if b["subject"] != m.Subject || b["forwarded_by"] != "owner@gmail.com" || b["truncated"] != nil {
		t.Errorf("body = %v", b)
	}
}

func TestParseHTMLOnlyLatin1(t *testing.T) {
	m, err := Parse(crlf(`
From: =?ISO-8859-1?Q?Caf=E9_Ng?= <hello@cafe.example>
To: x@in.taskiem.test
Subject: Order
Content-Type: text/html; charset=iso-8859-1
Content-Transfer-Encoding: quoted-printable

<html><head><style>p{color:red}</style><title>t</title></head><body>
<h1>New order</h1><p>Caf=E9 latte x2</p><ul><li>Pick-up: 5pm</li><li>Paid</li></ul>
<script>alert(1)</script></body></html>
`))
	if err != nil {
		t.Fatal(err)
	}
	if m.From.Name != "Café Ng" {
		t.Errorf("name = %q", m.From.Name)
	}
	want := "New order\nCafé latte x2\n- Pick-up: 5pm\n- Paid"
	if m.Text != want {
		t.Errorf("text = %q, want %q", m.Text, want)
	}
}

func TestLatest(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"gmail":           {"Yes, deliver on Friday.\n\nOn Mon, 19 Oct 2026 at 10:02, Ada <ada@x.example> wrote:\n> Can you deliver?\n", "Yes, deliver on Friday."},
		"wrapped":         {"Fine.\n\nOn Mon, 19 Oct 2026 at 10:02, Ada Obi\n<ada@x.example> wrote:\n> hi", "Fine."},
		"outlook":         {"Approved.\n\n________________________________\nFrom: Ada\nSent: Monday\n", "Approved."},
		"outlook headers": {"Approved.\n\nFrom: Ada <ada@x.example>\nSent: Monday, 19 October 2026\nTo: Bayo", "Approved."},
		"original":        {"See below\n-----Original Message-----\nFrom: x", "See below"},
		"quoted only":     {"Thanks\n> earlier\n> more\n", "Thanks"},
		"nothing quoted":  {"Just a message.\nTwo lines.", "Just a message.\nTwo lines."},
		"all quoted":      {"> only quote", "> only quote"},
	}
	for name, c := range cases {
		if got := Latest(c.in); got != c.want {
			t.Errorf("%s: Latest = %q, want %q", name, got, c.want)
		}
	}
}

func TestParseMalformed(t *testing.T) {
	if _, err := Parse([]byte("not a message at all")); err == nil {
		t.Error("want an error for a message without a header")
	}
	// A multipart that never ends keeps what was read.
	m, err := Parse(crlf(`
From: a@b.example
Content-Type: multipart/mixed; boundary="b"

--b
Content-Type: text/plain

hello
`))
	if err != nil || m.Text != "hello" {
		t.Errorf("truncated multipart: %v, %q", err, m.Text)
	}
	// Too deep is refused.
	inner := "Content-Type: text/plain\n\ndeep\n"
	for i := 12; i >= 0; i-- {
		b := "x" + itoa(i)
		inner = "Content-Type: multipart/mixed; boundary=\"" + b + "\"\n\n--" + b + "\n" + inner + "--" + b + "--\n"
	}
	if _, err := Parse(crlf("From: a@b.example\n" + inner)); err == nil {
		t.Error("want an error for a message nested too deep")
	}
}

func itoa(i int) string { return string(rune('a'+i)) + "" }

func TestAutomatic(t *testing.T) {
	m := func(h string) *Message {
		msg, err := Parse(crlf(h + "\nFrom: a@b.example\nSubject: s\n\nbody\n"))
		if err != nil {
			t.Fatal(err)
		}
		return msg
	}
	if why := Automatic(m("Auto-Submitted: auto-replied"), "a@b.example", nil); why == "" {
		t.Error("an auto-reply starts a run")
	}
	if why := Automatic(m("Precedence: auto_reply"), "a@b.example", nil); why == "" {
		t.Error("a Precedence auto_reply starts a run")
	}
	if why := Automatic(m("X-Taskiem-Run: r1"), "a@b.example", nil); why == "" {
		t.Error("Taskiem's own workflow mail starts a run")
	}
	if why := Automatic(m("X-Other: 1"), "", nil); why == "" {
		t.Error("a bounce (null sender) starts a run")
	}
	if why := Automatic(m("X-Other: 1"), "a@b.example", []string{"A@B.example"}); why == "" {
		t.Error("mail from Taskiem's own sender starts a run")
	}
	if why := Automatic(m("Auto-Submitted: no"), "a@b.example", nil); why != "" {
		t.Errorf("a person's mail is refused: %s", why)
	}
}

func TestSenderAllowed(t *testing.T) {
	proven := Auth{Aligned: true}
	if !SenderAllowed(nil, "x@y.example", Auth{}) {
		t.Error("an empty list refuses")
	}
	for _, c := range []struct {
		list []string
		from string
		want bool
	}{
		{[]string{"alerts@firstbank.example"}, "Alerts@FirstBank.example", true},
		{[]string{"firstbank.example"}, "alerts@mail.firstbank.example", true},
		{[]string{"@firstbank.example"}, "alerts@firstbank.example", true},
		{[]string{"firstbank.example"}, "alerts@notfirstbank.example", false},
		{[]string{"alerts@firstbank.example"}, "other@firstbank.example", false},
	} {
		if got := SenderAllowed(c.list, c.from, proven); got != c.want {
			t.Errorf("SenderAllowed(%v, %s) = %v", c.list, c.from, got)
		}
	}
	if SenderAllowed([]string{"firstbank.example"}, "alerts@firstbank.example", Auth{}) {
		t.Error("an unproven From passes the list")
	}
}

func TestAligned(t *testing.T) {
	for _, c := range []struct {
		vouching, from string
		want           bool
	}{
		{"firstbank.example", "firstbank.example", true},
		{"firstbank.example", "mail.firstbank.example", true},
		{"mail.firstbank.example", "firstbank.example", true},
		{"evil.example", "firstbank.example", false},
		{"example", "firstbank.example", false},
		{"", "firstbank.example", false},
	} {
		if got := Aligned(c.vouching, c.from); got != c.want {
			t.Errorf("Aligned(%q, %q) = %v", c.vouching, c.from, got)
		}
	}
}

var gmailConfirmation = crlf(`
From: Gmail Team <forwarding-noreply@google.com>
To: k3x9q2m7a1bz@in.taskiem.test
Subject: (#123456789) Gmail Forwarding Confirmation - Receive Mail from owner@gmail.com
Content-Type: text/plain; charset=utf-8

owner@gmail.com has requested to automatically forward mail to your email
address k3x9q2m7a1bz@in.taskiem.test.
Confirmation code: 123456789

To allow owner@gmail.com to automatically forward mail to your address,
please click the link below to confirm the request:

https://mail-settings.google.example/mail/vf-abc123-confirm
`)

func TestForwardingConfirmation(t *testing.T) {
	m, err := Parse(gmailConfirmation)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	c, ok := ForwardingConfirmation(m, Auth{Aligned: true, DKIM: []string{"google.com"}}, at)
	if !ok || c.Provider != "Gmail" || c.Code != "123456789" || c.For != "owner@gmail.com" ||
		c.Link != "https://mail-settings.google.example/mail/vf-abc123-confirm" {
		t.Errorf("confirmation = %+v, %v", c, ok)
	}
	if _, ok := ForwardingConfirmation(m, Auth{}, at); ok {
		t.Error("an unproven Gmail confirmation counts")
	}
	other, _ := Parse(crlf("From: a@b.example\nSubject: Forwarding confirmation code 123456\n\nConfirmation code: 123456\n"))
	if _, ok := ForwardingConfirmation(other, Auth{Aligned: true}, at); ok {
		t.Error("a confirmation from an unknown provider counts")
	}
}

func TestBodyClips(t *testing.T) {
	m := &Message{Text: strings.Repeat("ab", maxText), HTML: "<p>x</p>", From: Address{Address: "a@b.example"}}
	b := Body(m, nil, nil)
	if len(b["text"].(string)) != maxText || b["truncated"] != true {
		t.Errorf("text not clipped: %d", len(b["text"].(string)))
	}
	if s, cut := clip("ñññ", 3); s != "ñ" || !cut {
		t.Errorf("clip = %q", s)
	}
}
