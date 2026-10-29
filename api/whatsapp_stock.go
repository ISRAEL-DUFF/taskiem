package api

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Stock by chat (docs/spec/stock.md §4): "add 50 cartons of Indomie at
// 8,500" adds an item or tops one up, "stock indomie" and "how many bags
// of rice do I have?" answer from the catalogue table, "stock" alone is
// the low list, "adjust rice -2 spoilt" corrects a count with a reason,
// and "stock rice 12" (a count) sets it. Each change is shown and written
// on yes, through the same functions as the web's (stock.go), as a
// movement. Seeing stock needs table.read, changing it table.edit;
// starting to keep stock where nothing does also needs storefront.manage.

// stockChat is a stock change being confirmed, kept on the session's
// chatShop.
type stockChat struct {
	Quantity int64  `json:"quantity,omitempty"`
	Unit     string `json:"unit,omitempty"`
	CostKobo *int64 `json:"cost_kobo,omitempty"`
	Change   int64  `json:"change,omitempty"`
	Note     string `json:"note,omitempty"`
	// Setup: nothing keeps stock yet; yes starts it.
	Setup bool `json:"setup,omitempty"`
	// Shown is the item's unit, as the answer says the count in it.
	Shown string `json:"shown,omitempty"`
	// Sale is a sale being confirmed.
	Sale *saleChat `json:"sale,omitempty"`
	// Purchase is a supplier or purchase step being confirmed
	// (whatsapp_purchases.go).
	Purchase *purchaseChat `json:"purchase,omitempty"`
	// Count is a count being taken (stock_counts.go).
	Count *countChat `json:"count,omitempty"`
}

// waStockCommand acts on a stock command; false when cmd is not one (or
// stock is not kept on this platform), so the storefront's words answer.
func (s *Server) waStockCommand(ctx context.Context, c *chat, text, cmd string) (bool, error) {
	if !s.Stock.Ready() {
		return false, nil
	}
	words := strings.Fields(cmd)
	if len(words) == 0 {
		return false, nil
	}
	// Suppliers and purchase orders (K3).
	if done, err := s.waPurchaseCommand(ctx, c, text, cmd); done || err != nil {
		return done, err
	}
	if a, ok := stock.ParseAdd(text); ok {
		return true, s.waStockAdd(ctx, c, a)
	}
	switch {
	case words[0] == "count" && (len(words) == 1 || len(words) > 2):
		// "count", asked item by item, or "count rice 12, indomie 40".
		return true, s.waStockCount(ctx, c, strings.Join(strings.Fields(text)[1:], " "))
	case (words[0] == "sold" || words[0] == "sell") && len(words) > 1:
		return true, s.waStockSale(ctx, c, strings.Join(strings.Fields(text)[1:], " "))
	case cmd == "stock" || cmd == "low stock" || cmd == "what is low" || cmd == "what's low":
		return true, s.waStockLow(ctx, c)
	case words[0] == "how" && len(words) > 2 && (words[1] == "many" || words[1] == "much"):
		name, ok := stock.ParseHowMany(text)
		if !ok {
			return false, nil
		}
		return true, s.waStockShow(ctx, c, name)
	case words[0] == "stock" && len(words) >= 2:
		if _, err := strconv.ParseInt(words[len(words)-1], 10, 64); err == nil {
			return false, nil // "stock rice 12": a count, the storefront's word
		}
		return true, s.waStockShow(ctx, c, strings.Join(strings.Fields(text)[1:], " "))
	case words[0] == "adjust":
		return true, s.waStockAdjust(ctx, c, strings.Join(strings.Fields(text)[1:], " "))
	}
	return false, nil
}

// stockChatItems reads the business's items for a chat, or says why it
// cannot (ok false).
func (s *Server) stockChatItems(ctx context.Context, c *chat) (stockShop, []stock.Item, bool, error) {
	sh, items, err := s.stockItems(s.waTablesCtx(ctx, c), c.tenant.ID)
	switch {
	case errors.Is(err, errStockOff):
		return sh, nil, false, c.say(ctx, s, c.t("wa.stock.off", "org", c.tenant.Name))
	case err != nil:
		s.Logger.Warn("chat: reading stock", "tenant", c.tenant.ID, "err", err)
		return sh, nil, false, c.say(ctx, s, c.t("wa.table.unavailable"))
	}
	return sh, items, true, nil
}

