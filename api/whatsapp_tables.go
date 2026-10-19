package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/xenibase"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Data tables in the conversation (docs/whatsapp.md#data-tables).
// "tables" lists the organisation's tables; "add to <table>" collects one
// row, in a WhatsApp form where Flows are set up and column by column in
// chat otherwise, and adds it only after an explicit yes; "find <table>
// <text>" looks rows up. Rows go through the connector's actions on the
// data_tables connection exactly as the API's do (datatables_rows.go),
// the values checked by the same code; personal columns wait sealed in
// the session, like a run's inputs, and are masked in every message.
// Creating or changing a table, and editing or deleting rows, stay in
// the web app.

const (
	// tablesChatPurpose is the recorded purpose of the connection reads
	// the chat makes, so they are told apart from the API's in secret_reads.
	tablesChatPurpose = "tables.chat"
	// waFindRows is how many rows a find shows.
	waFindRows = 5
	// waPickMax is how many tables a numbered pick offers.
	waPickMax = 9
)

// tableTitle is a table's title, or its name without one.
func tableTitle(t datatables.Table) string {
	if t.Title != "" {
		return t.Title
	}
	return t.Name
}

func (s *Server) waListTables(ctx context.Context, tenant uuid.UUID) ([]datatables.Table, error) {
	var tables []datatables.Table
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		tables, err = datatables.ListTables(ctx, tx)
		return err
	})
	return tables, err
}

func (s *Server) waGetTable(ctx context.Context, tenant, id uuid.UUID) (datatables.Table, error) {
	var t datatables.Table
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		t, err = datatables.GetTable(ctx, tx, id)
		return err
	})
	return t, err
}

// matchTables finds tables by name or title, case-insensitively: the
// exact ones, else those whose name or title contains the words.
func matchTables(tables []datatables.Table, name string) []datatables.Table {
	key := matchKey(name)
	var exact, partial []datatables.Table
	for _, t := range tables {
		n, title := matchKey(t.Name), matchKey(t.Title)
		switch {
		case n == key || title == key:
			exact = append(exact, t)
		case strings.Contains(n, key) || strings.Contains(title, key):
			partial = append(partial, t)
		}
	}
	if len(exact) == 0 {
		return partial
	}
	return exact
}

// numberedTables lists tables for a pick.
func numberedTables(tables []datatables.Table) string {
	lines := make([]string, len(tables))
	for i, t := range tables {
		lines[i] = strconv.Itoa(i+1) + ". " + whatsapp.SafeText(tableTitle(t))
	}
	return strings.Join(lines, "\n")
}

// --- tables ---

// waTables answers "tables": the organisation's data tables with their
// estimated row counts where the project answers in time.
func (s *Server) waTables(ctx context.Context, c *chat) error {
	if !c.p.Can(PermTableRead) {
		return c.say(ctx, s, c.t("wa.table.denied_read", "org", c.tenant.Name))
	}
	tables, err := s.waListTables(ctx, c.tenant.ID)
	if err != nil {
		return err
	}
	if len(tables) == 0 {
		return c.say(ctx, s, c.t("wa.table.none", "org", c.tenant.Name, "link", s.waLink(c, "/tables")))
	}
	counts := s.rowCounts(s.waTablesCtx(ctx, c), c.tenant.ID, tablesChatPurpose, tables)
	lines := []string{c.t("wa.table.list_head", "org", c.tenant.Name)}
	for i, t := range tables {
		kv := []string{"n", strconv.Itoa(i + 1), "title", whatsapp.SafeText(tableTitle(t))}
		if counts[i] == nil {
			lines = append(lines, c.t("wa.table.list_line_nocount", kv...))
			continue
		}
		lines = append(lines, c.t("wa.table.list_line", append(kv, "count", strconv.FormatInt(*counts[i], 10))...))
	}
	tail := []string{c.t("wa.table.list_tail_find")}
	if c.p.Can(PermTableEdit) {
		tail = append(tail, c.t("wa.table.list_tail_add"))
	}
	return c.say(ctx, s, strings.Join(lines, "\n")+"\n\n"+strings.Join(tail, "\n"))
}

// waTablesCtx carries the person into the context, so the connection
// reads the chat makes are recorded in their name like the API's.
func (s *Server) waTablesCtx(ctx context.Context, c *chat) context.Context {
	return context.WithValue(ctx, ctxKey{}, c.p)
}

// --- add ---

