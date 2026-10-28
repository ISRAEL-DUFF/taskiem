package main

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/billing"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/templates"
)

// The product website (docs/strategy/website.md): the pages at the site's
// root that speak to a business owner, with the docs under docs/. The
// prose is in assets/www/*.html; everything that lists the product (the
// templates, the connectors, the plans) is read from the repository so
// the site never says something the product does not do.
//
// Three things the site says depend on the world outside the repository,
// and each is left out until it is true: the WhatsApp button needs a live
// number (-whatsapp), "Start on the web" needs the app's address (-app),
// and prices show only once deploy/plans.yaml has real ones
// (placeholder_prices: false).

// wwwPage is one page of the website.
type wwwPage struct {
	Name        string // the file, without .html; "index" is the home page
	Title       string // the <title> and the page's heading
	Description string // the meta description
}

var wwwPages = []wwwPage{
	{"index", "Taskiem · Run your business from WhatsApp", "Tell Taskiem what to do in your own words: remind the customers who owe you, thank each one who pays, pay your staff after you approve. It runs every day without being reminded."},
	{"how-it-works", "How it works · Taskiem", "You talk to Taskiem the way you talk to a manager. It tells you what it understood before it does anything, and does it every day after that."},
	{"templates", "What you can automate · Taskiem", "Ready-made workflows for Nigerian businesses: reminders to debtors, receipts when customers pay, salaries after approval, stock alerts, and more."},
	{"integrations", "Works with · Taskiem", "Taskiem connects to Paystack, Moniepoint, OPay, Flutterwave, Termii, WhatsApp, Google Sheets and the other services Nigerian businesses rely on every day."},
	{"pricing", "Pricing · Taskiem", "Flat monthly prices in naira, with VAT shown separately and no charge per run."},
	{"security", "Trust · Taskiem", "Payments wait for your approval and are never sent twice, customers' details stay private, and every action is on a tamper-evident record."},
	{"tables", "Your records · Taskiem", "Keep your debtors, customers, stock and staff in Taskiem instead of a spreadsheet, and let your workflows read and write them."},
	{"storefront", "Your page · Taskiem", "A one-page shop for your business from one message: your products or services, a Pay button, a QR code, and orders that come to you in chat."},
	{"partners", "For fintechs and partners · Taskiem", "Lenders, payroll companies and banks embed Taskiem so their customers get approvals, payouts and reminders under their own brand."},
	{"about", "About · Taskiem", "Taskiem is made in Nigeria for the businesses that run on WhatsApp and a notebook."},
	{"contact", "Contact · Taskiem", "How to reach the people behind Taskiem."},
	{"404", "Not found · Taskiem", ""},
}

// adapterKinds name each connector category on its own page.
var adapterKinds = map[string]string{"payments": "Payments & Banking", "messaging": "Messaging", "identity": "Identity Checks", "open_banking": "Open Banking", "productivity": "Spreadsheets & Email", "accounting": "Accounting", "database": "Database", "storage": "File Storage"}

// The home page's picks: the templates it shows as cards, and the
// services it names, by id. A build fails if one goes missing.
var (
	featuredTemplates  = []string{"debtor-reminder-table", "payment-thank-you-sms", "daily-sales-summary", "run-payroll", "stock-reorder-alert", "failed-transfer-alert"}
	featuredConnectors = []string{"paystack", "flutterwave", "moniepoint", "opay", "interswitch", "remita", "termii", "dojah", "mono", "googlesheets", "gmail", "slack", "telegram", "mpesa", "mtnmomo"}
)

// Template groups, in page order, by the library's category.
var templateGroups = []struct{ category, title string }{
	{"collections", "Money owed to you"},
	{"customers", "Customers"},
	{"sales", "Sales"},
	{"reporting", "Reports"},
	{"alerts", "Alerts"},
	{"payroll", "Staff and salaries"},
	{"suppliers", "Suppliers"},
	{"operations", "Stock and reminders"},
	{"compliance", "Checks before onboarding"},
	{"storefront", "Your page"},
}