// matchStock finds items by name as the storefront's words do, a unit's
// plural or singular alike.
func matchStock(items []stock.Item, name string) []stock.Item {
	cat := make([]storefront.Item, len(items))
	for i, it := range items {
		cat[i] = storefront.Item{ID: it.ID, Name: it.Name}
	}
	found := matchItems(cat, name)
	if len(found) == 0 && stock.Singular(name) != name {
		found = matchItems(cat, stock.Singular(name))
	}
	out := make([]stock.Item, 0, len(found))
	for _, f := range found {
		for _, it := range items {
			if it.ID == f.ID {
				out = append(out, it)
			}
		}
	}
	return out
}

// stockPick offers a numbered list when several items match.
func (s *Server) stockPick(ctx context.Context, c *chat, sh *chatShop, match []stock.Item) error {
	for _, it := range match[:min(shopPickMax, len(match))] {
		sh.Pick = append(sh.Pick, shopChoice{Row: it.ID, Name: it.Name})
	}
	if err := s.saveSession(ctx, c, stateCollecting, chatData{Shop: sh}, waPendingTTL); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.shop.which_item", "list", numberedChoices(sh.Pick)))
}

// waStockPick takes the number of an item from the list offered.
func (s *Server) waStockPick(ctx context.Context, c *chat, text string) error {
	sh := c.data.Shop
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || n < 1 || n > len(sh.Pick) {
		return c.say(ctx, s, c.t("wa.shop.pick_again", "list", numberedChoices(sh.Pick)))
	}
	chosen := sh.Pick[n-1]
	_, items, ok, err := s.stockChatItems(ctx, c)
	if !ok || err != nil {
		return err
	}
	for _, it := range items {
		if it.ID == chosen.Row {
			sh.Pick = nil
			switch sh.Action {
			case "stock_show":
				if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
					return err
				}
				return s.stockSay(ctx, c, it)
			case "stock_add":
				return s.stockAskAdd(ctx, c, sh, &it)
			case "stock_adjust":
				return s.stockAskAdjust(ctx, c, sh, it)
			}
		}
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.shop.no_item", "name", whatsapp.SafeText(chosen.Name)))
}

// waStockShow answers "stock <item>" and "how many <item>": the count,
// the unit, and the last movement.
func (s *Server) waStockShow(ctx context.Context, c *chat, name string) error {
	if !c.p.Can(PermTableRead) {
		return c.say(ctx, s, c.t("wa.stock.denied_read", "org", c.tenant.Name))
	}
	_, items, ok, err := s.stockChatItems(ctx, c)
	if !ok || err != nil {
		return err
	}
	match := matchStock(items, name)
	switch {
	case len(match) == 0:
		return c.say(ctx, s, c.t("wa.shop.no_item", "name", whatsapp.SafeText(name)))
	case len(match) > 1:
		return s.stockPick(ctx, c, &chatShop{Action: "stock_show"}, match)
	}
	return s.stockSay(ctx, c, match[0])
}

func (s *Server) stockSay(ctx context.Context, c *chat, it stock.Item) error {
	name := whatsapp.SafeText(it.Name)
	if it.Stock == nil {
		return c.say(ctx, s, c.t("wa.stock.no_count", "name", name))
	}
	msg := c.t("wa.stock.count", "name", name, "count", whatsapp.SafeText(it.Quantity(*it.Stock)))
	if it.Low() {
		msg += " " + c.t("wa.stock.is_low", "level", strconv.FormatInt(*it.Reorder, 10))
	}
	var last []stock.Movement
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		last, err = stock.ListTx(ctx, tx, c.tenant.ID, stock.Filter{ItemID: it.ID, Limit: 1})
		return err
	}); err != nil {
		return err
	}
	if len(last) == 1 {
		msg += "\n" + c.t("wa.stock.last", "what", stockWhat(c, it, last[0]), "when", stockWhen(last[0]))
	}
	return c.say(ctx, s, msg)
}

