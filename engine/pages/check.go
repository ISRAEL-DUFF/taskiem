package pages

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/israel-duff/taskiem/engine/areas"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/wdcheck"
)

// Env is what a page is checked against: the business's tables, and
// whether a name is taken by another page or by a tool.
type Env struct {
	Tables []datatables.Table
	// Taken says why name cannot be this page's ("" when it is free): another
	// page has it, or a tool's command is that word.
	Taken func(name string) string
	// AreaOn says whether a business area is on (engine/areas); nil, all
	// are. A part over Taskiem's records in an area that is off is refused.
	AreaOn func(area string) bool
	// Tool is the business's made tool by its command (G2); nil, none.
	Tool func(command string) (ToolInfo, bool)
	// Signal says why a page cannot send this signal to the runs of the
	// workflow named ("" when it can: the workflow declares it for pages);
	// nil, none can.
	Signal func(workflow, name string) string
	// Workflow says whether the business has a workflow by this name.
	Workflow func(name string) bool
}

// ReservedName reports whether a page's name is one of Taskiem's own
// command words in any language that is on (the list tools are held to,
// made tools §12), so /name always means the page.
func ReservedName(name string) bool { return wdcheck.ReservedCommand(name) }

// Check holds a parsed page against the business: its name is free and
// not Taskiem's own; every table, column and condition is the table's
// (through datatables.CheckQuery, the checked query of decision 0060);
// a sum adds up a number; a filter fits each table that uses it; and
// every expression reads only columns the table has.
//
// Nothing on a page may narrow, add up or group by a personal column
// (§8, §18): a viewer without table.read sees such a column masked, and a
// figure over it, a group by it, or a choice or dates filter on it would
// give back what the mask hides (a birth date found a day at a time).
func Check(p *Page, env Env) []Problem {
	var ps problems
	if ReservedName(p.Name) {
		ps.add("/name", "%s is a word Taskiem's chat already uses: pick another short name for the page.", p.Name)
	} else if env.Taken != nil {
		if why := env.Taken(p.Name); why != "" {
			ps.add("/name", "%s", why)
		}
	}
	filters := map[string]Filter{}
	for _, f := range p.Filters {
		filters[f.ID] = f
	}
	// A choice or dates filter is held to every table the page reads, used
	// or not (a part that uses it says so itself): its choices are values, shown at the page's top to everyone
	// who opens it, so one on a column personal in any of them is refused.
	for i, f := range p.Filters {
		if f.Type != "choice" && f.Type != "dates" || slices.ContainsFunc(p.Blocks, func(b Block) bool { return b.Source != nil && slices.Contains(b.Source.Uses, f.ID) }) {
			continue // a part that uses it says why it cannot
		}
		for _, name := range p.UsesTables() {
			t, ok := Lookup(env.Tables, name)
			if !ok {
				continue
			}
			if col, ok := columnOf(t, f.Column); ok && col.Personal {
				ps.add(fmt.Sprintf("/filters/%d/column", i), "The filter %s cannot pick by %s: it is personal, and its choices would show what is hidden.", f.word(), columnTitle(col))
				break
			}
		}
	}
	if p.Place != nil && p.Place.Area != "" {
		if _, ok := areas.Valid(p.Place.Area); !ok {
			ps.add("/place/area", "The page is a tab under %s, which is not one of Taskiem's areas.", p.Place.Area)
		}
	}
	for i, b := range p.Blocks {
		at := fmt.Sprintf("/blocks/%d", i)
		name := b.partName(i)
		switch b.Type {
		case "status":
			if b.Workflow != "" && (env.Workflow == nil || !env.Workflow(b.Workflow)) {
				ps.add(at+"/workflow", "%s shows a run of %s, which the business does not have.", name, b.Workflow)
			}
		case "buttons":
			for j, a := range b.Actions {
				checkActionEnv(&ps, fmt.Sprintf("%s/actions/%d", at, j), a, env)
			}
		}
		if b.Source == nil {
			continue
		}
		if b.Source.Tool != "" {
			checkToolSource(&ps, at, name, b, env)
			for j, a := range b.Actions {
				checkActionEnv(&ps, fmt.Sprintf("%s/actions/%d", at, j), a, env)
			}
			continue
		}
		for j, a := range b.Actions {
			checkActionEnv(&ps, fmt.Sprintf("%s/actions/%d", at, j), a, env)
		}
		var t datatables.Table
		if b.Source.Records != "" {
			var ok bool
			if t, ok = recordsChecks(&ps, at, name, b, env); !ok {
				continue
			}
		} else {
			var ok bool
			t, ok = Lookup(env.Tables, b.Source.Table)
			if !ok || t.Name != b.Source.Table {
				ps.add(at+"/source/table", "%s shows the table %s, which the business does not have.", name, b.Source.Table)
				continue
			}
		}
		if _, err := b.Source.query(t, nil); err != nil {
			ps.add(at+"/source", "%s: %s.", name, plainQueryError(err))
		}
		col := func(path, c, what string) (datatables.Column, bool) {
			found, ok := columnOf(t, c)
			if !ok {
				ps.add(path, "%s %s %s, which the table %s does not have.", name, what, c, tableTitle(t))
			}
			return found, ok
		}
		switch b.Type {
		case "figure":
			if b.Value == "sum" {
				if c, ok := col(at+"/of", b.Of, "adds up the column"); ok {
					if c.Type != "integer" && c.Type != "number" {
						ps.add(at+"/of", "%s adds up %s, which does not hold numbers.", name, columnTitle(c))
					} else if c.Personal {
						ps.add(at+"/of", "%s cannot add up %s: it is personal.", name, columnTitle(c))
					}
				}
			}
			if ch := b.Change; ch != nil {
				if c, ok := col(at+"/change/column", ch.Column, "compares by the column"); ok {
					if c.Type != "date" && c.Type != "datetime" {
						ps.add(at+"/change/column", "%s compares by %s, which does not hold dates.", name, columnTitle(c))
					} else if c.Personal {
						ps.add(at+"/change/column", "%s cannot compare by %s: it is personal.", name, columnTitle(c))
					}
				}
			}
		case "list":
			if g := b.Group; g != nil {
				if c, ok := col(at+"/group/column", g.Column, "is put in groups by"); ok {
					if c.Personal {
						ps.add(at+"/group/column", "%s cannot be put in groups by %s: it is personal.", name, columnTitle(c))
					}
					if c.Type == "json" {
						ps.add(at+"/group/column", "%s cannot be put in groups by %s.", name, columnTitle(c))
					}
				}
			}
			if b.Item != nil {
				checkReads(&ps, at+"/item/title", b.Item.Title, t, name)
				checkReads(&ps, at+"/item/sub", b.Item.Sub, t, name)
				if b.Item.State != nil {
					checkReads(&ps, at+"/item/state/text", b.Item.State.Text, t, name)
				}
			}
		case "table":
			for j, c := range b.Columns {
				found, ok := col(fmt.Sprintf("%s/columns/%d/column", at, j), c.Column, "shows the column")
				if ok && c.Naira && found.Type != "integer" && found.Type != "number" {
					ps.add(fmt.Sprintf("%s/columns/%d/naira", at, j), "%s shows %s as money, but it does not hold numbers.", name, columnTitle(found))
				}
				if ok && c.Edit && (found.Type != "boolean" || found.Personal || IsRecords(t.Name)) {
					ps.add(fmt.Sprintf("%s/columns/%d/edit", at, j), "%s can tick %s here only if it is a yes or no column of the business's own table.", name, columnTitle(found))
				}
			}
		}
		if b.Alert && b.Source.Records == "" && b.Type == "figure" && b.Value != "count" {
			ps.add(at+"/alert", "Only a figure that counts can be an alert.")
		}
		for j, a := range b.Actions {
			checkActionReads(&ps, fmt.Sprintf("%s/actions/%d", at, j), a, t, name)
		}
		if d := b.Detail; d != nil {
			checkReads(&ps, at+"/detail/title", d.Title, t, name)
			for j, l := range d.Lines {
				checkReads(&ps, fmt.Sprintf("%s/detail/lines/%d/value", at, j), l.Value, t, name)
			}
		}
		for j, u := range b.Source.Uses {
			f, ok := filters[u]
			if !ok {
				continue // Parse said so
			}
			path := fmt.Sprintf("%s/source/uses/%d", at, j)
			if why := f.fits(t); why != "" {
				ps.add(path, "%s cannot use the filter %s: %s.", name, f.word(), why)
			}
		}
	}
	return ps
}