// Connector groups, in page order, by the manifest's category. Taskiem's
// own connectors (notify, form, storefront, invoices, schools) are described in prose, not
// listed as services.
var connectorGroups = []struct {
	title      string
	categories []string
}{
	{"Payments and banking", []string{"payments"}},
	{"Messages", []string{"messaging"}},
	{"Identity and bank checks", []string{"identity", "open_banking"}},
	{"Sheets and email", []string{"productivity"}},
	{"Accounting", []string{"accounting"}},
	{"Your data", []string{"database"}},
	{"Files", []string{"storage"}},
}

// docAliases are the connectors one docs page covers with others.
var docAliases = map[string]string{"messenger": "messenger-and-instagram", "instagram": "messenger-and-instagram"}

var ownConnectors = map[string]bool{"notify": true, "form": true, "storefront": true}

// wwwTemplate is a template as the site shows it.
type wwwTemplate struct {
	ID, Title, Summary string
	Uses               []string // the services it connects, by name
	SetupURL           string   // the app's guided set-up, or "" without an app address
}

type wwwTemplateGroup struct {
	ID, Title string
	Items     []wwwTemplate
}

type wwwConnector struct {
	ID, Name, Category, Description string
	DocURL                          string // the connector's docs page, when it has one
	Kind                            string // its category's label, on its page
	// Can are a rail's capabilities and the payment events it sends
	// (connector/v1 rule 16), and the books' (QuickBooks, Zoho Books), for
	// naming the services a template runs on.
	Can map[string]bool
	// Its page on the website (K9): integrations/<id>.
	Page     string
	OneClick bool     // connected through Taskiem's app, by signing in; else with a key
	Built    bool     // part of Taskiem: nothing to connect
	Does     []string // what a workflow can do with it: its actions' titles
	Starts   []string // what can start a workflow: its triggers' titles
	// Templates are the ready-made workflows that use it.
	Templates []wwwTemplate
}

type wwwConnectorGroup struct {
	ID, Title string
	Items     []wwwConnector
}

// wwwPlan is a plan as the pricing page shows it.
type wwwPlan struct {
	ID, Name string
	Monthly  string // "₦15,000", or "" while prices are placeholders
	Annual   string
	Includes []string // in the owner's words, from the plan's limits and features
	Contact  bool     // priced by contract
}

type wwwPricing struct {
	Placeholder bool // prices not decided: the page says so and shows none
	Plans       []wwwPlan
	Compare     []wwwCompareRow // the comparison table, one row per limit or feature
	TrialDays   int
	TrialPlan   string
	GraceDays   int
	VATPercent  float64
}

// wwwCompareRow is one row of the pricing comparison table: a cell per plan.
type wwwCompareRow struct {
	Label string
	Cells []string
}

// wwwData is what every page template sees.
type wwwData struct {
	Base, Docs, App, Email string
	WhatsApp               string // the wa.me link, or ""
	// Start is where "Start on WhatsApp" goes: the WhatsApp link, else the
	// app's sign-up, else the owner's guide.
	Start            string
	Page             wwwPage
	Connector        *wwwConnector // on a connector's page
	Featured         []wwwTemplate
	FeaturedServices []string
	Templates        []wwwTemplateGroup
	Connectors       []wwwConnectorGroup
	ConnectorCount   int // every service connector (Taskiem's own left out)
	Pricing          wwwPricing
	Prices           map[string]string // monthly price by plan id, "" while placeholders
}

var wwwFuncs = template.FuncMap{
	"join": func(s []string) string { return strings.Join(s, ", ") },
	"add":  func(a, b int) int { return a + b },
}

