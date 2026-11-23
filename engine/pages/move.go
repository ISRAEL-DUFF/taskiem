package pages

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/israel-duff/taskiem/engine/datatables"
)

// A page's versions, and a page taken out of one business and into
// another (docs/spec/pages.md §18): what changed from one published
// version to the next, in plain words; the document as it is written to a
// file (no rows, nothing personal); and what a page read from a file names
// that this business does not have.

// WhoWords says who may open a page with these roles: "everyone in the
// business", or "owners and support, manager".
func WhoWords(roles []string) string {
	if len(roles) == 0 {
		return "everyone in the business"
	}
	words := make([]string, len(roles))
	for i, r := range roles {
		words[i] = strings.ReplaceAll(r, "_", " ")
	}
	return "owners and " + strings.Join(words, ", ")
}

// Changes says what a published version changed on the one before it
// (prev nil: the first): parts added, taken away and changed, a new name,
// the filters, and who may open it. Each is a short sentence; none says
// "block" or "schema".
func Changes(prev, cur *Page) []string {
	if cur == nil {
		return nil
	}
	if prev == nil {
		return []string{fmt.Sprintf("First published, with %s. Opened by %s.", partsWord(len(cur.Blocks)), WhoWords(cur.Roles))}
	}
	var out []string
	if prev.Title != cur.Title {
		out = append(out, fmt.Sprintf("Renamed %q.", cur.Title))
	}
	before := map[string]Block{}
	for _, b := range prev.Blocks {
		before[b.ID] = b
	}
	var added, changed, gone []string
	for i, b := range cur.Blocks {
		old, ok := before[b.ID]
		switch {
		case !ok:
			added = append(added, b.partName(i))
		case !sameJSON(old, b):
			changed = append(changed, b.partName(i))
		}
	}
	for i, b := range prev.Blocks {
		if !slices.ContainsFunc(cur.Blocks, func(c Block) bool { return c.ID == b.ID }) {
			gone = append(gone, b.partName(i))
		}
	}
	if len(added) > 0 {
		out = append(out, "Added "+andList(added)+".")
	}
	if len(gone) > 0 {
		out = append(out, "Took away "+andList(gone)+".")
	}
	if len(changed) > 0 {
		out = append(out, "Changed "+andList(changed)+".")
	}
	if len(added) == 0 && len(gone) == 0 && len(changed) == 0 && !slices.Equal(blockIDs(prev), blockIDs(cur)) {
		out = append(out, "Moved its parts.")
	}
	if !sameJSON(prev.Filters, cur.Filters) {
		out = append(out, "Changed the filters at the top.")
	}
	if !slices.Equal(prev.Roles, cur.Roles) {
		out = append(out, "Now opened by "+WhoWords(cur.Roles)+".")
	}
	if len(out) == 0 && (prev.About != cur.About || prev.Icon != cur.Icon) {
		out = append(out, "Changed the line about it or its picture.")
	}
	if len(out) == 0 {
		out = append(out, "Published again, unchanged.")
	}
	return out
}

func partsWord(n int) string {
	if n == 1 {
		return "1 part"
	}
	return fmt.Sprintf("%d parts", n)
}

func blockIDs(p *Page) []string {
	out := make([]string, len(p.Blocks))
	for i, b := range p.Blocks {
		out[i] = b.ID
	}
	return out
}

