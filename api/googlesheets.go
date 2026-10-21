package api

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
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
	out, err := s.connectorLookup(r, "googlesheets", "Google Sheets", req.Connection, "get_spreadsheet", "sheets.resolve", map[string]any{"spreadsheet_id": sheet})
	if isLookup(err) {
		err = fmt.Errorf("%w: Taskiem cannot open that sheet with your Google account. Check the link, and that the account you connected can open it", errBadRequest)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
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
