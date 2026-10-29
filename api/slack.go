package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
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

// Approvals in Slack (K2 of docs/spec/business-connectors.md; decision
// 0051): a third chat channel beside WhatsApp and Telegram, on the same
// notices, outbox, decision tokens and deciding (whatsapp_approvals.go).
// What differs is how a message travels: through the business's own Slack
// connection (its bot token, from Add to Slack or pasted), as a direct
// message from the bot to the member's linked Slack user. A Slack link is
// one business's (its workspace), so the channel speaks for that business
// alone. Button clicks come to Taskiem's Slack app's one interactivity URL,
// /channels/slack/interactions: approval and link buttons are handled
// here; every other click goes on to the slack@1 interactions trigger.

// SlackApp is Taskiem's Slack app (TASKIEM_OAUTH_SLACK_*): its signing
// secret verifies the clicks; nil turns the channel off.
type SlackApp struct {
	SigningSecret string
}

const (
	slackConnector = "slack@1"
	// slackDecide and slackLink are the action ids of Taskiem's own
	// buttons, and slackReply a conversation's (Yes, No, a template to
	// start: slack_chat.go); any other action is a workflow's.
	slackDecide = "taskiem.decide"
	slackLink   = "taskiem.link"
	slackReply  = "taskiem.reply"
)

var slackAddrRe = regexp.MustCompile(`^sl:([A-Z0-9]{1,24}):([A-Z0-9]{1,24})$`)

func slackAddr(team, user string) string { return "sl:" + team + ":" + user }

func parseSlackAddr(addr string) (team, user string, ok bool) {
	m := slackAddrRe.FindStringSubmatch(addr)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// slackChannel is a business's Slack workspace as a chat channel.
type slackChannel struct {
	s      *Server
	tenant uuid.UUID
}

var _ chatChannel = (*slackChannel)(nil)

func (c *slackChannel) Name() string         { return "slack" }
func (c *slackChannel) OwnTenant() uuid.UUID { return c.tenant }

// Prefix is empty: the workspace is the business's own.
func (c *slackChannel) Prefix(string) string { return "" }

// Contact: a Slack link always speaks to its business; Slack has no
// service window.
func (c *slackChannel) Contact(context.Context, string) (whatsapp.Contact, error) {
	t := c.tenant
	now := time.Now()
	return whatsapp.Contact{Tenant: &t, LastInbound: &now}, nil
}

func (c *slackChannel) Touch(context.Context, string, bool, bool, *uuid.UUID) error { return nil }

// Send posts m to the Slack user as a direct message from the business's
// bot: its text as plain text (never mrkdwn, so nothing in it formats or
// mentions), and its buttons as a row of Block Kit buttons carrying their
// payloads (a decision token fits Slack's 2,000 characters). It returns the
// message's ts.
func (c *slackChannel) Send(ctx context.Context, addr string, m whatsapp.Message) (string, error) {
	team, user, ok := parseSlackAddr(addr)
	if !ok {
		return "", errors.New("slack: not a Slack address")
	}
	text := strings.TrimSpace(m.Text)
	if text == "" {
		return "", fmt.Errorf("%w: nothing to say", whatsapp.ErrNothingToSend)
	}
	blocks := []any{map[string]any{"type": "section", "text": map[string]any{"type": "plain_text", "text": clip(text, 3000)}}}
	if len(m.Buttons) > 0 {
		var els []any
		for i, b := range m.Buttons {
			if b.ID == "" || len(b.ID) > 2000 {
				return "", errors.New("slack: a button's payload must be 1-2000 characters")
			}
			id := slackReply
			switch {
			case strings.HasPrefix(b.ID, "tk1."):
				id = slackDecide
			case strings.HasPrefix(b.ID, "taskiem-link:"):
				id = slackLink
			}
			el := map[string]any{"type": "button", "action_id": fmt.Sprintf("%s.%d", id, i), "value": b.ID,
				"text": map[string]any{"type": "plain_text", "text": clip(b.Title, 75)}}
			if i == 0 {
				el["style"] = "primary"
			}
			els = append(els, el)
		}
		blocks = append(blocks, map[string]any{"type": "actions", "elements": els})
	}
	if l := m.Link; l != nil && strings.HasPrefix(l.URL, "https://") {
		// A page to open (a connect link): a button with its URL.
		blocks = append(blocks, map[string]any{"type": "actions", "elements": []any{map[string]any{"type": "button", "action_id": "taskiem.open",
			"url": l.URL, "text": map[string]any{"type": "plain_text", "text": clip(l.Title, 75)}}}})
	}
	out, err := c.s.slackCall(ctx, c.tenant, team, "post_message", map[string]any{"channel": user, "text": clip(text, 3000), "blocks": blocks})
	if err != nil {
		return "", err
	}
	ts, _ := out["ts"].(string)
	return ts, nil
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// slackConnection is the business's Slack connection for a workspace: the
// one made through Taskiem's app for it, else its only active connection
// in prod (an own app). team may be empty: any workspace.
func (s *Server) slackConnection(ctx context.Context, tenant uuid.UUID, team string) (env, name string, err error) {
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if team != "" {
			err := tx.QueryRow(ctx, `SELECT a.environment, a.connection FROM connection_app_accounts a JOIN connections c ON c.id = a.connection_id AND c.status = 'active'
				WHERE a.connector = 'slack' AND a.account_key = $1 ORDER BY a.environment = 'prod' DESC LIMIT 1`, team).Scan(&env, &name)
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		rows, err := tx.Query(ctx, `SELECT environment, name FROM connections WHERE connector = 'slack' AND environment = 'prod' AND status = 'active' LIMIT 2`)
		if err != nil {
			return err
		}
		type conn struct{ env, name string }
		cs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (conn, error) {
			var c conn
			return c, r.Scan(&c.env, &c.name)
		})
		if err != nil {
			return err
		}
		if len(cs) != 1 {
			return fmt.Errorf("%w: connect Slack first (one Slack connection in production)", whatsapp.ErrNothingToSend)
		}
		env, name = cs[0].env, cs[0].name
		return nil
	})
	return env, name, err
}