// stockWhat says a movement in a few words: "sold 2 bags", "order
// AK-7QX4KD: 1".
func stockWhat(c *chat, it stock.Item, m stock.Movement) string {
	n := int64(0)
	switch {
	case m.Change != nil:
		n = *m.Change
	case m.Delta != nil:
		n = *m.Delta
	case m.SetTo != nil:
		n = *m.SetTo
	}
	if n < 0 {
		n = -n
	}
	qty := whatsapp.SafeText(it.Quantity(n))
	if m.Reason == stock.Count {
		qty = whatsapp.SafeText(it.Quantity(*m.SetTo))
	}
	return c.t("wa.stock.reason_"+m.Reason, "qty", qty, "reference", whatsapp.SafeText(m.Reference))
}

func stockWhen(m stock.Movement) string {
	at := m.CreatedAt
	if m.DoneAt != nil {
		at = *m.DoneAt
	}
	at = at.In(storefront.Lagos)
	now := time.Now().In(storefront.Lagos)
	if at.Year() == now.Year() && at.YearDay() == now.YearDay() {
		return at.Format("15:04")
	}
	return at.Format("2 Jan 15:04")
}

// waStockLow answers "stock": the items at or below their reorder level,
// with what to order.
func (s *Server) waStockLow(ctx context.Context, c *chat) error {
	if !c.p.Can(PermTableRead) {
		return c.say(ctx, s, c.t("wa.stock.denied_read", "org", c.tenant.Name))
	}
	_, items, ok, err := s.stockChatItems(ctx, c)
	if !ok || err != nil {
		return err
	}
	levels := 0
	for _, it := range items {
		if it.Reorder != nil {
			levels++
		}
	}
	if levels == 0 {
		return c.say(ctx, s, c.t("wa.stock.no_levels", "items", strconv.Itoa(len(items)))+s.waLink(c, "/storefront/stock"))
	}
	low := stock.Low(items)
	if len(low) == 0 {
		return c.say(ctx, s, c.t("wa.stock.none_low", "items", strconv.Itoa(len(items))))
	}
	lines := []string{c.t("wa.stock.low_title")}
	for i, it := range low {
		if i == 10 {
			lines = append(lines, c.t("wa.stock.low_more", "count", strconv.Itoa(len(low)-10)))
			break
		}
		lines = append(lines, c.t("wa.stock.low_line", "name", whatsapp.SafeText(it.Name), "count", whatsapp.SafeText(it.Quantity(*it.Stock)),
			"level", strconv.FormatInt(*it.Reorder, 10), "suggest", whatsapp.SafeText(it.Quantity(it.Suggest()))))
	}
	return c.say(ctx, s, strings.Join(lines, "\n"))
}

// waStockAdd answers "add 50 cartons of Indomie at 8,500": an item added,
// or goods received on one there, shown first.
func (s *Server) waStockAdd(ctx context.Context, c *chat, a stock.Added) error {
	if !c.p.Can(PermTableEdit) {
		return c.say(ctx, s, c.t("wa.stock.denied", "org", c.tenant.Name))
	}
	sh := &chatShop{Action: "stock_add", Name: a.Name, Stock: &a.Quantity, Old: a.Unit}
	sh.StockChat = &stockChat{Quantity: a.Quantity, Unit: a.Unit, CostKobo: a.CostKobo}
	_, items, err := s.stockItems(s.waTablesCtx(ctx, c), c.tenant.ID)
	if errors.Is(err, errStockOff) {
		if !c.p.Can(PermStorefrontManage) {
			return c.say(ctx, s, c.t("wa.stock.off_denied", "org", c.tenant.Name))
		}
		sh.StockChat.Setup = true
		return s.stockAskAdd(ctx, c, sh, nil)
	}
	if err != nil {
		s.Logger.Warn("chat: reading stock", "tenant", c.tenant.ID, "err", err)
		return c.say(ctx, s, c.t("wa.table.unavailable"))
	}
	match := matchStock(items, a.Name)
	var exact []stock.Item
	for _, it := range match {
		if matchKey(it.Name) == matchKey(a.Name) || matchKey(it.Name) == matchKey(stock.Singular(a.Name)) {
			exact = append(exact, it)
		}
	}
	switch {
	case len(exact) == 1:
		return s.stockAskAdd(ctx, c, sh, &exact[0])
	case len(match) == 1:
		return s.stockAskAdd(ctx, c, sh, &match[0])
	case len(match) > 1:
		return s.stockPick(ctx, c, sh, match)
	}
	return s.stockAskAdd(ctx, c, sh, nil)
}

