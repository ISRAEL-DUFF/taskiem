package api

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// A spreadsheet from its link (no-technical-steps N2): the person pastes
// the link from their browser, and Taskiem reads the sheet's title and
// tabs through their Google Sheets connection, so nobody copies an id or
// types a range.

// sheetLinkRe finds a spreadsheet id in a Google Sheets link
// (docs.google.com/spreadsheets/d/<id>/edit…), or takes a bare id.
var sheetLinkRe = regexp.MustCompile(`(?:/spreadsheets/d/|^)([A-Za-z0-9_-]{20,})`)

// SpreadsheetID returns the id in a pasted link or id, or "".
func SpreadsheetID(link string) string {
	m := sheetLinkRe.FindStringSubmatch(strings.TrimSpace(link))
	if m == nil {
		return ""
	}
	return m[1]
}

// resolveSpreadsheet answers POST /v1/sheets/resolve with {"link": "...",
// "connection": "main"}: the spreadsheet's id, title and tabs, read with
// the named Google Sheets connection in prod (or the only one).
func (s *Server) resolveSpreadsheet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Link       string `json:"link"`
		Connection string `json:"connection"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	sheet := SpreadsheetID(req.Link)
	if sheet == "" {
		s.fail(w, r, fmt.Errorf("%w: that is not a Google Sheets link; open the sheet in your browser and copy the address", errBadRequest))
		return
	}
	p := principalFrom(r.Context())
	const env, conn = "prod", "googlesheets"
	var name string
	if err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT name FROM connections WHERE environment = $1 AND connector = $2 AND status = 'active' AND ($3 = '' OR name = $3)`,
			env, conn, req.Connection)
		if err != nil {
			return err
		}
		names, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		switch len(names) {
		case 0:
			return fmt.Errorf("%w: connect Google Sheets first", errConflict)
		case 1:
			name = names[0]
			return nil
		}
		return fmt.Errorf("%w: you have more than one Google Sheets connection; choose which one", errBadRequest)
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	reg, err := s.Registry.For(r.Context(), p.TenantID.String())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	c, ok := connectorByID(reg, conn)
	if !ok {
		s.fail(w, r, fmt.Errorf("%w: Google Sheets is not available here", errBadRequest))
		return
	}
	creds, err := s.Vault.Credentials(secrets.WithUse(r.Context(), secrets.Use{Kind: secrets.KindConnection, Purpose: "sheets.resolve", Actor: p.Actor()}), p.TenantID, env, conn, name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	g := s.Egress
	if g == nil {
		g = &egress.Guard{Logger: s.Logger}
	}
	hc := g.Client(egress.Policy{Tenant: p.TenantID.String(), Hosts: c.Manifest.HostsFor(creds), Purpose: "connector:" + conn}, 20*time.Second)
	resp, err := c.Actions["get_spreadsheet"].Execute(r.Context(), connector.Request{Input: map[string]any{"spreadsheet_id": sheet}, Credentials: creds, HTTP: hc, Logger: s.Logger, Attempt: 1})
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: Taskiem cannot open that sheet with your Google account. Check the link, and that the account you connected can open it", errBadRequest))
		return
	}
	out, _ := resp.Output.(map[string]any)
	var tabs []string
	if list, ok := out["sheets"].([]any); ok {
		for _, t := range list {
			if m, ok := t.(map[string]any); ok {
				if title, _ := m["title"].(string); title != "" {
					tabs = append(tabs, title)
				}
			}
		}
	}
	title, _ := out["title"].(string)
	writeJSON(w, http.StatusOK, map[string]any{"spreadsheet_id": sheet, "title": title, "tabs": nonNil(tabs)})
}
