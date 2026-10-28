package api

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// The storefront from chat (docs/spec/storefronts.md 5.1 and 6.8), on
// WhatsApp and Telegram alike: "make my page" sets one up, a question at
// a time, and publishes it; "my page", "publish" and "qr" show and share
// it; "add product" (or a photo with a caption), "price", "hide", "show"
// and "stock" change the catalogue; "orders" and "order <ref>" show
// orders and "order <ref> ready" or "delivered" move one on; "add
// service" and "hours" set what is booked and when the business opens;
// "close shop" and "open shop" stop and resume orders. Every change is
// shown and confirmed with yes, needs storefront.manage (and table.edit
// for the catalogue's rows), goes through the same code as the web app's
// (rows through the data_tables connection, photos through the media
// service and the moderator) and is audited.

// chatShop is the storefront being set up, or a change to it being
// confirmed.
type chatShop struct {
	// Action is what is being done: setup (the questions), publish,
	// product, price, hide, show, stock, service, hours, close, open,
	// ready or delivered.
	Action string `json:"action"`
	// Step is the set-up question being answered: name, tagline, area,
	// hours or photo; Card holds the answers so far.
	Step string           `json:"step,omitempty"`
	Card *storefront.Card `json:"card,omitempty"`
	// The item or service: its name, its catalogue row, what it was, and
	// what it becomes.
	Name      string            `json:"name,omitempty"`
	Row       string            `json:"row,omitempty"`
	Old       string            `json:"old,omitempty"`
	PriceKobo *int64            `json:"price_kobo,omitempty"`
	Stock     *int64            `json:"stock,omitempty"`
	Photo     string            `json:"photo,omitempty"` // a storefront_media id
	Minutes   int               `json:"minutes,omitempty"`
	Hours     *storefront.Hours `json:"hours,omitempty"`
	Message   string            `json:"message,omitempty"` // close: what the page says
	// ReopenAt: closed for today, the page opens again by itself then.
	ReopenAt  *time.Time `json:"reopen_at,omitempty"`
	Reference string     `json:"reference,omitempty"` // ready, delivered
	// Pick are the items offered for a numbered pick, when a name matched
	// several.
	Pick []shopChoice `json:"pick,omitempty"`
	// Nonce is the row write's idempotency key.
	Nonce string `json:"nonce,omitempty"`
}

type shopChoice struct {
	Row  string `json:"row"`
	Name string `json:"name"`
}

const (
	// shopPickMax is how many items a numbered pick offers.
	shopPickMax = 9
	// shopOrdersShown is how many open orders "orders" lists.
	shopOrdersShown = 10
	// closedToday is what the page says after "close shop today".
	closedToday = "Closed today. We open again tomorrow."
)

// The catalogue and services tables a set-up makes, and their mappings.
var (
	productColumns = []datatables.Column{
		{Name: "name", Title: "Name", Type: "text", Required: true},
		{Name: "price", Title: "Price (₦)", Type: "number"},
		{Name: "photo", Title: "Photo", Type: "text"},
		{Name: "available", Title: "Available", Type: "boolean"},
		{Name: "stock", Title: "Stock", Type: "integer"},
	}
	productMapping = storefront.Columns{Name: "name", Price: "price", PriceUnit: "naira", Photo: "photo", Available: "available", Stock: "stock"}
	serviceColumns = []datatables.Column{
		{Name: "name", Title: "Name", Type: "text", Required: true},
		{Name: "price", Title: "Price (₦)", Type: "number"},
		{Name: "duration_minutes", Title: "Minutes", Type: "integer", Required: true},
		{Name: "capacity", Title: "Customers at once", Type: "integer"},
	}
	serviceMapping = storefront.Columns{Name: "name", Price: "price", PriceUnit: "naira", Duration: "duration_minutes", Capacity: "capacity"}
)

var (
	orderCmd  = regexp.MustCompile(`^order ` + bookingRef + `(?: (ready|delivered|collected|done|complete|completed))?$`)
	closeShop = regexp.MustCompile(`(?i)^\s*close (?:my )?shop\b\s*(.*)$`)
)

// waShopCommand acts on a storefront command; false when cmd is not one.
func (s *Server) waShopCommand(ctx context.Context, c *chat, text, cmd string) (bool, error) {
	words := strings.Fields(cmd)
	if len(words) == 0 {
		return false, nil
	}
	args := func(n int) string {
		f := strings.Fields(text)
		if len(f) <= n {
			return ""
		}
		return strings.Trim(strings.Join(f[n:], " "), " ?!")
	}
	var do func() error
	switch {
	case cmd == "make my page" || cmd == "make my shop" || cmd == "my page" || cmd == "my shop" || cmd == "my storefront":
		do = func() error { return s.waMyPage(ctx, c) }
	case cmd == "publish" || cmd == "publish my page" || cmd == "publish my shop":
		do = func() error { return s.waShopPublishAsk(ctx, c) }
	case words[0] == "appeal":
		do = func() error { return s.waShopAppeal(ctx, c, args(1)) }
	case cmd == "qr" || cmd == "qr code" || cmd == "my qr" || cmd == "my qr code":
		do = func() error { return s.waShopQR(ctx, c) }
	case len(words) >= 2 && words[0] == "add" && words[1] == "product":
		do = func() error { return s.waShopProduct(ctx, c, args(2), nil) }
	case len(words) >= 2 && words[0] == "add" && words[1] == "service":
		do = func() error { return s.waShopService(ctx, c, args(2)) }
	case words[0] == "price" || words[0] == "hide" || words[0] == "stock" || words[0] == "show" && cmd != "show tables":
		do = func() error { return s.waShopChange(ctx, c, words[0], args(1)) }
	case words[0] == "hours" || cmd == "opening hours" || strings.HasPrefix(cmd, "opening hours "):
		n := 1
		if words[0] == "opening" {
			n = 2
		}
		do = func() error { return s.waShopHours(ctx, c, args(n)) }
	case closeShop.MatchString(text):
		do = func() error { return s.waShopClose(ctx, c, strings.TrimSpace(closeShop.FindStringSubmatch(text)[1])) }
	case cmd == "turn on orders" || cmd == "take orders" || cmd == "start taking orders":
		if s.Orders == nil {
			return false, nil
		}
		return true, s.shopAsk(ctx, c, &chatShop{Action: "orders_on"}, c.t("wa.shop.turn_on_orders"))
	case cmd == "open shop" || cmd == "open my shop" || cmd == "reopen shop" || cmd == "reopen":
		do = func() error { return s.waShopOpen(ctx, c) }
	case s.Orders != nil && (cmd == "orders" || cmd == "my orders" || cmd == "open orders"):
		do = func() error { return s.waShopOrders(ctx, c) }
	case s.Orders != nil && orderCmd.MatchString(cmd):
		m := orderCmd.FindStringSubmatch(cmd)
		ref, move := strings.ToUpper(m[1]), m[2]
		switch move {
		case "":
			do = func() error { return s.waShopOrder(ctx, c, ref) }
		case "ready":
			do = func() error { return s.waShopOrderMove(ctx, c, ref, "ready") }
		default:
			do = func() error { return s.waShopOrderMove(ctx, c, ref, "delivered") }
		}
	default:
		return false, nil
	}
	if !c.p.Can(PermStorefrontManage) {
		return true, c.say(ctx, s, c.t("wa.shop.denied", "org", c.tenant.Name))
	}
	return true, do()
}

// waShopReply takes a message while the storefront is being set up or a
// change confirmed.
func (s *Server) waShopReply(ctx context.Context, c *chat, text, cmd string) error {
	sh := c.data.Shop
	switch c.state {
	case stateCollecting:
		if sh.Action == "setup" {
			return s.waShopSetup(ctx, c, text)
		}
		if len(sh.Pick) > 0 {
			return s.waShopPick(ctx, c, text)
		}
	case stateConfirming:
		switch cmd {
		case "yes", "y", "confirm":
			return s.waShopApply(ctx, c)
		case "no", "n":
			if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
				return err
			}
			return c.say(ctx, s, c.t("wa.shop.left"))
		}
		return c.say(ctx, s, c.t("wa.shop.confirm_reminder"), shopButtons(c, sh.Action)...)
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.not_understood")+" "+s.waHelp(c))
}

