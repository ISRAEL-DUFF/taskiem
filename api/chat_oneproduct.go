package api

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/areas"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// A One product page from chat (docs/spec/forms-that-sell.md §7, L2), on
// WhatsApp, Telegram and the app's chat alike: "sell Ear cleaner on a
// page", or "add a page for Ear cleaner", with packages if wanted ("… 1
// for 25k, 2 for 45k, 3 for 65k"), shows the page it makes and asks for
// yes; then the front is made from the catalogue row, its order workflow
// published (or asked for, under the approval rule, as "turn on orders"
// is), its order form and the front turned on, and the page published
// again. The other languages' words reach it as "add a page for …"
// (shopCommandOf).

var (
	// "sell Ear cleaner on a page", "sell Ear cleaner on its own page 1
	// for 25000, 2 for 45000".
	sellOnPageCmd = regexp.MustCompile(`(?i)^\s*sell\s+(.+?)\s+on\s+(?:a|its\s+own|a\s+page\s+of\s+its\s+own|my)\s*page\b[\s:,.-]*(.*?)\s*[.!]?\s*$`)
	// "add a page for Ear cleaner", "make a page for Ear cleaner: 1 for 25k".
	pageForCmd = regexp.MustCompile(`(?i)^\s*(?:add|make)\s+a\s+page\s+for\s+(.+?)\s*[.!]?\s*$`)
	// One package: "2 for 45,000", "3 for ₦65k".
	packageWords = regexp.MustCompile(`(?i)\b(\d{1,2})\s*(?:for|@|at)\s*((?:₦|#|n)?\s*\d[\d,]*(?:\.\d+)?\s*[km]?)`)
	// Where the packages start in "Ear cleaner 1 for 25k".
	packagesStart = regexp.MustCompile(`(?i)[\s:,-]+\d{1,2}\s*(?:for|@|at)\s*(?:₦|#|n)?\s*\d`)
)

// sellOnPageArgs reads a sell-on-a-page command: the product's name and
// the packages typed after it (none typed is none; typed but unreadable
// is ok false).
func sellOnPageArgs(text string) (name string, pkgs []storefront.Package, matched, ok bool) {
	var rest string
	if m := sellOnPageCmd.FindStringSubmatch(text); m != nil {
		name, rest = m[1], m[2]
	} else if m := pageForCmd.FindStringSubmatch(text); m != nil {
		name = m[1]
		if i := packagesStart.FindStringIndex(name); i != nil {
			name, rest = name[:i[0]], name[i[0]:]
		}
	} else {
		return "", nil, false, false
	}
	name = strings.Trim(strings.TrimSpace(name), " *\"'")
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return name, nil, true, name != ""
	}
	for _, m := range packageWords.FindAllStringSubmatch(rest, -1) {
		qty, err := strconv.Atoi(m[1])
		kobo, okp := storefront.ParsePrice(m[2])
		if err != nil || !okp {
			return name, nil, true, false
		}
		pkgs = append(pkgs, storefront.Package{Qty: qty, PriceKobo: kobo})
	}
	return name, pkgs, true, name != "" && len(pkgs) > 0
}

// waSellOnPage answers "sell <product> on a page": the product found on
// the catalogue, the page it makes shown, and yes asked for.
func (s *Server) waSellOnPage(ctx context.Context, c *chat, text string) error {
	name, pkgs, _, ok := sellOnPageArgs(text)
	if !ok {
		return c.say(ctx, s, c.t("wa.page.how"))
	}
	if !c.p.Can(PermWorkflowEdit) || !c.p.Can(PermWorkflowPublish) {
		return c.say(ctx, s, c.t("wa.publish.denied", "org", c.tenant.Name))
	}
	if !s.chatAreas(ctx, c).On(areas.Orders) {
		return c.say(ctx, s, c.t("wa.page.orders_off"))
	}
	sf, ok, err := s.shopNeed(ctx, c)
	if !ok || err != nil {
		return err
	}
	var items []storefront.Item
	var fronts []frontView
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		if items, err = storefront.Catalogue(ctx, tx, c.tenant.ID); err != nil {
			return err
		}
		fronts, err = frontViews(ctx, tx, c.tenant.ID)
		return err
	}); err != nil {
		return err
	}
	var products []storefront.Item
	for _, it := range items {
		if it.VariantOf == "" {
			products = append(products, it)
		}
	}
	match := matchItems(products, name)
	switch {
	case len(match) == 0:
		return c.say(ctx, s, c.t("wa.page.no_item", "name", whatsapp.SafeText(name)))
	case len(match) > 1:
		names := make([]string, 0, shopPickMax)
		for _, it := range match[:min(shopPickMax, len(match))] {
			names = append(names, "*"+whatsapp.SafeText(it.Name)+"*")
		}
		return c.say(ctx, s, c.t("wa.page.which", "items", strings.Join(names, ", ")))
	}
	it := match[0]
	if it.PriceKobo == nil {
		return c.say(ctx, s, c.t("wa.page.no_price", "name", whatsapp.SafeText(it.Name)))
	}
	for _, f := range fronts {
		if f.Kind != storefront.OneProduct {
			continue
		}
		if f.Sells != nil && f.Sells.Item == it.ID {
			return c.say(ctx, s, c.t("wa.page.exists", "name", whatsapp.SafeText(it.Name), "link", s.frontLink(sf, f.Front)))
		}
	}
	if len(pkgs) == 0 {
		pkgs = []storefront.Package{{Qty: 1, PriceKobo: *it.PriceKobo}}
	}
	if _, err := storefront.PackagesForm("catalogue", it.ID, pkgs, "", nil); err != nil {
		return c.say(ctx, s, c.t("wa.page.bad_packages", "reason", whatsapp.SafeText(chatReason(err))))
	}
	labels := make([]string, len(pkgs))
	for i, p := range pkgs {
		labels[i] = p.Title()
	}
	text = c.t("wa.page.confirm", "name", whatsapp.SafeText(it.Name), "packages", strings.Join(labels, "; "),
		"link", strings.TrimRight(s.shopLink(sf), "/")+"/"+storefront.FrontSlug(it.Name))
	for _, w := range storefront.SellWarnings([]storefront.Item{it}, mustPackagesForm(it.ID, pkgs)) {
		text += "\n" + c.t("wa.page.warning", "warning", whatsapp.SafeText(w))
	}
	return s.shopAsk(ctx, c, &chatShop{Action: "sell_page", Row: it.ID, Name: it.Name, Packages: pkgs}, text)
}

