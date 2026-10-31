package api

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/tablequery"
	"github.com/israel-duff/taskiem/engine/billing"
	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/whatsapp"

	"github.com/google/uuid"
)

// Questions over a business's tables in chat (decision 0060, R6;
// docs/spec/no-sheet-needed.md §6.5). "how many customers" and "total
// balance" need no model: the words name the table or the column. Any
// other question ("how many customers in Ikeja?", "who owes more than
// 10k?", "ask …", or one the model routes as ask) goes to the model with
// the question and the tables' names and columns, never a row; its spec
// is checked against the table (datatables.CheckQuery) before anything is
// read, and code writes the answer, followed by its reading so a wrong
// one shows.

const (
	// askPerHour is how many questions a chat may put to the model in an
	// hour.
	askPerHour = 20
	// askSumPage is how many rows a sum reads at a time.
	askSumPage = 1000
)

// chatModel is the model chat questions go to: the one that routes
// messages in other languages, else the AI builder's.
func (s *Server) chatModel() ai.Provider {
	if s.Languages != nil && s.Languages.Model != nil {
		return s.Languages.Model
	}
	if s.AI != nil {
		return s.AI.Provider
	}
	return nil
}

// tableAtStart finds the table the first words name (its name or title,
// "customer" for customers too) and what follows them.
func tableAtStart(tables []datatables.Table, words []string) (datatables.Table, string, bool) {
	single := func(k string) string { return strings.TrimSuffix(k, "s") }
	for n := len(words); n > 0; n-- {
		key := matchKey(strings.Join(words[:n], " "))
		if key == "" {
			continue
		}
		var found []datatables.Table
		for _, t := range tables {
			for _, k := range []string{matchKey(t.Name), matchKey(t.Title)} {
				if k != "" && (k == key || single(k) == single(key)) {
					found = append(found, t)
					break
				}
			}
		}
		if len(found) == 1 {
			return found[0], strings.Join(words[n:], " "), true
		}
	}
	return datatables.Table{}, "", false
}

// sumColumn finds the number column words name ("balance", "amount
// owed"), in the table named after in/of when one is, else in the one
// table that has it.
func sumColumn(tables []datatables.Table, words []string) (datatables.Table, datatables.Column, bool) {
	colWords, tableWords := words, []string(nil)
	for i, w := range words {
		if (w == "in" || w == "of" || w == "for") && i > 0 && i < len(words)-1 {
			colWords, tableWords = words[:i], words[i+1:]
			break
		}
	}
	if len(tableWords) > 0 {
		t, rest, ok := tableAtStart(tables, tableWords)
		if !ok || rest != "" {
			return datatables.Table{}, datatables.Column{}, false
		}
		tables = []datatables.Table{t}
	}
	key := matchKey(strings.Join(colWords, " "))
	var t datatables.Table
	var col datatables.Column
	n := 0
	for _, tb := range tables {
		for _, cl := range tb.Columns {
			if (cl.Type == "number" || cl.Type == "integer") && (matchKey(cl.Name) == key || matchKey(cl.Title) == key) {
				t, col, n = tb, cl, n+1
			}
		}
	}
	return t, col, n == 1
}

