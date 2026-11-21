package api

// The catalogue of what a person can do from the Taskiem chat
// (docs/spec/chat-menus.md §3, §4): every action under its topic, with who
// may use it, the part of Taskiem it belongs to, whether it only shows
// something, what it opens (an answer, a form, questions in the chat, or a
// page) and the typed command it stands for. The menus (chat_menus.go),
// GET /v1/chat/actions and the questions come from it.
//
// The words are English for now; the menus in the other languages follow
// as the catalogue's words are translated (the spec's open item).

import (
	"context"
	"maps"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/areas"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/riders"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// What an action does.
const (
	doesShow    = "show"    // only shows something
	doesAsks    = "asks"    // asks before it changes anything, by itself
	doesChanges = "changes" // changes something when done
)

// How an action is done.
const (
	opensShow      = "show"      // the answer, at once
	opensForm      = "form"      // a form in the app and on the web
	opensQuestions = "questions" // asked step by step in the chat
	opensPage      = "page"      // a page
)

// chatField is a value an action needs, asked for in the chat.
type chatField struct {
	Key  string
	Ask  string // the question
	Kind string // text, phone, money, number, signed, order, booking, rider, item, product, zone, a choice,
	// or a saved thing (saved objects): saved (any, by its name), saved_place
	// (a place), person (typed, or a saved person)
	// Choices are the fixed choices of a "choice" field.
	Choices  []string
	Optional bool
}

// chatAction is one thing a person can do (spec §3).
type chatAction struct {
	ID    string
	Topic string
	Title string
	Line  string
	Icon  string
	Does  string
	Opens string
	// Shortcut is the typed command, as the help gives it; "" when there is
	// none yet.
	Shortcut string
	// Tool is the chat tool a form opens (chat_tool_requests.go's keys),
	// with Add for its "add one" form; Page is a page's path on the web.
	Tool string
	Add  bool
	Page string
	// Fields are asked for in the chat; Make turns their values into the
	// command, read as typed. A form action with fields asks them on the
	// channels that have no forms.
	Fields []chatField
	Make   func(v map[string]string) string
	// may reports whether the person may use it here.
	may func(s *Server, c *chat, on areas.State) bool
}

// chatTopic is a topic of the menus (spec §4).
type chatTopic struct {
	ID, Name, Icon, Color string
	// Words name it, alone ("riders") or in a help ("help riders").
	Words []string
}

// The topics, in the order the menus show them.
var chatTopics = []chatTopic{
	{"sales", "Sales", "sell", "lime", []string{"sales", "sell", "selling"}},
	{"stock", "Stock", "stock", "orange", []string{"stock", "inventory"}},
	{"products", "Products", "products", "teal", []string{"products", "product", "services"}},
	{"page", "Your page", "storefront", "violet", []string{"page", "my shop", "shop"}},
	{"orders", "Orders", "orders", "blue", []string{"orders"}},
	{"riders", "Riders", "rider", "teal", []string{"riders", "rider", "deliveries", "delivery", "dispatch"}},
	{"bookings", "Bookings", "bookings", "violet", []string{"bookings", "appointments"}},
	{"buying", "Suppliers", "purchases", "orange", []string{"suppliers", "supplier", "buying"}},
	{"money", "Money", "money", "lime", []string{"money", "expenses", "invoices"}},
	{"owe", "Who owes you", "customers", "red", []string{"debts", "debtors", "credit"}},
	{"staff", "Staff and tasks", "tasks", "blue", []string{"staff", "tasks", "team"}},
	{"lists", "Your lists", "tables", "violet", []string{"lists", "tables"}},
	{"auto", "Automations", "workflows", "teal", []string{"automations", "automation", "workflows"}},
	{"saved", "Saved things", "saved", "orange", []string{"saved"}},
	{"school", "School", "school", "blue", []string{"school", "students", "fees"}},
	{"account", "Your account", "you", "red", []string{"account", "settings"}},
}

// Who may: a permission, a part of Taskiem, and what must be running.
func perm(p string) func(*Server, *chat, areas.State) bool {
	return func(_ *Server, c *chat, _ areas.State) bool { return c.p.Can(p) }
}

func permIn(p string, a areas.Area) func(*Server, *chat, areas.State) bool {
	return func(_ *Server, c *chat, on areas.State) bool { return c.p.Can(p) && on.On(a) }
}

func both(fs ...func(*Server, *chat, areas.State) bool) func(*Server, *chat, areas.State) bool {
	return func(s *Server, c *chat, on areas.State) bool {
		for _, f := range fs {
			if !f(s, c, on) {
				return false
			}
		}
		return true
	}
}

// ridersMay is who may use a riders action, as the chat's riders
// commands decide (whatsapp_riders.go): the riders themselves are the
// storefront's to manage, offering and releasing orders a cashier's too;
// the deliveries need the dispatch running; all in the Storefront part.
func ridersMay(p string, dispatch bool) func(*Server, *chat, areas.State) bool {
	return func(s *Server, c *chat, on areas.State) bool {
		return s.Riders.Ready() && (!dispatch || s.Dispatch.Ready()) && c.p.Can(p) && on.On(areas.Storefront)
	}
}

var (
	stockOn     = func(s *Server, _ *chat, _ areas.State) bool { return s.Stock.Ready() }
	ordersOn    = func(s *Server, _ *chat, _ areas.State) bool { return s.Orders != nil }
	purchasesOn = func(s *Server, _ *chat, _ areas.State) bool { return s.Purchases.Ready() }
	invoicesOn  = func(s *Server, _ *chat, _ areas.State) bool { return s.Invoices != nil }
	aiOn        = func(s *Server, _ *chat, _ areas.State) bool { return s.AI != nil }
	anyone      = func(*Server, *chat, areas.State) bool { return true }
	inBusiness  = func(_ *Server, c *chat, _ areas.State) bool { return c.tenant.ID != uuid.Nil }
)

// fill puts values into a command: "{name}" by its value; "[ {areas}]"
// only when the value inside is given.
func fill(tmpl string) func(map[string]string) string {
	opt := regexp.MustCompile(`\[([^\]]*)\]`)
	key := regexp.MustCompile(`\{([a-z_]+)\}`)
	return func(v map[string]string) string {
		out := opt.ReplaceAllStringFunc(tmpl, func(m string) string {
			inner := m[1 : len(m)-1]
			for _, k := range key.FindAllStringSubmatch(inner, -1) {
				if strings.TrimSpace(v[k[1]]) == "" {
					return ""
				}
			}
			return inner
		})
		out = key.ReplaceAllStringFunc(out, func(m string) string { return strings.TrimSpace(v[m[1:len(m)-1]]) })
		return strings.Join(strings.Fields(out), " ")
	}
}

func f(key, ask, kind string) chatField { return chatField{Key: key, Ask: ask, Kind: kind} }

// atNames is make with the named values' "@" taken off, as a saved thing's
// name is written after the command's own "@".
func atNames(mk func(map[string]string) string, keys ...string) func(map[string]string) string {
	return func(v map[string]string) string {
		w := maps.Clone(v)
		for _, k := range keys {
			w[k] = strings.TrimPrefix(strings.TrimSpace(w[k]), "@")
		}
		return mk(w)
	}
}

func opt(fl chatField) chatField { fl.Optional = true; return fl }

func choice(key, ask string, choices ...string) chatField {
	return chatField{Key: key, Ask: ask, Kind: "choice", Choices: choices}
}

// chatActions is the catalogue (spec §4).
var chatActions = []chatAction{
	// Sales
	{ID: "sales.sell", Topic: "sales", Title: "Record a sale", Line: "Items, amount, and how they paid", Icon: "sell", Does: doesChanges, Opens: opensForm,
		Shortcut: "sold 3 bags of rice 45,000 cash", Tool: "sell",
		Fields: []chatField{f("what", "What did you sell? Say how many, like 3 bags of rice.", "text"), f("amount", "For how much in all?", "money"),
			choice("paid", "How did they pay?", "cash", "transfer", "pos")},
		Make: fill("sold {what} {amount} {paid}"), may: both(permIn(PermTableEdit, areas.Stock), stockOn)},
	{ID: "sales.today", Topic: "sales", Title: "Today's sales", Line: "What came in today", Icon: "money", Does: doesShow, Opens: opensShow,
		Shortcut: "how did we do today", may: perm(PermTableRead)},
	{ID: "sales.week", Topic: "sales", Title: "How we did this week", Line: "Sales, money in and out", Icon: "money", Does: doesShow, Opens: opensShow,
		Shortcut: "how did we do this week", may: perm(PermTableRead)},
	{ID: "sales.handover", Topic: "sales", Title: "Hand over a shift", Line: "Cash and stock at the change", Icon: "handover", Does: doesChanges, Opens: opensForm,
		Shortcut: "/handover", Tool: "handover", may: both(permIn(PermTableRead, areas.Stock), stockOn)},

	// Stock
	{ID: "stock.low", Topic: "stock", Title: "See what's low", Line: "Items about to run out", Icon: "stock", Does: doesShow, Opens: opensShow,
		Shortcut: "stock", may: both(permIn(PermTableRead, areas.Stock), stockOn)},
	{ID: "stock.add", Topic: "stock", Title: "Add stock that came in", Line: "What came, how many, at what cost", Icon: "stock", Does: doesChanges, Opens: opensForm,
		Shortcut: "add 50 cartons of Indomie at 8,500", Tool: "stock", Add: true,
		Fields: []chatField{f("what", "What came in? Say how many, like 50 cartons of Indomie.", "text"), opt(f("cost", "What did each cost? Send skip if you'd rather not say.", "money"))},
		Make:   fill("add {what}[ at {cost}]"), may: both(permIn(PermTableEdit, areas.Stock), stockOn)},
	{ID: "stock.adjust", Topic: "stock", Title: "Correct a count", Line: "Spoilt, missing, or counted wrong", Icon: "stock", Does: doesChanges, Opens: opensQuestions,
		Shortcut: "adjust rice -2 spoilt",
		Fields: []chatField{f("item", "Which item?", "item"), f("by", "By how many? Use a minus for less, like -2.", "signed"),
			opt(choice("why", "Why? Send skip if you'd rather not say.", "spoilt", "missing", "counted wrong"))},
		Make: fill("adjust {item} {by}[ {why}]"), may: both(permIn(PermTableEdit, areas.Stock), stockOn)},
	{ID: "stock.one", Topic: "stock", Title: "How many of an item", Line: "One item's count", Icon: "stock", Does: doesShow, Opens: opensQuestions,
		Shortcut: "stock rice", Fields: []chatField{f("item", "Which item?", "item")},
		Make: fill("stock {item}"), may: both(permIn(PermTableRead, areas.Stock), stockOn)},
	{ID: "stock.count", Topic: "stock", Title: "Count the shelf", Line: "Item by item, what is really there", Icon: "stock", Does: doesAsks, Opens: opensShow,
		Shortcut: "count", may: both(permIn(PermTableEdit, areas.Stock), stockOn)},

	// Products
	{ID: "products.add", Topic: "products", Title: "Add a product", Line: "Name, price, photo, sizes", Icon: "products", Does: doesChanges, Opens: opensForm,
		Shortcut: "add product Jollof rice 2000", Tool: "products", Add: true,
		Fields: []chatField{f("name", "What's the product called?", "text"), f("price", "What's its price?", "money")},
		Make:   fill("add product {name} {price}"), may: perm(PermStorefrontManage)},
	{ID: "products.price", Topic: "products", Title: "Change a price", Line: "A new price for one product", Icon: "products", Does: doesChanges, Opens: opensQuestions,
		Shortcut: "price jollof rice 2500",
		Fields:   []chatField{f("name", "Which product?", "product"), f("price", "What's the new price?", "money")},
		Make:     fill("price {name} {price}"), may: perm(PermStorefrontManage)},
	{ID: "products.hide", Topic: "products", Title: "Hide or show a product", Line: "Off your page for now, or back", Icon: "products", Does: doesChanges, Opens: opensQuestions,
		Shortcut: "hide jollof rice",
		Fields:   []chatField{choice("do", "Hide it, or show it again?", "hide", "show"), f("name", "Which product?", "product")},
		Make:     fill("{do} {name}"), may: perm(PermStorefrontManage)},
	{ID: "products.service", Topic: "products", Title: "Add a service", Line: "A haircut, a fitting, a session", Icon: "services", Does: doesChanges, Opens: opensForm,
		Shortcut: "add service Haircut 30 2000", Tool: "services", Add: true,
		Fields: []chatField{f("name", "What's the service called?", "text"), f("minutes", "How many minutes does it take?", "number"), f("price", "What's its price?", "money")},
		Make:   fill("add service {name} {minutes} {price}"), may: both(perm(PermStorefrontManage), ordersOn)},
	{ID: "products.prices", Topic: "products", Title: "Price lists", Line: "Prices for your trade buyers", Icon: "prices", Does: doesShow, Opens: opensForm,
		Shortcut: "/prices", Tool: "prices", may: perm(PermStorefrontManage)},

	// Your page
	{ID: "page.see", Topic: "page", Title: "See your page", Line: "What customers see, and its QR code", Icon: "storefront", Does: doesShow, Opens: opensShow,
		Shortcut: "my page", may: permIn(PermStorefrontManage, areas.Storefront)},
	{ID: "page.hours", Topic: "page", Title: "Opening hours", Line: "Days and times you open", Icon: "bookings", Does: doesChanges, Opens: opensQuestions,
		Shortcut: "hours mon-sat 8-6", Fields: []chatField{f("hours", "When do you open? Like mon-sat 8-6.", "text")},
		Make: fill("hours {hours}"), may: permIn(PermStorefrontManage, areas.Storefront)},
	{ID: "page.shut", Topic: "page", Title: "Close or open the shop", Line: "Stop orders for a while, or start again", Icon: "storefront", Does: doesChanges, Opens: opensQuestions,
		Shortcut: "close shop", Fields: []chatField{choice("do", "Close the shop, or open it?", "close shop", "open shop")},
		Make: fill("{do}"), may: permIn(PermStorefrontManage, areas.Storefront)},
	{ID: "page.fronts", Topic: "page", Title: "Other pages", Line: "A menu, a snacks page", Icon: "storefront", Does: doesShow, Opens: opensShow,
		Shortcut: "fronts", may: permIn(PermStorefrontManage, areas.Storefront)},

	// Orders
	{ID: "orders.open", Topic: "orders", Title: "Open orders", Line: "Waiting, being made, on the way", Icon: "orders", Does: doesShow, Opens: opensShow,
		Shortcut: "orders", may: both(permIn(PermOrders, areas.Orders), ordersOn)},
	{ID: "orders.one", Topic: "orders", Title: "One order", Line: "Find it by its number", Icon: "orders", Does: doesShow, Opens: opensQuestions,
		Shortcut: "order TK-A1B2C3", Fields: []chatField{f("ref", "Which order?", "order")},
		Make: fill("order {ref}"), may: both(permIn(PermOrders, areas.Orders), ordersOn)},
	// "ready" and "delivered" are the shop page's words (chatIntentOn), so
	// this row is the Storefront part's, as the command is.
	{ID: "orders.move", Topic: "orders", Title: "Mark ready or delivered", Line: "Move an order on", Icon: "orders", Does: doesChanges, Opens: opensQuestions,
		Shortcut: "order TK-A1B2C3 ready",
		Fields:   []chatField{f("ref", "Which order?", "order"), choice("to", "Ready, or delivered?", "ready", "delivered")},
		Make:     fill("order {ref} {to}"), may: both(permIn(PermOrders, areas.Storefront), ordersOn)},
	{ID: "orders.refund", Topic: "orders", Title: "Refund an order", Line: "Money back to the customer", Icon: "orders", Does: doesChanges, Opens: opensQuestions,
		Shortcut: "refund TK-A1B2C3", Fields: []chatField{f("ref", "Which order?", "order")},
		Make: fill("refund {ref}"), may: both(perm(PermStorefrontManage), ordersOn)},

	// Riders
	{ID: "riders.list", Topic: "riders", Title: "Your riders, and who's free", Line: "Who is out, who can go now", Icon: "rider", Does: doesShow, Opens: opensShow,
		Shortcut: "riders", may: ridersMay(PermStorefrontManage, false)},
	{ID: "riders.add", Topic: "riders", Title: "Add a rider", Line: "Name, phone, and the areas they cover", Icon: "rider", Does: doesChanges, Opens: opensQuestions,
		Shortcut: "add rider Musa 0803 123 4567 Ikeja",
		Fields: []chatField{f("name", "What's the rider's name?", "text"), f("phone", "What's {name}'s phone number?", "phone"),
			opt(f("areas", "Which areas does {name} cover? Send skip for anywhere.", "zone"))},
		Make: fill("add rider {name} {phone}[ {areas}]"), may: ridersMay(PermStorefrontManage, false)},
	{ID: "riders.give", Topic: "riders", Title: "Give an order to a rider", Line: "Pick the order, then the rider", Icon: "rider", Does: doesChanges, Opens: opensQuestions,
		Shortcut: "order TK-A1B2C3 to Musa",
		Fields:   []chatField{f("ref", "Which order?", "order"), f("rider", "Which rider?", "rider")},
		Make:     fill("order {ref} to {rider}"), may: ridersMay(PermOrders, true)},
	{ID: "riders.today", Topic: "riders", Title: "Today's deliveries", Line: "Out, done, and waiting", Icon: "rider", Does: doesShow, Opens: opensShow,
		Shortcut: "deliveries", may: ridersMay(PermOrders, true)},
	{ID: "riders.back", Topic: "riders", Title: "Take an order back", Line: "From a rider who can't go", Icon: "rider", Does: doesChanges, Opens: opensQuestions,
		Shortcut: "release TK-A1B2C3", Fields: []chatField{f("ref", "Which order?", "order")},
		Make: fill("release {ref}"), may: ridersMay(PermOrders, true)},
	{ID: "riders.auto", Topic: "riders", Title: "Offer every order to riders", Line: "Riders take them as they come", Icon: "rider", Does: doesAsks, Opens: opensQuestions,
		Shortcut: "turn on deliveries", Make: fill("turn on deliveries"), may: ridersMay(PermStorefrontManage, true)},

	// Bookings
	{ID: "bookings.today", Topic: "bookings", Title: "Today's bookings", Line: "Who is coming, and when", Icon: "bookings", Does: doesShow, Opens: opensShow,
		Shortcut: "bookings", may: both(permIn(PermStorefrontManage, areas.Storefront), ordersOn)},
	{ID: "bookings.move", Topic: "bookings", Title: "Move a booking", Line: "A new day or time", Icon: "bookings", Does: doesAsks, Opens: opensQuestions,
		Shortcut: "move TK-A1B2C3 to Sat 11:00",
		Fields:   []chatField{f("ref", "Which booking?", "booking"), f("when", "To when? Like Sat 11:00.", "text")},
		Make:     fill("move {ref} to {when}"), may: both(permIn(PermStorefrontManage, areas.Storefront), ordersOn)},
	{ID: "bookings.cancel", Topic: "bookings", Title: "Cancel a booking", Line: "The customer is told", Icon: "bookings", Does: doesAsks, Opens: opensQuestions,
		Shortcut: "booking TK-A1B2C3 cancel", Fields: []chatField{f("ref", "Which booking?", "booking")},
		Make: fill("booking {ref} cancel"), may: both(permIn(PermStorefrontManage, areas.Storefront), ordersOn)},
	{ID: "bookings.off", Topic: "bookings", Title: "Take a day off", Line: "No bookings that day", Icon: "bookings", Does: doesAsks, Opens: opensQuestions,
		Shortcut: "off 25 Dec", Fields: []chatField{f("day", "Which day? Like 25 Dec.", "text")},
		Make: fill("off {day}"), may: both(permIn(PermStorefrontManage, areas.Storefront), ordersOn)},

	// Suppliers
	{ID: "buying.list", Topic: "buying", Title: "Your suppliers", Line: "Who you buy from", Icon: "purchases", Does: doesShow, Opens: opensShow,
		Shortcut: "suppliers", may: both(perm(PermStorefrontManage), purchasesOn)},
	{ID: "buying.order", Topic: "buying", Title: "Order from a supplier", Line: "What you need, sent to them", Icon: "purchases", Does: doesAsks, Opens: opensQuestions,
		Shortcut: "order from Dangote", Fields: []chatField{f("who", "From which supplier?", "text")},
		Make: fill("order from {who}"), may: both(perm(PermStorefrontManage), purchasesOn)},
	{ID: "buying.got", Topic: "buying", Title: "Goods received", Line: "Counted into your stock", Icon: "purchases", Does: doesChanges, Opens: opensQuestions,
		Shortcut: "received 50 cartons of indomie", Fields: []chatField{f("what", "What came? Say how many, like 50 cartons of Indomie.", "text")},
		Make: fill("received {what}"), may: both(perm(PermStorefrontManage), purchasesOn)},
	{ID: "buying.pay", Topic: "buying", Title: "Pay a supplier", Line: "Against their bill", Icon: "purchases", Does: doesChanges, Opens: opensQuestions,
		Shortcut: "paid Dangote 425,000 transfer",
		Fields:   []chatField{f("who", "Which supplier?", "text"), f("amount", "How much?", "money"), choice("how", "How did you pay?", "cash", "transfer")},
		Make:     fill("paid {who} {amount} {how}"), may: both(perm(PermStorefrontManage), purchasesOn)},
	{ID: "buying.add", Topic: "buying", Title: "Add a supplier", Line: "Name, phone, what they sell", Icon: "purchases", Does: doesChanges, Opens: opensQuestions,
		Shortcut: "add supplier Dangote Depot 0803 123 4567 for rice",
		Fields: []chatField{f("name", "What's the supplier called?", "text"), f("phone", "What's their phone number?", "phone"),
			opt(f("for", "What do you buy from them? Send skip if many things.", "text"))},
		Make: fill("add supplier {name} {phone}[ for {for}]"), may: both(perm(PermStorefrontManage), purchasesOn)},
	{ID: "buying.deliver", Topic: "buying", Title: "Deliver to a saved place", Line: "Where one order goes, like @warehouse", Icon: "purchases", Does: doesChanges, Opens: opensQuestions,
		Shortcut: "deliver P-7QX4KD to @warehouse",
		Fields:   []chatField{f("ref", "Which order? Its number, like P-7QX4KD.", "text"), f("place", "Where to? A saved place's name, like warehouse.", "saved_place")},
		Make:     atNames(fill("deliver {ref} to @{place}"), "place"), may: both(perm(PermStorefrontManage), purchasesOn, stockOn)},
	{ID: "buying.bills", Topic: "buying", Title: "Your bills", Line: "What you owe suppliers", Icon: "bills", Does: doesShow, Opens: opensPage,
		Shortcut: "/bills", Tool: "bills", Page: "/bills", may: perm(PermInvoiceRead)},

	// Money
	{ID: "money.spent", Topic: "money", Title: "Record money spent", Line: "Diesel, rent, transport", Icon: "money", Does: doesAsks, Opens: opensQuestions,
		Shortcut: "spent 12,000 on diesel",
		Fields:   []chatField{f("amount", "How much did you spend?", "money"), f("what", "On what?", "text")},
		Make:     fill("spent {amount} on {what}"), may: permIn(PermInvoiceManage, areas.Money)},
	{ID: "money.invoice", Topic: "money", Title: "Send an invoice", Line: "To a customer, from the chat", Icon: "invoices", Does: doesAsks, Opens: opensQuestions,
		Shortcut: "invoice Ada 25,000 for 5 bags of rice",
		Fields:   []chatField{f("who", "Who is it for? A name, or a saved person like @acme.", "person"), f("amount", "How much?", "money"), f("for", "For what?", "text")},
		Make:     fill("invoice {who} {amount} for {for}"), may: both(permIn(PermInvoiceManage, areas.Money), invoicesOn)},
	{ID: "money.cash", Topic: "money", Title: "Cash in or out", Line: "The till, the safe", Icon: "money", Does: doesAsks, Opens: opensQuestions,
		Shortcut: "cash in 20,000 till",
		Fields:   []chatField{choice("dir", "Cash in, or cash out?", "cash in", "cash out"), f("amount", "How much?", "money"), opt(f("what", "What for? Send skip if nothing.", "text"))},
		Make:     fill("{dir} {amount}[ {what}]"), may: permIn(PermInvoiceManage, areas.Money)},
	{ID: "money.week", Topic: "money", Title: "Your money", Line: "In, out, and what is left", Icon: "money", Does: doesShow, Opens: opensPage,
		Shortcut: "/money", Tool: "money", Page: "/money", may: permIn(PermInvoiceRead, areas.Money)},
	{ID: "money.invoices", Topic: "money", Title: "Your invoices", Line: "Sent, paid, and late", Icon: "invoices", Does: doesShow, Opens: opensPage,
		Shortcut: "/invoices", Tool: "invoices", Page: "/invoices", may: permIn(PermInvoiceRead, areas.Money)},

	// Who owes you
	{ID: "owe.list", Topic: "owe", Title: "Who owes you", Line: "Everyone, biggest first", Icon: "customers", Does: doesShow, Opens: opensShow,
		Shortcut: "who owes me", may: both(permIn(PermInvoiceManage, areas.Money), invoicesOn)},
	{ID: "owe.add", Topic: "owe", Title: "Someone owes you", Line: "Write it in the book", Icon: "customers", Does: doesAsks, Opens: opensQuestions,
		Shortcut: "Ada owes 5,000",
		Fields:   []chatField{f("who", "Who owes you?", "text"), f("amount", "How much?", "money"), opt(f("for", "For what? Send skip if nothing.", "text"))},
		Make:     fill("{who} owes {amount}[ for {for}]"), may: both(permIn(PermInvoiceManage, areas.Money), invoicesOn)},
	{ID: "owe.paid", Topic: "owe", Title: "Someone paid", Line: "Counted against what they owe", Icon: "customers", Does: doesChanges, Opens: opensQuestions,
		Shortcut: "Ada paid 2,000 cash",
		Fields:   []chatField{f("who", "Who paid?", "text"), f("amount", "How much?", "money"), choice("how", "Cash, or transfer?", "cash", "transfer")},
		Make:     fill("{who} paid {amount} {how}"), may: both(permIn(PermInvoiceManage, areas.Money), invoicesOn)},
	{ID: "owe.send", Topic: "owe", Title: "Send a customer their balance", Line: "A friendly reminder", Icon: "customers", Does: doesAsks, Opens: opensQuestions,
		Shortcut: "send Ada her balance", Fields: []chatField{f("who", "Which customer?", "text")},
		Make: fill("send {who} their balance"), may: both(permIn(PermInvoiceManage, areas.Money), invoicesOn)},

	// Staff and tasks
	{ID: "staff.mine", Topic: "staff", Title: "Your tasks", Line: "What is on you today", Icon: "tasks", Does: doesShow, Opens: opensShow,
		Shortcut: "my tasks", may: anyone},
	{ID: "staff.give", Topic: "staff", Title: "Give someone a task", Line: "With a time it should be done by", Icon: "tasks", Does: doesAsks, Opens: opensQuestions,
		Shortcut: "@Tunde count the float by 7am",
		Fields:   []chatField{f("who", "Who is it for? Their name as on the team.", "text"), f("what", "What should they do, and by when? Like count the float by 7am.", "text")},
		Make:     fill("@{who} {what}"), may: perm(PermTaskGive)},
	{ID: "staff.approvals", Topic: "staff", Title: "Waiting for your approval", Line: "Cash, discounts, payments", Icon: "approvals", Does: doesShow, Opens: opensShow,
		Shortcut: "approvals", may: perm(PermApprovalDecide)},

	// Your lists
	{ID: "lists.all", Topic: "lists", Title: "Your lists", Line: "Customers, members, anything you keep", Icon: "tables", Does: doesShow, Opens: opensShow,
		Shortcut: "tables", may: perm(PermTableRead)},
	{ID: "lists.find", Topic: "lists", Title: "Look something up", Line: "In one of your lists", Icon: "tables", Does: doesShow, Opens: opensQuestions,
		Shortcut: "find customers Ada",
		Fields:   []chatField{f("list", "In which list?", "text"), f("words", "What should I look for?", "text")},
		Make:     fill("find {list} {words}"), may: perm(PermTableRead)},
	{ID: "lists.add", Topic: "lists", Title: "Add to a list", Line: "One new entry", Icon: "tables", Does: doesAsks, Opens: opensQuestions,
		Shortcut: "add to customers", Fields: []chatField{f("list", "To which list?", "text")},
		Make: fill("add to {list}"), may: perm(PermTableEdit)},
	{ID: "lists.new", Topic: "lists", Title: "Make a new list", Line: "Anything you want to keep", Icon: "tables", Does: doesAsks, Opens: opensShow,
		Shortcut: "new table", may: perm(PermTableEdit)},

	// Automations
	{ID: "auto.status", Topic: "auto", Title: "What ran today", Line: "And anything that failed", Icon: "workflows", Does: doesShow, Opens: opensShow,
		Shortcut: "status", may: anyone},
	{ID: "auto.build", Topic: "auto", Title: "Automate something new", Line: "Say it, Taskiem sets it up", Icon: "workflows", Does: doesAsks, Opens: opensQuestions,
		Shortcut: "build every Friday text my customers who owe me",
		Fields:   []chatField{f("goal", "What should happen, and when? Like every Friday text my customers who owe me.", "text")},
		Make:     fill("build {goal}"), may: both(perm(PermWorkflowEdit), aiOn)},
	{ID: "auto.run", Topic: "auto", Title: "Run an automation", Line: "One you already have", Icon: "workflows", Does: doesAsks, Opens: opensQuestions,
		Shortcut: "run Salary reminders", Fields: []chatField{f("name", "Which one? Its name.", "text")},
		Make: fill("run {name}"), may: perm(PermRunStart)},

	// Saved things
	{ID: "saved.all", Topic: "saved", Title: "Your saved things", Line: "Accounts, addresses, notes", Icon: "saved", Does: doesShow, Opens: opensPage,
		Page: "/settings/saved", may: anyone},
	{ID: "saved.add", Topic: "saved", Title: "Save something", Line: "Call it up later by its name", Icon: "saved", Does: doesAsks, Opens: opensQuestions,
		Shortcut: "save mum: GTBank 0123456789",
		Fields:   []chatField{f("name", "What name should it have? Like mum.", "text"), f("what", "What should I keep? Like GTBank 0123456789.", "text")},
		Make:     fill("save {name}: {what}"), may: anyone},
	// What saved things do in the chat (saved objects S2, S3).
	{ID: "saved.ask", Topic: "saved", Title: "Ask about a saved thing", Line: "A detail, like @mum's phone", Icon: "saved", Does: doesShow, Opens: opensQuestions,
		Shortcut: "what's @mum's phone?",
		Fields: []chatField{f("name", "Which saved thing? Its name, like mum.", "saved"),
			opt(f("what", "What do you want to know? Like phone, or the insurance date. Send skip for all of it.", "text"))},
		Make: atNames(fill("what is @{name}[ {what}]"), "name"), may: anyone},
	{ID: "saved.remind", Topic: "saved", Title: "Remind me of a date", Line: "A saved date, told on its day", Icon: "saved", Does: doesChanges, Opens: opensQuestions,
		Shortcut: "remind me when @car insurance is due",
		Fields: []chatField{f("name", "Which saved thing? Its name, like car.", "saved"),
			opt(f("what", "Which of its dates? Like insurance or birthday. Send skip if it has one.", "text"))},
		Make: atNames(fill("remind me when @{name}[ {what}] is due"), "name"), may: anyone},
	{ID: "saved.reminders", Topic: "saved", Title: "Your reminders", Line: "The dates you asked to be told of", Icon: "saved", Does: doesShow, Opens: opensShow,
		Shortcut: "my reminders", may: anyone},
	{ID: "saved.share", Topic: "saved", Title: "Share a saved thing", Line: "With a linked business or a customer", Icon: "saved", Does: doesAsks, Opens: opensQuestions,
		Shortcut: "share @our-account with Ada's Kitchen",
		Fields: []chatField{f("name", "Which saved thing? Its name, like our-account.", "saved"),
			f("to", "Who with? A linked business or a customer, by name.", "text")},
		Make: atNames(fill("share @{name} with {to}"), "name"), may: inBusiness},
	{ID: "saved.import", Topic: "saved", Title: "Import from a file", Line: "A spreadsheet of accounts, people, places", Icon: "saved", Does: doesChanges, Opens: opensPage,
		Page: "/settings/saved", may: both(perm(PermSavedManage), inBusiness)},

	// School
	{ID: "school.students", Topic: "school", Title: "Students", Line: "By class, with what they owe", Icon: "school", Does: doesShow, Opens: opensPage,
		Page: "/schools", may: permIn(PermSchoolRead, areas.Schools)},
	{ID: "school.debtors", Topic: "school", Title: "Who owes, by how late", Line: "With reminders to send", Icon: "school", Does: doesShow, Opens: opensPage,
		Page: "/schools/debtors", may: permIn(PermSchoolRead, areas.Schools)},
	{ID: "school.pay", Topic: "school", Title: "Record a payment", Line: "Cash or transfer at the desk", Icon: "school", Does: doesChanges, Opens: opensPage,
		Page: "/schools/payments", may: permIn(PermSchoolManage, areas.Schools)},
	{ID: "school.clear", Topic: "school", Title: "Clearance", Line: "Who may sit exams", Icon: "school", Does: doesShow, Opens: opensPage,
		Page: "/schools/clearance", may: permIn(PermSchoolRead, areas.Schools)},

	// Your account
	{ID: "account.switch", Topic: "account", Title: "Switch business", Line: "Work in another one", Icon: "you", Does: doesChanges, Opens: opensShow,
		Shortcut: "switch", may: func(_ *Server, c *chat, _ areas.State) bool { return len(c.tenants) > 1 }},
	{ID: "account.lang", Topic: "account", Title: "Language", Line: "The language I answer in", Icon: "you", Does: doesChanges, Opens: opensShow,
		Shortcut: "language", may: func(_ *Server, c *chat, _ areas.State) bool { return len(c.lang.On) > 1 }},
	{ID: "account.signin", Topic: "account", Title: "Sign in on the web", Line: "A link for your computer", Icon: "you", Does: doesShow, Opens: opensShow,
		Shortcut: "sign in", may: func(s *Server, _ *chat, _ areas.State) bool { return s.PublicURL != "" }},
	{ID: "account.plan", Topic: "account", Title: "Your plan", Line: "What you pay, what you get", Icon: "you", Does: doesShow, Opens: opensShow,
		Shortcut: "plan", may: anyone},
}

// word is a catalogue word in a language: its message ("menu.<id>") where
// the language's catalogue has it, else the English written here, which is
// the source (en.json holds the same words for translators; a test keeps
// them equal).
func word(l lang.Tag, id, english string) string {
	if l == "" || l == lang.EN {
		return english
	}
	if t := tr(l, id); t != id && t != "" {
		return t
	}
	return english
}

// Ids of the catalogue's words.
func topicWordID(t chatTopic) string             { return "menu.topic." + t.ID }
func titleWordID(a chatAction) string            { return "menu." + a.ID }
func lineWordID(a chatAction) string             { return "menu." + a.ID + ".line" }
func askWordID(a chatAction, f chatField) string { return "menu." + a.ID + ".ask." + f.Key }

// chatActionByID finds an action.
func chatActionByID(id string) (chatAction, bool) {
	for _, a := range chatActions {
		if a.ID == id {
			return a, true
		}
	}
	return chatAction{}, false
}

func chatTopicByID(id string) (chatTopic, bool) {
	for _, t := range chatTopics {
		if t.ID == id {
			return t, true
		}
	}
	return chatTopic{}, false
}

// topicHints are words a message about a topic tends to have (§8.3),
// English and Pidgin, matched as the start of a word ("rider" finds
// "riders"); those with a space anywhere in the message. Used when no model
// can read the message.
var topicHints = map[string][]string{
	"sales":    {"sale", "sold", "sell", "selling", "customer bought", "customer buy"},
	"stock":    {"stock", "carton", "crate", "shelf", "finish", "remain", "spoil", "expire", "inventory", "count"},
	"products": {"product", "price", "service", "item", "photo"},
	"page":     {"page", "website", "qr", "opening", "close shop", "open shop", "hours"},
	"orders":   {"order", "refund", "delivered"},
	"riders":   {"rider", "dispatch", "delivery", "deliver", "okada", "bike", "keke", "motorcycle"},
	"bookings": {"booking", "appointment", "slot", "day off"},
	"buying":   {"supplier", "restock", "depot", "wholesale", "distributor", "purchase", "received"},
	"money":    {"money", "spent", "spend", "expense", "cash", "transfer", "invoice", "diesel", "fuel", "rent"},
	"owe":      {"owe", "owing", "debt", "debtor", "credit", "balance", "pay me", "dey owe"},
	"staff":    {"staff", "task", "worker", "team", "approv", "employee"},
	"lists":    {"list", "table", "record", "spreadsheet"},
	"auto":     {"automat", "workflow", "remind", "every week", "every day", "every month", "every friday"},
	"saved":    {"save", "saved", "remember"},
	"school":   {"student", "school", "fees", "parent", "pupil", "term", "clearance"},
	"account":  {"language", "password", "login", "sign in", "subscription", "my plan"},
}

var hintWordRe = regexp.MustCompile(`[\p{L}]+`)

// topicByWords is the one topic a message's words point to, among those
// the person can use: the most hints, and no tie.
func (s *Server) topicByWords(ctx context.Context, c *chat, text string) (chatTopic, bool) {
	low := strings.ToLower(text)
	words := hintWordRe.FindAllString(low, -1)
	best, score, tie := chatTopic{}, 0, false
	for _, t := range s.topicsFor(ctx, c) {
		n := 0
		for _, h := range topicHints[t.ID] {
			if strings.Contains(h, " ") {
				if strings.Contains(low, h) {
					n++
				}
				continue
			}
			for _, w := range words {
				if strings.HasPrefix(w, h) {
					n++
					break
				}
			}
		}
		switch {
		case n > score:
			best, score, tie = t, n, false
		case n == score && n > 0:
			tie = true
		}
	}
	return best, score > 0 && !tie
}

// chatTopicNamed finds a topic by one of its words ("riders", "help stock").
func chatTopicNamed(word string) (chatTopic, bool) {
	word = strings.ToLower(strings.TrimSpace(word))
	for _, t := range chatTopics {
		if word == strings.ToLower(t.Name) {
			return t, true
		}
		for _, w := range t.Words {
			if word == w {
				return t, true
			}
		}
	}
	return chatTopic{}, false
}

// mayAct reports whether the person may use an action here.
func (s *Server) mayAct(c *chat, on areas.State, a chatAction) bool {
	return c.p != nil && (a.may == nil || a.may(s, c, on))
}

// actionsOf are a topic's actions the person may use, in order.
func (s *Server) actionsOf(ctx context.Context, c *chat, topic string) []chatAction {
	on := s.chatAreas(ctx, c)
	var out []chatAction
	for _, a := range chatActions {
		if a.Topic == topic && s.mayAct(c, on, a) {
			out = append(out, a)
		}
	}
	// The person's most used first, in the last thirty days (§6.1); else
	// the catalogue's order, which puts the commonest first.
	if uses := s.actionUses(ctx, c); len(uses) > 0 {
		sort.SliceStable(out, func(i, j int) bool { return uses[out[i].ID] > uses[out[j].ID] })
	}
	return out
}

// topicsFor are the topics the person can use at all.
func (s *Server) topicsFor(ctx context.Context, c *chat) []chatTopic {
	var out []chatTopic
	used := map[string]int{}
	uses := s.actionUses(ctx, c)
	for _, t := range chatTopics {
		acts := s.actionsOf(ctx, c, t.ID)
		if len(acts) > 0 {
			out = append(out, t)
		}
		for _, a := range acts {
			used[t.ID] += uses[a.ID]
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return used[out[i].ID] > used[out[j].ID] })
	return out
}

// choicesFor are the choices offered for a field (spec §7.2): the five
// most likely, read from the business's own records.
func (s *Server) choicesFor(ctx context.Context, c *chat, fl chatField) []string {
	switch fl.Kind {
	case "choice":
		return fl.Choices
	case "saved", "saved_place", "person":
		return s.savedChoices(ctx, c, fl.Kind)
	case "rider", "zone":
		if !s.Riders.Ready() {
			return nil
		}
		var list []riders.Rider
		if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
			var err error
			list, err = s.Riders.ListTx(ctx, tx, c.tenant.ID, riders.Filter{ActiveOnly: true})
			return err
		}); err != nil {
			return nil
		}
		var out []string
		seen := map[string]bool{}
		for _, r := range list {
			names := []string{r.Name}
			if fl.Kind == "zone" {
				names = r.Zones
			}
			for _, n := range names {
				if k := strings.ToLower(n); n != "" && !seen[k] {
					seen[k] = true
					out = append(out, n)
				}
			}
		}
		return out[:min(len(out), 5)]
	case "order", "booking":
		// The newest open orders, or the coming week's bookings, as "TK-A1B2C3 · Ada".
		if s.Orders == nil {
			return nil
		}
		var list []storefront.Order
		if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
			var err error
			if fl.Kind == "booking" {
				from := time.Now().In(storefront.Lagos)
				list, err = s.Orders.Bookings(ctx, tx, c.tenant.ID, from, from.AddDate(0, 0, 7))
			} else {
				list, err = s.Orders.ListOrders(ctx, tx, c.tenant.ID, "open", "order", 5)
			}
			return err
		}); err != nil {
			return nil
		}
		var out []string
		for _, o := range list {
			label := strings.ToUpper(o.Reference)
			if n := strings.Fields(o.Customer.Name); len(n) > 0 {
				label += refSep + n[0]
			}
			out = append(out, label)
			if len(out) == 5 {
				break
			}
		}
		return out
	case "product":
		// The page's products, whether or not their stock is counted.
		if !s.Stock.Ready() {
			return nil
		}
		cat, ok := s.Stock.Table.(stock.Catalogue)
		if !ok {
			return nil
		}
		rows, m, err := cat.Rows(ctx, c.tenant.ID)
		if err != nil || m.Name == "" {
			return nil
		}
		var out []string
		for _, r := range rows {
			if n, _ := r[m.Name].(string); strings.TrimSpace(n) != "" {
				out = append(out, n)
			}
			if len(out) == 5 {
				break
			}
		}
		return out
	case "item":
		if !s.Stock.Ready() {
			return nil
		}
		items, err := s.Stock.Items(ctx, c.tenant.ID)
		if err != nil {
			return nil
		}
		var out []string
		for _, it := range items {
			if it.Name != "" {
				out = append(out, it.Name)
			}
			if len(out) == 5 {
				break
			}
		}
		return out
	}
	return nil
}

