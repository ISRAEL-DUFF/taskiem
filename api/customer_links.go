package api

// Chat links (docs/spec/taskiem-for-everyone.md §8.2, §9, §13, §15; stage
// 2, stream B). A business's chat link, https://<host>/m/{code}, opens a
// conversation with it in the app (the path is in appPaths) or the
// browser. Here:
//   - the business's card for a code, for anyone (GET /v1/chat-links/{code});
//   - the owner's link, its words to paste and QR code, "Make a new link"
//     and the link's settings (/v1/customers/chat-link, chat-settings);
//   - the page /m/{code} when this server has no web app to serve it;
//   - a person's own storefront orders, found by their verified phone
//     (/v1/me/storefront-orders), and "These are mine";
//   - the storefront's "Chat with us" (storefrontChat, at publishing).
// The conversations themselves, and the customer door, are elsewhere
// (stream A); they resolve codes with s.ChatLinks.Resolve and tell people
// a business answered with s.tellAnswered.

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/chatlinks"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/phoneindex"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// chatLinkWords are the words to paste with a link.
func chatLinkWords(business, link string) string {
	return "Chat with " + business + " on Taskiem: " + link
}

// chatLinkGone is what an old link says.
const chatLinkGone = "This link no longer works. Ask the business for its new link."

// tellAnswered tells a person in the browser chat that a business
// answered them (§9): stream A's conversations call it after keeping a
// business's reply. Errors are logged, never the reply's.
func (s *Server) tellAnswered(ctx context.Context, a chatlinks.Answer) {
	t := s.Answered
	if t == nil {
		return
	}
	if err := t.TellAnswered(ctx, a); err != nil && ctx.Err() == nil {
		s.Logger.Warn("chat: telling a customer the business answered", "err", err, "tenant", a.Tenant)
	}
}

// answerWords is what a push says of a business's message to a person:
// its words, or what it is (a card's title, a photo, a voice note).
func answerWords(m customerMessageView) string {
	if strings.TrimSpace(m.Body) != "" {
		return m.Body
	}
	switch {
	case m.Card != nil && m.Card.Title != "":
		return m.Card.Title
	case m.Card != nil:
		return "Sent you a card"
	case m.Attachment != nil && m.Attachment.Kind == "photo":
		return "Sent you a photo"
	case m.Attachment != nil && m.Attachment.Kind == "voice":
		return "Sent you a voice note"
	case m.Attachment != nil:
		return "Sent you a file"
	}
	return ""
}

// storefrontChat is what a storefront being published offers for chat:
// its chat link, and the owner's choice of button.
func (s *Server) storefrontChat(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (storefront.Chat, error) {
	if !s.ChatLinks.Ready() {
		return storefront.Chat{Button: chatlinks.ButtonWhatsApp}, nil
	}
	st, err := chatlinks.SettingsTx(ctx, tx, tenant)
	if err != nil {
		return storefront.Chat{}, err
	}
	if !st.TakesChats {
		return storefront.Chat{Button: chatlinks.ButtonWhatsApp}, nil
	}
	link, err := s.ChatLinks.LinkFor(ctx, tx, tenant)
	if err != nil {
		return storefront.Chat{}, err
	}
	return storefront.Chat{Link: link, Button: st.StorefrontButton}, nil
}

// republishForChat publishes a published storefront again after its chat
// link or button changed (or chats were turned on or off), so the page
// offers the new one. Only the button changes: the page as published is
// copied with it (storefront.RepublishChat), so the owner's unpublished
// draft stays unpublished and nothing goes to the moderators again. Its
// own transaction: a page that cannot be published now keeps the old
// button, and the change itself stands.
func (s *Server) republishForChat(r *http.Request) {
	p := principalFrom(r.Context())
	err := s.tx(r, func(tx pgx.Tx) error {
		chat, err := s.storefrontChat(r.Context(), tx, p.TenantID)
		if err != nil {
			return err
		}
		_, err = storefront.RepublishChat(r.Context(), tx, p.TenantID, p.Actor(), chat)
		if errors.Is(err, storefront.ErrNotFound) {
			return nil
		}
		return err
	})
	if err != nil {
		s.Logger.Warn("chat link: the storefront was not published again", "err", err, "tenant", p.TenantID)
	}
}

// --- the business's card, for anyone with the link ---

type chatCardOut struct {
	chatlinks.Card
	Code string `json:"code"`
	Link string `json:"link"`
	// Words are the business's words to share the link with.
	Words string `json:"words"`
	// App is where to get the Taskiem app (TASKIEM_APP_GET_URL), for the
	// page's Get the Taskiem app; empty where it is not listed.
	App string `json:"app,omitempty"`
}

// chatCardFor reads the card of the business a code names.
func (s *Server) chatCardFor(ctx context.Context, code string) (chatCardOut, error) {
	tenant, err := s.ChatLinks.Resolve(ctx, code)
	if err != nil {
		return chatCardOut{}, err
	}
	code = chatlinks.Clean(code)
	out := chatCardOut{Code: code, Link: s.ChatLinks.URL(code)}
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		out.Card, err = chatlinks.CardTx(ctx, tx, tenant, s.previewDomain(), time.Now())
		return err
	})
	out.Words = chatLinkWords(out.Name, out.Link)
	out.App = s.AppGetURL
	return out, err
}