// waAskCommand reads the questions that need no model, and "ask …"; false
// when the message is not one ("how many rice" is stock's).
func (s *Server) waAskCommand(ctx context.Context, c *chat, text, cmd string) (bool, error) {
	words := strings.Fields(cmd)
	if len(words) < 2 {
		return false, nil
	}
	if words[0] == "ask" {
		// Someone who cannot read the tables is not asking them.
		if !c.p.Can(PermTableRead) {
			return false, nil
		}
		return true, s.waAsk(ctx, c, strings.Join(strings.Fields(text)[1:], " "))
	}
	how := words[0] == "how" && len(words) > 2 && (words[1] == "many" || words[1] == "much")
	total := words[0] == "total" || words[0] == "sum"
	if (!how && !total) || !c.p.Can(PermTableRead) {
		return false, nil
	}
	tables, err := s.waListTables(ctx, c.tenant.ID)
	if err != nil || len(tables) == 0 {
		return false, err
	}
	if how {
		t, rest, ok := tableAtStart(tables, words[2:])
		if !ok {
			return false, nil
		}
		if rest == "" || rest == "do i have" || rest == "are there" || rest == "in all" {
			q, err := datatables.CheckQuery(datatables.QuerySpec{Table: t.Name, Aggregate: &datatables.QueryAggregate{Fn: "count"}}, tables)
			if err != nil {
				return true, err
			}
			return true, s.waAnswer(ctx, c, q, 0)
		}
		return true, s.waAsk(ctx, c, text)
	}
	rest := words[1:]
	if rest[0] == "of" || rest[0] == "the" {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		return false, nil
	}
	t, col, ok := sumColumn(tables, rest)
	if !ok {
		return false, nil
	}
	q, err := datatables.CheckQuery(datatables.QuerySpec{Table: t.Name, Aggregate: &datatables.QueryAggregate{Fn: "sum", Column: col.Name}}, tables)
	if err != nil {
		return true, err
	}
	return true, s.waAnswer(ctx, c, q, 0)
}

// looksLikeQuestion reads an English message nothing else recognised: it
// ends with "?" or starts with a question word.
func looksLikeQuestion(cmd, text string) bool {
	words := strings.Fields(cmd)
	if len(words) < 2 {
		return false
	}
	switch words[0] {
	case "how", "what", "who", "whose", "which", "list", "show", "count", "any", "do", "does", "are", "is":
		return true
	}
	return strings.HasSuffix(strings.TrimSpace(text), "?")
}

// waAskFallback puts an English message nothing else recognised to the
// model as a question over the tables when it looks like a question and
// there are tables; false when it is not one, or the model says the
// tables cannot answer it, so the chat's usual answer follows.
func (s *Server) waAskFallback(ctx context.Context, c *chat, text, cmd string) (bool, error) {
	if c.lang.Tag != lang.EN && c.lang.Tag != "" || !c.p.Can(PermTableRead) || s.chatModel() == nil || !looksLikeQuestion(cmd, text) {
		return false, nil
	}
	if !s.aiOnPlan(ctx, c) {
		return false, nil
	}
	tables, err := s.waListTables(ctx, c.tenant.ID)
	if err != nil || len(tables) == 0 {
		return false, err
	}
	return s.waAskFrom(ctx, c, text, true)
}

// aiOnPlan reports whether the business's plan has the ai feature.
func (s *Server) aiOnPlan(ctx context.Context, c *chat) bool {
	return s.Billing.Require(ctx, c.tenant.ID, billing.FeatureAI) == nil
}

// waAsk puts a question to the model within the plan's AI feature, the
// business's AI budget and askPerHour for the chat, checks the spec it
// gives and answers from the table.
func (s *Server) waAsk(ctx context.Context, c *chat, question string) error {
	_, err := s.waAskFrom(ctx, c, question, false)
	return err
}

