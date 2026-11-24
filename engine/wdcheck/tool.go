package wdcheck

import (
	"fmt"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/expansion"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/templates"
)

// Care is how much a tool asks before it runs (decision 0073, made tools
// §8), worked out from its steps, never declared by its author.
type Care struct {
	// Level: answers (every step that reaches outside only reads: it
	// answers at once, with an echo of what was read), yes (anything else:
	// the summary and yes, as run does) or step_up (a step moves money out
	// of the business: yes, then a passkey or the hand-off link).
	Level string `json:"level"`
	// Because names the steps that set it, by id: the first that writes,
	// and every one that moves money.
	Because []string `json:"because,omitempty"`
	// If, for a members' tool that asks yes only because of top-level
	// steps that run when a condition over what it was asked holds
	// (`"when": "=has(trigger.body.done)"`: a list whose row button marks
	// a row), are those conditions: a call for which none holds answers
	// at once, as Otherwise says (For). Nothing else relaxes care.
	If        []string `json:"if,omitempty"`
	Otherwise string   `json:"otherwise,omitempty"`
}

// triggerOnly reads a step's condition: only the trigger, never a step's
// output, a setting or a saved thing, so it is known before the run.
var triggerOnly = expr.MustNewWithRoots("trigger")

// For is the care a call with input meets: Otherwise when every
// condition of If is false for it, else Level. A condition that cannot be
// read counts as true, so a doubt asks.
func (c Care) For(input map[string]any) string {
	if len(c.If) == 0 || c.Otherwise == "" {
		return c.Level
	}
	act := map[string]any{"trigger": map[string]any{"body": input}}
	for _, cond := range c.If {
		holds, err := triggerOnly.EvalBool(cond, act)
		if err != nil || holds {
			return c.Level
		}
	}
	return c.Otherwise
}

const (
	CareAnswers = "answers"
	CareYes     = "yes"
	CareStepUp  = "step_up"
)

// ToolCare works out a definition's care, its template steps expanded with
// find (a template step is judged by the steps it becomes). A
// customers' tool never answers without yes (made tools §11). No
// approval policy names a workflow's start, so none is read at start.
func ToolCare(doc []byte, reg connector.Lookup, find expansion.Templates) (Care, error) {
	def, err := expandedDef(doc, find)
	if err != nil {
		return Care{}, err
	}
	return careOf(def, reg), nil
}

func careOf(def *wd.Definition, reg connector.Lookup) Care {
	c := Care{Level: CareAnswers}
	def.Walk(func(st *wd.Step) {
		switch stepCare(st, reg) {
		case CareStepUp:
			if c.Level != CareStepUp {
				c.Because = nil
			}
			c.Level = CareStepUp
			c.Because = append(c.Because, st.ID)
		case CareYes:
			if c.Level == CareAnswers {
				c.Level = CareYes
				c.Because = []string{st.ID}
			}
		}
	})
	if def.Tool != nil && def.Tool.Audience() != wd.ToolForMembers && c.Level == CareAnswers {
		c.Level = CareYes
	}
	if c.Level == CareYes && def.Tool != nil && def.Tool.Audience() == wd.ToolForMembers {
		c.If, c.Otherwise = guardedYes(def, reg)
	}
	return c
}

// guardedYes are the conditions of the steps that make a members' tool
// ask yes, when each such step is a top-level one whose `when` reads only
// the trigger: then a call for which none holds runs nothing that writes,
// and answers at once. Nil when any step asks yes unconditionally (or
// inside another step, whose own condition is not read here).
func guardedYes(def *wd.Definition, reg connector.Lookup) ([]string, string) {
	top := map[*wd.Step]bool{}
	for _, st := range def.Steps {
		top[st] = true
	}
	var conds []string
	ok := true
	def.Walk(func(st *wd.Step) {
		if !ok || stepCare(st, reg) == CareAnswers {
			return
		}
		if !top[st] || st.When == "" {
			ok = false
			return
		}
		refs, err := triggerOnly.References(st.When)
		if err != nil || len(refs.Roots) == 0 {
			ok = false
			return
		}
		conds = append(conds, st.When)
	})
	if !ok || len(conds) == 0 {
		return nil, ""
	}
	return conds, CareAnswers
}

// stepCare is one step's part in care. A step that reaches outside and
// only reads answers at once: a connector action of class read, an http
// GET or HEAD, a container marked read, an ai step. A step that writes,
// asks a person (a form, an approval), waits (for time, a signal or a
// call back), runs code (its host functions cannot be checked, so it
// counts as a write), or starts another workflow asks yes. One that moves
// money asks step-up. A step whose kind is not known counts as a write.
func stepCare(st *wd.Step, reg connector.Lookup) string {
	switch st.Type {
	case "branch", "parallel", "foreach", "transform", "ai":
		return CareAnswers
	case "connector", "capability", "http", "container", "subflow":
		if st.Type == "connector" && spendsMoney[st.Connector+"/"+st.Action] {
			return CareStepUp
		}
		switch templates.StepEffect(st, reg) {
		case templates.EffectMovesMoney:
			return CareStepUp
		case templates.EffectWrite:
			return CareYes
		}
		return CareAnswers
	}
	// code, form, approval, wait, signal, callback, respond, and anything
	// not known.
	return CareYes
}

