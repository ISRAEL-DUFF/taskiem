package pages

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/israel-duff/taskiem/engine/access"
	"github.com/israel-duff/taskiem/engine/areas"
	"github.com/israel-duff/taskiem/engine/datatables"
)

// Taskiem's own records as a page's source (pages §5, "Taskiem's own
// records, read only"; the contract's rule 10): `"source": {"records":
// "orders"}`. Each is a view Taskiem defines here, over its own tables,
// with a fixed list of the columns a page may show. A page reads it as it
// reads a table (conditions, order, the page's filters, a count or a sum),
// but nothing on a page writes to it (actions are G2).
//
// A view is drawn as a table whose name is "records:<view>" (a data
// table's name never has a colon), so the rules a table is held to (the
// checked query of decision 0060, what is masked, what may be ordered by)
// hold for it unchanged. Its rows are read with SQL built here from the
// view's own columns only, never from words a page or a viewer wrote: a
// column a view does not list cannot be named, and every value is a bound
// parameter.
//
// A view's personal columns (the customer's name, phone and email) are
// masked for every viewer, as the Orders and Invoices lists mask them; a
// page cannot narrow, add up, group or order by one (Check), so they are
// never read in SQL: they come from the record's sealed envelope, opened
// only for the rows shown.
//
// Who may see a view is the area's own read permission (Perm): a viewer
// without it is told so in place of the part. An area the business has
// turned off is refused at Check, and drawn as unavailable.
//
// Orders and invoices first (the founder's choice); deliveries, customers
// and staff are views added to RecordViews later.

// RecordsPrefix starts the name a view is drawn under as a table.
const RecordsPrefix = "records:"

// MaxRecordSkip is how far More reaches into a view's rows: a view's
// cursor is Taskiem's own count of the rows shown, never a value.
const MaxRecordSkip = 10000 - RowsShown

// RecordView is one of Taskiem's own records as a page reads it.
type RecordView struct {
	Name  string // as a page names it: orders
	Title string // Orders
	// Words is how a sentence names its rows: "orders".
	Words string
	// Area is the business area it is in (areas.Area), Perm the permission
	// a viewer needs to see it ("a|b" for either).
	Area areas.Area
	Perm string
	// From is the SQL it reads (one table, aliased), Tenant its tenant
	// column, Key a row's own key (shown as the row's id, and in its
	// link), Seal the sealed envelope its personal columns are in, Changed
	// when a row last changed (changed_at, migration 00555: the live
	// notices, api/pages_live.go; never updated_at, which the shop's
	// retention reads).
	From, Tenant, Key, Seal, Changed string
	// Link is the record's own screen, with %s for its key (escaped).
	Link    string
	Columns []RecordColumn
}

// RecordColumn is a column a view shows: a datatables column, read with
// SQL (an expression over the view's table), or, for a personal column,
// one of the sealed envelope's fields.
type RecordColumn struct {
	datatables.Column
	SQL    string
	Sealed string
	// NotNull: the column always has a value, so its order needs no NULLS
	// LAST and an index (tenant, status, created_at DESC) can serve it.
	NotNull bool
	// Exact: a word from a short fixed list Taskiem writes in small letters
	// (a status): "is" is an equality, so its index applies.
	Exact bool
}

