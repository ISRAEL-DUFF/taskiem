package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Slack as a conversation (docs/spec/chat-way-in.md 8, decision 0058): a
// direct message to Taskiem's Slack bot from a member linked to it is the
// same conversation WhatsApp and Telegram have (converse): status, runs,
// builds, tables, invoices, stock, approvals. Slack sends every event of
// Taskiem's app to one URL, /channels/slack/events, verified with the
// app's signing secret: Taskiem answers within Slack's three seconds,
// claims each event id once (Slack retries), and handles a person's DM
// after; every other event goes on, unchanged, to the slack@1 triggers of
// Taskiem's app (/hooks/apps/slack@1/events), as clicks already do. A DM
// names only the workspace and the user, and a workspace may hold several
// businesses (decision 0051): the conversation keeps a current business
// among those that linked this person, and `switch` changes it.

// slackDMChannel is a person's direct messages with Taskiem's bot, across
// the businesses that linked them in one workspace.
type slackDMChannel struct {
	s    *Server
	team string
	// linked are the businesses that linked this Slack user; every one of
	// them reaches the workspace through Taskiem's one bot there.
	linked []uuid.UUID
}

var _ chatChannel = (*slackDMChannel)(nil)

func (c *slackDMChannel) Name() string         { return "slack" }
func (c *slackDMChannel) OwnTenant() uuid.UUID { return uuid.Nil }

// Prefix names the business when the person is in more than one.
func (c *slackDMChannel) Prefix(tenant string) string {
	if tenant == "" || len(c.linked) < 2 {
		return ""
	}
	return "[" + tenant + "] "
}

func (c *slackDMChannel) Contact(ctx context.Context, addr string) (whatsapp.Contact, error) {
	var ct whatsapp.Contact
	err := c.s.Store.Pool.QueryRow(ctx, `SELECT current_tenant, last_inbound_at FROM taskiem_slack_contact($1)`, addr).Scan(&ct.Tenant, &ct.LastInbound)
	if errors.Is(err, pgx.ErrNoRows) {
		return ct, nil
	}
	return ct, err
}

func (c *slackDMChannel) Touch(ctx context.Context, addr string, inbound, setTenant bool, tenant *uuid.UUID) error {
	_, err := c.s.Store.Pool.Exec(ctx, `SELECT taskiem_slack_contact_touch($1, $2, $3, $4)`, addr, inbound, setTenant, tenant)
	return err
}

// Send posts through the first linked business whose Slack connection
// reaches the workspace: the bot is the same for all of them.
func (c *slackDMChannel) Send(ctx context.Context, addr string, m whatsapp.Message) (string, error) {
	last := fmt.Errorf("%w: no business here has Slack connected", whatsapp.ErrNothingToSend)
	for _, t := range c.linked {
		id, err := (&slackChannel{s: c.s, tenant: t}).Send(ctx, addr, m)
		if err == nil {
			return id, nil
		}
		last = err
		if !errors.Is(err, whatsapp.ErrNothingToSend) {
			break
		}
	}
	return "", last
}

// linkedTenants limits the conversation to the businesses that linked the
// person in this workspace (converse).
func (c *slackDMChannel) linkedTenants() []uuid.UUID { return c.linked }

// slackEvents receives the events of Taskiem's Slack app: POST
// /channels/slack/events.
func (s *Server) slackEvents(w http.ResponseWriter, r *http.Request) {
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
	var ev struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
		EventID   string `json:"event_id"`
		TeamID    string `json:"team_id"`
		Event     struct {
			Type        string `json:"type"`
			ChannelType string `json:"channel_type"`
			Subtype     string `json:"subtype"`
			BotID       string `json:"bot_id"`
			User        string `json:"user"`
			Text        string `json:"text"`
		} `json:"event"`
	}
	_ = json.Unmarshal(body, &ev)
	switch {
	case ev.Type == "url_verification":
		// Slack's check of the URL, when the operator sets it.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(ev.Challenge))
		return
	case ev.Type == "event_callback" && ev.Event.Type == "message" && ev.Event.ChannelType == "im" && ev.Event.Subtype == "" && ev.Event.BotID == "" && ev.Event.User != "":
		// A person writing to the bot. Slack retries what it thinks was
		// missed: each event once.
		fresh := true
		if ev.EventID != "" {
			if err := s.Store.Pool.QueryRow(r.Context(), `SELECT taskiem_slack_event_claim($1)`, ev.EventID).Scan(&fresh); err != nil {
				writeErr(w, http.StatusInternalServerError, "try again")
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		if fresh {
			team, user, text := ev.TeamID, ev.Event.User, ev.Event.Text
			s.background(r.Context(), "slack message", func(ctx context.Context) {
				s.slackDM(ctx, team, user, whatsapp.Inbound{ID: ev.EventID, Type: "text", Text: strings.TrimSpace(text)})
			})
		}
		return
	case ev.Type == "event_callback" && ev.Event.Type == "message" && ev.Event.ChannelType == "im":
		// The bot's own messages and edits in its DMs: nothing to do.
		w.WriteHeader(http.StatusOK)
		return
	}
	// Anything else is a workflow's: the slack@1 events trigger of
	// Taskiem's app, routed by workspace (decision 0051).
	if s.Ingest == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	fwd, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "/apps/"+slackConnector+"/events", bytes.NewReader(body))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "try again")
		return
	}
	fwd.Header = r.Header.Clone()
	s.Ingest.ServeHTTP(w, fwd)
}

// slackDM handles a message (or a conversation's button) from a Slack
// user: a linked member's goes to the conversation in the business they
// last worked in there; anyone else is told how to link.
func (s *Server) slackDM(ctx context.Context, team, slackUser string, in whatsapp.Inbound) {
	addr := slackAddr(team, slackUser)
	if _, _, ok := parseSlackAddr(addr); !ok {
		return
	}
	if !s.limiter("sl:"+addr, 3*time.Second, 20).Allow() {
		return
	}
	rows, err := s.Store.Pool.Query(ctx, `SELECT tenant_id, user_id FROM taskiem_slack_user_of($1, $2)`, team, slackUser)
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
	if len(links) == 0 {
		// Not linked: nothing can answer from a business. Said at most
		// twice in ten minutes; the bot cannot write without a business's
		// token, so this is best effort through any business here.
		return
	}
	ch := &slackDMChannel{s: s, team: team}
	for _, l := range links {
		ch.linked = append(ch.linked, l.tenant)
	}
	if err := ch.Touch(ctx, addr, true, false, nil); err != nil {
		s.Logger.Warn("slack: touching a contact", "err", err)
	}
	// The person in the business the conversation is in, else the first.
	user := links[0].user
	if ct, err := ch.Contact(ctx, addr); err == nil && ct.Tenant != nil {
		if i := slices.IndexFunc(links, func(l link) bool { return l.tenant == *ct.Tenant }); i >= 0 {
			user = links[i].user
			// That business first: its connection sends.
			ch.linked[0], ch.linked[i] = ch.linked[i], ch.linked[0]
		}
	}
	if err := s.converse(ctx, &chat{ch: ch, addr: addr, in: in}, user); err != nil {
		s.Logger.Warn("slack: a message", "err", err)
	}
}