// slackCall runs a slack@1 action with the business's connection for the
// workspace, through the egress guard.
func (s *Server) slackCall(ctx context.Context, tenant uuid.UUID, team, action string, input map[string]any) (map[string]any, error) {
	env, name, err := s.slackConnection(ctx, tenant, team)
	if err != nil {
		return nil, err
	}
	return s.slackCallWith(ctx, tenant, env, name, action, input)
}

// slackCallWith runs a slack@1 action with a named connection.
func (s *Server) slackCallWith(ctx context.Context, tenant uuid.UUID, env, name, action string, input map[string]any) (map[string]any, error) {
	reg, err := s.Registry.For(ctx, tenant.String())
	if err != nil {
		return nil, err
	}
	c, ok := reg.Get(slackConnector)
	if !ok {
		return nil, fmt.Errorf("%w: the Slack connector is not available", whatsapp.ErrNothingToSend)
	}
	use := secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: secrets.PurposeChatDeliver})
	creds, err := s.Vault.Credentials(use, tenant, env, c.Manifest.ID, name)
	if err != nil {
		return nil, err
	}
	g := s.Egress
	if g == nil {
		g = &egress.Guard{Logger: s.Logger}
	}
	hc := g.Client(egress.Policy{Tenant: tenant.String(), Hosts: c.Manifest.HostsFor(creds), Purpose: "chat:slack"}, 15*time.Second)
	resp, err := c.Actions[action].Execute(ctx, connector.Request{Input: input, Credentials: creds, HTTP: hc, Logger: s.Logger, Attempt: 1})
	if err != nil {
		return nil, err
	}
	out, _ := resp.Output.(map[string]any)
	return out, nil
}

// --- linking ---