// getChatLinkCard is GET /v1/chat-links/{code}, for anyone: the business's
// name, photo, hours and whether it takes chats. 410 for a link replaced
// in the last 30 days, 404 for any other that names no business.
func (s *Server) getChatLinkCard(w http.ResponseWriter, r *http.Request) {
	if !s.limiter("chat-link:"+limitIP(r), time.Minute, 60).Allow() {
		writeErr(w, http.StatusTooManyRequests, "Too many links opened. Wait a minute, then try again.")
		return
	}
	if !s.ChatLinks.Ready() {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	out, err := s.chatCardFor(r.Context(), chi.URLParam(r, "code"))
	switch {
	case errors.Is(err, chatlinks.ErrReplaced):
		writeJSON(w, http.StatusGone, map[string]any{"error": chatLinkGone, "gone": true})
	case errors.Is(err, chatlinks.ErrNotFound):
		writeErr(w, http.StatusNotFound, "This link is not a Taskiem chat link. Check it, or ask the business for it.")
	case err != nil:
		s.fail(w, r, err)
	default:
		w.Header().Set("Cache-Control", "public, max-age=60")
		writeJSON(w, http.StatusOK, out)
	}
}

// --- the business's own link ---

type chatLinkOut struct {
	chatlinks.Link
	Words    string             `json:"words"`
	QR       string             `json:"qr"`
	Settings chatlinks.Settings `json:"settings"`
}

func (s *Server) chatLinkView(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, k chatlinks.Link) (chatLinkOut, error) {
	var name string
	if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&name); err != nil {
		return chatLinkOut{}, err
	}
	st, err := chatlinks.SettingsTx(ctx, tx, tenant)
	if err != nil {
		return chatLinkOut{}, err
	}
	return chatLinkOut{Link: k, Words: chatLinkWords(name, k.URL), QR: "/v1/customers/chat-link/qr", Settings: st}, nil
}

func (s *Server) chatLinksOff(w http.ResponseWriter) bool {
	if s.ChatLinks.Ready() {
		return false
	}
	writeErr(w, http.StatusServiceUnavailable, "Chat links are not set up on this Taskiem.")
	return true
}

