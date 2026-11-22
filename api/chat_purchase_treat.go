package api

// The question in chat (what-a-business-buys §6, In chat; U2b). An order
// drafted in chat ("order 1 sewing machine from Singer Ltd", "@" and "#"
// mentions, "order from …") asks, one line at a time, about each line the
// business's memory does not know (unconfirmed): how to treat it (three
// buttons), for Add to stock which stock item (the business's items,
// materials included, or a new one asking Do you sell it?), then its label
// (the three most used, Another to type one, No label). Typed or spoken
// answers work at every step ("keep track of it, machines"). Each line is
// saved as the order's page saves it (stock.SetLineTx), which remembers it
// the first time; remembered lines ask nothing. Then the order is shown
// for Yes, as before.

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/spend"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// treatPrefix starts the ids of the question's buttons and list rows.
const treatPrefix = "pt:"

// The question's steps.
const (
	treatStepTreat = "treat"       // Just record it / Add to stock / Keep track of it
	treatStepItem  = "item"        // which stock item
	treatStepSell  = "sell"        // a new stock item: Do you sell it?
	treatStepMany  = "many"        // tracking more than stock.TrackAsk
	treatStepLabel = "label"       // the label
	treatStepTyped = "label_typed" // Another: the label typed
	treatItems     = 5             // stock items offered
	treatLabels    = 3             // labels offered
)

// treatAsk is the question on the lines of orders drafted in chat, kept in
// the session.
type treatAsk struct {
	// Orders are the orders drafted, shown for Yes once every line is
	// answered; Done the lines answered ("<order>:<position>").
	Orders []uuid.UUID `json:"orders"`
	Done   []string    `json:"done,omitempty"`
	// The line asked about.
	Order    uuid.UUID `json:"order"`
	Position int       `json:"position"`
	Item     string    `json:"item"`
	Unit     string    `json:"unit,omitempty"`
	Quantity int64     `json:"quantity"`
	Supplier string    `json:"supplier"`
	// HintItem and HintLabel are Taskiem's suggestion on the line, offered
	// first.
	HintItem  *uuid.UUID `json:"hint_item,omitempty"`
	HintLabel *uuid.UUID `json:"hint_label,omitempty"`
	Step      string     `json:"step"`
	// Choice is the answer so far; StockName its stock item's name, and
	// Label a label said with the treatment ("keep track of it, machines").
	Choice    stock.LineChoice `json:"choice"`
	StockName string           `json:"stock_name,omitempty"`
	Label     string           `json:"label,omitempty"`
	// Offered are the stock items or labels offered, by number.
	Offered []treatOffer `json:"offered,omitempty"`
}

// treatOffer is a stock item (its item's id) or a label (its id, or none
// for a name not yet a label) offered.
type treatOffer struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
}

func treatKey(order uuid.UUID, position int) string {
	return order.String() + ":" + strconv.Itoa(position)
}

// orderAsk asks the question on each line of orders just drafted in chat
// that the business's memory does not know, then shows them for Yes.
// Those who may not change stock are shown them at once, as suggested.
func (s *Server) orderAsk(ctx context.Context, c *chat, ids []uuid.UUID) error {
	if c.p.Can(PermTableEdit) {
		if asked, err := s.treatNext(ctx, c, &treatAsk{Orders: ids}, ""); asked || err != nil {
			return err
		}
	}
	return s.orderConfirm(ctx, c, ids, "")
}

