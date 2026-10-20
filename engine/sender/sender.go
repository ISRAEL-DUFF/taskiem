// Package sender sends SMS and email to people outside a tenant: the
// customers of a storefront and the respondents of a hosted form, who have
// no Taskiem account (Phase 5, S1; docs/messages.md). It sends through the
// platform's own providers, on a tenant's behalf, from a durable outbox.
//
// A message is queued (Queue.Enqueue) in the same transaction as whatever
// caused it, so it is never lost, and sent by the Worker, which claims each
// attempt once, falls back from one SMS provider to the next, backs off on
// failure and keeps dead letters. The recipient and the text are sealed
// under the recipient's personal-data subject and wiped once the message is
// finished.
package sender

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/secrets"
)

// Channels and purposes.
const (
	SMS   = "sms"
	Email = "email"

	// PurposeCode is a one-time code: it expires (CodeTTL by default) and
	// is dropped rather than sent late.
	PurposeCode         = "code"
	PurposeConfirmation = "confirmation"
	PurposeLink         = "link"
	PurposeNotice       = "notice"
)

// CodeTTL is how long a code message may wait to be sent.
const CodeTTL = 10 * time.Minute

// MaxSegments caps an SMS at three parts: longer texts cost more than they
// are worth on a phone, and belong in a link.
const MaxSegments = 3

var (
	// ErrInvalid wraps every refusal of a message's content or address.
	ErrInvalid = errors.New("sender: invalid message")
	// ErrRecipientLimit refuses a message to someone who has had too many
	// in the last hour or day, from any tenant.
	ErrRecipientLimit = errors.New("sender: too many messages to this recipient")
	// ErrAllowance refuses an SMS beyond the tenant's monthly allowance
	// (the plan's sms_monthly). Codes are never refused for it.
	ErrAllowance = errors.New("sender: this month's SMS allowance is used up")
)

// Message is one SMS or email to queue.
type Message struct {
	Tenant  uuid.UUID
	Channel string
	Purpose string
	// To is a phone number (+2348031234567, 2348031234567 or 08031234567)
	// or an email address.
	To string
	// SenderName is the business's name. An SMS starts with it, and an
	// email comes from "<SenderName> via Taskiem".
	SenderName string
	// ReplyTo is the business's own email, for replies to an email.
	ReplyTo string
	Subject string // email only
	Text    string
	// Key makes queueing idempotent: a second message with the same key
	// for the tenant is not queued.
	Key string
	// ExpiresAt drops the message if it could not be sent by then. Codes
	// default to CodeTTL from now.
	ExpiresAt time.Time
}

// Queue validates and queues messages.
type Queue struct {
	Vault *secrets.Vault
	// HashKey keys the recipient hash the platform-wide limits count by
	// (TASKIEM_MESSAGE_HASH_KEY, 32 bytes). Phone numbers are too few to
	// hash unkeyed: anyone could reverse the hash by trying them all.
	HashKey []byte
	// PerHour and PerDay limit messages to one recipient across tenants
	// (default 5 and 20).
	PerHour, PerDay int
	// Allowance is the tenant's monthly SMS allowance (0: no limit); nil
	// counts none.
	Allowance func(ctx context.Context, tenant uuid.UUID) (int64, error)
	// Countries are the calling codes SMS may go to (default 234):
	// international premium routes are where SMS-pumping fraud makes money.
	Countries []string
}

