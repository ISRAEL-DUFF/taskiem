package api

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/templates"
)

// Fronts from chat (docs/spec/fronts.md §7.2, F4), on WhatsApp and
// Telegram alike: "fronts" lists them; "add front Snacks" asks the kind as
// a numbered list, and "add food front Snacks" names it; "front Snacks on"
// and "off" switch one, publishing the page again with its checks. Every
// change is shown and confirmed with yes, as the storefront's other chat
// commands are, and goes through the code the web app's Fronts tab uses.

var (
	// "add front Snacks", "add food front Snacks".
	addFrontCmd = regexp.MustCompile(`(?i)^\s*add\s+(?:(\S+)\s+)?front\s+(.+?)\s*$`)
	// "add dish Jollof rice 2500", "add item Chin chin 500 to Snacks".
	addWordsCmd = regexp.MustCompile(`(?i)^\s*add\s+(dish|item)\s+(.+)$`)
	// "sold out Jollof rice": hidden until "show".
	soldOutCmd = regexp.MustCompile(`(?i)^\s*sold\s*out\s+(.+)$`)
	// "front Snacks on", "front Snacks off".
	frontSwitchCmd = regexp.MustCompile(`(?i)^\s*front\s+(.+?)\s+(on|off)\s*[.!]?\s*$`)
)

// frontKindWords are the words a kind is named by in a command.
var frontKindWords = map[string]string{
	"shop": "shop", "food": "food", "kitchen": "food", "hire": "hire", "rental": "hire", "questions": "questions", "question": "questions",
	"call-backs": "callbacks", "call-back": "callbacks", "callbacks": "callbacks", "callback": "callbacks", "custom": "custom",
}

// waFrontCommand acts on a fronts command; false when cmd is not one.
func (s *Server) waFrontCommand(ctx context.Context, c *chat, text, cmd string) (bool, error) {
	var do func() error
	switch {
	case cmd == "fronts" || cmd == "my fronts":
		do = func() error { return s.waFronts(ctx, c) }
	case addFrontCmd.MatchString(text):
		m := addFrontCmd.FindStringSubmatch(text)
		kind := ""
		if m[1] != "" {
			k, ok := frontKindWords[strings.ToLower(m[1])]
			if !ok {
				// "add product Front door mat 3000" is a product.
				return false, nil
			}
			kind = k
		}
		name := strings.Trim(m[2], " .!?")
		do = func() error { return s.waAddFront(ctx, c, name, kind) }
	case addWordsCmd.MatchString(text):
		m := addWordsCmd.FindStringSubmatch(text)
		do = func() error { return s.waFrontDish(ctx, c, strings.Trim(m[2], " ?!"), strings.EqualFold(m[1], "dish")) }
	case soldOutCmd.MatchString(text):
		do = func() error {
			return s.waShopChange(ctx, c, "hide", strings.Trim(soldOutCmd.FindStringSubmatch(text)[1], " ?!."))
		}
	case turnOnForCmd.MatchString(text):
		m := turnOnForCmd.FindStringSubmatch(text)
		do = func() error { return s.waFrontTurnOn(ctx, c, m[1], m[2]) }
	case s.Orders != nil && (strings.HasPrefix(cmd, "orders ") || strings.HasPrefix(cmd, "my orders ")):
		do = func() error { return s.waShopOrdersOn(ctx, c, ordersOnCmd.FindStringSubmatch(text)[1]) }
	case frontSwitchCmd.MatchString(text):
		m := frontSwitchCmd.FindStringSubmatch(text)
		do = func() error { return s.waFrontSwitch(ctx, c, strings.TrimSpace(m[1]), strings.EqualFold(m[2], "on")) }
	default:
		return false, nil
	}
	if !c.p.Can(PermStorefrontManage) {
		return true, c.say(ctx, s, c.t("wa.shop.denied", "org", c.tenant.Name))
	}
	return true, do()
}

// chatFronts are the storefront's fronts as the Fronts tab shows them, or
// says there is no page yet (ok false).
func (s *Server) chatFronts(ctx context.Context, c *chat) (storefront.Storefront, []frontView, bool, error) {
	sf, ok, err := s.shopNeed(ctx, c)
	if !ok || err != nil {
		return sf, nil, false, err
	}
	var list []frontView
	err = chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		list, err = frontViews(ctx, tx, c.tenant.ID)
		return err
	})
	return sf, list, err == nil, err
}

// findFront finds a front by its name or address: the exact one, else the
// only one the words start.
func findFront(list []frontView, name string) (frontView, bool) {
	key := matchKey(name)
	if key == "" {
		return frontView{}, false
	}
	var prefix []frontView
	for _, f := range list {
		if matchKey(f.Name) == key || f.Slug != "" && f.Slug == strings.ToLower(strings.TrimSpace(name)) {
			return f, true
		}
		if strings.HasPrefix(matchKey(f.Name), key) {
			prefix = append(prefix, f)
		}
	}
	if len(prefix) == 1 {
		return prefix[0], true
	}
	return frontView{}, false
}