// orderConfirm shows orders drafted in chat for Yes: one, or several sent
// by one Yes.
func (s *Server) orderConfirm(ctx context.Context, c *chat, ids []uuid.UUID, before string) error {
	orders := make([]stock.Purchase, 0, len(ids))
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		for _, id := range ids {
			x, err := stock.PurchaseTx(ctx, tx, c.tenant.ID, id, false)
			if err != nil {
				return err
			}
			orders = append(orders, x)
		}
		return nil
	}); err != nil {
		return err
	}
	var sh *chatShop
	var text string
	if len(orders) == 1 {
		x := orders[0]
		sh = &chatShop{Action: "stock_order", StockChat: &stockChat{Purchase: &purchaseChat{Purchase: x.ID}}}
		text = c.t("wa.order.confirm", "reference", x.Reference, "items", whatsapp.SafeText(x.Words()), "supplier", whatsapp.SafeText(x.Supplier),
			"total", storefront.FormatNaira(x.TotalKobo))
	} else {
		lines := make([]string, len(orders))
		for i, x := range orders {
			lines[i] = c.t("wa.mention.draft", "reference", x.Reference, "supplier", whatsapp.SafeText(x.Supplier), "items", whatsapp.SafeText(x.Words()),
				"total", storefront.FormatNaira(x.TotalKobo))
		}
		sh = &chatShop{Action: "stock_orders", StockChat: &stockChat{Purchase: &purchaseChat{Drafted: ids}}}
		text = c.t("wa.mention.confirm", "count", strconv.Itoa(len(orders)), "orders", strings.Join(lines, "\n"))
	}
	if before != "" {
		text = before + "\n\n" + text
	}
	return s.shopAsk(ctx, c, sh, text)
}

// treatNext asks about the next line not yet answered that the business's
// memory does not know; false when there is none. A line made known by an
// earlier answer is skipped: no order drafted in chat has two lines of one
// memory (a seller's item twice is refused as drafted, stock.checkTwice;
// "order from …" has one line per stock item, each its own name), so that
// is only ever an answer given elsewhere.
func (s *Server) treatNext(ctx context.Context, c *chat, ta *treatAsk, before string) (bool, error) {
	found := false
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		for _, id := range ta.Orders {
			x, err := stock.PurchaseTx(ctx, tx, c.tenant.ID, id, false)
			if err != nil {
				return err
			}
			for _, l := range x.Lines {
				if !l.Unconfirmed || l.Received > 0 || slices.Contains(ta.Done, treatKey(id, l.Position)) {
					continue
				}
				ta.Order, ta.Position, ta.Item, ta.Unit, ta.Quantity, ta.Supplier = id, l.Position, l.Item, l.Unit, l.Quantity, x.Supplier
				ta.HintItem, ta.HintLabel = nil, l.LabelID
				if l.Treatment == stock.AddToStock {
					ta.HintItem = l.StockItemID
				}
				found = true
				return nil
			}
		}
		return nil
	}); err != nil || !found {
		return false, err
	}
	ta.Step, ta.Choice, ta.StockName, ta.Label, ta.Offered = treatStepTreat, stock.LineChoice{}, "", "", nil
	return true, s.treatAskTreat(ctx, c, ta, before)
}

// treatFinish asks about the next line, or shows the orders for Yes.
func (s *Server) treatFinish(ctx context.Context, c *chat, ta *treatAsk, before string) error {
	if asked, err := s.treatNext(ctx, c, ta, before); asked || err != nil {
		return err
	}
	return s.orderConfirm(ctx, c, ta.Orders, before)
}

// treatSay keeps the question in the session and asks it.
func (s *Server) treatSay(ctx context.Context, c *chat, ta *treatAsk, before, text string, buttons []whatsapp.Button) error {
	sh := &chatShop{Action: "stock_treat", Nonce: uuid.NewString(), StockChat: &stockChat{Purchase: &purchaseChat{Treat: ta}}}
	if err := s.saveSession(ctx, c, stateCollecting, chatData{Shop: sh}, waPendingTTL); err != nil {
		return err
	}
	if before != "" {
		text = before + "\n\n" + text
	}
	return s.sayChoices(ctx, c, text, buttons)
}

// sayChoices asks with choices: buttons; on WhatsApp, which shows three,
// a list beyond that.
func (s *Server) sayChoices(ctx context.Context, c *chat, text string, choices []whatsapp.Button) error {
	if c.ch.Name() == "whatsapp" && len(choices) > 3 {
		l := &whatsapp.List{Button: c.t("menu.choose"), Sections: []whatsapp.ListSection{{}}}
		for _, b := range choices {
			l.Sections[0].Rows = append(l.Sections[0].Rows, whatsapp.ListRow{ID: b.ID, Title: clipRow(b.Title)})
		}
		if c.note != "" {
			text, c.note = c.note+"\n\n"+text, ""
		}
		_, err := c.ch.Send(ctx, c.addr, whatsapp.Message{Text: c.ch.Prefix(c.tenant.Name) + text, List: l})
		return err
	}
	bs := make([]whatsapp.Button, len(choices))
	for i, b := range choices {
		bs[i] = whatsapp.Button{ID: b.ID, Title: clipTitle(b.Title)}
	}
	return c.say(ctx, s, text, bs...)
}

