package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// A list brought into a table from a file sent in chat
// (docs/spec/no-sheet-needed.md §5.6; decision 0060, R2). A CSV or an
// Excel workbook's first sheet, sent on WhatsApp or Telegram as a
// document, is read and summed up; then **New table** makes a table from
// it (named from the file, its columns typed from their values),
// **Add to table** adds its rows to a table (headers matched by name or
// title, any other asked by number), or **Not now** drops it. The rows
// are written after yes, through the API's import (importRows): the same
// limits, batches and checks, and an import id from the file and the
// table, so the same file sent twice writes once.
//
// The file is never kept: the session holds its channel id, name and
// digest, and it is fetched again for the yes and checked against the
// digest. Its rows are audited as counts, never values.

const (
	// waFileMax is the largest file read from chat.
	waFileMax = 2 << 20
)

// chatFile is a file sent in chat, being brought into a table.
type chatFile struct {
	// The file as sent: fetched again for the yes, never kept. Sheet is
	// a Google Sheet's id instead, read again the same way.
	ID    string `json:"id,omitempty"`
	Sheet string `json:"sheet,omitempty"`
	Mime  string `json:"mime,omitempty"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Hash  string `json:"hash"`
	// What it holds: the header, and how many rows.
	Header []string `json:"header"`
	Rows   int      `json:"rows"`
	// Phase: pick (new, add, not now), map (a header asked), confirm.
	Phase string `json:"phase"`
	// Op: new or add. Offer is the table offered for Add to table.
	Op    string    `json:"op,omitempty"`
	Offer uuid.UUID `json:"offer,omitempty"`
	// Table is the table added to, or made.
	Table uuid.UUID `json:"table,omitempty"`
	// A new table's name, title and columns.
	TName   string              `json:"tname,omitempty"`
	Title   string              `json:"title,omitempty"`
	Columns []datatables.Column `json:"columns,omitempty"`
	// Mapping is header -> column ("" to leave it out) for the headers
	// that match no column; Ask the ones still to ask.
	Mapping map[string]string `json:"mapping,omitempty"`
	Ask     []string          `json:"ask,omitempty"`
}

// fileButtons are the three answers to a file.
func fileButtons(c *chat, offer bool) []whatsapp.Button {
	b := []whatsapp.Button{{ID: "file:new", Title: c.t("button.file_new")}}
	if offer {
		b = append(b, whatsapp.Button{ID: "file:add", Title: c.t("button.file_add")})
	}
	return append(b, whatsapp.Button{ID: "file:no", Title: c.t("button.not_now")})
}

// waDocument answers a file sent in chat.
func (s *Server) waDocument(ctx context.Context, c *chat) error {
	doc := c.in.Document
	if !c.p.Can(PermTableEdit) {
		return c.say(ctx, s, c.t("wa.file.denied", "org", c.tenant.Name))
	}
	if c.ch.Name() == "slack" {
		// Reading a Slack DM's file needs the files:read scope, which every
		// workspace would have to reinstall the app for (Q6).
		return c.say(ctx, s, c.t("wa.file.slack", "link", s.waLink(c, "/tables")))
	}
	if _, err := s.tables(); err != nil {
		return c.say(ctx, s, c.t("wa.table.unavailable"))
	}
	kind := datatables.FileKind(doc.Name, doc.MimeType)
	if kind == "" {
		return c.say(ctx, s, c.t("wa.file.unsupported"))
	}
	if !s.limiter("wa-file:"+c.addr, time.Minute, 5).Allow() {
		return c.say(ctx, s, c.t("wa.file.too_many"))
	}
	data, err := s.waFetchFile(ctx, c, doc)
	if errors.Is(err, whatsapp.ErrMediaTooLarge) {
		return c.say(ctx, s, c.t("wa.file.too_big", "link", s.waLink(c, "/tables")))
	}
	if err != nil {
		s.Logger.Warn("chat: fetching a file", "err", err)
		return c.say(ctx, s, c.t("wa.file.not_fetched"))
	}
	header, rows, err := datatables.ReadFile(kind, data, importMaxRows)
	if err != nil {
		return s.waFileUnread(ctx, c, err)
	}
	sum := sha256.Sum256(data)
	return s.waFileStart(ctx, c, &chatFile{ID: doc.ID, Mime: doc.MimeType, Name: fileName(doc.Name, kind), Kind: kind, Hash: hex.EncodeToString(sum[:]),
		Header: header, Rows: len(rows), Phase: "pick"})
}

// waFileStart offers what can be done with a file or sheet read: the
// table offered for Add to table is one it is named after, or the one with
// the most of its headings as columns (at least half of them).
func (s *Server) waFileStart(ctx context.Context, c *chat, f *chatFile) error {
	tables, err := s.waListTables(ctx, c.tenant.ID)
	if err != nil {
		return err
	}
	best := 0
	for _, t := range tables {
		if t.Kept() {
			continue
		}
		names, _, _ := headerColumns(f.Header, t.Columns, nil)
		n := 0
		for _, x := range names {
			if x != "" && x != "id" {
				n++
			}
		}
		named := len(matchTables([]datatables.Table{t}, f.base())) == 1
		if named && n > 0 {
			n += len(f.Header)
		}
		if n > best && 2*n >= len(f.Header) {
			best, f.Offer = n, t.ID
		}
	}
	if err := s.saveSession(ctx, c, stateCollecting, chatData{File: f}, waPendingTTL); err != nil {
		return err
	}
	return c.say(ctx, s, s.waFileGot(ctx, c, f), fileButtons(c, f.Offer != uuid.Nil)...)
}

// base is the name a new table takes: a file's without its extension, a
// sheet's title as it is.
func (f *chatFile) base() string {
	if f.Sheet != "" {
		return f.Name
	}
	return strings.TrimSuffix(f.Name, path.Ext(f.Name))
}

// fileName is the file's name as sent, or one for its kind.
func fileName(name, kind string) string {
	name = strings.TrimSpace(path.Base(strings.ReplaceAll(name, "\\", "/")))
	if name == "" || name == "." || name == "/" {
		return "list." + kind
	}
	return name
}

// waFileGot sums a file up and says what can be done with it.
func (s *Server) waFileGot(ctx context.Context, c *chat, f *chatFile) string {
	heads := make([]string, len(f.Header))
	for i, h := range f.Header {
		heads[i] = whatsapp.SafeText(h)
	}
	msg := c.t("wa.file.got", "file", whatsapp.SafeText(f.Name), "rows", strconv.Itoa(f.Rows), "columns", strings.Join(heads, ", "))
	if f.Offer != uuid.Nil {
		if t, err := s.waGetTable(ctx, c.tenant.ID, f.Offer); err == nil {
			msg += "\n" + c.t("wa.file.offer", "table", whatsapp.SafeText(tableTitle(t)))
		}
	}
	return msg + "\n" + c.t("wa.file.choose")
}

// waFileUnread says why a file could not be read.
func (s *Server) waFileUnread(ctx context.Context, c *chat, err error) error {
	var tm datatables.TooManyRows
	if errors.As(err, &tm) {
		return c.say(ctx, s, c.t("wa.file.too_many_rows", "limit", strconv.Itoa(tm.Limit)))
	}
	if errors.Is(err, datatables.ErrFile) {
		return c.say(ctx, s, c.t("wa.file.unreadable", "reason", whatsapp.SafeText(strings.TrimPrefix(err.Error(), datatables.ErrFile.Error()+": "))))
	}
	return err
}

// waFetchFile fetches a file from its channel, at most waFileMax bytes.
func (s *Server) waFetchFile(ctx context.Context, c *chat, doc *whatsapp.Media) ([]byte, error) {
	data, _, _, err := s.fetchMedia(ctx, c, doc, waFileMax)
	if err == nil && len(data) > waFileMax {
		return nil, whatsapp.ErrMediaTooLarge
	}
	return data, err
}

// waFileAgain fetches the file again for the yes and checks it is the one
// read; false (said) when it cannot be.
func (s *Server) waFileAgain(ctx context.Context, c *chat, f *chatFile) ([]string, [][]string, []byte, bool, error) {
	if f.Sheet != "" {
		return s.waSheetAgain(ctx, c, f)
	}
	data, err := s.waFetchFile(ctx, c, &whatsapp.Media{ID: f.ID, MimeType: f.Mime, Name: f.Name})
	sum := sha256.Sum256(data)
	if err != nil || hex.EncodeToString(sum[:]) != f.Hash {
		if err != nil {
			s.Logger.Warn("chat: fetching a file again", "err", err)
		}
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return nil, nil, nil, false, err
		}
		return nil, nil, nil, false, c.say(ctx, s, c.t("wa.file.again"))
	}
	header, rows, err := datatables.ReadFile(f.Kind, data, importMaxRows)
	if err != nil {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return nil, nil, nil, false, err
		}
		return nil, nil, nil, false, s.waFileUnread(ctx, c, err)
	}
	return header, rows, data, true, nil
}

// waFileReply takes a message while a file is being brought in.
func (s *Server) waFileReply(ctx context.Context, c *chat, text, cmd string) error {
	f := c.data.File
	if !c.p.Can(PermTableEdit) {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.file.denied", "org", c.tenant.Name))
	}
	switch f.Phase {
	case "pick":
		switch {
		case cmd == "file:new" || cmd == "new table" || cmd == "new" || cmd == "1":
			return s.waFileNew(ctx, c, f)
		case (cmd == "file:add" || cmd == "add" || cmd == "add to table") && f.Offer != uuid.Nil:
			t, err := s.waGetTable(ctx, c.tenant.ID, f.Offer)
			if err != nil {
				return s.waFileGone(ctx, c, err)
			}
			return s.waFileAdd(ctx, c, f, t)
		case cmd == "file:no" || cmd == "not now" || cmd == "no" || cmd == "n":
			if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
				return err
			}
			return c.say(ctx, s, c.t("wa.file.not_now"))
		}
		if name, ok := strings.CutPrefix(cmd, "add to "); ok {
			tables, err := s.waListTables(ctx, c.tenant.ID)
			if err != nil {
				return err
			}
			match := matchTables(tables, name)
			switch len(match) {
			case 1:
				return s.waFileAdd(ctx, c, f, match[0])
			case 0:
				return c.say(ctx, s, c.t("wa.table.no_match", "name", strconv.Quote(whatsapp.SafeText(name))))
			}
			return c.say(ctx, s, c.t("wa.file.which", "list", numberedTables(match[:min(waPickMax, len(match))])))
		}
		return c.say(ctx, s, c.t("wa.file.pick_reminder"), fileButtons(c, f.Offer != uuid.Nil)...)
	case "map":
		t, err := s.waGetTable(ctx, c.tenant.ID, f.Table)
		if err != nil {
			return s.waFileGone(ctx, c, err)
		}
		n, err := strconv.Atoi(strings.TrimSpace(text))
		if err != nil || n < 0 || n > len(t.Columns) {
			return c.say(ctx, s, c.t("wa.file.map_again", "max", strconv.Itoa(len(t.Columns))))
		}
		to := ""
		if n > 0 {
			to = t.Columns[n-1].Name
		}
		if f.Mapping == nil {
			// An empty mapping is not kept in the session.
			f.Mapping = map[string]string{}
		}
		f.Mapping[f.Ask[0]] = to
		f.Ask = f.Ask[1:]
		return s.waFileAskNext(ctx, c, f, t)
	case "confirm":
		switch cmd {
		case "yes", "y", "ok", "okay", "try again", "retry":
			return s.waFileImport(ctx, c, f)
		case "no", "n":
			if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
				return err
			}
			return c.say(ctx, s, c.t("wa.file.not_now"))
		}
		return c.say(ctx, s, c.t("wa.rows.reminder"), tableYesButtons(c)...)
	}
	return s.saveSession(ctx, c, stateIdle, chatData{}, 0)
}

// waFileGone ends the session when its table is no longer there.
func (s *Server) waFileGone(ctx context.Context, c *chat, err error) error {
	if !errors.Is(err, datatables.ErrNotFound) {
		return err
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.file.table_gone"))
}

// waFileNew reads a new table back: named from the file, a column per
// heading typed from its values, and the first rows as they will be.
func (s *Server) waFileNew(ctx context.Context, c *chat, f *chatFile) error {
	header, rows, _, ok, err := s.waFileAgain(ctx, c, f)
	if !ok || err != nil {
		return err
	}
	name, title, cols := datatables.TableFromFile(f.base(), header, rows)
	if datatables.CheckTableName(name) != nil {
		name = "list_" + name
		if datatables.CheckTableName(name) != nil {
			name = "list"
		}
	}
	f.Op, f.TName, f.Title, f.Columns, f.Mapping = "new", name, upperFirst(title), cols, nil
	return s.waFileConfirm(ctx, c, f, header, rows)
}

// waFileAdd matches the file's headings to the table's columns, asking
// about each that matches none.
func (s *Server) waFileAdd(ctx context.Context, c *chat, f *chatFile, t datatables.Table) error {
	if t.Kept() {
		return s.waTableKept(ctx, c, t)
	}
	_, unmatched, err := headerColumns(f.Header, t.Columns, nil)
	if err != nil {
		return err
	}
	f.Op, f.Table, f.Mapping, f.Ask = "add", t.ID, map[string]string{}, unmatched
	return s.waFileAskNext(ctx, c, f, t)
}

// waFileAskNext asks about the next heading that matches no column, or
// reads the rows back once each is answered.
func (s *Server) waFileAskNext(ctx context.Context, c *chat, f *chatFile, t datatables.Table) error {
	if len(f.Ask) > 0 {
		f.Phase = "map"
		if err := s.saveSession(ctx, c, stateCollecting, chatData{File: f}, waPendingTTL); err != nil {
			return err
		}
		lines := make([]string, len(t.Columns))
		for i, col := range t.Columns {
			lines[i] = strconv.Itoa(i+1) + ". " + whatsapp.SafeText(tableColumnLabel(col))
		}
		return c.say(ctx, s, c.t("wa.file.ask_header", "table", whatsapp.SafeText(tableTitle(t)), "header", whatsapp.SafeText(f.Ask[0]), "list", strings.Join(lines, "\n")))
	}
	names, _, err := headerColumns(f.Header, t.Columns, f.Mapping)
	if err != nil {
		return err
	}
	some := false
	for _, n := range names {
		some = some || (n != "" && n != "id" && n != "created_at" && n != "updated_at")
	}
	if !some {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.file.no_columns", "table", whatsapp.SafeText(tableTitle(t))))
	}
	f.Columns, f.Title = t.Columns, tableTitle(t)
	header, rows, _, ok, err := s.waFileAgain(ctx, c, f)
	if !ok || err != nil {
		return err
	}
	return s.waFileConfirm(ctx, c, f, header, rows)
}

// fileRows checks a file's rows against the columns, its numbers' commas
// and naira signs taken off first.
func fileRows(header []string, recs [][]string, cols []datatables.Column, mapping map[string]string) ([]any, bool, error) {
	names, _, err := headerColumns(header, cols, mapping)
	if err != nil {
		return nil, false, err
	}
	types := map[string]string{}
	for _, col := range cols {
		types[col.Name] = col.Type
	}
	clean := make([][]string, len(recs))
	for i, r := range recs {
		clean[i] = make([]string, len(r))
		for j, v := range r {
			if j < len(names) && (types[names[j]] == "number" || types[names[j]] == "integer") {
				v = datatables.FileNumber(v)
			}
			clean[i][j] = v
		}
	}
	return importRecords(header, clean, cols, mapping)
}

// waFileConfirm checks every row and reads back the count and the first
// three, personal values masked, for yes.
func (s *Server) waFileConfirm(ctx context.Context, c *chat, f *chatFile, header []string, recs [][]string) error {
	rows, _, err := fileRows(header, recs, f.Columns, f.Mapping)
	if err != nil {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.file.row_bad", "reason", whatsapp.SafeText(strings.TrimPrefix(err.Error(), errBadRequest.Error()+": "))))
	}
	f.Phase = "confirm"
	if err := s.saveSession(ctx, c, stateCollecting, chatData{File: f}, waPendingTTL); err != nil {
		return err
	}
	var first []string
	for _, r := range rows[:min(3, len(rows))] {
		m, _ := r.(map[string]any)
		first = append(first, "• "+strings.TrimPrefix(strings.TrimPrefix(waTableRowLine(f.Columns, m), "#"), " · "))
	}
	count := strconv.Itoa(len(rows))
	if f.Op == "new" {
		lines := make([]string, len(f.Columns))
		for i, col := range f.Columns {
			lines[i] = fmt.Sprintf("%d. %s: %s", i+1, whatsapp.SafeText(col.Title), c.t("wa.tablemake.type."+col.Type))
		}
		return c.say(ctx, s, c.t("wa.file.confirm_new", "table", whatsapp.SafeText(f.Title), "columns", strings.Join(lines, "\n"), "rows", count,
			"first", strings.Join(first, "\n")), tableYesButtons(c)...)
	}
	return c.say(ctx, s, c.t("wa.file.confirm_add", "table", whatsapp.SafeText(f.Title), "rows", count, "first", strings.Join(first, "\n")), tableYesButtons(c)...)
}

// waFileImport makes the table when it is new, then writes the rows,
// fetched again and checked against what was read back.
func (s *Server) waFileImport(ctx context.Context, c *chat, f *chatFile) error {
	header, recs, data, ok, err := s.waFileAgain(ctx, c, f)
	if !ok || err != nil {
		return err
	}
	rows, hasID, err := fileRows(header, recs, f.Columns, f.Mapping)
	if err != nil {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.file.row_bad", "reason", whatsapp.SafeText(strings.TrimPrefix(err.Error(), errBadRequest.Error()+": "))))
	}
	made := false
	if f.Op == "new" && f.Table == uuid.Nil {
		if s.tablesProvisioning(ctx, c.tenant.ID) {
			if err := c.say(ctx, s, c.t("wa.build.tables_setting_up")); err != nil {
				return err
			}
		}
		t := datatables.Table{Name: f.TName, Title: f.Title, Columns: f.Columns}
		names := []string{f.TName}
		for i := 2; i <= 9; i++ {
			names = append(names, f.TName+"_"+strconv.Itoa(i))
		}
		if err := s.chatMakeTable(ctx, c, &t, names...); err != nil {
			s.Logger.Warn("chat: making a table from a file", "tenant", c.tenant.ID, "err", err)
			return c.say(ctx, s, c.t("wa.file.failed", "reason", whatsapp.SafeText(tablesMakeReason(err))), tableYesButtons(c)...)
		}
		// Made: a yes again only writes the rows.
		f.Table, f.TName, made = t.ID, t.Name, true
		if err := s.saveSession(ctx, c, stateCollecting, chatData{File: f}, waPendingTTL); err != nil {
			return err
		}
	}
	t, err := s.waGetTable(ctx, c.tenant.ID, f.Table)
	if err != nil {
		return s.waFileGone(ctx, c, err)
	}
	if t.Kept() {
		return s.waTableKept(ctx, c, t)
	}
	tctx := s.waTablesCtx(ctx, c)
	tr, err := s.tableRowsFor(tctx, c.tenant.ID, t, tablesChatPurpose)
	if err != nil {
		s.Logger.Warn("chat: the data tables connection", "err", err)
		return c.say(ctx, s, c.t("wa.table.unavailable"))
	}
	importID := importIDFor(t.ID, "", data)
	res, err := s.importRows(tctx, tr, rows, hasID, importID)
	if err != nil {
		// The session stays: a yes again sends the same keys, and the
		// batches that went in are not written twice.
		return c.say(ctx, s, c.t("wa.file.failed", "reason", tablesChatReason(err)), tableYesButtons(c)...)
	}
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		return waAudit(ctx, tx, c.ch.Name(), c.p, "table.import", t.Name, map[string]any{"table_id": t.ID, "import_id": importID, "rows": len(rows),
			"imported": res.Imported, "batches": res.Batches, "replayed_batches": res.Replayed, "upsert": hasID, "file": f.Kind})
	}); err != nil {
		return err
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	title, file := whatsapp.SafeText(tableTitle(t)), whatsapp.SafeText(f.Name)
	var msg string
	switch {
	case made || f.Op == "new":
		msg = c.t("wa.file.made", "table", title, "rows", strconv.Itoa(len(rows)), "file", file, "name", t.Name)
	case res.Replayed == res.Batches:
		msg = c.t("wa.file.already", "table", title, "file", file)
	default:
		msg = c.t("wa.file.added", "table", title, "rows", strconv.Itoa(len(rows)), "file", file)
	}
	if f.Sheet != "" {
		// A copy, once: said, so nobody expects the sheet to follow.
		msg += "\n\n" + c.t("wa.sheet.no_sync")
	}
	return c.say(ctx, s, msg)
}

// --- a Google Sheet, once ---

// copySheetRe reads "copy my sheet <link>".
var copySheetRe = regexp.MustCompile(`(?i)^copy (?:my )?(?:google )?(?:spread)?sheet\b\s*(.*)$`)

// sheetRange is what a copy reads: the first tab's first 200 columns
// (A to GR), as many rows as an import takes, the header and one more to
// tell a sheet that has too many.
var sheetRange = fmt.Sprintf("A1:GR%d", importMaxRows+2)

// waCopySheet answers "copy my sheet <link>": a Google Sheet read once
// through the business's Google Sheets connection, then brought in as a
// file is. Nothing is synced afterwards.
func (s *Server) waCopySheet(ctx context.Context, c *chat, link string) error {
	if !c.p.Can(PermTableEdit) {
		return c.say(ctx, s, c.t("wa.file.denied", "org", c.tenant.Name))
	}
	id := SpreadsheetID(link)
	if id == "" {
		return c.say(ctx, s, c.t("wa.sheet.link"))
	}
	if _, err := s.tables(); err != nil {
		return c.say(ctx, s, c.t("wa.table.unavailable"))
	}
	if !s.limiter("wa-file:"+c.addr, time.Minute, 5).Allow() {
		return c.say(ctx, s, c.t("wa.file.too_many"))
	}
	meta, err := s.connectorAction(ctx, c.p, "googlesheets", "Google Sheets", "", "get_spreadsheet", tablesChatPurpose, map[string]any{"spreadsheet_id": id})
	if err != nil {
		return s.waSheetFailed(ctx, c, err)
	}
	recs, data, err := s.sheetValues(ctx, c, id)
	if err != nil {
		return s.waSheetFailed(ctx, c, err)
	}
	header, rows, err := datatables.ReadRecords(recs)
	if err != nil {
		return s.waFileUnread(ctx, c, err)
	}
	title, _ := meta["title"].(string)
	if strings.TrimSpace(title) == "" {
		title = "Sheet"
	}
	sum := sha256.Sum256(data)
	return s.waFileStart(ctx, c, &chatFile{Sheet: id, Name: strings.TrimSpace(title), Kind: "sheet", Hash: hex.EncodeToString(sum[:]),
		Header: header, Rows: len(rows), Phase: "pick"})
}

// sheetValues reads the sheet's first tab as records, and their bytes for
// the digest and the import id.
func (s *Server) sheetValues(ctx context.Context, c *chat, id string) ([][]string, []byte, error) {
	out, err := s.connectorAction(ctx, c.p, "googlesheets", "Google Sheets", "", "get_values", tablesChatPurpose,
		map[string]any{"spreadsheet_id": id, "range": sheetRange})
	if err != nil {
		return nil, nil, err
	}
	values, _ := out["values"].([]any)
	if len(values) > importMaxRows+1 {
		return nil, nil, datatables.TooManyRows{Limit: importMaxRows}
	}
	recs := make([][]string, len(values))
	for i, v := range values {
		cells, _ := v.([]any)
		recs[i] = make([]string, len(cells))
		for j, x := range cells {
			recs[i][j] = cell(x)
		}
	}
	data, err := json.Marshal(recs)
	return recs, data, err
}

// waSheetAgain reads the sheet again for the yes and checks it is as it
// was read back.
func (s *Server) waSheetAgain(ctx context.Context, c *chat, f *chatFile) ([]string, [][]string, []byte, bool, error) {
	recs, data, err := s.sheetValues(ctx, c, f.Sheet)
	sum := sha256.Sum256(data)
	if err != nil || hex.EncodeToString(sum[:]) != f.Hash {
		if err != nil {
			s.Logger.Warn("chat: reading a sheet again", "err", err)
		}
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return nil, nil, nil, false, err
		}
		return nil, nil, nil, false, c.say(ctx, s, c.t("wa.sheet.changed"))
	}
	header, rows, err := datatables.ReadRecords(recs)
	if err != nil {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return nil, nil, nil, false, err
		}
		return nil, nil, nil, false, s.waFileUnread(ctx, c, err)
	}
	return header, rows, data, true, nil
}

// waSheetFailed says why a sheet could not be read: no Google Sheets
// connection, more than one, or Google refusing it.
func (s *Server) waSheetFailed(ctx context.Context, c *chat, err error) error {
	var tm datatables.TooManyRows
	switch {
	case errors.As(err, &tm):
		return s.waFileUnread(ctx, c, err)
	case errors.Is(err, errConflict):
		return c.say(ctx, s, c.t("wa.sheet.connect_first"))
	case errors.Is(err, errBadRequest):
		return c.say(ctx, s, c.t("wa.sheet.cannot", "reason", whatsapp.SafeText(strings.TrimPrefix(err.Error(), errBadRequest.Error()+": "))))
	case isLookup(err):
		return c.say(ctx, s, c.t("wa.sheet.cannot_open"))
	}
	return err
}

// tablesMakeReason says, in a few words, why a table was not made.
func tablesMakeReason(err error) string {
	if le, ok := runtime.IsLimit(err); ok {
		return le.Message
	}
	if errors.Is(err, datatables.ErrInvalid) || errors.Is(err, datatables.ErrConflict) {
		return err.Error()
	}
	return tablesChatReason(err)
}
