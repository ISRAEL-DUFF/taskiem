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

// Device kinds: a browser (Web Push) or the phone app (Firebase Cloud
// Messaging, docs/spec/mobile-app-m4.md N1).
const (
	Browser = "webpush"
	App     = "fcm"
)

// The kinds of push a device may take (N3): business chats, customer chats,
// orders and money, approvals, alerts.
const (
	KindBusiness  = "business"
	KindCustomers = "customers"
	KindOrders    = "orders"
	KindApprovals = "approvals"
	KindAlerts    = "alerts"
)

// Kinds is every kind, in the order a settings page shows them.
var Kinds = []string{KindBusiness, KindCustomers, KindOrders, KindApprovals, KindAlerts}

// DefaultKinds is what a new device takes: everything for those who run the
// business (owners and admins), everything but alerts for the others.
func DefaultKinds(runsIt bool) []string {
	if runsIt {
		return append([]string(nil), Kinds...)
	}
	return []string{KindBusiness, KindCustomers, KindOrders, KindApprovals}
}

// CleanKinds keeps the known kinds, once each, in Kinds' order.
func CleanKinds(in []string) ([]string, error) {
	out := []string{}
	for _, k := range Kinds {
		for _, x := range in {
			if x == k {
				out = append(out, k)
				break
			}
		}
	}
	for _, x := range in {
		known := false
		for _, k := range Kinds {
			known = known || x == k
		}
		if !known {
			return nil, fmt.Errorf("%w: %q is not a kind of notification", ErrInvalid, x)
		}
	}
	return out, nil
}

// Subscription is what a device gives when it is allowed to be pushed to: a
// browser's address and keys, or the app's registration token.
type Subscription struct {
	// Kind is webpush (the default) or fcm.
	Kind     string `json:"kind"`
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	// Token is the app's Firebase registration token (kind fcm).
	Token string `json:"token"`
	// Platform is the app's (android, ios).
	Platform string `json:"platform"`
	Label    string `json:"label"`
	Preview  *bool  `json:"preview"`
	// Kinds, when sent, are what the device takes; left out, a new device
	// takes the defaults and a known one keeps its own.
	Kinds *[]string `json:"kinds"`
	// Replaces is the address of the same browser's previous subscription
	// (it renewed it): its name, preview and kinds move to this one, and it
	// goes.
	Replaces string `json:"replaces"`
	// Known asks only to find the device (a page that lost which one it is):
	// one the server does not have is not added again (ErrNotFound).
	Known bool `json:"known"`
}

// address is where the device is reached: the browser's endpoint, or the
// app's token.
func (s Subscription) address() string {
	if s.Kind == App {
		return s.Token
	}
	return s.Endpoint
}

