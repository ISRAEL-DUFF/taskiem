package api

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Rows changed and removed in chat (docs/spec/no-sheet-needed.md §6.3;
// decision 0060, R4). "find <table> <text>" numbers the rows it shows and
// keeps them in the session: *more* shows the next five, *change 2* shows
// row 2 again, read fresh, asks which detail and its new value (several
// may be changed), and *delete 2* shows it for removal; each is written
// only after an explicit yes.
//
// A row has no version: nothing changes updated_at after a row is made.
// So a write names the row by its id and by the values the person was
// shown, read when they said change or delete: an update by the value of
// every column it changes, a removal by every value. A row changed since
// matches nothing, nothing is written, and the row is shown again as it
// is now. A change to other columns is kept, since an update sets only
// its own. The guard can only refuse a write it cannot prove safe, never
// let one through: a value Xenibase compares differently from how it
// sent it (a JSON object's key order, say) refuses, and the person is
// shown the row again.

// chatFound is the rows a find showed, numbered from 1, and a change or
// removal being prepared on one of them.
type chatFound struct {
	Table uuid.UUID `json:"table"`
	Title string    `json:"title"`
	// Text is the search; Last the smallest id shown, where *more* goes on.
	Text string  `json:"text"`
	Rows []int64 `json:"rows"`
	Last int64   `json:"last,omitempty"`
	// Op is change or delete while one is prepared; Phase is column (which
	// detail), value (its new value) or confirm.
	Op    string `json:"op,omitempty"`
	Phase string `json:"phase,omitempty"`
	Row   int64  `json:"row,omitempty"`
	// Shown are the row's values when it was shown and Set the new ones,
	// personal ones sealed in both.
	Shown  map[string]any `json:"shown,omitempty"`
	Set    map[string]any `json:"set,omitempty"`
	Column string         `json:"column,omitempty"`
	// Nonce is the write's idempotency key, new for each read-back.
	Nonce string `json:"nonce,omitempty"`
}

var (
	changeRowRe = regexp.MustCompile(`^(?:change|edit|correct|update)\s+(?:row\s+)?(\d{1,3})$`)
	deleteRowRe = regexp.MustCompile(`^(?:delete|remove)\s+(?:row\s+)?(\d{1,3})$`)
)

// waFindPage shows the rows of t matching text, newest first, five at a
// time (those below before when going on), numbered; the page stays in
// the session so the numbers mean something.
func (s *Server) waFindPage(ctx context.Context, c *chat, t datatables.Table, text string, before int64) error {
	title := whatsapp.SafeText(tableTitle(t))
	filter, ok := searchFilter(t.Columns, text)
	if !ok {
		return c.say(ctx, s, c.t("wa.table.find_no_column", "table", title))
	}
	filters := []any{filter}
	if before > 0 {
		filters = append(filters, map[string]any{"column": "id", "op": "lt", "value": before})
	}
	tctx := s.waTablesCtx(ctx, c)
	tr, err := s.tableRowsFor(tctx, c.tenant.ID, t, tablesChatPurpose)
	if err != nil {
		s.Logger.Warn("whatsapp: the data tables connection", "err", err)
		return c.say(ctx, s, c.t("wa.table.unavailable"))
	}
	out, err := tr.call(tctx, s, "read_rows", map[string]any{"table": t.Name, "limit": waFindRows, "filters": filters,
		"order": []any{map[string]any{"column": "id", "desc": true}}, "count": "exact"}, "")
	if err != nil {
		s.Logger.Warn("whatsapp: reading rows", "table", t.Name, "err", err)
		return c.say(ctx, s, c.t("wa.table.unavailable"))
	}
	rows := rowsOf(out)
	quoted := strconv.Quote(whatsapp.SafeText(text))
	if len(rows) == 0 {
		if before > 0 {
			if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
				return err
			}
			return c.say(ctx, s, c.t("wa.rows.no_more", "table", title))
		}
		return c.say(ctx, s, c.t("wa.table.find_none", "table", title, "text", quoted))
	}
	f := &chatFound{Table: t.ID, Title: tableTitle(t), Text: text}
	lines := []string{c.t("wa.table.find_head", "table", title, "text", quoted)}
	for i, r := range rows {
		m, _ := r.(map[string]any)
		id, _ := strconv.ParseInt(cell(m["id"]), 10, 64)
		f.Rows = append(f.Rows, id)
		lines = append(lines, strconv.Itoa(i+1)+". "+waTableRowLine(t.Columns, m))
	}
	f.Last = slices.Min(f.Rows)
	if more := countOf(out) - int64(len(rows)); more > 0 {
		lines = append(lines, c.t("wa.table.find_more", "count", strconv.FormatInt(more, 10)))
	}
	if c.p.Can(PermTableEdit) && !t.Kept() {
		lines = append(lines, "", c.t("wa.rows.find_tail"))
	}
	if err := s.saveSession(ctx, c, stateCollecting, chatData{Found: f}, waPendingTTL); err != nil {
		return err
	}
	return c.say(ctx, s, strings.Join(lines, "\n"))
}