// frontNames lists the fronts' names, for a reply that found none.
func frontNames(list []frontView) string {
	names := make([]string, len(list))
	for i, f := range list {
		names[i] = "*" + whatsapp.SafeText(f.Name) + "*"
	}
	return strings.Join(names, ", ")
}

// noFront says no front has that name, naming the ones there are.
func (s *Server) noFront(ctx context.Context, c *chat, list []frontView, name string) error {
	return c.say(ctx, s, c.t("wa.front.none_named", "name", whatsapp.SafeText(name), "fronts", frontNames(list)))
}

func kindTitle(kind string) string {
	if k, ok := storefront.FrontKindOf(kind); ok {
		return k.Title
	}
	return kind
}

// frontLink is a front's address on the page.
func (s *Server) frontLink(sf storefront.Storefront, f storefront.Front) string {
	base := strings.TrimRight(s.shopLink(sf), "/")
	if f.Slug == "" {
		return base
	}
	return base + "/" + f.Slug
}

// waFronts answers "fronts": each front with its kind, whether it is on,
// its address and what it has.
func (s *Server) waFronts(ctx context.Context, c *chat) error {
	sf, list, ok, err := s.chatFronts(ctx, c)
	if !ok || err != nil {
		return err
	}
	lines := []string{c.t("wa.front.list_head")}
	for _, f := range list {
		state := c.t("wa.front.off")
		if f.Enabled {
			state = c.t("wa.front.on")
		}
		where := s.frontLink(sf, f.Front)
		if f.Slug == "" {
			where = c.t("wa.front.home") + " " + where
		}
		forms := 0
		for _, a := range f.Actions {
			if a.Enabled {
				forms++
			}
		}
		parts := []string{"*" + whatsapp.SafeText(f.Name) + "*", kindTitle(f.Kind), state, where}
		if f.Table != nil {
			parts = append(parts, c.t("wa.front.items", "n", strconv.Itoa(f.Items)))
		}
		parts = append(parts, c.t("wa.front.forms_on", "n", strconv.Itoa(forms)))
		lines = append(lines, "• "+strings.Join(parts, " · "))
	}
	return c.say(ctx, s, strings.Join(lines, "\n")+"\n\n"+c.t("wa.front.list_tail"))
}

// waAddFront answers "add front <name>": the kind asked as a numbered
// list, or the front shown with what it makes and confirmed.
func (s *Server) waAddFront(ctx context.Context, c *chat, name, kind string) error {
	if r := []rune(name); len(r) == 0 || len(r) > 80 {
		return c.say(ctx, s, c.t("wa.front.how_add"))
	}
	_, list, ok, err := s.chatFronts(ctx, c)
	if !ok || err != nil {
		return err
	}
	for _, f := range list {
		if matchKey(f.Name) == matchKey(name) {
			return c.say(ctx, s, c.t("wa.front.exists", "name", whatsapp.SafeText(f.Name)))
		}
	}
	if kind == "" {
		sh := &chatShop{Action: "front_kind", Name: name}
		if err := s.saveSession(ctx, c, stateCollecting, chatData{Shop: sh}, waPendingTTL); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.front.which_kind", "name", whatsapp.SafeText(name), "list", kindChoices()))
	}
	return s.frontAskAdd(ctx, c, name, kind)
}

// kindChoices are the kinds as a numbered list, each with its line.
func kindChoices() string {
	var lines []string
	for i, k := range storefront.FrontKinds() {
		lines = append(lines, strconv.Itoa(i+1)+". *"+k.Title+"*: "+k.Line)
	}
	return strings.Join(lines, "\n")
}

// waFrontKindPick takes the kind by its number or its word.
func (s *Server) waFrontKindPick(ctx context.Context, c *chat, text string) error {
	sh := c.data.Shop
	kinds := storefront.FrontKinds()
	kind := ""
	if n, err := strconv.Atoi(strings.TrimSpace(text)); err == nil && n >= 1 && n <= len(kinds) {
		kind = kinds[n-1].Name
	} else if k, ok := frontKindWords[strings.ToLower(strings.Trim(text, " .!"))]; ok {
		kind = k
	}
	if kind == "" {
		return c.say(ctx, s, c.t("wa.front.which_kind", "name", whatsapp.SafeText(sh.Name), "list", kindChoices()))
	}
	return s.frontAskAdd(ctx, c, sh.Name, kind)
}

// frontAskAdd shows the front a kind makes, and asks for yes.
func (s *Server) frontAskAdd(ctx context.Context, c *chat, name, kind string) error {
	k, ok := storefront.FrontKindOf(kind)
	if !ok {
		return c.say(ctx, s, c.t("wa.front.how_add"))
	}
	if k.Table != "" && !c.p.Can(PermTableEdit) {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.shop.denied_table", "org", c.tenant.Name))
	}
	text := c.t("wa.front.confirm_add", "name", whatsapp.SafeText(name), "kind", k.Title)
	if kt, ok := templates.KnownTable(k.Table); ok {
		text += " " + c.t("wa.front.confirm_table", "table", kt.Title)
	}
	if len(k.Actions) > 0 {
		labels := make([]string, len(k.Actions))
		for i, a := range k.Actions {
			labels[i] = "*" + a.Label + "*"
		}
		text += " " + c.t("wa.front.confirm_forms", "forms", strings.Join(labels, ", "))
	}
	text += " " + c.t("wa.front.confirm_off")
	return s.shopAsk(ctx, c, &chatShop{Action: "front_add", Name: name, Kind: kind}, text)
}

