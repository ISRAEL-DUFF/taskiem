package api

// The Taskiem conversation's history and the web as a chat channel
// (business links L3, §5.5; migration 00279). What a member says to
// Taskiem and what Taskiem says to them is kept on every channel, so the
// Chat page's Taskiem conversation shows the same history as the phone:
// each message in the business it concerns, only when its person is a
// member there, its words sealed, its buttons by their titles only, its
// links without their queries, and never a message that is only a code;
// each row expires (TASKIEM_CHAT_HISTORY_DAYS). The web is a fourth
// channel on converse: a message typed on the Chat page is read as one
// typed on WhatsApp, and Taskiem's replies are its answer.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/telegram"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// DefaultChatHistoryDays is how long the Taskiem conversation's history
// is kept when TASKIEM_CHAT_HISTORY_DAYS is not set.
const DefaultChatHistoryDays = 90

const maxHistoryText = 4000

func (s *Server) chatHistoryTTL() time.Duration {
	d := s.ChatHistoryDays
	if d <= 0 {
		d = DefaultChatHistoryDays
	}
	return time.Duration(d) * 24 * time.Hour
}

// chatHooks has every channel tell the history what it sent. Idempotent;
// called wherever the server starts sending.
func (s *Server) chatHooks() {
	if s.WhatsApp != nil && s.WhatsApp.Sent == nil {
		s.WhatsApp.Sent = s.recordSent
	}
	if s.Telegram != nil && s.Telegram.Sent == nil {
		s.Telegram.Sent = s.recordSent
	}
}

type chatTenantKey struct{}

// withChatTenant says which business a conversation's messages concern,
// for those sent without naming one (a reply in the conversation).
func withChatTenant(ctx context.Context, tenant uuid.UUID) context.Context {
	return context.WithValue(ctx, chatTenantKey{}, tenant)
}

func chatTenantOf(ctx context.Context) uuid.UUID {
	t, _ := ctx.Value(chatTenantKey{}).(uuid.UUID)
	return t
}

var (
	historyURL  = regexp.MustCompile(`https?://[^\s<>"]+`)
	historyCode = regexp.MustCompile(`^[\s\d-]+$`)
	opaquePart  = regexp.MustCompile(`^[A-Za-z0-9_.~=%+-]{20,}$`)
)

// historyText is what is kept of a message's words: a link keeps its
// host and path, without its query or fragment (where tokens travel) and
// with a long opaque part of its path cut; a message that is only a code
// (a PIN, a sign-in code) is not kept at all.
func historyText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if historyCode.MatchString(text) {
		if n := len(strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, text)); n >= 4 && n <= 12 {
			return ""
		}
	}
	text = historyURL.ReplaceAllStringFunc(text, func(raw string) string {
		trail := ""
		for strings.HasSuffix(raw, ".") || strings.HasSuffix(raw, ",") || strings.HasSuffix(raw, ")") {
			trail = raw[len(raw)-1:] + trail
			raw = raw[:len(raw)-1]
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return "…" + trail
		}
		parts := strings.Split(u.EscapedPath(), "/")
		for i, p := range parts {
			if opaquePart.MatchString(p) {
				parts[i] = "…"
			}
		}
		return u.Scheme + "://" + u.Host + strings.Join(parts, "/") + trail
	})
	if utf8.RuneCountInString(text) > maxHistoryText {
		text = string([]rune(text)[:maxHistoryText])
	}
	return text
}

func buttonTitles(bs []whatsapp.Button) []string {
	out := []string{}
	for _, b := range bs {
		if t := strings.TrimSpace(b.Title); t != "" && len(out) < 10 {
			out = append(out, cutRunes(t, 40))
		}
	}
	return out
}