// spendsMoney are actions outside the payments connectors that spend the
// business's money all the same (airtime from its balance): a tool with one
// asks a passkey too.
var spendsMoney = map[string]bool{
	"africastalking@1/send_airtime": true,
}

// toolProblems are the tool rules that need the platform: a command or a
// phrase Taskiem's own commands use, in any language (made tools §12),
// and a step that moves money in a tool for customers or linked
// businesses. def is the definition with its templates expanded.
func toolProblems(def *wd.Definition, reg connector.Lookup) []Problem {
	t := def.Tool
	if t == nil {
		return nil
	}
	var out []Problem
	if ReservedCommand(t.Command) {
		out = append(out, pr("/tool/command", fmt.Sprintf("%q is one of Taskiem's own commands; choose another word", t.Command)))
	}
	for i, s := range t.Says {
		if ReservedPhrase(s) {
			out = append(out, pr(fmt.Sprintf("/tool/says/%d", i), fmt.Sprintf("%q starts like one of Taskiem's own commands; choose other words", s)))
		}
	}
	if t.Audience() != wd.ToolForMembers {
		def.Walk(func(st *wd.Step) {
			if stepCare(st, reg) == CareStepUp {
				path := "/steps/" + st.ID
				if st.Origin != nil {
					top, _, _ := strings.Cut(st.Origin.Step, "__")
					path = "/steps/" + top
				}
				out = append(out, pr(path, "this step moves money, and only a member, with a passkey, may start a run that pays: a tool for customers cannot have it"))
			}
		})
	}
	return out
}

// extraReserved are English words Taskiem's chat reads in its own code,
// before or beside the reader's word lists (the money, stock, order and
// saving commands, tools and help): a tool may not take them either.
var extraReserved = []string{
	"tools", "tool", "help", "menu", "run", "yes", "no", "cancel", "stop", "status", "approvals", "approve", "reject",
	"pay", "paid", "transfer", "send", "invoice", "invoices", "bill", "bills", "balance", "receipt", "refund", "owes", "owe",
	"spent", "spend", "expense", "expenses", "cash", "sold", "sell", "sale", "sales", "received", "receive",
	"count", "stock", "take", "order", "orders", "customer", "customers", "product", "products", "price", "prices",
	"save", "saved", "remind", "reminder", "done", "can", "add", "invite", "link", "sign", "pin", "switch", "language",
	"tables", "find", "search", "publish", "build", "connect",
	// Building a workflow from words (the chat's build words).
	"create", "automate", "automatically", "every", "each", "whenever", "when", "daily", "weekly", "monthly", "once",
	"remind", "notify", "alert", "tell", "text", "sms", "whatsapp", "message", "email",
	// The built-in tools' own "/" words.
	"products", "sell", "handover", "services", "purchases", "students", "payment", "payments", "debtors", "clearance",
	"receipts", "money", "riders", "staff", "tasks", "schools", "fees", "attendance", "bookings", "clock", "briefing",
	// A route's own word: /v1/tools/buttons/{token} and its customers'
	// twin would take a tool called buttons.
	"buttons",
}

// ReservedCommand reports whether word starts one of Taskiem's own
// commands in any language (engine/lang's word lists, which the reader
// uses), so a tool may not take it.
func ReservedCommand(word string) bool {
	w := lang.Fold(word)
	if w == "" {
		return false
	}
	for _, x := range extraReserved {
		if w == x {
			return true
		}
	}
	return lang.CommandWord(w)
}

// ReservedPhrase reports whether a tool's phrase would be read as one of
// Taskiem's own commands, in any language.
func ReservedPhrase(phrase string) bool {
	f := strings.Fields(lang.Fold(phrase))
	if len(f) == 0 {
		return false
	}
	for _, x := range extraReserved {
		if f[0] == x {
			return true
		}
	}
	_, ok := lang.MatchIntent(phrase, lang.Tags())
	return ok
}

// expandedDef loads doc with its template steps expanded with find.
func expandedDef(doc []byte, find expansion.Templates) (*wd.Definition, error) {
	def, err := wd.Load(doc)
	if err != nil || !def.HasTemplates() {
		return def, err
	}
	expanded, _, err := expansion.ExpandTemplates(doc, find)
	if err != nil {
		return nil, err
	}
	return wd.Load(expanded)
}
