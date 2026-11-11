package api

// Mentions (business links L4, §5.5, §9): "@" names a linked business,
// "#" one of its items. In the Taskiem conversation, a message naming
// businesses ("Buy 400 bags of cement from @Dangote and 20 pieces of
// blocks from @Cherry") is read back as one draft order per business,
// from each one's price list, and one yes sends them all; a mention of a
// business not linked offers the link. People on the other side are not
// mentioned: businesses only (an owner's decision).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// mentionOrder is what a message with mentions asks for: per business
// mentioned, what of it; and the names mentioned that are no linked
// business.
type mentionOrder struct {
	Parts   []mentionPart
	Unknown []string
}

type mentionPart struct {
	Business string // as linked (the first of Links)
	// Links are the linked businesses the mention names: more than one is
	// asked about, never picked in silence (links.Mentioned).
	Links []links.Link
	Text  string // what was said before the mention: "400 bags of cement"
	// Typed is the mention's words as typed, without the "@"; At and End
	// are where it starts (the "@") and ends, in runes.
	Typed   string
	At, End int
}

var (
	mentionWordRe = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}'&.-]*`)
	orderVerbRe   = regexp.MustCompile(`(?i)^\s*(?:please\s+)?(?:buy|order|get|send|i\s+want|we\s+want|i\s+need|we\s+need)\b\s*`)
	connectorRe   = regexp.MustCompile(`(?i)^\s*(?:,|and\b|then\b|also\b|plus\b)\s*`)
	fromTailRe    = regexp.MustCompile(`(?i)\s*\b(?:from|by|at|with)\s*$`)
	qtyPhraseRe   = regexp.MustCompile(`^\s*(\d[\d,]*)\s+(.+?)\s*$`)
	qtyBeforeRe   = regexp.MustCompile(`(\d[\d,]*)\s+(?:[\p{L}]+\s+of\s+|[\p{L}]+\s+)?$`)
	phraseSplitRe = regexp.MustCompile(`(?i)\s*(?:,|\band\b)\s*(?:\d)`)
)

// parseMentions finds the "@" mentions in text against the active linked
// businesses, each by the start of its name (links.Mentioned: "@Dangote"
// names Dangote Nigeria Ltd), and what each is asked for: the words
// between it and the mention before.
func parseMentions(text string, active []links.Link) mentionOrder {
	r := []rune(text)
	var out mentionOrder
	prev := 0
	for i := 0; i < len(r); i++ {
		if r[i] != '@' || (i > 0 && !strings.ContainsRune(" \t\n,(", r[i-1])) {
			continue
		}
		rest := string(r[i+1:])
		var end int
		if hit, n := links.Mentioned(active, rest); n > 0 {
			end = i + 1 + n
			out.Parts = append(out.Parts, mentionPart{Business: hit[0].Name, Links: hit, Text: cleanAsk(string(r[prev:i])),
				Typed: string(r[i+1 : end]), At: i, End: end})
		} else {
			w := mentionWordRe.FindString(rest)
			// "@4500" is an amount ("cement @4500"), not a business.
			if w == "" || unicode.IsDigit([]rune(w)[0]) {
				continue
			}
			out.Unknown = append(out.Unknown, w)
			end = i + 1 + len([]rune(w))
		}
		prev = end
		i = end - 1
	}
	return out
}

// mentionChoices lists the businesses a mention could be, each with its
// role: "Dangote Cement (supplier), Dangote Sugar (customer)".
func mentionChoices(c *chat, ls []links.Link) string {
	list := make([]string, len(ls))
	for i, l := range ls {
		list[i] = whatsapp.SafeText(l.Name) + " (" + c.t("link.role."+l.Role) + ")"
	}
	return strings.Join(list, ", ")
}

