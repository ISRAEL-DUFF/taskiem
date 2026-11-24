package pages

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// A board and a chart (pages §4, G4).
//
// A board is a list in columns by a choice (Open, Doing, Done), fed by a
// business table or a tool's list; a card moved to another column runs a
// tool (board.move), held to the tool's roles and care exactly as a page's
// button is: the API reads the card again as the part shows it to the
// person, works the tool's values out from it and the column it goes to
// (to), and gives the tool's form filled in (api/pages_g4.go).
//
// A chart is bars or a line: one measure over time (a date column's days,
// weeks or months) or by a group (a column's values), added up from a
// business table; or a tool's list, a label and a number for each row. The
// server works out every figure: the points, their places on the scale,
// the scale's marks and the words a screen reader says. The web and the
// app only draw what they are given.

// Limits (§4).
const (
	MaxBoardColumns  = 6
	MaxChartPoints   = 12
	DefaultChartLast = 8
)

// Board is a board part's columns, and the tool a card moved runs.
type Board struct {
	// Column is the choice a card's column is read from: a table's text
	// column, or the field of a tool's row (item.status).
	Column  string        `json:"column"`
	Columns []BoardColumn `json:"columns"`
	// Move is the tool a card moved to another column opens, its values
	// over the row (or item) and to, the column it goes to.
	Move *Action `json:"move,omitempty"`
}

// BoardColumn is one of a board's columns: the value its cards hold, and
// its heading.
type BoardColumn struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// Chart is what a chart part shows.
type Chart struct {
	Kind string `json:"kind"` // bars or line
	// From a table: Of, the column added up (none: the rows counted); By,
	// a date column (with Period: day, week or month, the Last so many) or
	// a column whose values are the bars.
	Of     string `json:"of,omitempty"`
	By     string `json:"by,omitempty"`
	Period string `json:"period,omitempty"`
	Last   int    `json:"last,omitempty"`
	// From a tool's list: each row's Label and Value, over item.
	Label string `json:"label,omitempty"`
	Value string `json:"value,omitempty"`
}

// moveEngine reads a board's move over a table's row and the column a
// card goes to; toolMoveEngine over a tool's item.
var (
	moveEngine     = expr.MustNewWithRoots("row", "filters", "to")
	toolMoveEngine = expr.MustNewWithRoots("outputs", "item", "filters", "to")
)

// LabelOf is a board column's heading.
func (c BoardColumn) LabelOf() string {
	if c.Label != "" {
		return c.Label
	}
	return GroupLabel(c.Value)
}

// checkBoard is Parse's check of a board part.
func (b Block) checkBoard(name, at string, rowEngine *expr.Engine, fromTool bool, ps *problems) {
	if b.Item == nil || strings.TrimSpace(b.Item.Title) == "" {
		ps.add(at+"/item", "%s needs what each card's title shows.", name)
	} else {
		checkValueIn(rowEngine, at+"/item/title", b.Item.Title, ps)
		if b.Item.Sub != "" {
			checkValueIn(rowEngine, at+"/item/sub", b.Item.Sub, ps)
		}
		if s := b.Item.State; s != nil {
			checkValueIn(rowEngine, at+"/item/state/text", s.Text, ps)
		}
	}
	bd := b.Board
	if bd == nil {
		ps.add(at+"/board", "%s needs the choice its columns are by, and the columns.", name)
		return
	}
	if strings.TrimSpace(bd.Column) == "" {
		ps.add(at+"/board/column", "%s puts its cards in columns by a choice: say which.", name)
	}
	if len(bd.Columns) < 2 || len(bd.Columns) > MaxBoardColumns {
		ps.add(at+"/board/columns", "%s has 2 to %d columns.", name, MaxBoardColumns)
	}
	seen := map[string]bool{}
	for j, c := range bd.Columns {
		v := strings.ToLower(strings.TrimSpace(c.Value))
		if v == "" || seen[v] || len([]rune(c.Value)) > 100 {
			ps.add(fmt.Sprintf("%s/board/columns/%d/value", at, j), "Each column of %s needs the value its cards hold, given once.", name)
		}
		seen[v] = true
		if len([]rune(c.Label)) > 30 {
			ps.add(fmt.Sprintf("%s/board/columns/%d/label", at, j), "A column's heading is at most 30 letters.")
		}
	}
	if m := bd.Move; m != nil {
		e := moveEngine
		if fromTool {
			e = toolMoveEngine
		}
		if m.Kind() != ActTool {
			ps.add(at+"/board/move", "Moving a card on %s opens one of the business's tools.", name)
		} else {
			m.check(at+"/board/move", e, ps)
		}
		if m.Once || m.When != "" {
			ps.add(at+"/board/move", "Moving a card on %s works for every card, as often as it is moved: it has no once and no when.", name)
		}
	}
	if b.Group != nil || b.Columns != nil || b.Value != "" || b.Alert || b.Chart != nil {
		ps.add(at, "A board shows cards in columns.")
	}
}