func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func andList(words []string) string {
	if len(words) == 1 {
		return words[0]
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}

// ForExport is the page as written to a file: its document only (a page
// holds no rows), with nothing personal left in it. What could carry a
// value of a personal column is left out, and left says how much was, so
// the person exporting is told:
//
//   - a condition on a personal column that carries a value (a phone a part
//     is narrowed to); every such value of a part whose table the business
//     no longer has (its columns cannot be told apart);
//   - a choice filter on a column personal in a table the page reads (its
//     choices are values): the filter goes, and the parts stop using it;
//   - a list's group order by a personal column (the values in order), and
//     a state's colours by its words when its words read a personal column;
//   - an expression that reads a personal column and holds words of its
//     own (row.phone == '0803…' ? …): it becomes that column alone.
//
// A draft is written the same way: what it holds is left out alike.
func ForExport(p *Page, tables []datatables.Table) (out *Page, left int) {
	var cp Page
	_ = json.Unmarshal(p.Marshal(), &cp)
	// personal says whether a column is personal in t (any column, when
	// the table is not the business's: nothing tells them apart).
	personal := func(t datatables.Table, known bool, col string) bool {
		if !known {
			return true
		}
		c, ok := columnOf(t, col)
		return ok && c.Personal
	}
	// A choice filter on a column personal in a table the page reads.
	var dropped []string
	var keptFilters []Filter
	for _, f := range cp.Filters {
		drop := false
		if f.Type == "choice" {
			for _, name := range cp.UsesTables() {
				t, ok := Lookup(tables, name)
				if ok && personal(t, true, f.Column) {
					drop = true
				}
			}
		}
		if drop {
			left += len(f.Options)
			dropped = append(dropped, f.ID)
			continue
		}
		keptFilters = append(keptFilters, f)
	}
	cp.Filters = keptFilters
	for i := range cp.Blocks {
		b := &cp.Blocks[i]
		if b.Source == nil {
			continue
		}
		if len(dropped) > 0 {
			uses := b.Source.Uses[:0:0]
			for _, u := range b.Source.Uses {
				if !slices.Contains(dropped, u) {
					uses = append(uses, u)
				}
			}
			if len(uses) == 0 {
				uses = nil
			}
			b.Source.Uses = uses
		}
		t, known := Lookup(tables, b.Source.From())
		if known && b.Source.Records == "" && t.Name != b.Source.Table {
			known = false
		}
		var kept []Condition
		for _, c := range b.Source.Filters {
			if c.Value != nil && personal(t, known, c.Column) {
				left++
				continue
			}
			kept = append(kept, c)
		}
		b.Source.Filters = kept
		if g := b.Group; g != nil && len(g.Order) > 0 && personal(t, known, g.Column) {
			left += len(g.Order)
			g.Order = nil
		}
		reads := func(v string) bool {
			src, ok := strings.CutPrefix(v, "=")
			if !ok {
				return false
			}
			paths, err := engine.Paths(src, "row")
			if err != nil {
				return !known
			}
			for _, pth := range paths {
				if len(pth) > 1 && pth[0] == "row" && personal(t, known, pth[1]) {
					return true
				}
			}
			return false
		}
		clean := func(v *string) {
			src, ok := strings.CutPrefix(*v, "=")
			if !ok || !hasWordsOfItsOwn(src) || !reads(*v) {
				return
			}
			left++
			col := ""
			if paths, err := engine.Paths(src, "row"); err == nil {
				for _, pth := range paths {
					if len(pth) > 1 && pth[0] == "row" && personal(t, known, pth[1]) {
						col = pth[1]
						break
					}
				}
			}
			if col == "" {
				*v = "-"
				return
			}
			*v = "=row." + col
		}
		if it := b.Item; it != nil {
			if it.State != nil && len(it.State.Tones) > 0 && reads(it.State.Text) {
				left += len(it.State.Tones)
				it.State.Tones = nil
			}
			clean(&it.Title)
			if it.Sub != "" {
				clean(&it.Sub)
			}
			if it.State != nil {
				clean(&it.State.Text)
			}
		}
		if d := b.Detail; d != nil {
			clean(&d.Title)
			for j := range d.Lines {
				clean(&d.Lines[j].Value)
			}
		}
	}
	return &cp, left
}

// literalRe finds words of an expression's own: a quoted string.
var literalRe = regexp.MustCompile(`'[^']*'|"[^"]*"`)

// hasWordsOfItsOwn says whether an expression holds a quoted string with a
// letter or a digit in it (a separator like ' · ' carries nothing).
func hasWordsOfItsOwn(src string) bool {
	for _, lit := range literalRe.FindAllString(src, -1) {
		if strings.IndexFunc(lit, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
			return true
		}
	}
	return false
}

// Missing says, in plain words, what a page names that the business does
// not have: each table it shows that the business has no table of that
// name for, and each column of a table it has that the table lacks. A
// page read from a file says these first, before the rest of Check. A
// part over Taskiem's own records names a view Taskiem has, in an area
// the business has on (areaOn; nil, every area is), or that is said too.
func Missing(p *Page, tables []datatables.Table, areaOn func(area string) bool) []string {
	var out []string
	filters := map[string]Filter{}
	for _, f := range p.Filters {
		filters[f.ID] = f
	}
	cols := map[string][]string{} // by table, in the order named
	var names []string
	for _, b := range p.Blocks {
		s := b.Source
		from := s.From()
		if from == "" {
			continue
		}
		if !slices.Contains(names, from) {
			names = append(names, from)
		}
		add := func(c string) {
			if c != "" && !slices.Contains(cols[from], c) {
				cols[from] = append(cols[from], c)
			}
		}
		reads := func(v string) {
			src, ok := strings.CutPrefix(v, "=")
			if !ok {
				return
			}
			if paths, err := engine.Paths(src, "row"); err == nil {
				for _, pth := range paths {
					if len(pth) > 1 && pth[0] == "row" {
						add(pth[1])
					}
				}
			}
		}
		for _, c := range s.Filters {
			add(c.Column)
		}
		for _, o := range s.Order {
			add(o.Column)
		}
		for _, u := range s.Uses {
			f := filters[u]
			add(f.Column)
			for _, c := range f.Columns {
				add(c)
			}
		}
		add(b.Of)
		if b.Change != nil {
			add(b.Change.Column)
		}
		if b.Group != nil {
			add(b.Group.Column)
		}
		for _, c := range b.Columns {
			add(c.Column)
		}
		if b.Item != nil {
			reads(b.Item.Title)
			reads(b.Item.Sub)
			if b.Item.State != nil {
				reads(b.Item.State.Text)
			}
		}
		if b.Detail != nil {
			reads(b.Detail.Title)
			for _, l := range b.Detail.Lines {
				reads(l.Value)
			}
		}
	}
	for _, name := range names {
		if view, ok := strings.CutPrefix(name, RecordsPrefix); ok {
			v, known := RecordViewNamed(view)
			switch {
			case !known:
				out = append(out, fmt.Sprintf("Taskiem has no records called %s that a page can show.", view))
				continue
			case areaOn != nil && !areaOn(string(v.Area)):
				out = append(out, fmt.Sprintf("This business has %s turned off, so its %s cannot be shown.", AreaWords(v.Area), v.Words))
				continue
			}
		}
		t, ok := Lookup(tables, name)
		if !ok || t.Name != name {
			out = append(out, fmt.Sprintf("This business has no table called %s.", name))
			continue
		}
		var lack []string
		for _, c := range cols[name] {
			if _, ok := columnOf(t, c); !ok {
				lack = append(lack, c)
			}
		}
		switch len(lack) {
		case 0:
		case 1:
			out = append(out, fmt.Sprintf("The table %s has no column %s.", tableTitle(t), lack[0]))
		default:
			out = append(out, fmt.Sprintf("The table %s has no columns %s.", tableTitle(t), andList(lack)))
		}
	}
	return out
}