// waFoundReply takes a message while found rows are kept. With only the
// list kept, *more*, *change n* and *delete n* are read and anything else
// lets the list go and is read as a new message (false); while a change or
// removal is prepared, every message is its.
func (s *Server) waFoundReply(ctx context.Context, c *chat, text, cmd string) (bool, error) {
	f := c.data.Found
	if f.Op != "" {
		return true, s.waRowReply(ctx, c, f, text, cmd)
	}
	switch {
	case cmd == "more" || cmd == "next":
		t, err := s.waGetTable(ctx, c.tenant.ID, f.Table)
		if err != nil {
			return true, s.waRowTableGone(ctx, c, f, err)
		}
		return true, s.waFindPage(ctx, c, t, f.Text, f.Last)
	case changeRowRe.MatchString(cmd):
		n, _ := strconv.Atoi(changeRowRe.FindStringSubmatch(cmd)[1])
		return true, s.waRowStart(ctx, c, f, "change", n)
	case deleteRowRe.MatchString(cmd):
		n, _ := strconv.Atoi(deleteRowRe.FindStringSubmatch(cmd)[1])
		return true, s.waRowStart(ctx, c, f, "delete", n)
	}
	return false, s.saveSession(ctx, c, stateIdle, chatData{}, 0)
}

// waRowTableGone ends the session when the table is no longer there.
func (s *Server) waRowTableGone(ctx context.Context, c *chat, f *chatFound, err error) error {
	if !errors.Is(err, datatables.ErrNotFound) {
		return err
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.rows.table_gone", "table", whatsapp.SafeText(f.Title)))
}

// waRowStart answers *change n* or *delete n*: the row is read fresh and
// shown, and what it shows is what the write will be guarded by.
func (s *Server) waRowStart(ctx context.Context, c *chat, f *chatFound, op string, n int) error {
	if !c.p.Can(PermTableEdit) {
		return c.say(ctx, s, c.t("wa.rows.denied", "org", c.tenant.Name))
	}
	if n < 1 || n > len(f.Rows) {
		return c.say(ctx, s, c.t("wa.rows.no_number", "n", strconv.Itoa(n), "max", strconv.Itoa(len(f.Rows))))
	}
	t, err := s.waGetTable(ctx, c.tenant.ID, f.Table)
	if err != nil {
		return s.waRowTableGone(ctx, c, f, err)
	}
	if t.Kept() {
		return s.waTableKept(ctx, c, t)
	}
	f.Op, f.Row, f.Set = op, f.Rows[n-1], nil
	return s.waRowShow(ctx, c, f, t, "")
}

// waRowShow reads the row again and asks: which detail to change, or
// whether to remove it. lead, when set, is said first (the row changed).
func (s *Server) waRowShow(ctx context.Context, c *chat, f *chatFound, t datatables.Table, lead string) error {
	row, found, err := s.waReadRow(ctx, c, t, f.Row)
	if err != nil {
		s.Logger.Warn("whatsapp: reading a row", "table", t.Name, "err", err)
		return c.say(ctx, s, c.t("wa.table.unavailable"))
	}
	if !found {
		return s.waRowGone(ctx, c, f)
	}
	shown, err := s.waSealRow(ctx, c, t.Columns, row)
	if err != nil {
		return err
	}
	f.Shown, f.Set, f.Column = shown, nil, ""
	id, title := strconv.FormatInt(f.Row, 10), whatsapp.SafeText(tableTitle(t))
	if lead != "" {
		lead += "\n\n"
	}
	if f.Op == "delete" {
		f.Phase, f.Nonce = "confirm", uuid.NewString()
		if err := s.saveSession(ctx, c, stateCollecting, chatData{Found: f}, waPendingTTL); err != nil {
			return err
		}
		lines, err := s.waRowLines(ctx, c, t.Columns, f.Shown, false)
		if err != nil {
			return err
		}
		return c.say(ctx, s, lead+c.t("wa.rows.confirm_delete", "id", id, "table", title, "row", strings.Join(lines, "\n")), tableYesButtons(c)...)
	}
	f.Phase = "column"
	if err := s.saveSession(ctx, c, stateCollecting, chatData{Found: f}, waPendingTTL); err != nil {
		return err
	}
	lines, err := s.waRowLines(ctx, c, t.Columns, f.Shown, true)
	if err != nil {
		return err
	}
	return c.say(ctx, s, lead+c.t("wa.rows.pick_column", "id", id, "table", title, "row", strings.Join(lines, "\n")))
}

// waRowGone says the row is no longer there; the list stays.
func (s *Server) waRowGone(ctx context.Context, c *chat, f *chatFound) error {
	id := strconv.FormatInt(f.Row, 10)
	f.Op, f.Phase, f.Row, f.Shown, f.Set, f.Column, f.Nonce = "", "", 0, nil, nil, "", ""
	if err := s.saveSession(ctx, c, stateCollecting, chatData{Found: f}, waPendingTTL); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.rows.gone", "id", id, "table", whatsapp.SafeText(f.Title)))
}

// waReadRow reads one row by its id.
func (s *Server) waReadRow(ctx context.Context, c *chat, t datatables.Table, id int64) (map[string]any, bool, error) {
	tctx := s.waTablesCtx(ctx, c)
	tr, err := s.tableRowsFor(tctx, c.tenant.ID, t, tablesChatPurpose)
	if err != nil {
		return nil, false, err
	}
	out, err := tr.call(tctx, s, "read_rows", map[string]any{"table": t.Name, "limit": 1,
		"filters": []any{map[string]any{"column": "id", "op": "eq", "value": id}}}, "")
	if err != nil {
		return nil, false, err
	}
	rows := rowsOf(out)
	if len(rows) == 0 {
		return nil, false, nil
	}
	m, _ := rows[0].(map[string]any)
	return m, m != nil, nil
}

// waSealRow keeps a row's column values for the session, personal ones
// sealed as the values of a row being added are.
func (s *Server) waSealRow(ctx context.Context, c *chat, cols []datatables.Column, row map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(cols))
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		for _, col := range cols {
			v := row[col.Name]
			if col.Personal && v != nil {
				env, err := s.Store.PII.SealTx(ctx, tx, c.tenant.ID, tableCategory(col), v)
				if err != nil {
					return err
				}
				v = env
			}
			out[col.Name] = v
		}
		return nil
	})
	return out, err
}