// Device is a device as its owner sees it: never its address or keys.
type Device struct {
	ID         uuid.UUID  `json:"id"`
	Kind       string     `json:"kind"`
	Platform   *string    `json:"platform"`
	Label      string     `json:"label"`
	Preview    bool       `json:"preview"`
	Kinds      []string   `json:"kinds"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSentAt *time.Time `json:"last_sent_at"`
	LastOKAt   *time.Time `json:"last_ok_at"`
}

const deviceCols = `id, kind, platform, label, preview, kinds, created_at, last_sent_at, last_ok_at`

func scanDevice(r pgx.Row, d *Device) error {
	return r.Scan(&d.ID, &d.Kind, &d.Platform, &d.Label, &d.Preview, &d.Kinds, &d.CreatedAt, &d.LastSentAt, &d.LastOKAt)
}

// ValidToken refuses what is not a Firebase registration token: a few
// hundred letters, digits and :_- only (it never reaches a URL of ours: it is
// sent in the body of a request to Firebase).
func ValidToken(t string) error {
	if len(t) < 20 || len(t) > 4096 {
		return fmt.Errorf("%w: not a phone's notification token", ErrInvalid)
	}
	for _, c := range t {
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !letter && c != ':' && c != '_' && c != '-' {
			return fmt.Errorf("%w: not a phone's notification token", ErrInvalid)
		}
	}
	return nil
}

func hashOf(endpoint string) []byte {
	h := sha256.Sum256([]byte(endpoint))
	return h[:]
}

func cleanLabel(s, kind string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > 80 {
		s = string([]rune(s)[:80])
	}
	if s == "" {
		if kind == App {
			return "A phone"
		}
		return "A browser"
	}
	return s
}

// AddTx keeps a device for a member (the caller has checked a browser's
// address with Sender.Check, an app's token with ValidToken; the same device
// again is updated, not doubled); a device past MaxDevices replaces the
// oldest. defaults are the kinds a new device takes when none are sent.
func AddTx(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID, s Subscription, defaults []string) (Device, error) {
	switch s.Kind {
	case "", Browser:
		s.Kind = Browser
		if s.Keys.P256dh == "" || s.Keys.Auth == "" || len(s.Keys.P256dh) > 200 || len(s.Keys.Auth) > 100 {
			return Device{}, fmt.Errorf("%w: the browser's keys are missing", ErrInvalid)
		}
	case App:
		if err := ValidToken(s.Token); err != nil {
			return Device{}, err
		}
		if s.Platform != "android" && s.Platform != "ios" {
			return Device{}, fmt.Errorf("%w: the phone is android or ios", ErrInvalid)
		}
	default:
		return Device{}, fmt.Errorf("%w: a device is a browser or the app", ErrInvalid)
	}
	var kinds *[]string
	if s.Kinds != nil {
		k, err := CleanKinds(*s.Kinds)
		if err != nil {
			return Device{}, err
		}
		kinds = &k
	}
	// One at a time per member, so two devices cannot both take the last place.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('push:' || $1::text || $2::text, 0))`, tenant, user); err != nil {
		return Device{}, err
	}
	if s.Known {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM push_subscriptions WHERE tenant_id = $1 AND user_id = $2 AND endpoint_hash = $3`,
			tenant, user, hashOf(s.address())).Scan(&n); err != nil {
			return Device{}, err
		}
		if n == 0 {
			return Device{}, ErrNotFound
		}
	}
	// A browser that renewed its subscription: the old one's choices carry
	// over, and it goes.
	var label *string
	var preview *bool
	var kept []string
	if s.Kind == Browser && s.Replaces != "" && s.Replaces != s.Endpoint {
		var l string
		var p bool
		err := tx.QueryRow(ctx, `DELETE FROM push_subscriptions WHERE tenant_id = $1 AND user_id = $2 AND endpoint_hash = $3
			RETURNING label, preview, kinds`, tenant, user, hashOf(s.Replaces)).Scan(&l, &p, &kept)
		switch {
		case err == nil:
			label, preview = &l, &p
		case !errors.Is(err, pgx.ErrNoRows):
			return Device{}, err
		}
	}
	if s.Preview == nil {
		s.Preview = preview
	}
	if kinds == nil && kept != nil {
		kinds = &kept
	}
	name := cleanLabel(s.Label, s.Kind)
	if strings.TrimSpace(s.Label) == "" && label != nil {
		name = *label
	}
	var platform *string
	if s.Kind == App {
		platform = &s.Platform
	}
	var p256dh, auth *string
	if s.Kind == Browser {
		p256dh, auth = &s.Keys.P256dh, &s.Keys.Auth
	}
	var d Device
	addr := s.address()
	err := scanDevice(tx.QueryRow(ctx, `INSERT INTO push_subscriptions (id, tenant_id, user_id, kind, platform, endpoint, endpoint_hash, p256dh, auth, label, preview, kinds)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, COALESCE($11::boolean, true), COALESCE($12::text[], $13::text[]))
		ON CONFLICT (tenant_id, user_id, endpoint_hash) DO UPDATE
		   SET p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth, platform = EXCLUDED.platform,
		       label = CASE WHEN $14::boolean THEN EXCLUDED.label ELSE push_subscriptions.label END,
		       preview = COALESCE($11::boolean, push_subscriptions.preview),
		       kinds = COALESCE($12::text[], push_subscriptions.kinds), failed_since = NULL
		RETURNING `+deviceCols,
		uuid.Must(uuid.NewV7()), tenant, user, s.Kind, platform, addr, hashOf(addr), p256dh, auth, name, s.Preview, kinds, defaults, strings.TrimSpace(s.Label) != "" || label != nil), &d)
	if err != nil {
		return Device{}, err
	}
	_, err = tx.Exec(ctx, `DELETE FROM push_subscriptions WHERE tenant_id = $1 AND user_id = $2 AND id IN
		(SELECT id FROM push_subscriptions WHERE tenant_id = $1 AND user_id = $2 ORDER BY created_at DESC, id DESC OFFSET $3)`, tenant, user, MaxDevices)
	return d, err
}

// ListTx is a member's browsers, the oldest first.
func ListTx(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID) ([]Device, error) {
	rows, err := tx.Query(ctx, `SELECT `+deviceCols+` FROM push_subscriptions
		WHERE tenant_id = $1 AND user_id = $2 ORDER BY created_at, id`, tenant, user)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Device, error) {
		var d Device
		return d, scanDevice(r, &d)
	})
	if out == nil {
		out = []Device{}
	}
	return out, err
}

// UpdateTx renames a device, switches its preview or sets the kinds it takes
// (a nil leaves each).
func UpdateTx(ctx context.Context, tx pgx.Tx, tenant, user, id uuid.UUID, label *string, preview *bool, kinds *[]string) (Device, error) {
	var d Device
	var l *string
	if label != nil {
		c := cleanLabel(*label, "")
		l = &c
	}
	var k *[]string
	if kinds != nil {
		c, err := CleanKinds(*kinds)
		if err != nil {
			return d, err
		}
		k = &c
	}
	err := scanDevice(tx.QueryRow(ctx, `UPDATE push_subscriptions SET label = COALESCE($4, label), preview = COALESCE($5, preview),
		kinds = COALESCE($6::text[], kinds)
		WHERE tenant_id = $1 AND user_id = $2 AND id = $3
		RETURNING `+deviceCols, tenant, user, id, l, preview, k), &d)
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

// Target is a device to push to, with what sending needs.
type Target struct {
	ID   uuid.UUID
	User uuid.UUID
	// Kind is the device's: webpush or fcm.
	Kind string
	// Endpoint is a browser's address, or the app's token.
	Endpoint string
	P256dh   string
	Auth     string
	Preview  bool
	Platform string
	// Kinds are the kinds of push it takes.
	Kinds []string
}

// Takes reports whether the device takes this kind of push.
func (t Target) Takes(kind string) bool {
	for _, k := range t.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// TargetsTx is the devices of these members in the business.
func TargetsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, users []uuid.UUID) ([]Target, error) {
	if len(users) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `SELECT id, user_id, kind, endpoint, COALESCE(p256dh, ''), COALESCE(auth, ''), preview, COALESCE(platform, ''), kinds
		FROM push_subscriptions WHERE tenant_id = $1 AND user_id = ANY ($2) ORDER BY created_at, id`, tenant, users)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Target, error) {
		var t Target
		return t, r.Scan(&t.ID, &t.User, &t.Kind, &t.Endpoint, &t.P256dh, &t.Auth, &t.Preview, &t.Platform, &t.Kinds)
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
	// Icon is the sender's logo, a path inside the app the worker fetches with
	// the person's own session (/v1/links/<business>/logo); empty without one.
	Icon string `json:"icon,omitempty"`
	// Business is the receiving business (a tenant id): a person in two is
	// taken to the right one (N3).
	Business string `json:"business,omitempty"`
	// Kind is the kind of push (KindBusiness...): the app's channel.
	Kind string `json:"kind,omitempty"`
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

// For is the payload for a device: with the preview switched off a chat says
// only that someone wrote, and anything else only that something needs them
// (an order's or a bill's title can carry a name or an amount).
func (p Payload) For(t Target) Payload {
	if !t.Preview {
		switch p.Kind {
		case "", KindBusiness:
			p.Body = "New message"
		case KindCustomers:
			// A customer's name, or their number, is theirs: not on a locked screen.
			p.Title, p.Body = "Taskiem", "A customer wrote"
		default:
			p.Title, p.Body = "Taskiem", "Something needs you"
		}
	}
	return p
}

// --- the queue (migration 00446) ---

// Queued is a push waiting to be sent to a person's devices.
type Queued struct {
	ID       uuid.UUID
	User     uuid.UUID
	Kind     string
	Title    string
	Body     string
	Link     string
	Tag      string
	Icon     string
	Attempts int
}

// EnqueueTx queues a push for these members of the business in scope, in the
// caller's transaction: it is sent once that commits, and survives a restart.
// Members with no device are skipped.
func EnqueueTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, users []uuid.UUID, q Queued) error {
	if len(users) == 0 {
		return nil
	}
	var icon *string
	if q.Icon != "" {
		icon = &q.Icon
	}
	tag, err := tx.Exec(ctx, `INSERT INTO push_outbox (id, tenant_id, user_id, kind, title, body, link, tag, icon)
		SELECT gen_random_uuid(), $1, u, $3, left($4, 200), left($5, 1000), $6, left($7, 200), $8
		  FROM unnest($2::uuid[]) AS u
		 WHERE EXISTS (SELECT 1 FROM push_subscriptions s WHERE s.tenant_id = $1 AND s.user_id = u)`,
		tenant, users, q.Kind, cut(q.Title, 200), q.Body, q.Link, q.Tag, icon)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	_, err = tx.Exec(ctx, `SELECT pg_notify('push_outbox', '')`)
	return err
}

// DueTenantsTx is the businesses with pushes due (a narrow definer: the queue
// is drained in each business's own scope).
func DueTenantsTx(ctx context.Context, tx pgx.Tx, limit int) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT taskiem_push_due($1)`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// Lease is how long a claimed push is held for its sender: one that is not
// done or put back by then (its sender stopped) is due again.
const Lease = 90 * time.Second

