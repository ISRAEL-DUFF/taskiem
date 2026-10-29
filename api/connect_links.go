package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/telegram"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Connecting from chat (docs/spec/chat-way-in.md 7, decision 0058). Where
// chat sent a link to the web's Connections page, it now offers to
// connect from the chat: `connect <service>` sends a one-time connect
// link for one service, one business and the person who asked, from the
// chat that asked, good for 15 minutes. The link needs no web sign-in:
// its token is the authority, for that one connection only, and the
// person must still hold connection.manage when it is used. The page it
// opens is the web's guided form for a key (checked with the service
// before anything is saved), or starts the provider's consent for a
// service that signs in, with decision 0034's state, browser cookie and
// PKCE unchanged. On Telegram the page opens as a Mini App, and a key is
// taken only with Telegram's signed initData for the account the link was
// sent to. When the connection is made the link is spent, the chat is
// told, and the draft it was for carries on: what else it needs, or
// Publish it now?

const connectLinkTTL = 15 * time.Minute

// connectInitDataAge is how old a Mini App's initData may be.
const connectInitDataAge = 10 * time.Minute

// connectLink is a link's row.
type connectLink struct {
	Hash        []byte
	Tenant      uuid.UUID
	User        uuid.UUID
	Connector   string
	Environment string
	Channel     string
	Address     string
	Workflow    *uuid.UUID
	OAuthName   *string
	Expires     time.Time
	Spent       *time.Time
}

func connectTokenHash(tok string) []byte {
	h := sha256.Sum256([]byte(tok))
	return h[:]
}

var connectTokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{32}$`)

// newConnectLink makes a link for the chat's person to connect connector
// (a manifest id) in prod, carrying on with wf when it is set.
func (s *Server) newConnectLink(ctx context.Context, c *chat, connectorID string, wf *uuid.UUID) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := base64.RawURLEncoding.EncodeToString(b)
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO connect_links (token_hash, tenant_id, user_id, connector, environment, channel, address, workflow_id, expires_at)
			VALUES ($1, $2, $3, $4, 'prod', $5, $6, $7, now() + $8::interval)`, connectTokenHash(tok), c.tenant.ID, c.p.UserID, connectorID, c.ch.Name(), c.addr, wf, connectLinkTTL.String())
		if err != nil {
			return err
		}
		return waAudit(ctx, tx, c.ch.Name(), c.p, "connection.link_sent", connectorID, map[string]any{"minutes": int(connectLinkTTL.Minutes())})
	})
	return s.PublicURL + "/c/" + tok, err
}

// errConnectLinkGone: a link that is unknown, spent or past its 15
// minutes; not told apart.
var errConnectLinkGone = fmt.Errorf("%w: this link has been used or has expired: ask for a new one in the chat (send connect and the service)", errGone)

// openConnectLink finds a live link and what it is for: the person (who
// must still hold connection.manage), the connector and the business.
func (s *Server) openConnectLink(ctx context.Context, tok string) (*connectLink, *Principal, *connector.Connector, string, error) {
	if !connectTokenRe.MatchString(tok) {
		return nil, nil, nil, "", errConnectLinkGone
	}
	hash := connectTokenHash(tok)
	var tenant *uuid.UUID
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_connect_link_tenant($1)`, hash).Scan(&tenant); err != nil {
		return nil, nil, nil, "", err
	}
	if tenant == nil {
		return nil, nil, nil, "", errConnectLinkGone
	}
	l := &connectLink{Hash: hash, Tenant: *tenant}
	var business string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{*tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT l.user_id, l.connector, l.environment, l.channel, l.address, l.workflow_id, l.oauth_name, l.expires_at, l.spent_at, t.name
			FROM connect_links l JOIN tenants t ON t.id = l.tenant_id WHERE l.token_hash = $1`, hash).
			Scan(&l.User, &l.Connector, &l.Environment, &l.Channel, &l.Address, &l.Workflow, &l.OAuthName, &l.Expires, &l.Spent, &business)
	})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (l.Spent != nil || !time.Now().Before(l.Expires)) {
		return nil, nil, nil, "", errConnectLinkGone
	}
	if err != nil {
		return nil, nil, nil, "", err
	}
	p, err := s.principalOf(ctx, l.Tenant, l.User)
	if err != nil || !p.Can(PermConnectionManage) {
		return nil, nil, nil, "", fmt.Errorf("%w: you can no longer connect services for %s", errForbidden, business)
	}
	reg, err := s.Registry.For(ctx, l.Tenant.String())
	if err != nil {
		return nil, nil, nil, "", err
	}
	c, ok := connectorByID(reg, l.Connector)
	if !ok {
		return nil, nil, nil, "", fmt.Errorf("%w: %s is no longer available", errGone, l.Connector)
	}
	return l, p, c, business, nil
}