// clipRow fits a list row's title (24 characters on WhatsApp).
func clipRow(s string) string {
	if utf8.RuneCountInString(s) <= 24 {
		return s
	}
	return string([]rune(s)[:23]) + "…"
}

// treatQuantity is a line's quantity in words: "60 bags", "1".
func (ta *treatAsk) quantity() string {
	return (stock.Item{Unit: ta.Unit}).Quantity(ta.Quantity)
}

func (s *Server) treatAskTreat(ctx context.Context, c *chat, ta *treatAsk, before string) error {
	ta.Step = treatStepTreat
	text := c.t("wa.treat.ask", "item", whatsapp.SafeText(ta.Item), "count", whatsapp.SafeText(ta.quantity()), "supplier", whatsapp.SafeText(ta.Supplier)) +
		"\n" + c.t("wa.treat.how")
	return s.treatSay(ctx, c, ta, before, text, []whatsapp.Button{
		{ID: treatPrefix + string(stock.Record), Title: c.t("button.treat_record")},
		{ID: treatPrefix + string(stock.AddToStock), Title: c.t("button.treat_stock")},
		{ID: treatPrefix + string(stock.Track), Title: c.t("button.treat_track")},
	})
}

// treatChoosable are the business's items a line can go into: a product
// (counted, or counted from now) or a material; never a product sold in
// sizes, which keeps no stock of its own.
func treatChoosable(items []stock.Item) []stock.Item {
	out := make([]stock.Item, 0, len(items))
	for _, it := range items {
		if !it.HasSizes() {
			out = append(out, it)
		}
	}
	return out
}

// treatAskItem asks which stock item: those matching (the suggestion
// first), else the business's first items, then A new one.
func (s *Server) treatAskItem(ctx context.Context, c *chat, ta *treatAsk, before string, match []stock.Item) error {
	ta.Step = treatStepItem
	if match == nil {
		_, items, ok, err := s.stockChatItems(ctx, c)
		if !ok || err != nil {
			return err
		}
		items = treatChoosable(items)
		match = treatChoosable(matchStock(items, ta.Item))
		for _, it := range items {
			if !slices.ContainsFunc(match, func(m stock.Item) bool { return m.ID == it.ID }) {
				match = append(match, it)
			}
		}
		if ta.HintItem != nil {
			if i := slices.IndexFunc(match, func(m stock.Item) bool { return m.StockItemID == ta.HintItem.String() }); i > 0 {
				match = append([]stock.Item{match[i]}, append(match[:i:i], match[i+1:]...)...)
			}
		}
	}
	ta.Offered = nil
	bs := []whatsapp.Button{}
	for i, it := range match[:min(treatItems, len(match))] {
		ta.Offered = append(ta.Offered, treatOffer{ID: it.ID, Name: it.Name})
		bs = append(bs, whatsapp.Button{ID: treatPrefix + "item:" + strconv.Itoa(i+1), Title: it.Name})
	}
	bs = append(bs, whatsapp.Button{ID: treatPrefix + "item:new", Title: c.t("button.treat_new_item")})
	return s.treatSay(ctx, c, ta, before, c.t("wa.treat.which_item", "item", whatsapp.SafeText(ta.Item)), bs)
}

func (s *Server) treatAskSell(ctx context.Context, c *chat, ta *treatAsk, before string) error {
	ta.Step = treatStepSell
	return s.treatSay(ctx, c, ta, before, c.t("wa.treat.new_item", "name", whatsapp.SafeText(ta.Item))+"\n\n"+c.t("wa.stock.sell_how"), sellButtons(c))
}

func (s *Server) treatAskMany(ctx context.Context, c *chat, ta *treatAsk, before string) error {
	ta.Step = treatStepMany
	return s.treatSay(ctx, c, ta, before, c.t("wa.treat.many", "count", strconv.FormatInt(ta.Quantity, 10), "item", whatsapp.SafeText(ta.Item)),
		[]whatsapp.Button{{ID: treatPrefix + "many:yes", Title: c.t("button.treat_many_yes")}, {ID: treatPrefix + "many:record", Title: c.t("button.treat_many_record")}})
}

