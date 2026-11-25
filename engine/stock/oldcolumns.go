package stock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/alerts"
)

// The catalogue's stock columns are gone (docs/spec/what-a-business-buys.md
// §4.3, decision 0070): its mapping no longer names stock, unit,
// cost_price, reorder_level, target_level or supplier, and a product is
// counted by a stock item. A workflow the owner made before may still read
// or set them on the catalogue's table. OldColumnsTx finds those steps in
// the business's workflows, each with what to use instead; TellOldColumns
// tells the owners of them once.

// OldColumns are the catalogue mapping's stock columns S1 took away, by
// their mapping key; each is also the name Keep my stock gave its column.
var OldColumns = []string{"stock", "unit", "cost_price", "reorder_level", "target_level", "supplier"}

// AlertOldColumns is the kind of the alert listing them (told once).
const AlertOldColumns = "stock.old_columns"

// Reads and Sets are what a step does with an old column.
const (
	Reads = "reads"
	Sets  = "sets"
)

// OldColumnUse is one step of a workflow that reads or sets one old
// column of the catalogue's table.
type OldColumnUse struct {
	WorkflowID uuid.UUID `json:"workflow_id"`
	Workflow   string    `json:"workflow"`
	Version    int       `json:"version"`
	// Published: the version that runs; else the latest draft, listed
	// only for what the published version does not already show.
	Published bool `json:"published"`
	// Step is the step's id, or "trigger" for a row-changed trigger on
	// the catalogue's table; StepName its name when it has one.
	Step     string `json:"step"`
	StepName string `json:"step_name,omitempty"`
	// Column is the old mapping key (stock, cost_price, …); TableColumn
	// the column of the business's table it named.
	Column      string `json:"column"`
	TableColumn string `json:"table_column"`
	Uses        string `json:"uses"`
	// Instead is what to use instead (§4.3).
	Instead string `json:"instead"`
}

// Instead is what replaces a use of an old column (§4.3): reading the kept
// copy stock_items (read-only) or stock@1, and changing a count through
// stock@1, which records a movement.
func Instead(column, uses string) string {
	if uses == Sets {
		switch column {
		case "stock":
			return "Call stock@1 adjust (by a change), count (to what was counted) or receive (goods in): each records a movement."
		case "cost_price":
			return "Set the item's cost on the Stock page; workflows read it as cost_kobo (in kobo) from the kept copy stock_items."
		}
		return fmt.Sprintf("Set the item's %s on the Stock page; workflows read it from the kept copy stock_items.", strings.ReplaceAll(column, "_", " "))
	}
	switch column {
	case "stock":
		return "Read count from the kept copy stock_items (read-only), or call stock@1 get_item or list_items."
	case "cost_price":
		return "Read cost_kobo (in kobo) from the kept copy stock_items, or from stock@1 get_item."
	case "reorder_level":
		return "Read low_level from the kept copy stock_items, or call stock@1 low_items."
	}
	return fmt.Sprintf("Read %s from the kept copy stock_items, or from stock@1 get_item.", column)
}

// OldCatalogue is the catalogue's table and the names its old columns had
// there: the mapping's own when it still holds them, else the names Keep
// my stock gave them.
type OldCatalogue struct {
	Table string
	Names map[string]string // old mapping key -> the table's column
}