// connectLimited paces the connect page per address and per link.
func (s *Server) connectLimited(w http.ResponseWriter, r *http.Request) bool {
	tok := chi.URLParam(r, "token")
	if !s.limiter("connect-ip:"+clientIP(r), time.Minute, 30).Allow() || !s.limiter("connect-tok:"+tok, time.Minute, 12).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many tries: wait a minute")
		return true
	}
	return false
}

// connectBack is where the page sends the person back to: their chat.
func (s *Server) connectBack(channel string) string {
	switch {
	case channel == "whatsapp" && s.WhatsApp != nil:
		if n := strings.Trim(strings.ReplaceAll(s.WhatsApp.Config.DisplayNumber, " ", ""), "+"); n != "" {
			return "https://wa.me/" + n
		}
	case channel == "telegram" && s.Telegram != nil && s.Telegram.Config.BotUsername != "":
		return "https://t.me/" + s.Telegram.Config.BotUsername
	}
	return ""
}

// getConnectLink answers GET /v1/connect/{token}: what the link connects,
// to which business, until when.
func (s *Server) getConnectLink(w http.ResponseWriter, r *http.Request) {
	if s.connectLimited(w, r) {
		return
	}
	l, _, c, business, err := s.openConnectLink(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	m := c.Manifest
	info := connectorInfo{Ref: c.Ref(), ID: m.ID, Version: m.Version, Name: m.Name, Kind: m.KindOf(), Category: m.Category, Description: m.Description, Auth: m.Auth,
		Actions: map[string]connectorAction{}, Triggers: []string{}, OAuthLinkable: s.oauthLinkable(c), OAuthModes: s.oauthModes(c)}
	writeJSON(w, http.StatusOK, map[string]any{"business": business, "connector": info, "channel": l.Channel, "expires_at": l.Expires.UTC(),
		"back": s.connectBack(l.Channel), "mini_app": l.Channel == "telegram"})
}

// connectWithKey answers POST /v1/connect/{token}: the key form. On
// Telegram, the Mini App's initData must be the account the link was sent
// to. The link is spent before the key is checked, and given back if it
// is refused, so it makes one connection however often it is sent.
func (s *Server) connectWithKey(w http.ResponseWriter, r *http.Request) {
	if s.connectLimited(w, r) {
		return
	}
	var req struct {
		Credentials map[string]string `json:"credentials"`
		Mode        string            `json:"mode,omitempty"`
		InitData    string            `json:"init_data,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	l, p, c, business, err := s.openConnectLink(ctx, chi.URLParam(r, "token"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.connectMiniApp(l, req.InitData); err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.spendConnectLink(ctx, l, true) {
		s.fail(w, r, errConnectLinkGone)
		return
	}
	id, name, err := s.saveKeyConnection(ctx, p, l.Environment, c, "", req.Credentials, req.Mode, true)
	if err != nil {
		s.spendConnectLink(ctx, l, false)
		s.fail(w, r, err)
		return
	}
	s.connectMade(ctx, l, p, c, id, name, "connect_link")
	writeJSON(w, http.StatusCreated, map[string]any{"connected": c.Manifest.Name, "business": business, "name": name, "back": s.connectBack(l.Channel)})
}

// connectMiniApp checks a Telegram link's key form came from the Mini
// App opened by the account the link was sent to.
func (s *Server) connectMiniApp(l *connectLink, initData string) error {
	if l.Channel != "telegram" {
		return nil
	}
	if s.Telegram == nil {
		return fmt.Errorf("%w: Telegram is off on this Taskiem", errConflict)
	}
	id, err := telegram.VerifyInitData(s.Telegram.Config.BotToken, initData, connectInitDataAge, time.Now())
	if err != nil || telegram.Address(id) != l.Address {
		return fmt.Errorf("%w: open this form from the button in your Telegram chat with Taskiem", errForbidden)
	}
	return nil
}

// spendConnectLink marks a link used (spend), or gives it back (a
// refused key, a consent not finished): false when it was not live.
func (s *Server) spendConnectLink(ctx context.Context, l *connectLink, spend bool) bool {
	q := `UPDATE connect_links SET spent_at = now() WHERE token_hash = $1 AND spent_at IS NULL AND expires_at > now()`
	if !spend {
		q = `UPDATE connect_links SET spent_at = NULL WHERE token_hash = $1 AND connection_id IS NULL`
	}
	var n int64
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{l.Tenant}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, q, l.Hash)
		n = tag.RowsAffected()
		return err
	})
	if err != nil {
		s.Logger.Error("connect links: marking a link", "err", err)
		return false
	}
	return n == 1
}

// connectWithOAuth answers POST /v1/connect/{token}/oauth: the provider's
// consent URL, with this browser bound to the request as on the web. The
// link stays live until the callback makes the connection.
func (s *Server) connectWithOAuth(w http.ResponseWriter, r *http.Request) {
	if s.connectLimited(w, r) {
		return
	}
	var req struct {
		Mode string `json:"mode,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	ctx := r.Context()
	l, p, c, _, err := s.openConnectLink(ctx, chi.URLParam(r, "token"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if c.Manifest.Auth.OAuth2 == nil {
		s.fail(w, r, fmt.Errorf("%w: %s is connected with its keys, not by signing in", errBadRequest, c.Manifest.Name))
		return
	}
	consent, browser, name, err := s.beginOAuthConnection(ctx, p, c, l.Environment, "", req.Mode)
	if err != nil {
		s.failOAuth(w, r, err)
		return
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{l.Tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE connect_links SET oauth_name = $2, oauth_started_at = now() WHERE token_hash = $1`, l.Hash, name)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	s.setOAuthBrowser(w, browser)
	writeJSON(w, http.StatusOK, map[string]any{"url": consent, "expires_in": int(secrets.OAuthRequestTTL.Seconds())})
}

// oauthLinkOf is the live connect link an OAuth request was started from,
// if any: the same business, connector, environment and connection name.
func (s *Server) oauthLinkOf(ctx context.Context, req *secrets.OAuthRequest, c *connector.Connector) *connectLink {
	l := &connectLink{Tenant: req.Tenant}
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{req.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT token_hash, user_id, connector, environment, channel, address, workflow_id, expires_at FROM connect_links
			WHERE connector = $1 AND environment = $2 AND oauth_name = $3 AND spent_at IS NULL AND expires_at > now() - $4::interval
			ORDER BY oauth_started_at DESC LIMIT 1`, c.Manifest.ID, req.Environment, req.Name, secrets.OAuthRequestTTL.String()).
			Scan(&l.Hash, &l.User, &l.Connector, &l.Environment, &l.Channel, &l.Address, &l.Workflow, &l.Expires)
	})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			s.Logger.Warn("connect links: finding an OAuth request's link", "err", err)
		}
		return nil
	}
	return l
}

// connectDone sends the browser that used a connect link to the page that
// says how it went, with the way back to the chat.
func (s *Server) connectDone(w http.ResponseWriter, r *http.Request, l *connectLink, service string, err error) {
	q := url.Values{"service": {service}}
	if back := s.connectBack(l.Channel); back != "" {
		q.Set("back", back)
	}
	if err != nil {
		s.Logger.Warn("oauth connection from a connect link refused", "err", err, "ip", clientIP(r))
		msg := "the connection could not be saved; try again from the link in your chat"
		for _, known := range []error{secrets.ErrOAuthRequest, errBadRequest, errConflict, errOAuthUnavailable, errOAuthProvider, errOAuthGrant} {
			if errors.Is(err, known) {
				msg = err.Error()
			}
		}
		for _, p := range []string{"bad request: ", "conflict: ", "oauth unavailable: ", "provider: ", "grant: "} {
			msg = strings.TrimPrefix(msg, p)
		}
		q.Set("error", strings.ToUpper(msg[:1])+msg[1:])
	} else {
		q.Set("connected", "1")
	}
	http.Redirect(w, r, s.PublicURL+"/c/done?"+q.Encode(), http.StatusFound)
}

// connectMade records a connection made from a connect link (or the
// WhatsApp key form, via form), spends the link, and tells the chat in
// the background, carrying on with the draft it was for.
func (s *Server) connectMade(ctx context.Context, l *connectLink, p *Principal, c *connector.Connector, id uuid.UUID, name, via string) {
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{l.Tenant}, func(tx pgx.Tx) error {
		if l.Hash != nil {
			if _, err := tx.Exec(ctx, `UPDATE connect_links SET spent_at = COALESCE(spent_at, now()), connection_id = $2 WHERE token_hash = $1`, l.Hash, id); err != nil {
				return err
			}
		}
		return waAudit(ctx, tx, l.Channel, p, "connection.from_chat", id.String(), map[string]any{"connector": c.Manifest.ID, "name": name, "via": via})
	})
	if err != nil {
		s.Logger.Error("connect links: recording a connection", "err", err)
	}
	link := *l
	service := c.Manifest.Name
	s.background(ctx, "connected from chat", func(ctx context.Context) { s.connectTell(ctx, &link, service) })
}

