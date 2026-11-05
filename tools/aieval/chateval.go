package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/intent"
	"github.com/israel-duff/taskiem/engine/ai/tablequery"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/lang"
)

// The chat suite (decision 0060, R6; evals/chat): messages people send in
// the five languages, routed by the model to the table and debtor
// commands (kind route), and questions over a business's tables turned
// into query specs (kind spec), graded by the spec expected, or by its
// refusal: the model saying the tables cannot answer, or the spec failing
// datatables.CheckQuery, so nothing would be read. The tables are
// fixtures (tables.json beside the suite); the model never sees a row.

// ChatCase is one line of the chat suite.
type ChatCase struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"` // route | spec
	Language lang.Tag `json:"language"`
	Message  string   `json:"message"`
	// Tables names the fixture's table set a spec case is asked over.
	Tables string `json:"tables,omitempty"`
	Expect struct {
		// Intent is the intent a route case must give; Arg words its
		// argument must contain (case-insensitive), when given.
		Intent string `json:"intent,omitempty"`
		Arg    string `json:"arg,omitempty"`
		// Spec is the spec a spec case must give (the limit is not
		// graded), or Refused that it must give none.
		Spec    *datatables.QuerySpec `json:"spec,omitempty"`
		Refused bool                  `json:"refused,omitempty"`
	} `json:"expect"`
	Tags   []string `json:"tags,omitempty"`
	Source string   `json:"source,omitempty"`
}

func loadChat(path string) ([]ChatCase, map[string][]datatables.Table, error) {
	dir := path
	if filepath.Ext(path) == ".jsonl" {
		dir = filepath.Dir(path)
	}
	fixtures := map[string][]datatables.Table{}
	raw, err := os.ReadFile(filepath.Join(dir, "tables.json")) //nolint:gosec // the fixture beside the suite
	if err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		return nil, nil, fmt.Errorf("tables.json: %w", err)
	}
	var out []ChatCase
	seen := map[string]bool{}
	err = readJSONL(path, func(_ string, _ int, raw []byte) error {
		var c ChatCase
		if err := decodeStrict(raw, &c); err != nil {
			return err
		}
		if c.ID == "" || seen[c.ID] || strings.TrimSpace(c.Message) == "" || len(c.Tags) == 0 {
			return fmt.Errorf("every case needs a unique id, a message and a tag")
		}
		seen[c.ID] = true
		if _, ok := lang.Lookup(c.Language); !ok {
			return fmt.Errorf("%s: unknown language %q", c.ID, c.Language)
		}
		switch c.Kind {
		case "route":
			if !slices.Contains(intent.Allowed, lang.Intent(c.Expect.Intent)) && c.Expect.Intent != intent.Unknown {
				return fmt.Errorf("%s: %q is not an intent the model may give", c.ID, c.Expect.Intent)
			}
		case "spec":
			tables, ok := fixtures[c.Tables]
			if !ok {
				return fmt.Errorf("%s: no table set %q in tables.json", c.ID, c.Tables)
			}
			if (c.Expect.Spec == nil) == !c.Expect.Refused {
				return fmt.Errorf("%s: expect a spec or refused, not both", c.ID)
			}
			if c.Expect.Spec != nil {
				if _, err := datatables.CheckQuery(*c.Expect.Spec, tables); err != nil {
					return fmt.Errorf("%s: the expected spec fails its own checks: %w", c.ID, err)
				}
			}
		default:
			return fmt.Errorf("%s: kind is route or spec", c.ID)
		}
		if c.Source == "" {
			c.Source = "synthetic"
		}
		out = append(out, c)
		return nil
	})
	return out, fixtures, err
}

// ChatResult is one chat case's outcome.
type ChatResult struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Language lang.Tag `json:"language"`
	Tags     []string `json:"tags,omitempty"`
	Status   string   `json:"status"`
	Error    string   `json:"error,omitempty"`
	Expected string   `json:"expected"`
	Got      string   `json:"got"`
	Correct  bool     `json:"correct"`
	// Refusal: a spec case that expects none.
	Refusal bool     `json:"refusal,omitempty"`
	Model   string   `json:"model,omitempty"`
	Usage   ai.Usage `json:"usage"`
	CostUSD *float64 `json:"cost_usd,omitempty"`
}

// ChatSummary aggregates a chat run.
type ChatSummary struct {
	Cases           int      `json:"cases"`
	Errors          int      `json:"errors"`
	RouteAccuracy   float64  `json:"route_accuracy"`
	SpecAccuracy    float64  `json:"spec_accuracy"`
	RefusalAccuracy float64  `json:"refusal_accuracy"`
	Usage           ai.Usage `json:"usage"`
	CostUSD         *float64 `json:"cost_usd"`
}

