package pages

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdcheck"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Made tools as a page's source (pages §5, G2): `"source": {"tool":
// "leave-left", "inputs": {...}, "output": "left"}`. Only a tool whose care
// is "answers at once" may feed a part, so opening a page never changes
// anything; the API starts it as the viewer, keeps what it answered for a
// short while (page_source_cache) and draws the output named: a figure
// shows one output, a list lists one output of type list, each row read
// over item. What a tool answered is masked as its answers are.

// ToolInfo is a made tool as a page may name it.
type ToolInfo struct {
	Name string
	// Care is what the person meets: answers (at once), yes or step_up.
	Care string
	// Outputs are its outputs' types by name.
	Outputs map[string]string
	// Answer is its answer, for Pin as a page.
	Answer *wd.Answer
	// Inputs are its inputs' names, for checking a page's values.
	Inputs []string
	// PersonalInputs are its inputs that take personal data (declared
	// x-pii, a phone or an email, a saved person, account or place):
	// never kept on a page as a fixed value.
	PersonalInputs []string
	// Roles are who may use it (none: everyone in the business).
	Roles []string
}

// Literal is a fixed value as a page keeps it: words that read like an
// expression ("=…") are written as one that gives back those very words
// (a CEL string literal), at any depth, so nothing in a value is evaluated.
func Literal(v any) any {
	switch x := v.(type) {
	case string:
		if strings.HasPrefix(x, "=") {
			return "=" + strconv.Quote(x)
		}
		return x
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = Literal(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = Literal(e)
		}
		return out
	}
	return v
}

// narrower says whether a tool's roles let in fewer people than a page's:
// a page that opens to more would show them what the tool answers only
// to its own roles (owners always may both).
func narrower(tool, page []string) bool {
	if len(tool) == 0 {
		return false
	}
	if len(page) == 0 {
		return true
	}
	for _, r := range page {
		if r != "owner" && !contains(tool, r) {
			return true
		}
	}
	return false
}