// treatAskLabel offers the label: the suggestion, Stock bought for a
// line to stock, then the business's most used, three in all (a new name
// from the item when it has fewer), Another and No label.
func (s *Server) treatAskLabel(ctx context.Context, c *chat, ta *treatAsk, before string) error {
	ta.Step = treatStepLabel
	var list []spend.Label
	var hint *spend.Label
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		if list, err = spend.ListTx(ctx, tx, c.tenant.ID); err != nil {
			return err
		}
		if ta.HintLabel != nil {
			if l, err := spend.KeptTx(ctx, tx, c.tenant.ID, *ta.HintLabel); err == nil {
				hint = &l
			}
		}
		return nil
	}); err != nil {
		return err
	}
	ta.Offered = nil
	seen := map[string]bool{}
	add := func(id, name string) {
		if k := strings.ToLower(name); !seen[k] && len(ta.Offered) < treatLabels {
			seen[k] = true
			ta.Offered = append(ta.Offered, treatOffer{ID: id, Name: name})
		}
	}
	idOf := func(name string) string {
		for _, l := range list {
			if strings.EqualFold(l.Name, name) {
				return l.ID.String()
			}
		}
		return ""
	}
	if hint != nil {
		add(hint.ID.String(), hint.Name)
	}
	if ta.Choice.Treatment == stock.AddToStock {
		add(idOf(spend.StockBought), spend.StockBought)
	}
	for _, l := range list {
		add(l.ID.String(), l.Name)
	}
	for _, sg := range spend.Suggest(list, ta.Item, "", treatLabels) {
		if sg.LabelID == nil {
			add("", sg.Name)
		}
	}
	bs := []whatsapp.Button{}
	for i, o := range ta.Offered {
		bs = append(bs, whatsapp.Button{ID: treatPrefix + "label:" + strconv.Itoa(i+1), Title: o.Name})
	}
	bs = append(bs, whatsapp.Button{ID: treatPrefix + "label:another", Title: c.t("button.treat_label_another")},
		whatsapp.Button{ID: treatPrefix + "label:none", Title: c.t("button.treat_label_none")})
	return s.treatSay(ctx, c, ta, before, c.t("wa.treat.label", "item", whatsapp.SafeText(ta.Item)), bs)
}

func (s *Server) treatAskTyped(ctx context.Context, c *chat, ta *treatAsk, before string) error {
	ta.Step = treatStepTyped
	return s.treatSay(ctx, c, ta, before, c.t("wa.treat.label_type", "item", whatsapp.SafeText(ta.Item)), nil)
}

// treatNumber is a number among n typed ("2", "#2"); 0 when it is not one.
func treatNumber(text string, n int) int {
	k, err := strconv.Atoi(strings.Trim(strings.TrimSpace(text), " #."))
	if err != nil || k < 1 || k > n {
		return 0
	}
	return k
}