// fits says why a filter cannot narrow a table ("" when it can). A choice
// or dates filter on a personal column cannot: its values are masked for
// a viewer without table.read, and narrowing by them would show them.
func (f Filter) fits(t datatables.Table) string {
	switch f.Type {
	case "search":
		if len(f.Columns) == 0 {
			if len(SearchColumns(f, t, true)) == 0 {
				return fmt.Sprintf("the table %s has no text column to search", tableTitle(t))
			}
			return ""
		}
		for _, c := range f.Columns {
			col, ok := columnOf(t, c)
			if !ok {
				return fmt.Sprintf("the table %s has no column %s", tableTitle(t), c)
			}
			if col.Personal && IsRecords(t.Name) {
				return fmt.Sprintf("%s is personal, and hidden on the page", columnTitle(col))
			}
			if col.Type != "text" && col.Type != "email" && col.Type != "phone" {
				return fmt.Sprintf("%s does not hold words", columnTitle(col))
			}
		}
	case "choice":
		col, ok := columnOf(t, f.Column)
		if !ok {
			return fmt.Sprintf("the table %s has no column %s", tableTitle(t), f.Column)
		}
		if col.Type != "text" {
			return fmt.Sprintf("%s does not hold words", columnTitle(col))
		}
		if col.Personal {
			return fmt.Sprintf("%s is personal, and choosing by it would show what is hidden", columnTitle(col))
		}
	case "dates":
		col, ok := columnOf(t, f.Column)
		if !ok {
			return fmt.Sprintf("the table %s has no column %s", tableTitle(t), f.Column)
		}
		if col.Type != "date" && col.Type != "datetime" {
			return fmt.Sprintf("%s does not hold dates", columnTitle(col))
		}
		if col.Personal {
			return fmt.Sprintf("%s is personal, and narrowing by its dates would show what is hidden", columnTitle(col))
		}
	case "mine":
		col, ok := columnOf(t, f.Column)
		if !ok {
			return fmt.Sprintf("the table %s has no column %s", tableTitle(t), f.Column)
		}
		if col.Type != "email" {
			return fmt.Sprintf("Mine reads a column of email addresses, and %s is not one", columnTitle(col))
		}
		if IsRecords(t.Name) {
			return fmt.Sprintf("%s is the customer's, and hidden on the page", columnTitle(col))
		}
	}
	return ""
}