// ChatReport is a chat evaluation.
type ChatReport struct {
	Kind       string                 `json:"kind"` // chat
	Suite      string                 `json:"suite"`
	Provider   string                 `json:"provider"`
	Model      string                 `json:"model"`
	Summary    ChatSummary            `json:"summary"`
	ByLanguage map[string]ChatSummary `json:"by_language"`
	Results    []ChatResult           `json:"results"`
}

func runChatSuite(o options, prices map[string]Price, stdout io.Writer) error {
	cases, fixtures, err := loadChat(o.suite)
	if err != nil {
		return err
	}
	cases = filter(cases, func(c ChatCase) []string { return c.Tags }, o.tags, o.limit)
	if len(cases) == 0 {
		return errors.New("no cases")
	}
	prov, err := providerFor(o.provider, o.model, chatHeuristic, "heuristic")
	if err != nil {
		return err
	}
	results := make([]ChatResult, 0, len(cases))
	for _, c := range cases {
		results = append(results, runChat(context.Background(), prov, c, fixtures[c.Tables], prices))
	}
	rep := ChatReport{Kind: "chat", Suite: o.suite, Provider: prov.Name(), Model: prov.Model(), Summary: summariseChat(results), ByLanguage: map[string]ChatSummary{}, Results: results}
	by := map[string][]ChatResult{}
	for _, r := range results {
		by[string(r.Language)] = append(by[string(r.Language)], r)
	}
	for k, rs := range by {
		rep.ByLanguage[k] = summariseChat(rs)
	}
	s := rep.Summary
	fmt.Fprintf(stdout, "chat %s %s, %d cases: routing %s, specs %s, refusals %s; %d errors; %d tokens, cost %s\n", rep.Provider, rep.Model, s.Cases,
		pct(s.RouteAccuracy), pct(s.SpecAccuracy), pct(s.RefusalAccuracy), s.Errors, s.Usage.Total(), money(s.CostUSD))
	if o.verbose {
		for _, r := range results {
			fmt.Fprintf(stdout, "  %-40s %-5s %s want %s got %s\n", r.ID, r.Status, yes(r.Correct), r.Expected, r.Got)
		}
	}
	if err := writeOutputs(o, rep, chatMarkdown(rep), func() any { return rep }); err != nil {
		return err
	}
	if o.compare != "" {
		var base ChatReport
		if err := readReport(o.compare, &base); err != nil {
			return err
		}
		if base.Kind != "chat" {
			return fmt.Errorf("%s is not a chat baseline", o.compare)
		}
		regs := compareChat(base, rep, o.tolerance)
		printComparison(stdout, regs, nil, o.tolerance)
		if len(regs) > 0 {
			return errRegression
		}
	}
	return nil
}

func runChat(ctx context.Context, prov ai.Provider, c ChatCase, tables []datatables.Table, prices map[string]Price) ChatResult {
	r := ChatResult{ID: c.ID, Kind: c.Kind, Language: c.Language, Tags: c.Tags, Status: statusOK}
	info, _ := lang.Lookup(c.Language)
	var resp *ai.Response
	switch c.Kind {
	case "route":
		r.Expected = c.Expect.Intent + " " + c.Expect.Arg
		res, rsp, err := (&intent.Classifier{Provider: prov}).Classify(ctx, c.Message, info)
		resp = rsp
		if err != nil && !errors.Is(err, intent.ErrUnusable) {
			r.Status, r.Error = statusError, err.Error()
			return r
		}
		got := string(res.Intent)
		if got == "" {
			got = intent.Unknown
		}
		r.Got = got + " " + res.Arg
		r.Correct = got == c.Expect.Intent && strings.Contains(strings.ToLower(res.Arg), strings.ToLower(c.Expect.Arg))
	case "spec":
		r.Refusal = c.Expect.Refused
		described := make([]tablequery.Table, 0, len(tables))
		for _, t := range tables {
			d := tablequery.Table{Name: t.Name, Title: t.Title}
			for _, col := range datatables.QueryColumns(t) {
				d.Columns = append(d.Columns, tablequery.Column{Name: col.Name, Title: col.Title, Type: col.Type})
			}
			described = append(described, d)
		}
		spec, rsp, err := (&tablequery.Asker{Provider: prov}).Ask(ctx, c.Message, info, described)
		resp = rsp
		var q datatables.CheckedQuery
		switch {
		case errors.Is(err, tablequery.ErrUnanswerable), errors.Is(err, tablequery.ErrUnusable):
			r.Got = "refused"
		case err != nil:
			r.Status, r.Error = statusError, err.Error()
			return r
		default:
			if q, err = datatables.CheckQuery(querySpecOf(spec), tables); err != nil {
				r.Got = "refused: " + err.Error()
			} else {
				r.Got = specKey(specOfChecked(q))
			}
		}
		if c.Expect.Refused {
			r.Expected = "refused"
			r.Correct = strings.HasPrefix(r.Got, "refused")
		} else {
			want, _ := datatables.CheckQuery(*c.Expect.Spec, tables)
			r.Expected = specKey(specOfChecked(want))
			r.Correct = r.Got == r.Expected
		}
	}
	if resp != nil {
		r.Model, r.Usage = resp.Model, resp.Usage
		r.CostUSD = cost(prices, resp.Model, resp.Usage)
	}
	return r
}

