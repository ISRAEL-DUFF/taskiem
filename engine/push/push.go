// Package push is Web Push for a chat message when the app is closed
// (docs/spec/chat-push.md): the browsers a member allowed, what is sent to
// them, and the sending. A push is one more channel of the function that
// already tells a business's people about a message; nothing here decides
// who is told (the API does, with the same rules as the phone messages).
package push

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/egress"
)

// Limits.
const (
	// MaxDevices is how many browsers a member has in a business: a new one
	// past it replaces the oldest.
	MaxDevices = 10
	// MaxPayload is the most bytes a push carries (the push services take
	// 4096 after encryption; this leaves room).
	MaxPayload = 2048
	// TTL is how long a push service keeps a message for a browser that is
	// off, in seconds: after that the message is stale anyway.
	TTL = 12 * 60 * 60
	// DropAfter is how long a browser may fail in a row before it is dropped.
	DropAfter = 72 * time.Hour
	// SendTimeout bounds one request to a push service.
	SendTimeout = 10 * time.Second
	// Version is the payload's version, for a worker to tell what it reads.
	Version = 1
)

// ErrInvalid is for what a caller sent that cannot be kept.
var ErrInvalid = errors.New("push: invalid")

// ErrNotFound is for a device that is not this person's (or not there).
var ErrNotFound = errors.New("push: not found")

// Config is the operator's VAPID key pair and the address a push service may
// reach us at. With no keys the feature is off.
type Config struct {
	PublicKey  string
	PrivateKey string
	// Subject is a mailto: or https: address (RFC 8292).
	Subject string
}

// Enabled reports whether the keys are set.
func (c Config) Enabled() bool { return c.PublicKey != "" && c.PrivateKey != "" }

// Validate checks a configuration that is set is usable.
func (c Config) Validate() error {
	if !c.Enabled() {
		if c.PublicKey != "" || c.PrivateKey != "" {
			return errors.New("push: TASKIEM_VAPID_PUBLIC_KEY and TASKIEM_VAPID_PRIVATE_KEY are set together, or not at all")
		}
		return nil
	}
	if !strings.HasPrefix(c.Subject, "mailto:") && !strings.HasPrefix(c.Subject, "https://") {
		return errors.New("push: TASKIEM_VAPID_SUBJECT is a mailto: or https: address the push services may reach (RFC 8292)")
	}
	return nil
}

// subscriber is the subject as the library takes it: it adds mailto: itself to
// an address, and takes an https: address whole.
func (c Config) subscriber() string {
	return strings.TrimPrefix(c.Subject, "mailto:")
}

// GenerateKeys makes a VAPID key pair (base64url, as browsers take them).
func GenerateKeys() (privateKey, publicKey string, err error) {
	return webpush.GenerateVAPIDKeys()
}

// pushHosts are the push services a subscription may name: a browser maker's
// own hosts. A subscription for any other host is refused, so one cannot make
// the server call somewhere else (SSRF).
var pushHosts = []string{
	"fcm.googleapis.com",
	"updates.push.services.mozilla.com",
}

// pushSuffixes are the families of hosts of Mozilla's, Apple's and
// Microsoft's services.
var pushSuffixes = []string{
	".push.services.mozilla.com",
	".push.apple.com",
	".notify.windows.com",
}