func shopButtons(c *chat, action string) []whatsapp.Button {
	switch action {
	case "publish":
		return []whatsapp.Button{{ID: "yes", Title: c.t("button.yes_publish")}, {ID: "cancel", Title: c.t("button.cancel")}}
	case "product", "service":
		return addButtons(c)
	}
	return []whatsapp.Button{{ID: "yes", Title: c.t("button.yes")}, {ID: "cancel", Title: c.t("button.cancel")}}
}

// shopAsk keeps a change in the session and asks for yes.
func (s *Server) shopAsk(ctx context.Context, c *chat, sh *chatShop, text string) error {
	sh.Nonce = uuid.NewString()
	if err := s.saveSession(ctx, c, stateConfirming, chatData{Shop: sh}, waPendingTTL); err != nil {
		return err
	}
	return c.say(ctx, s, text+"\n\n"+c.t("wa.shop.confirm_reminder"), shopButtons(c, sh.Action)...)
}

// sayImage sends a photo by its public link, with a caption.
func (c *chat) sayImage(ctx context.Context, link, caption string) error {
	if c.note != "" {
		caption, c.note = c.note+"\n\n"+caption, ""
	}
	_, err := c.ch.Send(ctx, c.addr, whatsapp.Message{Text: c.ch.Prefix(c.tenant.Name) + caption, Image: link})
	return err
}

// shopOf reads the organisation's storefront.
func (s *Server) shopOf(ctx context.Context, c *chat) (storefront.Storefront, error) {
	var sf storefront.Storefront
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		sf, err = storefront.Get(ctx, tx, c.tenant.ID)
		return err
	})
	return sf, err
}

// shopNeed reads the storefront, or says there is none yet (ok false).
func (s *Server) shopNeed(ctx context.Context, c *chat) (storefront.Storefront, bool, error) {
	sf, err := s.shopOf(ctx, c)
	if errors.Is(err, storefront.ErrNotFound) {
		return sf, false, c.say(ctx, s, c.t("wa.shop.none_yet", "org", c.tenant.Name))
	}
	return sf, err == nil, err
}

func (s *Server) shopLink(sf storefront.Storefront) string {
	return sf.URL(s.previewDomain())
}

// --- make my page ---

// waMyPage starts the set-up when there is no storefront yet, and shows
// the one there is otherwise: its link, its status and today's orders.
func (s *Server) waMyPage(ctx context.Context, c *chat) error {
	sf, err := s.shopOf(ctx, c)
	if errors.Is(err, storefront.ErrNotFound) {
		sh := &chatShop{Action: "setup", Step: "name", Card: &storefront.Card{}}
		if err := s.saveSession(ctx, c, stateCollecting, chatData{Shop: sh}, waPendingTTL); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.shop.setup_start")+"\n\n"+s.shopPrompt(c, "name"))
	}
	if err != nil {
		return err
	}
	name := whatsapp.SafeText(sf.Draft.Name)
	switch {
	case sf.Status == "in_review":
		return c.say(ctx, s, c.t("wa.shop.in_review", "name", name))
	case sf.Status == "suspended":
		why := ""
		if sf.SuspendedReason != nil {
			why = whatsapp.SafeText(*sf.SuspendedReason)
		}
		return c.say(ctx, s, c.t("wa.shop.page_suspended", "name", name, "reason", why))
	case sf.PublishedVersion == 0 && sf.ReviewNote != nil:
		return c.say(ctx, s, c.t("wa.shop.page_turned_down", "name", name, "reason", whatsapp.SafeText(*sf.ReviewNote)))
	case sf.PublishedVersion == 0:
		return c.say(ctx, s, c.t("wa.shop.page_draft", "name", name))
	}
	var count int
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		now := time.Now().In(storefront.Lagos)
		day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, storefront.Lagos)
		return tx.QueryRow(ctx, `SELECT count(*) FROM storefront_orders WHERE tenant_id = $1 AND kind = 'order' AND created_at >= $2`, c.tenant.ID, day).Scan(&count)
	}); err != nil {
		return err
	}
	kv := []string{"name", name, "link", s.shopLink(sf), "count", strconv.Itoa(count)}
	if sf.Status == "closed" {
		msg := ""
		if sf.ClosedMessage != nil {
			msg = whatsapp.SafeText(*sf.ClosedMessage)
		}
		return c.say(ctx, s, c.t("wa.shop.page_closed", append(kv, "message", msg)...))
	}
	return c.say(ctx, s, c.t("wa.shop.page_live", kv...))
}

// shopPrompt is a set-up question.
func (s *Server) shopPrompt(c *chat, step string) string {
	return c.t("wa.shop.ask_" + step)
}

// shopSteps are the set-up's questions in order.
var shopSteps = []string{"name", "tagline", "area", "hours", "photo"}

// waShopSetup takes the answer to a set-up question.
func (s *Server) waShopSetup(ctx context.Context, c *chat, text string) error {
	sh := c.data.Shop
	if sh.Card == nil {
		sh.Card = &storefront.Card{}
	}
	text = strings.TrimSpace(text)
	card := *sh.Card
	skip := isSkip(text) || strings.EqualFold(text, "no")
	again := func(reason string) error {
		return c.say(ctx, s, c.t("wa.shop.bad_answer", "reason", whatsapp.SafeText(reason), "prompt", s.shopPrompt(c, sh.Step)))
	}
	switch sh.Step {
	case "name":
		if text == "" || skip {
			return c.say(ctx, s, s.shopPrompt(c, "name"))
		}
		card.Name = text
	case "tagline":
		if !skip {
			card.Tagline = text
		}
	case "area":
		switch {
		case skip:
		case len([]rune(text)) > 80:
			card.Address = text
		default:
			card.Area = text
		}
	case "hours":
		if !skip {
			h, err := storefront.ParseHours(text)
			if err != nil {
				return c.say(ctx, s, c.t("wa.shop.hours_how", "reason", whatsapp.SafeText(strings.TrimPrefix(err.Error(), "storefront: "))))
			}
			card.Hours = h
		}
	case "photo":
		if !skip {
			return c.say(ctx, s, c.t("wa.shop.photo_or_skip"))
		}
		return s.waShopCreate(ctx, c, card)
	}
	// Each answer is checked as publishing will check it.
	check := card
	if err := check.Check(); err != nil {
		return again(plainError(err))
	}
	sh.Card = &card
	next := ""
	for i, st := range shopSteps {
		if st == sh.Step && i+1 < len(shopSteps) {
			next = shopSteps[i+1]
		}
	}
	sh.Step = next
	if err := s.saveSession(ctx, c, stateCollecting, chatData{Shop: sh}, waPendingTTL); err != nil {
		return err
	}
	return c.say(ctx, s, s.shopPrompt(c, next))
}

// errShopExists: a storefront was made while the set-up was answered.
var errShopExists = errors.New("the organisation has a storefront already")

// waShopCreate saves the set-up's card as a draft at the first free
// address its name gives, makes and maps a catalogue table, and asks to
// publish.
func (s *Server) waShopCreate(ctx context.Context, c *chat, card storefront.Card) error {
	if !c.p.Can(PermStorefrontManage) {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.shop.denied", "org", c.tenant.Name))
	}
	actor := c.p.Actor()
	saved := false
	var sf storefront.Storefront
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		if _, err := storefront.Get(ctx, tx, c.tenant.ID); err == nil {
			return errShopExists
		} else if !errors.Is(err, storefront.ErrNotFound) {
			return err
		}
		for _, slug := range storefront.SlugCandidates(card.Name, card.Area) {
			if _, err := storefront.CheckSlug(slug); err != nil {
				continue // reserved or protected
			}
			// A taken address is tried inside a savepoint, so the next
			// can be tried in the same transaction.
			sp, err := tx.Begin(ctx)
			if err != nil {
				return err
			}
			sf, err = storefront.Save(ctx, sp, c.tenant.ID, slug, card, nil, storefront.Columns{}, actor)
			if errors.Is(err, storefront.ErrSlugTaken) {
				_ = sp.Rollback(ctx)
				continue
			}
			if err != nil {
				_ = sp.Rollback(ctx)
				return err
			}
			if err := sp.Commit(ctx); err != nil {
				return err
			}
			saved = true
			return waAudit(ctx, tx, c.ch.Name(), c.p, "storefront.save", sf.Slug, map[string]any{"setup": true})
		}
		return nil
	})
	if errors.Is(err, errShopExists) {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return s.waMyPage(ctx, c)
	}
	if err != nil {
		return err
	}
	if !saved {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.shop.no_address", "name", whatsapp.SafeText(card.Name))+s.waLink(c, "/storefront"))
	}
	sf, note := s.shopEnsureCatalogue(ctx, c, sf)
	lines := []string{"*" + whatsapp.SafeText(card.Name) + "*"}
	for _, v := range []string{card.Tagline, card.Area, card.Address} {
		if v != "" {
			lines = append(lines, whatsapp.SafeText(v))
		}
	}
	if hasHours(card.Hours) {
		lines = append(lines, storefront.FormatHours(card.Hours))
	}
	if card.Logo != "" {
		lines = append(lines, c.t("wa.shop.with_logo"))
	}
	msg := c.t("wa.shop.created", "details", strings.Join(lines, "\n"))
	if d := s.previewDomain(); d != "" {
		msg += "\n" + c.t("wa.shop.address", "link", "https://"+sf.Slug+"."+d)
	}
	if note != "" {
		msg += "\n\n" + note
	}
	return s.shopAsk(ctx, c, &chatShop{Action: "publish"}, msg+"\n\n"+c.t("wa.shop.confirm_publish"))
}