// waTableAdd answers "add to <table>": the table is matched by name or
// title (a numbered pick when several match), then one row is collected.
func (s *Server) waTableAdd(ctx context.Context, c *chat, name string) error {
	if !c.p.Can(PermTableEdit) {
		return c.say(ctx, s, c.t("wa.table.denied_edit", "org", c.tenant.Name))
	}
	if name == "" {
		return c.say(ctx, s, c.t("wa.table.add_what"))
	}
	tables, err := s.waListTables(ctx, c.tenant.ID)
	if err != nil {
		return err
	}
	match := matchTables(tables, name)
	switch {
	case len(match) == 0:
		return c.say(ctx, s, c.t("wa.table.no_match", "name", fmt.Sprintf("%q", whatsapp.SafeText(name))))
	case len(match) > 1:
		match = match[:min(waPickMax, len(match))]
		data := chatData{Pick: make([]uuid.UUID, len(match))}
		for i, t := range match {
			data.Pick[i] = t.ID
		}
		if err := s.saveSession(ctx, c, stateCollecting, data, waPendingTTL); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.table.which", "list", numberedTables(match)))
	}
	return s.waTableStart(ctx, c, match[0])
}

// waTableStart begins collecting a row for t: a form where Flows are set
// up (the first ten columns, required ones first; the rest follow in
// chat), else column by column in chat.
func (s *Server) waTableStart(ctx context.Context, c *chat, t datatables.Table) error {
	fields, _, err := waTableFields(t, c.lang.Tag)
	if err != nil {
		return err
	}
	data := chatData{Table: t.ID, Name: tableTitle(t), Row: map[string]any{}}
	if sent, err := s.waSendInputsFlow(ctx, c, data, fields[:min(whatsapp.FlowSlots, len(fields))]); err != nil || sent {
		return err
	}
	if err := s.saveSession(ctx, c, stateCollecting, data, waPendingTTL); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.table.needs_values", "table", whatsapp.SafeText(data.Name), "count", strconv.Itoa(len(fields)), "prompt", waTablePrompt(c, fields, 0)))
}

// waTableFields are a table's columns as chat fields, in the order they
// are asked: the table's own order, or, when the table has more columns
// than a form holds, the required ones first so a form takes those. The
// fields are built from a JSON schema of the columns, so the same prompts,
// parsing and form code serve a workflow's inputs and a table's columns;
// the table's own type checks (datatables.CheckValue) come after.
func waTableFields(t datatables.Table, l lang.Tag) ([]whatsapp.Field, []datatables.Column, error) {
	cols := slices.Clone(t.Columns)
	if len(cols) > whatsapp.FlowSlots {
		slices.SortStableFunc(cols, func(a, b datatables.Column) int {
			switch {
			case a.Required == b.Required:
				return 0
			case a.Required:
				return -1
			}
			return 1
		})
	}
	props := map[string]any{}
	required := make([]any, 0, len(cols))
	for _, col := range cols {
		p := map[string]any{"title": tableColumnLabel(col), "type": "string"}
		switch col.Type {
		case "integer", "number", "boolean":
			p["type"] = col.Type
		}
		if hint := tableHint(col, l); hint != "" {
			p["description"] = hint
		}
		if col.Personal {
			p["x-pii"] = tableCategory(col)
		}
		props[col.Name] = p
		required = append(required, col.Name)
	}
	schema, _ := json.Marshal(map[string]any{"type": "object", "properties": props, "required": required})
	fields, err := whatsapp.InputFields(schema, nil)
	if err != nil {
		return nil, nil, err
	}
	for i := range fields {
		fields[i].Required = cols[i].Required
	}
	return fields, cols, nil
}

func tableColumnLabel(col datatables.Column) string {
	if col.Title != "" {
		return col.Title
	}
	return col.Name
}

// tableHint says what a column of a type takes, under its question and in
// its form field; the simple types need none.
func tableHint(col datatables.Column, l lang.Tag) string {
	switch col.Type {
	case "date", "datetime", "phone", "email", "json":
		return tr(l, "wa.table.hint."+col.Type)
	}
	return ""
}

// tableCategory is the category a personal column's values are sealed
// and masked under: phone numbers show their last four digits, emails
// their first letter, other text its first letter, anything else nothing.
func tableCategory(col datatables.Column) string {
	switch col.Type {
	case "phone", "email":
		return col.Type
	case "text":
		return "name"
	}
	return "personal"
}