// checkChart is Parse's check of a chart part.
func (b Block) checkChart(name, at string, fromTool bool, ps *problems) {
	c := b.Chart
	if c == nil {
		ps.add(at+"/chart", "%s needs what it shows: bars or a line, and of what.", name)
		return
	}
	if c.Kind != "bars" && c.Kind != "line" {
		ps.add(at+"/chart/kind", "%s is bars or a line.", name)
	}
	if fromTool {
		if strings.TrimSpace(c.Label) == "" || strings.TrimSpace(c.Value) == "" {
			ps.add(at+"/chart", "%s shows a label and a number from each row the tool gives (item.week, item.total).", name)
		} else {
			checkValueIn(toolEngine, at+"/chart/label", c.Label, ps)
			checkValueIn(toolEngine, at+"/chart/value", c.Value, ps)
		}
		if c.Of != "" || c.By != "" || c.Period != "" || c.Last != 0 {
			ps.add(at+"/chart", "%s is worked out by a tool: it shows each row's label and number, not a table's columns.", name)
		}
	} else {
		if strings.TrimSpace(c.By) == "" {
			ps.add(at+"/chart/by", "%s shows its measure over time (a date column) or by a group (a column): say which.", name)
		}
		if c.Period != "" && !slices.Contains([]string{"day", "week", "month"}, c.Period) {
			ps.add(at+"/chart/period", "%s goes by day, week or month.", name)
		}
		if c.Period == "" && c.Last != 0 {
			ps.add(at+"/chart/last", "%s shows how many days, weeks or months only over time.", name)
		}
		if c.Last != 0 && (c.Last < 2 || c.Last > MaxChartPoints) {
			ps.add(at+"/chart/last", "%s shows 2 to %d periods.", name, MaxChartPoints)
		}
		if c.Period == "" && c.Kind == "line" {
			ps.add(at+"/chart/kind", "%s is a line, which shows a measure over time: by a group, draw bars.", name)
		}
		if c.Label != "" || c.Value != "" {
			ps.add(at+"/chart", "%s adds up a table's column: only a tool's chart reads each row's label and number.", name)
		}
	}
	if b.Item != nil || b.Columns != nil || b.Detail != nil || b.Group != nil || b.Value != "" || b.Actions != nil || b.Alert || b.Board != nil || b.RowStatus != nil {
		ps.add(at, "A chart shows one measure, as bars or a line.")
	}
	if b.Source != nil && len(b.Source.Uses) > 0 {
		ps.add(at+"/source/uses", "%s adds up every row its own conditions pick: the page's filters narrow its lists, tables and boards only.", name)
	}
}

