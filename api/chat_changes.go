package api

// Changes told as diary lines (docs/spec/chat-tools.md §4): a change a
// person makes, on any page or with a chat tool (a price, stock, a new
// product or service, a price list, an order's step), with chat tools
// on, is a line in their own Taskiem conversation and is told on their
// own chat apps, as their "changes" setting says. Changes made close
// together are told in one message.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/notices"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// diaryWindow is how long changes wait to be told together on the chat
// apps; DiaryWindow on the Server, when set, overrides it (tests).
const diaryWindow = time.Minute

// diaryQueue holds the changes waiting to be told, per person.
type diaryQueue struct {
	mu      sync.Mutex
	pending map[string]*diaryPending
}

type diaryPending struct {
	tenant, user uuid.UUID
	texts        []string
}

func (s *Server) diaryWait() time.Duration {
	if s.DiaryWindow > 0 {
		return s.DiaryWindow
	}
	return diaryWindow
}

// queueDiaryTell keeps a change to be told on the person's chat apps,
// with any others made within the window.
func (s *Server) queueDiaryTell(tenant, user uuid.UUID, text string) {
	key := tenant.String() + ":" + user.String()
	s.diary.mu.Lock()
	defer s.diary.mu.Unlock()
	if s.diary.pending == nil {
		s.diary.pending = map[string]*diaryPending{}
	}
	if p, ok := s.diary.pending[key]; ok {
		p.texts = append(p.texts, text)
		return
	}
	s.diary.pending[key] = &diaryPending{tenant: tenant, user: user, texts: []string{text}}
	time.AfterFunc(s.diaryWait(), func() {
		s.diary.mu.Lock()
		p := s.diary.pending[key]
		delete(s.diary.pending, key)
		s.diary.mu.Unlock()
		if p != nil {
			s.background(context.Background(), "chat diary", func(ctx context.Context) { s.tellDiary(ctx, p.user, p.tenant, p.texts) })
		}
	})
}

// diaryChange tells a change the person behind p made, when their business
// has chat tools on: a diary line in their Taskiem conversation, and on
// their chat apps (grouped). Nothing for an API key or a run.
func (s *Server) diaryChange(ctx context.Context, p *Principal, texts ...string) {
	if p == nil || p.UserID == uuid.Nil || p.TenantID == uuid.Nil {
		return
	}
	var on bool
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) (err error) {
		on, err = chatToolsOn(ctx, tx, p.TenantID)
		return err
	}); err != nil || !on {
		return
	}
	for _, t := range texts {
		t = strings.Join(strings.Fields(t), " ")
		if t == "" {
			continue
		}
		if len([]rune(t)) > 300 {
			t = string([]rune(t)[:299]) + "…"
		}
		s.keepHistory(ctx, p.TenantID, p.UserID, "web", false, "diary", t, nil)
		s.queueDiaryTell(p.TenantID, p.UserID, t)
	}
}

// --- the words of each change ---

func naira(kobo int64) string { return storefront.FormatNaira(kobo) }