// cleanAsk is the words of an ask without "buy", "and" or "from".
func cleanAsk(s string) string {
	s = connectorRe.ReplaceAllString(s, "")
	s = orderVerbRe.ReplaceAllString(s, "")
	s = connectorRe.ReplaceAllString(s, "")
	s = fromTailRe.ReplaceAllString(s, "")
	return strings.TrimSpace(strings.ReplaceAll(s, "#", ""))
}

// askedLine is an item of a price list asked for, and how many.
type askedLine struct {
	Item     links.PriceItem
	Quantity int64
}

// askedItems reads what an ask says of a price list: each item named in
// full (so names with commas hold), then each "20 bags of rice" left over
// matched by its words. bad names what could not be read.
func askedItems(ask string, items []links.PriceItem) (got []askedLine, bad []string, which [][]links.PriceItem) {
	lower := strings.ToLower(ask)
	taken := make([]bool, len(lower))
	byLen := slices.Clone(items)
	slices.SortFunc(byLen, func(a, b links.PriceItem) int { return len(b.Name) - len(a.Name) })
	for _, it := range byLen {
		name := strings.ToLower(it.Name)
		for from := 0; ; {
			j := strings.Index(lower[from:], name)
			if j < 0 {
				break
			}
			j += from
			from = j + len(name)
			if slices.Contains(taken[j:j+len(name)], true) {
				continue
			}
			m := qtyBeforeRe.FindStringSubmatchIndex(lower[:j])
			if m == nil {
				continue
			}
			q, err := strconv.ParseInt(strings.ReplaceAll(lower[m[2]:m[3]], ",", ""), 10, 64)
			if err != nil || q <= 0 {
				continue
			}
			for k := m[0]; k < j+len(name); k++ {
				taken[k] = true
			}
			got = append(got, askedLine{Item: it, Quantity: q})
		}
	}
	// What is left, phrase by phrase.
	var left strings.Builder
	for k := range lower {
		if taken[k] {
			left.WriteByte(' ')
		} else {
			left.WriteByte(lower[k])
		}
	}
	rest := left.String()
	var phrases []string
	for {
		loc := phraseSplitRe.FindStringIndex(rest)
		if loc == nil {
			break
		}
		phrases = append(phrases, rest[:loc[0]])
		rest = rest[loc[1]-1:]
	}
	phrases = append(phrases, rest)
	for _, p := range phrases {
		p = strings.Trim(strings.TrimSpace(p), ",")
		if p == "" || strings.TrimSpace(strings.ReplaceAll(p, "and", "")) == "" {
			continue
		}
		m := qtyPhraseRe.FindStringSubmatch(p)
		if m == nil {
			bad = append(bad, p)
			continue
		}
		q, err := strconv.ParseInt(strings.ReplaceAll(m[1], ",", ""), 10, 64)
		if err != nil || q <= 0 {
			bad = append(bad, p)
			continue
		}
		hits := matchPriceItems(items, m[2])
		switch len(hits) {
		case 0:
			bad = append(bad, m[2])
		case 1:
			got = append(got, askedLine{Item: hits[0], Quantity: q})
		default:
			which = append(which, hits)
		}
	}
	return got, bad, which
}

// matchPriceItems is the items of a list that words mean ("bags of rice"
// is rice): the one named exactly, else those whose name holds the words
// or is held in them.
func matchPriceItems(items []links.PriceItem, words string) []links.PriceItem {
	words = strings.ToLower(strings.TrimSpace(words))
	if u := unitOfRe.FindStringSubmatch(words); u != nil {
		words = u[1]
	}
	var hits []links.PriceItem
	for _, it := range items {
		n := strings.ToLower(it.Name)
		if n == words {
			return []links.PriceItem{it}
		}
		if words != "" && (strings.Contains(n, words) || strings.Contains(words, n)) {
			hits = append(hits, it)
		}
	}
	return hits
}