// waTreatReply takes an answer to the question on a line: a button or a
// list row pressed, a number, or words typed or spoken.
func (s *Server) waTreatReply(ctx context.Context, c *chat, text, cmd string) error {
	ta := c.data.Shop.StockChat.Purchase.Treat
	if !c.p.Can(PermTableEdit) {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.stock.denied", "org", c.tenant.Name))
	}
	press, pressed := strings.CutPrefix(text, treatPrefix)
	said, rest, saidOK := lang.MatchTreat(text, c.lang.Match())
	if pressed {
		saidOK = false
	}
	switch ta.Step {
	case treatStepTreat:
		var t stock.Treatment
		switch {
		case pressed:
			t = stock.Treatment(press)
		case treatNumber(text, 3) > 0:
			t = []stock.Treatment{stock.Record, stock.AddToStock, stock.Track}[treatNumber(text, 3)-1]
		case saidOK && (said == lang.TreatRecord || said == lang.TreatStock || said == lang.TreatTrack):
			t = stock.Treatment(said)
		}
		if !t.Valid() {
			return s.treatAskTreat(ctx, c, ta, c.t("wa.treat.reminder", "item", whatsapp.SafeText(ta.Item)))
		}
		ta.Choice = stock.LineChoice{Treatment: t}
		if t != stock.AddToStock {
			ta.Label = rest
			return s.treatTracked(ctx, c, ta, "")
		}
		item, label, _ := strings.Cut(rest, ",")
		if item = strings.TrimSpace(item); item == "" {
			return s.treatAskItem(ctx, c, ta, "", nil)
		}
		ta.Label = strings.TrimSpace(label)
		return s.treatItemNamed(ctx, c, ta, item)
	case treatStepItem:
		switch {
		case press == "item:new", saidOK && said == lang.TreatAnother:
			return s.treatAskSell(ctx, c, ta, "")
		case strings.HasPrefix(press, "item:"):
			if n := treatNumber(strings.TrimPrefix(press, "item:"), len(ta.Offered)); n > 0 {
				return s.treatItemOffered(ctx, c, ta, ta.Offered[n-1])
			}
		case pressed:
		case treatNumber(text, len(ta.Offered)) > 0:
			return s.treatItemOffered(ctx, c, ta, ta.Offered[treatNumber(text, len(ta.Offered))-1])
		case strings.TrimSpace(text) != "":
			return s.treatItemNamed(ctx, c, ta, strings.TrimSpace(text))
		}
		return s.treatAskItem(ctx, c, ta, c.t("wa.treat.item_reminder", "item", whatsapp.SafeText(ta.Item)), nil)
	case treatStepSell:
		switch cmd {
		case "yes", "y":
			return s.treatNewItem(ctx, c, ta, false)
		case "no", "n":
			return s.treatNewItem(ctx, c, ta, true)
		}
		return s.treatAskSell(ctx, c, ta, c.t("wa.stock.sell_reminder", "name", whatsapp.SafeText(ta.Item)))
	case treatStepMany:
		switch {
		case press == "many:yes", !pressed && cmd == "yes":
			ta.Choice.TrackMany = true
		case press == "many:record", !pressed && cmd == "no", saidOK && said == lang.TreatRecord:
			ta.Choice = stock.LineChoice{Treatment: stock.Record}
		default:
			return s.treatAskMany(ctx, c, ta, "")
		}
		return s.treatLabelStep(ctx, c, ta, "")
	case treatStepLabel, treatStepTyped:
		switch {
		case press == "label:none", saidOK && said == lang.TreatNoLabel:
			ta.Choice.LabelID = nil
			return s.treatSave(ctx, c, ta, "")
		case press == "label:another", ta.Step == treatStepLabel && saidOK && said == lang.TreatAnother:
			return s.treatAskTyped(ctx, c, ta, "")
		case strings.HasPrefix(press, "label:") && ta.Step == treatStepLabel:
			if n := treatNumber(strings.TrimPrefix(press, "label:"), len(ta.Offered)); n > 0 {
				return s.treatLabelOffered(ctx, c, ta, ta.Offered[n-1])
			}
		case pressed:
		case ta.Step == treatStepLabel && treatNumber(text, len(ta.Offered)) > 0:
			return s.treatLabelOffered(ctx, c, ta, ta.Offered[treatNumber(text, len(ta.Offered))-1])
		case strings.TrimSpace(text) != "":
			return s.treatSave(ctx, c, ta, spend.NameFrom(text))
		}
		if ta.Step == treatStepTyped {
			return s.treatAskTyped(ctx, c, ta, "")
		}
		return s.treatAskLabel(ctx, c, ta, c.t("wa.treat.label_reminder", "item", whatsapp.SafeText(ta.Item)))
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.not_understood")+" "+s.waHelp(c))
}

// treatTracked checks a line kept track of: more than stock.MaxTracked
// cannot be, and is just recorded; more than stock.TrackAsk asks first.
// Then the label.
func (s *Server) treatTracked(ctx context.Context, c *chat, ta *treatAsk, before string) error {
	if ta.Choice.Treatment == stock.Track {
		switch {
		case ta.Quantity > stock.MaxTracked:
			ta.Choice = stock.LineChoice{Treatment: stock.Record}
			before = joinNote(before, c.t("wa.treat.too_many", "count", strconv.FormatInt(ta.Quantity, 10), "item", whatsapp.SafeText(ta.Item),
				"max", strconv.Itoa(stock.MaxTracked)))
		case ta.Quantity > stock.TrackAsk && !ta.Choice.TrackMany:
			return s.treatAskMany(ctx, c, ta, before)
		}
	}
	return s.treatLabelStep(ctx, c, ta, before)
}

// treatLabelStep saves the line with the label said with its treatment,
// or asks for one.
func (s *Server) treatLabelStep(ctx context.Context, c *chat, ta *treatAsk, before string) error {
	label := strings.TrimSpace(ta.Label)
	if label == "" {
		return s.treatAskLabel(ctx, c, ta, before)
	}
	if said, _, ok := lang.MatchTreat(label, c.lang.Match()); ok && said == lang.TreatNoLabel {
		ta.Choice.LabelID = nil
		return s.treatSaveNote(ctx, c, ta, "", before)
	}
	return s.treatSaveNote(ctx, c, ta, spend.NameFrom(label), before)
}

func joinNote(a, b string) string {
	if a == "" {
		return b
	}
	return a + "\n" + b
}

// treatItemNamed takes a stock item by its name: one is taken; several
// are offered; none asks again.
func (s *Server) treatItemNamed(ctx context.Context, c *chat, ta *treatAsk, name string) error {
	_, items, ok, err := s.stockChatItems(ctx, c)
	if !ok || err != nil {
		return err
	}
	match := treatChoosable(matchStock(treatChoosable(items), name))
	switch len(match) {
	case 0:
		return s.treatAskItem(ctx, c, ta, c.t("wa.treat.no_item", "name", whatsapp.SafeText(name)), nil)
	case 1:
		return s.treatItemChosen(ctx, c, ta, match[0])
	}
	return s.treatAskItem(ctx, c, ta, "", match)
}

// treatItemOffered takes a stock item offered.
func (s *Server) treatItemOffered(ctx context.Context, c *chat, ta *treatAsk, o treatOffer) error {
	_, items, ok, err := s.stockChatItems(ctx, c)
	if !ok || err != nil {
		return err
	}
	for _, it := range items {
		if it.ID == o.ID {
			return s.treatItemChosen(ctx, c, ta, it)
		}
	}
	return s.treatAskItem(ctx, c, ta, c.t("wa.shop.no_item", "name", whatsapp.SafeText(o.Name)), nil)
}

// treatItemChosen puts the line into an item's stock item, with a
// product's uses, as a line by hand naming it does (stock.LineOf: a
// product not counted yet starts being counted). Then the label.
func (s *Server) treatItemChosen(ctx context.Context, c *chat, ta *treatAsk, it stock.Item) error {
	var l stock.PurchaseLine
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		l, err = stock.LineOf(ctx, tx, c.tenant.ID, it, ta.Quantity, nil, c.p.Actor())
		return err
	})
	if errors.Is(err, stock.ErrPurchase) {
		return s.treatAskItem(ctx, c, ta, whatsapp.SafeText(purchaseReason(err))+".", nil)
	}
	if err != nil {
		return err
	}
	ta.Choice = stock.LineChoice{Treatment: stock.AddToStock, StockItemID: l.StockItemID, Uses: l.Uses}
	ta.StockName = it.Name
	return s.treatLabelStep(ctx, c, ta, "")
}