// connectTell tells the chat the connection is made and, when the link
// was for a draft, carries on with it: what it still needs, or publishing.
func (s *Server) connectTell(ctx context.Context, l *connectLink, service string) {
	ch, err := s.chatChannelFor(ctx, l.Channel, l.Tenant)
	if err != nil {
		return
	}
	var business string
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{l.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, l.Tenant).Scan(&business)
	}); err != nil {
		return
	}
	c := &chat{ch: ch, addr: l.Address, tenant: tenantRef{ID: l.Tenant, Name: business}}
	if wa, ok := ch.(*whatsapp.Platform); ok {
		c.wa = wa
	}
	if c.p, err = s.principalOf(ctx, l.Tenant, l.User); err != nil {
		return
	}
	c.lang = s.chooseLang(ctx, l.Tenant, l.Address, "")
	msg := c.t("connect.done", "service", whatsapp.SafeText(service), "org", whatsapp.SafeText(business))
	if l.Workflow != nil {
		// The conversation turns to this business, for the answer.
		if err := ch.Touch(ctx, l.Address, false, true, &l.Tenant); err != nil {
			s.Logger.Warn("connect links: turning the chat to the business", "err", err)
		}
		b := &waBuild{Workflow: *l.Workflow}
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{l.Tenant}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT w.name, (SELECT max(version) FROM workflow_versions WHERE workflow_id = w.id) FROM workflows w WHERE w.id = $1`, b.Workflow).
				Scan(&b.Name, &b.Version)
		}); err == nil {
			if next, err := s.waBuildNext(ctx, c, b); err == nil {
				msg += "\n\n" + c.t("connect.carry_on", "name", whatsapp.SafeText(b.Name)) + "\n" + next
			}
		}
	}
	if err := c.say(ctx, s, msg); err != nil {
		s.Logger.Warn("connect links: telling the chat", "err", err)
	}
}

// --- in the chat ---

// waConnect answers "connect <service>": a connect link for it (or, on
// WhatsApp for a key that fits, the key form), carrying on with the draft
// that was waiting for it.
func (s *Server) waConnect(ctx context.Context, c *chat, name string) error {
	if !c.p.Can(PermConnectionManage) {
		return c.say(ctx, s, c.t("connect.denied", "org", c.tenant.Name))
	}
	reg, err := s.Registry.For(ctx, c.tenant.ID.String())
	if err != nil {
		return err
	}
	conn, ok := connectorNamed(reg.List(), name)
	if !ok {
		return c.say(ctx, s, c.t("connect.unknown", "service", whatsapp.SafeText(name)))
	}
	var wf *uuid.UUID
	if c.blocked != uuid.Nil {
		wf = &c.blocked
	}
	if sent, err := s.waSendConnectFlow(ctx, c, conn, wf); sent || err != nil {
		return err
	}
	link, err := s.newConnectLink(ctx, c, conn.Manifest.ID, wf)
	if err != nil {
		return err
	}
	service := whatsapp.SafeText(conn.Manifest.Name)
	if c.ch.Name() != "telegram" {
		return c.say(ctx, s, c.t("connect.link", "service", service, "link", link, "minutes", "15"))
	}
	// On Telegram the page opens inside the chat as a Mini App, which signs
	// who opened it (so the link is on the button only); a service that
	// signs in goes to the browser.
	_, err = c.ch.Send(ctx, c.addr, whatsapp.Message{Text: c.ch.Prefix(c.tenant.Name) + c.t("connect.link_button", "service", service, "minutes", "15"),
		Link: &whatsapp.LinkButton{Title: c.t("connect.button", "service", service), URL: link, WebApp: conn.Manifest.Auth.OAuth2 == nil}})
	return err
}

// connectorNamed finds a connector by its id or its name, in any case
// and spacing ("connect paystack", "connect Google Sheets").
func connectorNamed(list []*connector.Connector, name string) (*connector.Connector, bool) {
	norm := func(v string) string { return strings.ToLower(strings.Join(strings.Fields(v), "")) }
	want := norm(name)
	if want == "" {
		return nil, false
	}
	i := slices.IndexFunc(list, func(c *connector.Connector) bool {
		return norm(c.Manifest.ID) == want || norm(c.Manifest.Name) == want
	})
	if i < 0 {
		return nil, false
	}
	return list[i], true
}

// connectWaiting records, for the next message, the draft that is waiting
// for a connection: "connect <service>" then carries on with it.
func (s *Server) connectWaiting(ctx context.Context, c *chat, b *waBuild) error {
	return s.saveSession(ctx, c, stateCollecting, chatData{Build: &waBuild{Phase: buildBlocked, Workflow: b.Workflow, Version: b.Version, Name: b.Name}}, waPendingTTL)
}

// --- the pasted-key guard ---

// tokenShaped is a message that could be a key with no prefix: one word,
// 20 characters or more, of letters, digits, - and _ only.
var tokenShaped = regexp.MustCompile(`^[A-Za-z0-9_-]{20,}$`)

// keyInChat reports whether a message carries a key: a value starting the
// way a connector's secret does (sk_live_...), or, while a connect link is
// waiting for this chat, any token-shaped message. Such a message is never
// acted on or kept anywhere: the person is told to use the form and to
// replace the key, since the chat app has it now.
func (s *Server) keyInChat(ctx context.Context, tenant uuid.UUID, addr, text string) (bool, error) {
	text = strings.TrimSpace(text)
	if len(text) < 8 {
		return false, nil
	}
	who := ""
	if tenant != uuid.Nil {
		who = tenant.String()
	}
	reg, err := s.Registry.For(ctx, who)
	if err != nil {
		return false, err
	}
	for _, w := range strings.Fields(text) {
		w = strings.Trim(w, `"'.,;:()[]<>`)
		for _, c := range reg.List() {
			for _, f := range c.Manifest.Auth.Fields {
				if !f.Secret {
					continue
				}
				for _, p := range f.Prefixes {
					if len(p.Prefix) >= 3 && strings.HasPrefix(w, p.Prefix) && len(w) >= len(p.Prefix)+8 {
						return true, nil
					}
				}
			}
		}
	}
	if !tokenShaped.MatchString(text) {
		return false, nil
	}
	var pending bool
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_connect_pending($1)`, addr).Scan(&pending); err != nil {
		return false, err
	}
	return pending, nil
}

// keyGuard answers a message carrying a key (keyInChat) and drops it:
// true when it did. The registry is the first business's, or the
// platform's for someone in none.
func (s *Server) keyGuard(ctx context.Context, c *chat, text string) (bool, error) {
	if strings.TrimSpace(text) == "" {
		return false, nil
	}
	tenant := c.tenant.ID
	if tenant == uuid.Nil && len(c.tenants) > 0 {
		tenant = c.tenants[0].ID
	}
	key, err := s.keyInChat(ctx, tenant, c.addr, text)
	if err != nil || !key {
		return false, err
	}
	s.Logger.Info("chat: a key was sent as a message and dropped", "channel", c.ch.Name())
	return true, s.waSay(ctx, c.ch, c.addr, "", c.t("connect.key_in_chat"))
}

// connectFormFields are a connector's fields as the WhatsApp key form
// holds them: false when it does not fit (a service that signs in, a
// mode to choose by host, a value spanning lines or made for the person,
// more than five fields).
func connectFormFields(c *connector.Connector) ([]whatsapp.ConnectField, bool) {
	a := c.Manifest.Auth
	if a.OAuth2 != nil || len(a.HostModes) > 0 || len(a.Fields) == 0 || len(a.Fields) > whatsapp.ConnectSlots {
		return nil, false
	}
	var out []whatsapp.ConnectField
	for _, f := range a.Fields {
		if f.Multiline || f.Generate != "" {
			return nil, false
		}
		help := f.Help
		if f.Where != "" {
			help = strings.TrimSpace(help + " Find it in " + f.Where + ".")
		}
		out = append(out, whatsapp.ConnectField{Key: f.Key, Label: f.Label, Help: help, Secret: f.Secret, Required: f.IsRequired()})
	}
	return out, true
}

// waSendConnectFlow sends the WhatsApp key form for a key-based service
// that fits it, when Flows are set up; false leaves connecting to the
// connect link.
func (s *Server) waSendConnectFlow(ctx context.Context, c *chat, conn *connector.Connector, wf *uuid.UUID) (bool, error) {
	if c.wa == nil || c.wa.Config.FlowKey == nil {
		return false, nil
	}
	fields, ok := connectFormFields(conn)
	if !ok {
		return false, nil
	}
	title := c.t("connect.button", "service", conn.Manifest.Name)
	intro := c.t("connect.form_intro", "service", conn.Manifest.Name)
	form, err := whatsapp.ConnectForm(title, intro, fields, nil, "")
	if err != nil {
		return false, nil //nolint:nilerr // does not fit: the connect link instead
	}
	tok, hash := whatsapp.NewFlowToken(c.tenant.ID)
	meta := map[string]any{"title": title, "intro": intro, "connector": conn.Manifest.ID}
	if wf != nil {
		meta["workflow"] = wf.String()
	}
	raw, _ := json.Marshal(meta)
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO whatsapp_flows (token_hash, tenant_id, kind, number, phone_number_id, user_id, data, expires_at)
			VALUES ($1, $2, 'connect', $3, $4, $5, $6, now() + $7::interval)`, hash, c.tenant.ID, c.addr, c.wa.Config.PhoneNumberID, c.p.UserID, raw, connectLinkTTL.String())
		return err
	}); err != nil {
		return false, err
	}
	_, err = c.wa.Client.SendFlow(ctx, c.addr, whatsapp.FlowMessage{Flow: whatsapp.FlowConnect, Token: tok, CTA: c.t("connect.form_cta"), Screen: whatsapp.ScreenConnect,
		Data: form, Body: c.wa.Prefix(c.tenant.Name) + c.t("connect.form", "service", whatsapp.SafeText(conn.Manifest.Name))})
	if err != nil {
		s.Logger.Warn("whatsapp: sending a key form", "err", err)
		s.waDropFlow(ctx, c.tenant.ID, hex.EncodeToString(hash))
		return false, nil
	}
	return true, nil
}

