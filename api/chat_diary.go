package api

// Diary lines (docs/spec/chat-tools.md §3): a change made with a chat tool
// (a price changed, stock in, a service added, an order accepted), told as
// a line where the tool was used and on the person's own chat apps. A line
// reaches only the person who made the change: their Taskiem conversation,
// or their own side of a conversation with a business (the other business
// never sees it), and their own WhatsApp and Telegram.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// postChatDiary answers POST /v1/chat-diary with {"where": "taskiem" or a
// link, "text": "Price of Rice: ₦76,000 → ₦78,000"}.
func (s *Server) postChatDiary(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Where string `json:"where"`
		Text  string `json:"text"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	text := strings.Join(strings.Fields(in.Text), " ")
	if text == "" || len([]rune(text)) > 300 {
		s.fail(w, r, fmt.Errorf("%w: a diary line is 1 to 300 characters", errBadRequest))
		return
	}
	if p.UserID == uuid.Nil {
		s.fail(w, r, errForbidden)
		return
	}
	if !s.limiter("chat-diary:"+p.UserID.String(), time.Minute, 60).Allow() {
		writeErr(w, http.StatusTooManyRequests, "slow down: a moment between changes")
		return
	}
	var on bool
	if err := s.tx(r, func(tx pgx.Tx) (err error) {
		on, err = chatToolsOn(r.Context(), tx, p.TenantID)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if !on {
		s.fail(w, r, fmt.Errorf("%w: chat tools are off for this business", errConflict))
		return
	}
	var out any
	if in.Where == "taskiem" {
		id := s.keepHistory(r.Context(), p.TenantID, p.UserID, "web", false, "diary", text, nil)
		if id == uuid.Nil {
			s.fail(w, r, fmt.Errorf("%w: the line could not be kept", errConflict))
			return
		}
		out = TaskiemMessage{ID: id, Channel: "web", Kind: "diary", Text: text, CreatedAt: time.Now().UTC()}
	} else {
		link, err := uuid.Parse(in.Where)
		if err != nil {
			s.fail(w, r, fmt.Errorf("%w: where is taskiem or a link", errBadRequest))
			return
		}
		if !p.Can(PermChatTrade) {
			s.fail(w, r, errForbidden)
			return
		}
		var m links.Message
		if err := db.InTenantTx(r.Context(), s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
			l, err := links.GetTx(r.Context(), tx, link)
			if err != nil {
				return err
			}
			if l.State != links.Active {
				return fmt.Errorf("%w: you are no longer linked with %s", links.ErrState, l.Name)
			}
			var name string
			if err := tx.QueryRow(r.Context(), `SELECT name FROM users WHERE id = $1`, p.UserID).Scan(&name); err != nil {
				return err
			}
			m, err = links.DiaryTx(r.Context(), tx, p.TenantID, link, text, links.Sender{ID: p.Actor(), FirstName: firstWord(name)})
			return err
		}); err != nil {
			s.fail(w, r, linkMessageErr(err))
			return
		}
		out = m
	}
	s.background(r.Context(), "chat diary", func(ctx context.Context) { s.tellDiary(ctx, p.UserID, p.TenantID, text) })
	writeJSON(w, http.StatusCreated, map[string]any{"line": out})
}

// tellDiary tells the person on their own chat apps (outside WhatsApp's
// window as the alert template), so a change made on the web is still
// told where they chat.
func (s *Server) tellDiary(ctx context.Context, user, tenant uuid.UUID, text string) {
	type to struct{ channel, addr string }
	var sends []to
	var org string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&org); err != nil {
			return err
		}
		for _, channel := range []string{"whatsapp", "telegram"} {
			addrs, err := chatAddrsTx(ctx, tx, channel, []uuid.UUID{user})
			if err != nil {
				return err
			}
			if a, ok := addrs[user]; ok {
				sends = append(sends, to{channel, a})
			}
		}
		return nil
	})
	if err != nil {
		s.Logger.Warn("chat diary: telling", "err", err)
		return
	}
	link := strings.TrimRight(s.PublicURL, "/") + "/chat"
	// The line is in the history already: the phone's copy is not kept again.
	ctx = unkept(ctx)
	for _, t := range sends {
		ch, err := s.chatChannelFor(ctx, t.channel, tenant)
		if err != nil {
			continue
		}
		words := diaryWords(s.chooseLang(ctx, tenant, t.addr, "").Tag, text)
		tpl := whatsapp.TplAlert
		msg := whatsapp.Message{Tenant: tenant, Text: words, Template: &tpl, Vars: map[string]string{"tenant": org, "title": words, "link": link}}
		if _, err := ch.Send(ctx, t.addr, msg); err != nil {
			s.Logger.Warn("chat diary: the chat", "channel", t.channel, "err", err)
		}
	}
}

// diaryWords is a diary line as the chat apps say it.
func diaryWords(_ lang.Tag, text string) string {
	return "✓ " + whatsapp.SafeText(text)
}
