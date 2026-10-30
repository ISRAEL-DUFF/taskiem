package api

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/templates"
)

// Tables made and changed in chat (docs/spec/no-sheet-needed.md §6.1,
// §6.2; decision 0060, R3). "new table" offers the known tables a
// business does not have yet, or makes one from the owner's own words:
// what the list is called, then what is written down for each one, each
// item a column typed from its words, read back and made after yes.
// "add column <name> to <table>" asks the type in plain words;
// "rename <table> to <title>" changes the title. Dropping a column or
// deleting a table deletes data, so chat sends those to the web app, where
// the name is typed to confirm (Q1 is the founder's: until it is decided,
// chat takes the plan's fallback). Every change goes through the code the
// Tables page uses, with table.edit; kept tables refuse.

// chatTableMake is a table being made or changed in chat.
type chatTableMake struct {
	// Kind: new (a table), column (a column added), retry (a new table
	// waiting for the records project).
	Kind string `json:"kind"`
	// Phase of a new table: pick (known or own), name, columns, confirm;
	// of a column: type, confirm.
	Phase   string              `json:"phase"`
	Name    string              `json:"name,omitempty"`
	Title   string              `json:"title,omitempty"`
	Columns []datatables.Column `json:"columns,omitempty"`
	// Known are the known tables offered, in the order listed.
	Known []string `json:"known,omitempty"`
	// Table and Column are a column being added.
	Table  uuid.UUID `json:"table,omitempty"`
	Column string    `json:"column,omitempty"`
}

// waTableRetryTTL is how long a table agreed in chat waits for its records
// project after provisioning failed (§9.1): the conversation window.
const waTableRetryTTL = 24 * time.Hour

// chatColumnTypes are the types chat offers for a column, in the order
// listed, each said in plain words.
var chatColumnTypes = []string{"text", "number", "integer", "boolean", "date", "phone", "email"}

// waNewTable answers "new table" (with or without a name).
func (s *Server) waNewTable(ctx context.Context, c *chat, arg string) error {
	if !c.p.Can(PermTableEdit) {
		return c.say(ctx, s, c.t("wa.tablemake.denied", "org", c.tenant.Name))
	}
	if _, err := s.tables(); err != nil {
		return c.say(ctx, s, c.t("wa.table.unavailable"))
	}
	tables, err := s.waListTables(ctx, c.tenant.ID)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, t := range tables {
		have[t.Name] = true
	}
	m := &chatTableMake{Kind: "new"}
	for _, kt := range templates.KnownTables() {
		if !have[kt.Name] {
			m.Known = append(m.Known, kt.Name)
		}
	}
	if arg = strings.TrimSpace(arg); arg != "" {
		// "new table customers": a known one by its name or title.
		if kt, ok := knownByWords(arg, m.Known); ok {
			return s.waTableMakeKnown(ctx, c, m, kt)
		}
		return s.waTableMakeName(ctx, c, m, arg)
	}
	if len(m.Known) == 0 {
		m.Phase = "name"
		if err := s.saveSession(ctx, c, stateCollecting, chatData{TableMake: m}, waPendingTTL); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.tablemake.ask_name"))
	}
	m.Phase = "pick"
	if err := s.saveSession(ctx, c, stateCollecting, chatData{TableMake: m}, waPendingTTL); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.tablemake.pick", "list", knownList(c, m.Known)))
}

// knownList numbers the known tables offered, then Something else.
func knownList(c *chat, known []string) string {
	var lines []string
	for i, n := range known {
		kt, _ := templates.KnownTable(n)
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, kt.Title))
	}
	lines = append(lines, fmt.Sprintf("%d. %s", len(known)+1, c.t("wa.tablemake.something_else")))
	return strings.Join(lines, "\n")
}

// knownByWords is the known table among offered that words name.
func knownByWords(words string, offered []string) (templates.Table, bool) {
	key := matchKey(words)
	for _, n := range offered {
		kt, _ := templates.KnownTable(n)
		if key == matchKey(kt.Name) || key == matchKey(kt.Title) {
			return kt, true
		}
	}
	return templates.Table{}, false
}