// waTablePrompt asks for the i-th column.
func waTablePrompt(c *chat, fields []whatsapp.Field, i int) string {
	f := fields[i]
	s := fmt.Sprintf("%d/%d. %s", i+1, len(fields), f.PromptIn(c.lang.Tag))
	if !f.Required {
		s += "\n" + c.t("wa.table.optional")
	}
	return s
}

// isSkip reports an answer that leaves an optional column empty.
func isSkip(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "skip", "-", "none":
		return true
	}
	return false
}

// waTableCollect takes a message while a row is collected: the number of
// a table picked from a list, or the answer for the next column.
func (s *Server) waTableCollect(ctx context.Context, c *chat, text string) error {
	if len(c.data.Pick) > 0 {
		return s.waTablePick(ctx, c, text)
	}
	t, err := s.waGetTable(ctx, c.tenant.ID, c.data.Table)
	if err != nil {
		return s.waTableGone(ctx, c, err)
	}
	fields, cols, err := waTableFields(t, c.lang.Tag)
	if err != nil {
		return err
	}
	if c.data.Flow != "" {
		// Writing instead of using the form: the form is dropped, and the
		// columns are asked here one by one.
		data := c.data
		s.waDropFlow(ctx, c.tenant.ID, data.Flow)
		data.Flow, data.Index, data.Row = "", 0, map[string]any{}
		if err := s.saveSession(ctx, c, stateCollecting, data, waPendingTTL); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.run.form_closed", "prompt", waTablePrompt(c, fields, 0)))
	}
	if c.data.Index >= len(fields) {
		return s.waTableAskConfirm(ctx, c, c.data)
	}
	f, col := fields[c.data.Index], cols[c.data.Index]
	var v any
	if col.Required || !isSkip(text) {
		parsed, perr := f.ParseIn(text, c.lang.Tag)
		if perr == nil {
			// The table's own check, as the API makes it.
			v, perr = datatables.CheckValue(col, parsed)
		}
		if perr == nil && v == nil && col.Required {
			perr = fmt.Errorf("%s is required", tableColumnLabel(col))
		}
		if perr != nil {
			reason := strings.TrimPrefix(perr.Error(), "invalid: ")
			return c.say(ctx, s, c.t("wa.run.does_not_fit", "reason", whatsapp.SafeText(reason), "prompt", waTablePrompt(c, fields, c.data.Index)))
		}
	}
	data := c.data
	if data.Row == nil {
		data.Row = map[string]any{}
	}
	// Personal data waits sealed, like a run's inputs.
	if col.Personal && v != nil {
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
			env, err := s.Store.PII.SealTx(ctx, tx, c.tenant.ID, tableCategory(col), v)
			v = env
			return err
		})
		if err != nil {
			return err
		}
	}
	data.Row[col.Name] = v
	data.Index++
	if data.Index < len(fields) {
		if err := s.saveSession(ctx, c, stateCollecting, data, waPendingTTL); err != nil {
			return err
		}
		return c.say(ctx, s, waTablePrompt(c, fields, data.Index))
	}
	return s.waTableAskConfirm(ctx, c, data)
}

// waTablePick takes the number of a table from the list offered.
func (s *Server) waTablePick(ctx context.Context, c *chat, text string) error {
	tables, err := s.waListTables(ctx, c.tenant.ID)
	if err != nil {
		return err
	}
	var offered []datatables.Table
	for _, id := range c.data.Pick {
		if i := slices.IndexFunc(tables, func(t datatables.Table) bool { return t.ID == id }); i >= 0 {
			offered = append(offered, tables[i])
		}
	}
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || n < 1 || n > len(offered) {
		return c.say(ctx, s, c.t("wa.table.pick_again", "list", numberedTables(offered)))
	}
	if !c.p.Can(PermTableEdit) {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.table.denied_now", "org", c.tenant.Name))
	}
	return s.waTableStart(ctx, c, offered[n-1])
}

// waTableGone ends a session whose table is no longer there.
func (s *Server) waTableGone(ctx context.Context, c *chat, err error) error {
	if !errors.Is(err, datatables.ErrNotFound) {
		return err
	}
	name := c.data.Name
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.table.gone", "table", whatsapp.SafeText(name)))
}

