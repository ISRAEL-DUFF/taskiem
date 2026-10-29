package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Counts and shrinkage (docs/spec/stock.md §7, K4): the web's and the
// API's door to a count (a sheet, or a CSV the page reads into it) and to
// a period's shrinkage; chat's "count rice 12, indomie 40" and "count",
// asked item by item; the morning summary's line for what was lost.

// countLines turns what was counted, by item id, into a count's lines:
// what Taskiem had, and the cost. full is every item that holds a count
// counted.
func countLines(items []stock.Item, got map[string]int64) (lines []stock.CountLine, full bool, err error) {
	byID := map[string]stock.Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	for id := range got {
		if _, ok := byID[id]; !ok {
			return nil, false, errNoItem
		}
	}
	full = true
	for _, it := range items {
		n, ok := got[it.ID]
		if !ok {
			if it.Stock != nil {
				full = false
			}
			continue
		}
		lines = append(lines, stock.CountLine{ItemID: it.ID, Item: it.Name, Unit: it.Unit, Expected: it.Stock, Counted: n, CostKobo: it.CostKobo})
	}
	return lines, full, nil
}

// recordCount is POST /v1/stock/counts, with an Idempotency-Key: how many
// of each item are on the shelf, and why they differ.
func (s *Server) recordCount(w http.ResponseWriter, r *http.Request) {
	if !s.Stock.Ready() {
		writeErr(w, http.StatusServiceUnavailable, "stock is not available on this platform")
		return
	}
	key, err := requiredKey(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Lines []struct {
			ItemID string `json:"item_id"`
			Count  int64  `json:"count"`
		} `json:"lines"`
		Reason string `json:"reason"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	_, items, err := s.stockItems(r.Context(), p.TenantID)
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	got := map[string]int64{}
	for _, l := range in.Lines {
		if _, dup := got[l.ItemID]; dup {
			writeErr(w, http.StatusBadRequest, "an item is counted twice")
			return
		}
		got[l.ItemID] = l.Count
	}
	lines, full, err := countLines(items, got)
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	c, err := s.Stock.RecordCount(r.Context(), p.TenantID, stock.CountIn{Lines: lines, Full: full, Reason: in.Reason, Actor: p.Actor(), Key: "api-" + key})
	if err != nil {
		s.stockFail(w, r, err)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, "stock.count", c.Reference, map[string]any{"count_id": c.ID, "changed": c.Changed, "full": c.Full})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"count": c})
}

// listCounts is GET /v1/stock/counts: the latest counts.
func (s *Server) listCounts(w http.ResponseWriter, r *http.Request) {
	var list []stock.StockCount
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		list, err = stock.CountsTx(r.Context(), tx, principalFrom(r.Context()).TenantID, 20)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if list == nil {
		list = []stock.StockCount{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"counts": list})
}

// getShrinkage is GET /v1/stock/shrinkage?from=&to= (Lagos days, to
// included; this month by default): received, sold and lost, at cost.
func (s *Server) getShrinkage(w http.ResponseWriter, r *http.Request) {
	now := time.Now().In(storefront.Lagos)
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, storefront.Lagos)
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, storefront.Lagos)
	for _, f := range []struct {
		name string
		to   *time.Time
	}{{"from", &from}, {"to", &to}} {
		if v := r.URL.Query().Get(f.name); v != "" {
			t, err := time.ParseInLocation("2006-01-02", v, storefront.Lagos)
			if err != nil {
				writeErr(w, http.StatusBadRequest, f.name+" is a date like 2026-10-29")
				return
			}
			*f.to = t
		}
	}
	if to.Before(from) || to.Sub(from) > 366*24*time.Hour {
		writeErr(w, http.StatusBadRequest, "from is before to, at most a year apart")
		return
	}
	var out stock.Shrink
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = stock.ShrinkTx(r.Context(), tx, principalFrom(r.Context()).TenantID, from, to.AddDate(0, 0, 1))
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// shrinkDailyLine is what was lost on a day, at cost, for the morning
// summary; "" when nothing was.
func shrinkDailyLine(l lang.Tag, sh stock.Shrink) string {
	worst := sh.Worst()
	if worst == nil {
		return ""
	}
	return tr(l, "money.daily.shrink", "amount", storefront.FormatNaira(sh.LostKobo), "count", strconv.FormatInt(sh.Lost, 10),
		"item", whatsapp.SafeText(worst.Item), "lost", strconv.FormatInt(worst.Lost, 10))
}

// --- in chat ---

// countChat is a count being taken in chat: asked item by item (Next the
// one asked), or named in one message.
type countChat struct {
	Lines  []stock.CountLine `json:"lines"`
	Next   int               `json:"next"`
	Asking bool              `json:"asking,omitempty"`
	Reason string            `json:"reason,omitempty"`
}

// waStockCount answers "count rice 12, indomie 40: rats", and "count" to
// be asked item by item.
func (s *Server) waStockCount(ctx context.Context, c *chat, args string) error {
	if !c.p.Can(PermTableEdit) {
		return c.say(ctx, s, c.t("wa.stock.denied", "org", c.tenant.Name))
	}
	_, items, ok, err := s.stockChatItems(ctx, c)
	if !ok || err != nil {
		return err
	}
	if strings.TrimSpace(args) == "" {
		var lines []stock.CountLine
		for _, it := range items {
			lines = append(lines, stock.CountLine{ItemID: it.ID, Item: it.Name, Unit: it.Unit, Expected: it.Stock, CostKobo: it.CostKobo})
		}
		if len(lines) == 0 {
			return c.say(ctx, s, c.t("wa.count.none"))
		}
		cc := &countChat{Lines: lines, Asking: true}
		sh := &chatShop{Action: "stock_count", StockChat: &stockChat{Count: cc}, Nonce: uuid.NewString()}
		if err := s.saveSession(ctx, c, stateCollecting, chatData{Shop: sh}, waPendingTTL); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.count.start", "count", strconv.Itoa(len(lines)))+"\n\n"+countAsk(c, lines[0]))
	}
	said, reason, ok := stock.ParseCount(args)
	if !ok {
		return c.say(ctx, s, c.t("wa.count.how"))
	}
	got := map[string]int64{}
	for _, x := range said {
		match := matchStock(items, x.Name)
		if len(match) != 1 {
			return c.say(ctx, s, c.t("wa.shop.no_item", "name", whatsapp.SafeText(x.Name)))
		}
		got[match[0].ID] = x.Count
	}
	lines, _, err := countLines(items, got)
	if err != nil {
		return err
	}
	return s.countConfirm(ctx, c, &countChat{Lines: lines, Reason: reason})
}

// countAsk asks for one item's count.
func countAsk(c *chat, l stock.CountLine) string {
	had := "-"
	if l.Expected != nil {
		had = (stock.Item{Unit: l.Unit}).Quantity(*l.Expected)
	}
	return c.t("wa.count.ask", "item", whatsapp.SafeText(l.Item), "had", had)
}

// waStockCountAnswer takes the number asked for (or skip, or done), and
// asks the next; after the last, shows the differences.
func (s *Server) waStockCountAnswer(ctx context.Context, c *chat, text string) error {
	sh := c.data.Shop
	cc := sh.StockChat.Count
	w := strings.ToLower(strings.TrimSpace(text))
	switch w {
	case "cancel", "stop":
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.shop.left"))
	case "done", "finish":
		cc.Lines = cc.Lines[:cc.Next]
	case "skip":
		cc.Lines = append(cc.Lines[:cc.Next], cc.Lines[cc.Next+1:]...)
	default:
		n, err := strconv.ParseInt(strings.ReplaceAll(w, ",", ""), 10, 64)
		if err != nil || n < 0 || n > stock.MaxDelta {
			return c.say(ctx, s, c.t("wa.count.number")+"\n\n"+countAsk(c, cc.Lines[cc.Next]))
		}
		cc.Lines[cc.Next].Counted = n
		cc.Next++
	}
	if cc.Next < len(cc.Lines) {
		if err := s.saveSession(ctx, c, stateCollecting, chatData{Shop: sh}, waPendingTTL); err != nil {
			return err
		}
		return c.say(ctx, s, countAsk(c, cc.Lines[cc.Next]))
	}
	if len(cc.Lines) == 0 {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.shop.left"))
	}
	cc.Asking = false
	return s.countConfirm(ctx, c, cc)
}

// countConfirm shows a count's differences and asks for yes.
func (s *Server) countConfirm(ctx context.Context, c *chat, cc *countChat) error {
	var diffs []string
	same := 0
	for _, l := range cc.Lines {
		if l.Expected != nil && *l.Expected == l.Counted {
			same++
			continue
		}
		had := "-"
		if l.Expected != nil {
			had = strconv.FormatInt(*l.Expected, 10)
		}
		diffs = append(diffs, c.t("wa.count.line", "item", whatsapp.SafeText(l.Item), "had", had, "counted", (stock.Item{Unit: l.Unit}).Quantity(l.Counted)))
	}
	text := c.t("wa.count.confirm", "count", strconv.Itoa(len(cc.Lines)))
	if len(diffs) > 0 {
		text += "\n" + strings.Join(diffs, "\n")
	}
	if same > 0 {
		text += "\n" + c.t("wa.count.same", "count", strconv.Itoa(same))
	}
	return s.shopAsk(ctx, c, &chatShop{Action: "stock_count", StockChat: &stockChat{Count: cc}}, text)
}

// waStockCountApply records a confirmed count.
func (s *Server) waStockCountApply(ctx context.Context, c *chat) error {
	sh := c.data.Shop
	cc := sh.StockChat.Count
	if !c.p.Can(PermTableEdit) {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.stock.denied", "org", c.tenant.Name))
	}
	tctx := s.waTablesCtx(ctx, c)
	_, items, err := s.stockItems(tctx, c.tenant.ID)
	if err != nil {
		return s.stockChatFail(ctx, c, err)
	}
	got := map[string]int64{}
	for _, l := range cc.Lines {
		got[l.ItemID] = l.Counted
	}
	_, full, err := countLines(items, got)
	if errors.Is(err, errNoItem) {
		full = false
	} else if err != nil {
		return err
	}
	x, err := s.Stock.RecordCount(tctx, c.tenant.ID, stock.CountIn{Lines: cc.Lines, Full: full, Reason: cc.Reason, Actor: c.p.Actor(), Key: "wa-" + sh.Nonce})
	if err != nil {
		if errors.Is(err, stock.ErrCount) {
			if serr := s.saveSession(ctx, c, stateIdle, chatData{}, 0); serr != nil {
				return serr
			}
			return c.say(ctx, s, c.t("wa.count.how"))
		}
		return s.stockChatFail(ctx, c, err)
	}
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		return waAudit(ctx, tx, c.ch.Name(), c.p, "stock.count", x.Reference, map[string]any{"count_id": x.ID, "changed": x.Changed})
	}); err != nil {
		return err
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	msg := c.t("wa.count.done", "reference", x.Reference, "changed", strconv.Itoa(x.Changed))
	if x.LostKobo > 0 {
		msg += " " + c.t("wa.count.lost", "amount", storefront.FormatNaira(x.LostKobo))
	}
	return c.say(ctx, s, msg)
}