// waTableMakeKnown reads a known table back for yes.
func (s *Server) waTableMakeKnown(ctx context.Context, c *chat, m *chatTableMake, kt templates.Table) error {
	m.Name, m.Title, m.Columns = kt.Name, kt.Title, nil
	for _, col := range kt.Columns {
		dc := datatables.Column{Name: col.Name, Title: col.Title, Type: col.Type, Required: col.Required}
		if col.Personal != nil {
			dc.Personal = *col.Personal
		}
		if err := datatables.CheckColumn(&dc); err != nil {
			return err
		}
		m.Columns = append(m.Columns, dc)
	}
	return s.waTableMakeReadBack(ctx, c, m)
}

// waTableMakeName takes the list's title and asks its columns.
func (s *Server) waTableMakeName(ctx context.Context, c *chat, m *chatTableMake, title string) error {
	title = strings.TrimSpace(title)
	if len([]rune(title)) > 60 {
		return c.say(ctx, s, c.t("wa.tablemake.name_long"))
	}
	name := snakeName(title)
	if name == "" || datatables.CheckTableName(name) != nil {
		return c.say(ctx, s, c.t("wa.tablemake.name_bad"))
	}
	tables, err := s.waListTables(ctx, c.tenant.ID)
	if err != nil {
		return err
	}
	for _, t := range tables {
		if t.Name == name {
			return c.say(ctx, s, c.t("wa.tablemake.exists", "table", whatsapp.SafeText(tableTitle(t))))
		}
	}
	m.Name, m.Title, m.Phase = name, upperFirst(title), "columns"
	if err := s.saveSession(ctx, c, stateCollecting, chatData{TableMake: m}, waPendingTTL); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.tablemake.ask_columns", "table", whatsapp.SafeText(m.Title)))
}

// waTableMakeReadBack shows the table as it will be made and waits for yes.
func (s *Server) waTableMakeReadBack(ctx context.Context, c *chat, m *chatTableMake) error {
	m.Phase = "confirm"
	if err := s.saveSession(ctx, c, stateCollecting, chatData{TableMake: m}, waPendingTTL); err != nil {
		return err
	}
	var lines []string
	for i, col := range m.Columns {
		lines = append(lines, fmt.Sprintf("%d. %s: %s", i+1, whatsapp.SafeText(col.Title), c.t("wa.tablemake.type."+col.Type)))
	}
	return c.say(ctx, s, c.t("wa.tablemake.readback", "table", whatsapp.SafeText(m.Title), "columns", strings.Join(lines, "\n")), tableYesButtons(c)...)
}