// waRowLines are a row's values as a message shows them, personal ones
// masked, numbered when a detail is to be picked.
func (s *Server) waRowLines(ctx context.Context, c *chat, cols []datatables.Column, row map[string]any, numbered bool) ([]string, error) {
	var lines []string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		lines = waTableLines(cols, row, func(env map[string]any) (any, error) { return s.Store.PII.OpenTx(ctx, tx, c.tenant.ID, env) })
		return nil
	})
	if numbered {
		for i := range lines {
			lines[i] = strconv.Itoa(i+1) + ". " + lines[i]
		}
	}
	return lines, err
}

// waRowReply takes a message while a change or removal is prepared.
func (s *Server) waRowReply(ctx context.Context, c *chat, f *chatFound, text, cmd string) error {
	if !c.p.Can(PermTableEdit) {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.rows.denied", "org", c.tenant.Name))
	}
	t, err := s.waGetTable(ctx, c.tenant.ID, f.Table)
	if err != nil {
		return s.waRowTableGone(ctx, c, f, err)
	}
	if t.Kept() {
		return s.waTableKept(ctx, c, t)
	}
	pick := func() (datatables.Column, bool) {
		n, err := strconv.Atoi(strings.TrimSpace(text))
		if err != nil || n < 1 || n > len(t.Columns) {
			return datatables.Column{}, false
		}
		return t.Columns[n-1], true
	}
	switch f.Phase {
	case "column":
		col, ok := pick()
		if !ok {
			return c.say(ctx, s, c.t("wa.rows.pick_again", "max", strconv.Itoa(len(t.Columns))))
		}
		return s.waRowAskValue(ctx, c, f, t, col)
	case "value":
		if c.data.Flow != "" {
			// Written instead of using the form: the form is dropped and
			// the message taken as the value.
			s.waDropFlow(ctx, c.tenant.ID, c.data.Flow)
		}
		return s.waRowValue(ctx, c, f, t, text)
	case "confirm":
		switch cmd {
		case "yes", "y", "ok", "okay", "save", "remove", "delete":
			return s.waRowWrite(ctx, c, f, t)
		case "no", "n":
			f.Op, f.Phase, f.Shown, f.Set, f.Column, f.Nonce = "", "", nil, nil, "", ""
			if err := s.saveSession(ctx, c, stateCollecting, chatData{Found: f}, waPendingTTL); err != nil {
				return err
			}
			return c.say(ctx, s, c.t("wa.rows.left"))
		}
		if f.Op == "change" {
			if col, ok := pick(); ok {
				return s.waRowAskValue(ctx, c, f, t, col)
			}
		}
		return c.say(ctx, s, c.t("wa.rows.reminder"), tableYesButtons(c)...)
	}
	return s.saveSession(ctx, c, stateIdle, chatData{}, 0)
}