// querySpecOf is the model's spec as datatables checks it (as the API's).
func querySpecOf(s tablequery.Spec) datatables.QuerySpec {
	q := datatables.QuerySpec{Table: s.Table, Limit: s.Limit}
	for _, f := range s.Filters {
		q.Filters = append(q.Filters, datatables.QueryFilter{Column: f.Column, Op: f.Op, Value: f.Value})
	}
	for _, o := range s.Order {
		q.Order = append(q.Order, datatables.QueryOrder{Column: o.Column, Desc: o.Desc})
	}
	if s.Aggregate != nil {
		q.Aggregate = &datatables.QueryAggregate{Fn: s.Aggregate.Fn, Column: s.Aggregate.Column}
	}
	return q
}

type gradedSpec struct {
	table, agg string
	filters    []string
	order      string
}

func specOfChecked(q datatables.CheckedQuery) gradedSpec {
	g := gradedSpec{table: q.Table.Name}
	if q.Aggregate != nil {
		g.agg = q.Aggregate.Fn + ":" + q.Aggregate.Column
	}
	for _, f := range q.Filters {
		v := ""
		if f.Value != nil {
			v = strings.ToLower(fmt.Sprint(f.Value))
			if n, err := strconv.ParseFloat(v, 64); err == nil {
				v = strconv.FormatFloat(n, 'f', -1, 64)
			}
		}
		g.filters = append(g.filters, f.Column.Name+" "+f.Op+" "+v)
	}
	slices.Sort(g.filters)
	if len(q.Order) > 0 && q.Aggregate == nil {
		g.order = q.Order[0].Column + map[bool]string{true: " desc", false: " asc"}[q.Order[0].Desc]
	}
	return g
}

// specKey is a spec as graded: its table, aggregate, conditions (in any
// order, words without regard to case) and first order; not its limit.
func specKey(g gradedSpec) string {
	return g.table + " | " + g.agg + " | " + strings.Join(g.filters, " & ") + " | " + g.order
}

func summariseChat(results []ChatResult) ChatSummary {
	s := ChatSummary{Cases: len(results)}
	var route, routeOK, spec, specOK, ref, refOK int
	total, known := 0.0, true
	for _, r := range results {
		s.Usage = s.Usage.Add(r.Usage)
		if r.CostUSD != nil {
			total += *r.CostUSD
		} else if r.Usage.Total() > 0 {
			known = false
		}
		if r.Status != statusOK {
			s.Errors++
			continue
		}
		switch {
		case r.Kind == "route":
			route++
			if r.Correct {
				routeOK++
			}
		case r.Refusal:
			ref++
			if r.Correct {
				refOK++
			}
		default:
			spec++
			if r.Correct {
				specOK++
			}
		}
	}
	s.RouteAccuracy, s.SpecAccuracy, s.RefusalAccuracy = rate(routeOK, route), rate(specOK, spec), rate(refOK, ref)
	if known && s.Usage.Total() > 0 {
		c := math.Round(total*1e4) / 1e4
		s.CostUSD = &c
	}
	return s
}

