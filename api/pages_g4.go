package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/pages"
)

// A board and a chart (docs/spec/pages.md G4, engine/pages/g4.go).
//
// A board's cards are read a column at a time (each column's first rows,
// its count, and More for that column alone: GET .../blocks/{id}?lane=),
// shaped as a list's rows are, masked for the viewer before any expression
// sees them. Moving a card is POST /v1/pages/{name}/act with to, the
// column it goes to: the card is read again as the part shows it to this
// person, its column now checked, and the board's move tool given filled
// in, exactly as a row's tool button is (actTool: a tool_buttons row of the
// page, held at the start to the tool's roles, care and the button; the
// start's Idempotency-Key makes a repeated drop one run).
//
// A chart's figures are worked out here, from the table's rows (counted or
// added up, by period or by group, over at most 5,000 rows) or from a
// tool's list; the web and the app draw the points and marks they are
// given and the table behind them, and work nothing out.

type readRowsFunc func(in map[string]any) (map[string]any, error)

// readPartRows reads a list's, a table's or a board column's rows: the
// first page (or the one after cursor), shaped for the viewer, the count
// and More's cursor (nil at the end).
func (s *Server) readPartRows(b pages.Block, t datatables.Table, read pages.Read, readRows readRowsFunc, cursor pages.Cursor, order *pages.Order, vals pages.Values) ([]pages.Shown, int64, any, error) {
	shown := []pages.Shown{}
	if read.None {
		return shown, 0, nil, nil
	}
	ranked := pages.RankedByPersonal(b, t, order, vals.Viewer.Personal)
	in := map[string]any{"filters": read.Filters, "order": read.Order, "limit": pages.RowsShown, "count": "exact"}
	if ranked {
		in["limit"] = cursor.Skip + pages.RowsShown
	} else if cursor.After != "" {
		in["cursor"] = cursor.After
	}
	res, err := readRows(in)
	if err != nil {
		return nil, 0, nil, err
	}
	var rows []map[string]any
	for _, row := range rowsOf(res) {
		if m, ok := row.(map[string]any); ok {
			rows = append(rows, m)
		}
	}
	if ranked {
		rows = rows[min(cursor.Skip, len(rows)):]
	}
	shown = pages.Shape(b, t, rows, vals)
	var more any
	if c, _ := res["next_cursor"].(string); c != "" {
		next := pages.Cursor{Order: pages.OrderKey(order), After: c}
		if ranked {
			next = pages.Cursor{Order: pages.OrderKey(order), Skip: cursor.Skip + pages.RowsShown}
		}
		if !ranked || next.Skip <= pages.MaxSkip {
			more = next.String()
		}
	}
	return shown, countOf(res), more, nil
}

// boardHead is a board's head as drawn: its title and size, the words
// when a column is empty, and the move (greyed, with why, for someone
// the tool is not for).
func (s *Server) boardHead(ctx context.Context, pc *pageCtx, b pages.Block, out map[string]any) {
	out["title"], out["size"], out["empty"] = b.Title, nonEmpty(b.Size, "full"), nonEmpty(b.Empty, "Nothing here")
	out["has_detail"] = b.Detail != nil
	if m := b.Board.Move; m != nil {
		a := s.markActions(ctx, pc, []pages.ShownAction{{Label: m.Label, Kind: pages.ActTool, Tool: m.Tool}})[0]
		mv := map[string]any{"label": a.Label, "tool": a.Tool}
		if a.Disabled != "" {
			mv["disabled"] = a.Disabled
		}
		out["move"] = mv
	}
}

// lane is one of a board's columns as drawn, its rows' buttons greyed and
// their runs marked as a list's are.
func (s *Server) lane(ctx context.Context, pc *pageCtx, b pages.Block, c pages.BoardColumn, rows []pages.Shown, count int64, more any) map[string]any {
	lo := map[string]any{"value": c.Value, "label": c.LabelOf(), "rows": rows, "count": count, "more": more}
	s.markRows(ctx, pc, lo)
	s.markRowRuns(ctx, pc, b, lo)
	return lo
}

// drawBoard draws a board over a business table: each column's first
// rows (or, for More, the rows after cursor of the column pc.lane names).
func (s *Server) drawBoard(ctx context.Context, pc *pageCtx, b pages.Block, t datatables.Table, read pages.Read, readRows readRowsFunc, cursor pages.Cursor, out map[string]any) map[string]any {
	s.boardHead(ctx, pc, b, out)
	lanes := []map[string]any{}
	total := int64(0)
	for _, c := range b.Board.Columns {
		if pc.lane != "" && c.Value != pc.lane {
			continue
		}
		lr := read
		lr.Filters = append(slices.Clone(read.Filters), pages.LaneFilter(b, c.Value))
		cur := pages.Cursor{}
		if pc.lane != "" {
			cur = cursor
		}
		rows, count, more, err := s.readPartRows(b, t, lr, readRows, cur, nil, pc.values)
		if err != nil {
			s.Logger.Warn("pages: reading a board", "page", pc.page.Name, "part", b.ID, "err", err)
			out["error"] = "Taskiem could not read this part just now. Pull to try again."
			return out
		}
		lanes = append(lanes, s.lane(ctx, pc, b, c, rows, count, more))
		total += count
	}
	out["lanes"], out["count"] = lanes, total
	return out
}