// buildWebsite renders the website's pages and stylesheet into files.
// docPages are the docs site's public page names, for links to them.
func buildWebsite(root string, cfg Config, docsBase string, docPages map[string]bool, files map[string][]byte) error {
	data := wwwData{Base: cfg.Base, Docs: docsBase, App: cfg.App, Email: cfg.Email}
	if cfg.WhatsApp != "" {
		n := strings.TrimLeft(strings.ReplaceAll(cfg.WhatsApp, " ", ""), "+")
		if n == "" || strings.Trim(n, "0123456789") != "" {
			return fmt.Errorf("-whatsapp %q is not a phone number", cfg.WhatsApp)
		}
		data.WhatsApp = "https://wa.me/" + n + "?text=" + url.QueryEscape("hi")
	}
	if cfg.App != "" && !strings.HasSuffix(cfg.App, "/") {
		data.App = cfg.App + "/"
	}
	switch {
	case data.WhatsApp != "":
		data.Start = data.WhatsApp
	case data.App != "":
		data.Start = data.App + "signup"
	default:
		data.Start = docsBase + "guide/index"
	}
	conns, err := loadConnectors(root)
	if err != nil {
		return err
	}
	names := map[string]string{}
	for i, c := range conns {
		names[c.ID] = c.Name
		for _, page := range []string{c.ID, strings.ReplaceAll(c.ID, "_", "-"), docAliases[c.ID]} {
			if page != "" && docPages["integrations/"+page] {
				conns[i].DocURL = docsBase + "integrations/" + page
				break
			}
		}
	}
	data.Connectors = groupConnectors(conns)
	for _, c := range conns {
		if ownConnectors[c.ID] {
			continue
		}
		data.ConnectorCount++
	}
	for _, id := range featuredConnectors {
		n, ok := names[id]
		if !ok {
			return fmt.Errorf("website: featured connector %q is not in connectors/", id)
		}
		data.FeaturedServices = append(data.FeaturedServices, n)
	}
	data.Templates, data.Featured, err = groupTemplates(data.App, names, conns)
	if err != nil {
		return err
	}
	var services []wwwConnector
	for _, c := range conns {
		if ownConnectors[c.ID] {
			continue
		}
		for _, g := range data.Templates {
			for _, w := range g.Items {
				if t, _ := templates.Default().Get(w.ID); usesConnector(t, c) {
					c.Templates = append(c.Templates, w)
				}
			}
		}
		if c.Kind = adapterKinds[c.Category]; c.Kind == "" {
			c.Kind = "Integration"
		}
		services = append(services, c)
	}
	if data.Pricing, err = loadPricing(root); err != nil {
		return err
	}
	data.Prices = map[string]string{}
	for _, p := range data.Pricing.Plans {
		data.Prices[p.ID] = p.Monthly
	}
	for _, id := range []string{"starter", "growth", "enterprise"} {
		if _, ok := data.Prices[id]; !ok {
			return fmt.Errorf("website: the pricing page needs a public plan %q in deploy/plans.yaml", id)
		}
	}
	css, err := assets.ReadFile("assets/www.css")
	if err != nil {
		return err
	}
	files["www.css"] = css
	// The typefaces, self-hosted (Plus Jakarta Sans and JetBrains Mono, SIL OFL: assets/fonts).
	fonts, err := fs.ReadDir(assets, "assets/fonts")
	if err != nil {
		return err
	}
	for _, f := range fonts {
		b, err := assets.ReadFile("assets/fonts/" + f.Name())
		if err != nil {
			return err
		}
		files["fonts/"+f.Name()] = b
	}
	// The brand's mark and share picture (docs/brand/relay, copied to assets/www/img).
	imgs, err := fs.ReadDir(assets, "assets/www/img")
	if err != nil {
		return err
	}
	for _, f := range imgs {
		b, err := assets.ReadFile("assets/www/img/" + f.Name())
		if err != nil {
			return err
		}
		files["img/"+f.Name()] = b
	}
	for _, p := range wwwPages {
		t, err := template.New("layout.html").Funcs(wwwFuncs).ParseFS(assets, "assets/www/layout.html", "assets/www/parts.html", "assets/www/"+p.Name+".html")
		if err != nil {
			return fmt.Errorf("website: %s: %w", p.Name, err)
		}
		d := data
		d.Page = p
		var buf bytes.Buffer
		if err := t.Execute(&buf, d); err != nil {
			return fmt.Errorf("website: %s: %w", p.Name, err)
		}
		files[p.Name+".html"] = buf.Bytes()
	}
	// A page for each service (K9), from its manifest and the templates
	// that use it.
	t, err := template.New("layout.html").Funcs(wwwFuncs).ParseFS(assets, "assets/www/layout.html", "assets/www/parts.html", "assets/www/connector.html")
	if err != nil {
		return fmt.Errorf("website: connector: %w", err)
	}
	for i := range services {
		c := &services[i]
		d := data
		d.Page = wwwPage{Name: c.Page, Title: c.Name + " · Taskiem", Description: firstSentence(c.Description)}
		d.Connector = c
		var buf bytes.Buffer
		if err := t.Execute(&buf, d); err != nil {
			return fmt.Errorf("website: %s: %w", c.Page, err)
		}
		files[c.Page+".html"] = buf.Bytes()
	}
	return nil
}