// ClaimTx takes up to limit pushes due in the business in scope and leases
// them (another sender skips them until Lease has passed), so nothing is
// held open while the push services answer.
func ClaimTx(ctx context.Context, tx pgx.Tx, limit int) ([]Queued, error) {
	rows, err := tx.Query(ctx, `UPDATE push_outbox SET next_at = now() + make_interval(secs => $2)
		WHERE id IN (SELECT id FROM push_outbox WHERE next_at <= now() ORDER BY created_at LIMIT $1 FOR UPDATE SKIP LOCKED)
		RETURNING id, user_id, kind, title, body, link, tag, COALESCE(icon, ''), attempts`, limit, Lease.Seconds())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Queued, error) {
		var q Queued
		return q, r.Scan(&q.ID, &q.User, &q.Kind, &q.Title, &q.Body, &q.Link, &q.Tag, &q.Icon, &q.Attempts)
	})
}

// Backoff is how long a push waits after each failed attempt; past the last
// it is dropped, as is anything older than TTL.
var Backoff = []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour}

// DoneTx removes a push that went (or that no device takes).
func DoneTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `DELETE FROM push_outbox WHERE id = $1`, id)
	return err
}

// RetryTx puts a push back for later, or drops it past the last wait or its
// TTL; it answers whether it was dropped.
func RetryTx(ctx context.Context, tx pgx.Tx, q Queued) (bool, error) {
	if q.Attempts >= len(Backoff) {
		return true, DoneTx(ctx, tx, q.ID)
	}
	tag, err := tx.Exec(ctx, `UPDATE push_outbox SET attempts = attempts + 1, next_at = now() + make_interval(secs => $2)
		WHERE id = $1 AND created_at > now() - make_interval(secs => $3)`, q.ID, Backoff[q.Attempts].Seconds(), float64(TTL))
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return true, DoneTx(ctx, tx, q.ID)
	}
	return false, nil
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