// waRowField is a column's chat field, as add to asks it.
func waRowField(t datatables.Table, col datatables.Column, c *chat) (whatsapp.Field, error) {
	fields, cols, err := waTableFields(t, c.lang.Tag)
	if err != nil {
		return whatsapp.Field{}, err
	}
	i := slices.IndexFunc(cols, func(x datatables.Column) bool { return x.Name == col.Name })
	return fields[i], nil
}

// waRowAskValue asks a detail's new value: a personal one in a form where
// WhatsApp Flows are set up, so it does not stay in the chat's history,
// else in chat as add to asks it.
func (s *Server) waRowAskValue(ctx context.Context, c *chat, f *chatFound, t datatables.Table, col datatables.Column) error {
	field, err := waRowField(t, col, c)
	if err != nil {
		return err
	}
	f.Phase, f.Column = "value", col.Name
	if col.Personal {
		data := chatData{Found: f, Table: t.ID, Name: tableTitle(t)}
		if sent, err := s.waSendInputsFlow(ctx, c, data, []whatsapp.Field{field}); err != nil || sent {
			return err
		}
	}
	if err := s.saveSession(ctx, c, stateCollecting, chatData{Found: f}, waPendingTTL); err != nil {
		return err
	}
	prompt := field.PromptIn(c.lang.Tag)
	if !col.Required {
		prompt += "\n" + c.t("wa.table.optional")
	}
	return c.say(ctx, s, c.t("wa.rows.ask_value", "prompt", prompt))
}

