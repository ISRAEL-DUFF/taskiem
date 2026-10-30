package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/reach"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/telegram"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Purchase orders in chat (docs/spec/stock.md §6, K3): the order sent to
// its supplier with Confirm and Change, the supplier's taps and words
// (CONFIRM, CHANGE P-7QX4KD only 10 bags), and the owner's words: add
// supplier, suppliers, order from, received, paid, send and confirmed.

// --- the supplier's buttons ---

// purchaseButtonPrefix marks a supplier's button: Confirm or Change on an
// order, signed for the business, the order and the supplier's contact.
const purchaseButtonPrefix = "po1."

var purchaseActions = map[byte]string{'c': "confirm", 'x': "change"}

type purchaseButton struct {
	Action   byte
	Tenant   uuid.UUID
	Purchase uuid.UUID
	Contact  uuid.UUID
}

func (b purchaseButton) body() []byte {
	out := make([]byte, 0, 49)
	out = append(out, b.Action)
	out = append(out, b.Tenant[:]...)
	out = append(out, b.Purchase[:]...)
	return append(out, b.Contact[:]...)
}

func purchaseButtonMAC(key, body []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("taskiem/purchase-button/v1\x00"))
	m.Write(body)
	return m.Sum(nil)[:16]
}

func signPurchaseButton(key []byte, b purchaseButton) string {
	body := b.body()
	return purchaseButtonPrefix + base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(purchaseButtonMAC(key, body))
}

var errBadPurchaseButton = errors.New("api: not a valid purchase button")

func parsePurchaseButton(key []byte, payload string) (purchaseButton, error) {
	var b purchaseButton
	rest, ok := strings.CutPrefix(payload, purchaseButtonPrefix)
	if !ok || len(key) < 32 || len(payload) > 200 {
		return b, errBadPurchaseButton
	}
	b64, m64, ok := strings.Cut(rest, ".")
	if !ok {
		return b, errBadPurchaseButton
	}
	body, err1 := base64.RawURLEncoding.DecodeString(b64)
	mac, err2 := base64.RawURLEncoding.DecodeString(m64)
	if err1 != nil || err2 != nil || len(body) != 49 || len(mac) != 16 || !hmac.Equal(mac, purchaseButtonMAC(key, body)) {
		return b, errBadPurchaseButton
	}
	if _, ok := purchaseActions[body[0]]; !ok {
		return b, errBadPurchaseButton
	}
	b.Action = body[0]
	copy(b.Tenant[:], body[1:17])
	copy(b.Purchase[:], body[17:33])
	copy(b.Contact[:], body[33:49])
	return b, nil
}

// supplierOrderText is what the supplier is sent: the order, and how to
// answer by words when the buttons are not there (outside WhatsApp's
// window).
func supplierOrderText(l lang.Tag, x stock.Purchase) string {
	return tr(l, "supplier.order", "reference", x.Reference, "items", whatsapp.SafeText(x.Words()), "total", storefront.FormatNaira(x.TotalKobo))
}

