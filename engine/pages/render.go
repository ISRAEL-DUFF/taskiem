package pages

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Viewer is who opens a page, as far as what it shows them depends on it
// (pages §8): a page shows what its maker chose to those its roles let
// open it, reading with the business's connection, not the viewer's
// rights; a personal column is masked unless the viewer also holds
// table.read.
type Viewer struct {
	Email, Phone string
	// Personal: the viewer holds table.read, so personal columns show.
	Personal bool
	// Edit: the viewer holds table.edit, so a table's yes/no column marked
	// "can be changed here" is a tick they may change (§7).
	Edit bool
}

// PhoneForms are the ways a phone column may hold the viewer's number
// (Mine on a phone column, pages §6): as the account keeps it
// (+2348012345678), without its plus (a table's phone may be written
// either way), and, for a Nigerian number, as it is written at home
// (08012345678).
func PhoneForms(number string) []string {
	n := strings.TrimPrefix(strings.TrimSpace(number), "+")
	if n == "" {
		return nil
	}
	out := []string{"+" + n, n}
	if rest, ok := strings.CutPrefix(n, "234"); ok && len(rest) == 10 {
		out = append(out, "0"+rest)
	}
	return out
}

// ticks are a table row's yes/no cells that can be changed here.
func ticks(b Block, t datatables.Table, raw map[string]any) []*bool {
	var out []*bool
	some := false
	for _, c := range b.Columns {
		var v *bool
		if col, ok := columnOf(t, c.Column); c.Edit && ok && col.Type == "boolean" && !col.Personal {
			x, _ := raw[c.Column].(bool)
			v, some = &x, true
		}
		out = append(out, v)
	}
	if !some {
		return nil
	}
	return out
}

// Values are a page's filters as one viewer set them, checked.
type Values struct {
	Set    map[string]string // by filter id: the value as given
	Viewer Viewer
	Now    time.Time
}

// lagos is where a day, a week and a month start.
var lagos = func() *time.Location {
	if l, err := time.LoadLocation("Africa/Lagos"); err == nil {
		return l
	}
	return time.FixedZone("WAT", 3600)
}()

var rangeRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})\.\.(\d{4}-\d{2}-\d{2})$`)

// FilterValues checks what a viewer set against the page's filters: a
// value for a filter the page lacks, a choice it does not offer or dates
// it cannot read is a problem, said plainly; an empty one is no filter.
func (p *Page) FilterValues(set map[string]string, viewer Viewer, now time.Time) (Values, []Problem) {
	var ps problems
	out := Values{Set: map[string]string{}, Viewer: viewer, Now: now}
	for id, v := range set {
		v = strings.TrimSpace(v)
		i := slices.IndexFunc(p.Filters, func(f Filter) bool { return f.ID == id })
		if i < 0 {
			ps.add("/filters", "This page has no filter %s.", id)
			continue
		}
		if v == "" {
			continue
		}
		f := p.Filters[i]
		switch f.Type {
		case "search":
			if len([]rune(v)) > 100 {
				ps.add("/filters/"+id, "A search is at most 100 letters.")
				continue
			}
		case "choice":
			if !slices.ContainsFunc(f.Options, func(o Option) bool { return o.Value == v }) {
				ps.add("/filters/"+id, "%q is not one of this filter's choices.", v)
				continue
			}
		case "dates":
			if _, _, ok := DateRange(v, now); !ok {
				ps.add("/filters/"+id, "Dates are today, week, month, or two days as 2026-11-01..2026-11-30.")
				continue
			}
		case "mine":
			if v != "on" && v != "true" && v != "yes" {
				ps.add("/filters/"+id, "Mine is on, or left out.")
				continue
			}
			v = "on"
		}
		out.Set[id] = v
	}
	return out, ps
}

// DateRange is the days a dates filter's value covers, from (inclusive)
// to (exclusive), in Lagos: today, week (from Monday), month, or two days
// given as from..to (both included).
func DateRange(v string, now time.Time) (from, to time.Time, ok bool) {
	day := func(t time.Time) time.Time {
		t = t.In(lagos)
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, lagos)
	}
	today := day(now)
	switch v {
	case "today":
		return today, today.AddDate(0, 0, 1), true
	case "week":
		start := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
		return start, start.AddDate(0, 0, 7), true
	case "month":
		start := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, lagos)
		return start, start.AddDate(0, 1, 0), true
	}
	m := rangeRe.FindStringSubmatch(v)
	if m == nil {
		return time.Time{}, time.Time{}, false
	}
	a, err1 := time.ParseInLocation("2006-01-02", m[1], lagos)
	b, err2 := time.ParseInLocation("2006-01-02", m[2], lagos)
	if err1 != nil || err2 != nil || b.Before(a) || b.Sub(a) > 366*24*time.Hour {
		return time.Time{}, time.Time{}, false
	}
	return a, b.AddDate(0, 0, 1), true
}

// Period is this period and the one before for a figure's change.
func Period(period string, now time.Time) (from, to, prevFrom time.Time) {
	switch period {
	case "day":
		from, to, _ = DateRange("today", now)
		return from, to, from.AddDate(0, 0, -1)
	case "week":
		from, to, _ = DateRange("week", now)
		return from, to, from.AddDate(0, 0, -7)
	default:
		from, to, _ = DateRange("month", now)
		return from, to, from.AddDate(0, -1, 0)
	}
}

// Between is the read_rows conditions for a date column from (inclusive)
// to (exclusive): days for a date column, moments for a date and time.
func Between(col datatables.Column, from, to time.Time) []any {
	f := func(t time.Time) any {
		if col.Type == "date" {
			return t.Format("2006-01-02")
		}
		return t.UTC().Format(time.RFC3339)
	}
	return []any{
		map[string]any{"column": col.Name, "op": "gte", "value": f(from)},
		map[string]any{"column": col.Name, "op": "lt", "value": f(to)},
	}
}

// Read is what to read for one part: the table, read_rows' conditions and
// order. None is set when a filter can match no row (Mine for a viewer
// with no email or phone): nothing is read and the part is empty.
type Read struct {
	Table   datatables.Table
	Filters []any
	Order   []any
	None    bool
}

var likeEscape = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)

// ErrPersonalOrder refuses rows put in order by a personal column for a
// viewer who may not read it: the order alone would rank what the mask
// hides.
var ErrPersonalOrder = errors.New("the rows cannot be put in order by a personal column")

// ReadFor is what part b reads under the viewer's filter values: its own
// conditions, checked (datatables.CheckQuery), then each filter it uses.
// An order given overrides the part's (a table sorted by a press); a
// personal column is no order for a viewer without table.read.
//
// A figure stays whole: the page's filters narrow lists and tables only
// (Parse refuses uses on a figure; it is ignored here besides). A choice
// or dates filter on a personal column (Check refuses it) is left out for
// a viewer without table.read, as a search leaves such columns out.
func (p *Page) ReadFor(b Block, t datatables.Table, v Values, order *Order) (Read, error) {
	if IsRecords(t.Name) {
		// Taskiem's records' personal columns are masked for everyone.
		v.Viewer.Personal = false
	}
	q, err := b.Source.query(t, nil)
	if err != nil {
		return Read{}, err
	}
	r := Read{Table: t, Filters: q.ReadFilters()}
	if order != nil {
		col, ok := columnOf(t, order.Column)
		if !ok {
			return Read{}, fmt.Errorf("%w: the table has no column %s", datatables.ErrQuery, order.Column)
		}
		if col.Personal && !v.Viewer.Personal {
			return Read{}, ErrPersonalOrder
		}
		r.Order = []any{map[string]any{"column": order.Column, "desc": order.Desc}, map[string]any{"column": "id", "desc": true}}
	} else {
		r.Order = q.ReadOrder()
	}
	if b.Type == "figure" {
		return r, nil
	}
	for _, id := range b.Source.Uses {
		val := v.Set[id]
		if val == "" {
			continue
		}
		f := p.filter(id)
		if f.Type == "choice" || f.Type == "dates" {
			if col, ok := columnOf(t, f.Column); ok && col.Personal && !v.Viewer.Personal {
				continue
			}
		}
		switch f.Type {
		case "search":
			var anyOf []any
			for _, c := range SearchColumns(f, t, v.Viewer.Personal) {
				anyOf = append(anyOf, map[string]any{"column": c.Name, "op": "ilike", "value": "%" + likeEscape.Replace(val) + "%"})
			}
			switch len(anyOf) {
			case 0:
				r.None = true
			case 1:
				r.Filters = append(r.Filters, anyOf[0])
			default:
				r.Filters = append(r.Filters, map[string]any{"any": anyOf})
			}
		case "choice":
			r.Filters = append(r.Filters, map[string]any{"column": f.Column, "op": "ilike", "value": likeEscape.Replace(val)})
		case "dates":
			col, _ := columnOf(t, f.Column)
			from, to, _ := DateRange(val, v.Now)
			r.Filters = append(r.Filters, Between(col, from, to)...)
		case "mine":
			col, _ := columnOf(t, f.Column)
			switch {
			case col.Type == "email" && v.Viewer.Email != "":
				r.Filters = append(r.Filters, map[string]any{"column": col.Name, "op": "ilike", "value": likeEscape.Replace(v.Viewer.Email)})
			case col.Type == "phone" && v.Viewer.Phone != "":
				var anyOf []any
				for _, n := range PhoneForms(v.Viewer.Phone) {
					anyOf = append(anyOf, map[string]any{"column": col.Name, "op": "eq", "value": n})
				}
				r.Filters = append(r.Filters, map[string]any{"any": anyOf})
			default:
				r.None = true
			}
		}
	}
	return r, nil
}

// HiddenFilter says whether a filter is left off the page for a viewer:
// a choice or dates filter on a column personal in a table the page reads
// (Check refuses one; a page published before is drawn without it for a
// viewer without table.read, since its choices may name what is masked).
func (p *Page) HiddenFilter(f Filter, tables []datatables.Table, personal bool) bool {
	if personal || (f.Type != "choice" && f.Type != "dates") {
		return false
	}
	for _, name := range p.UsesTables() {
		if t, ok := Lookup(tables, name); ok {
			if col, ok := columnOf(t, f.Column); ok && col.Personal {
				return true
			}
		}
	}
	return false
}

// RankedByPersonal says whether part b's rows, read in this order (nil:
// the part's own), are ranked by a column the viewer may not read: the
// part's own order may name a personal column (its maker chose it), and
// the data API's cursor may carry the value a row was ranked by.
func RankedByPersonal(b Block, t datatables.Table, order *Order, personal bool) bool {
	if personal || b.Source == nil {
		return false
	}
	var cols []string
	if order != nil {
		cols = append(cols, order.Column)
	} else {
		for _, o := range b.Source.Order {
			cols = append(cols, o.Column)
		}
	}
	for _, c := range cols {
		if col, ok := columnOf(t, c); ok && col.Personal {
			return true
		}
	}
	return false
}

// MaxSkip bounds how far More reaches into rows ranked by a personal
// column for a viewer without table.read (a skip, not the data API's
// cursor): the rows read for the last page are at most the data API's
// 1,000.
const MaxSkip = 1000 - RowsShown

// Cursor is where a list's or a table's More picks up, as Taskiem gives
// it: opaque to the viewer, and taken back only with the order it was
// made for. After is the data API's cursor; Skip, the rows already shown,
// for rows ranked by a personal column the viewer may not read, so that
// nothing the viewer sends can carry a value to rank against.
type Cursor struct {
	Order string `json:"o,omitempty"`
	After string `json:"a,omitempty"`
	Skip  int    `json:"n,omitempty"`
}

// OrderKey names an order for a cursor: "" for the part's own.
func OrderKey(order *Order) string {
	if order == nil {
		return ""
	}
	if order.Desc {
		return order.Column + " desc"
	}
	return order.Column
}

// String is the cursor as handed to the viewer.
func (c Cursor) String() string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// ErrCursor refuses a cursor that is not one Taskiem gave for this order.
var ErrCursor = errors.New("this is not where these rows left off: open the part again")

// ReadCursor takes back a cursor given for rows in this order: ranked by
// a personal column the viewer may not read (ranked), only a skip; else
// only the data API's cursor.
func ReadCursor(s string, order *Order, ranked bool) (Cursor, error) {
	var c Cursor
	if s == "" {
		return c, nil
	}
	c, err := readCursorRaw(s)
	if err != nil || c.Order != OrderKey(order) {
		return Cursor{}, ErrCursor
	}
	if ranked {
		if c.After != "" || c.Skip <= 0 || c.Skip > MaxSkip {
			return Cursor{}, ErrCursor
		}
	} else if c.Skip != 0 || c.After == "" {
		return Cursor{}, ErrCursor
	}
	return c, nil
}

func readCursorRaw(s string) (Cursor, error) {
	var c Cursor
	if len(s) > 4096 {
		return c, ErrCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return c, ErrCursor
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&c) != nil {
		return Cursor{}, ErrCursor
	}
	return c, nil
}

func (p *Page) filter(id string) Filter {
	for _, f := range p.Filters {
		if f.ID == id {
			return f
		}
	}
	return Filter{}
}

// Shown is one row as drawn: a list's title, line and state, or a
// table's cells; its group; and its detail.
type Shown struct {
	ID     string       `json:"id"`
	Title  string       `json:"title,omitempty"`
	Sub    string       `json:"sub,omitempty"`
	State  *ShownState  `json:"state,omitempty"`
	Group  string       `json:"group,omitempty"`
	Cells  []string     `json:"cells,omitempty"`
	Detail *ShownDetail `json:"detail,omitempty"`
	// Link is the record's own screen, for a row of Taskiem's records
	// (/storefront/orders/AD-12): the web's address, which the app opens too.
	Link string `json:"link,omitempty"`
	// Actions are the row's buttons that show for it (when), by their
	// place among the part's actions (G2).
	Actions []ShownAction `json:"actions,omitempty"`
	// Ticks are a table's yes/no cells that can be changed here, as
	// true or false, by the column's place (nil for the others).
	Ticks []*bool `json:"ticks,omitempty"`
	// RunStatus is where the row's run is (the part's status), set by the
	// API: its words for every viewer, the run itself for one with run.read.
	RunStatus map[string]any `json:"run_status,omitempty"`
	// RunID and RunTo name the row's run (the part's status: run, or to),
	// worked out from the row as this viewer is shown it; never sent.
	RunID string `json:"-"`
	RunTo string `json:"-"`
}

// rowRun works out what names a row's run, for the part's status.
func (s *Shown) rowRun(b Block, act map[string]any) {
	if rs := b.RowStatus; rs != nil {
		if rs.Run != "" {
			s.RunID = Word(b, rs.Run, act)
		}
		if rs.To != "" {
			s.RunTo = Word(b, rs.To, act)
		}
	}
}

// ShownState is a row's state as drawn.
type ShownState struct {
	Text string `json:"text"`
	Tone string `json:"tone"`
}

// ShownDetail is a row's card as drawn.
type ShownDetail struct {
	Title string      `json:"title"`
	Lines []ShownLine `json:"lines"`
}

// ShownLine is one line of a row's card.
type ShownLine struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// maxShown bounds one value drawn.
const maxShown = 300

// Shape turns rows read for part b into what is drawn for this viewer:
// a personal column masked unless the viewer may read it (the mask is
// applied before any expression sees the row, so no expression can show
// what the mask hides), values written as people read them.
func Shape(b Block, t datatables.Table, rows []map[string]any, v Values) []Shown {
	if IsRecords(t.Name) {
		v.Viewer.Personal = false
	}
	out := make([]Shown, 0, len(rows))
	filters := map[string]any{}
	for k, x := range v.Set {
		filters[k] = x
	}
	for _, raw := range rows {
		row := viewRow(t, raw, v.Viewer.Personal)
		act := map[string]any{"row": row, "filters": filters}
		s := Shown{ID: shownCell(t, "id", raw["id"])}
		switch b.Type {
		case "list":
			s.Title = say(b.Item.Title, act)
			s.Sub = say(b.Item.Sub, act)
			if st := b.Item.State; st != nil {
				text := say(st.Text, act)
				if text != "" {
					tone := st.Tone
					if tt, ok := st.Tones[text]; ok {
						tone = tt
					} else if tt, ok := st.Tones[strings.ToLower(text)]; ok {
						tone = tt
					}
					if tone == "" {
						tone = "plain"
					}
					s.State = &ShownState{Text: text, Tone: tone}
				}
			}
			if b.Group != nil {
				s.Group = groupKey(row[b.Group.Column])
			}
		case "table":
			for _, c := range b.Columns {
				if c.Naira {
					s.Cells = append(s.Cells, nairaOf(row[c.Column]))
					continue
				}
				s.Cells = append(s.Cells, fmt.Sprint(row[c.Column]))
			}
			if v.Viewer.Edit && !IsRecords(t.Name) {
				s.Ticks = ticks(b, t, raw)
			}
		}
		if d := b.Detail; d != nil {
			sd := &ShownDetail{Title: say(d.Title, act), Lines: []ShownLine{}}
			for _, l := range d.Lines {
				val := say(l.Value, act)
				if l.Naira {
					val = nairaOf(val)
				}
				if val == "" {
					val = "-"
				}
				sd.Lines = append(sd.Lines, ShownLine{Label: l.Label, Value: val})
			}
			s.Detail = sd
		}
		s.Actions = ShownActions(b, act)
		s.rowRun(b, act)
		out = append(out, s)
	}
	return out
}

// viewRow is a row as a page's expressions read it: every value as words
// a person reads (dates in Lagos, Yes and No), personal columns masked
// for a viewer who may not read them.
func viewRow(t datatables.Table, raw map[string]any, personal bool) map[string]any {
	row := map[string]any{}
	for _, k := range []string{"id", "created_at", "updated_at"} {
		row[k] = shownCell(t, k, raw[k])
	}
	for _, c := range t.Columns {
		x := raw[c.Name]
		switch {
		case x == nil:
			row[c.Name] = ""
		case isEnvelope(x):
			row[c.Name] = whatsapp.MaskValue(category(c), nil)
		case c.Personal && !personal:
			row[c.Name] = whatsapp.MaskValue(category(c), cellText(x))
		default:
			row[c.Name] = shownCell(t, c.Name, x)
		}
	}
	return row
}

func isEnvelope(x any) bool {
	m, ok := x.(map[string]any)
	return ok && pii.IsEnvelope(m)
}

func category(c datatables.Column) string {
	switch c.Type {
	case "phone", "email":
		return c.Type
	case "text":
		return "name"
	}
	return "personal"
}

// shownCell writes one value of a column as a person reads it.
func shownCell(t datatables.Table, name string, x any) string {
	if x == nil {
		return ""
	}
	col, _ := columnOf(t, name)
	s := cellText(x)
	switch col.Type {
	case "boolean":
		switch s {
		case "true":
			return "Yes"
		case "false":
			return "No"
		}
	case "date":
		if d, err := time.Parse("2006-01-02", s); err == nil {
			return d.Format("2 Jan 2006")
		}
	case "datetime":
		if d, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return d.In(lagos).Format("2 Jan 2006, 15:04")
		}
	case "integer", "number":
		if f, err := strconv.ParseFloat(s, 64); err == nil && math.Abs(f) < 1e15 {
			return strconv.FormatFloat(f, 'f', -1, 64)
		}
	case "json":
		if r := []rune(s); len(r) > 80 {
			s = string(r[:79]) + "…"
		}
	}
	if r := []rune(s); len(r) > maxShown {
		s = string(r[:maxShown-1]) + "…"
	}
	return s
}

func cellText(x any) string {
	switch v := x.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(v, 10)
	case int:
		return strconv.Itoa(v)
	case json.Number:
		return v.String()
	}
	raw, _ := json.Marshal(x)
	return string(raw)
}

// nairaOf writes an amount in kobo as naira; anything else as it is.
func nairaOf(x any) string {
	s := strings.TrimSpace(cellText(x))
	if s == "" {
		return ""
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.Abs(f) > 1e15 {
		return s
	}
	return storefront.FormatNaira(int64(math.Round(f)))
}

// say works out words or an expression for one row ("" when it cannot,
// so a row with a value missing still draws).
func say(v string, act map[string]any) string {
	src, isExpr := strings.CutPrefix(v, "=")
	if !isExpr {
		return v
	}
	out, err := engine.Eval(src, act)
	if err != nil || out == nil {
		return ""
	}
	s := cellText(out)
	if r := []rune(s); len(r) > maxShown {
		s = string(r[:maxShown-1]) + "…"
	}
	return s
}

func groupKey(x any) string { return strings.TrimSpace(cellText(x)) }

// Groups are a grouped list's headings for the rows shown, in the order
// the part gives, then the rest as they come; a row with no value is under
// "Other".
func Groups(b Block, rows []Shown) []string {
	if b.Group == nil {
		return nil
	}
	var seen []string
	for _, r := range rows {
		if !slices.Contains(seen, r.Group) {
			seen = append(seen, r.Group)
		}
	}
	var out []string
	for _, g := range b.Group.Order {
		if i := slices.IndexFunc(seen, func(s string) bool { return strings.EqualFold(s, g) }); i >= 0 {
			out = append(out, seen[i])
		}
	}
	for _, g := range seen {
		if !slices.Contains(out, g) {
			out = append(out, g)
		}
	}
	return out
}

// GroupLabel is a group's heading.
func GroupLabel(key string) string {
	if key == "" {
		return "Other"
	}
	r := []rune(key)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// FigureShown writes a figure as drawn: a count as a whole number, an
// amount in kobo as naira.
func FigureShown(b Block, value float64) string {
	if b.Naira {
		return storefront.FormatNaira(int64(math.Round(value)))
	}
	if value == math.Trunc(value) && math.Abs(value) < 1e15 {
		s := strconv.FormatInt(int64(value), 10)
		neg := strings.HasPrefix(s, "-")
		s = strings.TrimPrefix(s, "-")
		for i := len(s) - 3; i > 0; i -= 3 {
			s = s[:i] + "," + s[i:]
		}
		if neg {
			s = "-" + s
		}
		return s
	}
	return strconv.FormatFloat(value, 'f', 2, 64)
}

// ChangeWords says a figure's change on the period before: "up 12% on
// last week", "down 3 on yesterday", "the same as last month".
func ChangeWords(period string, now, before float64) string {
	last := map[string]string{"day": "yesterday", "week": "last week", "month": "last month"}[period]
	switch {
	case now == before:
		return "the same as " + last
	case before == 0:
		return "up from none " + last
	}
	pct := math.Round((now - before) / math.Abs(before) * 100)
	if pct > 0 {
		return fmt.Sprintf("up %.0f%% on %s", pct, last)
	}
	return fmt.Sprintf("down %.0f%% on %s", -pct, last)
}

// Shows says, for publishing, what each table's columns a page shows
// (pages §8): "Tickets: Title, Customer, Status".
func Shows(p *Page, tables []datatables.Table) []string {
	var out []string
	for _, name := range p.UsesTables() {
		t, ok := Lookup(tables, name)
		if !ok {
			continue
		}
		var cols []string
		add := func(c string) {
			if col, ok := columnOf(t, c); ok && !slices.Contains(cols, columnTitle(col)) {
				cols = append(cols, columnTitle(col))
			}
		}
		reads := func(v string) {
			if src, ok := strings.CutPrefix(v, "="); ok {
				if paths, err := engine.Paths(src, "row"); err == nil {
					for _, pth := range paths {
						if len(pth) > 1 {
							add(pth[1])
						}
					}
				}
			}
		}
		for _, b := range p.Blocks {
			if b.Source == nil || b.Source.From() != name {
				continue
			}
			if b.Type == "figure" {
				if b.Value == "sum" {
					add(b.Of)
				}
				continue
			}
			if b.Item != nil {
				reads(b.Item.Title)
				reads(b.Item.Sub)
				if b.Item.State != nil {
					reads(b.Item.State.Text)
				}
			}
			for _, c := range b.Columns {
				add(c.Column)
			}
			if b.Group != nil {
				add(b.Group.Column)
			}
			if b.Detail != nil {
				reads(b.Detail.Title)
				for _, l := range b.Detail.Lines {
					reads(l.Value)
				}
			}
		}
		line := tableTitle(t) + ": "
		if len(cols) == 0 {
			line += "how many rows"
		} else {
			line += strings.Join(cols, ", ")
		}
		out = append(out, line)
	}
	// What tools work out for it (G2): "/spend's answer: total, rows".
	var tools []string
	outs := map[string][]string{}
	for _, b := range p.Blocks {
		if b.Source == nil || b.Source.Tool == "" {
			continue
		}
		if !slices.Contains(tools, b.Source.Tool) {
			tools = append(tools, b.Source.Tool)
		}
		if !slices.Contains(outs[b.Source.Tool], b.Source.Output) {
			outs[b.Source.Tool] = append(outs[b.Source.Tool], b.Source.Output)
		}
	}
	for _, c := range tools {
		out = append(out, "/"+c+"'s answer: "+strings.Join(outs[c], ", "))
	}
	return out
}