type slackBindingInfo struct {
	Enabled    bool       `json:"enabled"`
	Linked     bool       `json:"linked"`
	Pending    bool       `json:"pending"`
	Workspace  string     `json:"workspace,omitempty"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
}

var errSlackOff = fmt.Errorf("%w: approvals in Slack are not set up on this Taskiem (TASKIEM_OAUTH_SLACK_WEBHOOK_SECRET)", errConflict)

// getSlack answers whether the caller has linked Slack in this business.
func (s *Server) getSlack(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "a Slack account belongs to a person, not an API key")
		return
	}
	out := slackBindingInfo{Enabled: s.Slack != nil}
	err := s.tx(r, func(tx pgx.Tx) error {
		err := tx.QueryRow(r.Context(), `SELECT team_id, verified_at FROM slack_bindings WHERE user_id = $1`, p.UserID).Scan(&out.Workspace, &out.VerifiedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		out.Linked, out.Pending = out.VerifiedAt != nil, out.VerifiedAt == nil
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// startSlackLink finds the caller in the business's Slack workspace by
// their email and sends them a Confirm button there; the link holds once
// they press it. Like linking WhatsApp or Telegram, a linked Slack account
// decides approvals, so it takes the same proof as adding a passkey.
func (s *Server) startSlackLink(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "a Slack account belongs to a person, not an API key")
		return
	}
	if s.Slack == nil {
		s.fail(w, r, errSlackOff)
		return
	}
	var req struct {
		factorProof
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.proveFactor(w, r, req.factorProof, "slack.link") {
		return
	}
	if !s.limiter("slack-link:"+p.UserID.String(), 5*time.Minute, 5).Allow() {
		writeErr(w, http.StatusTooManyRequests, "too many links asked for; wait a few minutes")
		return
	}
	ctx := r.Context()
	var email string
	if err := s.tx(r, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, p.UserID).Scan(&email)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	who, err := s.slackCall(ctx, p.TenantID, "", "auth_test", map[string]any{})
	if err != nil {
		s.fail(w, r, slackErr(err))
		return
	}
	team, _ := who["team_id"].(string)
	found, err := s.slackCall(ctx, p.TenantID, team, "lookup_user_by_email", map[string]any{"email": email})
	if err != nil {
		s.fail(w, r, slackErr(err))
		return
	}
	user, _ := found["id"].(string)
	if found["found"] != true || !slackAddrRe.MatchString(slackAddr(team, user)) {
		s.fail(w, r, fmt.Errorf("%w: no one in your Slack workspace has the email %s: use the same email in Slack, or ask its admin", errConflict, email))
		return
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		s.fail(w, r, err)
		return
	}
	nonce := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(nonce))
	err = s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO slack_bindings (tenant_id, user_id, team_id, slack_user_id, nonce_hash, requested_at, verified_at)
			VALUES ($1, $2, $3, $4, $5, now(), NULL)
			ON CONFLICT (tenant_id, user_id) DO UPDATE SET team_id = EXCLUDED.team_id, slack_user_id = EXCLUDED.slack_user_id,
			  nonce_hash = EXCLUDED.nonce_hash, requested_at = now(), verified_at = NULL`, p.TenantID, p.UserID, team, user, sum[:]); err != nil {
			return err
		}
		return auditTx(r, tx, "slack.link_sent", p.UserID.String(), map[string]any{"workspace": team})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ch := &slackChannel{s: s, tenant: p.TenantID}
	l := s.chooseLang(ctx, p.TenantID, slackAddr(team, user), "").Tag
	if _, err := ch.Send(ctx, slackAddr(team, user), whatsapp.Message{Text: tr(l, "slack.link.confirm"),
		Buttons: []whatsapp.Button{{ID: "taskiem-link:" + nonce, Title: tr(l, "slack.link.button")}}}); err != nil {
		s.fail(w, r, slackErr(err))
		return
	}
	writeJSON(w, http.StatusCreated, slackBindingInfo{Enabled: true, Pending: true, Workspace: team})
}

// unlinkSlack removes the caller's Slack link in this business.
func (s *Server) unlinkSlack(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "a Slack account belongs to a person, not an API key")
		return
	}
	err := s.tx(r, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `DELETE FROM slack_bindings WHERE user_id = $1`, p.UserID)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		return auditTx(r, tx, "slack.unlink", p.UserID.String(), map[string]any{"channel": "web"})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// slackErr says in words why Slack could not be reached for a person.
func slackErr(err error) error {
	if errors.Is(err, whatsapp.ErrNothingToSend) {
		return fmt.Errorf("%w: %w", errConflict, err)
	}
	return fmt.Errorf("%w: Slack refused: %w", errConflict, err)
}

// --- clicks ---

// slackPayload is the part of a block_actions payload Taskiem reads.
type slackPayload struct {
	Type string `json:"type"`
	Team struct {
		ID string `json:"id"`
	} `json:"team"`
	User struct {
		ID string `json:"id"`
	} `json:"user"`
	Actions []struct {
		ActionID string `json:"action_id"`
		Value    string `json:"value"`
	} `json:"actions"`
	ResponseURL string `json:"response_url"`
	Message     struct {
		Blocks []map[string]any `json:"blocks"`
	} `json:"message"`
}

// SlackHooks receives the clicks on Taskiem's Slack app: POST
// /channels/slack/interactions, verified with the app's signing secret.
// Served on the edge role, and on the api role when it runs every role.
func (s *Server) SlackHooks() http.Handler { return http.HandlerFunc(s.slackInteractions) }

// SlackEvents receives the events of Taskiem's Slack app: POST
// /channels/slack/events (slack_chat.go), on the same roles.
func (s *Server) SlackEvents() http.Handler { return http.HandlerFunc(s.slackEvents) }