// OldCatalogueOf reads the business's catalogue table and its old column
// names from its storefront's mapping as stored (S1a refuses a mapping
// naming them, and leaves a stored one as it was); ok is false when the
// business has no catalogue table.
func OldCatalogueOf(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (OldCatalogue, bool, error) {
	var c OldCatalogue
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT d.name, COALESCE(s.catalogue_columns, '{}') FROM storefronts s JOIN data_tables d ON d.id = s.catalogue_table
		WHERE s.tenant_id = $1 LIMIT 1`, tenant).Scan(&c.Table, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, false, nil
	}
	if err != nil {
		return c, false, err
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	c.Names = map[string]string{}
	for _, k := range OldColumns {
		c.Names[k] = k
		if s, ok := m[k].(string); ok && s != "" {
			c.Names[k] = s
		}
	}
	return c, true, nil
}

// OldColumnsTx lists the steps of the business's workflows (each one's
// published version, and its latest draft when newer) that read or set
// an old column of the catalogue's table, published first, by workflow
// name.
func OldColumnsTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]OldColumnUse, error) {
	c, ok, err := OldCatalogueOf(ctx, tx, tenant)
	if err != nil || !ok {
		return []OldColumnUse{}, err
	}
	rows, err := tx.Query(ctx, `SELECT w.id, w.name, v.version, v.state = 'published', v.definition FROM workflows w
		JOIN workflow_versions v ON v.workflow_id = w.id
		WHERE w.tenant_id = $1 AND (v.state = 'published'
		   OR (v.state = 'draft' AND v.version = (SELECT max(version) FROM workflow_versions x WHERE x.workflow_id = w.id)
		       AND v.version > COALESCE(w.active_version, 0)))
		ORDER BY w.name, w.id, v.state = 'published' DESC`, tenant)
	if err != nil {
		return nil, err
	}
	type version struct {
		id        uuid.UUID
		name      string
		v         int
		published bool
		doc       []byte
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (version, error) {
		var x version
		return x, r.Scan(&x.id, &x.name, &x.v, &x.published, &x.doc)
	})
	if err != nil {
		return nil, err
	}
	out := []OldColumnUse{}
	seen := map[string]bool{}
	for _, x := range list {
		for _, u := range OldColumnsIn(x.doc, c) {
			key := x.id.String() + "\x00" + u.Step + "\x00" + u.Column + "\x00" + u.Uses
			if seen[key] {
				continue
			}
			seen[key] = true
			u.WorkflowID, u.Workflow, u.Version, u.Published = x.id, x.name, x.v, x.published
			out = append(out, u)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Published && !out[j].Published })
	return out, nil
}

// TellOldColumns tells the owners of uses once (their notifications and
// chat, as a setup problem): what each workflow's steps do with the old
// columns and what to use instead. told is false when there was nothing to
// tell or they were told before.
func TellOldColumns(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, uses []OldColumnUse) (told bool, err error) {
	if len(uses) == 0 {
		return false, nil
	}
	flows := map[uuid.UUID]bool{}
	var lines []string
	for _, u := range uses {
		flows[u.WorkflowID] = true
		step := u.Step
		if u.StepName != "" {
			step = u.StepName
		}
		draft := ""
		if !u.Published {
			draft = " (draft)"
		}
		lines = append(lines, fmt.Sprintf("%s%s, %s: %s %s. %s", u.Workflow, draft, step, u.Uses, u.TableColumn, u.Instead))
	}
	title := "A workflow uses the catalogue's old stock columns"
	if len(flows) > 1 {
		title = fmt.Sprintf("%d workflows use the catalogue's old stock columns", len(flows))
	}
	body := "Stock is now kept on stock items, not in the catalogue's table. " + strings.Join(lines, " ")
	if r := []rune(body); len(r) > 1000 {
		body = string(r[:999]) + "…"
	}
	detail := map[string]any{"uses": uses}
	return alerts.TellOwners(ctx, tx, tenant, alerts.Alert{Kind: AlertOldColumns, Dedup: AlertOldColumns, Title: title, Body: body,
		Link: "/workflows", Detail: detail})
}

// OldColumnsIn finds, in one workflow document, the steps that read or set
// an old column of the catalogue's table c (without its workflow fields):
//
//   - a xenibase step on the table (the data_tables connection) naming the
//     column in its select, filters or order (reads), or in the columns it
//     sets, inserts or upserts (sets), each also when given as an
//     expression ("=…"): the column as a string in it (a select's strings,
//     a filter's column, a set's or a row's keys);
//   - such a reading step with no select (every column) when anything else
//     in the workflow reads the column off a row (.stock, ['stock']);
//   - a row-changed trigger on the table that watches the column, or whose
//     row the workflow reads it off.
//
// A step on stock@1 or the kept copy stock_items is never one.
func OldColumnsIn(doc []byte, c OldCatalogue) []OldColumnUse {
	var root map[string]any
	if json.Unmarshal(doc, &root) != nil {
		return nil
	}
	var out []OldColumnUse
	add := func(step, name, col, uses string) {
		for _, u := range out {
			if u.Step == step && u.Column == col && u.Uses == uses {
				return
			}
		}
		out = append(out, OldColumnUse{Step: step, StepName: name, Column: col, TableColumn: c.Names[col], Uses: uses, Instead: Instead(col, uses)})
	}
	readsOff := func(texts []string, col string) bool {
		re := fieldRe(col)
		for _, t := range texts {
			if re.MatchString(t) {
				return true
			}
		}
		return false
	}
	steps := allSteps(root)
	text := make([][]string, len(steps))
	for i, st := range steps {
		text[i] = stringsOf(st)
	}

	// The trigger.
	if tr, ok := root["trigger"].(map[string]any); ok {
		cfg, _ := tr["config"].(map[string]any)
		if cfg != nil && isXenibase(sval(cfg["connector"])) && sval(cfg["trigger"]) == "row_changed" && onConnection(cfg) {
			opts, _ := cfg["options"].(map[string]any)
			tables, _ := opts["tables"].([]any)
			if hasTable(tables, c.Table) {
				watched := names(opts["columns"])
				var texts []string
				for _, t := range stringsOf(root) {
					if strings.Contains(t, "trigger.") {
						texts = append(texts, t)
					}
				}
				for _, col := range OldColumns {
					if watched[c.Names[col]] || readsOff(texts, c.Names[col]) {
						add("trigger", "", col, Reads)
					}
				}
			}
		}
	}

	for i, st := range steps {
		if !isXenibase(sval(st["connector"])) || !onConnection(st) {
			continue
		}
		in, _ := st["input"].(map[string]any)
		if in == nil || !sameTable(sval(in["table"]), c.Table) {
			continue
		}
		id, label := sval(st["id"]), sval(st["name"])
		action := sval(st["action"])
		read := map[string]bool{}
		for col := range identifiers(sval(in["select"])) {
			read[col] = true
		}
		for _, k := range []string{"filters", "order"} {
			for col := range filterColumns(in[k]) {
				read[col] = true
			}
		}
		set := map[string]bool{}
		switch action {
		case "update_rows":
			for col := range keys(in["set"]) {
				set[col] = true
			}
		case "insert_rows", "upsert_rows":
			for col := range keys(in["row"]) {
				set[col] = true
			}
			switch rows := in["rows"].(type) {
			case []any:
				for _, r := range rows {
					for col := range keys(r) {
						set[col] = true
					}
				}
			case string:
				for col := range keys(rows) {
					set[col] = true
				}
			}
			for col := range names(in["on_conflict"]) {
				read[col] = true
			}
		}
		whole := (action == "read_rows" || action == "query_rows" || action == "get_row") && strings.TrimSpace(sval(in["select"])) == ""
		var texts []string
		if whole {
			texts = fedBy(steps, text, i)
		}
		for _, col := range OldColumns {
			name := c.Names[col]
			if read[name] || (whole && readsOff(texts, name)) {
				add(id, label, col, Reads)
			}
			if set[name] {
				add(id, label, col, Sets)
			}
		}
	}
	return out
}

func sval(v any) string {
	s, _ := v.(string)
	return s
}

// isXenibase is a step's connector: xenibase, any version.
func isXenibase(c string) bool {
	return c == "xenibase" || strings.HasPrefix(c, "xenibase@")
}

// onConnection: the business's own data tables' connection, or none named.
func onConnection(m map[string]any) bool {
	c := sval(m["connection"])
	return c == "" || c == "data_tables"
}

func sameTable(t, table string) bool {
	t = strings.ToLower(strings.TrimSpace(t))
	return t != "" && (t == table || t == "public."+table)
}

func hasTable(list []any, table string) bool {
	for _, t := range list {
		if sameTable(sval(t), table) {
			return true
		}
	}
	return false
}

var identRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// isExpr: a value given as an expression ("=…"), not as itself.
func isExpr(s string) bool { return strings.HasPrefix(strings.TrimSpace(s), "=") }

// litRe is a string in an expression, in single or double quotes.
var litRe = regexp.MustCompile(`'((?:[^'\\\n]|\\.)*)'|"((?:[^"\\\n]|\\.)*)"`)

