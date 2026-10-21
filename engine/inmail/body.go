package inmail

import (
	"regexp"
	"strings"
	"time"
)

// Auth is what the receiving server found of a message's origin.
type Auth struct {
	// SPF is the envelope sender's check: pass, fail, softfail, neutral,
	// none, temperror or permerror.
	SPF string `json:"spf"`
	// DKIM lists the domains whose signatures verified.
	DKIM []string `json:"dkim"`
	// DMARC is the From domain's published policy (none, quarantine,
	// reject), empty when it has none.
	DMARC string `json:"dmarc,omitempty"`
	// Aligned: a verified DKIM signature, or a passing SPF check, belongs
	// to the From address's domain, so the From is not forged.
	Aligned bool `json:"aligned"`
}

// Meta is what the mail role knew of a message besides its content,
// sealed in the inbox with it.
type Meta struct {
	Reference  string    `json:"reference"`
	Address    string    `json:"address"`
	MailFrom   string    `json:"mail_from"`
	RemoteIP   string    `json:"remote_ip"`
	Helo       string    `json:"helo"`
	ReceivedAt time.Time `json:"received_at"`
	Auth       Auth      `json:"auth"`
}

// Automatic reports why a message must not start a run because a machine
// sent it in answer to mail (an out-of-office reply, a bounce, a list's
// notice), or "" when a person or a service sent it. Loops between a
// workflow that emails and an address that runs it end here.
func Automatic(m *Message, mailFrom string, ownSenders []string) string {
	h := m.Header
	if v := strings.ToLower(strings.TrimSpace(h.Get("Auto-Submitted"))); v != "" && v != "no" {
		return "an automatic reply (Auto-Submitted: " + v + ")"
	}
	switch strings.ToLower(strings.TrimSpace(h.Get("Precedence"))) {
	case "auto_reply", "junk":
		return "an automatic reply"
	}
	if h.Get("X-Autoreply") != "" || h.Get("X-Autorespond") != "" {
		return "an automatic reply"
	}
	if mailFrom == "" || strings.EqualFold(mailFrom, "<>") {
		return "a delivery report (no return address)"
	}
	if h.Get("X-Taskiem-Run") != "" {
		return "mail sent by a Taskiem workflow"
	}
	for _, own := range ownSenders {
		if own != "" && strings.EqualFold(m.From.Address, own) {
			return "mail sent by Taskiem"
		}
	}
	return ""
}

// ForwardedBy is the mailbox that forwarded a message here by a filter or
// rule, when the forwarding provider says so (Gmail's X-Forwarded-For
// names it first); "" otherwise.
func ForwardedBy(m *Message) string {
	f := strings.Fields(m.Header.Get("X-Forwarded-For"))
	if len(f) > 0 && strings.Contains(f[0], "@") {
		return strings.ToLower(strings.Trim(f[0], "<>,"))
	}
	return ""
}

// Confirmation is a mail provider's message asking to confirm this
// address as a forwarding destination.
type Confirmation struct {
	Provider   string    `json:"provider"`
	Code       string    `json:"code,omitempty"`
	Link       string    `json:"link,omitempty"`
	For        string    `json:"for,omitempty"` // the mailbox that asked to forward here
	ReceivedAt time.Time `json:"received_at"`
}

var (
	confirmCode = regexp.MustCompile(`(?i)(?:confirmation|verification)\s+code\s*[:#]?\s*([0-9]{6,12})`)
	subjectCode = regexp.MustCompile(`\(#([0-9]{6,12})\)`)
	confirmLink = regexp.MustCompile(`https://[^\s"'<>)]+`)
	receiveFrom = regexp.MustCompile(`(?i)receive\s+mail\s+from\s+([^\s]+@[^\s]+)`)
)

// confirmers are the providers whose forwarding confirmations are
// recognised, by the domain their confirmation mail is signed with.
var confirmers = map[string]string{
	"google.com":    "Gmail",
	"zoho.com":      "Zoho Mail",
	"yahoo.com":     "Yahoo Mail",
	"microsoft.com": "Outlook",
	"outlook.com":   "Outlook",
}

// ForwardingConfirmation reports whether a message is a provider's
// forwarding confirmation. Only one whose From is proven (DKIM aligned to
// the provider's domain) counts: anyone can claim to be Gmail.
func ForwardingConfirmation(m *Message, auth Auth, at time.Time) (Confirmation, bool) {
	provider := ""
	from := Domain(m.From.Address)
	for d, name := range confirmers {
		if from == d || strings.HasSuffix(from, "."+d) {
			provider = name
		}
	}
	if provider == "" || !auth.Aligned {
		return Confirmation{}, false
	}
	subj := strings.ToLower(m.Subject)
	if !strings.Contains(subj, "forwarding") || (!strings.Contains(subj, "confirm") && !strings.Contains(subj, "verif")) {
		return Confirmation{}, false
	}
	c := Confirmation{Provider: provider, ReceivedAt: at}
	if s := confirmCode.FindStringSubmatch(m.Text); s != nil {
		c.Code = s[1]
	} else if s := subjectCode.FindStringSubmatch(m.Subject); s != nil {
		c.Code = s[1]
	}
	for _, u := range confirmLink.FindAllString(m.Text, -1) {
		lu := strings.ToLower(u)
		if strings.Contains(lu, "confirm") || strings.Contains(lu, "verif") || strings.Contains(lu, "/vf") {
			c.Link = strings.TrimRight(u, ".,;")
			break
		}
	}
	if s := receiveFrom.FindStringSubmatch(m.Subject + " " + m.Text); s != nil {
		c.For = strings.ToLower(strings.Trim(s[1], "<>.,;"))
	}
	return c, c.Code != "" || c.Link != ""
}

// SenderAllowed reports whether a message's From matches the trigger's
// senders (addresses, "@domain" or "domain", subdomains included). An
// empty list takes everyone; a list is trusted only for a From that is
// proven, so a forged one does not pass.
func SenderAllowed(senders []string, from string, auth Auth) bool {
	if len(senders) == 0 {
		return true
	}
	if !auth.Aligned {
		return false
	}
	from = strings.ToLower(from)
	d := Domain(from)
	for _, s := range senders {
		s = strings.ToLower(strings.TrimSpace(s))
		switch {
		case strings.Contains(s, "@") && !strings.HasPrefix(s, "@"):
			if s == from {
				return true
			}
		default:
			s = strings.TrimPrefix(s, "@")
			if d == s || strings.HasSuffix(d, "."+s) {
				return true
			}
		}
	}
	return false
}

// Aligned reports whether a domain that vouched for a message (a DKIM
// signer, the SPF-checked envelope domain) is the From domain's, or one
// of the same organisation: relaxed alignment, taking the shorter of the
// two as the organisation's domain when one is under the other.
func Aligned(vouching, from string) bool {
	vouching, from = strings.ToLower(vouching), strings.ToLower(from)
	if !strings.Contains(vouching, ".") || from == "" {
		return false
	}
	return vouching == from || strings.HasSuffix(from, "."+vouching) || strings.HasSuffix(vouching, "."+from)
}
