package pages

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/datatables"
)

// Saved things of one kind as a source (G5, pages §5; the contract's rule
// 18): `"source": {"saved": "Generator", "coming": "next_service"}` lists
// the business's saved things of a kind it made, each with its fields, in
// the order a date of theirs comes, soonest first.
//
// A page shows them exactly as the business's saved things show to its
// people (saved objects §5.2, §8): the business's own only (never anyone's
// Mine), to people only (an API key has no saved things), each sensitive
// value as the lists show it (••3456), never opened. So a sensitive field
// is never what the things are put in order by, counted towards, grouped
// by or searched: it is marked personal in the kind's table here, and the
// checks of every other source hold it to that.

// SavedPrefix names a kind of saved things as a table ("saved:generator").
const SavedPrefix = "saved:"

// IsSaved reports whether a table name is a kind of saved things'.
func IsSaved(table string) bool { return strings.HasPrefix(table, SavedPrefix) }

// SavedKind is a kind the business made, as a page reads it: its name and
// its fields (never a value).
type SavedKind struct {
	Name   string
	Fields []SavedField
}

// SavedField is one field of a kind.
type SavedField struct {
	Key       string
	Label     string
	Type      string // the saved things' own: text, number, money, date, phone, email, choice, place, bank_account, file
	Sensitive bool
}

// SavedThing is one of the business's saved things, as everyone in it sees
// it: its values with each sensitive one masked (••3456).
type SavedThing struct {
	ID      string
	Name    string
	Title   string
	Values  map[string]string
	Created time.Time
	Updated time.Time
}

// savedColumnType is the type a field is read as on a page; "" leaves it
// off (a file is opened from the saved thing itself).
func savedColumnType(f SavedField) string {
	switch f.Type {
	case "number", "money":
		return "number"
	case "date":
		return "date"
	case "phone":
		return "phone"
	case "email":
		return "email"
	case "file":
		return ""
	}
	return "text"
}

// Table is the kind as a table: name, title, its fields (a sensitive one
// personal), and coming (when the date a part orders by comes, in words).
func (k SavedKind) Table() datatables.Table {
	t := datatables.Table{Name: SavedPrefix + strings.ToLower(k.Name), Title: k.Name}
	t.Columns = append(t.Columns,
		datatables.Column{Name: "name", Title: "Short name", Type: "text"},
		datatables.Column{Name: "title", Title: "Name", Type: "text"})
	for _, f := range k.Fields {
		typ := savedColumnType(f)
		if typ == "" || f.Key == "name" || f.Key == "title" || f.Key == "coming" {
			continue
		}
		t.Columns = append(t.Columns, datatables.Column{Name: f.Key, Title: f.Label, Type: typ, Personal: f.Sensitive || f.Type == "bank_account"})
	}
	t.Columns = append(t.Columns, datatables.Column{Name: "coming", Title: "When it comes", Type: "text"})
	return t
}

// field is the kind's field by key.
func (k SavedKind) field(key string) (SavedField, bool) {
	i := slices.IndexFunc(k.Fields, func(f SavedField) bool { return f.Key == key })
	if i < 0 {
		return SavedField{}, false
	}
	return k.Fields[i], true
}

// parseSaved is Parse's part for a source of saved things.
func (b Block) parseSaved(at, name string, ps *problems) {
	s := b.Source
	switch b.Type {
	case "list", "table":
	case "figure":
		if b.Value != "count" {
			ps.add(at+"/value", "%s counts saved things: it adds up nothing.", name)
		}
		if b.Change != nil {
			ps.add(at+"/change", "%s counts saved things: it compares no periods.", name)
		}
		if b.Alert {
			ps.add(at+"/alert", "%s counts saved things, which cannot be an alert.", name)
		}
	default:
		ps.add(at+"/source/saved", "%s shows a list, a table or how many there are of saved things, not a board or a chart.", name)
	}
	if len(s.Filters) > 0 || len(s.Order) > 0 {
		ps.add(at+"/source", "%s lists saved things in the order a date of theirs comes (coming), or by name: it has no conditions or order of its own.", name)
	}
	if s.Inputs != nil || s.Output != "" {
		ps.add(at+"/source", "%s shows saved things: only a tool is given values.", name)
	}
	if s.Within != 0 && s.Coming == "" {
		ps.add(at+"/source/within", "%s keeps the things whose date comes within some days: say which date (coming).", name)
	}
	if s.Within < 0 || s.Within > 366 {
		ps.add(at+"/source/within", "%s keeps the things whose date comes within 1 to 366 days.", name)
	}
	if s.Coming != "" && !idRe.MatchString(s.Coming) {
		ps.add(at+"/source/coming", "%s puts things in the order a date field comes: name the field by its short name.", name)
	}
	if b.Type == "table" {
		for j, c := range b.Columns {
			if c.Edit {
				ps.add(fmt.Sprintf("%s/columns/%d/edit", at, j), "%s shows saved things, which are changed where they are kept, not ticked here.", name)
			}
		}
	}
}

