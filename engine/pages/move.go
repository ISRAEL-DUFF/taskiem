package pages

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

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
// holds no rows), with nothing personal. A condition on a personal column
// that carries a value (a phone number a part is narrowed to) is left
// out; left says how many were, so the person exporting is told.
func ForExport(p *Page, tables []datatables.Table) (out *Page, left int) {
	var cp Page
	_ = json.Unmarshal(p.Marshal(), &cp)
	for i, b := range cp.Blocks {
		if b.Source == nil || len(b.Source.Filters) == 0 {
			continue
		}
		t, ok := datatables.FindQueryTable(tables, b.Source.Table)
		if !ok {
			continue
		}
		kept := b.Source.Filters[:0:0]
		for _, c := range b.Source.Filters {
			col, found := columnOf(t, c.Column)
			if found && col.Personal && c.Value != nil {
				left++
				continue
			}
			kept = append(kept, c)
		}
		if len(kept) == 0 {
			kept = nil
		}
		cp.Blocks[i].Source.Filters = kept
	}
	return &cp, left
}

// Missing says, in plain words, what a page names that the business does
// not have: each table it shows that the business has no table of that
// name for, and each column of a table it has that the table lacks. A
// page read from a file says these first, before the rest of Check.
func Missing(p *Page, tables []datatables.Table) []string {
	var out []string
	filters := map[string]Filter{}
	for _, f := range p.Filters {
		filters[f.ID] = f
	}
	cols := map[string][]string{} // by table, in the order named
	var names []string
	for _, b := range p.Blocks {
		s := b.Source
		if s == nil || s.Table == "" {
			continue
		}
		if !slices.Contains(names, s.Table) {
			names = append(names, s.Table)
		}
		add := func(c string) {
			if c != "" && !slices.Contains(cols[s.Table], c) {
				cols[s.Table] = append(cols[s.Table], c)
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
		t, ok := datatables.FindQueryTable(tables, name)
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
