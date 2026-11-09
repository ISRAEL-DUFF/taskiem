package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/knowledge"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// The readers of what a business's assistant knows (decision 0062 D12,
// docs/knowledge.md): each kind of knowledge source read as titled text.
// Personal data is kept out: a data table's personal columns (and its
// phone and email columns) are never read; a sheet's columns headed as
// contact details are left out and phone numbers, emails and identity
// numbers in the rest are masked; the storefront gives its published card
// and catalogue only. Erasing a customer asks every table and sheet
// source to be read again (eraseKnowledge).

// knowledgePurpose is the recorded purpose of the connection reads.
const knowledgePurpose = "knowledge.read"

// Limits of what one source reads.
const (
	maxAboutChars  = 50_000
	maxTableRows   = 2000
	maxSheetRows   = 2000
	maxBooksItems  = 5000
	rowsPerSection = 25
)

// knowledgeReaders are the readers this server has.
func (s *Server) knowledgeReaders() map[string]knowledge.Reader {
	return map[string]knowledge.Reader{
		knowledge.About:      knowledge.ReaderFunc(readAbout),
		knowledge.Storefront: knowledge.ReaderFunc(s.readStorefront),
		knowledge.Table:      knowledge.ReaderFunc(s.readTable),
		knowledge.Sheet:      knowledge.ReaderFunc(s.readSheet),
		knowledge.Books:      knowledge.ReaderFunc(s.readBooks),
		knowledge.Website:    knowledge.ReaderFunc(s.readWebsite),
		knowledge.File:       knowledge.ReaderFunc(readFile),
	}
}

func knowledgeConfig(src knowledge.Source, into any) error {
	if err := json.Unmarshal(src.Config, into); err != nil {
		return fmt.Errorf("the source's settings cannot be read: %w", err)
	}
	return nil
}

// readAbout is the About my business page: the owner's own text, a
// section per heading (a line starting with #, or ending with a colon).
func readAbout(_ context.Context, _ uuid.UUID, src knowledge.Source) ([]knowledge.Section, error) {
	var cfg struct {
		Text string `json:"text"`
	}
	if err := knowledgeConfig(src, &cfg); err != nil {
		return nil, err
	}
	return headedSections(cfg.Text, src.Title, ""), nil
}