// compareChat: no rate may drop by more than tol over the cases both runs
// have; errors may not grow.
func compareChat(base, cur ChatReport, tol float64) []Regression {
	in := map[string]bool{}
	for _, r := range base.Results {
		in[r.ID] = true
	}
	common := map[string]bool{}
	var curRes, baseRes []ChatResult
	for _, r := range cur.Results {
		if in[r.ID] {
			curRes = append(curRes, r)
			common[r.ID] = true
		}
	}
	for _, r := range base.Results {
		if common[r.ID] {
			baseRes = append(baseRes, r)
		}
	}
	b, c := summariseChat(baseRes), summariseChat(curRes)
	var regs []Regression
	check := func(name string, bv, cv float64) {
		if cv < bv-tol-1e-9 {
			regs = append(regs, Regression{name, bv, cv})
		}
	}
	check("route_accuracy", b.RouteAccuracy, c.RouteAccuracy)
	check("spec_accuracy", b.SpecAccuracy, c.SpecAccuracy)
	check("refusal_accuracy", b.RefusalAccuracy, c.RefusalAccuracy)
	if c.Errors > b.Errors {
		regs = append(regs, Regression{"errors", float64(b.Errors), float64(c.Errors)})
	}
	return regs
}

func chatMarkdown(rep ChatReport) string {
	var b strings.Builder
	s := rep.Summary
	fmt.Fprintf(&b, "# Chat evaluation\n\n`%s` on `%s`, %d cases, suite `%s`.\n\n| Measure | Value |\n| --- | --- |\n", rep.Model, rep.Provider, s.Cases, rep.Suite)
	fmt.Fprintf(&b, "| Routing | **%s** |\n| Specs | **%s** |\n| Refusals | **%s** |\n| Errors | %d |\n| Tokens | %d |\n| Cost | %s |\n",
		pct(s.RouteAccuracy), pct(s.SpecAccuracy), pct(s.RefusalAccuracy), s.Errors, s.Usage.Total(), money(s.CostUSD))
	b.WriteString("\n## By language\n\n| Language | Cases | Routing | Specs | Refusals |\n| --- | --- | --- | --- | --- |\n")
	for _, k := range sortedKeys(rep.ByLanguage) {
		g := rep.ByLanguage[k]
		fmt.Fprintf(&b, "| %s | %d | %s | %s | %s |\n", k, g.Cases, pct(g.RouteAccuracy), pct(g.SpecAccuracy), pct(g.RefusalAccuracy))
	}
	b.WriteString("\n## Cases\n\n| Case | Kind | Expected | Got | Correct |\n| --- | --- | --- | --- | --- |\n")
	for _, r := range rep.Results {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", r.ID, r.Kind, mdCell(r.Expected), mdCell(r.Got), yes(r.Correct))
	}
	return b.String()
}

func mdCell(s string) string { return strings.ReplaceAll(s, "|", "/") }

// --- the offline heuristic ---

// chatHeuristic stands in for the model offline: a keyword router and a
// spec reader over folded words. It is not a model; it exercises the
// suite, the prompts' plumbing and the grader, so a change that breaks
// them fails the comparison with the committed baseline.
func chatHeuristic(req ai.Request) (*ai.Response, error) {
	prompt := ""
	for _, m := range req.Messages {
		prompt += m.Text
	}
	answer := func(v any) (*ai.Response, error) {
		raw, _ := json.Marshal(v)
		return &ai.Response{Text: string(raw), JSON: raw, StopReason: "end_turn", Model: "heuristic"}, nil
	}
	if q, ok := between(prompt, "<question>\n", "\n</question>"); ok {
		var tables []tablequery.Table
		if t, ok := between(prompt, "The tables:\n", "\n\n"); ok {
			_ = json.Unmarshal([]byte(t), &tables)
		}
		return answer(heuristicSpec(q, tables))
	}
	msg, _ := between(prompt, "<message>\n", "\n</message>")
	i, arg := heuristicRoute(msg)
	return answer(map[string]string{"intent": i, "argument": arg})
}

func between(s, a, b string) (string, bool) {
	_, rest, ok := strings.Cut(s, a)
	if !ok {
		return "", false
	}
	out, _, _ := strings.Cut(rest, b)
	return out, true
}

var (
	digitsRe = regexp.MustCompile(`\d[\d,.]*k?`)
	capRe    = regexp.MustCompile(`\b[A-Z][a-zà-ÿ]+\b`)
)

func hasAny(s string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(" "+s+" ", " "+w+" ") || strings.Contains(s, w) && strings.Contains(w, " ") {
			return true
		}
	}
	return false
}

