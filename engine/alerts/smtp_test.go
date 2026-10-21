package alerts

import (
	"bufio"
	"context"
	"net"
	"net/url"
	"strings"
	"testing"
)

func TestAddress(t *testing.T) {
	for in, want := range map[string]string{
		"noreply@xenibase.ng":                     "noreply@xenibase.ng",
		"Taskiem <noreply@xenibase.ng>":           "noreply@xenibase.ng",
		` "Taskiem Staging" <noreply@ispend.ng> `: "noreply@ispend.ng",
		"not an address":                          "not an address",
	} {
		if got := Address(in); got != want {
			t.Errorf("Address(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSMTPEnvelope: a sender written with a display name goes to the
// server as its bare address in MAIL FROM (a 501 otherwise, as Resend
// answered), while the From header keeps the name.
func TestSMTPEnvelope(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	got := make(chan []string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		r, w := bufio.NewReader(conn), conn
		var seen []string
		say := func(s string) { _, _ = w.Write([]byte(s + "\r\n")) }
		say("220 fake ESMTP")
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				got <- seen
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if inData {
				if line == "." {
					inData = false
					say("250 queued")
				} else {
					seen = append(seen, "DATA "+line)
				}
				continue
			}
			seen = append(seen, line)
			switch cmd := strings.ToUpper(line); {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				say("250 fake")
			case strings.HasPrefix(cmd, "MAIL FROM:"):
				if strings.Count(line, "<") > 1 { // a display name: not an address
					say(`501 Error: Bad sender address syntax`)
					continue
				}
				say("250 ok")
			case strings.HasPrefix(cmd, "RCPT TO:"):
				say("250 ok")
			case cmd == "DATA":
				inData = true
				say("354 go ahead")
			case cmd == "QUIT":
				say("221 bye")
				got <- seen
				return
			default:
				say("250 ok")
			}
		}
	}()
	m := &SMTPMailer{URL: &url.URL{Scheme: "smtp", Host: ln.Addr().String()}}
	from := "Taskiem <noreply@xenibase.ng>"
	if err := m.Send(context.Background(), from, []string{"owner@acme.test"}, BuildEmail(from, []string{"owner@acme.test"}, "Hello", "Body", "id-1")); err != nil {
		t.Fatal(err)
	}
	lines := strings.Join(<-got, "\n")
	if !strings.Contains(lines, "MAIL FROM:<noreply@xenibase.ng>") || !strings.Contains(lines, "DATA From: Taskiem <noreply@xenibase.ng>") {
		t.Fatalf("the server saw:\n%s", lines)
	}
}