func hasHours(h storefront.Hours) bool {
	for _, d := range h.Days {
		if len(d) > 0 {
			return true
		}
	}
	return false
}

// shopEnsureCatalogue makes a products table and maps it as the
// catalogue when the storefront has none, returning the storefront and,
// when that could not be done, a note saying why.
func (s *Server) shopEnsureCatalogue(ctx context.Context, c *chat, sf storefront.Storefront) (storefront.Storefront, string) {
	if sf.CatalogueTable != nil {
		return sf, ""
	}
	if !c.p.Can(PermTableEdit) {
		return sf, c.t("wa.shop.no_table_edit")
	}
	t := datatables.Table{Name: "products", Title: "Products", Description: "The storefront's catalogue", Columns: productColumns}
	if err := s.chatMakeTable(ctx, c, &t, "products", "shop_products", "products_2", "products_3"); err != nil {
		s.Logger.Warn("chat: making the catalogue table", "tenant", c.tenant.ID, "err", err)
		return sf, c.t("wa.shop.no_tables", "reason", tablesChatReason(err))
	}
	m := productMapping
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		if err := m.Check(tableColumns(t)); err != nil {
			return err
		}
		var err error
		if sf, err = storefront.Save(ctx, tx, c.tenant.ID, sf.Slug, sf.Draft, &t.ID, m, c.p.Actor()); err != nil {
			return err
		}
		return waAudit(ctx, tx, c.ch.Name(), c.p, "storefront.save", sf.Slug, map[string]any{"catalogue_table": t.ID})
	})
	if err != nil {
		s.Logger.Warn("chat: mapping the catalogue table", "tenant", c.tenant.ID, "err", err)
		return sf, c.t("wa.shop.no_tables", "reason", whatsapp.SafeText(plainError(err)))
	}
	return sf, ""
}

// chatMakeTable makes a data table for a chat: makeOwnTable, audited as
// the chat's person on its channel.
func (s *Server) chatMakeTable(ctx context.Context, c *chat, t *datatables.Table, names ...string) error {
	return s.makeOwnTable(ctx, c.tenant.ID, c.p.Actor(), func(tx pgx.Tx, action, target string, detail map[string]any) error {
		return waAudit(ctx, tx, c.ch.Name(), c.p, action, target, detail)
	}, t, names...)
}

// makeOwnTable makes a data table as the API's createTable does, under
// the first of names that is free: the plan's limits, the organisation's
// project, the record, then the table in Xenibase (the record removed if
// that fails). Chat and the storefront editor's Make my catalogue use it
// (no-technical-steps N7).
func (s *Server) makeOwnTable(ctx context.Context, tenant uuid.UUID, actor string, audit func(tx pgx.Tx, action, target string, detail map[string]any) error,
	t *datatables.Table, names ...string) error {
	svc, err := s.tables()
	if err != nil {
		return err
	}
	lim, err := s.Store.LimitsFor(ctx, tenant)
	if err != nil {
		return err
	}
	if err := datatables.CheckColumns(t.Columns, lim.MaxDataTableColumns); err != nil {
		return err
	}
	inTx := func(fn func(pgx.Tx) error) error { return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, fn) }
	pickName := func(tx pgx.Tx) error {
		if err := s.checkCount(ctx, tx, tenant, "max_data_tables"); err != nil {
			return err
		}
		tables, err := datatables.ListTables(ctx, tx)
		if err != nil {
			return err
		}
		taken := map[string]bool{}
		for _, x := range tables {
			taken[x.Name] = true
		}
		for _, n := range names {
			if !taken[n] && datatables.CheckTableName(n) == nil {
				t.Name = n
				return nil
			}
		}
		return fmt.Errorf("%w: a table named %q already exists", datatables.ErrConflict, names[0])
	}
	if err := inTx(pickName); err != nil {
		return err
	}
	proj, err := svc.EnsureProject(ctx, tenant)
	if err != nil {
		return err
	}
	if !proj.Ready() {
		return datatables.ErrProvisioning
	}
	t.CreatedBy = actor
	if err := inTx(func(tx pgx.Tx) error {
		if err := pickName(tx); err != nil {
			return err
		}
		if err := datatables.InsertTable(ctx, tx, tenant, t); err != nil {
			return err
		}
		return audit(tx, "table.create", t.Name, map[string]any{"table_id": t.ID, "columns": columnNames(t.Columns)})
	}); err != nil {
		return err
	}
	if err := svc.Apply(ctx, tenant, actor, datatables.CreateTableChange(t.Name, t.Columns)); err != nil {
		_ = inTx(func(tx pgx.Tx) error { return datatables.DeleteTable(ctx, tx, t.ID) })
		return err
	}
	return nil
}

// --- publish and share ---

func (s *Server) waShopPublishAsk(ctx context.Context, c *chat) error {
	sf, ok, err := s.shopNeed(ctx, c)
	if !ok || err != nil {
		return err
	}
	msg := c.t("wa.shop.confirm_publish_again", "name", whatsapp.SafeText(sf.Draft.Name))
	if d := s.previewDomain(); d != "" {
		msg += "\n" + c.t("wa.shop.address", "link", "https://"+sf.Slug+"."+d)
	}
	return s.shopAsk(ctx, c, &chatShop{Action: "publish"}, msg)
}

// waShopPublish publishes the draft as the web app does (the catalogue
// copy refreshed first), then sends the link, the QR code and a status
// text to paste.
func (s *Server) waShopPublish(ctx context.Context, c *chat) error {
	if err := s.SyncStorefront(ctx, c.tenant.ID); err != nil {
		if errors.Is(err, storefront.ErrNotFound) {
			return c.say(ctx, s, c.t("wa.shop.none_yet", "org", c.tenant.Name))
		}
		s.Logger.Warn("chat: refreshing the catalogue", "tenant", c.tenant.ID, "err", err)
		return c.say(ctx, s, c.t("wa.shop.publish_failed", "reason", tablesChatReason(err)))
	}
	var sf storefront.Storefront
	var held bool
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		res, err := s.publishGated(ctx, tx, c.tenant.ID, c.p.Actor())
		if err != nil {
			return err
		}
		sf, held = res.Storefront, res.Held
		return waAudit(ctx, tx, c.ch.Name(), c.p, "storefront.publish", sf.Slug, map[string]any{"version": sf.PublishedVersion, "held_for_review": held})
	})
	if reason, ok := publishRefusal(err); ok {
		return c.say(ctx, s, c.t("wa.shop.publish_failed", "reason", whatsapp.SafeText(reason)))
	}
	if err != nil {
		return err
	}
	if held {
		return c.say(ctx, s, c.t("wa.shop.in_review", "name", whatsapp.SafeText(sf.Draft.Name)))
	}
	link := s.shopLink(sf)
	name := whatsapp.SafeText(sf.Draft.Name)
	if err := c.say(ctx, s, c.t("wa.shop.published", "link", link)); err != nil {
		return err
	}
	if err := c.sayImage(ctx, link+"/qr.png", c.t("wa.shop.qr_caption", "name", name, "link", link)); err != nil {
		s.Logger.Warn("chat: sending the QR code", "channel", c.ch.Name(), "err", err)
	}
	if err := c.say(ctx, s, c.t("wa.shop.status_post", "name", name, "link", link)); err != nil {
		return err
	}
	// Then: take orders from chat as well (N6).
	if s.Orders != nil && c.p.Can(PermWorkflowPublish) && !s.ordersOn(ctx, c.tenant.ID) {
		return s.shopAsk(ctx, c, &chatShop{Action: "orders_on"}, c.t("wa.shop.turn_on_orders"))
	}
	return nil
}