// mustPackagesForm is the packages form of a row checked already.
func mustPackagesForm(item string, pkgs []storefront.Package) []byte {
	form, _ := storefront.PackagesForm("catalogue", item, pkgs, "", nil)
	return form
}

// shopSellOnPage makes the page confirmed: the front and its order form
// (made off), the order workflow published from its template with the
// web app's checks, or asked for under the approval rule, which leaves the
// page off until it is published; then the order form and the front on,
// and the page published again.
func (s *Server) shopSellOnPage(ctx context.Context, c *chat) error {
	sh := c.data.Shop
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	if !c.p.Can(PermWorkflowEdit) || !c.p.Can(PermWorkflowPublish) {
		return c.say(ctx, s, c.t("wa.publish.denied", "org", c.tenant.Name))
	}
	var made storefront.OneProductFront
	var wf uuid.UUID
	var held, paystack bool
	audit := func(tx pgx.Tx, action, target string, detail map[string]any) error {
		return waAudit(ctx, tx, c.ch.Name(), c.p, action, target, detail)
	}
	err := s.makeOneProduct(ctx, c.p, storefront.NewOneProduct{Item: sh.Row, Packages: sh.Packages}, audit, func(tx pgx.Tx, m storefront.OneProductFront) error {
		made = m
		var err error
		wf, held, err = s.setupActionTx(ctx, tx, c.p, *m.Action.ID, func(action, target string, detail map[string]any) error {
			return audit(tx, action, target, detail)
		})
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM connections WHERE environment = 'prod' AND connector = 'paystack' AND status = 'active')`).Scan(&paystack)
	})
	if errors.Is(err, storefront.ErrInvalid) || errors.Is(err, errBadRequest) || errors.Is(err, errConflict) || errors.Is(err, errForbidden) {
		return c.say(ctx, s, c.t("wa.page.not_made", "reason", whatsapp.SafeText(chatReason(err))))
	}
	if err != nil {
		return err
	}
	name := whatsapp.SafeText(made.Front.Name)
	if !held {
		state, needs, err := s.publishFromChat(ctx, c, wf, 1)
		if err != nil {
			return c.say(ctx, s, c.t("wa.publish.failed", "reason", whatsapp.SafeText(plainError(err))))
		}
		if state != publishedNow {
			// Asked for under the approval rule: the page stays off until
			// its workflow is published; "front … on" turns it on then.
			return c.say(ctx, s, c.t("wa.page.made_off", "name", name)+"\n"+s.publishReply(ctx, c, state, made.Front.Name, needs, wf))
		}
	}
	var sf storefront.Storefront
	var f storefront.Front
	err = chatTx(ctx, s, c, func(tx pgx.Tx) error {
		next := made.Action
		next.Enabled = true
		if !paystack {
			// Paid at the door, in cash or by transfer, as the page says.
			next.Payment = "none"
		}
		if _, err := storefront.SaveAction(ctx, tx, c.tenant.ID, next); err != nil {
			return err
		}
		on := true
		var err error
		if f, err = storefront.UpdateFront(ctx, tx, c.tenant.ID, made.Front.ID, storefront.FrontPatch{Enabled: &on}); err != nil {
			return err
		}
		if sf, err = storefront.Get(ctx, tx, c.tenant.ID); err != nil {
			return err
		}
		if sf.PublishedVersion > 0 {
			res, err := s.publishGated(ctx, tx, c.tenant.ID, c.p.Actor())
			if err != nil {
				return err
			}
			sf = res.Storefront
		}
		return waAudit(ctx, tx, c.ch.Name(), c.p, "storefront.front_save", f.Slug, map[string]any{"enabled": true, "action": made.Action.ID})
	})
	if reason, ok := publishRefusal(err); ok {
		return c.say(ctx, s, c.t("wa.shop.publish_failed", "reason", whatsapp.SafeText(reason)))
	}
	if errors.Is(err, storefront.ErrInvalid) {
		return c.say(ctx, s, c.t("wa.page.not_made", "reason", whatsapp.SafeText(chatReason(err))))
	}
	if err != nil {
		return err
	}
	if sf.PublishedVersion == 0 {
		return c.say(ctx, s, c.t("wa.page.made_unpublished", "name", name)+" "+c.t("wa.shop.publish_hint"))
	}
	msg := c.t("wa.page.made", "name", name, "link", s.frontLink(sf, f))
	if !paystack {
		msg += "\n" + c.t("wa.page.pay_at_door")
	}
	return c.say(ctx, s, msg)
}