// literals are the strings an expression holds ("='name, ' + 'stock'":
// "name, " and "stock").
func literals(expr string) []string {
	var out []string
	for _, m := range litRe.FindAllStringSubmatch(expr, -1) {
		out = append(out, m[1]+m[2])
	}
	return out
}

// mapEntry is an entry of a map literal in an expression whose key is a
// string: the key, and the value when it is a string itself ("" else).
type mapEntry struct{ key, value string }

// mapEntries are the entries of the map literals in an expression
// ({'stock': 3, 'column': 'qty'}), read by a small scanner, not a
// pattern: a string is a key only directly inside braces, after the
// brace or a comma and before a colon, so a ternary's `c ? 'a' : 'b'`
// (H2 review fix 6) or a string in a list or a call is never one.
func mapEntries(expr string) []mapEntry {
	var out []mapEntry
	var stack []byte
	prev := byte(0) // the last character that is not a space or in a string
	str := func(i int) (string, int, bool) {
		q := expr[i]
		var b strings.Builder
		for j := i + 1; j < len(expr); j++ {
			switch c := expr[j]; {
			case c == '\\' && j+1 < len(expr):
				b.WriteByte(expr[j+1])
				j++
			case c == q:
				return b.String(), j + 1, true
			default:
				b.WriteByte(c)
			}
		}
		return "", len(expr), false
	}
	next := func(i int) int {
		for i < len(expr) && (expr[i] == ' ' || expr[i] == '\t' || expr[i] == '\n' || expr[i] == '\r') {
			i++
		}
		return i
	}
	for i := 0; i < len(expr); {
		c := expr[i]
		switch c {
		case '\'', '"':
			s, end, ok := str(i)
			if !ok {
				return out
			}
			i = end
			j := next(i)
			if len(stack) > 0 && stack[len(stack)-1] == '{' && (prev == '{' || prev == ',') && j < len(expr) && expr[j] == ':' {
				e := mapEntry{key: s}
				if k := next(j + 1); k < len(expr) && (expr[k] == '\'' || expr[k] == '"') {
					if v, end, ok := str(k); ok {
						if after := next(end); after >= len(expr) || expr[after] == ',' || expr[after] == '}' {
							e.value = v
						}
					}
				}
				out = append(out, e)
			}
			prev = '"'
			continue
		case '{', '[', '(':
			stack = append(stack, c)
		case '}', ']', ')':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			prev = c
		}
		i++
	}
	return out
}

