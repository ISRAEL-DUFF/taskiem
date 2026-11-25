package api

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/areas"
	"github.com/israel-duff/taskiem/engine/db"
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
	// for 25000, 2 for 45000", "sell Ear cleaner on a page of its own".
	sellOnPageCmd = regexp.MustCompile(`(?i)^\s*sell\s+(.+?)\s+on\s+(?:a\s+page\s+of\s+its\s+own|(?:a|its\s+own|my)\s+page)\b[\s:,.-]*(.*?)\s*[.!]?\s*$`)
	// "add a page for Ear cleaner", "make a page for Ear cleaner: 1 for 25k".
	pageForCmd = regexp.MustCompile(`(?i)^\s*(add|make)\s+a\s+page\s+for\s+(.+?)\s*[.!]?\s*$`)
	// Where packages start after a name: an explicit "N for PRICE" ("at"
	// after a number is a name's, as in "iPhone 13 at 500k").
	packagesStart = regexp.MustCompile(`(?i)(?:^|[\s:,-])\d+\s*for\s*(?:₦|#|ngn|n)?\s*\d`)
	// One package at the start of what is left: "2 for 45,000", "3 for
	// ₦65k", "2 for 45 000".
	packageHead = regexp.MustCompile(`(?i)^(\d+)\s*for\s*`)
	// A price at the start: thousands grouped by commas or spaces or not,
	// with k or m.
	priceGrouped = regexp.MustCompile(`(?i)^(?:₦|#|ngn|n)?\s*\d{1,3}(?:[ ,]\d{3})+(?:\.\d+)?(?:\s*[km])?\b`)
	pricePlain   = regexp.MustCompile(`(?i)^(?:₦|#|ngn|n)?\s*\d+(?:\.\d+)?(?:\s*[km])?\b`)
	// What may stand between packages.
	packageSep = regexp.MustCompile(`(?i)^(?:[\s,;/&+.:-]|\band\b)+`)
	// A word that starts another package after a price.
	packageNext = regexp.MustCompile(`(?i)^\s*for\b`)
)

// pageWords are a sell-on-a-page command as typed: what names the product
// (with the packages after it when the command does not mark where the
// name ends), the packages typed, and whether it was "make a page for".
type pageWords struct {
	subject, rest string
	split, make   bool
}

// readPageWords reads a sell-on-a-page command's words; false when text
// is not one.
func readPageWords(text string) (pageWords, bool) {
	if m := sellOnPageCmd.FindStringSubmatch(text); m != nil {
		return pageWords{subject: m[1], rest: m[2]}, true
	}
	if m := pageForCmd.FindStringSubmatch(text); m != nil {
		return pageWords{subject: m[2], split: true, make: strings.EqualFold(m[1], "make")}, true
	}
	return pageWords{}, false
}

// name is the product's name and the packages' words: after "on a page"
// as typed, else after the longest catalogue name the words start with,
// else at the first explicit "N for PRICE".
func (w pageWords) name(names []string) (string, string) {
	name, rest := w.subject, w.rest
	if w.split {
		best := ""
		for _, n := range names {
			n = strings.TrimSpace(n)
			if len(n) > len(best) && len(n) <= len(name) && strings.EqualFold(name[:len(n)], n) && wordEnds(name[len(n):]) {
				best = n
			}
		}
		switch {
		case best != "":
			name, rest = name[:len(best)], name[len(best):]
		default:
			if i := packagesStart.FindStringIndex(name); i != nil && i[0] > 0 {
				name, rest = name[:i[0]], name[i[0]:]
			}
		}
	}
	return strings.Trim(strings.TrimSpace(name), " *\"':,-"), strings.TrimSpace(rest)
}

