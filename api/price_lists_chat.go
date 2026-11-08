package api

// A linked supplier's prices in chat (business links L4, §9): "prices
// from Dangote" lists them; "order 20 bags of rice from Dangote" drafts a
// purchase order from them, at the list's price, and asks before it goes
// (as "order from Dangote" does).

import (
	"context"
	"errors"
	"fmt"
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
	hits, sized := pricedSizes(p.Items, m[2])
	if sized != nil {
		// A product sold in sizes and colours, its size not said: by
		// name here, or many at once in the chat on Taskiem.
		example := sized.Name
		if len(hits) > 0 {
			example = hits[0].Name
		}
		return c.say(ctx, s, c.t("wa.order.which_size", "name", whatsapp.SafeText(sized.Name), "business", whatsapp.SafeText(p.Business),
			"sizes", whatsapp.SafeText(pricedSummary(*sized)), "example", whatsapp.SafeText(fmt.Sprintf("order %d %s from %s", qty, example, p.Business)))+
			s.waLink(c, "/chat?with="+p.LinkID.String()))
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
	key := "wa-" + uuid.NewString()
	// A size: the buyer's own size of the same values, added to their
	// product when it lacks it, as the order sheet does (trade_sizes.go).
	if it.VariantOf != "" {
		product := it.Name
		for _, x := range p.Items {
			if x.ItemID == it.VariantOf {
				product = x.Name
			}
		}
		for _, o := range items {
			if o.VariantOf == "" && strings.EqualFold(strings.TrimSpace(o.Name), strings.TrimSpace(product)) {
				line.ItemID = o.ID // the first time, their product of the same name
			}
		}
		audit := func(tx pgx.Tx, action, target string, detail map[string]any) error {
			return waAudit(ctx, tx, c.ch.Name(), c.p, action, target, detail)
		}
		resolved, rerr := s.resolveSizeLines(s.waTablesCtx(ctx, c), c.p, audit, p.LinkID, []orderLineIn{line}, key)
		var differ errSizesDiffer
		var first errSizesFirst
		var unlinked errUnlinkedItem
		switch {
		case errors.As(rerr, &differ):
			return c.say(ctx, s, c.t("wa.prices.refused", "reason", whatsapp.SafeText(differ.Error())))
		case errors.As(rerr, &first):
			return c.say(ctx, s, c.t("wa.prices.refused", "reason", whatsapp.SafeText(first.Error())))
		case errors.As(rerr, &unlinked):
			return c.say(ctx, s, c.t("wa.prices.unlinked", "item", whatsapp.SafeText(product), "name", whatsapp.SafeText(p.Business)))
		case errors.Is(rerr, links.ErrInvalid), errors.Is(rerr, links.ErrState):
			return c.say(ctx, s, c.t("wa.prices.refused", "reason", whatsapp.SafeText(linkReason(rerr))))
		case rerr != nil:
			return rerr
		}
		line = resolved[0]
		if _, items, ok, err = s.stockChatItems(ctx, c); !ok || err != nil {
			return err
		}
		for _, x := range items {
			own[x.ID] = x
		}
	}
	var x stock.Purchase
	err = chatTx(ctx, s, c, func(tx pgx.Tx) error {
		linked, err := links.ItemLinksTx(ctx, tx, c.tenant.ID, p.LinkID)
		if err != nil {
			return err
		}
		known := it.VariantOf != "" // its size is the buyer's already
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
		x, err = s.draftFromPricesTx(ctx, tx, c.p, p.LinkID, []orderLineIn{line}, own, "", key)
		return err
	})
	var unlinked errUnlinkedItem
	switch {
	case errors.As(err, &unlinked):
		return c.say(ctx, s, c.t("wa.prices.unlinked", "item", whatsapp.SafeText(it.Name), "name", whatsapp.SafeText(p.Business)))
	case errors.Is(err, links.ErrInvalid), errors.Is(err, links.ErrState):
		return c.say(ctx, s, c.t("wa.prices.refused", "reason", whatsapp.SafeText(linkReason(err))))
	case errors.Is(err, stock.ErrPurchase), errors.Is(err, stock.ErrNoSupplier):
		return s.purchaseChatFail(ctx, c, err)
	case err != nil:
		return err
	}
	return s.shopAsk(ctx, c, &chatShop{Action: "stock_order", StockChat: &stockChat{Purchase: &purchaseChat{Purchase: x.ID}}},
		c.t("wa.order.confirm", "reference", x.Reference, "items", whatsapp.SafeText(x.Words()), "supplier", whatsapp.SafeText(x.Supplier),
			"total", storefront.FormatNaira(x.TotalKobo)))
}

// linkReason is a links error's words, without the package's prefix.
func linkReason(err error) string {
	msg := err.Error()
	for _, p := range []string{links.ErrInvalid.Error() + ": ", links.ErrState.Error() + ": "} {
		msg = strings.TrimPrefix(msg, p)
	}
	return msg
}

// pricedSummary says a listed product's sizes and colours on sale by its
// choices: "40, 41, 42 · Black, White".
func pricedSummary(product links.PriceItem) string {
	parts := make([]string, 0, len(product.Choices))
	for _, c := range product.Choices {
		var vals []string
		for _, v := range c.Values {
			for _, z := range product.Sizes {
				if z.Available && strings.EqualFold(z.Variant[c.Name], v) {
					vals = append(vals, v)
					break
				}
			}
		}
		if len(vals) > 0 {
			parts = append(parts, strings.Join(vals, ", "))
		}
	}
	return strings.Join(parts, " · ")
}