// shopAddFront makes the front confirmed, and says what to add first.
func (s *Server) shopAddFront(ctx context.Context, c *chat) error {
	sh := c.data.Shop
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	k, ok := storefront.FrontKindOf(sh.Kind)
	if !ok {
		return c.say(ctx, s, c.t("wa.front.how_add"))
	}
	var f storefront.Front
	err := s.makeFront(ctx, c.p, sh.Name, k, func(tx pgx.Tx, action, target string, detail map[string]any) error {
		return waAudit(ctx, tx, c.ch.Name(), c.p, action, target, detail)
	}, func(_ pgx.Tx, made storefront.Front) error {
		f = made
		return nil
	})
	if errors.Is(err, errForbidden) || errors.Is(err, errConflict) || errors.Is(err, errBadRequest) || errors.Is(err, storefront.ErrInvalid) {
		return c.say(ctx, s, c.t("wa.front.not_made", "reason", whatsapp.SafeText(chatReason(err))))
	}
	if err != nil {
		return err
	}
	msg := c.t("wa.front.made", "name", whatsapp.SafeText(f.Name), "kind", k.Title)
	switch {
	case k.Name == "food":
		msg += "\n" + c.t("wa.front.next_dish", "front", whatsapp.SafeText(f.Name))
	case k.Table != "":
		msg += "\n" + c.t("wa.front.next_item", "front", whatsapp.SafeText(f.Name))
	}
	if len(k.Actions) > 0 {
		msg += "\n" + c.t("wa.front.next_forms", "front", whatsapp.SafeText(f.Name), "what", turnOnWord(k.Actions[0].Kind))
	}
	msg += "\n" + c.t("wa.front.next_on", "front", whatsapp.SafeText(f.Name))
	return c.say(ctx, s, msg)
}

// chatReason is an error's reason in plain words, without its class.
func chatReason(err error) string {
	r := plainError(err)
	for _, p := range []string{"conflict: ", "forbidden: ", "bad request: ", "invalid: "} {
		r = strings.TrimPrefix(r, p)
	}
	return r
}

// waFrontSwitch answers "front <name> on" and "off": shown, then done on
// yes.
func (s *Server) waFrontSwitch(ctx context.Context, c *chat, name string, on bool) error {
	_, list, ok, err := s.chatFronts(ctx, c)
	if !ok || err != nil {
		return err
	}
	f, ok := findFront(list, name)
	if !ok {
		return s.noFront(ctx, c, list, name)
	}
	if f.Slug == "" {
		return c.say(ctx, s, c.t("wa.front.home_always_on", "name", whatsapp.SafeText(f.Name)))
	}
	if f.Enabled == on {
		key := "wa.front.already_off"
		if on {
			key = "wa.front.already_on"
		}
		return c.say(ctx, s, c.t(key, "name", whatsapp.SafeText(f.Name)))
	}
	sh := &chatShop{Action: "front_off", Front: f.ID.String(), FrontName: f.Name}
	text := c.t("wa.front.confirm_off_now", "name", whatsapp.SafeText(f.Name))
	if on {
		sh.Action = "front_on"
		text = c.t("wa.front.confirm_on", "name", whatsapp.SafeText(f.Name))
	}
	return s.shopAsk(ctx, c, sh, text)
}