// wordEnds says rest starts where a word ends: nothing, or not a letter
// or digit.
func wordEnds(rest string) bool {
	r, _ := utf8.DecodeRuneInString(rest)
	return rest == "" || !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

// readPackages reads packages typed after a product's name, every
// character of them: "1 for 25000, 2 for 45,000 and 3 for 65k". bad is
// the first bit that is not a package nor between two ("" when all
// were read). A quantity is read whatever it is; the packages' own check
// refuses one out of range.
func readPackages(rest string) (pkgs []storefront.Package, bad string) {
	for {
		if loc := packageSep.FindStringIndex(rest); loc != nil {
			rest = rest[loc[1]:]
		}
		if rest == "" {
			return pkgs, ""
		}
		head := packageHead.FindStringSubmatchIndex(rest)
		if head == nil {
			return nil, packagesBit(rest)
		}
		qty, err := strconv.Atoi(rest[head[2]:head[3]])
		after := rest[head[1]:]
		loc := priceGrouped.FindStringIndex(after)
		if loc == nil || packageNext.MatchString(after[loc[1]:]) {
			// "1 for 250 100 for 2m": 250, then the next package.
			loc = pricePlain.FindStringIndex(after)
		}
		if err != nil || loc == nil {
			return nil, packagesBit(rest)
		}
		kobo, ok := storefront.ParsePrice(after[:loc[1]])
		if !ok {
			return nil, packagesBit(rest)
		}
		pkgs = append(pkgs, storefront.Package{Qty: qty, PriceKobo: kobo})
		rest = after[loc[1]:]
		if rest != "" && !packageSep.MatchString(rest) {
			return nil, packagesBit(rest)
		}
	}
}

// packagesBit is the bit of rest to name as not understood: up to where
// the next package starts, at most 40 characters.
func packagesBit(rest string) string {
	if i := packagesStart.FindStringIndex(rest); i != nil && i[0] > 0 {
		rest = rest[:i[0]]
	}
	rest = strings.Trim(rest, " ,;:.-")
	if r := []rune(rest); len(r) > 40 {
		rest = string(r[:39]) + "…"
	}
	if rest == "" {
		rest = "?"
	}
	return rest
}

// sellOnPageArgs reads a sell-on-a-page command against the catalogue's
// names: the product's name and the packages typed after it (none typed
// is none). bad is the bit of the packages not understood; ok is false
// when text is not the command or names nothing.
func sellOnPageArgs(text string, names []string) (name string, pkgs []storefront.Package, bad string, ok bool) {
	w, matched := readPageWords(text)
	if !matched {
		return "", nil, "", false
	}
	name, rest := w.name(names)
	if name == "" {
		return "", nil, "", false
	}
	pkgs, bad = readPackages(rest)
	return name, pkgs, bad, true
}

// pageNamesProduct says "make a page for <words>" names a product of the
// catalogue, so it is a One product page and not the words of another
// command; no storefront or no catalogue is no.
func (s *Server) pageNamesProduct(ctx context.Context, c *chat, w pageWords) bool {
	var products []storefront.Item
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		products, err = pageProducts(ctx, tx, c.tenant.ID)
		return err
	}); err != nil {
		return false
	}
	name, _ := w.name(itemNames(products))
	return name != "" && len(matchItems(products, name)) > 0
}

// pageProducts are the catalogue's products (not their sizes and colours).
func pageProducts(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]storefront.Item, error) {
	items, err := storefront.Catalogue(ctx, tx, tenant)
	if err != nil {
		return nil, err
	}
	var products []storefront.Item
	for _, it := range items {
		if it.VariantOf == "" {
			products = append(products, it)
		}
	}
	return products, nil
}

// itemNames are the items' names.
func itemNames(items []storefront.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Name
	}
	return out
}

// waSellOnPage answers "sell <product> on a page": the product found on
// the catalogue, the page it makes shown, and yes asked for.
func (s *Server) waSellOnPage(ctx context.Context, c *chat, text string) error {
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
	var products []storefront.Item
	var fronts []frontView
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		if products, err = pageProducts(ctx, tx, c.tenant.ID); err != nil {
			return err
		}
		fronts, err = frontViews(ctx, tx, c.tenant.ID)
		return err
	}); err != nil {
		return err
	}
	name, pkgs, bad, ok := sellOnPageArgs(text, itemNames(products))
	switch {
	case !ok:
		return c.say(ctx, s, c.t("wa.page.how"))
	case bad != "":
		return c.say(ctx, s, c.t("wa.page.not_understood", "bit", whatsapp.SafeText(bad)))
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
	// The address CreateFront will give it, as it would now.
	var slug string
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		slug, err = storefront.FreeFrontSlug(ctx, tx, c.tenant.ID, it.Name)
		return err
	}); err != nil {
		return err
	}
	text = c.t("wa.page.confirm", "name", whatsapp.SafeText(it.Name), "packages", strings.Join(labels, "; "),
		"link", strings.TrimRight(s.shopLink(sf), "/")+"/"+slug)
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
	// The question's nonce is the yes's idempotency key: the session that
	// asked it is let go of only by the yes that still finds it, so two
	// yeses at once (a webhook sent twice) make one page.
	claimed, err := s.claimShopAsk(ctx, c, sh.Nonce)
	if err != nil || !claimed {
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
	err = s.makeOneProduct(ctx, c.p, storefront.NewOneProduct{Item: sh.Row, Packages: sh.Packages}, audit, func(tx pgx.Tx, m storefront.OneProductFront) error {
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

// claimShopAsk lets go of the conversation's question once, for the yes
// that answers it: the session is removed only while it still holds the
// question of nonce, so of two yeses at once one claims it and the other
// does nothing (false). A question with no nonce is claimed as any is.
func (s *Server) claimShopAsk(ctx context.Context, c *chat, nonce string) (bool, error) {
	if nonce == "" {
		return true, s.saveSession(ctx, c, stateIdle, chatData{}, 0)
	}
	c.state, c.data = stateIdle, chatData{}
	var n int64
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM chat_sessions WHERE tenant_id = $1 AND number = $2 AND data->'shop'->>'nonce' = $3`, c.tenant.ID, c.addr, nonce)
		n = tag.RowsAffected()
		return err
	})
	return n == 1, err
}