// identifiers are the names in a select ("name, stock, owner:profiles(name)");
// a select given as an expression, the names in the strings it holds
// ("='name, ' + (all ? 'stock' : 'unit')").
func identifiers(s string) map[string]bool {
	out := map[string]bool{}
	texts := []string{s}
	if isExpr(s) {
		texts = literals(s)
	}
	for _, t := range texts {
		for _, id := range identRe.FindAllString(t, -1) {
			out[id] = true
		}
	}
	return out
}

// filterColumns are the columns filters or an order name, any nested: a
// filter's column given as an expression names each string in it
// ("=low ? 'stock' : 'name'"), and filters, one filter or an order given
// as an expression the column of each filter in it
// ("=[{'column': 'stock', 'op': 'lt', 'value': 5}]"). A filter's value is
// never a column, so the strings of values are not read.
func filterColumns(v any) map[string]bool {
	out := map[string]bool{}
	fromExpr := func(s string) {
		for _, e := range mapEntries(s) {
			if e.key == "column" && e.value != "" {
				out[e.value] = true
			}
		}
	}
	if s, ok := v.(string); ok {
		if isExpr(s) {
			fromExpr(s)
		}
		return out
	}
	list, _ := v.([]any)
	for _, f := range list {
		if s, ok := f.(string); ok {
			if isExpr(s) {
				fromExpr(s)
			}
			continue
		}
		m, _ := f.(map[string]any)
		if c := sval(m["column"]); c != "" {
			if isExpr(c) {
				for _, l := range literals(c) {
					out[strings.TrimSpace(l)] = true
				}
			} else {
				out[c] = true
			}
		}
		for c := range filterColumns(m["any"]) {
			out[c] = true
		}
	}
	return out
}