// checkG4Env holds a board's or a chart's table to the business (Check).
func checkG4Env(ps *problems, at, name string, b Block, t datatables.Table, env Env) {
	col := func(path, c, what string) (datatables.Column, bool) {
		found, ok := columnOf(t, c)
		if !ok {
			ps.add(path, "%s %s %s, which the table %s does not have.", name, what, c, tableTitle(t))
		}
		return found, ok
	}
	switch b.Type {
	case "board":
		if b.Board == nil {
			return
		}
		if c, ok := col(at+"/board/column", b.Board.Column, "puts its cards in columns by"); ok {
			switch {
			case c.Personal:
				ps.add(at+"/board/column", "%s cannot put its cards in columns by %s: it is personal.", name, columnTitle(c))
			case c.Type != "text":
				ps.add(at+"/board/column", "%s puts its cards in columns by %s, which does not hold words.", name, columnTitle(c))
			}
		}
		if b.Board.Move != nil {
			checkActionEnv(ps, at+"/board/move", *b.Board.Move, env)
			checkActionReads(ps, at+"/board/move", *b.Board.Move, t, name)
		}
	case "chart":
		c := b.Chart
		if c == nil {
			return
		}
		if by, ok := col(at+"/chart/by", c.By, "is drawn by"); ok {
			switch {
			case by.Personal:
				ps.add(at+"/chart/by", "%s cannot be drawn by %s: it is personal.", name, columnTitle(by))
			case c.Period != "" && by.Type != "date" && by.Type != "datetime":
				ps.add(at+"/chart/by", "%s goes over time by %s, which does not hold dates.", name, columnTitle(by))
			case c.Period == "" && by.Type != "text" && by.Type != "boolean":
				ps.add(at+"/chart/by", "%s puts a bar for each of %s's values: pick a column of words, or go over time by a date.", name, columnTitle(by))
			}
		}
		if c.Of != "" {
			if of, ok := col(at+"/chart/of", c.Of, "adds up the column"); ok {
				switch {
				case of.Type != "integer" && of.Type != "number":
					ps.add(at+"/chart/of", "%s adds up %s, which does not hold numbers.", name, columnTitle(of))
				case of.Personal:
					ps.add(at+"/chart/of", "%s cannot add up %s: it is personal.", name, columnTitle(of))
				}
			}
		}
	}
}

// --- a board, drawn ---

// LaneOf is the board column a row is in, by its value ("" when none: the
// row is not on the board).
func LaneOf(b Block, value string) string {
	if b.Board == nil {
		return ""
	}
	v := strings.TrimSpace(value)
	for _, c := range b.Board.Columns {
		if strings.EqualFold(strings.TrimSpace(c.Value), v) {
			return c.Value
		}
	}
	return ""
}

// LaneFilter is the condition a table's rows in one of a board's columns
// meet (any case, as a choice filter matches).
func LaneFilter(b Block, lane string) any {
	return map[string]any{"column": b.Board.Column, "op": "ilike", "value": likeEscape.Replace(lane)}
}

// CardLane is the column a row (as act holds it, the row a viewer is
// shown, or a tool's item) is in now.
func CardLane(b Block, act map[string]any) string {
	if b.Board == nil {
		return ""
	}
	var v any
	if row, ok := act["row"].(map[string]any); ok {
		v = row[b.Board.Column]
	} else if item, ok := act["item"].(map[string]any); ok {
		v = item[b.Board.Column]
	}
	return LaneOf(b, cellText(v))
}

// ToolLanes puts a tool's rows (ShapeTool's, by their place) in a board's
// columns: a row whose value is none of them is left off the board.
func ToolLanes(b Block, outputs map[string]any, rows []Shown) map[string][]Shown {
	items, _ := outputs[b.Source.Output].([]any)
	out := map[string][]Shown{}
	for _, r := range rows {
		i, err := strconv.Atoi(r.ID)
		if err != nil || i < 0 || i >= len(items) {
			continue
		}
		item, _ := items[i].(map[string]any)
		if lane := LaneOf(b, cellText(item[b.Board.Column])); lane != "" {
			out[lane] = append(out[lane], r)
		}
	}
	return out
}

// --- a chart, worked out ---

