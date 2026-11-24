package pages

import (
	"regexp"
	"strings"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/expr"
)

// Actions (pages §7, G2): a button on a page's own buttons, or on each
// row of a list or a table (and its card). Each is one of three things:
//
//   - a made tool (tool, inputs, once): pressing it opens the tool's form
//     filled in, exactly as a button on a tool's answer does (made tools
//     §10): as whoever pressed it, held to the tool's roles and care; a
//     value shown masked is asked, never filled in;
//   - a signal (signal: workflow, name, run or to, input): sent to a run
//     of the workflow named, waiting at a signal step the workflow
//     declares a page may send ("pages": true), after a Yes;
//   - an approval (approve: workflow, run, step): the approval sheet, with
//     its PIN and rules, unchanged.
//
// when hides an action for a row while it is false. The API presses an
// action (api/pages_actions.go): it reads the row again, as the part
// shows it to that viewer, and works the values out from it there, so
// nothing the viewer sends is taken for a row's value.

// Limits (§4): three buttons for the page, two on a row.
const (
	MaxPageActions = 3
	MaxRowActions  = 2
	MaxActionLabel = 30
	maxInputs      = 20
)

// Action is one button.
type Action struct {
	Label   string         `json:"label"`
	Tool    string         `json:"tool,omitempty"`
	Inputs  map[string]any `json:"inputs,omitempty"`
	Once    bool           `json:"once,omitempty"`
	Signal  *SignalAction  `json:"signal,omitempty"`
	Approve *ApproveAction `json:"approve,omitempty"`
	When    string         `json:"when,omitempty"`
}

// SignalAction sends a signal to a run of a workflow: the run by its id
// (run), or the run waiting for this signal under a value (to, its
// correlation: a delivery has no run of its own, and its row names the
// delivery). Only to a run of workflow, waiting at a step that declares a
// page may send it.
type SignalAction struct {
	Workflow string         `json:"workflow"`
	Name     string         `json:"name"`
	Run      string         `json:"run,omitempty"`
	To       string         `json:"to,omitempty"`
	Input    map[string]any `json:"input,omitempty"`
}

// ApproveAction opens the approval a run of workflow waits for at step.
type ApproveAction struct {
	Workflow string `json:"workflow"`
	Run      string `json:"run"`
	Step     string `json:"step"`
}

// Action kinds.
const (
	ActTool    = "tool"
	ActSignal  = "signal"
	ActApprove = "approve"
)

// Kind is which of the three an action is ("" when none or several).
func (a Action) Kind() string {
	var kinds []string
	if a.Tool != "" {
		kinds = append(kinds, ActTool)
	}
	if a.Signal != nil {
		kinds = append(kinds, ActSignal)
	}
	if a.Approve != nil {
		kinds = append(kinds, ActApprove)
	}
	if len(kinds) != 1 {
		return ""
	}
	return kinds[0]
}

