package telegram

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

const (
	// CallbackTTL is how long a button's payload is kept: past the longest
	// decision token's life.
	CallbackTTL = 8 * 24 * time.Hour
	// LinkCodeTTL and LinkCodesPerHour bound link codes (00175).
	LinkCodeTTL      = 10 * time.Minute
	LinkCodesPerHour = 5
)

// ErrTooMany: the person has made five link codes this hour.
var ErrTooMany = errors.New("telegram: too many link codes this hour; try again later")

// Platform is the platform bot at work: sending, and the person-level
// records behind it.
type Platform struct {
	Pool   *pgxpool.Pool
	Config Config
	Client *Client
	Logger *slog.Logger
	// Now is the clock (tests).
	Now func() time.Time
}

// New builds the platform from its configuration.
func New(pool *pgxpool.Pool, cfg Config, guard *egress.Guard, log *slog.Logger) *Platform {
	if cfg.APIURL == "" {
		cfg.APIURL = DefaultAPIURL
	}
	// An operator pointing the bot at a fake Bot API on this machine
	// (browser tests) reaches loopback, and nothing else private.
	if u, err := url.Parse(cfg.APIURL); err == nil {
		if ip, err := netip.ParseAddr(u.Hostname()); err == nil && ip.IsLoopback() {
			var log *slog.Logger
			if guard != nil {
				log = guard.Logger
			}
			guard = &egress.Guard{Logger: log, Blocked: func(a netip.Addr) bool { return !a.IsLoopback() && egress.BlockedAddr(a) }}
		}
	}
	return &Platform{Pool: pool, Config: cfg, Logger: log, Client: &Client{BaseURL: cfg.APIURL, Token: cfg.BotToken, Egress: guard}}
}

// Name is the channel's name in the tables it shares with WhatsApp.
func (p *Platform) Name() string { return "telegram" }

// OwnTenant is uuid.Nil: one bot speaks for every tenant.
func (p *Platform) OwnTenant() uuid.UUID { return uuid.Nil }

// Send delivers m to a tg: address. Telegram has no service window, so
// only m.Text is sent (Template, Vars and Payloads are WhatsApp's), as
// m.Image's caption when there is one; each
// button becomes a row of the inline keyboard. A button carries c:<id>
// and its ID stays in the database, usable only from this account
// (callback_data holds 64 bytes, decision tokens are longer). It returns
// the message id.
func (p *Platform) Send(ctx context.Context, addr string, m whatsapp.Message) (string, error) {
	chat, ok := ParseAddress(addr)
	if !ok {
		return "", errors.New("telegram: not a Telegram address")
	}
	var rows [][]InlineButton
	for _, b := range m.Buttons {
		if b.ID == "" || len(b.ID) > 1024 {
			return "", errors.New("telegram: a button's payload must be 1-1024 bytes")
		}
		id, err := newCallbackID()
		if err != nil {
			return "", err
		}
		if _, err := p.Pool.Exec(ctx, `SELECT taskiem_tg_callback_put($1, $2, $3, make_interval(secs => $4))`,
			id, chat, b.ID, CallbackTTL.Seconds()); err != nil {
			return "", fmt.Errorf("telegram: storing a button: %w", err)
		}
		title := b.Title
		if title == "" {
			title = b.ID
		}
		rows = append(rows, []InlineButton{{Text: title, Data: "c:" + id}})
	}
	var id int64
	var err error
	if m.Image != "" {
		id, err = p.Client.SendPhoto(ctx, chat, m.Image, m.Text, rows)
	} else {
		id, err = p.Client.SendMessage(ctx, chat, m.Text, rows)
	}
	if err != nil {
		return "", err
	}
	return formatID(id), nil
}

var callbackIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

// newCallbackID is 22 base64url characters (132 random bits).
func newCallbackID() (string, error) {
	b := make([]byte, 17)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("telegram: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b)[:22], nil
}

// ResolveCallback is the payload behind a tapped button's data (c:<id>):
// ok is false for data the channel did not send, an expired button, or one
// sent to another account.
func (p *Platform) ResolveCallback(ctx context.Context, telegramID int64, data string) (string, bool, error) {
	if len(data) != 24 || data[:2] != "c:" || !callbackIDRe.MatchString(data[2:]) || telegramID <= 0 {
		return "", false, nil
	}
	var payload *string
	if err := p.Pool.QueryRow(ctx, `SELECT taskiem_tg_callback_get($1, $2)`, data[2:], telegramID).Scan(&payload); err != nil {
		return "", false, fmt.Errorf("telegram: reading a button: %w", err)
	}
	if payload == nil {
		return "", false, nil
	}
	return *payload, true, nil
}

// Contact reads an account's conversation.
func (p *Platform) Contact(ctx context.Context, addr string) (whatsapp.Contact, error) {
	var c whatsapp.Contact
	id, ok := ParseAddress(addr)
	if !ok {
		return c, errors.New("telegram: not a Telegram address")
	}
	err := p.Pool.QueryRow(ctx, `SELECT current_tenant, last_inbound_at FROM taskiem_tg_contact($1)`, id).Scan(&c.Tenant, &c.LastInbound)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("telegram: reading a contact: %w", err)
	}
	return c, nil
}

