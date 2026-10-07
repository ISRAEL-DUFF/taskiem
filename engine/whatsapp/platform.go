package whatsapp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/egress"
)

// Window is WhatsApp's customer service window: free-form messages may be
// sent for 24 hours after a person last wrote; after that, templates only.
const Window = 24 * time.Hour

// Config is the operator's platform number (docs/whatsapp.md). The
// credentials come from the environment (the deployment's Secret), never
// from chart values or the database.
type Config struct {
	PhoneNumberID string // TASKIEM_WHATSAPP_PHONE_NUMBER_ID
	AccessToken   string // TASKIEM_WHATSAPP_ACCESS_TOKEN
	AppSecret     string // TASKIEM_WHATSAPP_APP_SECRET: verifies webhook signatures
	VerifyToken   string // TASKIEM_WHATSAPP_VERIFY_TOKEN: the subscription handshake
	TokenKey      []byte // TASKIEM_WHATSAPP_TOKEN_KEY: signs decision tokens (32 bytes, base64)
	GraphURL      string // TASKIEM_WHATSAPP_GRAPH_URL: DefaultGraphURL unless overridden (tests)
	Language      string // TASKIEM_WHATSAPP_TEMPLATE_LANGUAGE: default "en"
	// DefaultCountry is the calling code for national numbers typed with
	// a leading 0 (TASKIEM_WHATSAPP_DEFAULT_COUNTRY, default 234).
	DefaultCountry string
	// DisplayNumber is the platform number as people should save it
	// (TASKIEM_WHATSAPP_DISPLAY_NUMBER), shown on the Account page.
	DisplayNumber string
}

// ConfigFromEnv reads the platform number's settings; nil when
// TASKIEM_WHATSAPP_PHONE_NUMBER_ID is unset (the channel is off).
func ConfigFromEnv(lookup func(string) (string, bool)) (*Config, error) {
	get := func(k string) string {
		v, _ := lookup(k)
		return strings.TrimSpace(v)
	}
	c := &Config{
		PhoneNumberID: get("TASKIEM_WHATSAPP_PHONE_NUMBER_ID"), AccessToken: get("TASKIEM_WHATSAPP_ACCESS_TOKEN"),
		AppSecret: get("TASKIEM_WHATSAPP_APP_SECRET"), VerifyToken: get("TASKIEM_WHATSAPP_VERIFY_TOKEN"),
		GraphURL: get("TASKIEM_WHATSAPP_GRAPH_URL"), Language: get("TASKIEM_WHATSAPP_TEMPLATE_LANGUAGE"),
		DefaultCountry: get("TASKIEM_WHATSAPP_DEFAULT_COUNTRY"), DisplayNumber: get("TASKIEM_WHATSAPP_DISPLAY_NUMBER"),
	}
	if c.PhoneNumberID == "" {
		return nil, nil
	}
	var missing []string
	for k, v := range map[string]string{"TASKIEM_WHATSAPP_ACCESS_TOKEN": c.AccessToken, "TASKIEM_WHATSAPP_APP_SECRET": c.AppSecret,
		"TASKIEM_WHATSAPP_VERIFY_TOKEN": c.VerifyToken, "TASKIEM_WHATSAPP_TOKEN_KEY": get("TASKIEM_WHATSAPP_TOKEN_KEY")} {
		if v == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("TASKIEM_WHATSAPP_PHONE_NUMBER_ID is set but %s is not", strings.Join(missing, ", "))
	}
	key, err := base64.StdEncoding.DecodeString(get("TASKIEM_WHATSAPP_TOKEN_KEY"))
	if err != nil || len(key) < 32 {
		return nil, errors.New("TASKIEM_WHATSAPP_TOKEN_KEY must be at least 32 bytes, base64 (openssl rand -base64 32)")
	}
	c.TokenKey = key
	return c, nil
}

// Platform is the platform number at work: sending (choosing text or a
// template by the window) and the person-level records behind it.
type Platform struct {
	Pool   *pgxpool.Pool
	Config Config
	Client *Client
	Signer Signer
	Logger *slog.Logger
	// Now is the clock (tests).
	Now func() time.Time
}

// New builds the platform from its configuration.
func New(pool *pgxpool.Pool, cfg Config, guard *egress.Guard, log *slog.Logger) *Platform {
	if cfg.Language == "" {
		cfg.Language = "en"
	}
	if cfg.DefaultCountry == "" {
		cfg.DefaultCountry = "234"
	}
	// An operator pointing the platform at a fake Graph API on this machine
	// (browser tests) reaches loopback, and nothing else private.
	if u, err := url.Parse(cfg.GraphURL); err == nil && cfg.GraphURL != "" {
		if ip, err := netip.ParseAddr(u.Hostname()); err == nil && ip.IsLoopback() {
			var log *slog.Logger
			if guard != nil {
				log = guard.Logger
			}
			guard = &egress.Guard{Logger: log, Blocked: func(a netip.Addr) bool { return !a.IsLoopback() && egress.BlockedAddr(a) }}
		}
	}
	return &Platform{Pool: pool, Config: cfg, Logger: log, Signer: Signer{Key: cfg.TokenKey},
		Client: &Client{BaseURL: cfg.GraphURL, PhoneNumberID: cfg.PhoneNumberID, Token: cfg.AccessToken, Egress: guard}}
}