func (s *Server) slackInteractions(w http.ResponseWriter, r *http.Request) {
	if s.Slack == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "body too large")
		return
	}
	verify := &connector.VerifySpec{Scheme: "slack_v0", Header: "X-Slack-Signature", TimestampHeader: "X-Slack-Request-Timestamp"}
	if err := connector.VerifyWebhook(verify, s.Slack.SigningSecret, r.Header, body); err != nil {
		writeErr(w, http.StatusUnauthorized, "signature invalid")
		return
	}
	form, _ := url.ParseQuery(string(body))
	var pl slackPayload
	_ = json.Unmarshal([]byte(form.Get("payload")), &pl)
	var action, value string
	if pl.Type == "block_actions" && len(pl.Actions) > 0 {
		action, value = pl.Actions[0].ActionID, pl.Actions[0].Value
	}
	switch {
	case strings.HasPrefix(action, slackLink+"."):
		w.WriteHeader(http.StatusOK)
		s.background(r.Context(), "slack link", func(ctx context.Context) { s.slackConfirm(ctx, pl, value) })
	case strings.HasPrefix(action, slackDecide+"."):
		w.WriteHeader(http.StatusOK)
		s.background(r.Context(), "slack decision", func(ctx context.Context) { s.slackDecision(ctx, pl, value) })
	case strings.HasPrefix(action, slackReply+"."):
		// A conversation's button, read as the person's answer (slack_chat.go).
		w.WriteHeader(http.StatusOK)
		s.background(r.Context(), "slack reply", func(ctx context.Context) {
			s.slackDM(ctx, pl.Team.ID, pl.User.ID, whatsapp.Inbound{ID: uuid.NewString(), Type: "interactive", Reply: value})
			s.slackReplace(ctx, pl, "")
		})
	case action == "taskiem.open":
		// A link button: Slack opens the page; nothing to do here.
		w.WriteHeader(http.StatusOK)
	default:
		// A workflow's button, modal or shortcut: the slack@1 interactions
		// trigger of Taskiem's app, routed by workspace (rule 18).
		if s.Ingest == nil {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		fwd, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "/apps/"+slackConnector+"/interactions", bytes.NewReader(body))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "try again")
			return
		}
		fwd.Header = r.Header.Clone()
		s.Ingest.ServeHTTP(w, fwd)
	}
}

// slackConfirm completes a link from its Confirm button, pressed by the
// Slack user it was sent to.
func (s *Server) slackConfirm(ctx context.Context, pl slackPayload, value string) {
	nonce, _ := strings.CutPrefix(value, "taskiem-link:")
	sum := sha256.Sum256([]byte(nonce))
	var tenant *uuid.UUID
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_slack_confirm($1, $2, $3)`, sum[:], pl.Team.ID, pl.User.ID).Scan(&tenant); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.Logger.Warn("slack: confirming a link", "err", err)
		return
	}
	addr := slackAddr(pl.Team.ID, pl.User.ID)
	if tenant == nil {
		s.slackReplace(ctx, pl, tr(s.chooseLang(ctx, uuid.Nil, addr, "").Tag, "slack.link.stale"))
		return
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{*tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'system', 'system', 'slack.link', $2, $3)`, *tenant, addr, `{"channel":"slack"}`)
		return err
	}); err != nil {
		s.Logger.Warn("slack: auditing a link", "err", err)
	}
	s.slackReplace(ctx, pl, tr(s.chooseLang(ctx, *tenant, addr, "").Tag, "slack.link.done"))
}