// getChatLink is GET /v1/customers/chat-link: the business's link (made
// the first time), its words to paste and its settings.
func (s *Server) getChatLink(w http.ResponseWriter, r *http.Request) {
	if s.chatLinksOff(w) {
		return
	}
	p := principalFrom(r.Context())
	var out chatLinkOut
	err := s.tx(r, func(tx pgx.Tx) error {
		k, err := s.ChatLinks.CurrentTx(r.Context(), tx, p.TenantID, p.ActorType()+":"+p.Actor())
		if err != nil {
			return err
		}
		out, err = s.chatLinkView(r.Context(), tx, p.TenantID, k)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// newChatLink is POST /v1/customers/chat-link: "Make a new link". The old
// one says "This link no longer works" for 30 days; a published storefront
// is published again with the new one.
func (s *Server) newChatLink(w http.ResponseWriter, r *http.Request) {
	if s.chatLinksOff(w) {
		return
	}
	p := principalFrom(r.Context())
	var out chatLinkOut
	err := s.tx(r, func(tx pgx.Tx) error {
		k, err := s.ChatLinks.ReplaceTx(r.Context(), tx, p.TenantID, p.ActorType()+":"+p.Actor())
		if err != nil {
			return err
		}
		if out, err = s.chatLinkView(r.Context(), tx, p.TenantID, k); err != nil {
			return err
		}
		return auditTx(r, tx, "chat_link.replaced", "chat_link", nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.republishForChat(r)
	writeJSON(w, http.StatusOK, out)
}

// chatLinkQR is GET /v1/customers/chat-link/qr: the link as a QR code to
// print, SVG or (?format=png) PNG.
func (s *Server) chatLinkQR(w http.ResponseWriter, r *http.Request) {
	if s.chatLinksOff(w) {
		return
	}
	p := principalFrom(r.Context())
	var k chatlinks.Link
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		k, err = s.ChatLinks.CurrentTx(r.Context(), tx, p.TenantID, p.ActorType()+":"+p.Actor())
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Query().Get("format") == "png" {
		png, err := storefront.QRPNG(k.URL)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
		return
	}
	svg, err := storefront.QRSVG(k.URL)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	_, _ = io.WriteString(w, svg)
}

// putChatSettings is PUT /v1/customers/chat-settings: whether the business
// takes chats, its storefront's button, and the link in updates on the
// shared number. A published storefront is published again when its
// button changes.
func (s *Server) putChatSettings(w http.ResponseWriter, r *http.Request) {
	var in chatlinks.SettingsIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var before, after chatlinks.Settings
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if before, err = chatlinks.SettingsTx(r.Context(), tx, p.TenantID); err != nil {
			return err
		}
		after, err = chatlinks.SaveSettingsTx(r.Context(), tx, p.TenantID, in, p.ActorType()+":"+p.Actor())
		if errors.Is(err, chatlinks.ErrInvalid) {
			return fmt.Errorf("%w: %s", errBadRequest, strings.TrimPrefix(err.Error(), "chatlinks: invalid: "))
		}
		if err != nil {
			return err
		}
		return auditTx(r, tx, "chat_link.settings", "chat_link", map[string]any{"takes_chats": after.TakesChats,
			"storefront_button": after.StorefrontButton, "link_in_updates": after.LinkInUpdates, "link_whatsapp": after.LinkWhatsApp})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if before.StorefrontButton != after.StorefrontButton || before.TakesChats != after.TakesChats {
		s.republishForChat(r)
	}
	writeJSON(w, http.StatusOK, after)
}

// --- a person's own storefront orders (§13) ---

// myStorefrontOrders is GET /v1/me/storefront-orders (?business= one
// business's): the storefront orders and bookings made with the person's
// verified phone, across businesses, so "Chat with {business} about this
// order" finds them. Orders made before the phone was on the account are
// only counted (waiting_count) until the person says they are theirs:
// nothing of them is shown, so a number that was someone else's never
// shows that person's orders.
func (s *Server) myStorefrontOrders(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil {
		writeErr(w, http.StatusForbidden, "Only a person's own session can list their orders.")
		return
	}
	var only uuid.UUID
	if b := r.URL.Query().Get("business"); b != "" {
		id, err := uuid.Parse(b)
		if err != nil {
			s.fail(w, r, fmt.Errorf("%w: business is a business's id", errBadRequest))
			return
		}
		only = id
	}
	limit := 50
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 200 {
		limit = v
	}
	if !s.PhoneIndex.Ready() {
		writeJSON(w, http.StatusOK, map[string]any{"orders": []phoneindex.Order{}, "waiting": false, "waiting_count": 0})
		return
	}
	list, err := s.PhoneIndex.Orders(r.Context(), p.UserID, only, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	waiting, err := s.PhoneIndex.Waiting(r.Context(), p.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": list, "waiting": waiting > 0, "waiting_count": waiting})
}

// confirmStorefrontOrders is POST /v1/me/storefront-orders/confirm: "These
// are mine", for the orders made with the person's phone before it was on
// their account. 409 when they have no phone.
func (s *Server) confirmStorefrontOrders(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil || !s.PhoneIndex.Ready() {
		writeErr(w, http.StatusForbidden, "Only a person's own session can confirm their orders.")
		return
	}
	ok, err := s.PhoneIndex.Confirm(r.Context(), p.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusConflict, "Add your phone number first: your orders are found by it.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"confirmed": true})
}

// --- the page /m/{code}, when no web app is served here ---

const chatLinkSource = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{if .Gone}}This link no longer works{{else if .Missing}}Link not found{{else}}Chat with {{.Card.Name}}{{end}} · Taskiem</title>
{{if .Card.Name}}<meta property="og:title" content="Chat with {{.Card.Name}}">{{end}}
<style>
:root{color-scheme:light dark;--bg:#fafaf7;--fg:#1c1c1a;--muted:#5f5f5a;--card:#fff;--line:#e4e4de;--accent:#0f6b4f;--on:#fff}
@media (prefers-color-scheme:dark){:root{--bg:#121211;--fg:#f1f1ec;--muted:#a9a9a2;--card:#1d1d1b;--line:#33332f;--accent:#3fb489;--on:#08140f}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.5 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif}
main{max-width:28rem;margin:0 auto;padding:2rem 1rem}.card{background:var(--card);border:1px solid var(--line);border-radius:16px;padding:1.5rem;text-align:center}
img{width:96px;height:96px;border-radius:50%;object-fit:cover}h1{font-size:1.5rem;margin:.75rem 0 .25rem}p{margin:.25rem 0;color:var(--muted)}
.btn{display:block;margin-top:1rem;padding:.8rem 1rem;border-radius:999px;text-decoration:none;font-weight:600;border:1px solid var(--accent);color:var(--accent)}
.btn.main{background:var(--accent);color:var(--on)}ul{list-style:none;padding:0;margin:1rem 0 0;color:var(--muted);font-size:.9rem}
</style></head><body><main><div class="card">
{{if .Gone}}<h1>This link no longer works</h1><p>The business made a new link. Ask them for it.</p>
{{else if .Missing}}<h1>Link not found</h1><p>Check the link, or ask the business for it.</p>
{{else}}{{with .Photo}}<img src="{{.}}" alt="">{{end}}
<h1>{{.Card.Name}}</h1>{{with .Card.Tagline}}<p>{{.}}</p>{{end}}{{with .Card.Area}}<p>{{.}}</p>{{end}}
{{if .Card.OpenNow}}<p>{{if deref .Card.OpenNow}}Open now{{else}}Closed now{{end}}</p>{{end}}
{{if .Card.TakesChats}}<a class="btn main" href="{{.Chat}}">{{if .Card.WhatsApp}}Chat on Taskiem{{else}}Chat here in your browser{{end}}</a>{{else if not .Card.WhatsApp}}<p>{{.Card.Name}} is not taking chats here right now.</p>{{end}}
{{with .Card.WhatsApp}}<a class="btn{{if not $.Card.TakesChats}} main{{end}}" href="{{.Link}}" rel="noopener">Chat on WhatsApp</a>{{end}}
{{with .App}}<a class="btn" href="{{.}}">Get the Taskiem app</a>{{end}}
{{with .Hours}}<ul>{{range .}}<li>{{.}}</li>{{end}}</ul>{{end}}{{end}}
</div></main></body></html>`

// smallPhoto is the smallest of a photo's widths that is at least 192
// pixels (else the largest), served over https; "" for none.
func smallPhoto(urls map[string]string) string {
	best, bestW, big, bigW := "", 0, "", 0
	for k, u := range urls {
		w, err := strconv.Atoi(strings.TrimSuffix(k, "w"))
		if err != nil || !strings.HasPrefix(u, "https://") {
			continue
		}
		if w >= 192 && (best == "" || w < bestW) {
			best, bestW = u, w
		}
		if w > bigW {
			big, bigW = u, w
		}
	}
	if best != "" {
		return best
	}
	return big
}

var hoursDays = [7]string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

// hoursLines are a card's hours as lines: "Monday: 08:00 to 18:00".
func hoursLines(h *storefront.Hours) []string {
	if h == nil {
		return nil
	}
	var out []string
	for i, spans := range h.Days {
		if len(spans) == 0 {
			out = append(out, hoursDays[i]+": closed")
			continue
		}
		parts := make([]string, len(spans))
		for j, sp := range spans {
			parts[j] = sp.Open + " to " + sp.Close
		}
		out = append(out, hoursDays[i]+": "+strings.Join(parts, ", "))
	}
	if h.Note != "" {
		out = append(out, h.Note)
	}
	return out
}

// chatLinkHTML serves /m/{code} where this server serves no web app (the
// web app's own page, with the browser chat, is used where it does): the
// business's name, photo and hours, "Chat here in your browser" (signing
// in by phone, then the chat) and "Get the Taskiem app". A business with
// its own WhatsApp number that offers it has both "Chat on Taskiem" and
// "Chat on WhatsApp" (§11).

func (s *Server) chatLinkHTML(w http.ResponseWriter, r *http.Request) {
	if !s.limiter("chat-link:"+limitIP(r), time.Minute, 60).Allow() {
		writeErr(w, http.StatusTooManyRequests, "Too many links opened. Wait a minute, then try again.")
		return
	}
	code := chatlinks.Clean(chi.URLParam(r, "code"))
	data := struct {
		Card          chatlinks.Card
		Gone, Missing bool
		Photo         string
		Chat, App     string
		Hours         []string
	}{App: s.AppGetURL}
	status := http.StatusOK
	if !s.ChatLinks.Ready() {
		data.Missing, status = true, http.StatusNotFound
	} else {
		out, err := s.chatCardFor(r.Context(), code)
		switch {
		case errors.Is(err, chatlinks.ErrReplaced):
			data.Gone, status = true, http.StatusGone
		case errors.Is(err, chatlinks.ErrNotFound):
			data.Missing, status = true, http.StatusNotFound
		case err != nil:
			s.fail(w, r, err)
			return
		default:
			data.Card = out.Card
			if out.Photo != nil {
				data.Photo = smallPhoto(out.Photo.URLs)
			}
			data.Hours = hoursLines(out.Hours)
			data.Chat = strings.TrimSuffix(s.PublicURL, "/") + "/login?next=" + "/m/" + code
		}
	}
	// The business's photo is stored apart from the app.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src https: data:; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(status)
	_ = chatLinkPage.Execute(w, data)
}

var chatLinkPage = template.Must(template.New("m").Funcs(template.FuncMap{"deref": func(b *bool) bool { return b != nil && *b }}).Parse(chatLinkSource))