// ordersOn reports whether the page's order form is on.
func (s *Server) ordersOn(ctx context.Context, tenant uuid.UUID) bool {
	on := false
	_ = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		list, err := storefront.Actions(ctx, tx, tenant)
		for _, a := range list {
			if a.Kind == "order" && a.Enabled {
				on = true
			}
		}
		return err
	})
	return on
}

// waShopQR sends the page's QR code (the public host's /qr.png) with its
// address, and where to print the card.
func (s *Server) waShopQR(ctx context.Context, c *chat) error {
	sf, ok, err := s.shopNeed(ctx, c)
	if !ok || err != nil {
		return err
	}
	link := s.shopLink(sf)
	if link == "" {
		return c.say(ctx, s, c.t("wa.shop.not_published"))
	}
	caption := c.t("wa.shop.qr_caption", "name", whatsapp.SafeText(sf.Draft.Name), "link", link)
	if s.PublicURL != "" {
		caption += "\n" + c.t("wa.shop.qr_print", "url", s.PublicURL+"/storefront")
	}
	return c.sayImage(ctx, link+"/qr.png", caption)
}

// --- the catalogue ---

// splitPrice reads "<name> <price>": the price is the last word, or the
// last two ("₦ 2,000", "2000 naira").
func splitPrice(s string) (string, int64, bool) {
	f := strings.Fields(s)
	for n := 1; n <= 2 && n < len(f); n++ {
		if kobo, ok := storefront.ParsePrice(strings.Join(f[len(f)-n:], " ")); ok {
			return strings.Join(f[:len(f)-n], " "), kobo, true
		}
	}
	return "", 0, false
}

// productArgs reads a product command's or a photo caption's words: the
// name and the price, "add product" before them or not.
func productArgs(s string) (string, int64, bool) {
	s = strings.TrimSpace(s)
	if f := strings.Fields(s); len(f) >= 2 && strings.EqualFold(f[0], "add") && strings.EqualFold(f[1], "product") {
		s = strings.Join(f[2:], " ")
	}
	name, kobo, ok := splitPrice(s)
	name = strings.Trim(name, " -:,")
	if !ok || name == "" || len([]rune(name)) > 120 {
		return "", 0, false
	}
	return name, kobo, true
}

// shopCatalogue is the storefront with its catalogue table, or says why
// the catalogue cannot be changed (ok false). A storefront without one
// gets one when create is set and the person may make tables.
func (s *Server) shopCatalogue(ctx context.Context, c *chat, create bool) (storefront.Storefront, datatables.Table, bool, error) {
	sf, ok, err := s.shopNeed(ctx, c)
	if !ok || err != nil {
		return sf, datatables.Table{}, false, err
	}
	if sf.CatalogueTable == nil {
		if !create {
			return sf, datatables.Table{}, false, c.say(ctx, s, c.t("wa.shop.no_catalogue"))
		}
		var note string
		if sf, note = s.shopEnsureCatalogue(ctx, c, sf); note != "" {
			return sf, datatables.Table{}, false, c.say(ctx, s, note)
		}
	}
	t, err := s.waGetTable(ctx, c.tenant.ID, *sf.CatalogueTable)
	if errors.Is(err, datatables.ErrNotFound) {
		return sf, t, false, c.say(ctx, s, c.t("wa.shop.no_catalogue"))
	}
	return sf, t, err == nil, err
}

// shopColumn says the catalogue has no column for what (ok false).
func (s *Server) shopColumn(ctx context.Context, c *chat, col, what string) (bool, error) {
	if col != "" {
		return true, nil
	}
	return false, c.say(ctx, s, c.t("wa.shop.no_column", "what", what)+s.waLink(c, "/storefront"))
}

// waShopProduct answers "add product <name> <price>", or a photo with
// that caption (photo is the photo, not yet stored): shown, then added
// on yes.
func (s *Server) waShopProduct(ctx context.Context, c *chat, args string, photo *whatsapp.Media) error {
	if !c.p.Can(PermTableEdit) {
		return c.say(ctx, s, c.t("wa.shop.denied_table", "org", c.tenant.Name))
	}
	name, kobo, ok := productArgs(args)
	if !ok {
		if photo != nil {
			return c.say(ctx, s, c.t("wa.shop.photo_caption"))
		}
		return c.say(ctx, s, c.t("wa.shop.product_how"))
	}
	sf, err := s.shopOf(ctx, c)
	if errors.Is(err, storefront.ErrNotFound) {
		return c.say(ctx, s, c.t("wa.shop.none_yet", "org", c.tenant.Name))
	}
	if err != nil {
		return err
	}
	m := sf.CatalogueColumns
	if sf.CatalogueTable == nil {
		m = productMapping // made on yes
	} else {
		if ok, err := s.shopColumn(ctx, c, m.Price, "price"); !ok || err != nil {
			return err
		}
		if photo != nil {
			if ok, err := s.shopColumn(ctx, c, m.Photo, "photo"); !ok || err != nil {
				return err
			}
		}
		items, err := s.shopItems(ctx, c)
		if err != nil {
			return err
		}
		for _, it := range items {
			if matchKey(it.Name) == matchKey(name) {
				return c.say(ctx, s, c.t("wa.shop.product_exists", "name", whatsapp.SafeText(it.Name), "price", priceText(c, it.PriceKobo)))
			}
		}
	}
	sh := &chatShop{Action: "product", Name: name, PriceKobo: &kobo}
	withPhoto := ""
	if photo != nil {
		id, ok, err := s.shopStorePhoto(ctx, c, photo)
		if !ok || err != nil {
			return err
		}
		sh.Photo, withPhoto = id, c.t("wa.shop.with_photo")
	}
	return s.shopAsk(ctx, c, sh, c.t("wa.shop.confirm_product", "name", whatsapp.SafeText(name), "price", storefront.FormatNaira(kobo), "photo", withPhoto))
}

func priceText(c *chat, kobo *int64) string {
	if kobo == nil {
		return c.t("wa.shop.price_none")
	}
	return storefront.FormatNaira(*kobo)
}

// waPhoto takes a photo: the logo the set-up asked for, or a product to
// add from its caption.
func (s *Server) waPhoto(ctx context.Context, c *chat) error {
	if c.state != stateIdle {
		if sh := c.data.Shop; sh != nil && sh.Action == "setup" && sh.Step == "photo" {
			return s.waShopLogo(ctx, c)
		}
		return c.say(ctx, s, c.t("wa.shop.photo_not_now"))
	}
	if !c.p.Can(PermStorefrontManage) {
		return c.say(ctx, s, c.t("wa.shop.denied", "org", c.tenant.Name))
	}
	if strings.TrimSpace(c.in.Text) == "" {
		return c.say(ctx, s, c.t("wa.shop.photo_caption"))
	}
	return s.waShopProduct(ctx, c, c.in.Text, c.in.Image)
}

// waShopLogo takes the set-up's photo as the page's logo and finishes the
// set-up.
func (s *Server) waShopLogo(ctx context.Context, c *chat) error {
	card := storefront.Card{}
	if c.data.Shop.Card != nil {
		card = *c.data.Shop.Card
	}
	id, ok, err := s.shopStorePhoto(ctx, c, c.in.Image)
	if err != nil || !ok {
		return err
	}
	card.Logo = id
	return s.waShopCreate(ctx, c, card)
}