// waRowValue takes the new value written in chat, checked as the API
// checks it.
func (s *Server) waRowValue(ctx context.Context, c *chat, f *chatFound, t datatables.Table, text string) error {
	i := slices.IndexFunc(t.Columns, func(x datatables.Column) bool { return x.Name == f.Column })
	if i < 0 {
		// The column went while its value was asked.
		return s.waRowShow(ctx, c, f, t, "")
	}
	col := t.Columns[i]
	field, err := waRowField(t, col, c)
	if err != nil {
		return err
	}
	var v any
	if col.Required || !isSkip(text) {
		parsed, perr := field.ParseIn(text, c.lang.Tag)
		if perr == nil {
			v, perr = datatables.CheckValue(col, parsed)
		}
		if perr == nil && v == nil && col.Required {
			perr = errors.New(tableColumnLabel(col) + " is required")
		}
		if perr != nil {
			prompt := field.PromptIn(c.lang.Tag)
			if !col.Required {
				prompt += "\n" + c.t("wa.table.optional")
			}
			if err := s.saveSession(ctx, c, stateCollecting, chatData{Found: f}, waPendingTTL); err != nil {
				return err
			}
			return c.say(ctx, s, c.t("wa.run.does_not_fit", "reason", whatsapp.SafeText(strings.TrimPrefix(perr.Error(), "invalid: ")), "prompt", prompt))
		}
	}
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
	return s.waRowConfirm(ctx, c, f, t, col.Name, v)
}

// waRowFormDone takes a detail's new value from its completed form, where
// it was checked and, being personal, sealed.
func (s *Server) waRowFormDone(ctx context.Context, c *chat, data chatData, inputs map[string]any) error {
	f := data.Found
	t, err := s.waGetTable(ctx, c.tenant.ID, f.Table)
	if err != nil {
		return s.waRowTableGone(ctx, c, f, err)
	}
	return s.waRowConfirm(ctx, c, f, t, f.Column, inputs[f.Column])
}

// waRowConfirm adds a detail's new value to the change and reads the
// change back, each detail as it was and as it will be, for yes. Another
// detail's number changes that too.
func (s *Server) waRowConfirm(ctx context.Context, c *chat, f *chatFound, t datatables.Table, name string, v any) error {
	if f.Set == nil {
		f.Set = map[string]any{}
	}
	f.Set[name] = v
	f.Phase, f.Column, f.Nonce = "confirm", "", uuid.NewString()
	if err := s.saveSession(ctx, c, stateCollecting, chatData{Found: f}, waPendingTTL); err != nil {
		return err
	}
	var lines []string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		open := func(env map[string]any) (any, error) { return s.Store.PII.OpenTx(ctx, tx, c.tenant.ID, env) }
		for _, col := range t.Columns {
			nv, ok := f.Set[col.Name]
			if !ok {
				continue
			}
			lines = append(lines, tableColumnLabel(col)+": "+waTableValue(col, f.Shown[col.Name], open)+" → "+waTableValue(col, nv, open))
		}
		return nil
	})
	if err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.rows.confirm_change", "id", strconv.FormatInt(f.Row, 10), "table", whatsapp.SafeText(tableTitle(t)),
		"changes", strings.Join(lines, "\n")), tableYesButtons(c)...)
}

// rowGuard is a write's conditions: the row's id and, for each column
// named, the value read when the row was shown (an empty one as is null,
// a yes or no as is), so the write meets only the row the person saw.
func rowGuard(cols []datatables.Column, id int64, shown map[string]any, names []string) []any {
	out := []any{map[string]any{"column": "id", "op": "eq", "value": id}}
	for _, col := range cols {
		if !slices.Contains(names, col.Name) {
			continue
		}
		switch v := shown[col.Name]; {
		case v == nil:
			out = append(out, map[string]any{"column": col.Name, "op": "is_null"})
		case col.Type == "boolean":
			out = append(out, map[string]any{"column": col.Name, "op": "is", "value": v})
		default:
			out = append(out, map[string]any{"column": col.Name, "op": "eq", "value": v})
		}
	}
	return out
}

// affectedOf reads a write's affected count, as the connector or the fake
// gives it.
func affectedOf(out map[string]any) int64 {
	switch n := out["affected"].(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	case json.Number:
		v, _ := n.Int64()
		return v
	}
	return 0
}

