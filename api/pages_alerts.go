package api

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/pages"
)

// A page as an alert (docs/spec/pages.md §9, G2): a figure that counts,
// marked "alert": true, is an alert while its count is above zero. The
// alerts at the end of the person's Taskiem conversation show the page's
// name and the count, and open the page, as the hand-built Confirm alert
// does. Only a published page the person may open, worked out for them as
// the page is (its roles, the area's permission for Taskiem's records).

// maxPageAlerts is how many pages' alerts are worked out for one look.
const maxPageAlerts = 5

// pageAlerts are the person's alerts from pages.
func (s *Server) pageAlerts(ctx context.Context, p *Principal) ([]chatAlert, error) {
	type found struct {
		row  pageRow
		page *pages.Page
	}
	var list []found
	var tables []datatables.Table
	var areaOn func(string) bool
	var email string
	err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		// Only pages whose published version has an alert are read further.
		rows, err := tx.Query(ctx, `SELECT `+pageCols+pageFrom+` WHERE p.published_version IS NOT NULL
			AND jsonb_path_exists(v.document, '$.blocks[*] ? (@.alert == true)') ORDER BY p.pinned DESC, p.pin_order, p.name LIMIT 20`)
		if err != nil {
			return err
		}
		all, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (pageRow, error) { return scanPage(row) })
		if err != nil {
			return err
		}
		for _, pg := range all {
			if !mayOpenPage(p, pg) || len(list) == maxPageAlerts {
				continue
			}
			if page, ps := pages.Parse(pg.Published); len(ps) == 0 {
				list = append(list, found{pg, page})
			}
		}
		if len(list) == 0 {
			return nil
		}
		if tables, err = datatables.ListTables(ctx, tx); err != nil {
			return err
		}
		if areaOn, err = pageAreas(ctx, tx); err != nil {
			return err
		}
		var e *string
		_ = tx.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, p.UserID).Scan(&e)
		if e != nil {
			email = *e
		}
		return nil
	})
	if err != nil || len(list) == 0 {
		return nil, err
	}
	var out []chatAlert
	now := time.Now()
	for _, f := range list {
		viewer := pages.Viewer{Email: email, Personal: p.Can(PermTableRead)}
		vals, _ := f.page.FilterValues(nil, viewer, now)
		pc := &pageCtx{row: f.row, page: f.page, tables: tables, values: vals, broken: map[int]string{}, off: map[int]string{}}
		problems := pages.Check(f.page, pages.Env{Tables: tables, AreaOn: areaOn})
		for i, b := range f.page.Blocks {
			if !b.Alert || b.Type != "figure" || b.Value != "count" || blockHasProblem(problems, i) {
				continue
			}
			if v, ok := pages.RecordViewNamed(recordsOf(b)); ok && (!areaOn(string(v.Area)) || !p.Can(v.Perm)) {
				continue
			}
			drawn := s.drawPart(ctx, pc, b, pages.Cursor{}, nil)
			n, _ := drawn["value"].(float64)
			if n <= 0 {
				continue
			}
			out = append(out, chatAlert{Key: "page:" + f.row.Name + ":" + b.ID, Kind: "page", At: now,
				Title: f.page.Title, Line: fmt.Sprintf("%s: %s", b.Label, pages.FigureShown(b, n)), Reference: f.row.Name})
		}
	}
	return out, nil
}

// blockHasProblem says whether part i has a problem (it is not drawn).
func blockHasProblem(ps []pages.Problem, i int) bool {
	prefix := fmt.Sprintf("/blocks/%d", i)
	for _, p := range ps {
		if p.Path == prefix || len(p.Path) > len(prefix) && p.Path[:len(prefix)+1] == prefix+"/" {
			return true
		}
	}
	return false
}