// heuristicRoute routes by the words of the five languages.
func heuristicRoute(msg string) (string, string) {
	f := lang.Fold(msg)
	num := digitsRe.FindString(msg)
	name := ""
	if m := capRe.FindString(msg); m != "" {
		name = m
	}
	switch {
	case num != "" && len(f) < 30 && hasAny(f, "change", "correct", "satunse", "yi pada", "gyara", "canza", "gbanwee", "dozie"):
		return "change_row", num
	case num != "" && len(f) < 30 && hasAny(f, "comot", "delete", "remove", "pa re", "yo kuro", "goge", "cire", "hichapu", "wepu"):
		return "delete_row", num
	case hasAny(f, "who owe", "ta lo je", "wa ke bina", "onye ji", "who dey owe"):
		return "owes", ""
	case name != "" && num != "" && hasAny(f, "paid", "don pay", "ti san", "ya biya", "kwuru"):
		return "paid", name + " paid " + num
	case name != "" && num != "" && hasAny(f, "owe", "je mi", "bina", "ji m"):
		return "owes", name + " owes " + num
	case hasAny(f, "how many", "how much", "meloo", "melo", "nawa", "ole", "total", "which", "who get"):
		return "ask", msg
	case hasAny(f, "new table", "new list", "tabili tuntun", "sabon tebur", "tebul ohuru", "ndeputa ohuru", "akojo tuntun", "sabon jeri"):
		return "new_table", ""
	case hasAny(f, "find", "search", "wa", "nemo", "nemi", "choo"):
		_, rest, _ := strings.Cut(f, " ")
		return "find", rest
	case hasAny(f, "add", "fi kun", "kara", "tinye", "put"):
		return "add", ""
	case hasAny(f, "tables", "table dem", "tabili", "teburori", "tebul"):
		return "tables", ""
	}
	return intent.Unknown, ""
}

// heuristicSpec reads a question: the table it names, count or sum, a
// place after in/for/dey/ni/a/na for a text column like area, and an
// amount after more than (or its words) for a number column.
func heuristicSpec(q string, tables []tablequery.Table) map[string]any {
	none := map[string]any{"answerable": false, "table": "", "filters": []any{}, "order": []any{}, "limit": 0, "aggregate": map[string]any{"fn": "none", "column": ""}}
	f := lang.Fold(q)
	var table *tablequery.Table
	for i, t := range tables {
		for _, k := range []string{lang.Fold(t.Name), lang.Fold(t.Title)} {
			if k != "" && strings.Contains(f, strings.TrimSuffix(k, "s")) {
				table = &tables[i]
			}
		}
	}
	if table == nil {
		return none
	}
	col := func(types []string, names ...string) string {
		for _, c := range table.Columns {
			if slices.Contains(types, c.Type) && (len(names) == 0 || slices.Contains(names, c.Name) || strings.Contains(f, lang.Fold(c.Name))) {
				return c.Name
			}
		}
		return ""
	}
	agg := map[string]any{"fn": "none", "column": ""}
	filters := []any{}
	order := []any{}
	switch {
	case hasAny(f, "how many", "meloo", "melo", "nawa", "ole", "count"):
		agg["fn"] = "count"
	case hasAny(f, "total", "how much", "lapapo", "jimilla", "ngukota"):
		c := col([]string{"number", "integer"}, "")
		if c == "" {
			return none
		}
		agg["fn"], agg["column"] = "sum", c
	}
	if m := regexp.MustCompile(`(?:more than|pass|ju|fiye da|karia|over)\s+(\d[\d,.]*k?)`).FindStringSubmatch(f); m != nil {
		c := col([]string{"number", "integer"}, "balance", "amount", "owed", "salary")
		v := strings.ReplaceAll(m[1], ",", "")
		if strings.HasSuffix(v, "k") {
			n, _ := strconv.ParseFloat(strings.TrimSuffix(v, "k"), 64)
			v = strconv.FormatFloat(n*1000, 'f', -1, 64)
		}
		if c != "" {
			filters = append(filters, map[string]any{"column": c, "op": "gt", "value": v})
			if agg["fn"] == "none" {
				order = append(order, map[string]any{"column": c, "desc": true})
			}
		}
	}
	places := capRe.FindAllString(q, -1)
	if len(places) > 0 {
		place := places[len(places)-1]
		if lang.Fold(place) != strings.Fields(f)[0] {
			if c := col([]string{"text"}, "area", "city", "town", "location", "state"); c != "" {
				filters = append(filters, map[string]any{"column": c, "op": "eq", "value": place})
			}
		}
	}
	if hasAny(f, "not active", "inactive", "no dey work") {
		if c := col([]string{"boolean"}, "active"); c != "" {
			filters = append(filters, map[string]any{"column": c, "op": "eq", "value": "false"})
		}
	}
	return map[string]any{"answerable": true, "table": table.Name, "filters": filters, "order": order, "limit": 0, "aggregate": agg}
}