// sendSupplierItem sends a queued order (stock.OutboxSupplier) to its
// supplier's chat, with Confirm and Change; outside WhatsApp's window as
// the update template, which says to answer by words.
func (s *Server) sendSupplierItem(ctx context.Context, it whatsapp.OutboxItem) error {
	key := s.chatSigner().Key
	if !s.Purchases.Ready() || len(key) < 32 {
		return fmt.Errorf("%w: suppliers are off", whatsapp.ErrNothingToSend)
	}
	if it.Contact == nil || it.Run == nil {
		return fmt.Errorf("%w: incomplete", whatsapp.ErrNothingToSend)
	}
	ch, err := s.chatChannelFor(ctx, it.Channel, it.Tenant)
	if err != nil {
		return fmt.Errorf("%w: %w", whatsapp.ErrNothingToSend, err)
	}
	var x stock.Purchase
	var addr, business, offer string
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
		var err error
		if x, err = stock.PurchaseTx(ctx, tx, it.Tenant, *it.Run, false); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, it.Tenant).Scan(&business); err != nil {
			return err
		}
		if addr, err = s.Purchases.Book.AddressTx(ctx, tx, it.Tenant, reach.Target{Channel: it.Channel, Contact: *it.Contact}); err != nil {
			return err
		}
		// A supplier that is a business on Taskiem is offered a link with
		// the first order (business links, links.go).
		if it.Channel == "whatsapp" && x.Status == stock.Sent {
			offer, err = s.linkOfferLineIn(ctx, tx, it.Tenant, addr, s.chooseLang(ctx, it.Tenant, addr, "").Tag, nil)
		}
		return err
	})
	switch {
	case errors.Is(err, reach.ErrGone), errors.Is(err, stock.ErrNoPurchase):
		return fmt.Errorf("%w: %w", whatsapp.ErrNothingToSend, err)
	case err != nil:
		return err
	}
	if x.Status != stock.Sent {
		return fmt.Errorf("%w: the order moved on", whatsapp.ErrNothingToSend)
	}
	l := s.chooseLang(ctx, it.Tenant, addr, "").Tag
	text := supplierOrderText(l, x)
	if offer != "" {
		text += "\n\n" + offer
	}
	b := purchaseButton{Tenant: it.Tenant, Purchase: x.ID, Contact: *it.Contact}
	buttons := make([]whatsapp.Button, 0, 2)
	for _, a := range []byte{'c', 'x'} {
		b.Action = a
		buttons = append(buttons, whatsapp.Button{ID: signPurchaseButton(key, b), Title: tr(l, "supplier.btn."+purchaseActions[a])})
	}
	tpl := whatsapp.TplUpdate
	m := whatsapp.Message{Tenant: it.Tenant, Buttons: buttons, Text: ch.Prefix(business) + text, Template: &tpl,
		Vars: map[string]string{"tenant": business, "about": "order " + x.Reference, "message": oneLine(text + " " + tr(l, "supplier.reply_words", "reference", x.Reference))}}
	_, err = ch.Send(ctx, addr, m)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, whatsapp.ErrWindowClosed), whatsapp.TemplateUnusable(err):
		return fmt.Errorf("%w: outside WhatsApp's 24-hour window: %w", whatsapp.ErrNothingToSend, err)
	case errors.Is(err, telegram.ErrBlocked):
		if berr := s.Purchases.Book.Blocked(ctx, it.Channel, addr); berr != nil {
			return berr
		}
		return fmt.Errorf("%w: %w", whatsapp.ErrNothingToSend, err)
	}
	return fmt.Errorf("to %s: %w", whatsapp.MaskNumber(addr), err)
}

// supplierPress takes a supplier's tapped button from any chat address;
// false when the payload is not one.
func (s *Server) supplierPress(ctx context.Context, ch chatChannel, channel, addr, payload string) (bool, error) {
	if !strings.HasPrefix(payload, purchaseButtonPrefix) {
		return false, nil
	}
	l := s.chooseLang(ctx, ch.OwnTenant(), addr, "").Tag
	invalid := func() (bool, error) { return true, s.waSay(ctx, ch, addr, "", tr(l, "supplier.button_invalid")) }
	if !s.Purchases.Ready() {
		return invalid()
	}
	if !s.limiter("supplier-press:"+addr, time.Minute, 12).Allow() {
		return true, nil
	}
	b, err := parsePurchaseButton(s.chatSigner().Key, payload)
	if err != nil || (ch.OwnTenant() != uuid.Nil && ch.OwnTenant() != b.Tenant) {
		return invalid()
	}
	// The pressing address must be the contact the button was sent to,
	// and that contact the order's supplier.
	var mine bool
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{b.Tenant}, func(tx pgx.Tx) error {
		h, found, err := s.Purchases.Book.ContactAtTx(ctx, tx, b.Tenant, b.Contact, channel, addr)
		if err != nil || !found {
			return err
		}
		sh, err := stock.SupplierPhoneHashTx(ctx, tx, b.Tenant, b.Purchase)
		if errors.Is(err, stock.ErrNoPurchase) {
			return nil
		}
		mine = err == nil && hmac.Equal(h, sh)
		return err
	})
	if err != nil {
		return true, err
	}
	if !mine {
		return invalid()
	}
	return true, s.supplierAct(ctx, ch, addr, b.Tenant, b.Purchase, purchaseActions[b.Action], "")
}