// Point is one bar or one point of a line, as drawn: its label on the
// axis (Tick: drawn under the axis, else left to the table and the
// tooltip), its long words for the table and a screen reader, its value
// as worked out and as written, and its place on the scale (0 at the
// scale's foot, 1 at its top).
type Point struct {
	Label string  `json:"label"`
	Long  string  `json:"long"`
	Value float64 `json:"value"`
	Shown string  `json:"shown"`
	Share float64 `json:"share"`
	Tick  bool    `json:"tick"`
}

// Tick is one mark on a chart's scale.
type Tick struct {
	Value float64 `json:"value"`
	Shown string  `json:"shown"`
	Share float64 `json:"share"`
}

// ChartShown is a chart as drawn.
type ChartShown struct {
	Kind   string  `json:"kind"`
	Points []Point `json:"points"`
	Ticks  []Tick  `json:"ticks"`
	// Zero is where nought is on the scale (the bars' foot).
	Zero float64 `json:"zero"`
	// LabelHead and ValueHead head the table behind the chart.
	LabelHead string `json:"label_head"`
	ValueHead string `json:"value_head"`
	// Summary is the chart in words (its accessible name).
	Summary string `json:"summary"`
	Over    string `json:"over,omitempty"`
}

// ChartPeriod is one day, week or month a chart over time shows.
type ChartPeriod struct {
	From, To    time.Time
	Label, Long string
}

// ChartPeriods are the periods a chart over time shows, oldest first, the
// last the one now is in (Lagos).
func ChartPeriods(c Chart, now time.Time) []ChartPeriod {
	n := c.Last
	if n == 0 {
		n = DefaultChartLast
	}
	from, _, _ := DateRange(map[string]string{"day": "today", "week": "week", "month": "month"}[c.Period], now)
	out := make([]ChartPeriod, n)
	for i := 0; i < n; i++ {
		back := n - 1 - i
		var p ChartPeriod
		switch c.Period {
		case "day":
			p.From = from.AddDate(0, 0, -back)
			p.To = p.From.AddDate(0, 0, 1)
			p.Label = p.From.Format("Mon 2")
			p.Long = p.From.Format("Monday 2 January")
		case "week":
			p.From = from.AddDate(0, 0, -7*back)
			p.To = p.From.AddDate(0, 0, 7)
			p.Label = p.From.Format("2 Jan")
			p.Long = "Week of " + p.From.Format("2 January")
		default:
			p.From = from.AddDate(0, -back, 0)
			p.To = p.From.AddDate(0, 1, 0)
			p.Label = p.From.Format("Jan")
			p.Long = p.From.Format("January 2006")
		}
		out[i] = p
	}
	return out
}

// ChartSpan is the days a chart over time reads, from (inclusive) to
// (exclusive).
func ChartSpan(c Chart, now time.Time) (from, to time.Time) {
	ps := ChartPeriods(c, now)
	return ps[0].From, ps[len(ps)-1].To
}

