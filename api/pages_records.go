package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/areas"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/pages"
	"github.com/israel-duff/taskiem/engine/pii"
)

// Taskiem's own records on a page (docs/spec/pages.md §5, the contract's
// rule 10; engine/pages/records.go): a part whose source is
// {"records": "orders"} or {"records": "invoices"} reads the business's
// orders or invoices here, in the business's own transaction (row-level
// security, and the tenant named besides, as the Orders and Invoices
// screens read them), with SQL built from the view's fixed columns only.
//
// Unlike a table, a view is not read with the business's connection for
// everyone the page's roles let in: the viewer needs the area's own read
// permission (what the Orders or Invoices screen needs), or the part says
// "You cannot see orders." in place of its rows, and the rest of the page
// draws. Its personal columns (the customer's name, phone and email) are
// masked for every viewer, as the lists of those screens mask them; the
// envelope they are sealed in is opened only for the rows shown.

// pageAreas is whether each of the business's areas is on, for Check.
func pageAreas(ctx context.Context, tx pgx.Tx) (func(string) bool, error) {
	st, err := areas.Load(ctx, tx, principalFrom(ctx).TenantID)
	if err != nil {
		return nil, err
	}
	return func(a string) bool { return st.On(areas.Area(a)) }, nil
}

// recordsOf is the view a part reads ("" for a table or none).
func recordsOf(b pages.Block) string {
	if b.Source == nil {
		return ""
	}
	return b.Source.Records
}

// drawRecords works out a part over one of Taskiem's record views, as
// drawBlock does a table's.
func (s *Server) drawRecords(ctx context.Context, pc *pageCtx, b pages.Block, t datatables.Table, read pages.Read, cursor pages.Cursor, order *pages.Order, out map[string]any) map[string]any {
	v, _ := pages.RecordViewNamed(b.Source.Records)
	p := principalFrom(ctx)
	if b.Type == "figure" {
		out["label"], out["tone"], out["lead"], out["naira"] = b.Label, nonEmpty(b.Tone, "plain"), b.Lead, b.Naira
	} else {
		partHead(pc, b, t, order, out)
	}
	if !p.Can(v.Perm) {
		out["unavailable"] = v.Unseen()
		return out
	}
	failed := func(err error) map[string]any {
		s.Logger.Warn("pages: reading Taskiem's records", "page", pc.page.Name, "part", b.ID, "err", err)
		out["error"] = "Taskiem could not read this part just now. Pull to try again."
		return out
	}
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		if b.Type == "figure" {
			figure := func(extra []any) (float64, error) {
				if read.None {
					return 0, nil
				}
				r := read
				r.Filters = append(slices.Clone(read.Filters), extra...)
				var sql string
				var args []any
				var err error
				if b.Value == "sum" {
					sql, args, err = v.RecordsSum(r, b.Of)
				} else {
					sql, args, err = v.RecordsCount(r)
				}
				if err != nil {
					return 0, err
				}
				args[0] = p.TenantID
				var n float64
				return n, tx.QueryRow(ctx, sql, args...).Scan(&n)
			}
			var extra, before []any
			if ch := b.Change; ch != nil {
				col := columnNamed(t, ch.Column)
				from, to, prev := pages.Period(ch.Period, pc.values.Now)
				extra, before = pages.Between(col, from, to), pages.Between(col, prev, from)
			}
			n, err := figure(extra)
			if err != nil {
				return err
			}
			out["value"], out["shown"] = n, pages.FigureShown(b, n)
			if b.Change != nil {
				pv, err := figure(before)
				if err != nil {
					return err
				}
				out["change"] = map[string]any{"before": pv, "words": pages.ChangeWords(b.Change.Period, n, pv), "up": n > pv, "down": n < pv}
			}
			return nil
		}
		shown := []pages.Shown{}
		var more any
		count := int64(0)
		if !read.None {
			sql, args, err := v.RecordsCount(read)
			if err != nil {
				return err
			}
			args[0] = p.TenantID
			if err := tx.QueryRow(ctx, sql, args...).Scan(&count); err != nil {
				return err
			}
			sql, args, err = v.RecordsSelect(read, pages.RowsShown, cursor.Skip)
			if err != nil {
				return err
			}
			args[0] = p.TenantID
			raw, keys, err := s.recordRows(ctx, tx, v, sql, args)
			if err != nil {
				return err
			}
			shown = pages.Shape(b, t, raw, pc.values)
			for i := range shown {
				shown[i].Link = v.LinkOf(keys[i])
			}
			if next := cursor.Skip + pages.RowsShown; int64(next) < count && next <= pages.MaxRecordSkip {
				more = pages.Cursor{Order: pages.OrderKey(order), Skip: next}.String()
			}
		}
		out["rows"], out["more"], out["count"] = shown, more, count
		if b.Type == "list" && b.Group != nil {
			groups := []map[string]any{}
			for _, g := range pages.Groups(b, shown) {
				groups = append(groups, map[string]any{"key": g, "label": pages.GroupLabel(g)})
			}
			out["groups"] = groups
		}
		return nil
	})
	if err != nil {
		return failed(err)
	}
	return out
}

// recordRows reads a view's rows: each as Shape takes it, the personal
// columns from the record's sealed envelope (an erased customer's are
// empty), and each row's key.
func (s *Server) recordRows(ctx context.Context, tx pgx.Tx, v pages.RecordView, sql string, args []any) ([]map[string]any, []string, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, nil, err
	}
	type read struct {
		key     string
		env     []byte
		created *string
		values  []*string
	}
	var got []read
	for rows.Next() {
		r := read{values: make([]*string, len(v.Columns))}
		dest := []any{&r.key, &r.env, &r.created}
		for i := range r.values {
			dest = append(dest, &r.values[i])
		}
		if err := rows.Scan(dest...); err != nil {
			rows.Close()
			return nil, nil, err
		}
		got = append(got, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	tenant := principalFrom(ctx).TenantID
	out := make([]map[string]any, 0, len(got))
	keys := make([]string, 0, len(got))
	for _, r := range got {
		var opened map[string]any
		var env map[string]any
		if s.Vault != nil && json.Unmarshal(r.env, &env) == nil && env != nil {
			x, err := s.Vault.OpenTx(ctx, tx, tenant, env)
			switch {
			case errors.Is(err, pii.ErrErased):
			case err != nil:
				return nil, nil, err
			default:
				opened, _ = x.(map[string]any)
			}
		}
		out = append(out, v.RecordRow(r.key, r.created, r.values, opened))
		keys = append(keys, r.key)
	}
	return out, keys, nil
}

// listPageRecords is GET /v1/page-records: the views of Taskiem's own
// records a page may show, for the editor's Add a part: each with its
// columns (their type, and whether personal: shown masked, never narrowed,
// added up or ordered by), whether its area is on, and whether the caller
// may see it.
func (s *Server) listPageRecords(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var areaOn func(string) bool
	if err := s.tx(r, func(tx pgx.Tx) (err error) {
		areaOn, err = pageAreas(r.Context(), tx)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	list := []map[string]any{}
	for _, v := range pages.RecordViews {
		cols := []map[string]any{}
		for _, c := range v.Columns {
			cols = append(cols, map[string]any{"name": c.Name, "title": c.Title, "type": c.Type, "personal": c.Personal})
		}
		list = append(list, map[string]any{"name": v.Name, "title": v.Title, "on": areaOn(string(v.Area)), "may_read": p.Can(v.Perm),
			"area": pages.AreaWords(v.Area), "columns": cols})
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": list})
}