// supplierAct is a supplier's Confirm, or Change with what they can supply
// (text "" asks for it).
func (s *Server) supplierAct(ctx context.Context, ch chatChannel, addr string, tenant, id uuid.UUID, action, text string) error {
	l := s.chooseLang(ctx, tenant, addr, "").Tag
	var x stock.Purchase
	var business string
	if action == "change" && strings.TrimSpace(text) == "" {
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			var err error
			x, err = stock.PurchaseTx(ctx, tx, tenant, id, false)
			return err
		})
		if err != nil {
			return err
		}
		return s.waSay(ctx, ch, addr, "", tr(l, "supplier.change_ask", "reference", x.Reference))
	}
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&business); err != nil {
			return err
		}
		var err error
		if action == "change" {
			x, err = stock.CounterTx(ctx, tx, tenant, id, text)
			return err
		}
		before, err := stock.PurchaseTx(ctx, tx, tenant, id, false)
		if err != nil {
			return err
		}
		if x, err = stock.ConfirmTx(ctx, tx, tenant, id, "supplier", "supplier"); err != nil {
			return err
		}
		if before.Status == stock.Sent {
			_, err = alerts.Notify(ctx, tx, tenant, alerts.Alert{Kind: stock.AlertConfirmed, Dedup: x.ID.String(),
				Title: x.Supplier + " confirmed order " + x.Reference, Body: "They will supply " + x.Words() + ".", Link: "/storefront/stock"})
		}
		return err
	})
	switch {
	case errors.Is(err, stock.ErrPurchase), errors.Is(err, stock.ErrNoPurchase):
		return s.waSay(ctx, ch, addr, "", tr(l, "supplier.button_invalid"))
	case err != nil:
		return err
	}
	if action == "change" {
		return s.waSay(ctx, ch, addr, "", tr(l, "supplier.changed", "business", whatsapp.SafeText(business), "reference", x.Reference))
	}
	return s.waSay(ctx, ch, addr, "", tr(l, "supplier.confirmed", "business", whatsapp.SafeText(business), "reference", x.Reference))
}

var supplierWordRe = regexp.MustCompile(`(?i)^\s*(confirm(?:ed)?|ok|change)\b\s*(?:order\s+)?(P-[A-Z0-9]{6})?\s*[:,.-]?\s*(.*?)\s*$`)

// supplierWord takes a supplier's CONFIRM or CHANGE in words (outside
// WhatsApp's window the order comes with no buttons), from an address
// linked to no member: the order named, else their only open one. false
// when the words are not those, or the sender has no order to answer.
func (s *Server) supplierWord(ctx context.Context, ch chatChannel, channel, addr, text string) (bool, error) {
	if !s.Purchases.Ready() {
		return false, nil
	}
	m := supplierWordRe.FindStringSubmatch(text)
	if m == nil {
		return false, nil
	}
	action := "confirm"
	if strings.EqualFold(m[1], "change") {
		action = "change"
	} else if m[3] != "" {
		return false, nil // "ok, thanks": not a confirm
	}
	phone := ""
	if channel == reach.WhatsApp {
		phone = addr
	}
	list, err := s.Purchases.SupplierOrders(ctx, phone, channel, addr)
	if err != nil || len(list) == 0 {
		return false, err
	}
	var mine []stock.SupplierOrder
	for _, o := range list {
		if ch.OwnTenant() != uuid.Nil && ch.OwnTenant() != o.Tenant {
			continue
		}
		if m[2] != "" && !strings.EqualFold(o.Reference, m[2]) {
			continue
		}
		if action == "confirm" && o.Status != stock.Sent {
			continue
		}
		mine = append(mine, o)
	}
	l := s.chooseLang(ctx, ch.OwnTenant(), addr, "").Tag
	switch len(mine) {
	case 0:
		return false, nil
	case 1:
		return true, s.supplierAct(ctx, ch, addr, mine[0].Tenant, mine[0].Purchase, action, m[3])
	}
	refs := make([]string, len(mine))
	for i, o := range mine {
		refs[i] = o.Reference
	}
	return true, s.waSay(ctx, ch, addr, "", tr(l, "supplier.which", "list", strings.Join(refs, ", ")))
}

// --- the owner's words ---

// purchaseChat is a purchase step being confirmed.
type purchaseChat struct {
	Supplier uuid.UUID `json:"supplier,omitempty"`
	Name     string    `json:"name,omitempty"`
	Phone    string    `json:"phone,omitempty"`
	// Items are the catalogue rows a new supplier supplies.
	Items    []string    `json:"items,omitempty"`
	Purchase uuid.UUID   `json:"purchase,omitempty"`
	Item     string      `json:"item,omitempty"`
	Quantity int64       `json:"quantity,omitempty"`
	Amount   int64       `json:"amount,omitempty"`
	How      string      `json:"how,omitempty"`
	Drafted  []uuid.UUID `json:"drafted,omitempty"`
}