// savedChecks is Check's part for a source of saved things: the kind is
// the business's, and coming names one of its dates that is not sensitive.
func savedChecks(ps *problems, at, name string, b Block, env Env) (datatables.Table, bool) {
	var k SavedKind
	ok := false
	if env.SavedKind != nil {
		k, ok = env.SavedKind(b.Source.Saved)
	}
	if !ok {
		ps.add(at+"/source/saved", "%s shows saved things of the kind %s, which the business has not made.", name, b.Source.Saved)
		return datatables.Table{}, false
	}
	if c := b.Source.Coming; c != "" {
		f, has := k.field(c)
		switch {
		case !has:
			ps.add(at+"/source/coming", "%s puts %s in the order of %s, which they do not have.", name, k.Name, c)
		case f.Sensitive:
			ps.add(at+"/source/coming", "%s cannot put %s in the order of %s: it is kept private, and the order would show it.", name, k.Name, fieldTitle(f))
		case f.Type != "date":
			ps.add(at+"/source/coming", "%s puts %s in the order of %s, which is not a date.", name, k.Name, fieldTitle(f))
		}
	}
	return k.Table(), true
}

func fieldTitle(f SavedField) string {
	if f.Label != "" {
		return f.Label
	}
	return f.Key
}

// SavedRead is what a part of saved things shows, worked out for a viewer:
// the things it keeps, in its order, each with when its date comes.
type SavedRead struct {
	Rows []map[string]any
	// Tones are each row's tone by how soon its date comes ("" none).
	Tones []string
}

// ReadSaved keeps the things a part shows, for the viewer's filters (a
// search in the part's uses), in its order: by when coming's date next
// comes, soonest first (those past first, as they are late), those with no
// date after; else by name. Within keeps those whose date comes within so
// many days, or has passed. Each row carries coming in words.
func (p *Page) ReadSaved(b Block, k SavedKind, things []SavedThing, v Values) SavedRead {
	t := k.Table()
	var searches []string
	if b.Type != "figure" {
		for _, id := range b.Source.Uses {
			if f := p.filter(id); f.Type == "search" && strings.TrimSpace(v.Set[id]) != "" {
				searches = append(searches, id)
			}
		}
	}
	today := dayOf(v.Now)
	type kept struct {
		row  map[string]any
		days int
		has  bool
		name string
	}
	var keep []kept
	for _, x := range things {
		row := map[string]any{"id": x.ID, "name": x.Name, "title": x.Title}
		if !x.Created.IsZero() {
			row["created_at"] = x.Created.UTC().Format(time.RFC3339Nano)
		}
		if !x.Updated.IsZero() {
			row["updated_at"] = x.Updated.UTC().Format(time.RFC3339Nano)
		}
		for _, c := range t.Columns {
			if c.Name == "name" || c.Name == "title" || c.Name == "coming" {
				continue
			}
			if val, ok := x.Values[c.Name]; ok && val != "" {
				row[c.Name] = val
			}
		}
		ok := true
		for _, id := range searches {
			f := p.filter(id)
			want := strings.ToLower(strings.TrimSpace(v.Set[id]))
			found := false
			for _, c := range SearchColumns(f, t, false) {
				if s, _ := row[c.Name].(string); strings.Contains(strings.ToLower(s), want) {
					found = true
				}
			}
			if !found {
				ok = false
			}
		}
		if !ok {
			continue
		}
		k := kept{row: row, name: strings.ToLower(x.Title + " " + x.Name)}
		if c := b.Source.Coming; c != "" {
			if d, err := time.Parse("2006-01-02", x.Values[c]); err == nil {
				k.days, k.has = int(d.Sub(today).Hours()/24), true
			}
			if b.Source.Within > 0 && (!k.has || k.days > b.Source.Within) {
				continue
			}
		}
		keep = append(keep, k)
	}
	sort.SliceStable(keep, func(i, j int) bool {
		a, z := keep[i], keep[j]
		if b.Source.Coming != "" && a.has != z.has {
			return a.has
		}
		if b.Source.Coming != "" && a.has && a.days != z.days {
			return a.days < z.days
		}
		return a.name < z.name
	})
	out := SavedRead{}
	for _, k := range keep {
		words, tone := "", ""
		if k.has {
			words, tone = ComingWords(k.days, today)
		}
		k.row["coming"] = words
		out.Rows = append(out.Rows, k.row)
		out.Tones = append(out.Tones, tone)
	}
	return out
}