// ChartFromRows works a chart out from a table's rows (read with the
// part's conditions, and over time within ChartSpan): each row counted, or
// its Of added up, under its period or its group. over: the rows read
// stopped at the sum's limit.
func ChartFromRows(b Block, t datatables.Table, rows []map[string]any, now time.Time, over bool) ChartShown {
	c := *b.Chart
	byCol, _ := columnOf(t, c.By)
	value := func(raw map[string]any) (float64, bool) {
		if c.Of == "" {
			return 1, true
		}
		return chartNumber(raw[c.Of])
	}
	var labels, longs []string
	var vals []float64
	if c.Period != "" {
		periods := ChartPeriods(c, now)
		vals = make([]float64, len(periods))
		for _, p := range periods {
			labels, longs = append(labels, p.Label), append(longs, p.Long)
		}
		for _, raw := range rows {
			at, ok := chartTime(byCol, raw[c.By])
			if !ok {
				continue
			}
			v, ok := value(raw)
			if !ok {
				continue
			}
			for i, p := range periods {
				if !at.Before(p.From) && at.Before(p.To) {
					vals[i] += v
					break
				}
			}
		}
	} else {
		sums := map[string]float64{}
		var keys []string
		for _, raw := range rows {
			v, ok := value(raw)
			if !ok {
				continue
			}
			k := groupKey(raw[c.By])
			if byCol.Type == "boolean" {
				k = map[string]string{"true": "Yes", "false": "No"}[k]
			}
			if _, seen := sums[k]; !seen {
				keys = append(keys, k)
			}
			sums[k] += v
		}
		sort.SliceStable(keys, func(i, j int) bool { return sums[keys[i]] > sums[keys[j]] })
		others := len(keys) > MaxChartPoints
		other := 0.0
		if others {
			for _, k := range keys[MaxChartPoints-1:] {
				other += sums[k]
			}
			keys = keys[:MaxChartPoints-1]
		}
		for _, k := range keys {
			labels, longs, vals = append(labels, GroupLabel(k)), append(longs, GroupLabel(k)), append(vals, sums[k])
		}
		if others {
			labels, longs, vals = append(labels, "Others"), append(longs, "All the others"), append(vals, other)
		}
	}
	out := chartOf(b, labels, longs, vals)
	out.LabelHead = map[string]string{"day": "Day", "week": "Week", "month": "Month"}[c.Period]
	if out.LabelHead == "" {
		out.LabelHead = columnTitle(byCol)
	}
	out.ValueHead = "How many"
	if c.Of != "" {
		if of, ok := columnOf(t, c.Of); ok {
			out.ValueHead = columnTitle(of)
		}
	}
	if over {
		out.Over = "Added up over the first 5,000 rows."
	}
	return out
}

// ChartFromTool works a chart out from a tool's list: each row's label
// and number (over item), in the tool's order, the first twelve. A row
// whose number is not one is left out.
func ChartFromTool(b Block, outputs map[string]any, v Values) ChartShown {
	items, _ := outputs[b.Source.Output].([]any)
	var labels []string
	var vals []float64
	more := false
	for _, it := range items {
		act := ToolAct(outputs, it, v)
		n, ok := chartNumber(evalIn(toolEngine, b.Chart.Value, act))
		if !ok {
			continue
		}
		if len(vals) == MaxChartPoints {
			more = true
			break
		}
		labels = append(labels, clip(whatsapp.SafeText(sayIn(toolEngine, b.Chart.Label, act))))
		vals = append(vals, n)
	}
	out := chartOf(b, labels, labels, vals)
	out.LabelHead, out.ValueHead = "What", "How many"
	if b.Naira {
		out.ValueHead = "Amount"
	}
	if more {
		out.Over = fmt.Sprintf("Shows the first %d.", MaxChartPoints)
	}
	return out
}

func evalIn(e *expr.Engine, v string, act map[string]any) any {
	src, isExpr := strings.CutPrefix(v, "=")
	if !isExpr {
		return v
	}
	out, err := e.Eval(src, act)
	if err != nil {
		return nil
	}
	return out
}

// chartOf places the points on a scale of round marks, from nought (or
// the lowest value, below it) to just over the highest.
func chartOf(b Block, labels, longs []string, vals []float64) ChartShown {
	out := ChartShown{Kind: b.Chart.Kind, Points: []Point{}, Ticks: []Tick{}}
	whole := true
	for _, v := range vals {
		if v != math.Trunc(v) {
			whole = false
		}
	}
	lo, hi, step := chartScale(vals, b.Naira, whole)
	share := func(v float64) float64 { return math.Round((v-lo)/(hi-lo)*10000) / 10000 }
	out.Zero = share(0)
	for v := lo; v <= hi+step/2; v += step {
		out.Ticks = append(out.Ticks, Tick{Value: v, Shown: compactShown(v, b.Naira), Share: share(v)})
	}
	every := 1
	if len(vals) > 8 {
		every = 2
	}
	for i, v := range vals {
		out.Points = append(out.Points, Point{Label: labels[i], Long: longs[i], Value: v, Shown: FigureShown(b, v), Share: share(v),
			Tick: (len(vals)-1-i)%every == 0})
	}
	out.Summary = chartSummary(out, b.Chart.Period != "" || b.Source.Tool != "")
	return out
}