func (p *Platform) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Message is something to send: Text (with optional Buttons) inside the
// window, otherwise Template with Vars (and quick-reply Payloads).
type Message struct {
	Text     string
	Buttons  []Button
	Template *Template
	Vars     map[string]string
	Payloads []string
}

// Send delivers m to a number: as text when the number wrote in the last
// 24 hours, else as its template. A message with no template can only be
// a reply, inside the window.
func (p *Platform) Send(ctx context.Context, number string, m Message) (string, error) {
	open := false
	if m.Text != "" {
		c, err := p.Contact(ctx, number)
		if err != nil {
			return "", err
		}
		open = c.LastInbound != nil && p.now().Sub(*c.LastInbound) < Window
	}
	if open {
		var id string
		var err error
		if len(m.Buttons) > 0 {
			id, err = p.Client.SendButtons(ctx, number, m.Text, m.Buttons)
		} else {
			id, err = p.Client.SendText(ctx, number, m.Text)
		}
		if !errors.Is(err, ErrWindowClosed) || m.Template == nil {
			return id, err
		}
	}
	if m.Template == nil {
		return "", ErrWindowClosed
	}
	return p.Client.SendTemplate(ctx, number, *m.Template, p.Config.Language, m.Vars, m.Payloads)
}

// Contact is a number's conversation.
type Contact struct {
	Tenant      *uuid.UUID // the tenant it speaks to now
	LastInbound *time.Time
}

// Contact reads a number's conversation.
func (p *Platform) Contact(ctx context.Context, number string) (Contact, error) {
	var c Contact
	err := p.Pool.QueryRow(ctx, `SELECT current_tenant, last_inbound_at FROM taskiem_wa_contact($1)`, number).Scan(&c.Tenant, &c.LastInbound)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, nil
	}
	return c, err
}

// Touch records an inbound message, and with setTenant the tenant the
// number now speaks to.
func (p *Platform) Touch(ctx context.Context, number string, inbound, setTenant bool, tenant *uuid.UUID) error {
	_, err := p.Pool.Exec(ctx, `SELECT taskiem_wa_contact_touch($1, $2, $3, $4)`, number, inbound, setTenant, tenant)
	return err
}

// ClaimInbound reports whether a message id is new (Meta retries).
func (p *Platform) ClaimInbound(ctx context.Context, id string) (bool, error) {
	var fresh bool
	err := p.Pool.QueryRow(ctx, `SELECT taskiem_wa_inbound_claim($1)`, id).Scan(&fresh)
	return fresh, err
}

// UserOf is the person a number is bound to, or uuid.Nil.
func (p *Platform) UserOf(ctx context.Context, number string) (uuid.UUID, error) {
	var u *uuid.UUID
	if err := p.Pool.QueryRow(ctx, `SELECT taskiem_wa_user_of($1)`, number).Scan(&u); err != nil {
		return uuid.Nil, err
	}
	if u == nil {
		return uuid.Nil, nil
	}
	return *u, nil
}

// Notify sends an alert to those of members who have bound a number, in
// the tenant's name (the WhatsApp alert channel).
func (p *Platform) Notify(ctx context.Context, tenant uuid.UUID, members []uuid.UUID, kind, title, body, link string, detail map[string]any) error {
	type dest struct {
		user   uuid.UUID
		number string
	}
	var name string
	var dests []dest
	err := db.InTenantTx(ctx, p.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&name); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT user_id, number FROM taskiem_wa_numbers($1)`, members)
		if err != nil {
			return err
		}
		dests, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (dest, error) {
			var d dest
			return d, r.Scan(&d.user, &d.number)
		})
		return err
	})
	if err != nil {
		return err
	}
	if len(dests) == 0 {
		return errors.New("none of the channel's members has bound a WhatsApp number")
	}
	m := AlertMessage(name, kind, title, body, link, detail)
	var errs []error
	for _, d := range dests {
		if _, err := p.Send(ctx, d.number, m); err != nil {
			errs = append(errs, fmt.Errorf("to %s: %w", MaskNumber(d.number), err))
		}
	}
	return errors.Join(errs...)
}

// AlertMessage is an alert as a WhatsApp message: text inside the window,
// the template for its kind outside it.
func AlertMessage(tenant, kind, title, body, link string, detail map[string]any) Message {
	str := func(k string) string {
		s, _ := detail[k].(string)
		if s == "" {
			return "-"
		}
		return s
	}
	if link == "" {
		link = "-"
	}
	text := "[" + tenant + "] " + SafeText(title) + "\n\n" + SafeText(body)
	if link != "-" {
		text += "\n\n" + link
	}
	tpl, vars := TplAlert, map[string]string{"tenant": tenant, "title": SafeText(title), "link": link}
	switch kind {
	case "run_failed":
		tpl, vars = TplRunFailed, map[string]string{"tenant": tenant, "workflow": str("workflow"), "environment": str("environment"), "link": link}
	case "run_completed":
		tpl, vars = TplRunCompleted, map[string]string{"tenant": tenant, "workflow": str("workflow"), "environment": str("environment"), "link": link}
	case "needs_reconciliation":
		tpl, vars = TplNeedsReconciliation, map[string]string{"tenant": tenant, "workflow": str("workflow"), "environment": str("environment"), "link": link}
	case "stuck_approval":
		tpl = TplApprovalWaiting
	}
	return Message{Text: text, Template: &tpl, Vars: vars}
}