// stockAskAdd shows an add: a new item (it nil) or goods received on it.
func (s *Server) stockAskAdd(ctx context.Context, c *chat, sh *chatShop, it *stock.Item) error {
	in := sh.StockChat
	cost := ""
	if in.CostKobo != nil {
		cost = c.t("wa.stock.at_cost", "price", storefront.FormatNaira(*in.CostKobo))
	}
	if it == nil {
		qty := whatsapp.SafeText((stock.Item{Unit: in.Unit}).Quantity(in.Quantity))
		key := "wa.stock.confirm_new"
		if in.Setup {
			key = "wa.stock.confirm_first"
		}
		sh.Row = ""
		return s.shopAsk(ctx, c, sh, c.t(key, "name", whatsapp.SafeText(sh.Name), "qty", qty, "cost", cost))
	}
	sh.Row, sh.Name = it.ID, it.Name
	if in.Unit == "" || it.Unit != "" {
		in.Unit = ""
	}
	unitItem := *it
	if unitItem.Unit == "" {
		unitItem.Unit = in.Unit
	}
	in.Shown = unitItem.Unit
	from, to := "?", "?"
	if it.Stock != nil {
		from, to = unitItem.Quantity(*it.Stock), unitItem.Quantity(*it.Stock+in.Quantity)
	}
	return s.shopAsk(ctx, c, sh, c.t("wa.stock.confirm_topup", "name", whatsapp.SafeText(it.Name), "qty", whatsapp.SafeText(unitItem.Quantity(in.Quantity)),
		"from", whatsapp.SafeText(from), "to", whatsapp.SafeText(to), "cost", cost))
}

// waStockAdjust answers "adjust rice -2 spoilt".
func (s *Server) waStockAdjust(ctx context.Context, c *chat, args string) error {
	if !c.p.Can(PermTableEdit) {
		return c.say(ctx, s, c.t("wa.stock.denied", "org", c.tenant.Name))
	}
	a, ok := stock.ParseAdjust(args)
	if !ok {
		return c.say(ctx, s, c.t("wa.stock.adjust_how"))
	}
	_, items, ok, err := s.stockChatItems(ctx, c)
	if !ok || err != nil {
		return err
	}
	sh := &chatShop{Action: "stock_adjust", Name: a.Name, StockChat: &stockChat{Change: a.Change, Note: a.Note}}
	match := matchStock(items, a.Name)
	switch {
	case len(match) == 0:
		return c.say(ctx, s, c.t("wa.shop.no_item", "name", whatsapp.SafeText(a.Name)))
	case len(match) > 1:
		return s.stockPick(ctx, c, sh, match)
	}
	return s.stockAskAdjust(ctx, c, sh, match[0])
}

func (s *Server) stockAskAdjust(ctx context.Context, c *chat, sh *chatShop, it stock.Item) error {
	sh.Row, sh.Name = it.ID, it.Name
	in := sh.StockChat
	in.Shown = it.Unit
	change := it.Quantity(in.Change)
	if in.Change > 0 {
		change = "+" + change
	}
	from, to := "?", "?"
	if it.Stock != nil {
		from, to = it.Quantity(*it.Stock), it.Quantity(max(0, *it.Stock+in.Change))
	}
	note := in.Note
	if note == "" {
		note = c.t("wa.stock.no_reason")
	}
	return s.shopAsk(ctx, c, sh, c.t("wa.stock.confirm_adjust", "name", whatsapp.SafeText(it.Name), "change", whatsapp.SafeText(change),
		"from", whatsapp.SafeText(from), "to", whatsapp.SafeText(to), "note", whatsapp.SafeText(note)))
}