// RecordViews are the views a page may show, in the editor's order.
var RecordViews = []RecordView{
	{
		Name: "orders", Title: "Orders", Words: "orders",
		Area: areas.Orders, Perm: access.StorefrontManage + "|" + access.OrderHandle,
		From: "storefront_orders o", Tenant: "o.tenant_id", Key: "o.reference", Seal: "o.customer", Changed: "o.changed_at",
		Link: "/storefront/orders/%s",
		Columns: []RecordColumn{
			{Column: datatables.Column{Name: "number", Title: "Number", Type: "text"}, SQL: "o.reference", NotNull: true},
			{Column: datatables.Column{Name: "customer", Title: "Customer", Type: "text", Personal: true}, Sealed: "name"},
			{Column: datatables.Column{Name: "phone", Title: "Phone", Type: "phone", Personal: true}, Sealed: "phone"},
			{Column: datatables.Column{Name: "email", Title: "Email", Type: "email", Personal: true}, Sealed: "email"},
			{Column: datatables.Column{Name: "status", Title: "Status", Type: "text"}, SQL: "o.status", NotNull: true, Exact: true},
			{Column: datatables.Column{Name: "kind", Title: "Kind", Type: "text"}, SQL: "o.kind", NotNull: true, Exact: true},
			{Column: datatables.Column{Name: "channel", Title: "Came from", Type: "text"},
				SQL: "CASE WHEN o.kind = 'trade' THEN 'business' WHEN o.person_id IS NOT NULL THEN 'app' ELSE 'shop' END", NotNull: true, Exact: true},
			{Column: datatables.Column{Name: "total", Title: "Total", Type: "integer"}, SQL: "o.total_kobo", NotNull: true},
			{Column: datatables.Column{Name: "paid", Title: "Paid", Type: "integer"}, SQL: "o.paid_kobo", NotNull: true},
			{Column: datatables.Column{Name: "fulfilment", Title: "Pickup or delivery", Type: "text"}, SQL: "o.fulfilment", Exact: true},
			{Column: datatables.Column{Name: "zone", Title: "Delivery area", Type: "text"}, SQL: "o.zone"},
			{Column: datatables.Column{Name: "delivery", Title: "Delivery", Type: "text"},
				SQL: "(SELECT d.status FROM deliveries d WHERE d.order_id = o.id ORDER BY d.offered_at DESC LIMIT 1)", Exact: true},
			{Column: datatables.Column{Name: "placed_at", Title: "Placed", Type: "datetime"}, SQL: "o.created_at", NotNull: true},
			{Column: datatables.Column{Name: "paid_at", Title: "Paid on", Type: "datetime"}, SQL: "o.paid_at"},
		},
	},
	{
		Name: "invoices", Title: "Invoices", Words: "invoices",
		Area: areas.Money, Perm: access.InvoiceRead,
		From: "customer_invoices i", Tenant: "i.tenant_id", Key: "i.number", Seal: "i.customer", Changed: "i.changed_at",
		Link: "/invoices/%s",
		Columns: []RecordColumn{
			{Column: datatables.Column{Name: "number", Title: "Number", Type: "text"}, SQL: "i.number", NotNull: true},
			{Column: datatables.Column{Name: "customer", Title: "Customer", Type: "text", Personal: true}, Sealed: "name"},
			{Column: datatables.Column{Name: "phone", Title: "Phone", Type: "phone", Personal: true}, Sealed: "phone"},
			{Column: datatables.Column{Name: "email", Title: "Email", Type: "email", Personal: true}, Sealed: "email"},
			{Column: datatables.Column{Name: "status", Title: "Status", Type: "text"}, SQL: "i.status", NotNull: true, Exact: true},
			{Column: datatables.Column{Name: "kind", Title: "Kind", Type: "text"}, SQL: "i.kind", NotNull: true, Exact: true},
			{Column: datatables.Column{Name: "total", Title: "Total", Type: "integer"}, SQL: "i.total_kobo", NotNull: true},
			{Column: datatables.Column{Name: "paid", Title: "Paid", Type: "integer"}, SQL: "i.paid_kobo", NotNull: true},
			{Column: datatables.Column{Name: "owed", Title: "Owed", Type: "integer"}, SQL: "GREATEST(i.total_kobo - i.paid_kobo, 0)", NotNull: true},
			{Column: datatables.Column{Name: "due_on", Title: "Due", Type: "date"}, SQL: "i.due_on"},
			{Column: datatables.Column{Name: "issued_at", Title: "Sent", Type: "datetime"}, SQL: "i.sent_at"},
			{Column: datatables.Column{Name: "paid_at", Title: "Paid on", Type: "datetime"}, SQL: "i.paid_at"},
		},
	},
}

// RecordViewNamed is a view by its name.
func RecordViewNamed(name string) (RecordView, bool) {
	i := slices.IndexFunc(RecordViews, func(v RecordView) bool { return v.Name == name })
	if i < 0 {
		return RecordView{}, false
	}
	return RecordViews[i], true
}

// RecordViewOf is the view a table name stands for ("records:orders").
func RecordViewOf(table string) (RecordView, bool) {
	name, ok := strings.CutPrefix(table, RecordsPrefix)
	if !ok {
		return RecordView{}, false
	}
	return RecordViewNamed(name)
}

// IsRecords reports whether a table name is a view's.
func IsRecords(table string) bool { return strings.HasPrefix(table, RecordsPrefix) }

// Table is the view as a table: its columns, under "records:<name>".
func (v RecordView) Table() datatables.Table {
	t := datatables.Table{Name: RecordsPrefix + v.Name, Title: v.Title}
	for _, c := range v.Columns {
		t.Columns = append(t.Columns, c.Column)
	}
	return t
}