func (f Filter) word() string {
	if f.Label != "" {
		return fmt.Sprintf("%q", f.Label)
	}
	return f.ID
}

// checkReads refuses an expression reading a column the table lacks.
func checkReads(ps *problems, at, v string, t datatables.Table, name string) {
	src, isExpr := strings.CutPrefix(v, "=")
	if !isExpr {
		return
	}
	paths, err := engine.Paths(src, "row")
	if err != nil {
		return // Parse said so
	}
	for _, p := range paths {
		if len(p) < 2 || p[0] != "row" {
			continue
		}
		if _, ok := columnOf(t, p[1]); !ok {
			ps.add(at, "%s shows row.%s, but the table %s has no column %s.", name, p[1], tableTitle(t), p[1])
		}
	}
}

// columnOf is a table's column by name, or one every table has.
func columnOf(t datatables.Table, name string) (datatables.Column, bool) {
	for _, c := range t.Columns {
		if c.Name == name {
			return c, true
		}
	}
	switch name {
	case "id":
		return datatables.Column{Name: "id", Title: "Number", Type: "integer"}, true
	case "created_at":
		return datatables.Column{Name: "created_at", Title: "Added", Type: "datetime"}, true
	case "updated_at":
		return datatables.Column{Name: "updated_at", Title: "Changed", Type: "datetime"}, true
	}
	return datatables.Column{}, false
}

func tableTitle(t datatables.Table) string {
	if t.Title != "" {
		return t.Title
	}
	return t.Name
}

func columnTitle(c datatables.Column) string {
	if c.Title != "" {
		return c.Title
	}
	return c.Name
}

// query checks a source's conditions and order against its table, with
// the checked query of decision 0060.
func (s *Source) query(t datatables.Table, extra []datatables.QueryFilter) (datatables.CheckedQuery, error) {
	spec := datatables.QuerySpec{Table: t.Name, Limit: 1}
	for _, c := range s.Filters {
		spec.Filters = append(spec.Filters, datatables.QueryFilter{Column: c.Column, Op: c.Op, Value: c.Value})
	}
	spec.Filters = append(spec.Filters, extra...)
	for _, o := range s.Order {
		spec.Order = append(spec.Order, datatables.QueryOrder{Column: o.Column, Desc: o.Desc})
	}
	if len(s.Filters) > MaxConditions {
		return datatables.CheckedQuery{}, fmt.Errorf("%w: at most %d conditions", datatables.ErrQuery, MaxConditions)
	}
	// The checked query bounds a question's conditions; a page's own and
	// its filters' are bounded apart (MaxConditions, MaxFilters).
	tables := []datatables.Table{t}
	if len(spec.Filters) > datatables.QueryMaxFilters {
		head := spec
		head.Filters = spec.Filters[:datatables.QueryMaxFilters]
		q, err := datatables.CheckQuery(head, tables)
		if err != nil {
			return q, err
		}
		tail := spec
		tail.Filters, tail.Order = spec.Filters[datatables.QueryMaxFilters:], nil
		q2, err := datatables.CheckQuery(tail, tables)
		if err != nil {
			return q, err
		}
		q.Filters = append(q.Filters, q2.Filters...)
		return q, nil
	}
	return datatables.CheckQuery(spec, tables)
}

func plainQueryError(err error) string {
	msg := err.Error()
	if errors.Is(err, datatables.ErrQuery) {
		msg = strings.TrimPrefix(msg, datatables.ErrQuery.Error()+": ")
	}
	r := strings.NewReplacer(`"eq"`, "is", `"neq"`, "is not", `"lt"`, "is less than", `"lte"`, "is at most", `"gt"`, "is more than",
		`"gte"`, "is at least", `"contains"`, "contains", `"is_null"`, "is empty", `"not_null"`, "is not empty")
	return r.Replace(msg)
}

// SearchColumns are the columns a search reads in a table: those it
// names, else every text column. Without personal (a viewer who may not
// read the table's personal columns), personal ones are left out, so a
// search cannot be used to guess a masked value.
func SearchColumns(f Filter, t datatables.Table, personal bool) []datatables.Column {
	var out []datatables.Column
	for _, c := range t.Columns {
		named := slices.Contains(f.Columns, c.Name)
		if len(f.Columns) == 0 {
			named = c.Type == "text"
		}
		if !named || (c.Personal && !personal) {
			continue
		}
		out = append(out, c)
	}
	return out
}