// waStockApply writes a confirmed stock change.
func (s *Server) waStockApply(ctx context.Context, c *chat) error {
	sh := c.data.Shop
	in := sh.StockChat
	if in != nil && in.Purchase != nil {
		return s.waPurchaseApply(ctx, c)
	}
	if in != nil && in.Count != nil {
		return s.waStockCountApply(ctx, c)
	}
	if sh.Action == "stock_sale" && in != nil && in.Sale != nil && c.p.Can(PermTableEdit) {
		if !s.limiter("wa-shop:"+c.addr, 20*time.Second, 10).Allow() {
			return c.say(ctx, s, c.t("wa.shop.too_many"))
		}
		return s.waStockSaleApply(ctx, c)
	}
	if !c.p.Can(PermTableEdit) || in == nil {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.stock.denied", "org", c.tenant.Name))
	}
	if !s.limiter("wa-shop:"+c.addr, 20*time.Second, 10).Allow() {
		return c.say(ctx, s, c.t("wa.shop.too_many"))
	}
	audit := func(tx pgx.Tx, action, target string, detail map[string]any) error {
		return waAudit(ctx, tx, c.ch.Name(), c.p, action, target, detail)
	}
	tctx := s.waTablesCtx(ctx, c)
	key := "wa-" + sh.Nonce
	if in.Setup {
		if !c.p.Can(PermStorefrontManage) {
			if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
				return err
			}
			return c.say(ctx, s, c.t("wa.stock.off_denied", "org", c.tenant.Name))
		}
		if _, err := s.stockSetup(tctx, c.tenant.ID, c.p.Actor(), audit); err != nil {
			s.Logger.Warn("chat: keeping stock", "tenant", c.tenant.ID, "err", err)
			// The session stays: yes again tries again.
			return c.say(ctx, s, c.t("wa.shop.no_tables", "reason", tablesChatReason(err)))
		}
	}
	var msg string
	switch sh.Action {
	case "stock_add":
		if sh.Row == "" {
			it, _, err := s.stockAdd(tctx, c.tenant.ID, c.p.Actor(), key, stockNew{Name: sh.Name, Quantity: in.Quantity, Unit: in.Unit, CostKobo: in.CostKobo}, audit)
			if errors.Is(err, errItemExists) {
				if serr := s.saveSession(ctx, c, stateIdle, chatData{}, 0); serr != nil {
					return serr
				}
				return c.say(ctx, s, c.t("wa.stock.exists", "name", whatsapp.SafeText(it.Name)))
			}
			if err != nil {
				s.Logger.Warn("chat: adding an item", "tenant", c.tenant.ID, "err", err)
				return c.say(ctx, s, c.t("wa.shop.add_failed", "reason", tablesChatReason(err)))
			}
			msg = c.t("wa.stock.added", "name", whatsapp.SafeText(it.Name), "count", whatsapp.SafeText(it.Quantity(stockOf(it))))
			if it.PriceKobo == nil {
				msg += " " + c.t("wa.stock.no_price", "name", whatsapp.SafeText(it.Name))
			}
			break
		}
		shop, err := s.stockShopOf(tctx, c.tenant.ID)
		if err != nil {
			return s.stockChatFail(ctx, c, err)
		}
		set := stockCells(shop.cols(), in.Unit, in.CostKobo, nil, nil, "")
		if err := s.stockSetCells(tctx, c.tenant.ID, shop, sh.Row, key+"-cells", set); err != nil {
			return s.stockChatFail(ctx, c, err)
		}
		q := in.Quantity
		m, err := s.stockMove(tctx, c.tenant.ID, stock.Move{ItemID: sh.Row, Item: sh.Name, Reason: stock.Received, Delta: &q, Actor: c.p.Actor(),
			Key: key, CostKobo: in.CostKobo}, audit)
		if err != nil {
			return s.stockChatFail(ctx, c, err)
		}
		msg = stockDone(c, sh, m)
	case "stock_adjust":
		d := in.Change
		m, err := s.stockMove(tctx, c.tenant.ID, stock.Move{ItemID: sh.Row, Item: sh.Name, Reason: stock.Adjustment, Delta: &d, Note: in.Note,
			Actor: c.p.Actor(), Key: key}, audit)
		if err != nil {
			return s.stockChatFail(ctx, c, err)
		}
		msg = stockDone(c, sh, m)
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	return c.say(ctx, s, msg)
}