// waAskFrom is waAsk; with fallback (a message nothing else recognised)
// a question the model says the tables cannot answer is not answered
// here (false).
func (s *Server) waAskFrom(ctx context.Context, c *chat, question string, fallback bool) (bool, error) {
	say := func(id string, kv ...string) (bool, error) { return true, c.say(ctx, s, c.t(id, kv...)) }
	if !c.p.Can(PermTableRead) {
		return say("wa.table.denied_read", "org", c.tenant.Name)
	}
	question = strings.TrimSpace(question)
	if question == "" {
		return say("wa.ask.what")
	}
	tables, err := s.waListTables(ctx, c.tenant.ID)
	if err != nil {
		return true, err
	}
	if len(tables) == 0 {
		return say("wa.table.none", "org", c.tenant.Name, "link", s.waLink(c, "/tables"))
	}
	model := s.chatModel()
	if model == nil || !s.aiOnPlan(ctx, c) {
		// Without a model only the word patterns answer: said once a day,
		// then the shorter answer.
		if s.limiter("wa-ask-nomodel:"+c.tenant.ID.String()+":"+c.addr, 24*time.Hour, 1).Allow() {
			return say("wa.ask.no_model")
		}
		return say("wa.ask.cant")
	}
	limit, used, err := s.aiBudget(ctx, c.tenant.ID)
	if err != nil {
		return true, err
	}
	if limit > 0 && used >= limit {
		return say("wa.ask.budget")
	}
	if !s.limiter("wa-ask:"+c.tenant.ID.String()+":"+c.addr, time.Hour/askPerHour, askPerHour).Allow() {
		return say("wa.ask.limit", "count", strconv.Itoa(askPerHour))
	}
	described := make([]tablequery.Table, 0, len(tables))
	for _, t := range tables {
		d := tablequery.Table{Name: t.Name, Title: t.Title}
		for _, col := range datatables.QueryColumns(t) {
			d.Columns = append(d.Columns, tablequery.Column{Name: col.Name, Title: col.Title, Type: col.Type})
		}
		described = append(described, d)
	}
	info, _ := lang.Lookup(c.lang.Tag)
	if info.Tag == "" {
		info, _ = lang.Lookup(lang.EN)
	}
	spec, resp, err := (&tablequery.Asker{Provider: model}).Ask(ctx, question, info, described)
	outcome, errText := "valid", ""
	switch {
	case errors.Is(err, tablequery.ErrUnanswerable):
		outcome = "unknown"
	case errors.Is(err, tablequery.ErrUnusable):
		outcome = "unparseable"
	case err != nil:
		outcome, errText = "error", err.Error()
	}
	var q datatables.CheckedQuery
	if err == nil {
		if q, err = datatables.CheckQuery(querySpecOf(spec), tables); err != nil {
			outcome, errText = "refused", err.Error()
		}
	}
	s.recordModelCall(ctx, c, "table_query", "tablequery", model, question, resp, outcome, errText)
	if fallback && outcome == "unknown" {
		// Not about the tables: the chat's usual answer follows.
		return false, nil
	}
	if err != nil {
		if outcome == "error" {
			s.Logger.Warn("whatsapp: a question over tables to the model", "err", err)
		}
		return say("wa.ask.cant")
	}
	return true, s.waAnswer(ctx, c, q, 0)
}

// querySpecOf is the model's spec as datatables checks it.
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

// specOf is a checked query as the chat keeps it, for *more*.
func specOf(q datatables.CheckedQuery) *datatables.QuerySpec {
	s := &datatables.QuerySpec{Table: q.Table.Name, Order: q.Order, Limit: q.Limit, Aggregate: q.Aggregate}
	for _, f := range q.Filters {
		s.Filters = append(s.Filters, datatables.QueryFilter{Column: f.Column.Name, Op: f.Op, Value: f.Value})
	}
	return s
}

// askReading is the question as it was read, in the person's language:
// "Customers where area is Ikeja", a personal value masked.
func askReading(c *chat, q datatables.CheckedQuery) string {
	title := whatsapp.SafeText(tableTitle(q.Table))
	var conds []string
	for _, f := range q.Filters {
		col := tableColumnLabel(f.Column)
		if f.Column.Name == "created_at" {
			col = c.t("wa.ask.added")
		}
		kv := []string{"column", whatsapp.SafeText(col)}
		if f.Value != nil {
			v := cell(f.Value)
			if f.Column.Personal {
				v = whatsapp.MaskValue(tableCategory(f.Column), v)
			}
			kv = append(kv, "value", whatsapp.SafeText(v))
		}
		conds = append(conds, c.t("wa.ask.op_"+f.Op, kv...))
	}
	out := title
	if len(conds) > 0 {
		out = c.t("wa.ask.where", "table", title, "conditions", strings.Join(conds, c.t("wa.ask.and")))
	}
	for _, o := range q.Order {
		label := o.Column
		for _, col := range datatables.QueryColumns(q.Table) {
			if col.Name == o.Column {
				label = tableColumnLabel(col)
			}
		}
		key := "wa.ask.order_asc"
		if o.Desc {
			key = "wa.ask.order_desc"
		}
		out += ", " + c.t(key, "column", whatsapp.SafeText(label))
	}
	return out
}