// Enqueue checks m and queues it in tx, a transaction in m.Tenant's scope.
// It reports false, and no error, when a message with the same key was
// queued before.
func (q *Queue) Enqueue(ctx context.Context, tx pgx.Tx, m Message) (bool, error) {
	to, category, err := q.check(&m)
	if err != nil {
		return false, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM platform_messages WHERE tenant_id = $1 AND dedup_key = $2)`,
		m.Tenant, m.Key).Scan(&exists); err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	if m.Channel == SMS && m.Purpose != PurposeCode && q.Allowance != nil {
		allowed, err := q.Allowance(ctx, m.Tenant)
		if err != nil {
			return false, err
		}
		if allowed > 0 {
			var used int64
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM platform_messages WHERE tenant_id = $1 AND channel = 'sms'
				AND created_at >= date_trunc('month', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'`, m.Tenant).Scan(&used); err != nil {
				return false, err
			}
			if used >= allowed {
				return false, ErrAllowance
			}
		}
	}
	perHour, perDay := q.PerHour, q.PerDay
	if perHour <= 0 {
		perHour = 5
	}
	if perDay <= 0 {
		perDay = 20
	}
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT taskiem_platform_recipient_take($1, $2, $3)`, q.hash(m.Channel, to), perHour, perDay).Scan(&ok); err != nil {
		return false, err
	}
	if !ok {
		return false, ErrRecipientLimit
	}
	recipient, err := q.Vault.SealTx(ctx, tx, m.Tenant, category, to)
	if err != nil {
		return false, err
	}
	subject, _ := recipient["subject"].(string)
	body, err := q.Vault.SealUnderTx(ctx, tx, m.Tenant, subject, category, map[string]any{"subject": m.Subject, "text": m.Text, "reply_to": m.ReplyTo})
	if err != nil {
		return false, err
	}
	var expires *time.Time
	if !m.ExpiresAt.IsZero() {
		expires = &m.ExpiresAt
	}
	tag, err := tx.Exec(ctx, `INSERT INTO platform_messages (id, tenant_id, channel, purpose, dedup_key, subject_id, recipient, body, sender_name, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) ON CONFLICT (tenant_id, dedup_key) DO NOTHING`,
		uuid.Must(uuid.NewV7()), m.Tenant, m.Channel, m.Purpose, m.Key, subject, recipient, body, m.SenderName, expires)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// check normalises m in place and returns its address and the personal-
// data category it is sealed under.
func (q *Queue) check(m *Message) (to, category string, err error) {
	switch m.Purpose {
	case PurposeCode, PurposeConfirmation, PurposeLink, PurposeNotice:
	default:
		return "", "", fmt.Errorf("%w: purpose %q", ErrInvalid, m.Purpose)
	}
	if m.Tenant == uuid.Nil {
		return "", "", fmt.Errorf("%w: no tenant", ErrInvalid)
	}
	if m.Key == "" || len(m.Key) > 200 {
		return "", "", fmt.Errorf("%w: a key of 1 to 200 characters is required", ErrInvalid)
	}
	if m.SenderName = CleanName(m.SenderName); m.SenderName == "" {
		return "", "", fmt.Errorf("%w: a sender name is required", ErrInvalid)
	}
	if strings.TrimSpace(m.Text) == "" {
		return "", "", fmt.Errorf("%w: no text", ErrInvalid)
	}
	if m.Purpose == PurposeCode && m.ExpiresAt.IsZero() {
		m.ExpiresAt = time.Now().Add(CodeTTL)
	}
	switch m.Channel {
	case SMS:
		if to, err = NormalisePhone(m.To, q.countries()); err != nil {
			return "", "", err
		}
		if m.Subject != "" || m.ReplyTo != "" {
			return "", "", fmt.Errorf("%w: an SMS has no subject or reply-to", ErrInvalid)
		}
		if !strings.HasPrefix(m.Text, m.SenderName) {
			m.Text = m.SenderName + ": " + m.Text
		}
		if n := Segments(m.Text); n > MaxSegments {
			return "", "", fmt.Errorf("%w: the SMS is %d parts, at most %d", ErrInvalid, n, MaxSegments)
		}
		return to, "phone", nil
	case Email:
		if to, err = NormaliseEmail(m.To); err != nil {
			return "", "", err
		}
		if m.ReplyTo != "" {
			if m.ReplyTo, err = NormaliseEmail(m.ReplyTo); err != nil {
				return "", "", fmt.Errorf("reply-to: %w", err)
			}
		}
		if m.Subject = strings.TrimSpace(strings.Map(dropControl, m.Subject)); m.Subject == "" || len(m.Subject) > 200 {
			return "", "", fmt.Errorf("%w: an email needs a subject of up to 200 characters", ErrInvalid)
		}
		if len(m.Text) > 20000 {
			return "", "", fmt.Errorf("%w: the email is longer than 20,000 characters", ErrInvalid)
		}
		return to, "email", nil
	}
	return "", "", fmt.Errorf("%w: channel %q", ErrInvalid, m.Channel)
}

func (q *Queue) countries() []string {
	if len(q.Countries) == 0 {
		return []string{"234"}
	}
	return q.Countries
}

func (q *Queue) hash(channel, to string) []byte {
	h := hmac.New(sha256.New, q.HashKey)
	h.Write([]byte(channel + "\x00" + to))
	return h.Sum(nil)
}

// NormalisePhone returns a phone number as E.164 (+2348031234567). A
// number starting 0 is Nigerian. The calling code must be one of
// countries.
func NormalisePhone(s string, countries []string) (string, error) {
	d := strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '(' || r == ')' || r == '.' {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	switch {
	case strings.HasPrefix(d, "+"):
		d = d[1:]
	case strings.HasPrefix(d, "00"):
		d = d[2:]
	case strings.HasPrefix(d, "0") && len(d) == 11:
		d = "234" + d[1:]
	}
	if len(d) < 8 || len(d) > 15 || strings.Trim(d, "0123456789") != "" || d[0] == '0' {
		return "", fmt.Errorf("%w: %q is not a phone number", ErrInvalid, s)
	}
	for _, c := range countries {
		if strings.HasPrefix(d, c) {
			if c == "234" && (len(d) != 13 || !strings.ContainsRune("789", rune(d[3]))) {
				return "", fmt.Errorf("%w: %q is not a Nigerian mobile number", ErrInvalid, s)
			}
			return "+" + d, nil
		}
	}
	return "", fmt.Errorf("%w: SMS cannot go to %q's country", ErrInvalid, s)
}

// NormaliseEmail returns a bare, lower-cased email address.
func NormaliseEmail(s string) (string, error) {
	s = strings.TrimSpace(s)
	a, err := mail.ParseAddress(s)
	if err != nil || a.Name != "" || a.Address != s || len(s) > 254 || !strings.Contains(s[strings.LastIndexByte(s, '@')+1:], ".") {
		return "", fmt.Errorf("%w: %q is not an email address", ErrInvalid, s)
	}
	return strings.ToLower(s), nil
}

// CleanName makes a business's name safe for an SMS prefix and an email
// display name: no control or quoting characters, at most 60 characters.
func CleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '"' || r == '<' || r == '>' || r == '\\' {
			return -1
		}
		return dropControl(r)
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 60 {
		s = strings.TrimSpace(string(r[:60]))
	}
	return s
}

func dropControl(r rune) rune {
	if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
		return -1
	}
	return r
}
