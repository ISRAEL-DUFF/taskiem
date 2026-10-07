package builder

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Context is what a tenant contributes to a prompt. The API fills it from
// the tenant's own tables, reading names and summaries only: connections
// by name and connector, variables by name, policies by name and summary.
// No field can hold a credential, a secret or a variable's value, so none
// can reach a prompt.
type Context struct {
	Connections []Connection       `json:"connections,omitempty"`
	Policies    []Policy           `json:"policies,omitempty"`
	Variables   []Variable         `json:"variables,omitempty"`
	Workflows   []ExistingWorkflow `json:"-"`
}

// Connection is a tenant's connection, by name.
type Connection struct {
	Environment string `json:"environment"`
	Connector   string `json:"connector"` // connector id, e.g. paystack
	Name        string `json:"name"`
	Status      string `json:"status,omitempty"`
}

// Policy is an active approval policy. Document is the policy itself (who
// approves, from what data: no personal data), used by the dry run.
type Policy struct {
	Name     string          `json:"name"`
	Summary  string          `json:"summary"`
	Document json.RawMessage `json:"-"`
}

// Variable is a tenant variable (the `env` root), by name only.
type Variable struct {
	Environment string `json:"environment"`
	Name        string `json:"name"`
}

// ExistingWorkflow is one of the tenant's workflows, for similarity. Only
// its structure reaches a prompt (names, trigger type, step types and
// connector actions), never its inputs or expressions.
type ExistingWorkflow struct {
	Name       string
	Definition json.RawMessage
}

// manifestOnly hides connector handlers from the pipeline: the builder
// and the dry run see manifests (schemas, classes) and nothing they could
// execute, so no code path from engine/ai reaches a real provider.
type manifestOnly struct{ l connector.Lookup }

// ManifestOnly wraps l so the connectors it returns carry no handlers.
func ManifestOnly(l connector.Lookup) connector.Lookup {
	if m, ok := l.(manifestOnly); ok {
		return m
	}
	return manifestOnly{l}
}

func strip(c *connector.Connector) *connector.Connector {
	return &connector.Connector{Manifest: c.Manifest}
}

func (m manifestOnly) Get(ref string) (*connector.Connector, bool) {
	c, ok := m.l.Get(ref)
	if !ok {
		return nil, false
	}
	return strip(c), true
}

func (m manifestOnly) List() []*connector.Connector {
	list := m.l.List()
	out := make([]*connector.Connector, len(list))
	for i, c := range list {
		out[i] = strip(c)
	}
	return out
}

func (m manifestOnly) Pins() map[string]string { return m.l.Pins() }

// synonyms widen a goal's words to the vocabulary manifests use.
var synonyms = map[string][]string{
	"pay": {"payments", "transfer"}, "payment": {"payments", "transfer"}, "payout": {"payments", "transfer"},
	"payouts": {"payments", "transfer"}, "disburse": {"payments", "transfer"}, "disbursement": {"payments", "transfer"},
	"salary": {"payments", "transfer"}, "salaries": {"payments", "transfer"}, "refund": {"payments", "refund"},
	"transfer": {"payments"}, "send": {"send"}, "money": {"payments"}, "wallet": {"payments", "wallet"},
	"sms": {"messaging", "sms"}, "text": {"sms"}, "otp": {"messaging", "otp"}, "message": {"messaging"},
	"notify": {"messaging", "send"}, "alert": {"messaging", "send"}, "whatsapp": {"whatsapp", "messaging"},
	"kyc": {"identity", "verify"}, "bvn": {"identity", "bvn"}, "nin": {"identity", "nin"}, "verify": {"identity", "verify"},
	"identity": {"identity"}, "email": {"gmail", "email"}, "mail": {"gmail", "email"}, "sheet": {"googlesheets", "productivity"},
	"spreadsheet": {"googlesheets", "productivity"}, "database": {"database", "query"}, "sql": {"database", "query"},
	"file": {"storage"}, "upload": {"storage"}, "bucket": {"s3", "storage"}, "statement": {"open_banking"},
	"bank": {"open_banking", "payments"}, "airtime": {"airtime"}, "chat": {"slack", "telegram"},
}

var stop = map[string]bool{"the": true, "and": true, "for": true, "with": true, "when": true, "then": true, "that": true,
	"this": true, "from": true, "into": true, "every": true, "each": true, "our": true, "their": true, "them": true,
	"workflow": true, "flow": true, "after": true, "before": true, "should": true, "want": true, "need": true}

// words splits text into lower-case keywords.
func words(s string) []string {
	f := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var out []string
	for _, w := range f {
		if len(w) >= 3 && !stop[w] {
			out = append(out, w)
		}
	}
	return out
}

// Ranked is a connector and its retrieval score.
type Ranked struct {
	Ref   string
	Score int
}