// Touch records an inbound message, and with setTenant the tenant the
// account now speaks to.
func (p *Platform) Touch(ctx context.Context, addr string, inbound, setTenant bool, tenant *uuid.UUID) error {
	id, ok := ParseAddress(addr)
	if !ok {
		return errors.New("telegram: not a Telegram address")
	}
	if _, err := p.Pool.Exec(ctx, `SELECT taskiem_tg_contact_touch($1, $2, $3, $4)`, id, inbound, setTenant, tenant); err != nil {
		return fmt.Errorf("telegram: touching a contact: %w", err)
	}
	return nil
}

// Prefix is how a tenant's messages start: its name in brackets (one bot
// speaks for every tenant).
func (p *Platform) Prefix(tenant string) string {
	if tenant == "" {
		return ""
	}
	return "[" + tenant + "] "
}

// ClaimInbound reports whether an update id is new (Telegram redelivers
// until it gets a 200).
func (p *Platform) ClaimInbound(ctx context.Context, updateID int64) (bool, error) {
	var fresh bool
	if err := p.Pool.QueryRow(ctx, `SELECT taskiem_tg_inbound_claim($1)`, updateID).Scan(&fresh); err != nil {
		return false, fmt.Errorf("telegram: claiming an update: %w", err)
	}
	return fresh, nil
}

// UserOf is the active person an account is bound to, or uuid.Nil.
func (p *Platform) UserOf(ctx context.Context, telegramID int64) (uuid.UUID, error) {
	var u *uuid.UUID
	if err := p.Pool.QueryRow(ctx, `SELECT taskiem_tg_user_of($1)`, telegramID).Scan(&u); err != nil {
		return uuid.Nil, fmt.Errorf("telegram: reading a binding: %w", err)
	}
	if u == nil {
		return uuid.Nil, nil
	}
	return *u, nil
}

// IssueLinkCode makes a one-time code for a person to open as
// t.me/<bot>?start=<code> (LinkURL), replacing any pending one. It is 24
// random bytes in base64url: 32 characters, all valid in a /start payload.
// Only its SHA-256 is kept.
func (p *Platform) IssueLinkCode(ctx context.Context, user uuid.UUID) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("telegram: %w", err)
	}
	code := base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(code))
	var outcome string
	if err := p.Pool.QueryRow(ctx, `SELECT taskiem_tg_code_issue($1, $2, make_interval(secs => $3), $4)`,
		user, sum[:], LinkCodeTTL.Seconds(), LinkCodesPerHour).Scan(&outcome); err != nil {
		return "", fmt.Errorf("telegram: issuing a link code: %w", err)
	}
	switch outcome {
	case "ok":
		return code, nil
	case "too_many":
		return "", ErrTooMany
	}
	return "", fmt.Errorf("telegram: issuing a link code: unexpected outcome %q", outcome)
}

// LinkURL is the link that opens the bot with code as the /start payload.
func (p *Platform) LinkURL(code string) string {
	return "https://t.me/" + p.Config.BotUsername + "?start=" + url.QueryEscape(code)
}

var (
	linkCodeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{32}$`)
	tgNameRe   = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
)

// Redeem binds the account that sent a code to the code's person, spending
// the code. outcome is "ok" (with the person), "invalid" (no such live
// code) or "account_taken" (the account is bound to someone else).
func (p *Platform) Redeem(ctx context.Context, code string, telegramID int64, username string) (uuid.UUID, string, error) {
	if !linkCodeRe.MatchString(code) || telegramID <= 0 {
		return uuid.Nil, "invalid", nil
	}
	if !tgNameRe.MatchString(username) {
		username = ""
	}
	sum := sha256.Sum256([]byte(code))
	var outcome string
	var u *uuid.UUID
	if err := p.Pool.QueryRow(ctx, `SELECT outcome, user_id FROM taskiem_tg_code_redeem($1, $2, $3)`, sum[:], telegramID, username).Scan(&outcome, &u); err != nil {
		return uuid.Nil, "", fmt.Errorf("telegram: redeeming a link code: %w", err)
	}
	if outcome != "ok" || u == nil {
		return uuid.Nil, outcome, nil
	}
	return *u, outcome, nil
}

// Binding is a person's linked account (TelegramID 0 when none) and when
// their pending code expires (nil when none).
type Binding struct {
	TelegramID     int64
	Username       string
	VerifiedAt     *time.Time
	PendingExpires *time.Time
}

// Binding reads a person's binding, for their account page.
func (p *Platform) Binding(ctx context.Context, user uuid.UUID) (Binding, error) {
	var b Binding
	var id *int64
	var name *string
	err := p.Pool.QueryRow(ctx, `SELECT telegram_id, username, verified_at, pending_expires_at FROM taskiem_tg_binding($1)`, user).
		Scan(&id, &name, &b.VerifiedAt, &b.PendingExpires)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, nil
	}
	if err != nil {
		return b, fmt.Errorf("telegram: reading a binding: %w", err)
	}
	if id != nil {
		b.TelegramID = *id
	}
	if name != nil {
		b.Username = *name
	}
	return b, nil
}

// Unbind removes a person's binding; it returns the account it held (0
// when none).
func (p *Platform) Unbind(ctx context.Context, user uuid.UUID) (int64, error) {
	var id *int64
	if err := p.Pool.QueryRow(ctx, `SELECT taskiem_tg_unbind($1)`, user).Scan(&id); err != nil {
		return 0, fmt.Errorf("telegram: unbinding: %w", err)
	}
	if id == nil {
		return 0, nil
	}
	return *id, nil
}