// shopSwitchFront turns the front confirmed on or off, as the Fronts tab
// does: on a live page the page is published again, with its checks.
func (s *Server) shopSwitchFront(ctx context.Context, c *chat) error {
	sh := c.data.Shop
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	id, err := uuid.Parse(sh.Front)
	if err != nil {
		return err
	}
	on := sh.Action == "front_on"
	var sf storefront.Storefront
	var f storefront.Front
	var formsOn bool
	var firstForm string
	err = chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		if f, err = storefront.UpdateFront(ctx, tx, c.tenant.ID, id, storefront.FrontPatch{Enabled: &on}); err != nil {
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
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM storefront_actions WHERE front_id = $1 AND enabled),
			COALESCE((SELECT kind FROM storefront_actions WHERE front_id = $1 ORDER BY position LIMIT 1), '')`, id).Scan(&formsOn, &firstForm); err != nil {
			return err
		}
		return waAudit(ctx, tx, c.ch.Name(), c.p, "storefront.front_save", f.Slug, map[string]any{"enabled": f.Enabled})
	})
	if reason, ok := publishRefusal(err); ok {
		return c.say(ctx, s, c.t("wa.shop.publish_failed", "reason", whatsapp.SafeText(reason)))
	}
	if errors.Is(err, storefront.ErrNotFound) {
		return c.say(ctx, s, c.t("wa.front.gone", "name", whatsapp.SafeText(sh.FrontName)))
	}
	if errors.Is(err, storefront.ErrInvalid) {
		return c.say(ctx, s, c.t("wa.front.not_changed", "reason", whatsapp.SafeText(chatReason(err))))
	}
	if err != nil {
		return err
	}
	name := whatsapp.SafeText(f.Name)
	if !on {
		return c.say(ctx, s, c.t("wa.front.now_off", "name", name))
	}
	msg := c.t("wa.front.now_on", "name", name)
	if sf.PublishedVersion > 0 {
		msg += " " + s.frontLink(sf, f)
	} else {
		msg += " " + c.t("wa.shop.publish_hint")
	}
	if !formsOn && firstForm != "" {
		msg += "\n" + c.t("wa.front.forms_off", "front", name, "what", turnOnWord(firstForm))
	}
	return c.say(ctx, s, msg)
}

// turnOnWord is the word "turn on … for <front>" takes for an action's
// kind.
func turnOnWord(kind string) string {
	switch kind {
	case "enquiry":
		return "questions"
	case "callback":
		return "call-backs"
	case "booking":
		return "bookings"
	case "hire":
		return "hire"
	}
	return "orders"
}

// --- items on a front's own list (F4b) ---

// frontList is a front's first list: its table and columns.
func frontList(f frontView) (storefront.ListConfig, bool) {
	for _, b := range f.Blocks {
		var lc storefront.ListConfig
		if b.Kind == "list" && json.Unmarshal(b.Config, &lc) == nil && lc.Table != nil {
			return lc, true
		}
	}
	return storefront.ListConfig{}, false
}

// frontSuffix reads "… to <front>" off an item command: the words before
// it and the front, when the words after the last "to" name one.
func frontSuffix(args string, list []frontView) (string, frontView, bool) {
	low := strings.ToLower(args)
	i := strings.LastIndex(low, " to ")
	if i < 0 {
		return args, frontView{}, false
	}
	f, ok := findFront(list, args[i+4:])
	if !ok {
		return args, frontView{}, false
	}
	return strings.TrimSpace(args[:i]), f, true
}

// waFrontProduct sends "add product … to <front>", or a photo with that
// caption, to the front's own list; false when no front is named, and the
// catalogue takes it as before.
func (s *Server) waFrontProduct(ctx context.Context, c *chat, args string, photo *whatsapp.Media) (bool, error) {
	if !strings.Contains(strings.ToLower(args), " to ") {
		return false, nil
	}
	if _, err := s.shopOf(ctx, c); err != nil {
		return false, nil //nolint:nilerr // the catalogue's path says why
	}
	var list []frontView
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		list, err = frontViews(ctx, tx, c.tenant.ID)
		return err
	}); err != nil {
		return true, err
	}
	rest, f, ok := frontSuffix(args, list)
	if !ok || f.Slug == "" {
		return false, nil
	}
	return true, s.frontAskItem(ctx, c, f, rest, photo)
}

// waFrontDish answers "add dish …" and "add item …": to the front named
// with "to <front>", or, for a dish, the one Food front there is.
func (s *Server) waFrontDish(ctx context.Context, c *chat, args string, dish bool) error {
	if !c.p.Can(PermTableEdit) {
		return c.say(ctx, s, c.t("wa.shop.denied_table", "org", c.tenant.Name))
	}
	_, list, ok, err := s.chatFronts(ctx, c)
	if !ok || err != nil {
		return err
	}
	rest, f, named := frontSuffix(args, list)
	if !named {
		var fit []frontView
		for _, x := range list {
			if _, has := frontList(x); has && x.Slug != "" && (!dish || x.Kind == "food") {
				fit = append(fit, x)
			}
		}
		switch {
		case len(fit) == 1 && dish:
			f = fit[0]
		case dish && len(fit) == 0:
			return c.say(ctx, s, c.t("wa.front.no_food"))
		case len(fit) == 0:
			return c.say(ctx, s, c.t("wa.front.no_item_fronts"))
		default:
			return c.say(ctx, s, c.t("wa.front.say_which", "fronts", frontNames(fit)))
		}
	}
	if f.Slug == "" {
		return s.waShopProduct(ctx, c, rest, nil)
	}
	return s.frontAskItem(ctx, c, f, rest, nil)
}

// frontAskItem shows an item to add to a front's list, and asks for yes.
func (s *Server) frontAskItem(ctx context.Context, c *chat, f frontView, args string, photo *whatsapp.Media) error {
	if !c.p.Can(PermTableEdit) {
		return c.say(ctx, s, c.t("wa.shop.denied_table", "org", c.tenant.Name))
	}
	lc, ok := frontList(f)
	if !ok {
		return c.say(ctx, s, c.t("wa.front.no_list", "front", whatsapp.SafeText(f.Name)))
	}
	name, kobo, ok := productArgs(args)
	if !ok {
		if photo != nil {
			return c.say(ctx, s, c.t("wa.shop.photo_caption"))
		}
		return c.say(ctx, s, c.t("wa.front.how_item", "front", whatsapp.SafeText(f.Name)))
	}
	m := lc.Columns
	if ok, err := s.shopColumn(ctx, c, m.Price, "price"); !ok || err != nil {
		return err
	}
	if photo != nil {
		if ok, err := s.shopColumn(ctx, c, m.Photo, "photo"); !ok || err != nil {
			return err
		}
	}
	if err := s.SyncStorefront(ctx, c.tenant.ID); err != nil {
		s.Logger.Warn("chat: refreshing the catalogue", "tenant", c.tenant.ID, "err", err)
	}
	var items []storefront.Item
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		items, err = storefront.ListItems(ctx, tx, c.tenant.ID, lc.TableName)
		return err
	}); err != nil {
		return err
	}
	for _, it := range items {
		if matchKey(it.Name) == matchKey(name) {
			return c.say(ctx, s, c.t("wa.front.item_exists", "name", whatsapp.SafeText(it.Name), "front", whatsapp.SafeText(f.Name), "price", priceText(c, it.PriceKobo)))
		}
	}
	sh := &chatShop{Action: "front_item", Name: name, PriceKobo: &kobo, Front: f.ID.String(), FrontName: f.Name, Table: lc.Table.String(), Columns: &m}
	withPhoto := ""
	if photo != nil {
		id, ok, err := s.shopStorePhoto(ctx, c, photo)
		if !ok || err != nil {
			return err
		}
		sh.Photo, withPhoto = id, c.t("wa.shop.with_photo")
	}
	return s.shopAsk(ctx, c, sh, c.t("wa.front.confirm_item", "name", whatsapp.SafeText(name), "price", storefront.FormatNaira(kobo),
		"front", whatsapp.SafeText(f.Name), "photo", withPhoto))
}

// shopAddFrontItem adds the item confirmed to its front's table.
func (s *Server) shopAddFrontItem(ctx context.Context, c *chat) error {
	sh := c.data.Shop
	t, ok, err := s.frontTable(ctx, c, sh)
	if !ok || err != nil {
		return err
	}
	m := *sh.Columns
	row := map[string]any{m.Name: sh.Name}
	if m.Price != "" && sh.PriceKobo != nil {
		row[m.Price] = priceCell(*sh.PriceKobo, m)
	}
	if m.Photo != "" && sh.Photo != "" {
		row[m.Photo] = sh.Photo
	}
	if m.Available != "" {
		row[m.Available] = true
	}
	id, ok, err := s.shopWrite(ctx, c, t, "", row)
	if !ok || err != nil {
		return err
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		return waAudit(ctx, tx, c.ch.Name(), c.p, "storefront.item_add", t.Name, map[string]any{"table_id": t.ID, "row_id": id, "front": sh.Front, "photo": sh.Photo != ""})
	}); err != nil {
		return err
	}
	if err := s.SyncStorefront(ctx, c.tenant.ID); err != nil {
		s.Logger.Warn("chat: refreshing the catalogue", "tenant", c.tenant.ID, "err", err)
	}
	front := whatsapp.SafeText(sh.FrontName)
	msg := c.t("wa.front.item_added", "name", whatsapp.SafeText(sh.Name), "price", priceText(c, sh.PriceKobo), "front", front)
	msg += " " + s.frontNote(ctx, c, sh)
	return c.say(ctx, s, msg)
}

// frontNote says where a change to a front's items shows: on the page
// now, once the front is on, or once the page is published.
func (s *Server) frontNote(ctx context.Context, c *chat, sh *chatShop) string {
	var sf storefront.Storefront
	var on bool
	_ = chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		if sf, err = storefront.Get(ctx, tx, c.tenant.ID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT enabled FROM storefront_fronts WHERE id = $1`, sh.Front).Scan(&on)
	})
	front := whatsapp.SafeText(sh.FrontName)
	switch {
	case !on:
		return c.t("wa.front.turn_on_hint", "front", front)
	case sf.PublishedVersion == 0:
		return c.t("wa.shop.publish_hint")
	}
	return c.t("wa.shop.on_page")
}