// shopStorePhoto fetches a photo sent in chat and stores it as the web
// app stores an upload (resized, stripped, moderated, audited); ok is
// false when it could not be, and the person has been told why.
func (s *Server) shopStorePhoto(ctx context.Context, c *chat, photo *whatsapp.Media) (string, bool, error) {
	if s.Media == nil {
		return "", false, c.say(ctx, s, c.t("wa.shop.photos_off"))
	}
	if !s.limiter("wa-photo:"+c.addr, 30*time.Second, 5).Allow() {
		return "", false, c.say(ctx, s, c.t("wa.shop.too_many"))
	}
	data, _, _, err := s.fetchMedia(ctx, c, photo, media.MaxImageBytes)
	if errors.Is(err, whatsapp.ErrMediaTooLarge) {
		return "", false, c.say(ctx, s, c.t("wa.shop.photo_bad", "reason", fmt.Sprintf("over %d MB", media.MaxImageBytes>>20)))
	}
	if err != nil {
		s.Logger.Warn("chat: fetching a photo", "channel", c.ch.Name(), "err", err)
		return "", false, c.say(ctx, s, c.t("wa.shop.photo_failed"))
	}
	m, err := s.putStorefrontImage(ctx, c.p, data, func(tx pgx.Tx, m storefront.Media) error {
		return waAudit(ctx, tx, c.ch.Name(), c.p, "storefront.media_add", m.ID.String(), map[string]any{"moderation": m.Moderation})
	})
	clear(data)
	switch {
	case errors.Is(err, media.ErrImage):
		return "", false, c.say(ctx, s, c.t("wa.shop.photo_bad", "reason", whatsapp.SafeText(plainError(err))))
	case errors.Is(err, errImageRejected):
		return "", false, c.say(ctx, s, c.t("wa.shop.photo_rejected"))
	case err != nil:
		s.Logger.Warn("chat: storing a photo", "tenant", c.tenant.ID, "err", err)
		return "", false, c.say(ctx, s, c.t("wa.shop.photo_failed"))
	}
	return m.ID.String(), true, nil
}

// shopItems are the catalogue's items, the copy refreshed from the table
// first (the old copy when the table cannot be read).
func (s *Server) shopItems(ctx context.Context, c *chat) ([]storefront.Item, error) {
	if err := s.SyncStorefront(ctx, c.tenant.ID); err != nil {
		s.Logger.Warn("chat: refreshing the catalogue", "tenant", c.tenant.ID, "err", err)
	}
	var items []storefront.Item
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		items, err = storefront.Catalogue(ctx, tx, c.tenant.ID)
		return err
	})
	return items, err
}

// matchItems finds items by name, case-insensitively: the exact ones,
// else those the words start, else those containing them.
func matchItems(items []storefront.Item, name string) []storefront.Item {
	key := matchKey(name)
	if key == "" {
		return nil
	}
	var exact, prefix, part []storefront.Item
	for _, it := range items {
		n := matchKey(it.Name)
		switch {
		case n == key:
			exact = append(exact, it)
		case strings.HasPrefix(n, key):
			prefix = append(prefix, it)
		case strings.Contains(n, key):
			part = append(part, it)
		}
	}
	switch {
	case len(exact) > 0:
		return exact
	case len(prefix) > 0:
		return prefix
	}
	return part
}

func numberedChoices(pick []shopChoice) string {
	lines := make([]string, len(pick))
	for i, p := range pick {
		lines[i] = strconv.Itoa(i+1) + ". " + whatsapp.SafeText(p.Name)
	}
	return strings.Join(lines, "\n")
}

// waShopChange answers "price <item> <price>", "hide <item>", "show
// <item>" and "stock <item> <n>".
func (s *Server) waShopChange(ctx context.Context, c *chat, action, args string) error {
	if !c.p.Can(PermTableEdit) {
		return c.say(ctx, s, c.t("wa.shop.denied_table", "org", c.tenant.Name))
	}
	sh := &chatShop{Action: action}
	name := args
	switch action {
	case "price":
		n, kobo, ok := splitPrice(args)
		if !ok || strings.TrimSpace(n) == "" {
			return c.say(ctx, s, c.t("wa.shop.how_price"))
		}
		name, sh.PriceKobo = n, &kobo
	case "stock":
		f := strings.Fields(args)
		var n int64 = -1
		if len(f) >= 2 {
			if v, err := strconv.ParseInt(f[len(f)-1], 10, 64); err == nil {
				n = v
			}
		}
		if n < 0 || n > 1_000_000 {
			return c.say(ctx, s, c.t("wa.shop.how_stock"))
		}
		name, sh.Stock = strings.Join(f[:len(f)-1], " "), &n
	}
	if strings.TrimSpace(name) == "" {
		return c.say(ctx, s, c.t("wa.shop.how_item"))
	}
	sf, _, ok, err := s.shopCatalogue(ctx, c, false)
	if !ok || err != nil {
		return err
	}
	col := map[string]string{"price": sf.CatalogueColumns.Price, "hide": sf.CatalogueColumns.Available, "show": sf.CatalogueColumns.Available,
		"stock": sf.CatalogueColumns.Stock}[action]
	what := map[string]string{"price": "price", "hide": "available", "show": "available", "stock": "stock"}[action]
	if ok, err := s.shopColumn(ctx, c, col, what); !ok || err != nil {
		return err
	}
	items, err := s.shopItems(ctx, c)
	if err != nil {
		return err
	}
	match := matchItems(items, name)
	switch {
	case len(match) == 0:
		return c.say(ctx, s, c.t("wa.shop.no_item", "name", whatsapp.SafeText(name)))
	case len(match) > 1:
		for _, it := range match[:min(shopPickMax, len(match))] {
			sh.Pick = append(sh.Pick, shopChoice{Row: it.ID, Name: it.Name})
		}
		if err := s.saveSession(ctx, c, stateCollecting, chatData{Shop: sh}, waPendingTTL); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.shop.which_item", "list", numberedChoices(sh.Pick)))
	}
	return s.shopAskChange(ctx, c, sh, match[0])
}

// waShopPick takes the number of an item from the list offered.
func (s *Server) waShopPick(ctx context.Context, c *chat, text string) error {
	sh := c.data.Shop
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || n < 1 || n > len(sh.Pick) {
		return c.say(ctx, s, c.t("wa.shop.pick_again", "list", numberedChoices(sh.Pick)))
	}
	chosen := sh.Pick[n-1]
	items, err := s.shopItems(ctx, c)
	if err != nil {
		return err
	}
	for _, it := range items {
		if it.ID == chosen.Row {
			sh.Pick = nil
			return s.shopAskChange(ctx, c, sh, it)
		}
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.shop.no_item", "name", whatsapp.SafeText(chosen.Name)))
}

// shopAskChange shows an item's change, old to new, and asks for yes.
func (s *Server) shopAskChange(ctx context.Context, c *chat, sh *chatShop, it storefront.Item) error {
	sh.Row, sh.Name = it.ID, it.Name
	name := whatsapp.SafeText(it.Name)
	var text string
	switch sh.Action {
	case "price":
		sh.Old = priceText(c, it.PriceKobo)
		text = c.t("wa.shop.confirm_price", "name", name, "from", sh.Old, "to", storefront.FormatNaira(*sh.PriceKobo))
	case "hide":
		text = c.t("wa.shop.confirm_hide", "name", name)
	case "show":
		text = c.t("wa.shop.confirm_show", "name", name)
	case "stock":
		sh.Old = c.t("wa.shop.stock_untracked")
		if it.Stock != nil {
			sh.Old = strconv.FormatInt(*it.Stock, 10)
		}
		text = c.t("wa.shop.confirm_stock", "name", name, "from", sh.Old, "to", strconv.FormatInt(*sh.Stock, 10))
	}
	return s.shopAsk(ctx, c, sh, text)
}

// priceCell is a price as the catalogue's price column holds it.
func priceCell(kobo int64, m storefront.Columns) any {
	switch {
	case m.PriceUnit == "kobo":
		return kobo
	case kobo%100 == 0:
		return kobo / 100
	}
	return float64(kobo) / 100
}

// shopWrite inserts a row into a table (key empty) or updates one by
// its id, through the data_tables connection as the API does: the values
// checked by the table's columns, the session's nonce the idempotency
// key. ok is false when it was refused, and the person has been told.
func (s *Server) shopWrite(ctx context.Context, c *chat, t datatables.Table, key string, row map[string]any) (string, bool, error) {
	checked, err := datatables.CheckRow(t.Columns, row, key == "", false)
	if err != nil {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return "", false, err
		}
		return "", false, c.say(ctx, s, c.t("wa.shop.not_saved", "reason", whatsapp.SafeText(strings.TrimPrefix(err.Error(), "invalid: "))))
	}
	tctx := s.waTablesCtx(ctx, c)
	tr, err := s.tableRowsFor(tctx, c.tenant.ID, t, tablesChatPurpose)
	if err != nil {
		s.Logger.Warn("chat: the data tables connection", "err", err)
		return "", false, c.say(ctx, s, c.t("wa.table.unavailable"))
	}
	idem := "wa-" + c.data.Shop.Nonce
	var out map[string]any
	if key == "" {
		out, err = tr.call(tctx, s, "insert_rows", map[string]any{"table": t.Name, "rows": []any{checked}}, idem)
	} else {
		id, perr := strconv.ParseInt(key, 10, 64)
		if perr != nil {
			return "", false, perr
		}
		out, err = tr.call(tctx, s, "update_rows", map[string]any{"table": t.Name, "key": id, "set": checked, "max_affected": 1}, idem)
	}
	if err != nil {
		// The session stays as it is: a yes again sends the same key.
		return "", false, c.say(ctx, s, c.t("wa.shop.add_failed", "reason", tablesChatReason(err)))
	}
	id := key
	if rows := rowsOf(out); len(rows) > 0 {
		if m, ok := rows[0].(map[string]any); ok && m["id"] != nil {
			id = cell(m["id"])
		}
	} else if key != "" && countValue(out["affected"]) == 0 {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return "", false, err
		}
		return "", false, c.say(ctx, s, c.t("wa.shop.no_item", "name", whatsapp.SafeText(c.data.Shop.Name)))
	}
	return id, true, nil
}