// dayOf is the day it is in Lagos, as a date at midnight UTC.
func dayOf(now time.Time) time.Time {
	l := now.In(lagos)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.UTC)
}

// ComingWords says when a date comes, days from today: "Today",
// "Tomorrow", "In 5 days", "On 3 Mar 2027" past two months; "Yesterday",
// "3 days ago" once it has passed. Its tone: bad once passed, warn within
// a week, plain after.
func ComingWords(days int, today time.Time) (string, string) {
	switch {
	case days < -1:
		return fmt.Sprintf("%d days ago", -days), "bad"
	case days == -1:
		return "Yesterday", "bad"
	case days == 0:
		return "Today", "warn"
	case days == 1:
		return "Tomorrow", "warn"
	case days <= 7:
		return fmt.Sprintf("In %d days", days), "warn"
	case days <= 60:
		return fmt.Sprintf("In %d days", days), "plain"
	}
	return "On " + today.AddDate(0, 0, days).Format("2 Jan 2006"), "plain"
}

// ShapeSaved draws the rows of a part of saved things for the viewer, as
// Shape draws a table's: each sensitive value already masked as the lists
// show it, so no expression reads it. A row's state that shows coming and
// gives no colours of its own takes its tone from how soon it comes.
func ShapeSaved(b Block, k SavedKind, read SavedRead, v Values) []Shown {
	t := k.Table()
	// The values are masked already (••3456): drawn as they are.
	for i := range t.Columns {
		t.Columns[i].Personal = false
	}
	v.Viewer.Personal = false
	shown := Shape(b, t, read.Rows, v)
	if b.Item != nil && b.Item.State != nil && strings.TrimSpace(b.Item.State.Text) == "=row.coming" && b.Item.State.Tone == "" && len(b.Item.State.Tones) == 0 {
		for i := range shown {
			if shown[i].State != nil && i < len(read.Tones) && read.Tones[i] != "" {
				shown[i].State.Tone = read.Tones[i]
			}
		}
	}
	for i := range shown {
		if i < len(read.Rows) {
			shown[i].ID, _ = read.Rows[i]["id"].(string)
		}
	}
	return shown
}

// SavedKinds are the kinds a page's parts show, each once, in order.
func (p *Page) SavedKinds() []string {
	var out []string
	for _, b := range p.Blocks {
		if b.Source != nil && b.Source.Saved != "" && !slices.ContainsFunc(out, func(s string) bool { return strings.EqualFold(s, b.Source.Saved) }) {
			out = append(out, b.Source.Saved)
		}
	}
	return out
}

// SavedRowAct is what a button pressed on a saved thing's row reads: the
// row as the part shows it (each private value already masked), and the
// filters.
func SavedRowAct(k SavedKind, raw map[string]any, v Values) map[string]any {
	t := k.Table()
	for i := range t.Columns {
		t.Columns[i].Personal = false
	}
	return map[string]any{"row": viewRow(t, raw, true), "filters": filterAct(v)}
}