var (
	addSupplierRe = regexp.MustCompile(`(?i)^\s*add\s+supplier\s+(.+?)\s+(\+?\d[\d\s-]{8,16}\d)(?:\s+for\s+(.+))?\s*$`)
	orderFromRe   = regexp.MustCompile(`(?i)^\s*(?:order|reorder)\s+from\s+(.+?)\s*$`)
	receivedRe    = regexp.MustCompile(`(?i)^\s*received\s+(\d[\d,]*\s+.+?)\s*$`)
	paidRe        = regexp.MustCompile(`(?i)^\s*paid\s+(.+?)\s+(?:₦|n|ngn)?\s*(\d[\d,.]*k?)\s*(cash|transfer|bank)?\s*$`)
	poRefRe       = regexp.MustCompile(`(?i)^\s*(send|confirmed)\s+(P-[A-Z0-9]{6})\s*$`)
)

// waPurchaseCommand acts on the owner's purchase words; false when text is
// none of them (so "paid ..." that names no supplier goes on to money).
func (s *Server) waPurchaseCommand(ctx context.Context, c *chat, text, cmd string) (bool, error) {
	if !s.Purchases.Ready() || !s.Stock.Ready() {
		return false, nil
	}
	switch {
	case cmd == "suppliers" || cmd == "my suppliers":
		return true, s.waSuppliers(ctx, c)
	case addSupplierRe.MatchString(text):
		return true, s.waAddSupplier(ctx, c, addSupplierRe.FindStringSubmatch(text))
	case strings.HasPrefix(cmd, "add supplier"):
		return true, c.say(ctx, s, c.t("wa.supplier.how"))
	case orderFromRe.MatchString(text):
		return true, s.waOrderFrom(ctx, c, orderFromRe.FindStringSubmatch(text)[1])
	case receivedRe.MatchString(text):
		return s.waReceived(ctx, c, receivedRe.FindStringSubmatch(text)[1])
	case paidRe.MatchString(text):
		return s.waPaidSupplier(ctx, c, paidRe.FindStringSubmatch(text))
	case poRefRe.MatchString(text):
		m := poRefRe.FindStringSubmatch(text)
		return true, s.waPurchaseRef(ctx, c, strings.ToLower(m[1]), strings.ToUpper(m[2]))
	}
	return false, nil
}

func (s *Server) chatSuppliers(ctx context.Context, c *chat) ([]stock.Supplier, error) {
	var list []stock.Supplier
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		list, err = s.Purchases.SuppliersTx(ctx, tx, c.tenant.ID, false)
		return err
	})
	return list, err
}

// waSuppliers answers "suppliers": each with their open orders and what
// is owed to them.
func (s *Server) waSuppliers(ctx context.Context, c *chat) error {
	if !c.p.Can(PermStorefrontManage) {
		return c.say(ctx, s, c.t("wa.supplier.denied", "org", c.tenant.Name))
	}
	list, err := s.chatSuppliers(ctx, c)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return c.say(ctx, s, c.t("wa.suppliers.none"))
	}
	lines := []string{c.t("wa.suppliers.title")}
	for _, x := range list {
		lines = append(lines, c.t("wa.suppliers.line", "name", whatsapp.SafeText(x.Name), "open", fmt.Sprint(x.Open), "owed", storefront.FormatNaira(x.OwedKobo)))
	}
	return c.say(ctx, s, strings.Join(lines, "\n"))
}

// waAddSupplier answers "add supplier Dangote Depot 0803 123 4567 for
// rice, indomie".
func (s *Server) waAddSupplier(ctx context.Context, c *chat, m []string) error {
	if !c.p.Can(PermStorefrontManage) {
		return c.say(ctx, s, c.t("wa.supplier.denied", "org", c.tenant.Name))
	}
	name := strings.TrimSpace(m[1])
	pc := &purchaseChat{Name: name, Phone: strings.Join(strings.Fields(m[2]), "")}
	var names []string
	if m[3] != "" {
		_, items, ok, err := s.stockChatItems(ctx, c)
		if !ok || err != nil {
			return err
		}
		for _, w := range strings.Split(m[3], ",") {
			for _, part := range strings.Split(w, " and ") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				match := matchStock(items, part)
				if len(match) != 1 {
					return c.say(ctx, s, c.t("wa.shop.no_item", "name", whatsapp.SafeText(part)))
				}
				pc.Items = append(pc.Items, match[0].ID)
				names = append(names, match[0].Name)
			}
		}
	}
	list := ""
	if len(names) > 0 {
		list = c.t("wa.supplier.for", "list", whatsapp.SafeText(strings.Join(names, ", ")))
	}
	return s.shopAsk(ctx, c, &chatShop{Action: "stock_supplier", StockChat: &stockChat{Purchase: pc}},
		c.t("wa.supplier.add_confirm", "name", whatsapp.SafeText(name), "items", list))
}