// waTableMakeReply takes a message while a table is made or changed.
func (s *Server) waTableMakeReply(ctx context.Context, c *chat, text, cmd string) error {
	m := c.data.TableMake
	if !c.p.Can(PermTableEdit) {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.tablemake.denied", "org", c.tenant.Name))
	}
	switch m.Kind + ":" + m.Phase {
	case "new:pick":
		n, err := strconv.Atoi(strings.TrimSpace(text))
		if err != nil {
			if kt, ok := knownByWords(text, m.Known); ok {
				return s.waTableMakeKnown(ctx, c, m, kt)
			}
			return c.say(ctx, s, c.t("wa.table.pick_again", "list", knownList(c, m.Known)))
		}
		switch {
		case n >= 1 && n <= len(m.Known):
			kt, _ := templates.KnownTable(m.Known[n-1])
			return s.waTableMakeKnown(ctx, c, m, kt)
		case n == len(m.Known)+1:
			m.Phase = "name"
			if err := s.saveSession(ctx, c, stateCollecting, chatData{TableMake: m}, waPendingTTL); err != nil {
				return err
			}
			return c.say(ctx, s, c.t("wa.tablemake.ask_name"))
		}
		return c.say(ctx, s, c.t("wa.table.pick_again", "list", knownList(c, m.Known)))
	case "new:name":
		return s.waTableMakeName(ctx, c, m, text)
	case "new:columns", "new:confirm":
		if m.Phase == "confirm" {
			switch cmd {
			case "yes", "y", "make it", "ok", "okay":
				return s.waTableMakeNow(ctx, c, m)
			case "no", "n":
				if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
					return err
				}
				return c.say(ctx, s, c.t("wa.tablemake.not_made"))
			}
		}
		cols := columnsFromWords(text)
		if len(cols) == 0 {
			return c.say(ctx, s, c.t("wa.tablemake.no_columns"))
		}
		if err := datatables.CheckColumns(cols, 0); err != nil {
			return c.say(ctx, s, c.t("wa.tablemake.no_columns"))
		}
		m.Columns = cols
		return s.waTableMakeReadBack(ctx, c, m)
	case "retry:confirm":
		switch cmd {
		case "yes", "y", "try again", "retry", "again":
			return s.waTableMakeNow(ctx, c, m)
		case "no", "n":
			if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
				return err
			}
			return c.say(ctx, s, c.t("wa.tablemake.not_made"))
		}
		return c.say(ctx, s, c.t("wa.tablemake.retry_reminder", "table", whatsapp.SafeText(m.Title)), retryButtons(c)...)
	case "column:type":
		n, err := strconv.Atoi(strings.TrimSpace(text))
		if err != nil || n < 1 || n > len(chatColumnTypes) {
			return c.say(ctx, s, c.t("wa.tablecol.ask_type", "column", whatsapp.SafeText(m.Column), "list", typeList(c)))
		}
		m.Columns = []datatables.Column{{Name: m.Column, Title: upperFirst(humanName(m.Column)), Type: chatColumnTypes[n-1]}}
		m.Phase = "confirm"
		if err := s.saveSession(ctx, c, stateCollecting, chatData{TableMake: m}, waPendingTTL); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.tablecol.readback", "column", whatsapp.SafeText(m.Column), "type", c.t("wa.tablemake.type."+chatColumnTypes[n-1]),
			"table", whatsapp.SafeText(m.Title)), tableYesButtons(c)...)
	case "column:confirm":
		switch cmd {
		case "yes", "y", "ok", "okay":
			return s.waAddColumnNow(ctx, c, m)
		case "no", "n":
			if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
				return err
			}
			return c.say(ctx, s, c.t("wa.tablemake.not_made"))
		}
		return c.say(ctx, s, c.t("wa.tablecol.readback", "column", whatsapp.SafeText(m.Column), "type", c.t("wa.tablemake.type."+m.Columns[0].Type),
			"table", whatsapp.SafeText(m.Title)), tableYesButtons(c)...)
	}
	return s.saveSession(ctx, c, stateIdle, chatData{}, 0)
}

// waTableMakeNow makes the table agreed. The first table makes the
// business's records project, which takes a minute or two: that is said
// first, and if it fails the table waits in the session for a day, with
// Try again (§9.1).
func (s *Server) waTableMakeNow(ctx context.Context, c *chat, m *chatTableMake) error {
	if s.tablesProvisioning(ctx, c.tenant.ID) {
		if err := c.say(ctx, s, c.t("wa.build.tables_setting_up")); err != nil {
			return err
		}
	}
	t := datatables.Table{Name: m.Name, Title: m.Title, Columns: m.Columns}
	err := s.chatMakeTable(ctx, c, &t, m.Name)
	switch {
	case err == nil:
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.tablemake.made", "table", whatsapp.SafeText(m.Title), "name", t.Name, "link", s.waLink(c, "/tables")))
	case errors.Is(err, datatables.ErrConflict):
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.tablemake.exists", "table", whatsapp.SafeText(m.Title)))
	case errors.Is(err, datatables.ErrInvalid):
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.tablemake.refused", "reason", whatsapp.SafeText(err.Error())))
	}
	if le, ok := runtime.IsLimit(err); ok {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.tablemake.refused", "reason", whatsapp.SafeText(le.Message)))
	}
	// Anything else is the records project or Xenibase not answering (the
	// project still being made, its creation failed, the platform out of
	// room): the table agreed waits a day for Try again.
	s.Logger.Warn("chat: making a table", "tenant", c.tenant.ID, "table", m.Name, "err", err)
	m.Kind, m.Phase = "retry", "confirm"
	if err := s.saveSession(ctx, c, stateCollecting, chatData{TableMake: m}, waTableRetryTTL); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.tablemake.failed", "table", whatsapp.SafeText(m.Title)), retryButtons(c)...)
}