// headedSections splits a text at its headings: lines starting with #,
// or short lines ending with a colon.
func headedSections(text, title, link string) []knowledge.Section {
	var out []knowledge.Section
	cur := knowledge.Section{Title: title, Link: link}
	var b strings.Builder
	flush := func() {
		if t := strings.TrimSpace(b.String()); t != "" {
			cur.Text = t
			out = append(out, cur)
		}
		b.Reset()
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trim := strings.TrimSpace(line)
		heading := ""
		switch {
		case strings.HasPrefix(trim, "#"):
			heading = strings.TrimSpace(strings.TrimLeft(trim, "#"))
		case strings.HasSuffix(trim, ":") && len(trim) < 60 && !strings.Contains(trim, ". "):
			heading = strings.TrimSuffix(trim, ":")
		}
		if heading != "" {
			flush()
			cur = knowledge.Section{Title: heading, Link: link}
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	flush()
	return out
}

// readStorefront is the business's storefront: its card (what it does,
// where, how to reach it, its hours) and its catalogue with prices.
func (s *Server) readStorefront(ctx context.Context, tenant uuid.UUID, src knowledge.Source) ([]knowledge.Section, error) {
	// What customers see: the published card (checked and moderated), not
	// the draft being edited.
	var sf storefront.Storefront
	var items []storefront.Item
	var model storefront.Model
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		if sf, err = storefront.Get(ctx, tx, tenant); err != nil {
			return err
		}
		if sf.Status == "suspended" || sf.PublishedVersion == 0 {
			return nil
		}
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT model FROM storefront_snapshots WHERE storefront_id = $1 AND version = $2`, sf.ID, sf.PublishedVersion).Scan(&raw); err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &model); err != nil {
			return err
		}
		items, err = storefront.Catalogue(ctx, tx, tenant)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, storefront.ErrNotFound) {
		return nil, knowledge.Plainly(knowledge.ErrGone, "the business has no storefront yet")
	}
	if err != nil {
		return nil, err
	}
	if sf.Status == "suspended" {
		return nil, knowledge.Plainly(knowledge.ErrGone, "the storefront is suspended")
	}
	if sf.PublishedVersion == 0 {
		return nil, knowledge.Plainly(nil, "the storefront is not published yet")
	}
	link := sf.URL(s.previewDomain())
	c := model.Card
	var about []string
	add := func(label, v string) {
		if v = strings.TrimSpace(v); v != "" {
			about = append(about, label+": "+v)
		}
	}
	add("Name", c.Name)
	add("In short", c.Tagline)
	add("About", c.About)
	add("Area", c.Area)
	add("Address", c.Address)
	add("Phone", c.Phone)
	add("WhatsApp", c.WhatsApp)
	if sf.ClosedMessage != nil && strings.TrimSpace(*sf.ClosedMessage) != "" {
		about = append(about, "Closed now: "+strings.TrimSpace(*sf.ClosedMessage))
	}
	out := []knowledge.Section{{Title: "About " + c.Name, Text: strings.Join(about, "\n"), Link: link}}
	if hours := hoursText(c.Hours); hours != "" {
		out = append(out, knowledge.Section{Title: "Opening hours", Text: hours, Link: link})
	}
	var lines []string
	flush := func() {
		if len(lines) > 0 {
			out = append(out, knowledge.Section{Title: "What we sell and its prices", Text: strings.Join(lines, "\n"), Link: link})
			lines = nil
		}
	}
	for _, it := range items {
		line := it.Name
		if it.Category != "" {
			line = it.Category + ": " + line
		}
		if it.PriceKobo != nil {
			line += ", " + storefront.FormatNaira(*it.PriceKobo)
		}
		if it.SoldOut() || !it.Available {
			line += " (not available now)"
		}
		if d := strings.TrimSpace(it.Description); d != "" {
			line += ". " + d
		}
		lines = append(lines, line)
		if len(lines) == rowsPerSection {
			flush()
		}
	}
	flush()
	return out, nil
}

var knowledgeDays = [7]string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

func hoursText(h storefront.Hours) string {
	var lines []string
	any := false
	for i, spans := range h.Days {
		if len(spans) == 0 {
			lines = append(lines, knowledgeDays[i]+": closed")
			continue
		}
		any = true
		parts := make([]string, len(spans))
		for j, sp := range spans {
			parts[j] = sp.Open + " to " + sp.Close
		}
		lines = append(lines, knowledgeDays[i]+": "+strings.Join(parts, ", "))
	}
	if !any {
		return ""
	}
	if h.Note != "" {
		lines = append(lines, h.Note)
	}
	return strings.Join(lines, "\n")
}

// readTable is a data table's rows, the columns the owner chose (all but
// the personal ones when none were chosen); personal columns are never
// read, whatever was chosen.
func (s *Server) readTable(ctx context.Context, tenant uuid.UUID, src knowledge.Source) ([]knowledge.Section, error) {
	var cfg struct {
		Table   string    `json:"table"`
		TableID uuid.UUID `json:"table_id"`
		Columns []string  `json:"columns"`
	}
	if err := knowledgeConfig(src, &cfg); err != nil {
		return nil, err
	}
	// The table the owner picked, by its id: a table made later under the
	// same name is not it.
	var t datatables.Table
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		var err error
		t, err = datatables.GetTable(ctx, tx, cfg.TableID)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, datatables.ErrNotFound) || (err == nil && t.ID != cfg.TableID) {
		return nil, knowledge.Plainly(knowledge.ErrGone, "the table %q no longer exists", cfg.Table)
	}
	if err != nil {
		return nil, err
	}
	if t.KeptBy != "" {
		return nil, knowledge.Plainly(knowledge.ErrGone, "the table %q is kept by Taskiem and is not read", t.Name)
	}
	cols := knowledgeColumns(t, cfg.Columns)
	if len(cols) == 0 {
		return nil, knowledge.Plainly(nil, "the table has no column to read that is not personal")
	}
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	tr, err := s.tableRowsFor(ctx, tenant, t, knowledgePurpose)
	if err != nil {
		return nil, err
	}
	var out []knowledge.Section
	var lines []string
	title := t.Title
	if title == "" {
		title = t.Name
	}
	cursor := ""
	for read := 0; read < maxTableRows; {
		in := map[string]any{"table": t.Name, "select": strings.Join(names, ","), "order": []any{map[string]any{"column": "id"}}, "limit": 500}
		if cursor != "" {
			in["cursor"] = cursor
		}
		page, err := tr.call(ctx, s, "read_rows", in, "")
		if err != nil {
			return nil, err
		}
		rows, _ := page["rows"].([]any)
		for _, r := range rows {
			m, _ := r.(map[string]any)
			var parts []string
			for _, c := range cols {
				if v, ok := m[c.Name]; ok && v != nil && fmt.Sprint(v) != "" {
					label := c.Title
					if label == "" {
						label = c.Name
					}
					parts = append(parts, label+": "+fmt.Sprint(v))
				}
			}
			if len(parts) > 0 {
				lines = append(lines, strings.Join(parts, "; "))
			}
			if len(lines) == rowsPerSection {
				out = append(out, knowledge.Section{Title: title, Text: strings.Join(lines, "\n")})
				lines = nil
			}
		}
		read += len(rows)
		next, _ := page["next_cursor"].(string)
		if next == "" || len(rows) == 0 {
			break
		}
		cursor = next
	}
	if len(lines) > 0 {
		out = append(out, knowledge.Section{Title: title, Text: strings.Join(lines, "\n")})
	}
	return out, nil
}

// knowledgeColumns are the columns a table source reads: those chosen, or
// all, never a personal one (nor a phone or email column).
func knowledgeColumns(t datatables.Table, chosen []string) []datatables.Column {
	var out []datatables.Column
	for _, c := range t.Columns {
		if c.Personal || c.Type == "phone" || c.Type == "email" {
			continue
		}
		if len(chosen) > 0 && !containsString(chosen, c.Name) {
			continue
		}
		out = append(out, c)
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// connectionFor is a business's connection in prod to a connector, with
// an egress-guarded client to its hosts; the read is recorded.
func (s *Server) connectionFor(ctx context.Context, tenant uuid.UUID, ref, name, purpose string) (*connector.Connector, map[string]string, *http.Client, error) {
	return s.connectionIn(ctx, tenant, "prod", ref, name, purpose)
}

// connectionIn is connectionFor in an environment.
func (s *Server) connectionIn(ctx context.Context, tenant uuid.UUID, env, ref, name, purpose string) (*connector.Connector, map[string]string, *http.Client, error) {
	if s.Registry == nil || s.Vault == nil {
		return nil, nil, nil, errors.New("no connector registry or vault")
	}
	reg, err := s.Registry.For(ctx, tenant.String())
	if err != nil {
		return nil, nil, nil, err
	}
	conn, ok := reg.Get(ref)
	if !ok {
		return nil, nil, nil, knowledge.Plainly(nil, "the %s connector is not available", strings.TrimSuffix(ref, "@1"))
	}
	if name == "" {
		name = "main"
	}
	creds, err := s.Vault.Credentials(secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: purpose, Actor: knowledgeActor(ctx)}), tenant, env, conn.Manifest.ID, name)
	if errors.Is(err, secrets.ErrNotFound) {
		return nil, nil, nil, knowledge.Plainly(err, "the %s connection %q is missing: connect it again in Connections", conn.Manifest.Name, name)
	}
	if err != nil {
		return nil, nil, nil, err
	}
	g := s.Egress
	if g == nil {
		g = &egress.Guard{Logger: s.Logger}
	}
	hc := g.Client(egress.Policy{Tenant: tenant.String(), Hosts: conn.Manifest.HostsFor(creds), Purpose: "connector:" + conn.Manifest.ID}, 60*time.Second)
	return conn, creds, hc, nil
}

type knowledgeActorKey struct{}

// knowledgeActor is who a read is for, as recorded in the secret reads:
// the owner who pressed a button, else Taskiem's daily refresh.
func knowledgeActor(ctx context.Context) string {
	if a, ok := ctx.Value(knowledgeActorKey{}).(string); ok && a != "" {
		return a
	}
	return "system"
}

// readSheet is a Google Sheet range: its first row as the headings, each
// other row a line.
func (s *Server) readSheet(ctx context.Context, tenant uuid.UUID, src knowledge.Source) ([]knowledge.Section, error) {
	var cfg struct {
		Connection    string `json:"connection"`
		SpreadsheetID string `json:"spreadsheet_id"`
		Range         string `json:"range"`
	}
	if err := knowledgeConfig(src, &cfg); err != nil {
		return nil, err
	}
	conn, creds, hc, err := s.connectionFor(ctx, tenant, "googlesheets@1", cfg.Connection, knowledgePurpose)
	if err != nil {
		return nil, err
	}
	resp, err := conn.Actions["get_values"].Execute(ctx, connector.Request{Input: map[string]any{"spreadsheet_id": cfg.SpreadsheetID, "range": cfg.Range},
		Credentials: creds, HTTP: hc, Logger: s.Logger, Attempt: 1})
	if err != nil {
		return nil, err
	}
	out, _ := resp.Output.(map[string]any)
	values, _ := out["values"].([]any)
	link := "https://docs.google.com/spreadsheets/d/" + url.PathEscape(cfg.SpreadsheetID)
	return gridSections(values, src.Title, link, maxSheetRows, true), nil
}

// personalHeading is a sheet column whose heading says it holds personal
// contact details: it is left out, as a table's personal columns are.
var personalHeading = regexp.MustCompile(`(?i)\b(phone|mobile|tel|telephone|whatsapp|e-?mail|bvn|nin|account (no|number)|date of birth|dob)\b`)

// gridSections are rows of cells as lines, labelled by the first row.
// guard leaves out columns headed as personal contact details and masks
// phone numbers, emails and identity numbers in the rest (a sheet has no
// personal columns marked, as a table has).
func gridSections(values []any, title, link string, max int, guard bool) []knowledge.Section {
	if len(values) == 0 {
		return nil
	}
	cells := func(r any) []string {
		row, _ := r.([]any)
		out := make([]string, len(row))
		for i, c := range row {
			out[i] = strings.TrimSpace(fmt.Sprint(c))
		}
		return out
	}
	head := cells(values[0])
	var out []knowledge.Section
	var lines []string
	for i, r := range values[1:] {
		if i == max {
			break
		}
		var parts []string
		for j, c := range cells(r) {
			if c == "" {
				continue
			}
			if guard {
				if j < len(head) && personalHeading.MatchString(head[j]) {
					continue
				}
				c = pii.Redact(c)
			}
			if j < len(head) && head[j] != "" {
				parts = append(parts, head[j]+": "+c)
			} else {
				parts = append(parts, c)
			}
		}
		if len(parts) > 0 {
			lines = append(lines, strings.Join(parts, "; "))
		}
		if len(lines) == rowsPerSection {
			out = append(out, knowledge.Section{Title: title, Text: strings.Join(lines, "\n"), Link: link})
			lines = nil
		}
	}
	if len(lines) > 0 {
		out = append(out, knowledge.Section{Title: title, Text: strings.Join(lines, "\n"), Link: link})
	}
	return out
}

// readBooks is the items in the business's books (QuickBooks, Zoho Books,
// any rail with books_items) with their prices.
func (s *Server) readBooks(ctx context.Context, tenant uuid.UUID, src knowledge.Source) ([]knowledge.Section, error) {
	var cfg struct {
		Connector  string `json:"connector"`
		Connection string `json:"connection"`
	}
	if err := knowledgeConfig(src, &cfg); err != nil {
		return nil, err
	}
	ref := cfg.Connector
	if !strings.Contains(ref, "@") {
		ref += "@1"
	}
	conn, creds, hc, err := s.connectionFor(ctx, tenant, ref, cfg.Connection, knowledgePurpose)
	if err != nil {
		return nil, err
	}
	if _, ok := conn.Manifest.Capabilities["books_items"]; !ok {
		return nil, knowledge.Plainly(nil, "%s does not list its items for Taskiem", conn.Manifest.Name)
	}
	out, err := connector.CallCapability(ctx, conn, "books_items", map[string]any{},
		connector.Request{Credentials: creds, HTTP: hc, Logger: s.Logger, Attempt: 1}, func(string) (string, error) { return "", nil })
	if err != nil {
		return nil, err
	}
	items, _ := out["items"].([]any)
	var sections []knowledge.Section
	var lines []string
	for i, it := range items {
		if i == maxBooksItems {
			break
		}
		m, _ := it.(map[string]any)
		name := strings.TrimSpace(fmt.Sprint(m["name"]))
		if name == "" || name == "<nil>" {
			continue
		}
		line := name
		if k, ok := m["amount_kobo"].(float64); ok {
			line += ", " + storefront.FormatNaira(int64(k))
		} else if k, ok := m["amount_kobo"].(int64); ok {
			line += ", " + storefront.FormatNaira(k)
		}
		lines = append(lines, line)
		if len(lines) == rowsPerSection {
			sections = append(sections, knowledge.Section{Title: "Items and prices", Text: strings.Join(lines, "\n")})
			lines = nil
		}
	}
	if len(lines) > 0 {
		sections = append(sections, knowledge.Section{Title: "Items and prices", Text: strings.Join(lines, "\n")})
	}
	return sections, nil
}

// readFile is a file the owner gave: its text, extracted when it was
// given (the file itself is not kept).
func readFile(_ context.Context, _ uuid.UUID, src knowledge.Source) ([]knowledge.Section, error) {
	var cfg struct {
		Name string `json:"name"`
		Text string `json:"text"`
	}
	if err := knowledgeConfig(src, &cfg); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Text) == "" {
		return nil, errors.New("the file has no text")
	}
	return headedSections(cfg.Text, src.Title, ""), nil
}