func stockOf(it stock.Item) int64 {
	if it.Stock == nil {
		return 0
	}
	return *it.Stock
}

// stockDone says a movement landed (the count now), or that it is on its
// way to the table.
func stockDone(c *chat, sh *chatShop, m stock.Movement) string {
	if m.Status != "done" {
		return c.t("wa.stock.waiting", "name", whatsapp.SafeText(sh.Name))
	}
	it := stock.Item{Unit: sh.StockChat.Shown}
	return c.t("wa.stock.now", "name", whatsapp.SafeText(sh.Name), "count", whatsapp.SafeText(it.Quantity(*m.After)))
}

// stockChatFail says why a stock change could not be made; the session
// stays, so yes again tries again.
func (s *Server) stockChatFail(ctx context.Context, c *chat, err error) error {
	switch {
	case errors.Is(err, errStockOff):
		if serr := s.saveSession(ctx, c, stateIdle, chatData{}, 0); serr != nil {
			return serr
		}
		return c.say(ctx, s, c.t("wa.stock.off", "org", c.tenant.Name))
	case errors.Is(err, errNoItem):
		if serr := s.saveSession(ctx, c, stateIdle, chatData{}, 0); serr != nil {
			return serr
		}
		return c.say(ctx, s, c.t("wa.shop.no_item", "name", whatsapp.SafeText(c.data.Shop.Name)))
	}
	s.Logger.Warn("chat: a stock change", "tenant", c.tenant.ID, "err", err)
	return c.say(ctx, s, c.t("wa.shop.add_failed", "reason", tablesChatReason(err)))
}