// waAddColumn answers "add column <name> to <table>".
func (s *Server) waAddColumn(ctx context.Context, c *chat, column, table string) error {
	if !c.p.Can(PermTableEdit) {
		return c.say(ctx, s, c.t("wa.tablemake.denied", "org", c.tenant.Name))
	}
	t, done, err := s.waOneTable(ctx, c, table)
	if done || err != nil {
		return err
	}
	name := snakeName(column)
	if name == "" || datatables.CheckName("column", name) != nil {
		return c.say(ctx, s, c.t("wa.tablecol.name_bad"))
	}
	for _, x := range t.Columns {
		if x.Name == name {
			return c.say(ctx, s, c.t("wa.tablecol.exists", "table", whatsapp.SafeText(tableTitle(t)), "column", whatsapp.SafeText(name)))
		}
	}
	m := &chatTableMake{Kind: "column", Phase: "type", Table: t.ID, Title: tableTitle(t), Column: name}
	if typ := typeFromWords(column); typ != "text" {
		// The words say it (a phone, an amount, a date): read back at once.
		m.Phase, m.Columns = "confirm", []datatables.Column{{Name: name, Title: upperFirst(strings.TrimSpace(column)), Type: typ}}
		if err := s.saveSession(ctx, c, stateCollecting, chatData{TableMake: m}, waPendingTTL); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.tablecol.readback", "column", whatsapp.SafeText(name), "type", c.t("wa.tablemake.type."+typ),
			"table", whatsapp.SafeText(m.Title)), tableYesButtons(c)...)
	}
	if err := s.saveSession(ctx, c, stateCollecting, chatData{TableMake: m}, waPendingTTL); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.tablecol.ask_type", "column", whatsapp.SafeText(name), "list", typeList(c)))
}

// waAddColumnNow adds the column agreed, as the Tables page does: the
// plan's column limit, the change in Xenibase, then the record and audit.
func (s *Server) waAddColumnNow(ctx context.Context, c *chat, m *chatTableMake) error {
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	svc, err := s.tables()
	if err != nil {
		return c.say(ctx, s, c.t("wa.table.unavailable"))
	}
	t, err := s.waGetTable(ctx, c.tenant.ID, m.Table)
	if err == nil {
		err = datatables.Writable(t)
	}
	if errors.Is(err, datatables.ErrKept) {
		return c.say(ctx, s, c.t("wa.table.kept", "table", whatsapp.SafeText(tableTitle(t))))
	}
	if err != nil {
		return s.waTableGone(ctx, c, err)
	}
	col := m.Columns[0]
	if err := datatables.CheckColumn(&col); err != nil {
		return c.say(ctx, s, c.t("wa.tablemake.refused", "reason", whatsapp.SafeText(err.Error())))
	}
	for _, x := range t.Columns {
		if x.Name == col.Name {
			return c.say(ctx, s, c.t("wa.tablecol.exists", "table", whatsapp.SafeText(tableTitle(t)), "column", whatsapp.SafeText(col.Name)))
		}
	}
	lim, err := s.Store.LimitsFor(ctx, c.tenant.ID)
	if err != nil {
		return err
	}
	if lim.MaxDataTableColumns > 0 && len(t.Columns) >= lim.MaxDataTableColumns {
		return c.say(ctx, s, c.t("wa.tablemake.refused", "reason", fmt.Sprintf("this plan allows at most %d columns per table", lim.MaxDataTableColumns)))
	}
	if err := svc.Apply(ctx, c.tenant.ID, c.p.Actor(), datatables.AddColumnChange(t.Name, col)); err != nil {
		s.Logger.Warn("chat: adding a column", "tenant", c.tenant.ID, "table", t.Name, "err", err)
		return c.say(ctx, s, c.t("wa.tablemake.refused", "reason", whatsapp.SafeText(errorReason(err))))
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		if err := datatables.InsertColumn(ctx, tx, c.tenant.ID, t.ID, col); err != nil {
			return err
		}
		return waAudit(ctx, tx, c.ch.Name(), c.p, "table.column.add", t.Name+"."+col.Name, map[string]any{"table_id": t.ID, "type": col.Type, "required": col.Required, "personal": col.Personal})
	}); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.tablecol.added", "column", whatsapp.SafeText(col.Name), "table", whatsapp.SafeText(tableTitle(t))))
}