// From is what a source reads, as a table's name: the data table's, or
// "records:<view>".
func (s *Source) From() string {
	if s == nil {
		return ""
	}
	if s.Records != "" {
		return RecordsPrefix + s.Records
	}
	return s.Table
}

// Lookup is the table a source names: a view of Taskiem's own records by
// its exact name, else one of the business's tables (by name or title,
// as the checked query finds one; never a view).
func Lookup(tables []datatables.Table, name string) (datatables.Table, bool) {
	if IsRecords(name) {
		if v, ok := RecordViewOf(name); ok {
			return v.Table(), true
		}
		return datatables.Table{}, false
	}
	own := make([]datatables.Table, 0, len(tables))
	for _, t := range tables {
		if !IsRecords(t.Name) {
			own = append(own, t)
		}
	}
	return datatables.FindQueryTable(own, name)
}

// LinkOf is a record's own screen for its key.
func (v RecordView) LinkOf(key string) string {
	if v.Link == "" || key == "" {
		return ""
	}
	return fmt.Sprintf(v.Link, urlPathEscape(key))
}

func urlPathEscape(s string) string {
	var b strings.Builder
	for _, r := range []byte(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.', r == '~':
			b.WriteByte(r)
		default:
			fmt.Fprintf(&b, "%%%02X", r)
		}
	}
	return b.String()
}

// ErrRecordsRead refuses a read a view cannot answer: a condition or an
// order on a column it does not have, or on a personal one.
var ErrRecordsRead = errors.New("pages: this cannot be read from Taskiem's records")

// sqlOf is a column's SQL for a condition or an order: never a personal
// column's (it is sealed, and masked for everyone).
func (v RecordView) sqlOf(name string, order bool) (RecordColumn, string, error) {
	switch name {
	case "created_at":
		return RecordColumn{Column: datatables.Column{Name: name, Type: "datetime"}, NotNull: true}, v.alias() + ".created_at", nil
	case "id":
		if order {
			return RecordColumn{Column: datatables.Column{Name: name, Type: "text"}, NotNull: true}, v.alias() + ".id", nil
		}
	}
	for _, c := range v.Columns {
		if c.Name != name {
			continue
		}
		if c.Personal || c.SQL == "" {
			return c, "", fmt.Errorf("%w: %s is personal", ErrRecordsRead, name)
		}
		return c, c.SQL, nil
	}
	return RecordColumn{}, "", fmt.Errorf("%w: no column %s", ErrRecordsRead, name)
}

func (v RecordView) alias() string { return strings.SplitN(v.From, " ", 2)[1] }

// Orderable says whether a view's rows may be put in a column's order: one
// of its own columns that is not personal, or when a row was made.
func (v RecordView) Orderable(name string) bool {
	_, _, err := v.sqlOf(name, false)
	return err == nil
}

// likeExact is the value an ILIKE pattern with no wildcard matches, or
// false when it has one (a contains, a search).
func likeExact(p string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '\\':
			if i+1 < len(p) {
				i++
				b.WriteByte(p[i])
				continue
			}
			b.WriteByte(p[i])
		case '%', '_':
			return "", false
		default:
			b.WriteByte(p[i])
		}
	}
	return b.String(), true
}

// castOf is the SQL type a value of a column's type is compared as.
func castOf(typ string) string {
	switch typ {
	case "integer":
		return "bigint"
	case "number":
		return "numeric"
	case "date":
		return "date"
	case "datetime":
		return "timestamptz"
	case "boolean":
		return "boolean"
	}
	return "text"
}

// recordsQuery builds the WHERE of a read over a view: the tenant ($1),
// then each condition of r (the read_rows conditions ReadFor makes), every
// value a parameter.
type recordsQuery struct {
	v    RecordView
	args []any
}

func (q *recordsQuery) arg(x any) string {
	q.args = append(q.args, x)
	return fmt.Sprintf("$%d", len(q.args))
}

var recordOps = map[string]string{"eq": "=", "neq": "<>", "lt": "<", "lte": "<=", "gt": ">", "gte": ">="}