// waMentionOrder answers a message naming businesses in the Taskiem
// conversation; false when the message names none (it is something
// else).
func (s *Server) waMentionOrder(ctx context.Context, c *chat, text string) (bool, error) {
	var linked []links.Link
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		linked, _, err = links.ListTx(ctx, tx)
		return err
	}); err != nil {
		return true, err
	}
	mo := parseMentions(text, linked)
	if len(mo.Parts) == 0 && len(mo.Unknown) == 0 {
		return false, nil
	}
	if len(mo.Unknown) > 0 {
		return true, c.say(ctx, s, c.t("wa.mention.unlinked", "name", whatsapp.SafeText(mo.Unknown[0])))
	}
	for _, part := range mo.Parts {
		// Two businesses the mention names: never one picked in silence.
		if len(part.Links) > 1 {
			return true, c.say(ctx, s, c.t("wa.mention.which", "name", whatsapp.SafeText(part.Typed), "list", mentionChoices(c, part.Links)))
		}
	}
	if !c.p.Can(PermStorefrontManage) {
		return true, c.say(ctx, s, c.t("wa.supplier.denied", "org", c.tenant.Name))
	}
	// Each business's price list, and what is asked of it.
	var prices []links.SupplierPrices
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		prices, err = links.SupplierPricesTx(ctx, tx, c.tenant.ID, uuid.Nil)
		return err
	}); err != nil {
		return true, err
	}
	type draft struct {
		link  links.Link
		lines []askedLine
	}
	var drafts []draft
	for _, part := range mo.Parts {
		l := part.Links[0]
		i := slices.IndexFunc(prices, func(p links.SupplierPrices) bool { return p.LinkID == l.ID })
		if i < 0 {
			text := c.t("wa.prices.none", "name", whatsapp.SafeText(l.Name))
			if !strings.ContainsAny(part.Text, "0123456789") {
				// Not an order at all: how to send it as a message.
				text += "\n" + c.t("chat.direct.hint", "name", whatsapp.SafeText(l.Name))
			}
			return true, c.say(ctx, s, text)
		}
		if !strings.ContainsAny(part.Text, "0123456789") {
			return true, c.say(ctx, s, c.t("wa.mention.how", "name", whatsapp.SafeText(l.Name)))
		}
		got, bad, which := askedItems(part.Text, prices[i].Items)
		switch {
		case len(which) > 0:
			list := make([]string, len(which[0]))
			for k, h := range which[0] {
				list[k] = h.Name
			}
			return true, c.say(ctx, s, c.t("wa.order.which", "name", whatsapp.SafeText(part.Text), "list", whatsapp.SafeText(strings.Join(list, ", "))))
		case len(bad) > 0:
			return true, c.say(ctx, s, c.t("wa.prices.no_item", "name", whatsapp.SafeText(l.Name), "item", whatsapp.SafeText(bad[0])))
		case len(got) == 0:
			return true, c.say(ctx, s, c.t("wa.mention.how", "name", whatsapp.SafeText(l.Name)))
		}
		k := slices.IndexFunc(drafts, func(d draft) bool { return d.link.ID == l.ID })
		if k < 0 {
			drafts = append(drafts, draft{link: l})
			k = len(drafts) - 1
		}
		drafts[k].lines = append(drafts[k].lines, got...)
	}
	_, items, ok, err := s.stockChatItems(ctx, c)
	if !ok || err != nil {
		return true, err
	}
	own := map[string]stock.Item{}
	for _, x := range items {
		own[x.ID] = x
	}
	var made []stock.Purchase
	var unlinked errUnlinkedItem
	var unlinkedFrom string
	err = chatTx(ctx, s, c, func(tx pgx.Tx) error {
		for _, d := range drafts {
			known, err := links.ItemLinksTx(ctx, tx, c.tenant.ID, d.link.ID)
			if err != nil {
				return err
			}
			in := make([]orderLineIn, 0, len(d.lines))
			for _, l := range d.lines {
				line := orderLineIn{SellerItemID: l.Item.ItemID, Quantity: l.Quantity}
				if !slices.ContainsFunc(known, func(k links.ItemLink) bool { return k.SellerItemID == l.Item.ItemID }) {
					// The first time, an own item of the same name is the one.
					var same []stock.Item
					for _, o := range items {
						if strings.EqualFold(strings.TrimSpace(o.Name), strings.TrimSpace(l.Item.Name)) {
							same = append(same, o)
						}
					}
					if len(same) == 1 {
						line.ItemID = same[0].ID
					}
				}
				in = append(in, line)
			}
			x, err := s.draftFromPricesTx(ctx, tx, c.p, d.link.ID, in, own, "", "wa-"+uuid.NewString())
			if errAs(err, &unlinked) {
				unlinkedFrom = d.link.Name
			}
			if err != nil {
				return err
			}
			made = append(made, x)
		}
		return nil
	})
	switch {
	case unlinkedFrom != "":
		return true, c.say(ctx, s, c.t("wa.prices.unlinked", "item", whatsapp.SafeText(unlinked.Item.Name), "name", whatsapp.SafeText(unlinkedFrom)))
	case errIsAny(err, links.ErrInvalid, links.ErrState):
		return true, c.say(ctx, s, c.t("wa.prices.refused", "reason", whatsapp.SafeText(linkReason(err))))
	case errIsAny(err, stock.ErrPurchase, stock.ErrNoSupplier):
		return true, s.purchaseChatFail(ctx, c, err)
	case err != nil:
		return true, err
	}
	if len(made) == 1 {
		x := made[0]
		return true, s.shopAsk(ctx, c, &chatShop{Action: "stock_order", StockChat: &stockChat{Purchase: &purchaseChat{Purchase: x.ID}}},
			c.t("wa.order.confirm", "reference", x.Reference, "items", whatsapp.SafeText(x.Words()), "supplier", whatsapp.SafeText(x.Supplier),
				"total", storefront.FormatNaira(x.TotalKobo)))
	}
	ids := make([]uuid.UUID, len(made))
	lines := make([]string, len(made))
	for i, x := range made {
		ids[i] = x.ID
		lines[i] = c.t("wa.mention.draft", "reference", x.Reference, "supplier", whatsapp.SafeText(x.Supplier), "items", whatsapp.SafeText(x.Words()),
			"total", storefront.FormatNaira(x.TotalKobo))
	}
	return true, s.shopAsk(ctx, c, &chatShop{Action: "stock_orders", StockChat: &stockChat{Purchase: &purchaseChat{Drafted: ids}}},
		c.t("wa.mention.confirm", "count", strconv.Itoa(len(made)), "orders", strings.Join(lines, "\n")))
}

