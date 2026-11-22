package pages

import (
	"regexp"
	"strings"

	"github.com/israel-duff/taskiem/engine/datatables"
)

// FromTable is the first page Show as a page makes from a table (pages
// §10, 1): how many rows there are, how many were added this week beside
// last week, the rows as a list (grouped by a status column when the
// table has one), each opening as a card of its columns, and a search.
// The owner changes it from there; it is a draft until published.
func FromTable(t datatables.Table) *Page {
	title := tableTitle(t)
	var text, other []datatables.Column
	var status *datatables.Column
	for i, c := range t.Columns {
		if c.Personal || c.Type == "json" {
			continue
		}
		if status == nil && c.Type == "text" && statusRe.MatchString(c.Name) {
			status = &t.Columns[i]
			continue
		}
		if c.Type == "text" {
			text = append(text, c)
		} else {
			other = append(other, c)
		}
	}
	item := &Item{Title: "='#' + row.id"}
	rest := append(append([]datatables.Column{}, text...), other...)
	if len(rest) > 0 {
		item.Title = "=row." + rest[0].Name
		rest = rest[1:]
	}
	if len(rest) > 0 {
		item.Sub = "=row." + rest[0].Name
	}
	list := Block{ID: "rows", Type: "list", Title: title, Item: item,
		Source: &Source{Table: t.Name, Order: []Order{{Column: "created_at", Desc: true}}},
		Empty:  "Nothing here yet"}
	if status != nil {
		item.State = &State{Text: "=row." + status.Name, Tone: "plain", Tones: map[string]string{
			"open": "warn", "new": "warn", "waiting": "warn", "pending": "warn",
			"doing": "plain", "in progress": "plain",
			"done": "good", "closed": "good", "paid": "good", "resolved": "good",
			"late": "bad", "overdue": "bad", "failed": "bad"}}
		list.Group = &Group{Column: status.Name, Order: []string{"open", "new", "waiting", "pending", "doing", "in progress", "done", "closed", "resolved"}}
	}
	detail := &Detail{Title: item.Title}
	for _, c := range t.Columns {
		if len(detail.Lines) == MaxDetailLines-1 {
			break
		}
		if c.Type == "json" {
			continue
		}
		detail.Lines = append(detail.Lines, Line{Label: columnTitle(c), Value: "=row." + c.Name})
	}
	detail.Lines = append(detail.Lines, Line{Label: "Added", Value: "=row.created_at"})
	list.Detail = detail
	p := &Page{Page: Format, Name: PageName(t.Name), Title: title, Icon: "tables",
		Blocks: []Block{
			{ID: "all", Type: "figure", Label: "All", Value: "count", Lead: true, Source: &Source{Table: t.Name}},
			{ID: "this_week", Type: "figure", Label: "Added this week", Value: "count", Source: &Source{Table: t.Name},
				Change: &Change{Column: "created_at", Period: "week"}},
			list,
		}}
	if len(text) > 0 || status != nil {
		p.Filters = []Filter{{ID: "q", Type: "search", Label: "Search"}}
		p.Blocks[2].Source.Uses = []string{"q"}
	}
	return p
}

var statusRe = regexp.MustCompile(`(^|_)(status|state|stage)($|_)`)

// PageName is a page's short name from a table's: dashes for
// underscores, 24 letters at most, and not one of Taskiem's own words
// (then it ends -page).
func PageName(table string) string {
	n := strings.Trim(strings.ReplaceAll(table, "_", "-"), "-")
	if len(n) > 24 {
		n = strings.TrimRight(n[:24], "-")
	}
	if len(n) < 2 {
		n = "page-" + n
	}
	if ReservedName(n) {
		if len(n) > 19 {
			n = strings.TrimRight(n[:19], "-")
		}
		n += "-page"
	}
	return n
}
