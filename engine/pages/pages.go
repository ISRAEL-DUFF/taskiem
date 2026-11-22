// Package pages is the page/v1 document (decision 0072, docs/spec/pages.md,
// docs/contracts/page-v1.md): a screen a business makes for itself out of
// parts Taskiem draws, fed by its data tables. A page runs nothing.
//
// Parse reads a document and refuses what is wrong with it alone; Check
// holds it against the business's tables. Every problem is a plain
// sentence an owner can act on: the editor shows them as they are, so
// they name parts by their titles and never say "block", "schema" or
// "query".
//
// Reading a page's rows is the API's (api/pages.go): this package says
// what to read for each part (Reads) and turns the rows read into what is
// drawn (Shape), so the rules of §8 (what a viewer is shown, masked) are
// held in one place and tested without a database.
package pages

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/internal/schemacheck"
	"github.com/israel-duff/taskiem/schemas"
)

// Format is the document's format.
const Format = "page/v1"

// Limits (pages §4, §6).
const (
	MaxBlocks        = 12
	MaxFilters       = 4
	MaxFiguresInRow  = 4
	MaxTableColumns  = 6
	MaxDetailLines   = 12
	MaxConditions    = 5
	RowsShown        = 25 // rows a list or a table shows, then More
	MaxGroups        = 20
	MaxChoiceOptions = 20
)

var schema = schemacheck.New("https://schemas.taskiem.dev/page/v1.json", schemas.PageV1)

// engine reads a page's expressions: over row (a table's row) and filters
// (the page's filter values) only. outputs and item are a tool source's
// (G2) and are refused until pages have them.
var engine = expr.MustNewWithRoots("row", "filters")