// Sender sends pushes with the operator's keys, through the egress guard:
// Web Push to browsers with the VAPID keys, FCM to the app with the service
// account (either may be off).
type Sender struct {
	Config Config
	// FCM is the phone app's transport (nil: off).
	FCM   *FCM
	Guard *egress.Guard
	// Validate, when set, replaces ValidEndpoint (tests only).
	Validate func(string) error
	// Retry is how long to wait before the one retry (default 5 s).
	Retry time.Duration
}

// Enabled reports whether pushes can be sent to any device.
func (s *Sender) Enabled() bool { return s.WebPushOn() || s.FCMOn() }

// WebPushOn reports whether browsers can be pushed to (the VAPID keys).
func (s *Sender) WebPushOn() bool { return s != nil && s.Config.Enabled() }

// FCMOn reports whether the app can be pushed to (Firebase).
func (s *Sender) FCMOn() bool { return s != nil && s.FCM != nil }

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
	retryFCM := s.Retry
	if retryFCM == 0 {
		retryFCM = 5 * time.Second
	}
	if t.Kind == App {
		if !s.FCMOn() {
			return Failed, 0, errors.New("push: Firebase is not set up")
		}
		return s.FCM.send(ctx, s.Guard, tenant, t, p, retryFCM)
	}
	if !s.WebPushOn() {
		return Failed, 0, errors.New("push: the VAPID keys are not set")
	}
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