// waTableFormDone takes a completed row form: the columns the form held,
// then the rest in chat when the table has more than a form holds, else
// the summary.
func (s *Server) waTableFormDone(ctx context.Context, c *chat, data chatData, inputs map[string]any) error {
	t, err := s.waGetTable(ctx, c.tenant.ID, data.Table)
	if err != nil {
		return s.waTableGone(ctx, c, err)
	}
	fields, _, err := waTableFields(t, c.lang.Tag)
	if err != nil {
		return err
	}
	data.Row, data.Index = inputs, min(whatsapp.FlowSlots, len(fields))
	if data.Index < len(fields) {
		if err := s.saveSession(ctx, c, stateCollecting, data, waPendingTTL); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.table.form_more", "count", strconv.Itoa(len(fields)-data.Index), "prompt", waTablePrompt(c, fields, data.Index)))
	}
	return s.waTableAskConfirm(ctx, c, data)
}

func addButtons(c *chat) []whatsapp.Button {
	return []whatsapp.Button{{ID: "yes", Title: c.t("button.yes_add")}, {ID: "cancel", Title: c.t("button.cancel")}}
}

// waTableAskConfirm shows the row, personal values masked, and waits for
// an explicit yes. The nonce taken here is the insert's idempotency key.
func (s *Server) waTableAskConfirm(ctx context.Context, c *chat, data chatData) error {
	t, err := s.waGetTable(ctx, c.tenant.ID, data.Table)
	if err != nil {
		return s.waTableGone(ctx, c, err)
	}
	data.Nonce = uuid.NewString()
	if err := s.saveSession(ctx, c, stateConfirming, data, waPendingTTL); err != nil {
		return err
	}
	var lines []string
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		lines = waTableLines(t.Columns, data.Row, func(env map[string]any) (any, error) { return s.Store.PII.OpenTx(ctx, tx, c.tenant.ID, env) })
		return nil
	})
	if err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.table.confirm", "table", whatsapp.SafeText(data.Name), "row", strings.Join(lines, "\n")), addButtons(c)...)
}

// waTableLines renders a row for a message, one column a line: personal
// values masked by their column's category (sealed ones opened only to
// be masked), the rest through the redactor, an empty one as a dash.
func waTableLines(cols []datatables.Column, row map[string]any, open whatsapp.Opener) []string {
	lines := make([]string, 0, len(cols))
	for _, col := range cols {
		lines = append(lines, tableColumnLabel(col)+": "+waTableValue(col, row[col.Name], open))
	}
	return lines
}

// waTableValue is one value as a message shows it.
func waTableValue(col datatables.Column, v any, open whatsapp.Opener) string {
	if v == nil {
		return "-"
	}
	if env, ok := v.(map[string]any); ok && pii.IsEnvelope(env) {
		if open == nil {
			return whatsapp.MaskValue(tableCategory(col), nil)
		}
		plain, err := open(env)
		if err != nil {
			return "[hidden]"
		}
		return whatsapp.MaskValue(tableCategory(col), cell(plain))
	}
	if col.Personal {
		return whatsapp.MaskValue(tableCategory(col), cell(v))
	}
	s := whatsapp.SafeText(cell(v))
	if r := []rune(s); len(r) > 80 {
		s = string(r[:79]) + "…"
	}
	return s
}

// waTableInsert adds the confirmed row through the same path as the API:
// table.edit, the columns' checks, insert_rows under the session's nonce
// as the idempotency key (a yes repeated after a lost answer replays the
// first attempt), audited without the row's values.
func (s *Server) waTableInsert(ctx context.Context, c *chat) error {
	data := c.data
	if !c.p.Can(PermTableEdit) {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.table.denied_now", "org", c.tenant.Name))
	}
	if !s.limiter("wa-table:"+c.addr, 20*time.Second, 3).Allow() {
		return c.say(ctx, s, c.t("wa.table.too_many"))
	}
	t, err := s.waGetTable(ctx, c.tenant.ID, data.Table)
	if err != nil {
		return s.waTableGone(ctx, c, err)
	}
	var row map[string]any
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		opened, err := pii.Open(ctx, s.Store.PII, tx, c.tenant.ID, data.Row, pii.Taint{})
		row, _ = opened.(map[string]any)
		return err
	}); err != nil {
		return err
	}
	checked, err := datatables.CheckRow(t.Columns, row, true, false)
	if err != nil {
		// The columns changed under the row.
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.table.not_added", "table", whatsapp.SafeText(data.Name), "reason", whatsapp.SafeText(strings.TrimPrefix(err.Error(), "invalid: "))))
	}
	tctx := s.waTablesCtx(ctx, c)
	tr, err := s.tableRowsFor(tctx, c.tenant.ID, t, tablesChatPurpose)
	if err != nil {
		s.Logger.Warn("whatsapp: the data tables connection", "err", err)
		return c.say(ctx, s, c.t("wa.table.unavailable"))
	}
	out, err := tr.call(tctx, s, "insert_rows", map[string]any{"table": t.Name, "rows": []any{checked}}, "wa-"+data.Nonce)
	if err != nil {
		// The session stays as it is: a yes again sends the same key.
		return c.say(ctx, s, c.t("wa.table.add_failed", "reason", tablesChatReason(err)))
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	id := ""
	if rows := rowsOf(out); len(rows) > 0 {
		if m, ok := rows[0].(map[string]any); ok {
			id = cell(m["id"])
		}
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		return waAudit(ctx, tx, c.ch.Name(), c.p, "table.row.add", t.Name, map[string]any{"table_id": t.ID, "row_id": id, "replayed": out["replayed"] == true})
	}); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.table.added", "id", id, "table", whatsapp.SafeText(data.Name)))
}