func (q *recordsQuery) cond(f any) (string, error) {
	m, ok := f.(map[string]any)
	if !ok {
		return "", fmt.Errorf("%w: a condition", ErrRecordsRead)
	}
	if anyOf, ok := m["any"].([]any); ok {
		if len(anyOf) == 0 {
			return "false", nil
		}
		parts := make([]string, 0, len(anyOf))
		for _, a := range anyOf {
			s, err := q.cond(a)
			if err != nil {
				return "", err
			}
			parts = append(parts, s)
		}
		return "(" + strings.Join(parts, " OR ") + ")", nil
	}
	name, _ := m["column"].(string)
	op, _ := m["op"].(string)
	c, expr, err := q.v.sqlOf(name, false)
	if err != nil {
		return "", err
	}
	val := m["value"]
	text := func() string { return cellText(val) }
	switch op {
	case "is_null":
		return expr + " IS NULL", nil
	case "not_null":
		return expr + " IS NOT NULL", nil
	case "ilike":
		if exact, ok := likeExact(text()); ok && c.Exact {
			// A status or a kind is written in small letters: "is" is an
			// equality, which the (tenant, status, created_at) index serves.
			return expr + " = " + q.arg(strings.ToLower(exact)), nil
		}
		return "(" + expr + ")::text ILIKE " + q.arg(text()), nil
	case "is":
		return expr + " IS " + map[bool]string{true: "TRUE", false: "FALSE"}[text() == "true"], nil
	}
	sym, ok := recordOps[op]
	if !ok {
		return "", fmt.Errorf("%w: the condition %s", ErrRecordsRead, op)
	}
	return expr + " " + sym + " " + q.arg(text()) + "::text::" + castOf(c.Type), nil
}

func (q *recordsQuery) where(r Read) (string, error) {
	q.args = nil
	parts := []string{q.v.Tenant + " = $1"}
	q.args = append(q.args, nil) // the tenant, set by the caller
	for _, f := range r.Filters {
		s, err := q.cond(f)
		if err != nil {
			return "", err
		}
		parts = append(parts, s)
	}
	return " FROM " + q.v.From + " WHERE " + strings.Join(parts, " AND "), nil
}

