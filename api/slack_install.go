package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Signing up from Slack (docs/spec/chat-way-in.md 9, decision 0058). Add
// to Slack on Taskiem's website installs Taskiem's app in a workspace
// with no business behind it: GET /v1/slack/install records a request
// that is the platform's, not a tenant's (its state bound to the browser
// by a cookie, and a PKCE verifier sealed with the platform's key, for ten
// minutes, once), and sends the browser to Slack. Slack sends it back to
// /v1/slack/install/callback with a code, exchanged for the workspace's
// bot token. The token waits a day, sealed, for the installer to make a
// business; the bot writes to them in Slack with the same steps as
// WhatsApp's (the terms, the business's name with the workspace's
// offered, what they sell and how they are paid; their name is Slack's),
// and the business is made with that token as its Slack connection. A
// token no business took is given back to Slack. The installer's email
// is Slack's record, never taken as confirmed and never matched to
// someone's account. A workspace that is already a business's gets its
// connections the new token, and the installer is told whose it is.

const (
	slackInstallCallbackPath = "/v1/slack/install/callback"
	// slackInstallCookie binds an install to the browser that started it,
	// as oauthBrowserCookie does a tenant's linking.
	slackInstallCookie = "taskiem_slack_install"
	// slackInstallTTL is how long an install waits for a business.
	slackInstallTTL = 24 * time.Hour
)

var slackIDRe = regexp.MustCompile(`^[A-Z0-9]{1,24}$`)

// slackSignup is who signs up from Slack and the token their business's
// Slack connection takes: an install's, or, for a linked person making
// another business, their business's in that workspace.
type slackSignup struct {
	team, user string
	teamName   string
	person     string // the installer's name, from Slack
	email      string // Slack's record of the installer's email
	token      string
	// install: the token is an install's waiting for a business.
	install bool
	// linked are the businesses the person is in here, whose connection
	// lends its token.
	linked []uuid.UUID
	// kept: the install's token is held (by the install record or a
	// business's connection), so a later failure does not give it back.
	kept bool
}

// slackInstallOn reports whether Add to Slack makes businesses here: the
// Slack app, sign-up from Slack turned on with terms to agree to, and
// Taskiem registered with Slack at a public address.
func (s *Server) slackInstallOn() bool {
	return s.Slack != nil && s.PublicURL != "" && s.Terms.On() && slices.Contains(s.ChatSignup.Channels, "slack") && s.OAuthClients["slack"].ID != ""
}

func slackInstallState(state, browser string) []byte {
	h := sha256.Sum256([]byte("slack-install:" + state + "\x00" + browser))
	return h[:]
}

func randomURLToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// slackInstallStart begins Add to Slack: GET /v1/slack/install.
func (s *Server) slackInstallStart(w http.ResponseWriter, r *http.Request) {
	if !s.slackInstallOn() {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if !s.limiter("slack-install:"+clientIP(r), time.Minute, 10).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many requests; try again in a minute")
		return
	}
	ctx := r.Context()
	c, ok := s.Registry.Get(slackConnector)
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	prov, err := s.oauthFor(c, "")
	if err != nil {
		s.failOAuth(w, r, err)
		return
	}
	var state, browser, verifier string
	for _, p := range []*string{&state, &browser, &verifier} {
		if *p, err = randomURLToken(); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	hash := slackInstallState(state, browser)
	sealed, key, err := s.Vault.SealPlatform(ctx, "slack-install-request:"+hex.EncodeToString(hash), []byte(verifier))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_slack_install_request_put($1, $2, $3, $4::interval)`, hash, sealed, key, secrets.OAuthRequestTTL.String()); err != nil {
		s.fail(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: slackInstallCookie, Value: browser, Path: slackInstallCallbackPath, MaxAge: int(secrets.OAuthRequestTTL.Seconds()), //nolint:gosec // Secure is TASKIEM_SECURE_COOKIES (on by default), as for the session cookie
		HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteLaxMode})
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"client_id":             {prov.client.ID},
		"redirect_uri":          {s.PublicURL + slackInstallCallbackPath},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		"scope":                 {strings.Join(c.Manifest.Auth.OAuth2.Scopes, c.Manifest.Auth.OAuth2.ScopeSeparator())},
	}
	http.Redirect(w, r, prov.authorize+"?"+q.Encode(), http.StatusFound)
}

// slackInstallCallback is where Slack sends the browser back. Whatever
// happens, the browser ends in Slack, at the bot's messages, or on the
// web page that says what went wrong.
func (s *Server) slackInstallCallback(w http.ResponseWriter, r *http.Request) {
	if !s.slackInstallOn() {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	ctx, q := r.Context(), r.URL.Query()
	cookie, _ := r.Cookie(slackInstallCookie)
	http.SetCookie(w, &http.Cookie{Name: slackInstallCookie, Value: "", Path: slackInstallCallbackPath, MaxAge: -1, HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteLaxMode}) //nolint:gosec // as when it is set
	fail := func(msg string, err error) {
		if err != nil {
			s.Logger.Warn("slack install refused", "err", err, "ip", clientIP(r))
		}
		http.Redirect(w, r, s.PublicURL+"/slack/added?"+url.Values{"error": {msg}}.Encode(), http.StatusFound)
	}
	if cookie == nil {
		fail("Finish adding Taskiem in the browser where you started. Start again from Taskiem's website.", nil)
		return
	}
	hash := slackInstallState(q.Get("state"), cookie.Value)
	var sealed, key string
	err := s.Store.Pool.QueryRow(ctx, `SELECT verifier, kms_key FROM taskiem_slack_install_request_take($1)`, hash).Scan(&sealed, &key)
	if errors.Is(err, pgx.ErrNoRows) {
		fail("This request is unknown, already used or expired. Start again from Taskiem's website.", nil)
		return
	}
	if err != nil {
		fail("Taskiem could not finish adding itself. Try again.", err)
		return
	}
	if e := q.Get("error"); e != "" {
		if e == "access_denied" {
			fail("You did not add Taskiem to Slack.", nil)
		} else {
			fail("Slack refused the request ("+e+").", nil)
		}
		return
	}
	verifier, err := s.Vault.OpenPlatform(ctx, "slack-install-request:"+hex.EncodeToString(hash), key, sealed)
	if err != nil {
		fail("Taskiem could not finish adding itself. Try again.", err)
		return
	}
	c, ok := s.Registry.Get(slackConnector)
	if !ok {
		fail("Taskiem could not finish adding itself. Try again.", errors.New("no Slack connector"))
		return
	}
	prov, err := s.oauthFor(c, "")
	if err != nil {
		fail("Taskiem cannot be added to Slack here right now.", err)
		return
	}
	prov.redirect = s.PublicURL + slackInstallCallbackPath
	answer, err := s.oauthExchange(ctx, uuid.Nil, c, prov, q.Get("code"), string(verifier))
	if err != nil {
		fail("Slack did not finish the install. Try again.", err)
		return
	}
	token := asString(answer["access_token"])
	team, _ := answer["team"].(map[string]any)
	who, _ := answer["authed_user"].(map[string]any)
	ss := &slackSignup{team: asString(team["id"]), teamName: clip(strings.TrimSpace(asString(team["name"])), 200), user: asString(who["id"]), token: token, install: true}
	app := asString(answer["app_id"])
	if !slackIDRe.MatchString(ss.team) || !slackIDRe.MatchString(ss.user) || !strings.HasPrefix(token, "xoxb-") {
		s.oauthRevoke(ctx, uuid.Nil, c, prov, token)
		fail("Slack's answer was not what Taskiem expected. Try again.", fmt.Errorf("slack install: team %q, user %q", ss.team, ss.user))
		return
	}
	if err := s.slackInstalled(ctx, ss, app); err != nil {
		if !ss.kept {
			s.oauthRevoke(ctx, uuid.Nil, c, prov, token)
		}
		fail("Taskiem was added, but could not write to you in Slack. Try again.", err)
		return
	}
	if slackIDRe.MatchString(app) {
		// Slack opens at the bot's messages, where the sign-up waits.
		http.Redirect(w, r, "https://slack.com/app_redirect?"+url.Values{"app": {app}, "team": {ss.team}}.Encode(), http.StatusFound)
		return
	}
	http.Redirect(w, r, s.PublicURL+"/slack/added", http.StatusFound)
}

// slackInstalled keeps an install and writes to its installer: the
// sign-up's first step, or, in a workspace that is already a business's,
// whose it is.
func (s *Server) slackInstalled(ctx context.Context, ss *slackSignup, app string) error {
	// Every connection of a known workspace runs on Taskiem's one app
	// there: each takes the token this install returned (decision 0051).
	rows, err := s.Store.Pool.Query(ctx, `SELECT tenant_id, name, connection_id FROM taskiem_slack_team_businesses($1)`, ss.team)
	if err != nil {
		return err
	}
	type known struct {
		tenant uuid.UUID
		name   string
		conn   uuid.UUID
	}
	businesses, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (known, error) {
		var k known
		return k, r.Scan(&k.tenant, &k.name, &k.conn)
	})
	if err != nil {
		return err
	}
	var names []string
	for _, b := range businesses {
		if !slices.Contains(names, b.name) {
			names = append(names, b.name)
		}
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{b.tenant}, func(tx pgx.Tx) error {
			creds, err := s.Vault.ConnectionCredentialsTx(ctx, tx, b.tenant, b.conn)
			if err != nil {
				return err
			}
			creds["bot_token"], creds["team_name"] = ss.token, ss.teamName
			return s.Vault.ReplaceCredentialsTx(ctx, tx, b.tenant, b.conn, creds, "system")
		}); err != nil {
			s.Logger.Warn("slack install: renewing a connection's token", "tenant", b.tenant, "err", err)
		}
	}
	shared := len(businesses) > 0
	ss.kept = shared
	ss.person, ss.email = s.slackPerson(ctx, ss.token, ss.user)
	sealed, key, err := s.Vault.SealPlatform(ctx, "slack-install:"+ss.team, []byte(ss.token))
	if err != nil {
		return err
	}
	if _, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_slack_install_put($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::interval)`,
		ss.team, ss.teamName, ss.user, app, sealed, key, ss.person, ss.email, shared, slackInstallTTL.String()); err != nil {
		return err
	}
	ss.kept = true
	ch := &slackInstallChannel{s: s, team: ss.team, token: ss.token}
	addr := slackAddr(ss.team, ss.user)
	sc := &signupChat{ch: ch, addr: addr, lang: s.chooseLang(ctx, uuid.Nil, addr, "").Tag, slack: ss}
	var linked int
	if err := s.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM taskiem_slack_user_of($1, $2)`, ss.team, ss.user).Scan(&linked); err != nil {
		return err
	}
	start := whatsapp.Button{ID: "su:start", Title: sc.t("slack.install.button_separate")}
	switch {
	case linked > 0:
		// Already in a business here: the conversation carries on.
		return s.suSay(ctx, sc, sc.t("slack.install.back", "workspace", ss.teamName))
	case shared:
		// Someone's business already: a separate one only if they choose.
		return s.suSay(ctx, sc, sc.t("slack.install.known", "workspace", ss.teamName, "businesses", strings.Join(names, ", ")), start)
	}
	if ss.email != "" {
		var taken bool
		err := s.Store.Pool.QueryRow(ctx, `SELECT user_id FROM taskiem_auth_find_user($1)`, ss.email).Scan(new(uuid.UUID))
		switch {
		case err == nil:
			taken = true
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		if taken {
			// Never merged: they link that account, or start under Slack.
			start.Title = sc.t("slack.install.button_new")
			return s.suSay(ctx, sc, sc.t("slack.install.email_taken", "email", ss.email, "where", s.accountWhere()), start)
		}
	}
	if err := s.suSay(ctx, sc, sc.t("slack.install.welcome", "workspace", ss.teamName)); err != nil {
		return err
	}
	return s.signupBegin(ctx, sc)
}

// slackPerson is a Slack user's name and email, as the workspace has
// them; empty when Slack does not say.
func (s *Server) slackPerson(ctx context.Context, token, user string) (name, email string) {
	out, err := s.slackWithToken(ctx, token, "get_user", map[string]any{"user": user})
	if err != nil {
		s.Logger.Warn("slack install: reading the installer", "err", err)
		return "", ""
	}
	name = strings.TrimSpace(asString(out["real_name"]))
	if name == "" {
		name = strings.TrimSpace(asString(out["display_name"]))
	}
	if n, ok := plainName(name, 1, 80); ok {
		name = n
	} else {
		name = ""
	}
	email = strings.ToLower(strings.TrimSpace(asString(out["email"])))
	if a, err := mail.ParseAddress(email); err != nil || a.Address != email || len(email) > 254 {
		email = ""
	}
	return name, email
}

// slackWithToken runs a slack@1 action with a bot token no business holds
// yet (an install's), through the egress guard.
func (s *Server) slackWithToken(ctx context.Context, token, action string, input map[string]any) (map[string]any, error) {
	c, ok := s.Registry.Get(slackConnector)
	if !ok {
		return nil, fmt.Errorf("%w: the Slack connector is not available", whatsapp.ErrNothingToSend)
	}
	creds := map[string]string{"bot_token": token}
	hc := s.egressGuard().Client(egress.Policy{Tenant: "", Hosts: c.Manifest.HostsFor(creds), Purpose: "chat:slack"}, 15*time.Second)
	resp, err := c.Actions[action].Execute(ctx, connector.Request{Input: input, Credentials: creds, HTTP: hc, Logger: s.Logger, Attempt: 1})
	if err != nil {
		return nil, err
	}
	out, _ := resp.Output.(map[string]any)
	return out, nil
}

// slackInstall is a workspace's install waiting for a business, opened.
func (s *Server) slackInstall(ctx context.Context, team string) (*slackSignup, error) {
	ss := &slackSignup{team: team, install: true}
	var app, email *string
	var sealed, key string
	var shared bool
	err := s.Store.Pool.QueryRow(ctx, `SELECT team_name, installer, app_id, token, kms_key, person, email, shared FROM taskiem_slack_install_get($1)`, team).
		Scan(&ss.teamName, &ss.user, &app, &sealed, &key, &ss.person, &email, &shared)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if email != nil {
		ss.email = *email
	}
	token, err := s.Vault.OpenPlatform(ctx, "slack-install:"+team, key, sealed)
	if err != nil {
		return nil, err
	}
	ss.token = string(token)
	return ss, nil
}

// slackStranger answers a Slack user no business here has linked: the
// installer of a workspace's waiting install is signing up; anyone else
// gets nothing until a business brings them in.
func (s *Server) slackStranger(ctx context.Context, team, user string, in whatsapp.Inbound) error {
	ss, err := s.slackInstall(ctx, team)
	if err != nil || ss == nil || ss.user != user {
		return err
	}
	addr := slackAddr(team, user)
	text := ""
	if in.Type == "text" {
		text = in.Text
	}
	sc := &signupChat{ch: &slackInstallChannel{s: s, team: team, token: ss.token}, addr: addr, lang: s.chooseLang(ctx, uuid.Nil, addr, text).Tag, slack: ss}
	if done, err := s.keyGuard(ctx, &chat{ch: sc.ch, addr: addr, lang: langChoice{Tag: sc.lang}}, text); done || err != nil {
		return err
	}
	if done, err := s.chatSignupMessage(ctx, sc, text, in.Reply); done || err != nil {
		return err
	}
	if !s.limiter("sl-unbound:"+addr, 10*time.Minute, 2).Allow() {
		return nil
	}
	return s.signupOffer(ctx, sc, text)
}

// slackSignupToken fills in the token a linked person's new business
// takes: their business's in this workspace.
func (s *Server) slackSignupToken(ctx context.Context, ss *slackSignup) error {
	if ss.token != "" {
		return nil
	}
	for _, t := range ss.linked {
		env, name, err := s.slackConnection(ctx, t, ss.team)
		if err != nil {
			continue
		}
		use := secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: secrets.PurposeChatDeliver})
		creds, err := s.Vault.Credentials(use, t, env, "slack", name)
		if err != nil {
			continue
		}
		if ss.teamName == "" {
			ss.teamName = creds["team_name"]
		}
		if ss.token = creds["bot_token"]; ss.token != "" {
			return nil
		}
	}
	return fmt.Errorf("%w: no business here has Slack connected", whatsapp.ErrNothingToSend)
}

// slackJoinTx gives a new business its Slack: the token as its main
// Slack connection, through Taskiem's app for the workspace, and the
// owner linked there.
func (s *Server) slackJoinTx(ctx context.Context, tx pgx.Tx, tenant, owner uuid.UUID, ss *slackSignup) error {
	if _, err := tx.Exec(ctx, `INSERT INTO slack_bindings (tenant_id, user_id, team_id, slack_user_id, verified_at) VALUES ($1, $2, $3, $4, now())`,
		tenant, owner, ss.team, ss.user); err != nil {
		return err
	}
	c, ok := s.Registry.Get(slackConnector)
	if !ok {
		return errors.New("slack: the connector is not available")
	}
	creds := map[string]string{"bot_token": ss.token, "team_id": ss.team, "team_name": ss.teamName}
	id, err := s.Vault.CreateOAuthConnectionTx(ctx, tx, tenant, "prod", c.Manifest.ID, "main", creds, nil, owner.String())
	if err != nil {
		return err
	}
	if err := secrets.SetAppAccountTx(ctx, tx, tenant, id, c.Manifest.ClientFor(""), ss.team); err != nil {
		return err
	}
	if err := secrets.SetRouteKeysTx(ctx, tx, tenant, id, c.Manifest.RouteKeys(creds)); err != nil {
		return err
	}
	if ss.install {
		_, err = tx.Exec(ctx, `SELECT taskiem_slack_install_drop($1)`, ss.team)
	}
	return err
}

// slackPlaceholderEmail is the sign-in name of a person who signed up in
// Slack without an email Taskiem may use: it receives no mail.
func slackPlaceholderEmail(team, user string) string {
	h := sha256.Sum256([]byte("taskiem-owner:sl:" + team + ":" + user))
	return fmt.Sprintf("owner+%x@slack.taskiem.invalid", h[:8])
}

// slackOwnerEmail is the account's email for an installer: Slack's, kept
// unconfirmed, unless it is someone's account already (never merged).
func (s *Server) slackOwnerEmail(ctx context.Context, ss *slackSignup) (string, error) {
	if ss.email == "" {
		return slackPlaceholderEmail(ss.team, ss.user), nil
	}
	err := s.Store.Pool.QueryRow(ctx, `SELECT user_id FROM taskiem_auth_find_user($1)`, ss.email).Scan(new(uuid.UUID))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ss.email, nil
	case err != nil:
		return "", err
	}
	return slackPlaceholderEmail(ss.team, ss.user), nil
}

// slackInstallSweep gives back the tokens of installs no business took in
// a day, at most once an hour per replica (WhatsAppTick). A token a
// business already shares stays theirs.
func (s *Server) slackInstallSweep(ctx context.Context) error {
	if s.Slack == nil || !s.limiter("slack-install-sweep", time.Hour, 1).Allow() {
		return nil
	}
	rows, err := s.Store.Pool.Query(ctx, `SELECT team_id, token, kms_key, shared FROM taskiem_slack_installs_expired()`)
	if err != nil {
		return err
	}
	type gone struct {
		team, sealed, key string
		shared            bool
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (gone, error) {
		var g gone
		return g, r.Scan(&g.team, &g.sealed, &g.key, &g.shared)
	})
	if err != nil {
		return err
	}
	c, ok := s.Registry.Get(slackConnector)
	if !ok {
		return nil
	}
	prov, perr := s.oauthFor(c, "")
	for _, g := range list {
		if g.shared || perr != nil {
			continue
		}
		token, err := s.Vault.OpenPlatform(ctx, "slack-install:"+g.team, g.key, g.sealed)
		if err != nil {
			s.Logger.Warn("slack install: opening an unused token", "err", err)
			continue
		}
		s.oauthRevoke(ctx, uuid.Nil, c, prov, string(token))
	}
	if len(list) > 0 {
		s.Logger.Info("slack installs: unused, given back", "count", len(list))
	}
	return nil
}

// slackInstallChannel is a person's DMs with the bot in a workspace whose
// install waits for a business: the install's token sends.
type slackInstallChannel struct {
	s     *Server
	team  string
	token string
}

var _ chatChannel = (*slackInstallChannel)(nil)

func (c *slackInstallChannel) Name() string         { return "slack" }
func (c *slackInstallChannel) OwnTenant() uuid.UUID { return uuid.Nil }
func (c *slackInstallChannel) Prefix(string) string { return "" }

func (c *slackInstallChannel) Contact(ctx context.Context, addr string) (whatsapp.Contact, error) {
	return (&slackDMChannel{s: c.s}).Contact(ctx, addr)
}

func (c *slackInstallChannel) Touch(ctx context.Context, addr string, inbound, setTenant bool, tenant *uuid.UUID) error {
	return (&slackDMChannel{s: c.s}).Touch(ctx, addr, inbound, setTenant, tenant)
}

func (c *slackInstallChannel) Send(ctx context.Context, addr string, m whatsapp.Message) (string, error) {
	team, input, err := slackMessage(addr, m)
	if err != nil {
		return "", err
	}
	if team != c.team {
		return "", errors.New("slack: another workspace's address")
	}
	out, err := c.s.slackWithToken(ctx, c.token, "post_message", input)
	if err != nil {
		return "", err
	}
	ts, _ := out["ts"].(string)
	return ts, nil
}