// countValue reads a count as the connector or the fake gives it.
func countValue(v any) int64 {
	return countOf(map[string]any{"count": v})
}

// shopPublishedNote says where a change shows: on the page now, or once
// it is published.
func (s *Server) shopPublishedNote(c *chat, sf storefront.Storefront) string {
	if sf.PublishedVersion > 0 {
		return c.t("wa.shop.on_page")
	}
	return c.t("wa.shop.publish_hint")
}

// shopRepublish refreshes the copies and, when the storefront is
// published, publishes the draft again so the page shows the change.
func (s *Server) shopRepublish(ctx context.Context, c *chat) (storefront.Storefront, error) {
	if err := s.SyncStorefront(ctx, c.tenant.ID); err != nil {
		s.Logger.Warn("chat: refreshing the catalogue", "tenant", c.tenant.ID, "err", err)
	}
	var sf storefront.Storefront
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		if sf, err = storefront.Get(ctx, tx, c.tenant.ID); err != nil || sf.PublishedVersion == 0 {
			return err
		}
		res, err := s.publishGated(ctx, tx, c.tenant.ID, c.p.Actor())
		if err != nil {
			return err
		}
		sf = res.Storefront
		return waAudit(ctx, tx, c.ch.Name(), c.p, "storefront.publish", sf.Slug, map[string]any{"version": sf.PublishedVersion})
	})
	return sf, err
}

// --- services and hours ---

// waShopService answers "add service <name> <minutes> <price>".
func (s *Server) waShopService(ctx context.Context, c *chat, args string) error {
	if !c.p.Can(PermTableEdit) {
		return c.say(ctx, s, c.t("wa.shop.denied_table", "org", c.tenant.Name))
	}
	f := strings.Fields(args)
	if len(f) < 3 {
		return c.say(ctx, s, c.t("wa.shop.service_how"))
	}
	kobo, okP := storefront.ParsePrice(f[len(f)-1])
	minutes, okM := storefront.ParseMinutes(f[len(f)-2])
	name := strings.Join(f[:len(f)-2], " ")
	if !okP || !okM || name == "" || len([]rune(name)) > 120 {
		return c.say(ctx, s, c.t("wa.shop.service_how"))
	}
	sf, ok, err := s.shopNeed(ctx, c)
	if !ok || err != nil {
		return err
	}
	if sf.ServicesTable != nil {
		if ok, err := s.shopColumn(ctx, c, sf.ServicesColumns.Price, "price"); !ok || err != nil {
			return err
		}
	}
	sh := &chatShop{Action: "service", Name: name, Minutes: minutes, PriceKobo: &kobo}
	return s.shopAsk(ctx, c, sh, c.t("wa.shop.confirm_service", "name", whatsapp.SafeText(name), "minutes", strconv.Itoa(minutes), "price", storefront.FormatNaira(kobo)))
}

// waShopHours answers "hours mon-fri 9-6".
func (s *Server) waShopHours(ctx context.Context, c *chat, args string) error {
	h, err := storefront.ParseHours(args)
	if err != nil {
		return c.say(ctx, s, c.t("wa.shop.hours_how", "reason", whatsapp.SafeText(strings.TrimPrefix(err.Error(), "storefront: "))))
	}
	if _, ok, err := s.shopNeed(ctx, c); !ok || err != nil {
		return err
	}
	return s.shopAsk(ctx, c, &chatShop{Action: "hours", Hours: &h}, c.t("wa.shop.confirm_hours", "hours", storefront.FormatHours(h)))
}

// --- close and open ---

func (s *Server) waShopClose(ctx context.Context, c *chat, rest string) error {
	sf, ok, err := s.shopNeed(ctx, c)
	if !ok || err != nil {
		return err
	}
	if sf.PublishedVersion == 0 {
		return c.say(ctx, s, c.t("wa.shop.not_published"))
	}
	msg := rest
	var reopen *time.Time
	switch strings.ToLower(strings.Trim(rest, " .!")) {
	case "today", "for today":
		msg = closedToday
		t := storefront.TomorrowMorning(time.Now())
		reopen = &t
	case "", "now":
		msg = ""
	}
	if len([]rune(msg)) > 200 || storefront.CheckClosedMessage(msg) != nil {
		return c.say(ctx, s, c.t("wa.shop.close_how"))
	}
	shown := msg
	if shown == "" {
		shown = storefront.DefaultClosedMessage
	}
	ask := c.t("wa.shop.confirm_close", "message", whatsapp.SafeText(shown))
	if reopen != nil {
		ask += "\n" + c.t("wa.shop.reopens", "time", reopen.Format("Monday 3pm"))
	}
	return s.shopAsk(ctx, c, &chatShop{Action: "close", Message: msg, ReopenAt: reopen}, ask)
}

func (s *Server) waShopOpen(ctx context.Context, c *chat) error {
	sf, ok, err := s.shopNeed(ctx, c)
	if !ok || err != nil {
		return err
	}
	switch {
	case sf.PublishedVersion == 0:
		return c.say(ctx, s, c.t("wa.shop.not_published"))
	case sf.Status != "closed":
		return c.say(ctx, s, c.t("wa.shop.already_open"))
	}
	return s.shopAsk(ctx, c, &chatShop{Action: "open"}, c.t("wa.shop.confirm_open"))
}

// --- orders ---

// shopOrderLine is an order in a list: reference, what, total, status
// and the customer, masked.
func (s *Server) shopOrderLine(c *chat, o storefront.Order) string {
	what := whatsapp.SafeText(storefront.Summary(o.Basket))
	switch o.Kind {
	case "booking":
		what = whatsapp.SafeText(o.ServiceName)
		if o.StartsAt != nil {
			what += ", " + storefront.When(*o.StartsAt)
		}
	case "enquiry", "callback":
		what = c.t("wa.shop.kind." + o.Kind)
	}
	cust := o.Customer
	maskCustomer(&cust)
	parts := []string{"*" + o.Reference + "*", what}
	if o.TotalKobo > 0 {
		parts = append(parts, storefront.FormatNaira(o.TotalKobo))
	}
	parts = append(parts, c.t("wa.shop.status."+o.Status), strings.TrimSpace(cust.Name+" "+cust.Phone))
	return "• " + strings.Join(parts, " · ")
}

// waShopOrders lists the open orders, newest first.
func (s *Server) waShopOrders(ctx context.Context, c *chat) error {
	var list []storefront.Order
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		list, err = s.Orders.ListOrders(ctx, tx, c.tenant.ID, "open", "", shopOrdersShown)
		return err
	}); err != nil {
		return err
	}
	if len(list) == 0 {
		return c.say(ctx, s, c.t("wa.shop.no_orders"))
	}
	lines := []string{c.t("wa.shop.orders_head")}
	for _, o := range list {
		lines = append(lines, s.shopOrderLine(c, o))
	}
	return c.say(ctx, s, strings.Join(lines, "\n")+"\n\n"+c.t("wa.shop.orders_tail"))
}