func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// keepHistory writes a message of the Taskiem conversation in tenant,
// when user is a member there, sealed; it reports the row's id (uuid.Nil
// when nothing was kept). A failure is logged, never returned: the
// message was said either way.
func (s *Server) keepHistory(ctx context.Context, tenant, user uuid.UUID, channel string, inbound bool, kind, text string, buttons []string) uuid.UUID {
	text = historyText(text)
	if tenant == uuid.Nil || user == uuid.Nil || text == "" {
		return uuid.Nil
	}
	ctx = context.WithoutCancel(ctx)
	id := uuid.Must(uuid.NewV7())
	kept := false
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var member bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM memberships WHERE tenant_id = $1 AND user_id = $2)`, tenant, user).Scan(&member); err != nil || !member {
			return err
		}
		env, err := s.Store.PII.SealTx(ctx, tx, tenant, "other", text)
		if err != nil {
			return err
		}
		if buttons == nil {
			buttons = []string{}
		}
		_, err = tx.Exec(ctx, `INSERT INTO chat_history (id, tenant_id, user_id, channel, inbound, kind, body, buttons, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now() + $9::interval)`,
			id, tenant, user, channel, inbound, kind, env, buttons, s.chatHistoryTTL().String())
		kept = err == nil
		return err
	})
	if err != nil {
		s.Logger.Warn("chat history: a message not kept", "tenant", tenant, "channel", channel, "err", err)
	}
	if !kept {
		return uuid.Nil
	}
	return id
}

// recordSent keeps a message a channel sent to a member: in the business
// it names, or the one the conversation is with.
func (s *Server) recordSent(ctx context.Context, channel, addr string, m whatsapp.Message) {
	tenant := m.Tenant
	if tenant == uuid.Nil {
		tenant = chatTenantOf(ctx)
	}
	if tenant == uuid.Nil || historyText(m.Text) == "" {
		return
	}
	user, err := s.chatUserOf(ctx, channel, addr, tenant)
	if err != nil {
		s.Logger.Warn("chat history: whose address", "channel", channel, "err", err)
		return
	}
	s.keepHistory(ctx, tenant, user, channel, false, "text", m.Text, buttonTitles(m.Buttons))
}

// chatUserOf is the member an address on a channel belongs to, uuid.Nil
// for someone who is not a member (a customer, a rider).
func (s *Server) chatUserOf(ctx context.Context, channel, addr string, tenant uuid.UUID) (uuid.UUID, error) {
	switch channel {
	case "whatsapp":
		if s.WhatsApp == nil {
			return uuid.Nil, nil
		}
		return s.WhatsApp.UserOf(ctx, addr)
	case "telegram":
		id, ok := telegram.ParseAddress(addr)
		if !ok || s.Telegram == nil {
			return uuid.Nil, nil
		}
		return s.Telegram.UserOf(ctx, id)
	case "slack":
		team, su, ok := parseSlackAddr(addr)
		if !ok {
			return uuid.Nil, nil
		}
		rows, err := s.Store.Pool.Query(ctx, `SELECT tenant_id, user_id FROM taskiem_slack_user_of($1, $2)`, team, su)
		if err != nil {
			return uuid.Nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var t, u uuid.UUID
			if err := rows.Scan(&t, &u); err != nil {
				return uuid.Nil, err
			}
			if t == tenant {
				return u, nil
			}
		}
		return uuid.Nil, rows.Err()
	case "web":
		u, _ := webUser(addr)
		return u, nil
	}
	return uuid.Nil, nil
}

// recordHeard keeps what the person said, once the conversation knows
// whom it is with: their words, the button they pressed, or what they
// sent (a voice note, a photo, a form, a place).
func (s *Server) recordHeard(ctx context.Context, c *chat) {
	in := c.in
	kind, text := "text", in.Text
	switch {
	case in.Flow != "":
		kind, text = "form", "Sent the form"
	case in.Media != nil:
		kind, text = "voice", "Voice note"
	case in.Image != nil:
		kind, text = "photo", strings.TrimSpace("Photo "+in.Text)
	case in.Location != "":
		kind, text = "location", "Shared a location"
	case in.Reply != "":
		kind = "button"
		if strings.TrimSpace(text) == "" {
			text = "Pressed a button"
		}
	}
	id := s.keepHistory(ctx, c.tenant.ID, c.p.UserID, c.ch.Name(), true, kind, text, nil)
	if live := liveOf(ctx); live != nil {
		live.add(TaskiemMessage{ID: id, Channel: "web", Inbound: true, Kind: kind, Text: text, CreatedAt: time.Now().UTC()})
	}
}

// purgeChatHistory deletes the history past its retention, in every
// business, through a definer that reads only the expiry.
func (s *Server) purgeChatHistory(ctx context.Context) error {
	_, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_chat_history_purge()`)
	return err
}

// --- the web as a channel ---

// TaskiemButton is a button of Taskiem's: pressable (with its id) while
// the answer it came with is on the page; by its title only in the
// history.
type TaskiemButton struct {
	ID    string `json:"id,omitempty"`
	Title string `json:"title"`
}

// TaskiemMessage is a message of the Taskiem conversation.
type TaskiemMessage struct {
	ID      uuid.UUID       `json:"id"`
	Channel string          `json:"channel"`
	Inbound bool            `json:"inbound"`
	Kind    string          `json:"kind"`
	Text    string          `json:"text"`
	Buttons []TaskiemButton `json:"buttons,omitempty"`
	// Link is a page Taskiem's answer opens (Telegram's link button).
	Link      *whatsapp.LinkButton `json:"-"`
	CreatedAt time.Time            `json:"created_at"`
}

// webLive collects what Taskiem says to the person while their message
// on the web is answered: the answer, with its buttons live.
type webLive struct {
	user uuid.UUID
	mu   sync.Mutex
	msgs []TaskiemMessage
}