// waOrderFrom answers "order from Dangote": the low items naming the
// supplier, drafted as one order, sent on yes.
func (s *Server) waOrderFrom(ctx context.Context, c *chat, name string) error {
	if !c.p.Can(PermStorefrontManage) {
		return c.say(ctx, s, c.t("wa.supplier.denied", "org", c.tenant.Name))
	}
	list, err := s.chatSuppliers(ctx, c)
	if err != nil {
		return err
	}
	sup, names, err := stock.FindSupplier(list, name)
	if err != nil {
		if len(names) > 0 {
			return c.say(ctx, s, c.t("wa.order.which", "name", whatsapp.SafeText(name), "list", whatsapp.SafeText(strings.Join(names, ", "))))
		}
		return c.say(ctx, s, c.t("wa.order.no_supplier", "name", whatsapp.SafeText(name)))
	}
	_, items, ok, err := s.stockChatItems(ctx, c)
	if !ok || err != nil {
		return err
	}
	var theirs []stock.Item
	for _, it := range items {
		if strings.EqualFold(strings.TrimSpace(it.Supplier), sup.Name) {
			theirs = append(theirs, it)
		}
	}
	var drafted []stock.Purchase
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		drafted, _, err = s.Purchases.DraftLowTx(ctx, tx, c.tenant.ID, theirs, c.p.Actor(), "wa-"+uuid.NewString())
		return err
	}); err != nil {
		return err
	}
	if len(drafted) == 0 {
		return c.say(ctx, s, c.t("wa.order.none", "name", whatsapp.SafeText(sup.Name)))
	}
	x := drafted[0]
	return s.shopAsk(ctx, c, &chatShop{Action: "stock_order", StockChat: &stockChat{Purchase: &purchaseChat{Purchase: x.ID}}},
		c.t("wa.order.confirm", "reference", x.Reference, "items", whatsapp.SafeText(x.Words()), "supplier", whatsapp.SafeText(x.Supplier),
			"total", storefront.FormatNaira(x.TotalKobo)))
}

// waReceived answers "received 50 cartons of Indomie": against the open
// order from the item's supplier, else a plain receipt (as add does).
func (s *Server) waReceived(ctx context.Context, c *chat, rest string) (bool, error) {
	a, ok := stock.ParseAdd("add " + rest)
	if !ok {
		return false, nil
	}
	if !c.p.Can(PermTableEdit) {
		return true, c.say(ctx, s, c.t("wa.stock.denied", "org", c.tenant.Name))
	}
	_, items, ok, err := s.stockChatItems(ctx, c)
	if !ok || err != nil {
		return true, err
	}
	match := matchStock(items, a.Name)
	if len(match) == 1 && match[0].Supplier != "" {
		it := match[0]
		var x stock.Purchase
		err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
			var err error
			x, err = stock.OpenFromTx(ctx, tx, c.tenant.ID, it.Supplier, it.ID)
			return err
		})
		if err == nil {
			return true, s.shopAsk(ctx, c, &chatShop{Action: "stock_receive", Name: it.Name, Row: it.ID,
				StockChat: &stockChat{Purchase: &purchaseChat{Purchase: x.ID, Item: it.ID, Quantity: a.Quantity}}},
				c.t("wa.received.confirm", "count", whatsapp.SafeText(it.Quantity(a.Quantity)), "item", whatsapp.SafeText(it.Name),
					"reference", x.Reference, "supplier", whatsapp.SafeText(x.Supplier)))
		}
		if !errors.Is(err, stock.ErrNoPurchase) {
			return true, err
		}
	}
	return true, s.waStockAdd(ctx, c, a)
}