// ValidEndpoint refuses an address that is not a push service's: it must be
// https, on a host of a known push service, with no user information, no
// other port, no query that is not the service's and no fragment.
func ValidEndpoint(raw string) error {
	if len(raw) == 0 || len(raw) > 2048 {
		return fmt.Errorf("%w: the address is too long or empty", ErrInvalid)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Hostname() == "" {
		return fmt.Errorf("%w: not a push service's address", ErrInvalid)
	}
	if p := u.Port(); p != "" && p != "443" {
		return fmt.Errorf("%w: not a push service's address", ErrInvalid)
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range pushHosts {
		if host == h {
			return nil
		}
	}
	for _, s := range pushSuffixes {
		if strings.HasSuffix(host, s) && len(host) > len(s) {
			return nil
		}
	}
	return fmt.Errorf("%w: not a push service's address", ErrInvalid)
}

// Subscription is what a browser gives when it is allowed to be pushed to.
type Subscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	Label   string `json:"label"`
	Preview *bool  `json:"preview"`
}

// Device is a browser as its owner sees it: never its address or keys.
type Device struct {
	ID         uuid.UUID  `json:"id"`
	Label      string     `json:"label"`
	Preview    bool       `json:"preview"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSentAt *time.Time `json:"last_sent_at"`
	LastOKAt   *time.Time `json:"last_ok_at"`
}

func hashOf(endpoint string) []byte {
	h := sha256.Sum256([]byte(endpoint))
	return h[:]
}

func cleanLabel(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > 80 {
		s = string([]rune(s)[:80])
	}
	if s == "" {
		return "A browser"
	}
	return s
}

// AddTx keeps a browser for a member (the caller has checked its address with
// Sender.Check; the same browser again is updated, not doubled); a browser past MaxDevices replaces the oldest.
func AddTx(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID, s Subscription) (Device, error) {
	if s.Keys.P256dh == "" || s.Keys.Auth == "" || len(s.Keys.P256dh) > 200 || len(s.Keys.Auth) > 100 {
		return Device{}, fmt.Errorf("%w: the browser's keys are missing", ErrInvalid)
	}
	// One at a time per member, so two browsers cannot both take the last place.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('push:' || $1::text || $2::text, 0))`, tenant, user); err != nil {
		return Device{}, err
	}
	var d Device
	err := tx.QueryRow(ctx, `INSERT INTO push_subscriptions (id, tenant_id, user_id, endpoint, endpoint_hash, p256dh, auth, label, preview)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE($9::boolean, true))
		ON CONFLICT (tenant_id, user_id, endpoint_hash) DO UPDATE
		   SET p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth, label = EXCLUDED.label,
		       preview = COALESCE($9::boolean, push_subscriptions.preview), failed_since = NULL
		RETURNING id, label, preview, created_at, last_sent_at, last_ok_at`,
		uuid.Must(uuid.NewV7()), tenant, user, s.Endpoint, hashOf(s.Endpoint), s.Keys.P256dh, s.Keys.Auth, cleanLabel(s.Label), s.Preview).
		Scan(&d.ID, &d.Label, &d.Preview, &d.CreatedAt, &d.LastSentAt, &d.LastOKAt)
	if err != nil {
		return Device{}, err
	}
	_, err = tx.Exec(ctx, `DELETE FROM push_subscriptions WHERE tenant_id = $1 AND user_id = $2 AND id IN
		(SELECT id FROM push_subscriptions WHERE tenant_id = $1 AND user_id = $2 ORDER BY created_at DESC, id DESC OFFSET $3)`, tenant, user, MaxDevices)
	return d, err
}

// ListTx is a member's browsers, the oldest first.
func ListTx(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID) ([]Device, error) {
	rows, err := tx.Query(ctx, `SELECT id, label, preview, created_at, last_sent_at, last_ok_at FROM push_subscriptions
		WHERE tenant_id = $1 AND user_id = $2 ORDER BY created_at, id`, tenant, user)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Device, error) {
		var d Device
		return d, r.Scan(&d.ID, &d.Label, &d.Preview, &d.CreatedAt, &d.LastSentAt, &d.LastOKAt)
	})
	if out == nil {
		out = []Device{}
	}
	return out, err
}