// Rank scores every connector against the goal by keyword overlap (no
// vector database): a goal word naming the connector counts most, then its
// category, then its actions and triggers. Connectors the tenant has a
// connection for get a nudge. Ties keep ref order, so retrieval is
// deterministic.
func Rank(goal string, reg connector.Lookup, conns []Connection) []Ranked {
	terms := map[string]int{}
	for _, w := range words(goal) {
		terms[w] = max(terms[w], 2)
		for _, s := range synonyms[w] {
			terms[s] = max(terms[s], 1)
		}
	}
	have := map[string]bool{}
	for _, c := range conns {
		have[c.Connector] = true
	}
	var out []Ranked
	for _, c := range reg.List() {
		m := c.Manifest
		score := 0
		id := strings.ToLower(m.ID)
		name := strings.ToLower(m.Name)
		for t, wgt := range terms {
			switch {
			case t == id || t == name || (len(t) >= 4 && strings.Contains(id, t)):
				score += 10 * wgt
			case t == strings.ToLower(m.Category):
				score += 4 * wgt
			}
			for an, a := range m.Actions {
				if hit(t, an) || hit(t, a.Title) {
					score += 2 * wgt
				}
			}
			for tn := range m.Triggers {
				if hit(t, tn) {
					score += wgt
				}
			}
			if hit(t, m.Description) {
				score += wgt
			}
		}
		if score > 0 && have[m.ID] {
			score += 3
		}
		if score > 0 {
			out = append(out, Ranked{Ref: c.Ref(), Score: score})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Ref < out[j].Ref
	})
	return out
}

func hit(term, text string) bool {
	for _, w := range words(text) {
		if w == term || (len(term) >= 4 && strings.HasPrefix(w, term)) {
			return true
		}
	}
	return false
}

// Catalogue lists every connector in one line each, ordered by ref with
// actions and triggers sorted, so the text is byte-identical between
// requests and caches as a prompt prefix.
func Catalogue(reg connector.Lookup) string {
	var b strings.Builder
	b.WriteString("# Connector catalogue\n\nEvery connector available to this tenant: `ref` (what a step's `connector` field pins), name, category, then its actions as `name(class)` and its triggers. Detailed schemas for the connectors relevant to a request come with the request.\n\n")
	for _, c := range reg.List() {
		m := c.Manifest
		fmt.Fprintf(&b, "- %s: %s [%s]. Actions: ", c.Ref(), m.Name, m.Category)
		names := make([]string, 0, len(m.Actions))
		for n := range m.Actions {
			names = append(names, n)
		}
		sort.Strings(names)
		for i, n := range names {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s(%s)", n, m.Actions[n].Class)
		}
		if len(m.Triggers) > 0 {
			tn := make([]string, 0, len(m.Triggers))
			for n := range m.Triggers {
				tn = append(tn, n)
			}
			sort.Strings(tn)
			b.WriteString(". Triggers: " + strings.Join(tn, ", "))
		}
		b.WriteString(".\n")
	}
	return b.String()
}

// detail is one connector's full description for a request.
func detail(c *connector.Connector) map[string]any {
	m := c.Manifest
	actions := map[string]any{}
	for n, a := range m.Actions {
		act := map[string]any{"title": a.Title, "class": a.Class, "input": a.Input, "output": a.Output}
		if a.Description != "" {
			act["description"] = a.Description
		}
		if a.Compensate != nil {
			act["compensate"] = *a.Compensate
		}
		if a.Reconcile != "" {
			act["reconcile"] = a.Reconcile
		}
		actions[n] = act
	}
	triggers := map[string]any{}
	for n, t := range m.Triggers {
		triggers[n] = map[string]any{"events": t.Events}
	}
	out := map[string]any{"ref": c.Ref(), "name": m.Name, "category": m.Category, "actions": actions}
	if m.Description != "" {
		out["description"] = m.Description
	}
	if len(triggers) > 0 {
		out["triggers"] = triggers
	}
	return out
}

// structure summarises a workflow for similarity: what it does, not what
// it handles. Inputs, expressions and constants are left out.
func structure(w ExistingWorkflow) (map[string]any, bool) {
	def, err := wd.Load(w.Definition)
	if err != nil {
		return nil, false
	}
	var steps []string
	var walk func(list []*wd.Step)
	walk = func(list []*wd.Step) {
		for _, st := range list {
			s := st.ID + ": " + st.Type
			if st.Type == "connector" {
				s += " " + st.Connector + "." + st.Action
			}
			if len(st.Needs) > 0 {
				s += " after " + strings.Join(st.Needs, ",")
			}
			steps = append(steps, s)
			for _, sub := range st.Children() {
				walk(sub)
			}
		}
	}
	walk(def.Steps)
	return map[string]any{"name": w.Name, "description": def.Description, "trigger": def.Trigger.Type, "steps": steps}, true
}

// similar picks up to n workflows sharing the most words with the goal.
func similar(goal string, list []ExistingWorkflow, n int) []map[string]any {
	terms := map[string]bool{}
	for _, w := range words(goal) {
		terms[w] = true
	}
	type scored struct {
		s     map[string]any
		score int
		name  string
	}
	var all []scored
	for _, w := range list {
		st, ok := structure(w)
		if !ok {
			continue
		}
		score := 0
		raw, _ := json.Marshal(st)
		for _, word := range words(string(raw)) {
			if terms[word] {
				score++
			}
		}
		if score > 0 {
			all = append(all, scored{st, score, w.Name})
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].name < all[j].name
	})
	var out []map[string]any
	for i := 0; i < len(all) && i < n; i++ {
		out = append(out, all[i].s)
	}
	return out
}

// Detail is one connector's full description (actions with their schemas,
// classes, compensation and reconcile actions), as a prompt carries it.
func Detail(c *connector.Connector) map[string]any { return detail(c) }
