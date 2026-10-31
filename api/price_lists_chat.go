package api

// A linked supplier's prices in chat (business links L4, §9): "prices
// from Dangote" lists them; "order 20 bags of rice from Dangote" drafts a
// purchase order from them, at the list's price, and asks before it goes
// (as "order from Dangote" does).

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

var (
	pricesFromRe = regexp.MustCompile(`(?i)^\s*(?:prices?|price\s+list)\s+(?:from|of)\s+(.+?)\s*$`)
	orderItemRe  = regexp.MustCompile(`(?i)^\s*order\s+(\d[\d,]*)\s+(.+?)\s+from\s+(.+?)\s*$`)
	unitOfRe     = regexp.MustCompile(`(?i)^\S+\s+of\s+(.+)$`)
)

// chatSupplierPrices is the linked supplier a name means, among those
// sharing prices with the business: one, or the names it could be.
func (s *Server) chatSupplierPrices(ctx context.Context, c *chat, name string) (*links.SupplierPrices, []string, error) {
	var got []links.SupplierPrices
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		got, err = links.SupplierPricesTx(ctx, tx, c.tenant.ID, uuid.Nil)
		return err
	}); err != nil {
		return nil, nil, err
	}
	want := strings.ToLower(strings.TrimSpace(name))
	var hits []links.SupplierPrices
	for _, p := range got {
		n := strings.ToLower(p.Business)
		if n == want {
			return &p, nil, nil
		}
		if strings.HasPrefix(n, want) || strings.Contains(n, " "+want) {
			hits = append(hits, p)
		}
	}
	if len(hits) == 1 {
		return &hits[0], nil, nil
	}
	names := []string{}
	for _, h := range hits {
		names = append(names, h.Business)
	}
	return nil, names, nil
}

// priceLine is an item of a price list in chat: "*Rice, 50 kg*: ₦45,000 a
// bag (at least 5)".
func priceLine(c *chat, it links.PriceItem) string {
	line := "*" + whatsapp.SafeText(it.Name) + "*: " + storefront.FormatNaira(it.PriceKobo)
	if it.Unit != "" {
		line += " " + c.t("wa.prices.per", "unit", whatsapp.SafeText(it.Unit))
	}
	if it.MinQuantity > 1 {
		line += " " + c.t("wa.prices.min", "count", strconv.Itoa(it.MinQuantity))
	}
	if !it.Available {
		line += " " + c.t("wa.prices.out")
	}
	return line
}

// waPricesFrom answers "prices from Dangote".
func (s *Server) waPricesFrom(ctx context.Context, c *chat, name string) error {
	p, names, err := s.chatSupplierPrices(ctx, c, name)
	if err != nil {
		return err
	}
	if p == nil {
		if len(names) > 1 {
			return c.say(ctx, s, c.t("wa.order.which", "name", whatsapp.SafeText(name), "list", whatsapp.SafeText(strings.Join(names, ", "))))
		}
		return c.say(ctx, s, c.t("wa.prices.none", "name", whatsapp.SafeText(name)))
	}
	lines := make([]string, 0, len(p.Items))
	for i, it := range p.Items {
		if i == 30 {
			lines = append(lines, c.t("wa.prices.more", "count", strconv.Itoa(len(p.Items)-30)))
			break
		}
		lines = append(lines, priceLine(c, it))
	}
	return c.say(ctx, s, c.t("wa.prices.list", "name", whatsapp.SafeText(p.Business), "items", strings.Join(lines, "\n")))
}

