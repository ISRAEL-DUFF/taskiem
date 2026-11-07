package api

// Diary lines (docs/spec/chat-tools.md §3): a change made with a chat tool
// (a price changed, stock in, a service added, an order accepted), told as
// a line where the tool was used and on the person's own chat apps. A line
// reaches only the person who made the change: their Taskiem conversation,
// or their own side of a conversation with a business (the other business
// never sees it), and their own WhatsApp and Telegram.

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/links"
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
	s.queueDiaryTell(p.TenantID, p.UserID, text)
	writeJSON(w, http.StatusCreated, map[string]any{"line": out})
}