var (
	commandRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	eventRe   = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
	stepRe    = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`)
)

func (a Action) check(at string, e *expr.Engine, ps *problems) {
	if l := strings.TrimSpace(a.Label); l == "" || len([]rune(a.Label)) > MaxActionLabel {
		ps.add(at+"/label", "A button needs a label of up to %d letters, like New ticket.", MaxActionLabel)
	}
	values := func(path string, m map[string]any) {
		if len(m) > maxInputs {
			ps.add(path, "A button gives at most %d values.", maxInputs)
		}
		for k, v := range m {
			if s, ok := v.(string); ok && strings.HasPrefix(s, "=") {
				checkValueIn(e, path+"/"+k, s, ps)
			}
		}
	}
	switch a.Kind() {
	case ActTool:
		if !commandRe.MatchString(a.Tool) {
			ps.add(at+"/tool", "A button opens one of the business's tools by its short word.")
		}
		values(at+"/inputs", a.Inputs)
	case ActSignal:
		s := a.Signal
		if strings.TrimSpace(s.Workflow) == "" {
			ps.add(at+"/signal/workflow", "%q tells a run of a workflow: say which workflow.", a.Label)
		}
		if !eventRe.MatchString(s.Name) {
			ps.add(at+"/signal/name", "%q tells a run something by a short name, like confirm.", a.Label)
		}
		if (s.Run == "") == (s.To == "") {
			ps.add(at+"/signal", "%q tells one run: by its number (run) or by what it waits for (to), one of them.", a.Label)
		}
		for _, v := range []struct{ path, v string }{{"/signal/run", s.Run}, {"/signal/to", s.To}} {
			if v.v != "" {
				checkValueIn(e, at+v.path, v.v, ps)
			}
		}
		values(at+"/signal/input", s.Input)
		if a.Once || a.Inputs != nil {
			ps.add(at, "%q tells a run: it opens no tool's form.", a.Label)
		}
	case ActApprove:
		ap := a.Approve
		if strings.TrimSpace(ap.Workflow) == "" || strings.TrimSpace(ap.Run) == "" {
			ps.add(at+"/approve", "%q opens a run's approval: say which workflow and which run.", a.Label)
		} else {
			checkValueIn(e, at+"/approve/run", ap.Run, ps)
		}
		if !stepRe.MatchString(ap.Step) {
			ps.add(at+"/approve/step", "%q opens the approval at one step of the run: say which.", a.Label)
		}
		if a.Once || a.Inputs != nil {
			ps.add(at, "%q opens an approval: it opens no tool's form.", a.Label)
		}
	default:
		ps.add(at, "A button opens a tool, tells a run something, or opens an approval: one of them.")
	}
	if a.When != "" {
		if !strings.HasPrefix(a.When, "=") {
			ps.add(at+"/when", "When a button shows is a condition, like =row.status == 'open'.")
		} else {
			checkValueIn(e, at+"/when", a.When, ps)
		}
	}
}

// engineFor is what a part's words and buttons read: a table's row, a
// tool's item, or (the page's buttons) the filters alone.
func engineFor(b Block) *expr.Engine {
	switch {
	case b.Type == "buttons":
		return filtersEngine
	case b.Type == "board" && b.Source != nil && b.Source.Tool != "":
		// A board's move reads the column a card goes to (to) besides.
		return toolMoveEngine
	case b.Type == "board":
		return moveEngine
	case b.Source != nil && b.Source.Tool != "":
		return toolEngine
	}
	return engine
}

// ShownAction is a button as drawn for one row (or the page): its place
// among the part's actions, its words and kind, the tool it opens, and
// why it is greyed (set by the API: the tool is not the viewer's).
type ShownAction struct {
	Index    int    `json:"index"`
	Label    string `json:"label"`
	Kind     string `json:"kind"`
	Tool     string `json:"tool,omitempty"`
	Disabled string `json:"disabled,omitempty"`
}

// Shows says whether an action shows for act: no when, or a when that is
// true (one that cannot be worked out hides it).
func (a Action) Shows(e *expr.Engine, act map[string]any) bool {
	if a.When == "" {
		return true
	}
	ok, err := e.EvalBool(a.When, act)
	return err == nil && ok
}

// ActionShows says whether b's action i shows for act (its when).
func (b Block) ActionShows(i int, act map[string]any) bool {
	return i >= 0 && i < len(b.Actions) && b.Actions[i].Shows(engineFor(b), act)
}

// ShownActions are the actions that show for act, as drawn.
func ShownActions(b Block, act map[string]any) []ShownAction {
	e := engineFor(b)
	var out []ShownAction
	for i, a := range b.Actions {
		if !a.Shows(e, act) {
			continue
		}
		out = append(out, ShownAction{Index: i, Label: a.Label, Kind: a.Kind(), Tool: a.Tool})
	}
	return out
}

// ActionValues works out an action's values for act: words as they are, an
// expression's result; one that cannot be worked out is left out (asked).
func ActionValues(b Block, in map[string]any, act map[string]any) map[string]any {
	e := engineFor(b)
	out := map[string]any{}
	for k, v := range in {
		got, err := e.Resolve(v, act, false)
		if err != nil || got == nil {
			continue
		}
		out[k] = got
	}
	return out
}

// Word works out one value (a run's number, a step) for act: "" when it
// cannot.
func Word(b Block, v string, act map[string]any) string {
	src, isExpr := strings.CutPrefix(v, "=")
	if !isExpr {
		return v
	}
	got, err := engineFor(b).Eval(src, act)
	if err != nil || got == nil {
		return ""
	}
	return cellText(got)
}

// RowAct is the names a row's words read, as one viewer is shown them: the
// row masked for a viewer who may not read its personal columns (before
// any expression sees it), and the filters.
func RowAct(t datatables.Table, raw map[string]any, v Values) map[string]any {
	personal := v.Viewer.Personal && !IsRecords(t.Name)
	return map[string]any{"row": viewRow(t, raw, personal), "filters": filterAct(v)}
}

// ToolAct is the names a tool's row reads: the item, the outputs and the
// filters.
func ToolAct(outputs map[string]any, item any, v Values) map[string]any {
	return map[string]any{"outputs": outputs, "item": item, "filters": filterAct(v)}
}

// FiltersAct is what the page's own buttons read.
func FiltersAct(v Values) map[string]any { return map[string]any{"filters": filterAct(v)} }

func filterAct(v Values) map[string]any {
	out := map[string]any{}
	for k, x := range v.Set {
		out[k] = x
	}
	return out
}

// checkActionReads refuses an action's expression reading a column the
// part's table lacks.
func checkActionReads(ps *problems, at string, a Action, tb datatables.Table, name string) {
	for k, v := range a.Inputs {
		if s, ok := v.(string); ok {
			checkReads(ps, at+"/inputs/"+k, s, tb, name)
		}
	}
	if s := a.Signal; s != nil {
		checkReads(ps, at+"/signal/run", s.Run, tb, name)
		checkReads(ps, at+"/signal/to", s.To, tb, name)
		for k, v := range s.Input {
			if str, ok := v.(string); ok {
				checkReads(ps, at+"/signal/input/"+k, str, tb, name)
			}
		}
	}
	if ap := a.Approve; ap != nil {
		checkReads(ps, at+"/approve/run", ap.Run, tb, name)
	}
	checkReads(ps, at+"/when", a.When, tb, name)
}

// checkActionEnv holds an action to the business: its tool is one, its
// signal is one the workflow declares a page may send, its workflow is
// the business's.
func checkActionEnv(ps *problems, at string, a Action, env Env) {
	switch a.Kind() {
	case ActTool:
		if _, ok := toolOf(env, a.Tool); !ok {
			ps.add(at+"/tool", "%q opens the tool %s, which the business does not have.", a.Label, a.Tool)
		}
	case ActSignal:
		why := "the business has no workflow " + a.Signal.Workflow
		if env.Signal != nil {
			why = env.Signal(a.Signal.Workflow, a.Signal.Name)
		}
		if why != "" {
			ps.add(at+"/signal", "%q cannot tell %s: %s.", a.Label, a.Signal.Name, why)
		}
	case ActApprove:
		if env.Workflow == nil || !env.Workflow(a.Approve.Workflow) {
			ps.add(at+"/approve/workflow", "%q opens an approval of the workflow %s, which the business does not have.", a.Label, a.Approve.Workflow)
		}
	}
}

func toolOf(env Env, command string) (ToolInfo, bool) {
	if env.Tool == nil {
		return ToolInfo{}, false
	}
	return env.Tool(command)
}