// RecordsSelect is the SQL that reads a view's rows for r: its key, its
// sealed envelope, then each column (Columns' order; NULL for a personal
// one, filled from the envelope), each as text as a table's value is
// written (a date as 2026-11-23, a moment in RFC 3339), limit rows after
// skip. args[0] is for the tenant.
func (v RecordView) RecordsSelect(r Read, limit, skip int) (string, []any, error) {
	q := &recordsQuery{v: v}
	from, err := q.where(r)
	if err != nil {
		return "", nil, err
	}
	alias := v.alias()
	sel := []string{v.Key, v.Seal, "to_char(" + alias + ".created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"')"}
	for _, c := range v.Columns {
		switch {
		case c.SQL == "":
			sel = append(sel, "NULL::text")
		case c.Type == "datetime":
			sel = append(sel, "to_char(("+c.SQL+") AT TIME ZONE 'UTC', 'YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"')")
		case c.Type == "date":
			sel = append(sel, "to_char("+c.SQL+", 'YYYY-MM-DD')")
		default:
			sel = append(sel, "("+c.SQL+")::text")
		}
	}
	// The part's order, then newest first, then the row's id: ReadFor
	// always ends an order with the id as a tie-breaker, which on its own
	// would put rows in their random ids' order. A column that always has a
	// value is ordered without NULLS LAST, so the indexes on (tenant,
	// created_at DESC) and (tenant, status, created_at DESC) serve it.
	var order []string
	seen := map[string]bool{}
	add := func(c RecordColumn, expr string, desc bool) {
		if seen[expr] {
			return
		}
		seen[expr] = true
		switch {
		case desc && c.NotNull:
			order = append(order, expr+" DESC")
		case desc:
			order = append(order, expr+" DESC NULLS LAST")
		case c.NotNull:
			order = append(order, expr+" ASC")
		default:
			order = append(order, expr+" ASC NULLS LAST")
		}
	}
	for _, o := range r.Order {
		m, _ := o.(map[string]any)
		name, _ := m["column"].(string)
		desc, _ := m["desc"].(bool)
		if name == "id" {
			continue // the tie-breaker, last
		}
		c, expr, err := v.sqlOf(name, true)
		if err != nil {
			return "", nil, err
		}
		add(c, expr, desc)
	}
	add(RecordColumn{NotNull: true}, alias+".created_at", true)
	add(RecordColumn{NotNull: true}, alias+".id", true)
	sql := "SELECT " + strings.Join(sel, ", ") + from + " ORDER BY " + strings.Join(order, ", ") +
		fmt.Sprintf(" LIMIT %d OFFSET %d", max(0, limit), max(0, skip))
	return sql, q.args, nil
}

// MaxRecordCount is how far a view's rows are counted: past it, the count
// says MaxRecordCount+1 (a page says "over 10,000").
const MaxRecordCount = 10000

// RecordsCount is the SQL that counts a view's rows for r, up to
// MaxRecordCount+1.
func (v RecordView) RecordsCount(r Read) (string, []any, error) {
	q := &recordsQuery{v: v}
	from, err := q.where(r)
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("SELECT count(*) FROM (SELECT 1%s LIMIT %d) c", from, MaxRecordCount+1), q.args, nil
}

// RecordsSum is the SQL that adds up a view's number column for r (in
// the database: a view's total is never stopped at 5,000 rows).
func (v RecordView) RecordsSum(r Read, of string) (string, []any, error) {
	c, expr, err := v.sqlOf(of, false)
	if err != nil {
		return "", nil, err
	}
	if c.Type != "integer" && c.Type != "number" {
		return "", nil, fmt.Errorf("%w: %s holds no numbers", ErrRecordsRead, of)
	}
	q := &recordsQuery{v: v}
	from, err := q.where(r)
	if err != nil {
		return "", nil, err
	}
	return "SELECT COALESCE(sum(" + expr + "), 0)::float8" + from, q.args, nil
}

// RecordRow is one row read with RecordsSelect, as Shape takes it: the
// row's key as its id, each column's value, the personal ones from the
// envelope opened (opened: the envelope's fields; nil when erased or not
// opened).
func (v RecordView) RecordRow(key string, createdAt *string, values []*string, opened map[string]any) map[string]any {
	row := map[string]any{"id": key}
	if createdAt != nil {
		row["created_at"] = *createdAt
	}
	for i, c := range v.Columns {
		if c.Sealed != "" {
			if x, ok := opened[c.Sealed].(string); ok && x != "" {
				row[c.Name] = x
			}
			continue
		}
		if i < len(values) && values[i] != nil {
			row[c.Name] = *values[i]
		}
	}
	return row
}

// ReadRecordCursor takes back a view's cursor: only a count of the rows
// shown (a view never pages by a value), given for this order.
func ReadRecordCursor(s string, order *Order) (Cursor, error) {
	if s == "" {
		return Cursor{}, nil
	}
	c, err := readCursorRaw(s)
	if err != nil || c.Order != OrderKey(order) || c.After != "" || c.Skip <= 0 || c.Skip > MaxRecordSkip {
		return Cursor{}, ErrCursor
	}
	return c, nil
}

// recordsChecks are a records source's own refusals, past those a table
// has: a view the page names that Taskiem does not have, an area that is
// off, and a condition or order on a personal column (masked for everyone,
// so nobody narrows or ranks by one).
func recordsChecks(ps *problems, at, name string, b Block, env Env) (datatables.Table, bool) {
	v, ok := RecordViewNamed(b.Source.Records)
	if !ok {
		words := make([]string, 0, len(RecordViews))
		for _, x := range RecordViews {
			words = append(words, x.Words)
		}
		ps.add(at+"/source/records", "%s shows Taskiem's %s, which a page cannot show: it shows %s.", name, b.Source.Records, andList(words))
		return datatables.Table{}, false
	}
	if env.AreaOn != nil && !env.AreaOn(string(v.Area)) {
		ps.add(at+"/source/records", "%s shows the business's %s, and %s is turned off in Settings.", name, v.Words, AreaWords(v.Area))
		return datatables.Table{}, false
	}
	t := v.Table()
	for j, c := range b.Source.Filters {
		if col, ok := columnOf(t, c.Column); ok && col.Personal {
			ps.add(fmt.Sprintf("%s/source/filters/%d/column", at, j), "%s cannot pick %s by %s: it is personal, and hidden on the page.", name, v.Words, columnTitle(col))
		}
	}
	for j, o := range b.Source.Order {
		if col, ok := columnOf(t, o.Column); ok && col.Personal {
			ps.add(fmt.Sprintf("%s/source/order/%d/column", at, j), "%s cannot put %s in order by %s: it is personal, and hidden on the page.", name, v.Words, columnTitle(col))
		}
	}
	return t, true
}

// AreaWords names an area in a sentence.
func AreaWords(a areas.Area) string {
	for _, i := range areas.Catalogue() {
		if i.Area == a {
			return i.Name
		}
	}
	return string(a)
}

// Unseen says, in place of a part over a view, why the viewer does not
// see it: "You cannot see orders."
func (v RecordView) Unseen() string { return "You cannot see " + v.Words + "." }

// Unavailable says a part over a view whose area is off is not drawn.
func (v RecordView) Unavailable() string {
	return fmt.Sprintf("%s is turned off for this business, so this part is not shown.", AreaWords(v.Area))
}