// sendDrafted sends each of the orders one yes answered: those above the
// business's limit wait for a second person, and say so.
func (s *Server) sendDrafted(ctx context.Context, c *chat, ids []uuid.UUID) (string, error) {
	var sent, waiting []string
	for _, id := range ids {
		var x stock.Purchase
		err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
			var err error
			if x, err = s.Purchases.SendTx(ctx, tx, c.tenant.ID, id, c.p.Actor()); err != nil {
				return err
			}
			return waAudit(ctx, tx, c.ch.Name(), c.p, "purchase.send", x.Reference, map[string]any{"purchase_id": x.ID})
		})
		if errIsAny(err, stock.ErrSecondApproval) {
			_ = chatTx(ctx, s, c, func(tx pgx.Tx) error {
				x, err = stock.PurchaseTx(ctx, tx, c.tenant.ID, id, false)
				return err
			})
			waiting = append(waiting, x.Reference)
			continue
		}
		if err != nil {
			return "", err
		}
		sent = append(sent, c.t("wa.mention.sent_one", "reference", x.Reference, "supplier", whatsapp.SafeText(x.Supplier)))
	}
	if err := s.waDrainOutbox(ctx, c.tenant.ID); err != nil {
		s.Logger.Warn("stock: sending orders", "err", err)
	}
	msg := c.t("wa.mention.sent", "orders", strings.Join(sent, "\n"))
	if len(sent) == 0 {
		msg = ""
	}
	if len(waiting) > 0 {
		msg = strings.TrimSpace(msg + "\n" + c.t("wa.mention.second", "references", strings.Join(waiting, ", ")))
	}
	return msg, nil
}