// checkToolSource holds a tool source to the business: the tool is one,
// it answers at once, and gives the output named, of a type the part can
// show.
func checkToolSource(ps *problems, at, name string, b Block, env Env, pageRoles []string) {
	s := b.Source
	t, ok := toolOf(env, s.Tool)
	if !ok {
		ps.add(at+"/source/tool", "%s shows what the tool %s works out, which the business does not have.", name, s.Tool)
		return
	}
	if narrower(t.Roles, pageRoles) {
		ps.add(at+"/source/tool", "%s is worked out by %s, which only %s may use, and the page opens to more people: let the page open to %s only.", name, t.Name, strings.Join(t.Roles, ", "), strings.Join(t.Roles, ", "))
	}
	if t.Care != wdcheck.CareAnswers {
		ps.add(at+"/source/tool", "%s is worked out by %s, which asks before it runs: only a tool that answers at once can feed a page, so opening it never changes anything.", name, t.Name)
	}
	typ, ok := t.Outputs[s.Output]
	if !ok {
		ps.add(at+"/source/output", "%s shows %s's answer %s, which it does not give.", name, t.Name, s.Output)
		return
	}
	switch b.Type {
	case "figure":
		if typ == "list" || typ == "object" {
			ps.add(at+"/source/output", "%s shows one figure, and %s's %s is a list.", name, t.Name, s.Output)
		}
		if b.Naira && typ != "integer" && typ != "number" {
			ps.add(at+"/naira", "%s shows %s as money, but it is not a number.", name, s.Output)
		}
	case "list":
		if typ != "list" {
			ps.add(at+"/source/output", "%s lists rows, and %s's %s is not a list.", name, t.Name, s.Output)
		}
	}
	if len(t.Inputs) > 0 || len(s.Inputs) > 0 {
		for k := range s.Inputs {
			if !contains(t.Inputs, k) {
				ps.add(at+"/source/inputs/"+k, "%s gives %s a value it does not ask for: %s.", name, t.Name, k)
			}
		}
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// ToolInputs works out a tool source's values for the viewer's filters:
// words as they are, an expression over filters; one that cannot be
// worked out is left out.
func ToolInputs(s *Source, v Values) map[string]any {
	out := map[string]any{}
	act := FiltersAct(v)
	for k, x := range s.Inputs {
		got, err := filtersEngine.Resolve(x, act, false)
		if err != nil || got == nil {
			continue
		}
		out[k] = got
	}
	return out
}

// ToolFigure writes a tool's output as a figure: a number as a count is
// written (or as naira from kobo), anything else as its words, masked as
// an answer's words are.
func ToolFigure(b Block, x any) (shown string, value *float64) {
	switch n := x.(type) {
	case float64:
		return FigureShown(b, n), &n
	case int64:
		f := float64(n)
		return FigureShown(b, f), &f
	case int:
		f := float64(n)
		return FigureShown(b, f), &f
	case string:
		if b.Naira {
			if f, err := strconv.ParseFloat(strings.TrimSpace(n), 64); err == nil && math.Abs(f) < 1e15 {
				return FigureShown(b, f), &f
			}
		}
		return clip(whatsapp.SafeText(n)), nil
	case nil:
		return "—", nil
	case bool:
		if n {
			return "Yes", nil
		}
		return "No", nil
	}
	return clip(whatsapp.SafeText(cellText(x))), nil
}

func clip(s string) string {
	if r := []rune(s); len(r) > maxShown {
		return string(r[:maxShown-1]) + "…"
	}
	return s
}

// ShapeTool turns a tool's list into the rows drawn: each item's title,
// line, state and card read over item and outputs, every word masked as a
// tool's answer's are (whatsapp.SafeText). A row's id is its place in the
// list. At most RowsShown rows.
func ShapeTool(b Block, outputs map[string]any, v Values) (rows []Shown, count int) {
	items, _ := outputs[b.Source.Output].([]any)
	count = len(items)
	rows = []Shown{}
	for i, it := range items {
		if i >= RowsShown {
			break
		}
		act := ToolAct(outputs, it, v)
		word := func(s string) string { return clip(whatsapp.SafeText(sayIn(toolEngine, s, act))) }
		s := Shown{ID: strconv.Itoa(i)}
		if b.Item != nil {
			s.Title, s.Sub = word(b.Item.Title), word(b.Item.Sub)
			if st := b.Item.State; st != nil {
				if text := word(st.Text); text != "" {
					s.State = &ShownState{Text: text, Tone: toneOf(st, text)}
				}
			}
		}
		if d := b.Detail; d != nil {
			sd := &ShownDetail{Title: word(d.Title), Lines: []ShownLine{}}
			for _, l := range d.Lines {
				val := word(l.Value)
				if l.Naira {
					val = nairaOf(val)
				}
				if val == "" {
					val = "-"
				}
				sd.Lines = append(sd.Lines, ShownLine{Label: l.Label, Value: val})
			}
			s.Detail = sd
		}
		s.Actions = ShownActions(b, act)
		rows = append(rows, s)
	}
	return rows, count
}

// ToolItem is a tool list's row by its place, for an action pressed on it.
func ToolItem(b Block, outputs map[string]any, id string) (any, bool) {
	items, _ := outputs[b.Source.Output].([]any)
	i, err := strconv.Atoi(id)
	if err != nil || i < 0 || i >= len(items) || i >= RowsShown {
		return nil, false
	}
	return items[i], true
}

func toneOf(st *State, text string) string {
	tone := st.Tone
	if tt, ok := st.Tones[text]; ok {
		tone = tt
	} else if tt, ok := st.Tones[strings.ToLower(text)]; ok {
		tone = tt
	}
	if tone == "" {
		tone = "plain"
	}
	return tone
}

// sayIn is say with the names e reads.
func sayIn(e interface {
	Eval(string, map[string]any) (any, error)
}, v string, act map[string]any) string {
	src, isExpr := strings.CutPrefix(v, "=")
	if !isExpr {
		return v
	}
	out, err := e.Eval(src, act)
	if err != nil || out == nil {
		return ""
	}
	return cellText(out)
}

// FromTool is the page Pin as a page makes from a tool's answer (§10, 2):
// a list answer becomes a list part fed by the tool (its rows, their words
// and buttons as the answer has them); a card answer becomes a figure for
// each of its first four lines that shows one of the tool's outputs. The
// tool's values are those the answer was given for. It is a draft until
// published. An answer of words alone, or a tool that asks before it runs,
// cannot be pinned (an error in plain words).
func FromTool(command string, t ToolInfo, inputs map[string]any) (*Page, error) {
	if t.Care != wdcheck.CareAnswers {
		return nil, fmt.Errorf("%s asks before it runs: only a tool that answers at once can feed a page", t.Name)
	}
	a := t.Answer
	if a == nil || (a.Type != "list" && a.Type != "card") {
		return nil, fmt.Errorf("only a list or a card from %s can be pinned as a page", t.Name)
	}
	name := pageNameFrom(command)
	title := t.Name
	if r := []rune(title); len(r) > 60 {
		title = string(r[:60])
	}
	// The page opens to those the tool is for (none named: everyone).
	p := &Page{Page: Format, Name: name, Title: title, Icon: "reports", Roles: append([]string(nil), t.Roles...)}
	src := func(output string) *Source {
		s := &Source{Tool: command, Output: output}
		for k, v := range inputs {
			// A personal value (a phone, an account) is never kept on a
			// page for everyone who opens it: the part asks for none.
			if contains(t.PersonalInputs, k) {
				continue
			}
			if s.Inputs == nil {
				s.Inputs = map[string]any{}
			}
			s.Inputs[k] = Literal(v)
		}
		return s
	}
	switch a.Type {
	case "list":
		if a.Item == nil || a.From == "" {
			return nil, fmt.Errorf("the list from %s says no rows to show", t.Name)
		}
		item := &Item{Title: a.Item.Title, Sub: a.Item.Sub}
		if a.Item.State != nil {
			item.State = &State{Text: a.Item.State.Text, Tone: a.Item.State.Tone}
		}
		b := Block{ID: "list", Type: "list", Source: src(a.From), Item: item, Empty: a.Empty}
		if t := strings.TrimSpace(a.Title); t != "" && !strings.HasPrefix(t, "=") && len([]rune(t)) <= 60 {
			b.Title = t
		}
		for i, btn := range a.Actions {
			if i == MaxRowActions {
				break
			}
			b.Actions = append(b.Actions, Action{Label: btn.Label, Tool: btn.Tool, Inputs: btn.Inputs, Once: btn.Once})
		}
		p.Blocks = []Block{b}
	case "card":
		for _, l := range a.Lines {
			out, ok := strings.CutPrefix(strings.TrimSpace(l.Value), "=outputs.")
			if !ok || !outputNameRe.MatchString(out) || len(p.Blocks) == MaxFiguresInRow {
				continue
			}
			typ := t.Outputs[out]
			if typ == "" || typ == "list" || typ == "object" {
				continue
			}
			label := l.Label
			if r := []rune(label); len(r) > 40 {
				label = string(r[:40])
			}
			p.Blocks = append(p.Blocks, Block{ID: freeID(p.Blocks, out), Type: "figure", Label: label, Value: "output", Source: src(out)})
		}
		if len(p.Blocks) == 0 {
			return nil, fmt.Errorf("none of the card's lines from %s shows one of its answers as it is", t.Name)
		}
	}
	return p, nil
}

var outputNameRe = idRe

func freeID(bs []Block, base string) string {
	id := base
	if len(id) > 28 {
		id = id[:28]
	}
	for i := 2; ; i++ {
		taken := false
		for _, b := range bs {
			if b.ID == id {
				taken = true
			}
		}
		if !taken {
			return id
		}
		id = fmt.Sprintf("%s_%d", base[:min(len(base), 28)], i)
	}
}

// pageNameFrom is a page's short name from a tool's command: never the
// command itself, which /command already means.
func pageNameFrom(command string) string {
	n := strings.Trim(command, "-")
	if len(n) > 19 {
		n = strings.TrimRight(n[:19], "-")
	}
	n += "-page"
	if !nameRe.MatchString(n) {
		n = "page"
	}
	return n
}

// StatusWords says a run's state in plain words (§4): queued, working,
// waiting, done, failed, stopped.
func StatusWords(status string) (words, tone string) {
	switch status {
	case "queued", "pending":
		return "Queued", "plain"
	case "running":
		return "Working", "plain"
	case "waiting":
		return "Waiting", "warn"
	case "completed":
		return "Done", "good"
	case "failed":
		return "Failed", "bad"
	case "cancelled":
		return "Stopped", "plain"
	}
	return "Not started", "plain"
}