// UpdateTx renames a browser or switches its preview (a nil leaves it).
func UpdateTx(ctx context.Context, tx pgx.Tx, tenant, user, id uuid.UUID, label *string, preview *bool) (Device, error) {
	var d Device
	var l *string
	if label != nil {
		c := cleanLabel(*label)
		l = &c
	}
	err := tx.QueryRow(ctx, `UPDATE push_subscriptions SET label = COALESCE($4, label), preview = COALESCE($5, preview)
		WHERE tenant_id = $1 AND user_id = $2 AND id = $3
		RETURNING id, label, preview, created_at, last_sent_at, last_ok_at`, tenant, user, id, l, preview).
		Scan(&d.ID, &d.Label, &d.Preview, &d.CreatedAt, &d.LastSentAt, &d.LastOKAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

// RemoveTx takes a member's browser off.
func RemoveTx(ctx context.Context, tx pgx.Tx, tenant, user, id uuid.UUID) error {
	tag, err := tx.Exec(ctx, `DELETE FROM push_subscriptions WHERE tenant_id = $1 AND user_id = $2 AND id = $3`, tenant, user, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveEndpointTx takes the browser with this address off (it took itself
// off: the switch, or signing out). Nothing there is not an error.
func RemoveEndpointTx(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID, endpoint string) error {
	_, err := tx.Exec(ctx, `DELETE FROM push_subscriptions WHERE tenant_id = $1 AND user_id = $2 AND endpoint_hash = $3`, tenant, user, hashOf(endpoint))
	return err
}

// DropOrphansTx takes off the browsers of a person who has no role left in
// the business in scope (the business is scoped by the transaction): nobody to
// write to, so nothing to push (a push is also checked against the role at
// send time).
func DropOrphansTx(ctx context.Context, tx pgx.Tx, user uuid.UUID) error {
	_, err := tx.Exec(ctx, `DELETE FROM push_subscriptions WHERE user_id = $1 AND NOT EXISTS (SELECT 1 FROM memberships WHERE user_id = $1)`, user)
	return err
}

// Target is a browser to push to, with what sending needs.
type Target struct {
	ID       uuid.UUID
	User     uuid.UUID
	Endpoint string
	P256dh   string
	Auth     string
	Preview  bool
}

// TargetsTx is the browsers of these members in the business.
func TargetsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, users []uuid.UUID) ([]Target, error) {
	if len(users) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `SELECT id, user_id, endpoint, p256dh, auth, preview FROM push_subscriptions
		WHERE tenant_id = $1 AND user_id = ANY ($2) ORDER BY created_at, id`, tenant, users)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Target, error) {
		var t Target
		return t, r.Scan(&t.ID, &t.User, &t.Endpoint, &t.P256dh, &t.Auth, &t.Preview)
	})
}

// Outcome is how a push went, for the record of the browser.
type Outcome int

// Outcomes.
const (
	Sent   Outcome = iota // the push service took it
	Gone                  // 404 or 410: the browser is gone for good
	Failed                // anything else: it may come back
)

// RecordTx writes what happened: a browser that is gone is deleted, one that
// answered is marked, one that has failed for DropAfter is dropped.
func RecordTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, o Outcome) error {
	switch o {
	case Gone:
		_, err := tx.Exec(ctx, `DELETE FROM push_subscriptions WHERE tenant_id = $1 AND id = $2`, tenant, id)
		return err
	case Sent:
		_, err := tx.Exec(ctx, `UPDATE push_subscriptions SET last_sent_at = now(), last_ok_at = now(), failed_since = NULL WHERE tenant_id = $1 AND id = $2`, tenant, id)
		return err
	default:
		_, err := tx.Exec(ctx, `UPDATE push_subscriptions SET last_sent_at = now(), failed_since = COALESCE(failed_since, now()) WHERE tenant_id = $1 AND id = $2`, tenant, id)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM push_subscriptions WHERE tenant_id = $1 AND id = $2 AND failed_since < now() - make_interval(secs => $3)`, tenant, id, DropAfter.Seconds()) // seconds as a number
		return err
	}
}

// Payload is what the service worker reads. It holds no message id, no
// reference and no amount: the words are the sender's business and a line.
type Payload struct {
	Version int    `json:"v"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	// Link is a path inside the app (/chat?with=<link>).
	Link string `json:"link"`
	// Tag is the conversation: a notification replaces the last one with it.
	Tag string `json:"tag"`
	// Unread is how many conversations have something unread (the icon's badge).
	Unread int `json:"unread"`
}

// cut shortens s to at most n bytes at a letter's edge, with an ellipsis.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return strings.TrimSpace(s[:n]) + "…"
}