// waFlowConnect answers the key form: its screen on opening; on
// submission the values are checked (their starts, then with the
// service) and the connection saved as the person the form was sent to,
// who must still manage connections. A refused value comes back in the
// form, in plain words.
func (s *Server) waFlowConnect(ctx context.Context, row flowRow, req whatsapp.FlowRequest) any {
	title, _ := row.data["title"].(string)
	intro, _ := row.data["intro"].(string)
	id, _ := row.data["connector"].(string)
	var fields []whatsapp.ConnectField
	var conn *connector.Connector
	if reg, err := s.Registry.For(ctx, row.tenant.String()); err == nil {
		if c, ok := connectorByID(reg, id); ok {
			conn = c
			fields, _ = connectFormFields(c)
		}
	}
	screen := func(errs map[string]string, problem string) any {
		d, _ := whatsapp.ConnectForm(title, intro, fields, errs, problem)
		return flowScreen(whatsapp.ScreenConnect, d)
	}
	finish := func(status, outcome string) any {
		if err := s.setFlow(ctx, row, status, map[string]any{"title": title, "intro": intro, "connector": id, "outcome": outcome}); err != nil {
			s.Logger.Error("whatsapp: closing a key form", "err", err)
		}
		if status == "done" {
			return flowDone(req.FlowToken)
		}
		return screen(nil, outcome)
	}
	if conn == nil || row.user == nil {
		return finish("refused", "This service is no longer available.")
	}
	if req.Action != "data_exchange" {
		return screen(nil, "")
	}
	p, err := s.principalOf(ctx, row.tenant, *row.user)
	if err != nil || !p.Can(PermConnectionManage) {
		return finish("refused", "You can no longer connect services for this business.")
	}
	creds := whatsapp.ConnectValues(fields, req.Data)
	errs := map[string]string{}
	for _, f := range conn.Manifest.Auth.Fields {
		if err := f.CheckValue(creds[f.Key], ""); err != nil {
			msg := err.Error()
			errs[f.Key] = strings.ToUpper(msg[:1]) + msg[1:] + "."
		}
	}
	if len(errs) > 0 {
		return screen(errs, "")
	}
	cid, name, err := s.saveKeyConnection(ctx, p, "prod", conn, "", creds, "", true)
	if err != nil {
		msg := plainError(err)
		return screen(nil, strings.ToUpper(msg[:1])+msg[1:])
	}
	link := &connectLink{Tenant: row.tenant, User: *row.user, Connector: id, Environment: "prod", Channel: "whatsapp", Address: row.number}
	if w, _ := row.data["workflow"].(string); w != "" {
		if u, err := uuid.Parse(w); err == nil {
			link.Workflow = &u
		}
	}
	// The chat hears when the form closes (waFlowCompleted) and carries on.
	s.connectMade(ctx, link, p, conn, cid, name, "form")
	return finish("done", conn.Manifest.Name+" is connected.")
}