// shopCountItem writes "stock <item> <n>" as a count: a movement setting
// the stock, so the count says why it changed.
func (s *Server) shopCountItem(ctx context.Context, c *chat, sf storefront.Storefront) error {
	sh := c.data.Shop
	n := *sh.Stock
	m, err := s.stockMove(s.waTablesCtx(ctx, c), c.tenant.ID, stock.Move{ItemID: sh.Row, Item: sh.Name, Reason: stock.Count, SetTo: &n,
		Actor: c.p.Actor(), Key: "wa-" + sh.Nonce}, func(tx pgx.Tx, action, target string, detail map[string]any) error {
		return waAudit(ctx, tx, c.ch.Name(), c.p, action, target, detail)
	})
	if err != nil {
		return s.stockChatFail(ctx, c, err)
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	if m.Status != "done" {
		return c.say(ctx, s, c.t("wa.stock.waiting", "name", whatsapp.SafeText(sh.Name)))
	}
	return c.say(ctx, s, c.t("wa.shop.changed", "name", whatsapp.SafeText(sh.Name))+" "+s.shopPublishedNote(c, sf))
}

// saleChat is a sale being confirmed.
type saleChat struct {
	Lines      []stock.SaleLine `json:"lines"`
	AmountKobo *int64           `json:"amount_kobo,omitempty"`
	PaidHow    string           `json:"paid_how"`
	// Beyond names the items sold beyond the stock there is.
	Beyond []string `json:"beyond,omitempty"`
}

// waStockSale answers "sold 3 bags of rice 45,000 cash" (and the same in
// the other languages, after their sale word): the sale shown, recorded on
// yes. Items are found as the other stock words find them; one that
// matches several, or none, is said, and nothing is recorded.
func (s *Server) waStockSale(ctx context.Context, c *chat, args string) error {
	if !c.p.Can(PermTableEdit) {
		return c.say(ctx, s, c.t("wa.stock.denied", "org", c.tenant.Name))
	}
	said, ok := stock.ParseSale(args)
	if !ok {
		return c.say(ctx, s, c.t("wa.sale.how"))
	}
	_, items, ok, err := s.stockChatItems(ctx, c)
	if !ok || err != nil {
		return err
	}
	sale := &saleChat{AmountKobo: said.AmountKobo, PaidHow: said.PaidHow}
	if sale.PaidHow == "" {
		sale.PaidHow = stock.Cash
	}
	var total int64
	for _, x := range said.Items {
		match := matchStock(items, x.Name)
		if len(match) > 1 {
			var exact []stock.Item
			for _, it := range match {
				if matchKey(it.Name) == matchKey(x.Name) {
					exact = append(exact, it)
				}
			}
			if len(exact) == 1 {
				match = exact
			}
		}
		switch {
		case len(match) == 0:
			return c.say(ctx, s, c.t("wa.shop.no_item", "name", whatsapp.SafeText(x.Name)))
		case len(match) > 1:
			names := make([]string, 0, min(shopPickMax, len(match)))
			for _, it := range match[:min(shopPickMax, len(match))] {
				names = append(names, whatsapp.SafeText(it.Name))
			}
			return c.say(ctx, s, c.t("wa.sale.which", "name", whatsapp.SafeText(x.Name), "list", strings.Join(names, ", ")))
		}
		it := match[0]
		price := int64(0)
		if it.PriceKobo != nil {
			price = *it.PriceKobo
		} else if said.AmountKobo == nil {
			return c.say(ctx, s, c.t("wa.sale.no_price", "name", whatsapp.SafeText(it.Name)))
		}
		unit := it.Unit
		sale.Lines = append(sale.Lines, stock.SaleLine{ItemID: it.ID, Item: it.Name, Quantity: x.Quantity, Unit: unit, PriceKobo: price, LineKobo: price * x.Quantity,
			CostKobo: it.CostKobo})
		total += price * x.Quantity
		if it.Stock != nil && *it.Stock < x.Quantity {
			sale.Beyond = append(sale.Beyond, it.Name)
		}
	}
	if sale.AmountKobo == nil {
		sale.AmountKobo = &total
	}
	if *sale.AmountKobo <= 0 {
		return c.say(ctx, s, c.t("wa.sale.how"))
	}
	text := c.t("wa.sale.confirm", "items", whatsapp.SafeText(stock.SaleWords(sale.Lines)), "amount", storefront.FormatNaira(*sale.AmountKobo),
		"how", c.t("sales.how_"+sale.PaidHow))
	if len(sale.Beyond) > 0 {
		text += "\n" + c.t("wa.sale.beyond", "names", whatsapp.SafeText(strings.Join(sale.Beyond, ", ")))
	}
	return s.shopAsk(ctx, c, &chatShop{Action: "stock_sale", StockChat: &stockChat{Sale: sale}}, text)
}

// waStockSaleApply records a confirmed sale.
func (s *Server) waStockSaleApply(ctx context.Context, c *chat) error {
	sh := c.data.Shop
	sale := sh.StockChat.Sale
	x, err := s.Stock.RecordSale(s.waTablesCtx(ctx, c), c.tenant.ID, stock.SaleIn{Lines: sale.Lines, AmountKobo: sale.AmountKobo, PaidHow: sale.PaidHow,
		Actor: c.p.Actor(), Key: "wa-" + sh.Nonce}, s.moneyLog())
	if err != nil {
		if errors.Is(err, stock.ErrSaleInvalid) {
			if serr := s.saveSession(ctx, c, stateIdle, chatData{}, 0); serr != nil {
				return serr
			}
			return c.say(ctx, s, c.t("wa.sale.how"))
		}
		return s.stockChatFail(ctx, c, err)
	}
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		return waAudit(ctx, tx, c.ch.Name(), c.p, "stock.sale", x.Reference, map[string]any{"sale_id": x.ID, "amount_kobo": x.AmountKobo, "paid_how": x.PaidHow})
	}); err != nil {
		return err
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	lines := []string{c.t("wa.sale.recorded", "reference", x.Reference, "items", whatsapp.SafeText(stock.SaleWords(x.Lines)),
		"amount", storefront.FormatNaira(x.AmountKobo), "how", c.t("sales.how_"+x.PaidHow))}
	switch x.Status {
	case "waiting":
		lines = append(lines, c.t("wa.sale.waiting_"+x.PaidHow))
	case "matched":
		lines = append(lines, c.t("wa.sale.matched"))
	}
	if len(x.Short) > 0 {
		lines = append(lines, c.t("wa.sale.short", "names", whatsapp.SafeText(strings.Join(x.Short, ", ")), "name", whatsapp.SafeText(x.Short[0])))
	}
	return c.say(ctx, s, strings.Join(lines, "\n"))
}