// tablesChatReason says, in a few words, why Xenibase refused a row.
func tablesChatReason(err error) string {
	var pe *xenibase.Error
	var xe *datatables.Error
	msg := "Xenibase could not be reached"
	switch {
	case errors.As(err, &pe):
		msg = pe.Message
		if pe.Code != "" {
			msg = pe.Code + ": " + pe.Message
		}
	case errors.As(err, &xe):
		msg = xe.Code + ": " + xe.Message
	case errors.Is(err, datatables.ErrNotFound):
		msg = "the data_tables connection is missing"
	}
	msg = whatsapp.SafeText(msg)
	if r := []rune(msg); len(r) > 200 {
		msg = string(r[:199]) + "…"
	}
	return msg
}

// --- find ---

// waTableFind answers "find <table> <text>": the table is the longest
// name or title the words start with, the rest is the search (text alone
// on the table's first text column, or column:text), and up to five rows
// come back as short lines, personal values masked.
func (s *Server) waTableFind(ctx context.Context, c *chat, arg string) error {
	if !c.p.Can(PermTableRead) {
		return c.say(ctx, s, c.t("wa.table.denied_read", "org", c.tenant.Name))
	}
	words := strings.Fields(arg)
	if len(words) == 0 {
		return c.say(ctx, s, c.t("wa.table.find_what"))
	}
	tables, err := s.waListTables(ctx, c.tenant.ID)
	if err != nil {
		return err
	}
	if len(tables) == 0 {
		return c.say(ctx, s, c.t("wa.table.none", "org", c.tenant.Name, "link", s.waLink(c, "/tables")))
	}
	var match []datatables.Table
	text := ""
	for n := len(words); n > 0 && len(match) == 0; n-- {
		key := matchKey(strings.Join(words[:n], " "))
		for _, t := range tables {
			if matchKey(t.Name) == key || matchKey(t.Title) == key {
				match = append(match, t)
			}
		}
		text = strings.Join(words[n:], " ")
	}
	switch {
	case len(match) == 0:
		return c.say(ctx, s, c.t("wa.table.no_match", "name", fmt.Sprintf("%q", whatsapp.SafeText(arg))))
	case len(match) > 1:
		return c.say(ctx, s, c.t("wa.table.which_find", "list", numberedTables(match)))
	case text == "":
		return c.say(ctx, s, c.t("wa.table.find_what"))
	}
	t := match[0]
	title := whatsapp.SafeText(tableTitle(t))
	filter, ok := searchFilter(t.Columns, text)
	if !ok {
		return c.say(ctx, s, c.t("wa.table.find_no_column", "table", title))
	}
	tctx := s.waTablesCtx(ctx, c)
	tr, err := s.tableRowsFor(tctx, c.tenant.ID, t, tablesChatPurpose)
	if err != nil {
		s.Logger.Warn("whatsapp: the data tables connection", "err", err)
		return c.say(ctx, s, c.t("wa.table.unavailable"))
	}
	out, err := tr.call(tctx, s, "read_rows", map[string]any{"table": t.Name, "limit": waFindRows, "filters": []any{filter},
		"order": []any{map[string]any{"column": "id", "desc": true}}, "count": "exact"}, "")
	if err != nil {
		s.Logger.Warn("whatsapp: reading rows", "table", t.Name, "err", err)
		return c.say(ctx, s, c.t("wa.table.unavailable"))
	}
	rows := rowsOf(out)
	quoted := fmt.Sprintf("%q", whatsapp.SafeText(text))
	if len(rows) == 0 {
		return c.say(ctx, s, c.t("wa.table.find_none", "table", title, "text", quoted))
	}
	lines := []string{c.t("wa.table.find_head", "table", title, "text", quoted)}
	for _, r := range rows {
		m, _ := r.(map[string]any)
		lines = append(lines, waTableRowLine(t.Columns, m))
	}
	if more := countOf(out) - int64(len(rows)); more > 0 {
		lines = append(lines, c.t("wa.table.find_more", "count", strconv.FormatInt(more, 10)))
	}
	return c.say(ctx, s, strings.Join(lines, "\n"))
}