// askNumber writes a count or a sum: digits grouped in threes, two decimals
// only when there is a fraction.
func askNumber(f float64) string {
	neg := f < 0
	f = math.Abs(f)
	whole := math.Floor(f)
	frac := math.Round((f - whole) * 100)
	if frac == 100 {
		whole, frac = whole+1, 0
	}
	digits := strconv.FormatFloat(whole, 'f', 0, 64)
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	out := b.String()
	if frac > 0 {
		out += "." + strconv.FormatFloat(frac, 'f', 0, 64)
		if frac < 10 {
			out = strings.Replace(out, ".", ".0", 1)
		}
	}
	if neg {
		out = "-" + out
	}
	return out
}

func numberOf(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	}
	return 0, false
}

// waAnswer reads a checked question through the chat's connection reads
// and answers it: the count, the sum (over the first QuerySumRows rows at
// most), or the rows numbered like find's, personal values masked, kept
// for *more*, *change n* and *delete n*; each with its reading.
func (s *Server) waAnswer(ctx context.Context, c *chat, q datatables.CheckedQuery, before int64) error {
	tctx := s.waTablesCtx(ctx, c)
	tr, err := s.tableRowsFor(tctx, c.tenant.ID, q.Table, tablesChatPurpose)
	if err != nil {
		s.Logger.Warn("whatsapp: the data tables connection", "err", err)
		return c.say(ctx, s, c.t("wa.table.unavailable"))
	}
	reading := askReading(c, q)
	filters := q.ReadFilters()
	read := func(in map[string]any) (map[string]any, error) {
		in["table"] = q.Table.Name
		out, err := tr.call(tctx, s, "read_rows", in, "")
		if err != nil {
			s.Logger.Warn("whatsapp: reading rows for a question", "table", q.Table.Name, "err", err)
		}
		return out, err
	}
	if q.Aggregate != nil && q.Aggregate.Fn == "count" {
		out, err := read(map[string]any{"filters": filters, "limit": 1, "select": "id", "count": "exact"})
		if err != nil {
			return c.say(ctx, s, c.t("wa.table.unavailable"))
		}
		return c.say(ctx, s, c.t("wa.ask.count", "reading", reading, "count", askNumber(float64(countOf(out)))))
	}
	if col, ok := q.SumColumn(); ok {
		var sum float64
		cursor, seen, over := "", 0, false
		for {
			in := map[string]any{"filters": filters, "limit": askSumPage, "select": "id," + col.Name, "order": []any{map[string]any{"column": "id", "desc": true}}}
			if cursor != "" {
				in["cursor"] = cursor
			}
			out, err := read(in)
			if err != nil {
				return c.say(ctx, s, c.t("wa.table.unavailable"))
			}
			for _, r := range rowsOf(out) {
				m, _ := r.(map[string]any)
				if f, ok := numberOf(m[col.Name]); ok {
					sum += f
				}
				seen++
			}
			cursor, _ = out["next_cursor"].(string)
			if cursor == "" {
				break
			}
			if seen >= datatables.QuerySumRows {
				over = true
				break
			}
		}
		msg := c.t("wa.ask.sum", "column", whatsapp.SafeText(tableColumnLabel(col)), "reading", reading, "total", askNumber(sum))
		if over {
			msg += "\n" + c.t("wa.ask.sum_over", "count", askNumber(datatables.QuerySumRows))
		}
		return c.say(ctx, s, msg)
	}
	if before > 0 {
		filters = append(filters, map[string]any{"column": "id", "op": "lt", "value": before})
	}
	out, err := read(map[string]any{"filters": filters, "limit": q.Limit, "order": q.ReadOrder(), "count": "exact"})
	if err != nil {
		return c.say(ctx, s, c.t("wa.table.unavailable"))
	}
	rows := rowsOf(out)
	if len(rows) == 0 {
		if before > 0 {
			if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
				return err
			}
			return c.say(ctx, s, c.t("wa.rows.no_more", "table", whatsapp.SafeText(tableTitle(q.Table))))
		}
		return c.say(ctx, s, c.t("wa.ask.none", "reading", reading))
	}
	f := &chatFound{Table: q.Table.ID, Title: tableTitle(q.Table), Ask: specOf(q)}
	lines := []string{c.t("wa.ask.rows_head", "reading", reading, "count", askNumber(float64(countOf(out))))}
	last := int64(0)
	for i, r := range rows {
		m, _ := r.(map[string]any)
		id, _ := strconv.ParseInt(cell(m["id"]), 10, 64)
		f.Rows = append(f.Rows, id)
		if last == 0 || id < last {
			last = id
		}
		lines = append(lines, strconv.Itoa(i+1)+". "+waTableRowLine(q.Table.Columns, m))
	}
	f.Last = last
	// More rows are offered only in the order *more* can page: newest
	// first.
	if more := countOf(out) - int64(len(rows)); more > 0 && len(q.Order) == 0 {
		lines = append(lines, c.t("wa.table.find_more", "count", strconv.FormatInt(more, 10)))
	}
	if c.p.Can(PermTableEdit) && !q.Table.Kept() {
		lines = append(lines, "", c.t("wa.rows.find_tail"))
	}
	if err := s.saveSession(ctx, c, stateCollecting, chatData{Found: f}, waPendingTTL); err != nil {
		return err
	}
	return c.say(ctx, s, strings.Join(lines, "\n"))
}