func (l *webLive) add(m TaskiemMessage) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.msgs = append(l.msgs, m)
}

type webLiveKey struct{}

func liveOf(ctx context.Context) *webLive {
	l, _ := ctx.Value(webLiveKey{}).(*webLive)
	return l
}

func webAddr(user uuid.UUID) string { return "web:" + user.String() }

func webUser(addr string) (uuid.UUID, bool) {
	s, ok := strings.CutPrefix(addr, "web:")
	if !ok {
		return uuid.Nil, false
	}
	u, err := uuid.Parse(s)
	return u, err == nil
}

// webChannel is the Chat page's Taskiem conversation in one business:
// what is sent there is kept in the history, and, while the person's
// message is answered, is the answer.
type webChannel struct {
	s      *Server
	tenant uuid.UUID
}

var _ chatChannel = (*webChannel)(nil)

func (w *webChannel) Name() string               { return "web" }
func (w *webChannel) OwnTenant() uuid.UUID       { return w.tenant }
func (w *webChannel) Prefix(string) string       { return "" }
func (w *webChannel) linkedTenants() []uuid.UUID { return []uuid.UUID{w.tenant} }

func (w *webChannel) Contact(context.Context, string) (whatsapp.Contact, error) {
	t := w.tenant
	now := time.Now()
	return whatsapp.Contact{Tenant: &t, LastInbound: &now}, nil
}

func (w *webChannel) Touch(context.Context, string, bool, bool, *uuid.UUID) error { return nil }

func (w *webChannel) Send(ctx context.Context, addr string, m whatsapp.Message) (string, error) {
	user, ok := webUser(addr)
	if !ok {
		return "", errors.New("web: not a web address")
	}
	if strings.TrimSpace(m.Text) == "" {
		return "", whatsapp.ErrNothingToSend
	}
	tenant := m.Tenant
	if tenant == uuid.Nil {
		tenant = w.tenant
	}
	id := w.s.keepHistory(ctx, tenant, user, "web", false, "text", m.Text, buttonTitles(m.Buttons))
	if live := liveOf(ctx); live != nil && live.user == user && tenant == w.tenant {
		msg := TaskiemMessage{ID: id, Channel: "web", Kind: "text", Text: strings.TrimSpace(m.Text), CreatedAt: time.Now().UTC()}
		for _, b := range m.Buttons {
			msg.Buttons = append(msg.Buttons, TaskiemButton{ID: b.ID, Title: b.Title})
		}
		if m.Link != nil {
			msg.Text += "\n" + m.Link.Title + ": " + m.Link.URL
		}
		live.add(msg)
	}
	if id == uuid.Nil {
		id = uuid.Must(uuid.NewV7())
	}
	return id.String(), nil
}

// --- the API ---

func (s *Server) taskiemChatRoutes(r chi.Router) {
	r.Get("/taskiem-chat", s.taskiemHistory)
	r.Post("/taskiem-chat", s.taskiemSay)
	r.Post("/taskiem-chat/files", s.taskiemSend)
}

// webMediaID names a file that came with a web message (chat.upload).
const webMediaID = "web"

type taskiemChatOut struct {
	Messages []TaskiemMessage `json:"messages"`
}