// usesConnector reports whether a template works with the connector:
// named, or by capabilities it has (a payment on a rail, an invoice in
// the books).
func usesConnector(t *templates.Template, c wwwConnector) bool {
	var uses []string
	for _, ref := range t.Connectors {
		if u, ok := strings.CutPrefix(ref, templates.CapabilityPrefix); ok {
			uses = append(uses, u)
			continue
		}
		if id, _, _ := strings.Cut(ref, "@"); id == c.ID {
			return true
		}
	}
	return len(uses) > 0 && c.Can != nil && railsFor(uses, []wwwConnector{c}) != ""
}

// firstSentence is a description's first sentence, for a page's meta
// description.
func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i+1]
	}
	return s
}

// loadConnectors reads every manifest under connectors/.
func loadConnectors(root string) ([]wwwConnector, error) {
	// A package may hold several (connectors/meta: Messenger and Instagram).
	paths, err := filepath.Glob(filepath.Join(root, "connectors", "*", "*.yaml"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, errors.New("website: no connector manifests under connectors/")
	}
	var out []wwwConnector
	for _, p := range paths {
		src, err := os.ReadFile(p) //nolint:gosec // the repository's own manifests
		if err != nil {
			return nil, err
		}
		m, probs := connector.Parse(src)
		if len(probs) > 0 {
			return nil, fmt.Errorf("%s: %s", p, strings.Join(probs, "; "))
		}
		c := wwwConnector{ID: m.ID, Name: m.Name, Category: m.Category, Description: strings.TrimSpace(m.Description),
			Page: "integrations/" + m.ID, OneClick: m.Auth.OAuth2 != nil, Built: m.Auth.Type == "none"}
		for _, a := range m.Actions {
			c.Does = append(c.Does, a.Title)
		}
		for _, t := range m.Triggers {
			if t.Title != "" {
				c.Starts = append(c.Starts, t.Title)
			}
		}
		slices.Sort(c.Does)
		slices.Sort(c.Starts)
		if m.KindOf() == "rail" || m.KindOf() == "product" {
			c.Can = map[string]bool{}
			for n := range m.Capabilities {
				c.Can[n] = true
			}
			for n := range m.Events {
				c.Can[n] = true
			}
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

func groupConnectors(conns []wwwConnector) []wwwConnectorGroup {
	byCat := map[string][]wwwConnector{}
	for _, c := range conns {
		if ownConnectors[c.ID] {
			continue
		}
		byCat[c.Category] = append(byCat[c.Category], c)
	}
	var out []wwwConnectorGroup
	for _, g := range connectorGroups {
		var items []wwwConnector
		for _, cat := range g.categories {
			items = append(items, byCat[cat]...)
			delete(byCat, cat)
		}
		if len(items) > 0 {
			sort.Slice(items, func(i, j int) bool { return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name) })
			out = append(out, wwwConnectorGroup{ID: g.categories[0], Title: g.title, Items: items})
		}
	}
	// Anything in a category the page does not name is still shown.
	var rest []wwwConnector
	for _, items := range byCat {
		rest = append(rest, items...)
	}
	if len(rest) > 0 {
		sort.Slice(rest, func(i, j int) bool { return strings.ToLower(rest[i].Name) < strings.ToLower(rest[j].Name) })
		out = append(out, wwwConnectorGroup{ID: "more", Title: "More", Items: rest})
	}
	return out
}

// railOrder is the order a business most likely knows the rails in.
var railOrder = []string{"paystack", "moniepoint", "iswallet", "flutterwave", "anchor"}

// railsFor names the rails that can run a template's capabilities: each
// capability, and any one of its payment events ("Paystack, Moniepoint
// or Flutterwave").
func railsFor(uses []string, conns []wwwConnector) string {
	var names []string
	rank := func(id string) int {
		if i := slices.Index(railOrder, id); i >= 0 {
			return i
		}
		return len(railOrder)
	}
	rails := slices.Clone(conns)
	sort.SliceStable(rails, func(i, j int) bool { return rank(rails[i].ID) < rank(rails[j].ID) })
	for _, c := range rails {
		if c.Can == nil {
			continue
		}
		can, events, sends := true, false, false
		for _, u := range uses {
			if strings.Contains(u, ".") { // a payment event: payout.failed
				events = true
				sends = sends || c.Can[u]
			} else {
				can = can && c.Can[u]
			}
		}
		if can && (!events || sends) {
			names = append(names, c.Name)
		}
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// groupTemplates arranges the embedded library by category, and picks
// the home page's cards.
func groupTemplates(app string, names map[string]string, conns []wwwConnector) ([]wwwTemplateGroup, []wwwTemplate, error) {
	lib := templates.Default()
	as := func(t *templates.Template) wwwTemplate {
		w := wwwTemplate{ID: t.ID, Title: t.Title, Summary: t.Summary}
		var uses []string
		for _, c := range t.Connectors {
			if u, ok := strings.CutPrefix(c, templates.CapabilityPrefix); ok {
				uses = append(uses, u)
			}
		}
		if len(uses) > 0 {
			// The rails that can, named once, as the business would.
			w.Uses = append(w.Uses, railsFor(uses, conns))
		}
		for _, c := range t.Connectors {
			if strings.HasPrefix(c, templates.CapabilityPrefix) {
				continue
			}
			id, _, _ := strings.Cut(c, "@")
			if ownConnectors[id] {
				continue
			}
			if n, ok := names[id]; ok {
				w.Uses = append(w.Uses, n)
			} else {
				w.Uses = append(w.Uses, id)
			}
		}
		if app != "" {
			w.SetupURL = app + "start/guide?template=" + url.QueryEscape(t.ID)
		}
		return w
	}
	byCat := map[string][]wwwTemplate{}
	for _, t := range lib.Offered() {
		byCat[t.Category] = append(byCat[t.Category], as(t))
	}
	var groups []wwwTemplateGroup
	for _, g := range templateGroups {
		if items := byCat[g.category]; len(items) > 0 {
			groups = append(groups, wwwTemplateGroup{ID: g.category, Title: g.title, Items: items})
			delete(byCat, g.category)
		}
	}
	var restCats []string
	for c := range byCat {
		restCats = append(restCats, c)
	}
	sort.Strings(restCats)
	for _, c := range restCats {
		groups = append(groups, wwwTemplateGroup{ID: c, Title: strings.ToUpper(c[:1]) + c[1:], Items: byCat[c]})
	}
	var featured []wwwTemplate
	for _, id := range featuredTemplates {
		t, ok := lib.Get(id)
		if !ok {
			return nil, nil, fmt.Errorf("website: featured template %q is not in the library", id)
		}
		featured = append(featured, as(t))
	}
	return groups, featured, nil
}

// loadPricing reads deploy/plans.yaml into the pricing page's terms.
func loadPricing(root string) (wwwPricing, error) {
	c, err := billing.LoadConfig(filepath.Join(root, "deploy", "plans.yaml"))
	if err != nil {
		return wwwPricing{}, err
	}
	p := wwwPricing{Placeholder: c.PlaceholderPrices, TrialDays: c.TrialDays, GraceDays: c.GraceDays, VATPercent: c.VATPercent}
	plans := slices.Clone(c.Plans)
	sort.SliceStable(plans, func(i, j int) bool { return plans[i].Sort < plans[j].Sort })
	for _, pl := range plans {
		if !pl.Public || !pl.Active {
			continue
		}
		w := wwwPlan{ID: pl.ID, Name: pl.Name}
		if !c.PlaceholderPrices {
			w.Monthly, w.Annual = naira(pl.MonthlyKobo), naira(pl.AnnualKobo)
		}
		w.Includes = includes(pl)
		p.Plans = append(p.Plans, w)
		if pl.ID == c.TrialPlan {
			p.TrialPlan = pl.Name
		}
	}
	if len(p.Plans) == 0 {
		return wwwPricing{}, errors.New("website: deploy/plans.yaml has no public plan")
	}
	p.Compare = compareRows(plans)
	return p, nil
}

// compareRows is the comparison table: the plans' limits and features side by side.
func compareRows(plans []billing.Plan) []wwwCompareRow {
	var public []billing.Plan
	for _, pl := range plans {
		if pl.Public && pl.Active {
			public = append(public, pl)
		}
	}
	count := func(key, unit, none string) func(billing.Plan) string {
		return func(pl billing.Plan) string {
			n, ok := limitInt(pl.Limits, key)
			switch {
			case !ok:
				return "—"
			case n == 0:
				return none
			}
			return group(n) + unit
		}
	}
	feature := func(key string) func(billing.Plan) string {
		return func(pl billing.Plan) string {
			if pl.Has(key) {
				return "Yes"
			}
			return "—"
		}
	}
	rows := []struct {
		label string
		cell  func(billing.Plan) string
	}{
		{"Workflows", count("max_workflows", "", "Unlimited")},
		{"Tables of records", count("max_data_tables", "", "Unlimited")},
		{"WhatsApp messages a month", count("whatsapp_templates_monthly", "", "—")},
		{"SMS a month", count("sms_monthly", "", "—")},
		{"Describe a workflow in your own words", feature(billing.FeatureAI)},
		{"Items on your page", count("storefront_items", "", "Unlimited")},
		{"Take payment when a customer orders", feature(billing.FeatureStorefrontPayOnOrder)},
		{"Run history kept", count("max_retention_days", " days", "—")},
		{"Single sign-on", feature(billing.FeatureSSO)},
		{"Embed Taskiem in your product", feature(billing.FeatureEmbedded)},
	}
	var out []wwwCompareRow
	for _, r := range rows {
		row := wwwCompareRow{Label: r.label}
		for _, pl := range public {
			row.Cells = append(row.Cells, r.cell(pl))
		}
		out = append(out, row)
	}
	return out
}

// includes puts a plan's limits and features in the owner's words.
func includes(p billing.Plan) []string {
	var out []string
	count := func(key, one, many, none string) {
		n, ok := limitInt(p.Limits, key)
		switch {
		case !ok:
		case n == 0 && none != "":
			out = append(out, none)
		case n == 1:
			out = append(out, one)
		case n > 1:
			out = append(out, strings.Replace(many, "N", group(n), 1))
		}
	}
	count("max_workflows", "1 workflow", "N workflows", "Unlimited workflows")
	count("max_data_tables", "1 table of records", "N tables of records", "Unlimited tables of records")
	count("whatsapp_templates_monthly", "1 WhatsApp message a month", "N WhatsApp messages a month", "")
	count("sms_monthly", "1 SMS a month", "N SMS a month", "")
	if p.Has(billing.FeatureAI) {
		out = append(out, "Describe a workflow in your own words")
	}
	count("storefront_items", "Your page, with 1 item", "Your page, with up to N items", "Your page, with unlimited items")
	if p.Has(billing.FeatureStorefrontPayOnOrder) {
		out = append(out, "Take payment when a customer orders")
	}
	count("max_retention_days", "Run history kept 1 day", "Run history kept N days", "")
	if p.Has(billing.FeatureSSO) {
		out = append(out, "Single sign-on for your team")
	}
	if p.Has(billing.FeatureEmbedded) {
		out = append(out, "Embed Taskiem in your own product")
	}
	return out
}

func limitInt(m map[string]any, key string) (int64, bool) {
	switch v := m[key].(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		return n, err == nil
	}
	return 0, false
}

// naira formats kobo as ₦ with thousands separators and no kobo.
func naira(kobo int64) string { return "₦" + group(kobo/100) }

func group(n int64) string {
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}