// --- GET /v1/chat/actions ---

// chatFieldOut is a value an action takes, as a form shows it (M4): a
// short label (the question's first sentence), a hint (the rest), its kind
// and its fixed choices.
type chatFieldOut struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Hint     string   `json:"hint,omitempty"`
	Kind     string   `json:"kind"`
	Choices  []string `json:"choices,omitempty"`
	Optional bool     `json:"optional,omitempty"`
}

var (
	possessiveRe = regexp.MustCompile(`\{[a-z_]+\}'s`)
	doesRe       = regexp.MustCompile(`does \{[a-z_]+\}`)
	sentenceRe   = regexp.MustCompile(`[^.?!]+[.?!]?`)
)

// formLabel parts a question into a form's label and hint: its first
// sentence, and the rest less what only a chat needs ("Send skip ...");
// another value's name is a pronoun ("What's {name}'s phone number?" is
// "What's their phone number?").
func formLabel(ask string) (label, hint string) {
	ask = doesRe.ReplaceAllString(possessiveRe.ReplaceAllString(ask, "their"), "do they")
	var rest []string
	for i, sn := range sentenceRe.FindAllString(ask, -1) {
		sn = strings.TrimSpace(sn)
		switch {
		case i == 0:
			label = sn
		case sn == "" || strings.HasPrefix(sn, "Send skip") || strings.HasPrefix(sn, "Send its number"):
		default:
			rest = append(rest, sn)
		}
	}
	return label, strings.Join(rest, " ")
}