// frontTable reads the table of the item on a front's list being changed
// (ok false when it is gone, and the person has been told).
func (s *Server) frontTable(ctx context.Context, c *chat, sh *chatShop) (datatables.Table, bool, error) {
	id, err := uuid.Parse(sh.Table)
	if err != nil || sh.Columns == nil {
		return datatables.Table{}, false, s.saveSession(ctx, c, stateIdle, chatData{}, 0)
	}
	t, err := s.waGetTable(ctx, c.tenant.ID, id)
	if errors.Is(err, datatables.ErrNotFound) {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return t, false, err
		}
		return t, false, c.say(ctx, s, c.t("wa.front.no_list", "front", whatsapp.SafeText(sh.FrontName)))
	}
	return t, err == nil, err
}

// listedItem is an item on the catalogue (Table empty) or on a front's
// own list.
type listedItem struct {
	storefront.Item
	Table   string
	Front   string
	Columns storefront.Columns
}

// frontItems are the items on the fronts' own lists, apart from the
// catalogue's, each table once: none when there are no other fronts.
func (s *Server) frontItems(ctx context.Context, c *chat) ([]listedItem, error) {
	var out []listedItem
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		list, err := frontViews(ctx, tx, c.tenant.ID)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, f := range list {
			if lc, ok := frontList(f); ok && f.Slug == "" {
				seen[lc.TableName] = true
			}
		}
		for _, f := range list {
			lc, ok := frontList(f)
			if !ok || f.Slug == "" || seen[lc.TableName] {
				continue
			}
			seen[lc.TableName] = true
			items, err := storefront.ListItems(ctx, tx, c.tenant.ID, lc.TableName)
			if err != nil {
				return err
			}
			for _, it := range items {
				out = append(out, listedItem{Item: it, Table: lc.Table.String(), Front: f.Name, Columns: lc.Columns})
			}
		}
		return nil
	})
	return out, err
}