// qty is n of a unit: "1 bag", "20 bags", "4".
func qty(n int64, unit string) string {
	unit = strings.TrimSpace(unit)
	if unit == "" {
		return strconv.FormatInt(n, 10)
	}
	if n == 1 || n == -1 {
		return fmt.Sprintf("%d %s", n, unit)
	}
	if strings.HasSuffix(unit, "s") {
		return fmt.Sprintf("%d %s", n, unit)
	}
	if strings.HasSuffix(unit, "x") || strings.HasSuffix(unit, "ch") || strings.HasSuffix(unit, "sh") {
		return fmt.Sprintf("%d %ses", n, unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func priceChangeWords(name string, from *int64, to int64) string {
	if from == nil {
		return fmt.Sprintf("Price of %s set to %s", name, naira(to))
	}
	return fmt.Sprintf("Price of %s: %s → %s", name, naira(*from), naira(to))
}

// orderStepWords is an order's step in a line: "Accepted order AD-1 from Rivers Foods".
func orderStepWords(step, reference, buyer string) string {
	switch step {
	case "accept":
		return fmt.Sprintf("Accepted order %s from %s", reference, buyer)
	case "decline":
		return fmt.Sprintf("Declined order %s from %s", reference, buyer)
	case "ready":
		return fmt.Sprintf("Order %s from %s is ready", reference, buyer)
	case "dispatched":
		return fmt.Sprintf("Order %s from %s is on its way", reference, buyer)
	case "delivered":
		return fmt.Sprintf("Delivered order %s to %s", reference, buyer)
	case "cancel":
		return fmt.Sprintf("Cancelled order %s from %s", reference, buyer)
	}
	return fmt.Sprintf("Order %s from %s: %s", reference, buyer, strings.ReplaceAll(step, "_", " "))
}

// buyerStepWords is a diary line for a buyer's own step with a supplier:
// an order sent, a quote confirmed, all of it come, a bill paid from
// Taskiem, paid outside it, or questioned.
func buyerStepWords(step, reference, supplier string, kobo int64) string {
	switch step {
	case "send":
		return fmt.Sprintf("Sent order %s to %s: %s", reference, supplier, naira(kobo))
	case "confirm":
		return fmt.Sprintf("Confirmed %s's quote for order %s", supplier, reference)
	case "all_came":
		return fmt.Sprintf("All of order %s came from %s", reference, supplier)
	case "not_all_came":
		return fmt.Sprintf("Not all of order %s came from %s", reference, supplier)
	case "pay":
		return fmt.Sprintf("Started paying %s from Taskiem: %s to %s", reference, naira(kobo), supplier)
	case "declare":
		return fmt.Sprintf("Told %s you paid %s of %s outside Taskiem", supplier, naira(kobo), reference)
	case "query":
		return fmt.Sprintf("Questioned %s from %s", reference, supplier)
	}
	return fmt.Sprintf("%s with %s: %s", reference, supplier, strings.ReplaceAll(step, "_", " "))
}

// tellDiary tells the person on their own chat apps, unless their changes
// setting says not to (outside WhatsApp's window as the alert template):
// one change as it is, several as one message.
func (s *Server) tellDiary(ctx context.Context, user, tenant uuid.UUID, texts []string) {
	if len(texts) == 0 {
		return
	}
	type to struct{ channel, addr string }
	var sends []to
	var org string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&org); err != nil {
			return err
		}
		chat, _, err := notices.Wants(ctx, tx, []uuid.UUID{user}, notices.GroupChanges)
		if err != nil || len(chat) == 0 {
			return err
		}
		for _, channel := range []string{"whatsapp", "telegram", "slack"} {
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
	words := diaryWords(texts)
	link := strings.TrimRight(s.PublicURL, "/") + "/chat?with=taskiem"
	// The lines are in the history already: the phone's copy is not kept again.
	ctx = unkept(ctx)
	for _, t := range sends {
		ch, err := s.chatChannelFor(ctx, t.channel, tenant)
		if err != nil {
			continue
		}
		tpl := whatsapp.TplAlert
		title := words
		if len(texts) > 1 {
			title = fmt.Sprintf("%d changes made", len(texts))
		}
		msg := whatsapp.Message{Tenant: tenant, Text: words, Template: &tpl, Vars: map[string]string{"tenant": org, "title": title, "link": link}}
		if _, err := ch.Send(ctx, t.addr, msg); err != nil {
			s.Logger.Warn("chat diary: the chat", "channel", t.channel, "err", err)
		}
	}
}

// diaryWords is the changes as the chat apps say them: "✓ …", or a list.
func diaryWords(texts []string) string {
	if len(texts) == 1 {
		return "✓ " + whatsapp.SafeText(texts[0])
	}
	var b strings.Builder
	fmt.Fprintf(&b, "✓ %d changes:", len(texts))
	for _, t := range texts {
		b.WriteString("\n• " + whatsapp.SafeText(t))
	}
	return b.String()
}