// waRenameTable answers "rename <table> to <title>": the title changes,
// the name never does (workflows use it).
func (s *Server) waRenameTable(ctx context.Context, c *chat, table, title string) error {
	if !c.p.Can(PermTableEdit) {
		return c.say(ctx, s, c.t("wa.tablemake.denied", "org", c.tenant.Name))
	}
	title = strings.TrimSpace(title)
	if title == "" || len(title) > 120 {
		return c.say(ctx, s, c.t("wa.tablemake.name_long"))
	}
	t, done, err := s.waOneTable(ctx, c, table)
	if done || err != nil {
		return err
	}
	if err := datatables.Writable(t); err != nil {
		return c.say(ctx, s, c.t("wa.table.kept", "table", whatsapp.SafeText(tableTitle(t))))
	}
	old := tableTitle(t)
	title = upperFirst(title)
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		if err := datatables.UpdateTable(ctx, tx, t.ID, &title, nil); err != nil {
			return err
		}
		return waAudit(ctx, tx, c.ch.Name(), c.p, "table.update", t.Name, map[string]any{"table_id": t.ID, "title": true})
	}); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.tablemake.renamed", "old", whatsapp.SafeText(old), "table", whatsapp.SafeText(title)))
}

// waTableDropToWeb answers dropping a column or deleting a table: it
// deletes data, so the web app does it, where the name is typed to
// confirm (§6.2, Q1).
func (s *Server) waTableDropToWeb(ctx context.Context, c *chat) error {
	if !c.p.Can(PermTableEdit) {
		return c.say(ctx, s, c.t("wa.tablemake.denied", "org", c.tenant.Name))
	}
	return c.say(ctx, s, c.t("wa.tablemake.drop_web", "link", s.waLink(c, "/tables")))
}

// waOneTable is the one table words name, or says why there is none
// (done).
func (s *Server) waOneTable(ctx context.Context, c *chat, words string) (datatables.Table, bool, error) {
	tables, err := s.waListTables(ctx, c.tenant.ID)
	if err != nil {
		return datatables.Table{}, true, err
	}
	match := matchTables(tables, words)
	switch {
	case len(match) == 0:
		return datatables.Table{}, true, c.say(ctx, s, c.t("wa.table.no_match", "name", fmt.Sprintf("%q", whatsapp.SafeText(words))))
	case len(match) > 1:
		return datatables.Table{}, true, c.say(ctx, s, c.t("wa.tablemake.which", "list", numberedTables(match[:min(waPickMax, len(match))])))
	}
	return match[0], false, nil
}

// waTableEditCommand reads the table words of R3 in English: new table,
// add column … to …, rename … to …, and dropping or deleting.
func (s *Server) waTableEditCommand(ctx context.Context, c *chat, text, cmd string) (bool, error) {
	switch cmd {
	case "new table", "make a table", "new list", "make a list", "keep a list", "create a table":
		return true, s.waNewTable(ctx, c, "")
	}
	for _, p := range []string{"new table ", "make a table for ", "keep a list of ", "new list "} {
		if rest, ok := strings.CutPrefix(cmd, p); ok && rest != "" {
			return true, s.waNewTable(ctx, c, strings.TrimSpace(text[len(text)-len(rest):]))
		}
	}
	if m := addColumnRe.FindStringSubmatch(strings.TrimSpace(text)); m != nil {
		return true, s.waAddColumn(ctx, c, m[1], m[2])
	}
	if m := renameTableRe.FindStringSubmatch(strings.TrimSpace(text)); m != nil {
		return true, s.waRenameTable(ctx, c, m[1], m[2])
	}
	// A Google Sheet copied into a table once (chat_tables_files.go).
	if m := copySheetRe.FindStringSubmatch(strings.TrimSpace(text)); m != nil {
		return true, s.waCopySheet(ctx, c, m[1])
	}
	for _, p := range []string{"drop column ", "delete column ", "remove column ", "delete table ", "remove table ", "drop table "} {
		if strings.HasPrefix(cmd, p) {
			return true, s.waTableDropToWeb(ctx, c)
		}
	}
	return false, nil
}

