package alerts

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

// BuildEmail formats a plain-text message (RFC 5322). Header values are
// stripped of line breaks so a title cannot add headers.
func BuildEmail(from string, to []string, subject, body, id string) []byte {
	clean := func(s string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) }
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", clean(from))
	fmt.Fprintf(&b, "To: %s\r\n", clean(strings.Join(to, ", ")))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", clean(subject)))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@taskiem>\r\n", clean(id))
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\nAuto-Submitted: auto-generated\r\n\r\n")
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n"))
	b.WriteString("\r\n")
	return []byte(b.String())
}

// SMTPMailer sends through the deployment's mail server:
// smtp://user:pass@host:587 (STARTTLS, required when there is a password)
// or smtps://user:pass@host:465 (TLS from the start).
type SMTPMailer struct {
	URL *url.URL
}

// NewSMTPMailer parses TASKIEM_SMTP_URL.
func NewSMTPMailer(raw string) (*SMTPMailer, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "smtp" && u.Scheme != "smtps") || u.Hostname() == "" {
		return nil, errors.New("TASKIEM_SMTP_URL must look like smtp://user:pass@host:587 or smtps://user:pass@host:465")
	}
	return &SMTPMailer{URL: u}, nil
}

// Send delivers one message.
func (m *SMTPMailer) Send(ctx context.Context, from string, to []string, msg []byte) error {
	host := m.URL.Hostname()
	port := m.URL.Port()
	if port == "" {
		port = map[string]string{"smtp": "587", "smtps": "465"}[m.URL.Scheme]
	}
	d := net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	var err error
	if m.URL.Scheme == "smtps" {
		conn, err = (&tls.Dialer{NetDialer: &d, Config: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	} else {
		conn, err = d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	}
	if err != nil {
		return err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(time.Minute))
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer func() { _ = c.Close() }()
	pass, hasPass := m.URL.User.Password()
	if m.URL.Scheme == "smtp" {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
				return err
			}
		} else if hasPass {
			return errors.New("the mail server does not offer STARTTLS: refusing to send the password in the clear")
		}
	}
	if hasPass {
		if err := c.Auth(smtp.PlainAuth("", m.URL.User.Username(), pass, host)); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