// waAskMore pages a question's rows on (*more*).
func (s *Server) waAskMore(ctx context.Context, c *chat, f *chatFound) error {
	tables, err := s.waListTables(ctx, c.tenant.ID)
	if err != nil {
		return err
	}
	q, err := datatables.CheckQuery(*f.Ask, tables)
	if err != nil {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.ask.cant"))
	}
	if len(q.Order) > 0 {
		return c.say(ctx, s, c.t("wa.rows.no_more", "table", whatsapp.SafeText(tableTitle(q.Table))))
	}
	return s.waAnswer(ctx, c, q, f.Last)
}

// recordModelCall records a chat's model call like a build's
// (ai_interactions), so it counts against the business's AI budget.
func (s *Server) recordModelCall(ctx context.Context, c *chat, kind, digest string, model ai.Provider, text string, resp *ai.Response, outcome, errText string) {
	var u ai.Usage
	var respText *string
	var stop, name string
	if resp != nil {
		r := pii.Redact(resp.Text)
		respText, stop, name, u = &r, resp.StopReason, resp.Model, resp.Usage
	}
	if name == "" {
		name = model.Model()
	}
	ctx = context.WithoutCancel(ctx)
	msgs := []map[string]string{{"role": "user", "text": pii.Redact(text)}}
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO ai_interactions (id, tenant_id, build_id, round, kind, actor, provider, model, system_digest, messages,
			response, stop_reason, outcome, error, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, total_tokens)
			VALUES ($1, $2, NULL, 0, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, ''), $11, NULLIF($12, ''), $13, $14, $15, $16, $17)`,
			uuid.Must(uuid.NewV7()), c.tenant.ID, kind, c.p.Actor(), model.Name(), name, digest, msgs, respText, stop, outcome, pii.Redact(errText),
			u.InputTokens, u.OutputTokens, u.CacheCreationTokens, u.CacheReadTokens, u.Total())
		return err
	})
	if err != nil {
		s.Logger.Error("whatsapp: recording a model call", "kind", kind, "err", err)
	}
}