var (
	addColumnRe   = regexp.MustCompile(`(?i)^add (?:a )?column (.+?) to (?:the )?(?:table )?(.+)$`)
	renameTableRe = regexp.MustCompile(`(?i)^rename (?:the )?(?:table )?(.+?) to (.+)$`)
)

// typeList numbers the column types chat offers, in plain words.
func typeList(c *chat) string {
	lines := make([]string, len(chatColumnTypes))
	for i, t := range chatColumnTypes {
		lines[i] = fmt.Sprintf("%d. %s", i+1, c.t("wa.tablemake.type."+t))
	}
	return strings.Join(lines, "\n")
}

func tableYesButtons(c *chat) []whatsapp.Button {
	return []whatsapp.Button{{ID: "yes", Title: c.t("button.yes")}, {ID: "cancel", Title: c.t("button.cancel")}}
}

func retryButtons(c *chat) []whatsapp.Button {
	return []whatsapp.Button{{ID: "try again", Title: c.t("button.try_again")}, {ID: "cancel", Title: c.t("button.cancel")}}
}

// columnsFromWords reads "name, phone, what they bought, date" as
// columns, each named in snake case, titled as typed and typed from its
// words (§6.1). Repeated names are made unique; reserved ones renamed.
func columnsFromWords(text string) []datatables.Column {
	var out []datatables.Column
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		title := strings.TrimSpace(part)
		title = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(title, "and "), "And "))
		name := snakeName(title)
		if name == "" {
			continue
		}
		switch name {
		case "id":
			name = "ref"
		case "created_at", "updated_at":
			name += "_on"
		}
		base := name
		for i := 2; seen[name]; i++ {
			name = fmt.Sprintf("%s_%d", base, i)
		}
		if datatables.CheckName("column", name) != nil {
			continue
		}
		seen[name] = true
		c := datatables.Column{Name: name, Title: upperFirst(title), Type: typeFromWords(title)}
		if datatables.CheckColumn(&c) != nil {
			continue
		}
		out = append(out, c)
		if len(out) == 50 {
			break
		}
	}
	return out
}

// typeFromWords is the type a column's words say (§6.1): a phone, an
// email, an amount, a date, a yes or no; text otherwise.
func typeFromWords(words string) string {
	w := " " + strings.ToLower(words) + " "
	has := func(xs ...string) bool {
		for _, x := range xs {
			if strings.Contains(w, " "+x+" ") || strings.Contains(w, " "+x+"s ") {
				return true
			}
		}
		return false
	}
	switch {
	case has("phone", "whatsapp", "mobile") || strings.Contains(w, "number to call"):
		return "phone"
	case has("email", "e-mail"):
		return "email"
	case has("amount", "price", "balance", "naira", "cost", "fee", "total", "owed", "paid amount"):
		return "number"
	case has("date", "day", "birthday", "due"):
		return "date"
	case has("paid", "done", "active", "delivered"):
		return "boolean"
	case has("quantity", "qty", "count", "how many"):
		return "integer"
	}
	return "text"
}

// snakeName is words as a table or column name: lower case, letters and
// digits joined by underscores, starting with a letter, at most 63.
func snakeName(words string) string {
	var b strings.Builder
	under := false
	for _, r := range strings.ToLower(words) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			if under && b.Len() > 0 {
				b.WriteByte('_')
			}
			under = false
			b.WriteRune(r)
		default:
			under = true
		}
	}
	name := b.String()
	if name != "" && !unicode.IsLetter(rune(name[0])) {
		name = "list_" + name
	}
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "_")
	}
	return name
}

// upperFirst capitalises the first letter, as a title is shown.
func upperFirst(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) == 0 {
		return ""
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// errorReason is the part of a tables error a person can read.
func errorReason(err error) string {
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 && i+2 < len(msg) {
		return msg[i+2:]
	}
	return msg
}