func (s *Server) shopGetOrder(ctx context.Context, c *chat, ref string) (storefront.Order, bool, error) {
	var o storefront.Order
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		o, err = s.Orders.GetOrder(ctx, tx, c.tenant.ID, ref)
		return err
	})
	if errors.Is(err, storefront.ErrNoOrder) {
		return o, false, c.say(ctx, s, c.t("wa.shop.no_order", "ref", ref))
	}
	return o, err == nil, err
}

// waShopOrder shows one order, the customer masked.
func (s *Server) waShopOrder(ctx context.Context, c *chat, ref string) error {
	o, ok, err := s.shopGetOrder(ctx, c, ref)
	if !ok || err != nil {
		return err
	}
	var items []string
	switch o.Kind {
	case "booking":
		line := whatsapp.SafeText(o.ServiceName)
		if o.StartsAt != nil {
			line += ", " + storefront.When(*o.StartsAt)
		}
		items = append(items, line)
	case "enquiry", "callback":
		items = append(items, c.t("wa.shop.kind."+o.Kind))
	}
	for _, l := range o.Basket {
		items = append(items, fmt.Sprintf("%d × %s  %s", l.Quantity, whatsapp.SafeText(l.Name), storefront.FormatNaira(l.LineKobo)))
	}
	if o.Fulfilment != "" {
		items = append(items, whatsapp.SafeText(o.Fulfilment))
	}
	cust := o.Customer
	maskCustomer(&cust)
	return c.say(ctx, s, c.t("wa.shop.order_detail", "ref", o.Reference, "status", c.t("wa.shop.status."+o.Status), "items", strings.Join(items, "\n"),
		"total", storefront.FormatNaira(o.TotalKobo), "customer", strings.TrimSpace(cust.Name+" "+cust.Phone), "when", storefront.When(o.CreatedAt)))
}

// waShopOrderMove answers "order <ref> ready" and "order <ref>
// delivered": shown, then done on yes.
//
// Ready tells the customer (by text, through Orders.NotifyCustomer) that
// the order is ready to collect, and leaves its status as it is: the
// order is not finished until it is collected. Delivered (or collected,
// done) completes it, as the order workflow's "mark it delivered or
// collected" step does (decide complete).
func (s *Server) waShopOrderMove(ctx context.Context, c *chat, ref, move string) error {
	o, ok, err := s.shopGetOrder(ctx, c, ref)
	if !ok || err != nil {
		return err
	}
	status := c.t("wa.shop.status." + o.Status)
	if move == "ready" {
		if o.Kind != "order" || o.Status != "accepted" && o.Status != "paid" {
			return c.say(ctx, s, c.t("wa.shop.not_ready_now", "ref", ref, "status", status))
		}
		return s.shopAsk(ctx, c, &chatShop{Action: "ready", Reference: ref}, c.t("wa.shop.confirm_ready", "ref", ref))
	}
	switch o.Status {
	case "received", "accepted", "paid":
	default:
		return c.say(ctx, s, c.t("wa.shop.cannot_complete", "ref", ref, "status", status))
	}
	return s.shopAsk(ctx, c, &chatShop{Action: "delivered", Reference: ref}, c.t("wa.shop.confirm_complete", "ref", ref))
}

// --- doing it ---

// waShopApply does the confirmed change.
func (s *Server) waShopApply(ctx context.Context, c *chat) error {
	sh := c.data.Shop
	if !c.p.Can(PermStorefrontManage) {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.shop.denied", "org", c.tenant.Name))
	}
	switch sh.Action {
	case "product", "price", "hide", "show", "stock", "service":
		if !c.p.Can(PermTableEdit) {
			if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
				return err
			}
			return c.say(ctx, s, c.t("wa.shop.denied_table", "org", c.tenant.Name))
		}
		if !s.limiter("wa-shop:"+c.addr, 20*time.Second, 10).Allow() {
			return c.say(ctx, s, c.t("wa.shop.too_many"))
		}
	}
	switch sh.Action {
	case "publish":
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return s.waShopPublish(ctx, c)
	case "product":
		return s.shopAddProduct(ctx, c)
	case "price", "hide", "show", "stock":
		return s.shopChangeItem(ctx, c)
	case "service":
		return s.shopAddService(ctx, c)
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	switch sh.Action {
	case "hours":
		return s.shopSaveHours(ctx, c, *sh.Hours)
	case "close", "open":
		return s.shopCloseOpen(ctx, c, sh.Action, sh.Message, sh.ReopenAt)
	case "ready":
		return s.shopReady(ctx, c, sh.Reference)
	case "delivered":
		return s.shopComplete(ctx, c, sh.Reference)
	case "orders_on":
		return s.shopTurnOnOrders(ctx, c)
	case "deliveries_on":
		return s.shopTurnOnDeliveries(ctx, c)
	}
	return nil
}

func (s *Server) shopAddProduct(ctx context.Context, c *chat) error {
	sh := c.data.Shop
	sf, t, ok, err := s.shopCatalogue(ctx, c, true)
	if !ok || err != nil {
		if !ok && err == nil {
			err = s.saveSession(ctx, c, stateIdle, chatData{}, 0)
		}
		return err
	}
	m := sf.CatalogueColumns
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
		return waAudit(ctx, tx, c.ch.Name(), c.p, "storefront.item_add", t.Name, map[string]any{"table_id": t.ID, "row_id": id, "photo": sh.Photo != ""})
	}); err != nil {
		return err
	}
	if err := s.SyncStorefront(ctx, c.tenant.ID); err != nil {
		s.Logger.Warn("chat: refreshing the catalogue", "tenant", c.tenant.ID, "err", err)
	}
	return c.say(ctx, s, c.t("wa.shop.product_added", "name", whatsapp.SafeText(sh.Name), "price", priceText(c, sh.PriceKobo))+" "+s.shopPublishedNote(c, sf))
}

func (s *Server) shopChangeItem(ctx context.Context, c *chat) error {
	sh := c.data.Shop
	sf, t, ok, err := s.shopCatalogue(ctx, c, false)
	if !ok || err != nil {
		if !ok && err == nil {
			err = s.saveSession(ctx, c, stateIdle, chatData{}, 0)
		}
		return err
	}
	m := sf.CatalogueColumns
	set := map[string]any{}
	switch sh.Action {
	case "price":
		set[m.Price] = priceCell(*sh.PriceKobo, m)
	case "hide", "show":
		set[m.Available] = sh.Action == "show"
	case "stock":
		set[m.Stock] = *sh.Stock
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
		return waAudit(ctx, tx, c.ch.Name(), c.p, "storefront.item_change", t.Name, map[string]any{"table_id": t.ID, "row_id": sh.Row, "change": sh.Action})
	}); err != nil {
		return err
	}
	if err := s.SyncStorefront(ctx, c.tenant.ID); err != nil {
		s.Logger.Warn("chat: refreshing the catalogue", "tenant", c.tenant.ID, "err", err)
	}
	return c.say(ctx, s, c.t("wa.shop.changed", "name", whatsapp.SafeText(sh.Name))+" "+s.shopPublishedNote(c, sf))
}