// drawToolBoard draws a board over a tool's list: its rows (the first
// 25, as a list's) in the columns their value names.
func (s *Server) drawToolBoard(ctx context.Context, pc *pageCtx, b pages.Block, outputs map[string]any, out map[string]any) map[string]any {
	s.boardHead(ctx, pc, b, out)
	rows, _ := pages.ShapeTool(b, outputs, pc.values)
	by := pages.ToolLanes(b, outputs, rows)
	lanes := []map[string]any{}
	total := int64(0)
	for _, c := range b.Board.Columns {
		r := by[c.Value]
		if r == nil {
			r = []pages.Shown{}
		}
		lanes = append(lanes, s.lane(ctx, pc, b, c, r, int64(len(r)), nil))
		total += int64(len(r))
	}
	out["lanes"], out["count"] = lanes, total
	return out
}

// chartHead is a chart's head as drawn.
func chartHead(b pages.Block, out map[string]any) {
	out["title"], out["size"], out["naira"] = b.Title, nonEmpty(b.Size, "full"), b.Naira
	out["empty"] = nonEmpty(b.Empty, "Nothing to show yet")
}

// chartOut puts a chart's figures on its part.
func chartOut(c pages.ChartShown, out map[string]any) map[string]any {
	out["kind"], out["points"], out["ticks"], out["zero"] = c.Kind, c.Points, c.Ticks, c.Zero
	out["label_head"], out["value_head"], out["summary"] = c.LabelHead, c.ValueHead, c.Summary
	if c.Over != "" {
		out["over"] = c.Over
	}
	return out
}

// drawChart works a chart out over a business table: its rows (by its own
// conditions; over time, within the periods it shows) read a thousand at
// a time, at most 5,000, counted or added up by period or by group.
func (s *Server) drawChart(ctx context.Context, pc *pageCtx, b pages.Block, t datatables.Table, read pages.Read, readRows readRowsFunc, out map[string]any) map[string]any {
	chartHead(b, out)
	c := *b.Chart
	filters := slices.Clone(read.Filters)
	if c.Period != "" {
		from, to := pages.ChartSpan(c, pc.values.Now)
		filters = append(filters, pages.Between(columnNamed(t, c.By), from, to)...)
	}
	sel := "id," + c.By
	if c.Of != "" && c.Of != c.By {
		sel += "," + c.Of
	}
	var rows []map[string]any
	over := false
	cur := ""
	for {
		in := map[string]any{"filters": filters, "limit": 1000, "select": sel, "order": []any{map[string]any{"column": "id", "desc": true}}}
		if cur != "" {
			in["cursor"] = cur
		}
		res, err := readRows(in)
		if err != nil {
			s.Logger.Warn("pages: reading a chart", "page", pc.page.Name, "part", b.ID, "err", err)
			out["error"] = "Taskiem could not read this part just now. Pull to try again."
			return out
		}
		for _, row := range rowsOf(res) {
			if m, ok := row.(map[string]any); ok {
				rows = append(rows, m)
			}
		}
		cur, _ = res["next_cursor"].(string)
		if cur == "" {
			break
		}
		if len(rows) >= datatables.QuerySumRows {
			over = true
			break
		}
	}
	return chartOut(pages.ChartFromRows(b, t, rows, pc.values.Now, over), out)
}

// actMove moves a board's card to another column: the card read again as
// the part shows this person, the column it is in now, and the move
// tool's form given filled in (actTool), exactly as a row's button is.
func (s *Server) actMove(w http.ResponseWriter, r *http.Request, pc *pageCtx, i int, req pageActReq) {
	b := pc.page.Blocks[i]
	if b.Type != "board" || b.Board == nil || b.Board.Move == nil {
		writeErr(w, http.StatusNotFound, "these cards do not move")
		return
	}
	if _, broken := pc.broken[i]; broken {
		writeErr(w, http.StatusConflict, "this part needs fixing before its cards move")
		return
	}
	to := pages.LaneOf(b, req.To)
	if to == "" {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("this board has no column %q", req.To))
		return
	}
	act, err := s.pageRowAct(r.Context(), pc, b, req.Row)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	from := pages.CardLane(b, act)
	switch from {
	case "":
		writeJSON(w, http.StatusConflict, map[string]any{"error": "This card is not on the board any more.", "code": "moved_on"})
		return
	case to:
		writeJSON(w, http.StatusConflict, map[string]any{"error": "This card is in that column already.", "code": "moved_on"})
		return
	}
	act["to"] = to
	req.To = to
	s.actTool(w, r, pc, b, *b.Board.Move, req, act)
}

// g4ShortLine is a board or a chart on a line of a page's short form in a
// chat (pages §9): a board's columns with how many cards each holds; a
// chart's last four points. "" when it could not be drawn for them.
func g4ShortLine(b pages.Block, out map[string]any) string {
	if out["unavailable"] != nil || out["error"] != nil || out["working"] != nil {
		return ""
	}
	var parts []string
	switch b.Type {
	case "board":
		lanes, _ := out["lanes"].([]map[string]any)
		for _, l := range lanes {
			parts = append(parts, fmt.Sprintf("%v %v", l["label"], l["count"]))
		}
	case "chart":
		points, _ := out["points"].([]pages.Point)
		for _, p := range points[max(0, len(points)-4):] {
			parts = append(parts, p.Label+" "+p.Shown)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	line := strings.Join(parts, " · ")
	if b.Title != "" {
		line = "*" + b.Title + "*: " + line
	}
	return line
}