// waRowWrite writes the change or the removal agreed, through the same
// path as the API's (table.edit, the columns' checks, the session's nonce
// as the idempotency key, so a yes repeated after a lost answer replays
// the first attempt), guarded by the values shown and audited without
// them. A row changed since it was shown is shown again as it is now.
func (s *Server) waRowWrite(ctx context.Context, c *chat, f *chatFound, t datatables.Table) error {
	if !s.limiter("wa-rows:"+c.addr, 20*time.Second, 3).Allow() {
		return c.say(ctx, s, c.t("wa.rows.too_many"))
	}
	var shown, set map[string]any
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		opened, err := pii.Open(ctx, s.Store.PII, tx, c.tenant.ID, f.Shown, pii.Taint{})
		if err != nil {
			return err
		}
		shown, _ = opened.(map[string]any)
		if f.Set != nil {
			opened, err = pii.Open(ctx, s.Store.PII, tx, c.tenant.ID, f.Set, pii.Taint{})
			set, _ = opened.(map[string]any)
		}
		return err
	}); err != nil {
		return err
	}
	in := map[string]any{"table": t.Name, "max_affected": 1}
	action, audit := "delete_rows", "table.row.delete"
	names := make([]string, 0, len(t.Columns))
	for _, col := range t.Columns {
		names = append(names, col.Name)
	}
	detail := map[string]any{"table_id": t.ID, "row_id": f.Row}
	if f.Op == "change" {
		checked, err := datatables.CheckRow(t.Columns, set, false, false)
		if err != nil {
			// The columns changed under the change.
			f.Op, f.Phase, f.Shown, f.Set, f.Nonce = "", "", nil, nil, ""
			if err := s.saveSession(ctx, c, stateCollecting, chatData{Found: f}, waPendingTTL); err != nil {
				return err
			}
			return c.say(ctx, s, c.t("wa.rows.not_saved", "reason", whatsapp.SafeText(strings.TrimPrefix(err.Error(), "invalid: "))))
		}
		action, audit, in["set"] = "update_rows", "table.row.update", checked
		names = names[:0]
		for k := range checked {
			names = append(names, k)
		}
		detail["columns"] = len(checked)
	}
	in["filters"] = rowGuard(t.Columns, f.Row, shown, names)
	tctx := s.waTablesCtx(ctx, c)
	tr, err := s.tableRowsFor(tctx, c.tenant.ID, t, tablesChatPurpose)
	if err != nil {
		s.Logger.Warn("whatsapp: the data tables connection", "err", err)
		return c.say(ctx, s, c.t("wa.table.unavailable"))
	}
	out, err := tr.call(tctx, s, action, in, "wa-"+f.Nonce)
	if err != nil {
		// The session stays as it is: a yes again sends the same key.
		return c.say(ctx, s, c.t("wa.rows.failed", "reason", tablesChatReason(err)), tableYesButtons(c)...)
	}
	id, title := strconv.FormatInt(f.Row, 10), whatsapp.SafeText(tableTitle(t))
	if affectedOf(out) == 0 {
		// Changed or removed since it was shown: nothing written.
		return s.waRowShow(ctx, c, f, t, c.t("wa.rows.changed_since", "id", id))
	}
	detail["replayed"] = out["replayed"] == true
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		return waAudit(ctx, tx, c.ch.Name(), c.p, audit, t.Name, detail)
	}); err != nil {
		return err
	}
	op := f.Op
	f.Op, f.Phase, f.Shown, f.Set, f.Column, f.Nonce = "", "", nil, nil, "", ""
	if err := s.saveSession(ctx, c, stateCollecting, chatData{Found: f}, waPendingTTL); err != nil {
		return err
	}
	if op == "delete" {
		return c.say(ctx, s, c.t("wa.rows.deleted", "id", id, "table", title))
	}
	return c.say(ctx, s, c.t("wa.rows.saved", "id", id, "table", title))
}