// chartSummary says a chart in words: its highest, and (over time) its
// latest; "Nothing yet." when every value is nought.
func chartSummary(c ChartShown, overTime bool) string {
	if len(c.Points) == 0 {
		return "Nothing to show yet."
	}
	top := c.Points[0]
	allZero := true
	for _, p := range c.Points {
		if p.Value > top.Value {
			top = p
		}
		if p.Value != 0 {
			allZero = false
		}
	}
	if allZero {
		return "Nothing yet: every one is " + c.Points[0].Shown + "."
	}
	s := fmt.Sprintf("Highest %s (%s)", top.Shown, top.Long)
	if overTime {
		last := c.Points[len(c.Points)-1]
		s += fmt.Sprintf("; latest %s (%s)", last.Shown, last.Long)
	}
	return fmt.Sprintf("%s, %d in all.", s, len(c.Points))
}

// chartScale is a scale of round steps from nought (or the lowest value)
// to the highest: four or so steps, each 1, 2 or 5 times a power of ten
// (whole naira for money, whole numbers for counts).
func chartScale(vals []float64, naira, whole bool) (lo, hi, step float64) {
	for _, v := range vals {
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	if hi == lo {
		hi = lo + 1
	}
	minStep := 0.0
	switch {
	case naira:
		minStep = 100
	case whole:
		minStep = 1
	}
	step = math.Max(niceStep((hi-lo)/4), minStep)
	hi = math.Ceil(hi/step-1e-9) * step
	lo = math.Floor(lo/step+1e-9) * step
	if hi == lo {
		hi = lo + step
	}
	return lo, hi, step
}

func niceStep(x float64) float64 {
	if x <= 0 {
		return 1
	}
	exp := math.Floor(math.Log10(x))
	f := x / math.Pow(10, exp)
	var nf float64
	switch {
	case f <= 1:
		nf = 1
	case f <= 2:
		nf = 2
	case f <= 5:
		nf = 5
	default:
		nf = 10
	}
	return nf * math.Pow(10, exp)
}

// compactShown writes a scale's mark short: 0, 500, 2.5k, 1.2m; money in
// naira from kobo (₦25k).
func compactShown(v float64, naira bool) string {
	if naira {
		v /= 100
	}
	neg := v < 0
	a := math.Abs(v)
	var s string
	switch {
	case a >= 1e9:
		s = trimNum(a/1e9) + "bn"
	case a >= 1e6:
		s = trimNum(a/1e6) + "m"
	case a >= 1e3:
		s = trimNum(a/1e3) + "k"
	default:
		s = trimNum(a)
	}
	if naira {
		s = "₦" + s
	}
	if neg && a != 0 {
		s = "-" + s
	}
	return s
}

func trimNum(f float64) string {
	return strconv.FormatFloat(math.Round(f*100)/100, 'f', -1, 64)
}

// chartNumber reads a value as a number.
func chartNumber(x any) (float64, bool) {
	switch n := x.(type) {
	case float64:
		return n, !math.IsNaN(n) && !math.IsInf(n, 0)
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	}
	return 0, false
}

// chartTime reads a date or a date and time as the moment it is.
func chartTime(col datatables.Column, x any) (time.Time, bool) {
	s := strings.TrimSpace(cellText(x))
	if s == "" {
		return time.Time{}, false
	}
	if col.Type == "date" {
		if d, err := time.ParseInLocation("2006-01-02", s[:min(len(s), 10)], lagos); err == nil {
			return d, true
		}
		return time.Time{}, false
	}
	d, err := time.Parse(time.RFC3339Nano, s)
	return d, err == nil
}