func errAs(err error, target any) bool { return err != nil && errors.As(err, target) }

func errIsAny(err error, targets ...error) bool {
	for _, t := range targets {
		if errors.Is(err, t) {
			return true
		}
	}
	return false
}

// mentionRef is a mention as the composer sends it: a business by its
// link, or an item of the conversation's price list by its id.
type mentionRef struct {
	Kind      string    `json:"kind"`
	Link      uuid.UUID `json:"link"`
	ItemID    string    `json:"item_id"`
	ServiceID string    `json:"service_id"`
	// Ref is an invoice's or an order's id (from /link-chats/{link}/refs).
	Ref string `json:"ref"`
}

// conversationItems is what "#" names in a conversation with a link: the
// price list they share with us, else the one we share with them.
func conversationItems(ctx context.Context, tx pgx.Tx, tenant, link uuid.UUID) (string, []links.PriceItem, error) {
	got, err := links.SupplierPricesTx(ctx, tx, tenant, link)
	if err != nil {
		return "", nil, err
	}
	if len(got) > 0 {
		return "theirs", got[0].Items, nil
	}
	_, items, ok, err := links.LinkPricesTx(ctx, tx, tenant, link)
	if err != nil || !ok {
		return "", []links.PriceItem{}, err
	}
	return "ours", items, nil
}

// checkMentions makes a message's mentions from the composer's: each
// business one of ours, linked; each item on the conversation's price
// list, with its unit and price; each named in the text. A mention whose
// name the text does not hold is left out.
func checkMentions(ctx context.Context, tx pgx.Tx, p *Principal, link uuid.UUID, text string, refs []mentionRef) ([]links.Mention, error) {
	tenant := p.TenantID
	if len(refs) > 10 {
		return nil, fmt.Errorf("%w: up to 10 mentions in a message", links.ErrInvalid)
	}
	var items []links.PriceItem
	var services []storefront.Item
	var shared []chatRef
	lower := strings.ToLower(text)
	out := []links.Mention{}
	for _, r := range refs {
		var m links.Mention
		switch r.Kind {
		case "business":
			l, err := links.GetTx(ctx, tx, r.Link)
			if err != nil || l.State != links.Active {
				return nil, fmt.Errorf("%w: you can mention only businesses you are linked with", links.ErrInvalid)
			}
			m = links.Mention{Kind: "business", Name: l.Name, Link: l.ID}
		case "item":
			if items == nil {
				var err error
				if _, items, err = conversationItems(ctx, tx, tenant, link); err != nil {
					return nil, err
				}
			}
			i := slices.IndexFunc(items, func(it links.PriceItem) bool { return it.ItemID == r.ItemID })
			if i < 0 {
				return nil, fmt.Errorf("%w: that item is not on this conversation's price list", links.ErrInvalid)
			}
			it := items[i]
			m = links.Mention{Kind: "item", Name: it.Name, ItemID: it.ItemID, Unit: it.Unit, PriceKobo: it.PriceKobo}
		case "service":
			// One of our own services (chat tools), as our storefront offers it.
			if services == nil {
				var err error
				if services, err = storefront.Services(ctx, tx, tenant); err != nil {
					return nil, err
				}
				if services == nil {
					services = []storefront.Item{}
				}
			}
			i := slices.IndexFunc(services, func(it storefront.Item) bool { return it.ID == r.ServiceID })
			if i < 0 {
				return nil, fmt.Errorf("%w: that is not one of your services", links.ErrInvalid)
			}
			sv := services[i]
			m = links.Mention{Kind: "service", Name: sv.Name, ServiceID: sv.ID, Minutes: sv.DurationMinutes}
			if sv.PriceKobo != nil {
				m.PriceKobo = *sv.PriceKobo
			}
		case "invoice", "bill", "order", "purchase":
			// Between the two businesses only (linkRefsTx).
			if shared == nil {
				l, err := links.GetTx(ctx, tx, link)
				if err != nil {
					return nil, err
				}
				if shared, err = linkRefsTx(ctx, tx, p, l); err != nil {
					return nil, err
				}
			}
			i := slices.IndexFunc(shared, func(x chatRef) bool { return x.Kind == r.Kind && x.ID == r.Ref })
			if i < 0 {
				return nil, fmt.Errorf("%w: you can name only invoices and orders between you and this business", links.ErrInvalid)
			}
			x := shared[i]
			m = links.Mention{Kind: x.Kind, Name: x.Name, Ref: x.ID, PriceKobo: *x.TotalKobo, Status: x.Status}
		default:
			return nil, fmt.Errorf("%w: a mention is a business, an item, a service, an invoice, a bill or an order", links.ErrInvalid)
		}
		// A business is "@Name"; an item "#Name", or "@Name" as the chat
		// tools picker writes it (docs/spec/chat-tools.md §3).
		name := strings.ToLower(m.Name)
		if strings.Contains(lower, "@"+name) || (m.Kind != "business" && strings.Contains(lower, "#"+name)) {
			out = append(out, m)
		}
	}
	return out, nil
}