// waPaidSupplier answers "paid Dangote 425,000 transfer": the oldest
// order with something owed, else the oldest still open. false when the
// name is no supplier's (the money words take it).
func (s *Server) waPaidSupplier(ctx context.Context, c *chat, m []string) (bool, error) {
	list, err := s.chatSuppliers(ctx, c)
	if err != nil {
		return true, err
	}
	sup, _, ferr := stock.FindSupplier(list, m[1])
	if ferr != nil {
		return false, nil //nolint:nilerr // no supplier's name: the money words read it
	}
	if !c.p.Can(PermStorefrontManage) {
		return true, c.say(ctx, s, c.t("wa.supplier.denied", "org", c.tenant.Name))
	}
	amount, ok := storefront.ParsePrice(m[2])
	if !ok || amount <= 0 {
		return true, c.say(ctx, s, c.t("wa.paid.how"))
	}
	how := stock.Transfer
	if strings.EqualFold(m[3], "cash") {
		how = stock.Cash
	}
	var orders []stock.Purchase
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		orders, err = stock.UnpaidOfTx(ctx, tx, c.tenant.ID, sup.ID)
		return err
	}); err != nil {
		return true, err
	}
	if len(orders) == 0 {
		return true, c.say(ctx, s, c.t("wa.paid.none", "name", whatsapp.SafeText(sup.Name)))
	}
	x := orders[0]
	return true, s.shopAsk(ctx, c, &chatShop{Action: "stock_pay", StockChat: &stockChat{Purchase: &purchaseChat{Purchase: x.ID, Amount: amount, How: how}}},
		c.t("wa.paid.confirm", "supplier", whatsapp.SafeText(sup.Name), "amount", storefront.FormatNaira(amount), "how", c.t("sales.how_"+how),
			"reference", x.Reference))
}

// waPurchaseRef answers "send P-7QX4KD" (a draft approved and sent, by a
// second person above the limit) and "confirmed P-7QX4KD" (the supplier
// said yes by phone or SMS).
func (s *Server) waPurchaseRef(ctx context.Context, c *chat, verb, ref string) error {
	if !c.p.Can(PermStorefrontManage) {
		return c.say(ctx, s, c.t("wa.supplier.denied", "org", c.tenant.Name))
	}
	var x stock.Purchase
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		o, err := stock.PurchaseByReferenceTx(ctx, tx, c.tenant.ID, ref)
		if err != nil {
			return err
		}
		if verb == "send" {
			x, err = s.Purchases.SendTx(ctx, tx, c.tenant.ID, o.ID, c.p.Actor())
		} else {
			x, err = stock.ConfirmTx(ctx, tx, c.tenant.ID, o.ID, "owner", c.p.Actor())
		}
		if err != nil {
			return err
		}
		return waAudit(ctx, tx, c.ch.Name(), c.p, "purchase."+verb, x.Reference, map[string]any{"purchase_id": x.ID})
	})
	if err != nil {
		return s.purchaseChatFail(ctx, c, err)
	}
	if verb == "send" {
		if err := s.waDrainOutbox(ctx, c.tenant.ID); err != nil {
			s.Logger.Warn("stock: sending an order", "err", err)
		}
		return c.say(ctx, s, c.t("wa.order.sent", "reference", x.Reference, "supplier", whatsapp.SafeText(x.Supplier)))
	}
	return c.say(ctx, s, c.t("wa.order.marked_confirmed", "reference", x.Reference))
}

// purchaseChatFail says why a purchase step could not be made.
func (s *Server) purchaseChatFail(ctx context.Context, c *chat, err error) error {
	msg := err.Error()
	for _, p := range []string{"stock: that does not fit the order: ", "stock: already a supplier: ", "stock: a second person approves this order: "} {
		msg = strings.ReplaceAll(msg, p, "")
	}
	switch {
	case errors.Is(err, stock.ErrNoPurchase):
		return c.say(ctx, s, c.t("wa.order.unknown"))
	case errors.Is(err, stock.ErrPurchase), errors.Is(err, stock.ErrSupplierExists), errors.Is(err, stock.ErrSecondApproval):
		if serr := s.saveSession(ctx, c, stateIdle, chatData{}, 0); serr != nil {
			return serr
		}
		return c.say(ctx, s, whatsapp.SafeText(msg)+".")
	}
	return err
}