// keys are the columns a set or a row names: its keys, or given as an
// expression, the strings it uses as keys ("={'stock': trigger.body.n}").
func keys(v any) map[string]bool {
	out := map[string]bool{}
	if s, ok := v.(string); ok {
		if isExpr(s) {
			for _, e := range mapEntries(s) {
				out[e.key] = true
			}
		}
		return out
	}
	m, _ := v.(map[string]any)
	for k := range m {
		out[k] = true
	}
	return out
}

// names are the strings of a list (an on_conflict's columns); given as
// an expression, the names in the strings it holds.
func names(v any) map[string]bool {
	out := map[string]bool{}
	if s, ok := v.(string); ok {
		if isExpr(s) {
			for _, l := range literals(s) {
				for _, id := range identRe.FindAllString(l, -1) {
					out[id] = true
				}
			}
		}
		return out
	}
	list, _ := v.([]any)
	for _, x := range list {
		if s := sval(x); s != "" {
			if isExpr(s) {
				for _, l := range literals(s) {
					out[strings.TrimSpace(l)] = true
				}
				continue
			}
			out[s] = true
		}
	}
	return out
}

// fieldRe matches reading col off a value: .col, ['col'] or ["col"].
func fieldRe(col string) *regexp.Regexp {
	q := regexp.QuoteMeta(col)
	return regexp.MustCompile(`\.` + q + `\b|\[\s*['"]` + q + `['"]\s*\]`)
}

// allSteps are every step of the document, those inside containers and
// on_error too: the objects of every "steps" list.
func allSteps(v any) []map[string]any {
	var out []map[string]any
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				if list, ok := val.([]any); ok && k == "steps" {
					for _, s := range list {
						if m, ok := s.(map[string]any); ok && sval(m["id"]) != "" {
							out = append(out, m)
						}
					}
				}
				walk(val)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(v)
	return out
}

// stringsOf are the strings in v, but names, titles and descriptions
// (words for people, not reads).
func stringsOf(v any) []string {
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				if k != "name" && k != "description" && k != "title" {
					walk(val)
				}
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		case string:
			out = append(out, x)
		}
	}
	walk(v)
	return out
}

// fedBy is the text of the steps fed by step i's output: those naming
// steps.<id> of i or of a step fed by it, in turn (a foreach over its rows
// with all the steps inside it).
func fedBy(steps []map[string]any, text [][]string, i int) []string {
	fed := map[int]bool{i: true}
	for grew := true; grew; {
		grew = false
		for j := range steps {
			if fed[j] {
				continue
			}
			for k := range fed {
				ref := "steps." + sval(steps[k]["id"]) + "."
				if slices.ContainsFunc(text[j], func(t string) bool { return strings.Contains(t, ref) }) {
					fed[j], grew = true, true
					break
				}
			}
		}
	}
	var out []string
	for j := range fed {
		if j != i {
			out = append(out, text[j]...)
		}
	}
	return out
}