// linkChatItems is GET /v1/link-chats/{link}/items: what "#" names in the
// conversation, with each item's unit and price: the price list the other
// business shares with us (whose: theirs), else ours with them (ours).
func (s *Server) linkChatItems(w http.ResponseWriter, r *http.Request) {
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	out := struct {
		Whose string            `json:"whose,omitempty"`
		Items []links.PriceItem `json:"items"`
	}{Items: []links.PriceItem{}}
	if err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := links.GetTx(r.Context(), tx, link); err != nil {
			return err
		}
		var err error
		out.Whose, out.Items, err = conversationItems(r.Context(), tx, p.TenantID, link)
		if out.Items == nil {
			out.Items = []links.PriceItem{}
		}
		return err
	}); err != nil {
		s.fail(w, r, linkMessageErr(err))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// linkChatPhoto is GET /v1/link-chats/{link}/photo?item=&w=: the photo of
// one of the other business's items, as our copy of its storefront names
// it (business-chat-tools.md §2). The picture is in their storage, which
// the app's pages may not load: it comes through here, fetched with their
// connection from the address their own storefront wrote, never one the
// caller gives.
func (s *Server) linkChatPhoto(w http.ResponseWriter, r *http.Request) {
	link, err := linkParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if s.Media == nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	item := r.URL.Query().Get("item")
	width := r.URL.Query().Get("w")
	p := principalFrom(r.Context())
	var seller uuid.UUID
	var urls map[string]string
	if err := s.tx(r, func(tx pgx.Tx) error {
		l, err := links.GetTx(r.Context(), tx, link)
		if err != nil {
			return err
		}
		seller = l.Business
		var raw []byte
		if err := tx.QueryRow(r.Context(), `SELECT photo FROM supplier_prices WHERE tenant_id = $1 AND link_id = $2 AND item_id = $3`,
			p.TenantID, link, item).Scan(&raw); err != nil {
			return err
		}
		if len(raw) == 0 {
			return pgx.ErrNoRows
		}
		return json.Unmarshal(raw, &urls)
	}); err != nil {
		s.fail(w, r, linkMessageErr(err))
		return
	}
	src := urls[width]
	for _, k := range []string{"320", "800", "2000"} {
		if src == "" {
			src = urls[k]
		}
	}
	if src == "" {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	data, ctype, err := s.Media.FetchPublic(r.Context(), seller, src)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(data) //nolint:gosec // a JPEG or PNG by its bytes (FetchPublic), sent as that type with nosniff
}