// treatNewItem makes the line's item a new stock item, as counting in
// something new does: a product counted by it (Do you sell it? Yes), or a
// material (No), with nothing in it yet.
func (s *Server) treatNewItem(ctx context.Context, c *chat, ta *treatAsk, material bool) error {
	audit := func(tx pgx.Tx, action, target string, detail map[string]any) error {
		return waAudit(ctx, tx, c.ch.Name(), c.p, action, target, detail)
	}
	key := "wa-treat-" + treatKey(ta.Order, ta.Position)
	it, _, err := s.stockAdd(s.waTablesCtx(ctx, c), c.tenant.ID, c.p.Actor(), key, stockNew{Name: ta.Item, Unit: ta.Unit, Material: material}, audit)
	if errors.Is(err, errItemExists) {
		// One of that name is there already: the line goes into it.
		return s.treatItemNamed(ctx, c, ta, ta.Item)
	}
	if err != nil {
		s.Logger.Warn("chat: a new stock item for a purchase line", "tenant", c.tenant.ID, "err", err)
		return s.treatAskItem(ctx, c, ta, c.t("wa.shop.add_failed", "reason", tablesChatReason(err)), nil)
	}
	return s.treatItemChosen(ctx, c, ta, it)
}

// treatLabelOffered saves the line with a label offered: one of the
// business's, or a new name, made now.
func (s *Server) treatLabelOffered(ctx context.Context, c *chat, ta *treatAsk, o treatOffer) error {
	if id, err := uuid.Parse(o.ID); err == nil {
		ta.Choice.LabelID = &id
		return s.treatSave(ctx, c, ta, "")
	}
	return s.treatSave(ctx, c, ta, o.Name)
}