// waFrontChange is "price", "hide", "show", "stock" and "sold out" over
// the catalogue and the fronts' own lists, once there are other fronts
// with items; false when there are none, and the catalogue's path answers
// as before.
func (s *Server) waFrontChange(ctx context.Context, c *chat, sh *chatShop, name string) (bool, error) {
	others, err := s.frontItems(ctx, c)
	if err != nil || len(others) == 0 {
		return false, err
	}
	if err := s.SyncStorefront(ctx, c.tenant.ID); err != nil {
		s.Logger.Warn("chat: refreshing the catalogue", "tenant", c.tenant.ID, "err", err)
	}
	if others, err = s.frontItems(ctx, c); err != nil {
		return true, err
	}
	sf, err := s.shopOf(ctx, c)
	if err != nil {
		return true, err
	}
	var all []listedItem
	if sf.CatalogueTable != nil {
		var cat []storefront.Item
		if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
			var err error
			cat, err = storefront.Catalogue(ctx, tx, c.tenant.ID)
			return err
		}); err != nil {
			return true, err
		}
		for _, it := range cat {
			all = append(all, listedItem{Item: it, Columns: sf.CatalogueColumns})
		}
	}
	all = append(all, others...)
	match := matchListed(all, name)
	switch {
	case len(match) == 0:
		return true, c.say(ctx, s, c.t("wa.shop.no_item", "name", whatsapp.SafeText(name)))
	case len(match) > 1:
		apart := false
		for _, it := range match {
			apart = apart || it.Table != match[0].Table
		}
		for _, it := range match[:min(shopPickMax, len(match))] {
			label := it.Name
			if apart {
				front := it.Front
				if it.Table == "" {
					front = c.t("wa.front.catalogue")
				}
				label += " (" + front + ")"
			}
			sh.Pick = append(sh.Pick, shopChoice{Row: it.ID, Name: label, Table: it.Table})
		}
		if err := s.saveSession(ctx, c, stateCollecting, chatData{Shop: sh}, waPendingTTL); err != nil {
			return true, err
		}
		return true, c.say(ctx, s, c.t("wa.shop.which_item", "list", numberedChoices(sh.Pick)))
	}
	return true, s.frontAskChange(ctx, c, sh, match[0])
}

// matchListed is matchItems over listed items.
func matchListed(items []listedItem, name string) []listedItem {
	plain := make([]storefront.Item, len(items))
	for i, it := range items {
		plain[i] = it.Item
		plain[i].ID = strconv.Itoa(i)
	}
	var out []listedItem
	for _, it := range matchItems(plain, name) {
		n, _ := strconv.Atoi(it.ID)
		out = append(out, items[n])
	}
	return out
}

