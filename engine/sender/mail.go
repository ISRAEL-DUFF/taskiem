package sender

import (
	"fmt"
	"mime"
	"net/mail"
	"strings"
	"time"
)

// buildEmail renders a plain-text email from the platform's no-reply
// address. The display name says whose message it is and that the platform
// carried it ("Ada's Kitchen via Taskiem"), so a business cannot pass its
// email off as anyone else's, and Reply-To takes answers to the business.
func buildEmail(from, senderName, to, replyTo, subject, text, id string, now time.Time) []byte {
	clean := func(s string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) }
	// The platform's sender may be written with a name of its own; the
	// business's name replaces it.
	bare := from
	if a, err := mail.ParseAddress(strings.TrimSpace(from)); err == nil {
		bare = a.Address
	}
	addr := mail.Address{Name: CleanName(senderName) + " via Taskiem", Address: bare}
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", addr.String())
	fmt.Fprintf(&b, "To: %s\r\n", clean(to))
	if replyTo != "" {
		fmt.Fprintf(&b, "Reply-To: %s\r\n", clean(replyTo))
	}
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", clean(subject)))
	fmt.Fprintf(&b, "Date: %s\r\n", now.UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@taskiem>\r\n", clean(id))
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\nAuto-Submitted: auto-generated\r\n\r\n")
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", "\r\n"))
	b.WriteString("\r\n")
	return []byte(b.String())
}

// ProbeEmail is the message `taskiem messages test --email` sends.
func ProbeEmail(from, to, text string, now time.Time) []byte {
	return buildEmail(from, "Taskiem test", to, "", "Taskiem test message", text, "test-"+now.UTC().Format("20060102150405"), now)
}