type chatActionOut struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Line     string `json:"line"`
	Icon     string `json:"icon"`
	Does     string `json:"does"`
	Opens    string `json:"opens"`
	Shortcut string `json:"shortcut,omitempty"`
	Tool     string `json:"tool,omitempty"`
	Add      bool   `json:"add,omitempty"`
	Page     string `json:"page,omitempty"`
	// Fields are what the action's form asks for (M4).
	Fields []chatFieldOut `json:"fields,omitempty"`
}

type chatTopicOut struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Icon    string          `json:"icon"`
	Color   string          `json:"color"`
	Actions []chatActionOut `json:"actions"`
}

func actionOut(l lang.Tag, a chatAction) chatActionOut {
	out := chatActionOut{ID: a.ID, Title: word(l, titleWordID(a), a.Title), Line: word(l, lineWordID(a), a.Line), Icon: a.Icon, Does: a.Does, Opens: a.Opens,
		Shortcut: a.Shortcut, Tool: a.Tool, Add: a.Add, Page: a.Page}
	for _, f := range a.Fields {
		label, hint := formLabel(word(l, askWordID(a, f), f.Ask))
		out.Fields = append(out.Fields, chatFieldOut{Key: f.Key, Label: label, Hint: hint, Kind: f.Kind, Choices: f.Choices, Optional: f.Optional})
	}
	return out
}