func (s *Server) treatSave(ctx context.Context, c *chat, ta *treatAsk, label string) error {
	return s.treatSaveNote(ctx, c, ta, label, "")
}

// treatSaveNote saves the answer to the line (a label typed found or
// made), as the order's page does: the business's memory of the item is
// kept as this, the first time. Then the next line, or the orders for Yes.
func (s *Server) treatSaveNote(ctx context.Context, c *chat, ta *treatAsk, label, before string) error {
	var labelName string
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		if label != "" {
			l, made, err := spend.FindOrMakeTx(ctx, tx, c.tenant.ID, label, c.p.Actor())
			if err != nil {
				return err
			}
			if made {
				if err := waAudit(ctx, tx, c.ch.Name(), c.p, "money.label.made", l.Name, map[string]any{"label_id": l.ID}); err != nil {
					return err
				}
			}
			ta.Choice.LabelID = &l.ID
		}
		if ta.Choice.LabelID != nil {
			l, err := spend.KeptTx(ctx, tx, c.tenant.ID, *ta.Choice.LabelID)
			if err != nil {
				return err
			}
			labelName = l.Name
		}
		x, err := stock.SetLineTx(ctx, tx, c.tenant.ID, ta.Order, ta.Position, ta.Choice, false, c.p.Actor())
		if err != nil {
			return err
		}
		return waAudit(ctx, tx, c.ch.Name(), c.p, "purchase.line_set", x.Reference, map[string]any{"purchase_id": x.ID, "position": ta.Position,
			"treatment": ta.Choice.Treatment})
	})
	var track *stock.TrackError
	switch {
	case errors.As(err, &track):
		ta.Label = label
		if errors.Is(err, stock.ErrTrackAsk) {
			return s.treatAskMany(ctx, c, ta, before)
		}
		return s.treatTracked(ctx, c, ta, before)
	case errors.Is(err, spend.ErrLabel):
		ta.Choice.LabelID = nil
		return s.treatAskLabel(ctx, c, ta, joinNote(before, whatsapp.SafeText(strings.TrimPrefix(err.Error(), spend.ErrLabel.Error()+": "))+"."))
	case errors.Is(err, stock.ErrPurchase), errors.Is(err, stock.ErrLineLocked), errors.Is(err, stock.ErrNoPurchase):
		// The line cannot take it (the order moved on meanwhile): said,
		// and the next.
		ta.Done = append(ta.Done, treatKey(ta.Order, ta.Position))
		return s.treatFinish(ctx, c, ta, joinNote(before, c.t("wa.treat.not_saved", "item", whatsapp.SafeText(ta.Item), "reason", whatsapp.SafeText(purchaseReason(err)))))
	case err != nil:
		return err
	}
	ta.Done = append(ta.Done, treatKey(ta.Order, ta.Position))
	return s.treatFinish(ctx, c, ta, joinNote(before, c.t("wa.treat.saved", "item", whatsapp.SafeText(ta.Item), "how", whatsapp.SafeText(treatHow(c, ta, labelName)))))
}

// treatHow says how a line is treated now: "Keep track of it · Machines".
func treatHow(c *chat, ta *treatAsk, label string) string {
	how := c.t("button.treat_record")
	switch ta.Choice.Treatment {
	case stock.AddToStock:
		how = c.t("wa.treat.to_stock", "item", ta.StockName)
	case stock.Track:
		how = c.t("button.treat_track")
	}
	if label == "" {
		label = c.t("wa.treat.not_labelled")
	}
	return how + " · " + label
}

// purchaseReason is a purchase error's words, without the package's.
func purchaseReason(err error) string {
	msg := err.Error()
	for _, p := range []string{stock.ErrPurchase.Error() + ": ", stock.ErrLineLocked.Error() + ": ", "stock: "} {
		msg = strings.TrimPrefix(msg, p)
	}
	return msg
}
