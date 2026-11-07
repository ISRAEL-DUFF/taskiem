package inmail

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// Email threads (decision 0063, docs/spec/tickets.md): a workflow's
// address takes a reply tag per ticket, <id>+<tag>@<domain>, which
// Taskiem makes from the ticket key and a secret of the address.

// MaxThreadKey is the longest ticket key.
const MaxThreadKey = 24

// MaxTag is the longest subaddress tag taken (the part after +).
const MaxTag = 40

// tokenLen is the length of a reply tag's secret part: 12 base32
// characters, 60 bits.
const tokenLen = 12

// ReplyEvent is the signal a reply in a thread is delivered as, correlated
// by its ticket key.
const ReplyEvent = "email.reply"

var (
	threadKey = regexp.MustCompile(`^[A-Za-z0-9]+(-[A-Za-z0-9]+)*$`)
	tagSyntax = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	// subjectThread is a [#T-1042] subject tag.
	subjectThread = regexp.MustCompile(`\[#([A-Za-z0-9]+(?:-[A-Za-z0-9]+)*)\]`)
	b32           = base32.StdEncoding.WithPadding(base32.NoPadding)
)

// ThreadKey is a ticket key in its one written form (capitals), and
// whether it is one: letters, digits and single hyphens, at most
// MaxThreadKey characters.
func ThreadKey(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > MaxThreadKey || !threadKey.MatchString(s) {
		return "", false
	}
	return strings.ToUpper(s), true
}

// Tag is an address's subaddress tag as received, lower-cased, or "" when
// it is not one Taskiem reads (too long, or other characters).
func Tag(s string) string {
	s = strings.ToLower(s)
	if len(s) > MaxTag || !tagSyntax.MatchString(s) {
		return ""
	}
	return s
}

func mac(key []byte, parts ...string) []byte {
	m := hmac.New(sha256.New, key)
	for _, p := range parts {
		m.Write([]byte(p))
		m.Write([]byte{0})
	}
	return m.Sum(nil)
}

func token(key []byte, endpoint uuid.UUID, thread string) string {
	return strings.ToLower(b32.EncodeToString(mac(key, "taskiem/reply-tag/v1", endpoint.String(), thread)))[:tokenLen]
}

// ReplyTag is the tag of a thread at an endpoint: the key in lower case,
// a hyphen, and a token only the address's secret makes.
func ReplyTag(key []byte, endpoint uuid.UUID, thread string) string {
	return strings.ToLower(thread) + "-" + token(key, endpoint, thread)
}

// VerifyTag reports the thread a tag names, when the address's secret
// made it. A tag guessed or altered names nothing.
func VerifyTag(key []byte, endpoint uuid.UUID, tag string) (string, bool) {
	if len(key) == 0 {
		return "", false
	}
	i := strings.LastIndexByte(tag, '-')
	if i <= 0 || len(tag)-i-1 != tokenLen {
		return "", false
	}
	thread, ok := ThreadKey(tag[:i])
	if !ok {
		return "", false
	}
	if !hmac.Equal([]byte(tag[i+1:]), []byte(token(key, endpoint, thread))) {
		return "", false
	}
	return thread, true
}

// ThreadMessageID is the Message-ID (without brackets) of an email sent
// in a thread: derived from the step's idempotency key under the
// address's secret, so a retry sends the same one and nobody else can
// make one.
func ThreadMessageID(key []byte, endpoint uuid.UUID, idem, domain string) string {
	return "tk-" + strings.ToLower(b32.EncodeToString(mac(key, "taskiem/thread-message/v1", endpoint.String(), idem))[:26]) + "@" + domain
}

// SubjectThread is the ticket key of a [#T-1042] tag in a subject, or "".
func SubjectThread(subject string) string {
	m := subjectThread.FindStringSubmatch(subject)
	if m == nil {
		return ""
	}
	k, _ := ThreadKey(m[1])
	return k
}

// TagSubject puts a thread's [#KEY] tag before a subject, once: a subject
// that carries it already (a reply's "Re: … [#T-1042]") is left as it is.
func TagSubject(subject, thread string) string {
	subject = strings.TrimSpace(subject)
	if strings.EqualFold(SubjectThread(subject), thread) {
		return subject
	}
	t := "[#" + thread + "]"
	if subject == "" {
		return t
	}
	return t + " " + subject
}