// frontAskChange asks for a change to an item, on the catalogue or on a
// front's own list.
func (s *Server) frontAskChange(ctx context.Context, c *chat, sh *chatShop, it listedItem) error {
	if it.Table == "" {
		col := map[string]string{"price": it.Columns.Price, "hide": it.Columns.Available, "show": it.Columns.Available, "stock": it.Columns.Stock}[sh.Action]
		what := map[string]string{"price": "price", "hide": "available", "show": "available", "stock": "stock"}[sh.Action]
		if ok, err := s.shopColumn(ctx, c, col, what); !ok || err != nil {
			return err
		}
		return s.shopAskChange(ctx, c, sh, it.Item)
	}
	if sh.Action == "stock" {
		return c.say(ctx, s, c.t("wa.front.stock_catalogue_only", "name", whatsapp.SafeText(it.Name), "front", whatsapp.SafeText(it.Front)))
	}
	col := map[string]string{"price": it.Columns.Price, "hide": it.Columns.Available, "show": it.Columns.Available}[sh.Action]
	what := map[string]string{"price": "price", "hide": "available", "show": "available"}[sh.Action]
	if ok, err := s.shopColumn(ctx, c, col, what); !ok || err != nil {
		return err
	}
	cols := it.Columns
	sh.Table, sh.Columns, sh.FrontName = it.Table, &cols, it.Front
	var f string
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT f.id::text FROM storefront_blocks b JOIN storefront_fronts f ON f.id = b.front_id
			WHERE b.kind = 'list' AND b.config->>'table' = $1 ORDER BY f.position LIMIT 1`, it.Table).Scan(&f)
	}); err != nil {
		return err
	}
	sh.Front = f
	return s.shopAskChange(ctx, c, sh, it.Item)
}

// waFrontPick takes the number of an item offered across the lists.
func (s *Server) waFrontPick(ctx context.Context, c *chat, text string) error {
	sh := c.data.Shop
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || n < 1 || n > len(sh.Pick) {
		return c.say(ctx, s, c.t("wa.shop.pick_again", "list", numberedChoices(sh.Pick)))
	}
	chosen := sh.Pick[n-1]
	if chosen.Table == "" {
		sh.Pick = []shopChoice{chosen}
		return s.waShopPick(ctx, c, "1")
	}
	others, err := s.frontItems(ctx, c)
	if err != nil {
		return err
	}
	for _, it := range others {
		if it.ID == chosen.Row && it.Table == chosen.Table {
			sh.Pick = nil
			return s.frontAskChange(ctx, c, sh, it)
		}
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.shop.no_item", "name", whatsapp.SafeText(chosen.Name)))
}

// shopChangeFrontItem makes the change confirmed to an item on a front's
// own list.
func (s *Server) shopChangeFrontItem(ctx context.Context, c *chat) error {
	sh := c.data.Shop
	t, ok, err := s.frontTable(ctx, c, sh)
	if !ok || err != nil {
		return err
	}
	m := *sh.Columns
	set := map[string]any{}
	switch sh.Action {
	case "price":
		set[m.Price] = priceCell(*sh.PriceKobo, m)
	case "hide", "show":
		set[m.Available] = sh.Action == "show"
	}
	for col := range set {
		if col == "" {
			if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
				return err
			}
			_, err := s.shopColumn(ctx, c, "", sh.Action)
			return err
		}
	}
	if _, ok, err := s.shopWrite(ctx, c, t, sh.Row, set); !ok || err != nil {
		return err
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		return waAudit(ctx, tx, c.ch.Name(), c.p, "storefront.item_change", t.Name, map[string]any{"table_id": t.ID, "row_id": sh.Row, "change": sh.Action, "front": sh.Front})
	}); err != nil {
		return err
	}
	if err := s.SyncStorefront(ctx, c.tenant.ID); err != nil {
		s.Logger.Warn("chat: refreshing the catalogue", "tenant", c.tenant.ID, "err", err)
	}
	return c.say(ctx, s, c.t("wa.front.changed", "name", whatsapp.SafeText(sh.Name), "front", whatsapp.SafeText(sh.FrontName))+" "+s.frontNote(ctx, c, sh))
}

// --- a front's forms, deliveries and orders (F4c) ---

var (
	// "turn on orders for Snacks", "turn on questions for Ask us",
	// "turn on deliveries for Snacks".
	turnOnForCmd = regexp.MustCompile(`(?i)^\s*turn\s+on\s+(orders|hire|questions|call-?backs|deliveries)\s+for\s+(.+?)\s*[.!]?\s*$`)
	// "orders Snacks", "orders for Snacks".
	ordersOnCmd = regexp.MustCompile(`(?i)^\s*(?:my\s+)?orders\s+(?:for\s+|on\s+|from\s+)?(.+?)\s*[.!?]?\s*$`)
)

// turnOnKinds are the action kinds "turn on … for <front>" names.
var turnOnKinds = map[string]string{"orders": "order", "hire": "hire", "questions": "enquiry", "callbacks": "callback", "call-backs": "callback", "deliveries": "order"}

// waFrontTurnOn answers "turn on <orders|questions|call-backs|deliveries>
// for <front>": the front's action named, shown and done on yes.
func (s *Server) waFrontTurnOn(ctx context.Context, c *chat, what, name string) error {
	what = strings.ToLower(what)
	_, list, ok, err := s.chatFronts(ctx, c)
	if !ok || err != nil {
		return err
	}
	f, ok := findFront(list, name)
	if !ok {
		return s.noFront(ctx, c, list, name)
	}
	if f.Slug == "" {
		// The home page's forms: as today.
		if what == "deliveries" {
			if !s.Dispatch.Ready() {
				return c.say(ctx, s, c.t("wa.front.no_deliveries"))
			}
			return s.shopAsk(ctx, c, &chatShop{Action: "deliveries_on"}, c.t("wa.shop.turn_on_deliveries"))
		}
		return s.shopAsk(ctx, c, &chatShop{Action: "orders_on"}, c.t("wa.shop.turn_on_orders"))
	}
	kind := turnOnKinds[what]
	var a *storefront.Action
	for i := range f.Actions {
		if f.Actions[i].Kind == kind && f.Actions[i].ID != nil {
			a = &f.Actions[i]
			break
		}
	}
	front := whatsapp.SafeText(f.Name)
	if a == nil {
		return c.say(ctx, s, c.t("wa.front.no_form", "front", front, "what", what))
	}
	sh := &chatShop{Action: "front_action_on", Front: f.ID.String(), FrontName: f.Name, Kind: kind, Row: a.ID.String()}
	if what == "deliveries" {
		if !s.Dispatch.Ready() {
			return c.say(ctx, s, c.t("wa.front.no_deliveries"))
		}
		sh.Action = "front_deliveries_on"
		return s.shopAsk(ctx, c, sh, c.t("wa.front.confirm_deliveries", "front", front))
	}
	if a.Enabled && a.Workflow != nil {
		return c.say(ctx, s, c.t("wa.front.form_already_on", "front", front, "form", whatsapp.SafeText(a.Label)))
	}
	return s.shopAsk(ctx, c, sh, c.t("wa.front.confirm_form_on", "front", front, "form", whatsapp.SafeText(a.Label)))
}

// shopTurnOnFrontAction turns a front's action on from chat, as the Forms
// page's Set up and switch do: its workflow made from its kind's template
// naming it by id and published with the web app's checks (unless one is
// deployed for it already), then the action switched on and the page
// published again.
func (s *Server) shopTurnOnFrontAction(ctx context.Context, c *chat) error {
	sh := c.data.Shop
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	if !c.p.Can(PermWorkflowEdit) || !c.p.Can(PermWorkflowPublish) {
		return c.say(ctx, s, c.t("wa.publish.denied", "org", c.tenant.Name))
	}
	id, err := uuid.Parse(sh.Row)
	if err != nil {
		return err
	}
	front := whatsapp.SafeText(sh.FrontName)
	var wf uuid.UUID
	var held, paystack bool
	err = chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		wf, held, err = s.setupActionTx(ctx, tx, c.p, id, func(action, target string, detail map[string]any) error {
			return waAudit(ctx, tx, c.ch.Name(), c.p, action, target, detail)
		})
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM connections WHERE environment = 'prod' AND connector = 'paystack' AND status = 'active')`).Scan(&paystack)
	})
	if errors.Is(err, storefront.ErrNotFound) {
		return c.say(ctx, s, c.t("wa.front.gone", "name", front))
	}
	if errors.Is(err, errConflict) {
		return c.say(ctx, s, c.t("wa.publish.failed", "reason", whatsapp.SafeText(chatReason(err))))
	}
	if err != nil {
		return err
	}
	if !held {
		state, needs, err := s.publishFromChat(ctx, c, wf, 1)
		if err != nil {
			return c.say(ctx, s, c.t("wa.publish.failed", "reason", whatsapp.SafeText(plainError(err))))
		}
		if state != publishedNow {
			return c.say(ctx, s, s.publishReply(ctx, c, state, sh.FrontName, needs, wf))
		}
	}
	var on bool
	err = chatTx(ctx, s, c, func(tx pgx.Tx) error {
		actions, err := storefront.Actions(ctx, tx, c.tenant.ID)
		if err != nil {
			return err
		}
		var cur *storefront.Action
		for i := range actions {
			if actions[i].ID != nil && *actions[i].ID == id {
				cur = &actions[i]
			}
		}
		if cur == nil {
			return storefront.ErrNotFound
		}
		next := *cur
		next.Enabled = true
		if next.Kind == "order" && !paystack {
			next.Payment = "none"
		}
		a, err := storefront.SaveAction(ctx, tx, c.tenant.ID, next)
		if err != nil {
			return err
		}
		if sf, err := storefront.Get(ctx, tx, c.tenant.ID); err == nil && sf.PublishedVersion > 0 {
			if _, err := s.publishGated(ctx, tx, c.tenant.ID, c.p.Actor()); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(ctx, `SELECT enabled FROM storefront_fronts WHERE id = $1`, sh.Front).Scan(&on); err != nil {
			return err
		}
		return waAudit(ctx, tx, c.ch.Name(), c.p, "storefront.action_save", id.String(), map[string]any{"enabled": true, "payment": a.Payment, "front": sh.Front})
	})
	if reason, ok := publishRefusal(err); ok {
		return c.say(ctx, s, c.t("wa.shop.publish_failed", "reason", whatsapp.SafeText(reason)))
	}
	if errors.Is(err, storefront.ErrNotFound) {
		return c.say(ctx, s, c.t("wa.front.gone", "name", front))
	}
	if errors.Is(err, storefront.ErrInvalid) {
		return c.say(ctx, s, c.t("wa.front.not_changed", "reason", whatsapp.SafeText(chatReason(err))))
	}
	if err != nil {
		return err
	}
	msg := c.t("wa.front.form_on", "front", front, "what", turnOnWord(sh.Kind))
	if !on {
		msg += " " + c.t("wa.front.turn_on_hint", "front", front)
	}
	if sh.Kind == "order" && !paystack {
		msg += "\n" + c.t("wa.shop.orders_no_paystack", "link", c.t("connect.hint", "service", "paystack"))
	}
	return c.say(ctx, s, msg)
}

// waShopOrdersOn answers "orders <front>": the front's open orders.
func (s *Server) waShopOrdersOn(ctx context.Context, c *chat, name string) error {
	_, list, ok, err := s.chatFronts(ctx, c)
	if !ok || err != nil {
		return err
	}
	f, ok := findFront(list, name)
	if !ok {
		return s.noFront(ctx, c, list, name)
	}
	return s.shopListOrders(ctx, c, &f.ID, f.Name, nil)
}