// waOrderItem answers "order 20 bags of rice from Dangote": the item on
// Dangote's price list, drafted at its price, sent on yes.
func (s *Server) waOrderItem(ctx context.Context, c *chat, m []string) error {
	if !c.p.Can(PermStorefrontManage) {
		return c.say(ctx, s, c.t("wa.supplier.denied", "org", c.tenant.Name))
	}
	qty, err := strconv.ParseInt(strings.ReplaceAll(m[1], ",", ""), 10, 64)
	if err != nil || qty <= 0 {
		return c.say(ctx, s, c.t("wa.prices.how"))
	}
	p, names, err := s.chatSupplierPrices(ctx, c, m[3])
	if err != nil {
		return err
	}
	if p == nil {
		if len(names) > 1 {
			return c.say(ctx, s, c.t("wa.order.which", "name", whatsapp.SafeText(m[3]), "list", whatsapp.SafeText(strings.Join(names, ", "))))
		}
		return c.say(ctx, s, c.t("wa.prices.none", "name", whatsapp.SafeText(m[3])))
	}
	words := strings.ToLower(strings.TrimSpace(m[2]))
	if u := unitOfRe.FindStringSubmatch(words); u != nil {
		words = u[1]
	}
	var hits []links.PriceItem
	for _, it := range p.Items {
		n := strings.ToLower(it.Name)
		if n == words {
			hits = []links.PriceItem{it}
			break
		}
		if strings.Contains(n, words) || strings.Contains(words, n) {
			hits = append(hits, it)
		}
	}
	switch len(hits) {
	case 0:
		return c.say(ctx, s, c.t("wa.prices.no_item", "name", whatsapp.SafeText(p.Business), "item", whatsapp.SafeText(m[2])))
	case 1:
	default:
		list := make([]string, len(hits))
		for i, h := range hits {
			list[i] = h.Name
		}
		return c.say(ctx, s, c.t("wa.order.which", "name", whatsapp.SafeText(m[2]), "list", whatsapp.SafeText(strings.Join(list, ", "))))
	}
	it := hits[0]
	_, items, ok, err := s.stockChatItems(ctx, c)
	if !ok || err != nil {
		return err
	}
	own := map[string]stock.Item{}
	for _, x := range items {
		own[x.ID] = x
	}
	line := orderLineIn{SellerItemID: it.ItemID, Quantity: qty}
	var x stock.Purchase
	err = chatTx(ctx, s, c, func(tx pgx.Tx) error {
		linked, err := links.ItemLinksTx(ctx, tx, c.tenant.ID, p.LinkID)
		if err != nil {
			return err
		}
		known := false
		for _, l := range linked {
			known = known || l.SellerItemID == it.ItemID
		}
		// The first time, an own item of the same name is the one.
		if !known {
			var same []stock.Item
			for _, o := range items {
				if strings.EqualFold(strings.TrimSpace(o.Name), strings.TrimSpace(it.Name)) {
					same = append(same, o)
				}
			}
			if len(same) == 1 {
				line.ItemID = same[0].ID
			}
		}
		x, err = s.draftFromPricesTx(ctx, tx, c.p, p.LinkID, []orderLineIn{line}, own, "", "wa-"+uuid.NewString())
		return err
	})
	var unlinked errUnlinkedItem
	switch {
	case errors.As(err, &unlinked):
		return c.say(ctx, s, c.t("wa.prices.unlinked", "item", whatsapp.SafeText(it.Name), "name", whatsapp.SafeText(p.Business)))
	case errors.Is(err, links.ErrInvalid), errors.Is(err, links.ErrState):
		return c.say(ctx, s, c.t("wa.prices.refused", "reason", whatsapp.SafeText(chatReason(err))))
	case errors.Is(err, stock.ErrPurchase), errors.Is(err, stock.ErrNoSupplier):
		return s.purchaseChatFail(ctx, c, err)
	case err != nil:
		return err
	}
	return s.shopAsk(ctx, c, &chatShop{Action: "stock_order", StockChat: &stockChat{Purchase: &purchaseChat{Purchase: x.ID}}},
		c.t("wa.order.confirm", "reference", x.Reference, "items", whatsapp.SafeText(x.Words()), "supplier", whatsapp.SafeText(x.Supplier),
			"total", storefront.FormatNaira(x.TotalKobo)))
}

// chatReason is an error's words, without the package's prefix.
func chatReason(err error) string {
	msg := err.Error()
	for _, p := range []string{links.ErrInvalid.Error() + ": ", links.ErrState.Error() + ": "} {
		msg = strings.TrimPrefix(msg, p)
	}
	return msg
}