// Encode is the payload as sent: JSON, at most MaxPayload bytes (the body is
// shortened to fit).
func (p Payload) Encode() []byte {
	p.Version = Version
	p.Title = cut(p.Title, 200)
	p.Body = cut(p.Body, 400)
	b, _ := json.Marshal(p)
	for len(b) > MaxPayload && len(p.Body) > 20 {
		p.Body = cut(p.Body, len(p.Body)/2)
		b, _ = json.Marshal(p)
	}
	return b
}

// For is the payload for a browser: with the preview switched off it says
// only that a business wrote.
func (p Payload) For(t Target) Payload {
	if !t.Preview {
		p.Body = "New message"
	}
	return p
}

// Topic is the header that lets a push service replace an undelivered push
// for the same conversation with the next (RFC 8030: at most 32 base64url
// characters).
func Topic(link string) string {
	h := sha256.Sum256([]byte("taskiem/push/" + link))
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	out := make([]byte, 32)
	for i := range out {
		out[i] = alphabet[int(h[i])%64]
	}
	return string(out)
}

// Sender sends pushes with the operator's keys, through the egress guard.
type Sender struct {
	Config Config
	Guard  *egress.Guard
	// Validate, when set, replaces ValidEndpoint (tests only).
	Validate func(string) error
	// Retry is how long to wait before the one retry (default 5 s).
	Retry time.Duration
}

// Enabled reports whether pushes can be sent.
func (s *Sender) Enabled() bool { return s != nil && s.Config.Enabled() }

// Check refuses an address that is not a push service's (ValidEndpoint, or
// the test's own rule).
func (s *Sender) Check(endpoint string) error {
	if s.Validate != nil {
		return s.Validate(endpoint)
	}
	return ValidEndpoint(endpoint)
}

// Send sends one payload to one browser: a retry after a network error, a 429
// or a 5xx, and then the outcome.
func (s *Sender) Send(ctx context.Context, tenant uuid.UUID, t Target, p Payload) (Outcome, int, error) {
	if err := s.Check(t.Endpoint); err != nil {
		return Gone, 0, err // an address that should never have been kept
	}
	u, err := url.Parse(t.Endpoint)
	if err != nil {
		return Gone, 0, err
	}
	body := p.For(t).Encode()
	retry := s.Retry
	if retry == 0 {
		retry = 5 * time.Second
	}
	var status int
	var last error
	for try := 0; try < 2; try++ {
		if try > 0 {
			select {
			case <-ctx.Done():
				return Failed, status, ctx.Err()
			case <-time.After(retry):
			}
		}
		client := s.Guard.Client(egress.Policy{Tenant: tenant.String(), Hosts: []string{u.Hostname()}, Purpose: "webpush"}, SendTimeout)
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		resp, err := webpush.SendNotificationWithContext(ctx, body, &webpush.Subscription{Endpoint: t.Endpoint, Keys: webpush.Keys{Auth: t.Auth, P256dh: t.P256dh}}, &webpush.Options{
			HTTPClient:      client,
			Subscriber:      s.Config.subscriber(),
			VAPIDPublicKey:  s.Config.PublicKey,
			VAPIDPrivateKey: s.Config.PrivateKey,
			TTL:             TTL,
			Urgency:         webpush.UrgencyHigh,
			Topic:           Topic(p.Tag),
		})
		if err != nil {
			last = err
			continue
		}
		status = resp.StatusCode
		_ = resp.Body.Close()
		switch {
		case status >= 200 && status < 300:
			return Sent, status, nil
		case status == http.StatusNotFound || status == http.StatusGone:
			return Gone, status, nil
		case status == http.StatusTooManyRequests || status >= 500:
			last = fmt.Errorf("push service answered %d", status)
			continue
		default:
			return Failed, status, fmt.Errorf("push service answered %d", status)
		}
	}
	return Failed, status, last
}