// waPurchaseApply writes a confirmed purchase step.
func (s *Server) waPurchaseApply(ctx context.Context, c *chat) error {
	sh := c.data.Shop
	pc := sh.StockChat.Purchase
	need := PermStorefrontManage
	if sh.Action == "stock_receive" {
		need = PermTableEdit
	}
	if !c.p.Can(need) {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.supplier.denied", "org", c.tenant.Name))
	}
	if !s.limiter("wa-shop:"+c.addr, 20*time.Second, 10).Allow() {
		return c.say(ctx, s, c.t("wa.shop.too_many"))
	}
	key := "wa-" + sh.Nonce
	var msg string
	switch sh.Action {
	case "stock_supplier":
		var x stock.Supplier
		err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
			var err error
			if x, err = s.Purchases.AddSupplierTx(ctx, tx, c.tenant.ID, pc.Name, pc.Phone, c.p.Actor()); err != nil {
				return err
			}
			return waAudit(ctx, tx, c.ch.Name(), c.p, "supplier.add", x.Name, map[string]any{"supplier_id": x.ID})
		})
		if err != nil {
			return s.purchaseChatFail(ctx, c, err)
		}
		if err := s.supplyItems(s.waTablesCtx(ctx, c), c.tenant.ID, x.Name, pc.Items, key); err != nil {
			s.Logger.Warn("chat: naming a supplier on items", "tenant", c.tenant.ID, "err", err)
		}
		msg = c.t("wa.supplier.added", "name", whatsapp.SafeText(x.Name))
	case "stock_order":
		var x stock.Purchase
		err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
			var err error
			if x, err = s.Purchases.SendTx(ctx, tx, c.tenant.ID, pc.Purchase, c.p.Actor()); err != nil {
				return err
			}
			return waAudit(ctx, tx, c.ch.Name(), c.p, "purchase.send", x.Reference, map[string]any{"purchase_id": x.ID})
		})
		if errors.Is(err, stock.ErrSecondApproval) {
			var limit stock.Settings
			_ = chatTx(ctx, s, c, func(tx pgx.Tx) error {
				var err error
				limit, err = stock.SettingsTx(ctx, tx, c.tenant.ID)
				x, _ = stock.PurchaseTx(ctx, tx, c.tenant.ID, pc.Purchase, false)
				return err
			})
			lim := int64(0)
			if limit.PurchaseLimitKobo != nil {
				lim = *limit.PurchaseLimitKobo
			}
			msg = c.t("wa.order.second", "reference", x.Reference, "limit", storefront.FormatNaira(lim))
			break
		}
		if err != nil {
			return s.purchaseChatFail(ctx, c, err)
		}
		if err := s.waDrainOutbox(ctx, c.tenant.ID); err != nil {
			s.Logger.Warn("stock: sending an order", "err", err)
		}
		msg = c.t("wa.order.sent", "reference", x.Reference, "supplier", whatsapp.SafeText(x.Supplier))
	case "stock_receive":
		x, err := s.Purchases.Receive(s.waTablesCtx(ctx, c), c.tenant.ID, pc.Purchase, map[string]int64{pc.Item: pc.Quantity}, c.p.Actor(), key)
		if err != nil {
			return s.purchaseChatFail(ctx, c, err)
		}
		if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
			return waAudit(ctx, tx, c.ch.Name(), c.p, "purchase.receive", x.Reference, map[string]any{"purchase_id": x.ID, "status": x.Status})
		}); err != nil {
			return err
		}
		msg = c.t("wa.received.done", "reference", x.Reference, "status", c.t("purchase.status_"+x.Status))
	case "stock_pay":
		var x stock.Purchase
		err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
			var err error
			if x, err = stock.PayTx(ctx, tx, c.tenant.ID, pc.Purchase, pc.Amount, pc.How, c.p.Actor(), key, s.moneyLog()); err != nil {
				return err
			}
			return waAudit(ctx, tx, c.ch.Name(), c.p, "purchase.pay", x.Reference, map[string]any{"purchase_id": x.ID, "amount_kobo": pc.Amount})
		})
		if err != nil {
			return s.purchaseChatFail(ctx, c, err)
		}
		msg = c.t("wa.paid.done", "amount", storefront.FormatNaira(pc.Amount), "supplier", whatsapp.SafeText(x.Supplier), "reference", x.Reference,
			"owed", storefront.FormatNaira(x.OwedKobo))
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	return c.say(ctx, s, msg)
}