func (s *Server) shopAddService(ctx context.Context, c *chat) error {
	sh := c.data.Shop
	sf, ok, err := s.shopNeed(ctx, c)
	if !ok || err != nil {
		if !ok && err == nil {
			err = s.saveSession(ctx, c, stateIdle, chatData{}, 0)
		}
		return err
	}
	var t datatables.Table
	if sf.ServicesTable == nil {
		// A services table, mapped with the availability kept as it is.
		t = datatables.Table{Name: "services", Title: "Services", Description: "The storefront's services to book", Columns: serviceColumns}
		if err := s.chatMakeTable(ctx, c, &t, "services", "shop_services", "services_2", "services_3"); err != nil {
			s.Logger.Warn("chat: making the services table", "tenant", c.tenant.ID, "err", err)
			if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
				return err
			}
			return c.say(ctx, s, c.t("wa.shop.no_tables", "reason", tablesChatReason(err)))
		}
		m := serviceMapping
		err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
			if err := m.CheckServices(tableColumns(t)); err != nil {
				return err
			}
			var err error
			if sf, err = storefront.SaveBookings(ctx, tx, c.tenant.ID, &t.ID, m, sf.Availability); err != nil {
				return err
			}
			return waAudit(ctx, tx, c.ch.Name(), c.p, "storefront.bookings_save", sf.Slug, map[string]any{"services_table": t.ID})
		})
		if err != nil {
			return err
		}
	} else if t, err = s.waGetTable(ctx, c.tenant.ID, *sf.ServicesTable); err != nil {
		return err
	}
	m := sf.ServicesColumns
	row := map[string]any{m.Name: sh.Name}
	if m.Duration != "" {
		row[m.Duration] = sh.Minutes
	}
	if m.Price != "" && sh.PriceKobo != nil {
		row[m.Price] = priceCell(*sh.PriceKobo, m)
	}
	if m.Capacity != "" {
		row[m.Capacity] = 1
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
		return waAudit(ctx, tx, c.ch.Name(), c.p, "storefront.service_add", t.Name, map[string]any{"table_id": t.ID, "row_id": id})
	}); err != nil {
		return err
	}
	var others bool
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		cur, err := storefront.Get(ctx, tx, c.tenant.ID)
		others = err == nil && pendingEdits(cur)
		return err
	}); err != nil {
		return err
	}
	if sf, err = s.shopRepublish(ctx, c); err != nil {
		s.Logger.Warn("chat: republishing", "tenant", c.tenant.ID, "err", err)
	}
	msg := c.t("wa.shop.service_added", "name", whatsapp.SafeText(sh.Name)) + " " + c.t("wa.shop.service_hint")
	if others && sf.PublishedVersion > 0 {
		msg += "\n" + c.t("wa.shop.other_edits_live")
	}
	return c.say(ctx, s, msg)
}

func (s *Server) shopSaveHours(ctx context.Context, c *chat, h storefront.Hours) error {
	var sf storefront.Storefront
	var others bool
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		cur, err := storefront.Get(ctx, tx, c.tenant.ID)
		if err != nil {
			return err
		}
		others = pendingEdits(cur)
		card := cur.Draft
		h.Note = card.Hours.Note
		card.Hours = h
		if sf, err = storefront.Save(ctx, tx, c.tenant.ID, cur.Slug, card, cur.CatalogueTable, cur.CatalogueColumns, c.p.Actor()); err != nil {
			return err
		}
		return waAudit(ctx, tx, c.ch.Name(), c.p, "storefront.save", sf.Slug, map[string]any{"hours": true})
	})
	if errors.Is(err, storefront.ErrNotFound) {
		return c.say(ctx, s, c.t("wa.shop.none_yet", "org", c.tenant.Name))
	}
	if err != nil {
		return err
	}
	if sf.PublishedVersion > 0 {
		if _, err := s.shopRepublish(ctx, c); err != nil {
			if errors.Is(err, storefront.ErrInvalid) || errors.Is(err, storefront.ErrSlugTaken) {
				return c.say(ctx, s, c.t("wa.shop.hours_saved")+" "+c.t("wa.shop.publish_failed", "reason", whatsapp.SafeText(plainError(err))))
			}
			return err
		}
	}
	msg := c.t("wa.shop.hours_saved") + " " + s.shopPublishedNote(c, sf)
	if others && sf.PublishedVersion > 0 {
		msg += "\n" + c.t("wa.shop.other_edits_live")
	}
	return c.say(ctx, s, msg)
}

// pendingEdits reports whether a published page's draft holds changes
// saved since it was published: republishing for one change from chat
// makes those live too, and the owner is told (N6).
func pendingEdits(sf storefront.Storefront) bool {
	return sf.PublishedVersion > 0 && sf.PublishedAt != nil && sf.UpdatedAt.After(sf.PublishedAt.Add(time.Second))
}

func (s *Server) shopCloseOpen(ctx context.Context, c *chat, action, message string, reopen *time.Time) error {
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var sf storefront.Storefront
		var err error
		if action == "close" {
			sf, err = storefront.CloseUntil(ctx, tx, c.tenant.ID, message, reopen)
		} else {
			sf, err = storefront.Reopen(ctx, tx, c.tenant.ID)
		}
		if err != nil {
			return err
		}
		return waAudit(ctx, tx, c.ch.Name(), c.p, "storefront."+map[string]string{"close": "close", "open": "reopen"}[action], sf.Slug, nil)
	})
	switch {
	case errors.Is(err, storefront.ErrNotPublished):
		return c.say(ctx, s, c.t("wa.shop.not_published"))
	case errors.Is(err, storefront.ErrInvalid):
		return c.say(ctx, s, c.t("wa.shop.close_how"))
	case err != nil:
		return err
	case action == "close" && reopen != nil:
		return c.say(ctx, s, c.t("wa.shop.closed")+" "+c.t("wa.shop.reopens", "time", reopen.Format("Monday 3pm")))
	case action == "close":
		return c.say(ctx, s, c.t("wa.shop.closed"))
	}
	return c.say(ctx, s, c.t("wa.shop.opened"))
}

func (s *Server) shopReady(ctx context.Context, c *chat, ref string) error {
	o, ok, err := s.shopGetOrder(ctx, c, ref)
	if !ok || err != nil {
		return err
	}
	if o.Kind != "order" || o.Status != "accepted" && o.Status != "paid" {
		return c.say(ctx, s, c.t("wa.shop.not_ready_now", "ref", ref, "status", c.t("wa.shop.status."+o.Status)))
	}
	sf, err := s.shopOf(ctx, c)
	if err != nil {
		return err
	}
	// To the customer, in the page's words: the business's name and the
	// reference, nothing else.
	// In the business's language for its customers (N6).
	text := tr(s.Orders.CustomerLanguage(ctx, c.tenant.ID), "customer.order_ready", "name", sf.Draft.Name, "reference", ref)
	out, err := s.Orders.NotifyCustomer(ctx, c.tenant.ID, ref, text, "", "ready:"+ref)
	if err != nil {
		s.Logger.Warn("chat: telling a customer", "tenant", c.tenant.ID, "err", err)
		return c.say(ctx, s, c.t("wa.shop.ready_failed", "ref", ref, "reason", whatsapp.SafeText(plainError(err))))
	}
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		return waAudit(ctx, tx, c.ch.Name(), c.p, "storefront.order_ready", ref, map[string]any{"queued": out["queued"]})
	}); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.shop.ready_sent", "ref", ref))
}

func (s *Server) shopComplete(ctx context.Context, c *chat, ref string) error {
	_, err := s.Orders.Decide(ctx, c.tenant.ID, ref, "complete", "", c.p.Actor())
	switch {
	case errors.Is(err, storefront.ErrNoOrder):
		return c.say(ctx, s, c.t("wa.shop.no_order", "ref", ref))
	case errors.Is(err, storefront.ErrDecision):
		// Moved on meanwhile.
		o, ok, err := s.shopGetOrder(ctx, c, ref)
		if !ok || err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.shop.cannot_complete", "ref", ref, "status", c.t("wa.shop.status."+o.Status)))
	case err != nil:
		return err
	}
	return c.say(ctx, s, c.t("wa.shop.completed", "ref", ref))
}

// waShopAppeal answers "appeal <message>": a suspended page, or a first
// publish turned down, is looked at again (trust.go).
func (s *Server) waShopAppeal(ctx context.Context, c *chat, message string) error {
	if !c.p.Can(PermStorefrontManage) {
		return c.say(ctx, s, c.t("wa.shop.denied", "org", c.tenant.Name))
	}
	if strings.TrimSpace(message) == "" {
		return c.say(ctx, s, c.t("wa.shop.appeal_how"))
	}
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		return s.appeal(ctx, tx, c.tenant.ID, message, c.p.Actor(), func(target string) error {
			return waAudit(ctx, tx, c.ch.Name(), c.p, "storefront.appeal", target, nil)
		})
	})
	switch {
	case errors.Is(err, errConflict):
		return c.say(ctx, s, c.t("wa.shop.appeal_none"))
	case errors.Is(err, errBadRequest):
		return c.say(ctx, s, c.t("wa.shop.appeal_how"))
	case errors.Is(err, storefront.ErrNotFound), errors.Is(err, pgx.ErrNoRows):
		return c.say(ctx, s, c.t("wa.shop.none_yet", "org", c.tenant.Name))
	case err != nil:
		return err
	}
	return c.say(ctx, s, c.t("wa.shop.appeal_sent"))
}