// slackDecision decides an approval from its button, as WhatsApp's and
// Telegram's are (waDecide): the token says which business, and the
// clicker must be the member it was sent to, linked there. The message's
// buttons are then taken away, so it cannot be pressed again.
func (s *Server) slackDecision(ctx context.Context, pl slackPayload, value string) {
	addr := slackAddr(pl.Team.ID, pl.User.ID)
	if _, _, ok := parseSlackAddr(addr); !ok {
		return
	}
	claims, _, err := whatsapp.ParseToken(value)
	if err != nil {
		s.slackReplace(ctx, pl, tr(s.chooseLang(ctx, uuid.Nil, addr, "").Tag, "wa.approval.stale"))
		return
	}
	rows, err := s.Store.Pool.Query(ctx, `SELECT tenant_id, user_id FROM taskiem_slack_user_of($1, $2)`, pl.Team.ID, pl.User.ID)
	if err != nil {
		s.Logger.Warn("slack: reading a link", "err", err)
		return
	}
	type link struct{ tenant, user uuid.UUID }
	links, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (link, error) {
		var l link
		return l, r.Scan(&l.tenant, &l.user)
	})
	if err != nil {
		s.Logger.Warn("slack: reading a link", "err", err)
		return
	}
	var user uuid.UUID
	for _, l := range links {
		if l.tenant == claims.Tenant {
			user = l.user
		}
	}
	if user == uuid.Nil {
		s.slackReplace(ctx, pl, tr(s.chooseLang(ctx, claims.Tenant, addr, "").Tag, "slack.not_linked"))
		return
	}
	ch := &slackChannel{s: s, tenant: claims.Tenant}
	if err := s.converse(ctx, &chat{ch: ch, addr: addr, in: whatsapp.Inbound{ID: uuid.NewString(), Type: "interactive", Reply: value}}, user); err != nil {
		s.Logger.Warn("slack: deciding", "err", err)
	}
	s.slackReplace(ctx, pl, "")
}

// slackReplace rewrites the clicked message through its response_url: its
// text kept, its buttons gone, and note added when there is one.
func (s *Server) slackReplace(ctx context.Context, pl slackPayload, note string) {
	if !strings.HasPrefix(pl.ResponseURL, "https://hooks.slack.com/") {
		return
	}
	var blocks []any
	for _, b := range pl.Message.Blocks {
		if b["type"] != "actions" {
			blocks = append(blocks, b)
		}
	}
	if note != "" {
		blocks = append(blocks, map[string]any{"type": "context", "elements": []any{map[string]any{"type": "plain_text", "text": clip(note, 2000)}}})
	}
	raw, _ := json.Marshal(map[string]any{"replace_original": true, "text": note, "blocks": blocks})
	g := s.Egress
	if g == nil {
		g = &egress.Guard{Logger: s.Logger}
	}
	hc := g.Client(egress.Policy{Tenant: "", Hosts: []string{"hooks.slack.com"}, Purpose: "chat:slack"}, 10*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pl.ResponseURL, bytes.NewReader(raw))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		s.Logger.Warn("slack: updating a message", "err", err)
		return
	}
	_ = resp.Body.Close()
}

// slackAddrsTx are the Slack addresses of users who linked Slack in the
// tenant tx is scoped to.
func slackAddrsTx(ctx context.Context, tx pgx.Tx, users []uuid.UUID) (map[uuid.UUID]string, error) {
	rows, err := tx.Query(ctx, `SELECT user_id, 'sl:' || team_id || ':' || slack_user_id FROM slack_bindings WHERE verified_at IS NOT NULL AND user_id = ANY ($1)`, users)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID]string{}
	for rows.Next() {
		var u uuid.UUID
		var a string
		if err := rows.Scan(&u, &a); err != nil {
			rows.Close()
			return nil, err
		}
		out[u] = a
	}
	rows.Close()
	return out, rows.Err()
}

// SlackAlerts posts alerts through a business's Slack connection (alert
// channels made from it; engine/alerts.Slack).
type SlackAlerts struct{ Server *Server }

// PostAlert posts text to channel with the production connection named
// connection.
func (a *SlackAlerts) PostAlert(ctx context.Context, tenant uuid.UUID, connection, channel, text string) error {
	_, err := a.Server.slackCallWith(ctx, tenant, "prod", connection, "post_message", map[string]any{"channel": channel, "text": text})
	return err
}

// listSlackChannels answers GET /v1/slack/channels?connection=: the
// channels a production Slack connection's bot can post in, for the
// templates' channel picker (no-technical-steps N8).
func (s *Server) listSlackChannels(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	name := r.URL.Query().Get("connection")
	if name == "" {
		name = "main"
	}
	out, err := s.slackCallWith(r.Context(), p.TenantID, "prod", name, "list_conversations",
		map[string]any{"types": "public_channel,private_channel", "exclude_archived": true, "limit": 1000})
	if errors.Is(err, secrets.ErrNotFound) {
		s.fail(w, r, fmt.Errorf("%w: there is no active Slack connection named %q in production", errConflict, name))
		return
	}
	if err != nil {
		s.fail(w, r, slackErr(err))
		return
	}
	channels := []map[string]any{}
	list, _ := out["channels"].([]any)
	for _, c := range list {
		m, _ := c.(map[string]any)
		channels = append(channels, map[string]any{"id": m["id"], "name": m["name"], "is_private": m["is_private"], "is_member": m["is_member"]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": channels})
}