var (
	nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,23}$`)
	idRe   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	roleRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
)

// Page is a page/v1 document.
type Page struct {
	Page    string   `json:"page"`
	Name    string   `json:"name"`
	Title   string   `json:"title"`
	About   string   `json:"about,omitempty"`
	Icon    string   `json:"icon,omitempty"`
	Roles   []string `json:"roles,omitempty"`
	Filters []Filter `json:"filters,omitempty"`
	Blocks  []Block  `json:"blocks"`
}

// Filter is a control at the top of a page (§6).
type Filter struct {
	ID      string   `json:"id"`
	Type    string   `json:"type"` // search, choice, dates, mine
	Label   string   `json:"label,omitempty"`
	Column  string   `json:"column,omitempty"`
	Columns []string `json:"columns,omitempty"` // search: the columns searched (else every text column)
	Options []Option `json:"options,omitempty"` // choice
}

// Option is one of a choice filter's values.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// Block is one part of a page (§4): a heading, a figure, a list or a
// table. A row's detail is part of its list or table.
type Block struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	// Heading.
	Title string `json:"title,omitempty"`
	Text  string `json:"text,omitempty"`
	// Figure.
	Label  string  `json:"label,omitempty"`
	Value  string  `json:"value,omitempty"` // count or sum
	Of     string  `json:"of,omitempty"`    // the column a sum adds up
	Naira  bool    `json:"naira,omitempty"` // the figure is an amount in kobo
	Tone   string  `json:"tone,omitempty"`
	Lead   bool    `json:"lead,omitempty"`
	Change *Change `json:"change,omitempty"`
	// List and table.
	Size    string        `json:"size,omitempty"`
	Item    *Item         `json:"item,omitempty"`
	Group   *Group        `json:"group,omitempty"`
	Columns []TableColumn `json:"columns,omitempty"`
	Detail  *Detail       `json:"detail,omitempty"`
	Empty   string        `json:"empty,omitempty"`
	// Every part but a heading.
	Source *Source `json:"source,omitempty"`
}

// Change makes a figure this period's, beside the period before.
type Change struct {
	Column string `json:"column"`
	Period string `json:"period"` // day, week, month
}

// Source is what feeds a part: a data table's rows.
type Source struct {
	Table   string      `json:"table"`
	Filters []Condition `json:"filters,omitempty"`
	Order   []Order     `json:"order,omitempty"`
	Uses    []string    `json:"uses,omitempty"`
}

// Condition is one condition every row shown meets.
type Condition struct {
	Column string `json:"column"`
	Op     string `json:"op"`
	Value  any    `json:"value,omitempty"`
}

// Order orders the rows by a column.
type Order struct {
	Column string `json:"column"`
	Desc   bool   `json:"desc,omitempty"`
}

// Item is a list's row (shapes/v1, as a made tool's list answer's).
type Item struct {
	Title string `json:"title"`
	Sub   string `json:"sub,omitempty"`
	State *State `json:"state,omitempty"`
}

// State is a row's state: its words and tone.
type State struct {
	Text  string            `json:"text"`
	Tone  string            `json:"tone,omitempty"`
	Tones map[string]string `json:"tones,omitempty"`
}

// Group heads a list's rows by a column's value.
type Group struct {
	Column string   `json:"column"`
	Order  []string `json:"order,omitempty"`
}

// TableColumn is a column a table part shows.
type TableColumn struct {
	Column string `json:"column"`
	Label  string `json:"label,omitempty"`
	Naira  bool   `json:"naira,omitempty"`
}

// Detail is one row as a card (shapes/v1).
type Detail struct {
	Title string `json:"title"`
	Lines []Line `json:"lines,omitempty"`
}

// Line is one labelled line of a detail.
type Line struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Naira bool   `json:"naira,omitempty"`
}

// Problem is one thing wrong with a page, in plain words. Path points
// into the document (/blocks/2/source/table) for the editor.
type Problem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (p Problem) String() string { return p.Path + ": " + p.Message }

type problems []Problem

func (ps *problems) add(path, format string, a ...any) {
	*ps = append(*ps, Problem{Path: path, Message: fmt.Sprintf(format, a...)})
}

// Parse reads a page document and checks what can be checked without the
// business's tables: the shape, the limits, the names, the filters a part
// uses and the expressions. A document with problems is returned as far
// as it was read (nil when it is not a page at all).
func Parse(doc []byte) (*Page, []Problem) {
	var p Page
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, []Problem{{Path: "/", Message: plainDecodeError(err)}}
	}
	var ps problems
	p.check(&ps)
	if len(ps) > 0 {
		return &p, ps
	}
	// The schema is the contract's own words; what passes the checks
	// above and fails it is reported as it says, so the two never drift
	// apart unnoticed (pages_test.go reads both on every row).
	for _, msg := range schema.Validate(doc) {
		path, rest, _ := strings.Cut(msg, ": ")
		ps.add(path, "This page does not match the page format here: %s.", rest)
	}
	return &p, ps
}

func plainDecodeError(err error) string {
	msg := err.Error()
	if f, ok := strings.CutPrefix(msg, "json: unknown field "); ok {
		return "The page has something Taskiem does not know: " + f + "."
	}
	return "This is not a page Taskiem can read: " + msg + "."
}

// partName names a part for a sentence: its title or label, else its kind
// and place ("the list (part 3)").
func (b Block) partName(i int) string {
	switch {
	case b.Title != "":
		return fmt.Sprintf("%q", b.Title)
	case b.Label != "":
		return fmt.Sprintf("%q", b.Label)
	}
	kind := map[string]string{"heading": "heading", "figure": "figure", "list": "list", "table": "table"}[b.Type]
	if kind == "" {
		kind = "part"
	}
	return fmt.Sprintf("the %s (part %d)", kind, i+1)
}

var tones = []string{"", "plain", "good", "warn", "bad"}

func (p *Page) check(ps *problems) {
	if p.Page != Format {
		ps.add("/page", "A page starts with \"page\": \"%s\".", Format)
	}
	if !nameRe.MatchString(p.Name) {
		ps.add("/name", "A page's short name is 2 to 24 small letters, digits and dashes, starting with a letter (like tickets or stock-count).")
	}
	if t := strings.TrimSpace(p.Title); t == "" || len([]rune(t)) > 60 {
		ps.add("/title", "A page needs a name of up to 60 letters.")
	}
	if len([]rune(p.About)) > 200 {
		ps.add("/about", "The line about the page is at most 200 letters.")
	}
	if p.Icon != "" && !slices.Contains(Icons, p.Icon) {
		ps.add("/icon", "Pick one of Taskiem's pictures for the page.")
	}
	for i, r := range p.Roles {
		if !roleRe.MatchString(r) || slices.Index(p.Roles, r) != i {
			ps.add(fmt.Sprintf("/roles/%d", i), "Who may open the page is a list of the business's roles, each once.")
		}
	}
	if len(p.Roles) > 20 {
		ps.add("/roles", "At most 20 roles may open a page.")
	}
	if len(p.Filters) > MaxFilters {
		ps.add("/filters", "A page has at most %d filters at its top.", MaxFilters)
	}
	filters := map[string]Filter{}
	for i, f := range p.Filters {
		at := fmt.Sprintf("/filters/%d", i)
		if !idRe.MatchString(f.ID) {
			ps.add(at+"/id", "Each filter needs a short name of small letters, digits and underscores.")
		} else if _, dup := filters[f.ID]; dup {
			ps.add(at+"/id", "Two filters are both called %s.", f.ID)
		}
		filters[f.ID] = f
		f.check(at, ps)
	}
	if len(p.Blocks) == 0 {
		ps.add("/blocks", "A page needs at least one part.")
	}
	if len(p.Blocks) > MaxBlocks {
		ps.add("/blocks", "A page has at most %d parts.", MaxBlocks)
	}
	ids := map[string]bool{}
	run, leads := 0, 0
	for i, b := range p.Blocks {
		at := fmt.Sprintf("/blocks/%d", i)
		if !idRe.MatchString(b.ID) {
			ps.add(at+"/id", "Each part needs a short name of small letters, digits and underscores.")
		} else if ids[b.ID] {
			ps.add(at+"/id", "Two parts are both called %s.", b.ID)
		}
		ids[b.ID] = true
		if b.Type == "figure" {
			run++
			if run == MaxFiguresInRow+1 {
				ps.add(at, "At most %d figures sit side by side: put another part between them.", MaxFiguresInRow)
			}
			if b.Lead {
				leads++
			}
		} else {
			run = 0
		}
		b.check(i, at, filters, ps)
	}
	if leads > 1 {
		ps.add("/blocks", "Only one figure can be the page's main figure.")
	}
}

func (f Filter) check(at string, ps *problems) {
	if len([]rune(f.Label)) > 30 {
		ps.add(at+"/label", "A filter's label is at most 30 letters.")
	}
	switch f.Type {
	case "search":
		if f.Column != "" || len(f.Options) > 0 {
			ps.add(at, "A search looks in the columns it lists (columns), or every text column.")
		}
		if len(f.Columns) > 6 {
			ps.add(at+"/columns", "A search looks in at most 6 columns.")
		}
	case "choice":
		if f.Column == "" {
			ps.add(at+"/column", "A choice filter needs the column it picks from.")
		}
		if len(f.Options) == 0 || len(f.Options) > MaxChoiceOptions {
			ps.add(at+"/options", "A choice filter needs 1 to %d choices.", MaxChoiceOptions)
		}
		seen := map[string]bool{}
		for j, o := range f.Options {
			if strings.TrimSpace(o.Value) == "" || seen[o.Value] {
				ps.add(fmt.Sprintf("%s/options/%d", at, j), "Each choice needs a value, given once.")
			}
			seen[o.Value] = true
		}
	case "dates", "mine":
		if f.Column == "" {
			ps.add(at+"/column", "This filter needs the column it reads.")
		}
		if len(f.Options) > 0 || len(f.Columns) > 0 {
			ps.add(at, "Only a choice filter has choices, and only a search several columns.")
		}
	default:
		ps.add(at+"/type", "A filter is a search, a choice, dates or Mine.")
	}
}

func (b Block) check(i int, at string, filters map[string]Filter, ps *problems) {
	name := b.partName(i)
	if !slices.Contains(tones, b.Tone) {
		ps.add(at+"/tone", "The colour of %s is plain, good, warn or bad.", name)
	}
	if b.Size != "" && b.Size != "full" && b.Size != "half" {
		ps.add(at+"/size", "%s is full width or half.", name)
	}
	needsSource := true
	switch b.Type {
	case "heading":
		needsSource = false
		if strings.TrimSpace(b.Title) == "" {
			ps.add(at+"/title", "A heading needs its words.")
		}
		if b.Source != nil || b.Item != nil || b.Columns != nil || b.Value != "" || b.Label != "" {
			ps.add(at, "A heading shows words only.")
		}
	case "figure":
		if strings.TrimSpace(b.Label) == "" || len([]rune(b.Label)) > 40 {
			ps.add(at+"/label", "A figure needs a label of up to 40 letters, like Open or Paid this week.")
		}
		switch b.Value {
		case "count":
			if b.Of != "" || b.Naira {
				ps.add(at, "%s counts rows: it adds up no column.", name)
			}
		case "sum":
			if b.Of == "" {
				ps.add(at+"/of", "%s adds up a column: say which.", name)
			}
		default:
			ps.add(at+"/value", "%s counts the rows or adds up a column.", name)
		}
		if c := b.Change; c != nil && (c.Column == "" || !slices.Contains([]string{"day", "week", "month"}, c.Period)) {
			ps.add(at+"/change", "%s compares a day, a week or a month by a date column.", name)
		}
		if b.Item != nil || b.Columns != nil || b.Detail != nil || b.Group != nil || b.Size != "" {
			ps.add(at, "A figure shows one number.")
		}
	case "list":
		if b.Item == nil || strings.TrimSpace(b.Item.Title) == "" {
			ps.add(at+"/item", "%s needs what each row's title shows.", name)
		} else {
			checkValue(at+"/item/title", b.Item.Title, ps)
			if b.Item.Sub != "" {
				checkValue(at+"/item/sub", b.Item.Sub, ps)
			}
			if s := b.Item.State; s != nil {
				checkValue(at+"/item/state/text", s.Text, ps)
				if !slices.Contains(tones, s.Tone) {
					ps.add(at+"/item/state/tone", "A row's state is plain, good, warn or bad.")
				}
				for k, v := range s.Tones {
					if !slices.Contains(tones[1:], v) {
						ps.add(at+"/item/state/tones", "The colour for %q is plain, good, warn or bad.", k)
					}
				}
			}
		}
		if g := b.Group; g != nil {
			if g.Column == "" {
				ps.add(at+"/group/column", "%s is put in groups by a column: say which.", name)
			}
			if len(g.Order) > MaxGroups {
				ps.add(at+"/group/order", "%s has at most %d groups in order.", name, MaxGroups)
			}
		}
		if b.Columns != nil || b.Value != "" {
			ps.add(at, "A list shows rows, each a title, a line under it and a state.")
		}
	case "table":
		if len(b.Columns) == 0 || len(b.Columns) > MaxTableColumns {
			ps.add(at+"/columns", "%s shows 1 to %d columns.", name, MaxTableColumns)
		}
		for j, c := range b.Columns {
			if c.Column == "" {
				ps.add(fmt.Sprintf("%s/columns/%d", at, j), "Each column of %s needs the table's column it shows.", name)
			}
		}
		if b.Item != nil || b.Group != nil || b.Value != "" {
			ps.add(at, "A table shows rows in columns.")
		}
	default:
		needsSource = false
		ps.add(at+"/type", "Taskiem does not draw a part of the kind %q: a page has headings, figures, lists and tables.", b.Type)
	}
	if b.Detail != nil {
		checkValue(at+"/detail/title", b.Detail.Title, ps)
		if len(b.Detail.Lines) > MaxDetailLines {
			ps.add(at+"/detail/lines", "A row's card has at most %d lines.", MaxDetailLines)
		}
		for j, l := range b.Detail.Lines {
			if strings.TrimSpace(l.Label) == "" {
				ps.add(fmt.Sprintf("%s/detail/lines/%d/label", at, j), "Each line of a row's card needs a label.")
			}
			checkValue(fmt.Sprintf("%s/detail/lines/%d/value", at, j), l.Value, ps)
		}
	}
	if !needsSource {
		return
	}
	s := b.Source
	if s == nil || s.Table == "" {
		ps.add(at+"/source", "%s needs the table it shows.", name)
		return
	}
	if len(s.Filters) > MaxConditions {
		ps.add(at+"/source/filters", "%s has at most %d conditions.", name, MaxConditions)
	}
	if len(s.Order) > 2 {
		ps.add(at+"/source/order", "%s is put in order by at most two columns.", name)
	}
	for j, u := range s.Uses {
		if _, ok := filters[u]; !ok {
			ps.add(fmt.Sprintf("%s/source/uses/%d", at, j), "%s uses a filter the page does not have: %s.", name, u)
		}
	}
}

// checkValue checks words or an expression (=...) over row and filters.
func checkValue(at, v string, ps *problems) {
	if strings.TrimSpace(v) == "" {
		ps.add(at, "This needs words, or a column to show.")
		return
	}
	src, isExpr := strings.CutPrefix(v, "=")
	if !isExpr {
		return
	}
	if err := engine.Check(src); err != nil {
		ps.add(at, "Taskiem cannot read %q: a page shows a row's columns (row.name) and its filters (filters.q).", v)
	}
}

// Icons are the pictures a page may carry (drawn by Taskiem).
var Icons = []string{"tables", "orders", "money", "invoices", "members", "box", "tag", "clock", "check", "bell", "star", "place", "forms", "reports", "store", "grid"}

// Marshal writes a page as its document.
func (p *Page) Marshal() []byte {
	b, _ := json.Marshal(p)
	return b
}

// UsesTables are the tables a page reads, each once, in order.
func (p *Page) UsesTables() []string {
	var out []string
	for _, b := range p.Blocks {
		if b.Source != nil && !slices.Contains(out, b.Source.Table) {
			out = append(out, b.Source.Table)
		}
	}
	return out
}