// taskiemSay is POST /v1/taskiem-chat: the person's message to Taskiem
// on the web (text, or reply, a button's id, with its title as text),
// read as one typed on WhatsApp; the answer is what Taskiem said, with
// its buttons live.
func (s *Server) taskiemSay(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		s.fail(w, r, errForbidden)
		return
	}
	var in struct {
		Text  string `json:"text"`
		Reply string `json:"reply"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	in.Text = strings.TrimSpace(in.Text)
	if (in.Text == "" && in.Reply == "") || utf8.RuneCountInString(in.Text) > maxHistoryText || len(in.Reply) > 2048 {
		s.fail(w, r, errBadRequest)
		return
	}
	msg := whatsapp.Inbound{Type: "text", Text: in.Text, Reply: in.Reply}
	if in.Reply != "" {
		msg.Type = "interactive"
	}
	s.taskiemAnswer(w, r, p, msg, nil)
}

// taskiemAnswer reads a message on the web as one typed on WhatsApp, and
// answers what Taskiem said.
func (s *Server) taskiemAnswer(w http.ResponseWriter, r *http.Request, p *Principal, msg whatsapp.Inbound, upload []byte) {
	if !s.limiter("web-chat:"+p.UserID.String(), 3*time.Second, 20).Allow() {
		writeErr(w, http.StatusTooManyRequests, "slow down: a moment between messages")
		return
	}
	s.chatHooks()
	live := &webLive{user: p.UserID}
	ctx := context.WithValue(r.Context(), webLiveKey{}, live)
	c := &chat{ch: &webChannel{s: s, tenant: p.TenantID}, addr: webAddr(p.UserID), in: msg, upload: upload}
	err := s.converse(ctx, c, p.UserID)
	clear(upload) // the file is not kept
	if err != nil {
		s.fail(w, r, err)
		return
	}
	live.mu.Lock()
	out := slices.Clone(live.msgs)
	live.mu.Unlock()
	writeJSON(w, http.StatusOK, taskiemChatOut{Messages: out})
}

// taskiemSend is POST /v1/taskiem-chat/files: a voice note or a photo
// sent to Taskiem on the web, as a multipart form (file; text, a photo's
// caption). Read as one sent on WhatsApp: a voice note is transcribed
// (when the business has voice notes on) and read as typed; a photo with
// a caption adds a product, or is the logo the set-up asked for. The file
// is held in memory only.
func (s *Server) taskiemSend(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		s.fail(w, r, errForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, media.MaxVoiceBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil { //nolint:gosec // the body is bounded by MaxBytesReader above
		s.fail(w, r, fmt.Errorf("%w: send the file as a multipart form with a file field", errBadRequest))
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	f, _, err := r.FormFile("file")
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: the form has no file", errBadRequest))
		return
	}
	data, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil || len(data) == 0 {
		s.fail(w, r, fmt.Errorf("%w: the file could not be read", errBadRequest))
		return
	}
	caption := strings.TrimSpace(r.FormValue("text"))
	if utf8.RuneCountInString(caption) > maxHistoryText {
		s.fail(w, r, errBadRequest)
		return
	}
	msg := whatsapp.Inbound{}
	switch ctype := http.DetectContentType(data); {
	case media.AudioType(data) != "":
		msg.Type, msg.Media = "audio", &whatsapp.Media{ID: webMediaID, MimeType: media.AudioType(data), Voice: true}
	case ctype == "image/jpeg" || ctype == "image/png" || ctype == "image/webp":
		msg.Type, msg.Image, msg.Text = "image", &whatsapp.Media{ID: webMediaID, MimeType: ctype}, caption
	default:
		s.fail(w, r, fmt.Errorf("%w: send a voice note or a photo (JPEG, PNG or WebP)", errBadRequest))
		return
	}
	s.taskiemAnswer(w, r, p, msg, data)
}

// taskiemHistory is GET /v1/taskiem-chat: the person's own conversation
// with Taskiem in this business, on every channel, oldest first: the
// newest page, before (older) or after (newer, to follow it).
func (s *Server) taskiemHistory(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	out := taskiemChatOut{Messages: []TaskiemMessage{}}
	if p.UserID == uuid.Nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	q := r.URL.Query()
	var before, after *time.Time
	for k, dst := range map[string]**time.Time{"before": &before, "after": &after} {
		if v := q.Get(k); v != "" {
			t, err := time.Parse(time.RFC3339Nano, v)
			if err != nil {
				s.fail(w, r, errBadRequest)
				return
			}
			*dst = &t
		}
	}
	err := s.tx(r, func(tx pgx.Tx) error {
		sql := `SELECT id, channel, inbound, kind, body, buttons, created_at FROM chat_history
			WHERE tenant_id = $1 AND user_id = $2 AND expires_at > now()`
		args := []any{p.TenantID, p.UserID}
		order := ` ORDER BY created_at DESC, id DESC LIMIT 50`
		switch {
		case after != nil:
			sql += ` AND created_at > $3`
			args = append(args, *after)
			order = ` ORDER BY created_at, id LIMIT 200`
		case before != nil:
			sql += ` AND created_at < $3`
			args = append(args, *before)
		}
		rows, err := tx.Query(r.Context(), sql+order, args...)
		if err != nil {
			return err
		}
		type row struct {
			m       TaskiemMessage
			env     map[string]any
			buttons []string
		}
		got, err := pgx.CollectRows(rows, func(rr pgx.CollectableRow) (row, error) {
			var x row
			err := rr.Scan(&x.m.ID, &x.m.Channel, &x.m.Inbound, &x.m.Kind, &x.env, &x.buttons, &x.m.CreatedAt)
			return x, err
		})
		if err != nil {
			return err
		}
		for _, x := range got {
			v, err := s.Store.PII.OpenTx(r.Context(), tx, p.TenantID, x.env)
			switch {
			case errors.Is(err, pii.ErrErased):
				x.m.Text = pii.Erased
			case err != nil:
				return err
			default:
				x.m.Text, _ = v.(string)
			}
			for _, b := range x.buttons {
				x.m.Buttons = append(x.m.Buttons, TaskiemButton{Title: b})
			}
			out.Messages = append(out.Messages, x.m)
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if after == nil {
		slices.Reverse(out.Messages)
	}
	writeJSON(w, http.StatusOK, out)
}