// catalogueFor is the person's own catalogue: the topics and actions they
// may use, in order.
func (s *Server) actionCatalogue(ctx context.Context, c *chat) []chatTopicOut {
	out := []chatTopicOut{}
	for _, t := range s.topicsFor(ctx, c) {
		to := chatTopicOut{ID: t.ID, Name: word(c.lang.Tag, topicWordID(t), t.Name), Icon: t.Icon, Color: t.Color}
		for _, a := range s.actionsOf(ctx, c, t.ID) {
			to.Actions = append(to.Actions, actionOut(c.lang.Tag, a))
		}
		out = append(out, to)
	}
	return out
}

// chatActionsList is GET /v1/chat/actions: the signed-in person's own
// catalogue in this business, for the tools panel and the menus.
func (s *Server) chatActionsList(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil || p.TenantID == uuid.Nil {
		s.fail(w, r, errForbidden)
		return
	}
	c := &chat{ch: &webChannel{s: s, tenant: p.TenantID}, addr: webAddr(p.UserID), p: p, tenant: tenantRef{ID: p.TenantID}}
	c.lang = s.chooseLang(r.Context(), p.TenantID, c.addr, "")
	writeJSON(w, http.StatusOK, map[string]any{"topics": s.actionCatalogue(r.Context(), c)})
}

// chatActionChoices is GET /v1/chat/actions/{id}/choices: the choices its
// form offers for the fields that take one of the business's own records
// (its riders and their areas, its stock items, its open orders and coming
// bookings), the five most likely each.
func (s *Server) chatActionChoices(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if p.UserID == uuid.Nil || p.TenantID == uuid.Nil {
		s.fail(w, r, errForbidden)
		return
	}
	c := &chat{ch: &webChannel{s: s, tenant: p.TenantID}, addr: webAddr(p.UserID), p: p, tenant: tenantRef{ID: p.TenantID}}
	a, ok := chatActionByID(chi.URLParam(r, "id"))
	if !ok || !s.mayAct(c, s.chatAreas(r.Context(), c), a) {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	out := map[string][]string{}
	for _, f := range a.Fields {
		if f.Kind == "choice" {
			continue
		}
		if ch := s.choicesFor(r.Context(), c, f); len(ch) > 0 {
			out[f.Key] = ch
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"fields": out})
}