// waTableRowLine is one found row on a line: its id and its columns that
// have a value, personal ones masked.
func waTableRowLine(cols []datatables.Column, row map[string]any) string {
	parts := []string{"• #" + cell(row["id"])}
	for _, col := range cols {
		v := row[col.Name]
		if v == nil {
			continue
		}
		s := waTableValue(col, v, nil)
		if r := []rune(s); len(r) > 40 {
			s = string(r[:39]) + "…"
		}
		parts = append(parts, tableColumnLabel(col)+": "+s)
	}
	return strings.Join(parts, " · ")
}

// countOf reads a read_rows count, as the connector or the fake gives it.
func countOf(out map[string]any) int64 {
	switch n := out["count"].(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case json.Number:
		v, _ := n.Int64()
		return v
	}
	return 0
}

// --- the form ---

// waFlowTableInputs answers a row form: its screen on opening, or the
// submitted answers checked column by column as the API checks a row,
// kept (personal ones sealed) until the person confirms in chat.
func (s *Server) waFlowTableInputs(ctx context.Context, row flowRow, req whatsapp.FlowRequest) (any, error) {
	title, _ := row.data["title"].(string)
	tid, _ := row.data["table"].(string)
	id, err := uuid.Parse(tid)
	if err != nil {
		return nil, fmt.Errorf("a row form without its table: %w", err)
	}
	l := s.chooseLang(ctx, row.tenant, row.number, "").Tag
	t, err := s.waGetTable(ctx, row.tenant, id)
	if errors.Is(err, datatables.ErrNotFound) {
		d, err := whatsapp.FlowForm(title, nil, nil)
		if err != nil {
			return nil, err
		}
		d["error"], d["has_error"] = tr(l, "wa.table.gone", "table", title), true
		return flowScreen(whatsapp.ScreenInputs, d), nil
	}
	if err != nil {
		return nil, err
	}
	fields, cols, err := waTableFields(t, l)
	if err != nil {
		return nil, err
	}
	n := min(whatsapp.FlowSlots, len(fields))
	fields, cols = fields[:n], cols[:n]
	if req.Action != "data_exchange" {
		d, err := whatsapp.FlowForm(title, fields, nil)
		if err != nil {
			return nil, err
		}
		return flowScreen(whatsapp.ScreenInputs, d), nil
	}
	vals, errs := whatsapp.FlowValues(fields, req.Data)
	for i, f := range fields {
		v, ok := vals[f.Name]
		if !ok || errs[f.Name] != "" {
			continue
		}
		x, err := datatables.CheckValue(cols[i], v)
		switch {
		case err != nil:
			delete(vals, f.Name)
			errs[f.Name] = strings.TrimPrefix(err.Error(), "invalid: ")
		case x == nil:
			delete(vals, f.Name)
			if cols[i].Required {
				errs[f.Name] = "this is required"
			}
		default:
			vals[f.Name] = x
		}
	}
	if len(errs) > 0 {
		d, err := whatsapp.FlowForm(title, fields, errs)
		if err != nil {
			return nil, err
		}
		return flowScreen(whatsapp.ScreenInputs, d), nil
	}
	// Personal data waits sealed, as in chat.
	sealed := map[string]any{}
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{row.tenant}, func(tx pgx.Tx) error {
		for i, f := range fields {
			v, ok := vals[f.Name]
			if !ok {
				continue
			}
			if cols[i].Personal {
				env, err := s.Store.PII.SealTx(ctx, tx, row.tenant, tableCategory(cols[i]), v)
				if err != nil {
					return err
				}
				sealed[f.Name] = env
				continue
			}
			sealed[f.Name] = v
		}
		raw, _ := json.Marshal(map[string]any{"title": title, "table": tid, "inputs": sealed})
		_, err := tx.Exec(ctx, `UPDATE whatsapp_flows SET status = 'submitted', data = $3, updated_at = now() WHERE token_hash = $1 AND tenant_id = $2 AND status = 'open'`,
			row.hash, row.tenant, raw)
		return err
	})
	if err != nil {
		return nil, err
	}
	return flowDone(req.FlowToken), nil
}
