// Package expansion turns a workflow's capability steps into the steps of
// the rails its connections are (decision 0044, spec 4.4, wd-v1 capability
// steps). A capability step becomes the rail's own action step (or steps,
// for a composite), marked with the rail's mapping so the engine maps the
// capability's input to the action's and the action's output back; an
// await_settlement or await_reversal becomes signals on the rail's
// normalised payout events raced against a deadline. The result is an
// ordinary definition: every rail action keeps its class, idempotency,
// reconciliation and pii rules unchanged.
package expansion

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/capability"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Rail is the connection a capability step runs on.
type Rail struct {
	// Ref is the rail's connector, as steps name it (paystack@1).
	Ref string
	// Connection is the connection's name in the environment.
	Connection string
	// Name is the rail's name for messages and the canvas (Paystack).
	Name     string
	Manifest *connector.Manifest
}

// Resolver finds the rail for a capability step: the connection it names,
// or, when it names none, the environment's only connection whose rail
// declares the capability. Its error is said to the business as it is.
type Resolver func(connection, uses string) (Rail, error)

// Choice is the rail one capability step was expanded onto, for the run's
// audit entry.
type Choice struct {
	Step       string `json:"step"`
	Uses       string `json:"uses"`
	Connection string `json:"connection"`
	Connector  string `json:"connector"`
}

// Refusal is why a capability step cannot run on its rail, in plain words.
type Refusal struct {
	Step    string
	Message string
	Err     error // what the resolver said, when it said it
}

func (r *Refusal) Error() string { return "step " + r.Step + ": " + r.Message }

func (r *Refusal) Unwrap() error { return r.Err }

// Has reports whether a definition has capability or template steps: steps
// a run expands before it starts.
func Has(d *wd.Definition) bool {
	found := false
	d.Walk(func(s *wd.Step) { found = found || s.Type == "capability" || s.Type == "template" })
	return found
}

// HasCapabilities reports whether a definition has capability steps.
func HasCapabilities(d *wd.Definition) bool {
	found := false
	d.Walk(func(s *wd.Step) { found = found || s.Type == "capability" })
	return found
}

// Expand expands every capability step of doc (after its template steps:
// ExpandTemplates). It returns doc unchanged, and no choices, when there
// is none.
func Expand(doc []byte, resolve Resolver) ([]byte, []Choice, error) {
	d, err := wd.Load(doc)
	if err != nil {
		return nil, nil, err
	}
	if !HasCapabilities(d) {
		return doc, nil, nil
	}
	root, err := expr.DecodeJSON(doc)
	if err != nil {
		return nil, nil, err
	}
	x := &expander{def: d, resolve: resolve, rails: map[string]Rail{}}
	m := root.(map[string]any)
	if m["steps"], err = x.steps(m["steps"].([]any)); err != nil {
		return nil, nil, err
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil, nil, err
	}
	if _, err := wd.Load(out); err != nil {
		return nil, nil, fmt.Errorf("expansion: %w", err)
	}
	sort.Slice(x.choices, func(i, j int) bool { return x.choices[i].Step < x.choices[j].Step })
	return out, x.choices, nil
}

type expander struct {
	def     *wd.Definition
	resolve Resolver
	rails   map[string]Rail // capability step id -> its rail
	choices []Choice
}

// steps expands one list of steps, and every list nested in them.
func (x *expander) steps(list []any) ([]any, error) {
	var out []any
	for _, raw := range list {
		st := raw.(map[string]any)
		if err := x.nested(st); err != nil {
			return nil, err
		}
		if st["type"] != "capability" {
			out = append(out, st)
			continue
		}
		var exp []any
		var err error
		switch uses := st["uses"].(string); {
		case uses == "await_payment":
			exp, err = x.awaitPayment(st)
		case wd.IsWait(uses):
			exp, err = x.wait(st)
		default:
			exp, err = x.capability(st)
		}
		if err != nil {
			return nil, err
		}
		keepOrigin(st, exp)
		out = append(out, exp...)
	}
	return out, nil
}

// keepOrigin marks the steps a capability step expanded from a template
// became with its origin: the one that keeps its id keeps its id inside
// the template too (its output's name in its scope's results), and the
// others name only the template and template step.
func keepOrigin(st map[string]any, exp []any) {
	o, ok := st["origin"].(map[string]any)
	if !ok {
		return
	}
	for _, e := range exp {
		em := e.(map[string]any)
		switch {
		case em["id"] == st["id"]:
			em["origin"] = o
		case em["origin"] == nil:
			em["origin"] = map[string]any{"template": o["template"], "step": o["step"]}
		}
	}
}

func (x *expander) nested(st map[string]any) error {
	var err error
	if oe, ok := st["on_error"].(map[string]any); ok {
		if oe["steps"], err = x.steps(oe["steps"].([]any)); err != nil {
			return err
		}
	}
	cfg, _ := st["config"].(map[string]any)
	if cfg == nil {
		return nil
	}
	for _, key := range []string{"paths", "branches"} {
		list, _ := cfg[key].([]any)
		for _, p := range list {
			pm := p.(map[string]any)
			if pm["steps"], err = x.steps(pm["steps"].([]any)); err != nil {
				return err
			}
		}
	}
	if def, ok := cfg["default"].(map[string]any); ok {
		if def["steps"], err = x.steps(def["steps"].([]any)); err != nil {
			return err
		}
	}
	if st["type"] == "foreach" {
		if cfg["steps"], err = x.steps(cfg["steps"].([]any)); err != nil {
			return err
		}
	}
	return nil
}

// rail resolves (once) the rail a capability step runs on.
func (x *expander) rail(id string) (Rail, error) {
	if r, ok := x.rails[id]; ok {
		return r, nil
	}
	s := x.def.Step(id)
	r, err := x.resolve(s.Connection, s.Uses)
	if err != nil {
		return Rail{}, &Refusal{Step: id, Message: strings.TrimPrefix(err.Error(), ErrNoConnection.Error()+": "), Err: err}
	}
	x.rails[id] = r
	x.choices = append(x.choices, Choice{Step: id, Uses: s.Uses, Connection: r.Connection, Connector: r.Ref})
	return r, nil
}

// capability expands a capability step into its rail's action steps; the
// last keeps the step's id, so steps.<id>.output is the capability's
// output whichever rail ran it.
func (x *expander) capability(st map[string]any) ([]any, error) {
	id, uses := st["id"].(string), st["uses"].(string)
	r, err := x.rail(id)
	if err != nil {
		return nil, err
	}
	cm, ok := r.Manifest.Capabilities[uses]
	spec, known := capability.Get(uses)
	if !ok || !known {
		return nil, &Refusal{Step: id, Message: fmt.Sprintf("your %s connection cannot %s", r.Name, lowerFirst(titleOf(uses)))}
	}
	given, _ := st["input"].(map[string]any)
	in, err := x.input(id, r, cm, spec, given)
	if err != nil {
		return nil, err
	}
	plan := cm.Plan()
	ids := make([]string, len(plan))
	local := map[string]string{} // the rail's step name -> expanded id
	for i, ps := range plan {
		ids[i] = id
		if i < len(plan)-1 {
			name := ps.ID
			if name == "" {
				name = ps.Action
			}
			ids[i] = id + "__" + name
		}
	}
	var out []any
	for i, ps := range plan {
		step := map[string]any{
			"id": ids[i], "type": "connector", "connector": r.Ref, "action": ps.Action, "connection": r.Connection,
			"input": in,
		}
		earlier := map[string]any{}
		for k, v := range local {
			earlier[k] = v
		}
		mapping := map[string]any{"uses": uses, "input": dropAbsent(ps.Input, in), "output": map[string]any{}}
		if len(earlier) > 0 {
			mapping["steps"] = earlier
		}
		if i == len(plan)-1 {
			mapping["output"] = cm.Output
		}
		step["capability"] = mapping
		for _, k := range []string{"retry", "timeout", "effect"} {
			if v, ok := st[k]; ok {
				step[k] = v
			}
		}
		if oe, ok := st["on_error"]; ok {
			if i == len(plan)-1 {
				step["on_error"] = oe
			} else {
				// An earlier call's failure is handled the same way: a copy
				// of on_error, its steps renamed, reading this call's error.
				step["on_error"] = renamed(oe, id, ids[i])
			}
		}
		if i == 0 {
			for _, k := range []string{"needs", "when"} {
				if v, ok := st[k]; ok {
					step[k] = v
				}
			}
		} else {
			step["needs"] = []any{ids[i-1]}
		}
		name, _ := st["name"].(string)
		if name == "" {
			name = spec.Title
		}
		if i == len(plan)-1 {
			step["name"] = name + " (" + r.Name + ")"
			if v, ok := st["description"]; ok {
				step["description"] = v
			}
		} else {
			title := r.Manifest.Actions[ps.Action].Title
			if title == "" {
				title = ps.Action
			}
			step["name"] = name + ": " + lowerFirst(title) + " (" + r.Name + ")"
		}
		out = append(out, step)
		if ps.ID != "" {
			local[ps.ID] = ids[i]
		}
	}
	return out, nil
}

// input is the step's capability input with the capability's defaults,
// refused when it breaks the rail's requires, unsupported or fixed.
func (x *expander) input(id string, r Rail, cm connector.CapabilityMap, spec *capability.Spec, given map[string]any) (map[string]any, error) {
	// An empty value is no value: a template's optional parameter left
	// blank (the wallet to pay from, on a rail with one balance).
	if len(given) > 0 {
		kept := map[string]any{}
		for k, v := range given {
			if v != nil && v != "" {
				kept[k] = v
			}
		}
		given = kept
	}
	in := spec.WithDefaults(given)
	refuse := func(format string, args ...any) error {
		return &Refusal{Step: id, Message: fmt.Sprintf(format, args...)}
	}
	for _, n := range cm.Requires {
		top := strings.Split(n, ".")[0]
		v, ok := in[top]
		if !ok {
			return nil, refuse("%s needs %s to %s", r.Name, describe(spec, n), lowerFirst(spec.Title))
		}
		// A nested field given as a literal object must be there; one given
		// by an expression is checked when the step runs.
		if obj, isObj := v.(map[string]any); isObj && strings.Contains(n, ".") {
			if _, ok := obj[strings.SplitN(n, ".", 2)[1]]; !ok {
				return nil, refuse("%s needs %s to %s", r.Name, describe(spec, n), lowerFirst(spec.Title))
			}
		}
	}
	for _, n := range cm.Unsupported {
		top, rest, nested := strings.Cut(n, ".")
		v, ok := given[top]
		if nested {
			obj, isObj := v.(map[string]any)
			_, ok = obj[rest]
			ok = ok && isObj
		}
		if ok {
			return nil, refuse("%s cannot take %s to %s; leave it out, or use a rail that can", r.Name, n, lowerFirst(spec.Title))
		}
	}
	for _, n := range sortedKeys(cm.Fixed) {
		v, ok := in[n]
		if !ok || expr.IsExpr(v) {
			continue
		}
		if fmt.Sprint(v) != fmt.Sprint(cm.Fixed[n]) {
			return nil, refuse("%s takes %s %v only, not %v", r.Name, n, cm.Fixed[n], v)
		}
	}
	return in, nil
}

// dropAbsent leaves out the mapping's fields that read only capability
// inputs the step does not give (input.?source when there is no source):
// known now, so the action never sees them.
func dropAbsent(mapping map[string]any, in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range mapping {
		src, ok := v.(string)
		if !ok || !expr.IsExpr(src) {
			out[k] = v
			continue
		}
		paths, err := mapEngine.Paths(src, "input", "output", "steps")
		if err != nil {
			out[k] = v
			continue
		}
		reads, present := 0, 0
		for _, p := range paths {
			switch {
			case p[0] == "input" && len(p) > 1:
				reads++
				if _, ok := in[p[1]]; ok {
					present++
				}
			case p[0] != "input":
				reads, present = 1, 1 // reads an earlier step: keep it
			}
		}
		if reads == 0 || present > 0 {
			out[k] = v
		}
	}
	return out
}

var mapEngine = capability.MappingEngine

// wait expands await_settlement or await_reversal: the rail's payout
// events for the payment, raced against a deadline; for a settlement, a
// lookup when the deadline comes first. The step's own id is a transform
// that says how it ended.
func (x *expander) wait(st map[string]any) ([]any, error) {
	id, uses := st["id"].(string), st["uses"].(string)
	in, _ := st["input"].(map[string]any)
	payout := in["payout_id"]
	var r Rail
	var cm connector.CapabilityMap
	var ok bool
	if pay, err := x.paymentFor(id, payout); err == nil {
		if r, err = x.rail(pay.ID); err != nil {
			return nil, err
		}
		cm, ok = r.Manifest.Capabilities[pay.Uses]
	} else if _, named := st["connection"]; named || !expr.IsExpr(fmt.Sprint(payout)) || !readsStep(payout) {
		// A payout from elsewhere (a list of earlier payouts): the step's
		// own connection, and how that rail settles a bank payout.
		if r, err = x.rail(id); err != nil {
			return nil, err
		}
		cm, ok = r.Manifest.Capabilities["pay_bank"]
	} else {
		return nil, err
	}
	if !ok || cm.SettlesBy == nil {
		return nil, &Refusal{Step: id, Message: fmt.Sprintf("%s finishes a payment when it is made: there is nothing to wait for", r.Name)}
	}
	if cm.SettlesBy.Trigger == "" {
		return nil, &Refusal{Step: id, Message: fmt.Sprintf("%s sends no payment events, and waiting by lookups alone is not supported yet", r.Name)}
	}
	if !slices.ContainsFunc(x.choices, func(c Choice) bool { return c.Step == id }) {
		x.choices = append(x.choices, Choice{Step: id, Uses: uses, Connection: r.Connection, Connector: r.Ref})
	}
	corr := asExpr(payout)
	timeout, _ := st["timeout"].(string)
	waitID, deadline, lookup := id+"__wait", id+"__deadline", id+"__lookup"

	events := []string{"payout.settled", "payout.failed", "payout.reversed"}
	if uses == "await_reversal" {
		events = []string{"payout.reversed"}
	}
	var branches []any
	for _, ev := range events {
		if e, ok := r.Manifest.Events[ev]; !ok || e.Trigger != cm.SettlesBy.Trigger {
			continue
		}
		name := strings.TrimPrefix(ev, "payout.")
		branches = append(branches, map[string]any{"name": name, "steps": []any{map[string]any{
			"id": id + "__" + name, "type": "signal", "name": capability.EventTitle(ev) + " (" + r.Name + ")",
			"config": map[string]any{"event": capability.SignalName(r.Ref, ev), "correlation": corr, "timeout": timeout},
		}}})
	}
	// A rail that sends no reversal event (Flutterwave) is watched by
	// looking the payout up when the watch ends.
	byLookup := len(branches) == 0 && uses == "await_reversal"
	if len(branches) == 0 && !byLookup {
		return nil, &Refusal{Step: id, Message: fmt.Sprintf("%s sends no %s event", r.Name, strings.Join(events, " or "))}
	}
	deadlineSteps := []any{map[string]any{"id": deadline, "type": "wait", "name": "Deadline", "config": map[string]any{"duration": timeout}}}
	if uses == "await_settlement" || byLookup {
		look, err := x.lookup(lookup, deadline, id, r, corr)
		if err != nil {
			return nil, err
		}
		deadlineSteps = append(deadlineSteps, look)
	}
	branches = append(branches, map[string]any{"name": "deadline", "steps": deadlineSteps})
	join := "any"
	if byLookup {
		// Nothing to race: the lookup alone says how the watch ended
		// (a parallel needs two branches, so the other does nothing).
		branches = append(branches, map[string]any{"name": "no_event", "steps": []any{map[string]any{
			"id": id + "__no_event", "type": "transform", "name": r.Name + " sends no reversal event", "config": map[string]any{"output": map[string]any{}}}}})
		join = "all"
	}
	race := map[string]any{"id": waitID, "type": "parallel", "name": capability.WaitTitle(uses) + " (" + r.Name + ")",
		"config": map[string]any{"branches": branches, "join": join}}
	for _, k := range []string{"needs", "when"} {
		if v, ok := st[k]; ok {
			race[k] = v
		}
	}
	name, _ := st["name"].(string)
	if name == "" {
		name = capability.WaitTitle(uses)
	}
	result := map[string]any{"id": id, "type": "transform", "name": name, "needs": []any{waitID},
		"config": map[string]any{"output": outcome(uses, "steps."+waitID+".output", id, lookup)}}
	if v, ok := st["on_error"]; ok {
		race["on_error"] = v
	}
	return []any{race, result}, nil
}

// awaitPayment expands await_payment: the rail's payment.received for the
// payment page a collect_link made (or the one-time account a
// collect_transfer opened), raced against a deadline, then a
// get_payment lookup in case the event never came. The step's own id is a
// transform that says how it ended: paid, failed or unpaid.
func (x *expander) awaitPayment(st map[string]any) ([]any, error) {
	id := st["id"].(string)
	in, _ := st["input"].(map[string]any)
	payment := in["payment_id"]
	var r Rail
	var err error
	if link := x.collectFor(payment); link != nil {
		r, err = x.rail(link.ID)
	} else if _, named := st["connection"]; named || !readsStep(payment) {
		r, err = x.rail(id) // a payment page made elsewhere: the step's own connection
	} else {
		return nil, &Refusal{Step: id, Message: "payment_id must read the output of a collect_link or collect_transfer step (=steps.<id>.output.payment_id)"}
	}
	if err != nil {
		return nil, err
	}
	if _, ok := r.Manifest.Events["payment.received"]; !ok {
		return nil, &Refusal{Step: id, Message: fmt.Sprintf("%s does not say when a customer pays", r.Name)}
	}
	if !slices.ContainsFunc(x.choices, func(c Choice) bool { return c.Step == id }) {
		x.choices = append(x.choices, Choice{Step: id, Uses: "await_payment", Connection: r.Connection, Connector: r.Ref})
	}
	corr := asExpr(payment)
	timeout, _ := st["timeout"].(string)
	waitID, deadline, lookup := id+"__wait", id+"__deadline", id+"__lookup"
	deadlineSteps := []any{map[string]any{"id": deadline, "type": "wait", "name": "Deadline", "config": map[string]any{"duration": timeout}}}
	if cm, ok := r.Manifest.Capabilities["get_payment"]; ok && len(cm.Plan()) == 1 {
		ps := cm.Plan()[0]
		look := map[string]any{"payment_id": corr}
		deadlineSteps = append(deadlineSteps, map[string]any{
			"id": lookup, "type": "connector", "connector": r.Ref, "action": ps.Action, "connection": r.Connection,
			"name": "Look the payment up (" + r.Name + ")", "needs": []any{deadline}, "input": look,
			"capability": map[string]any{"uses": "get_payment", "input": dropAbsent(ps.Input, look), "output": cm.Output},
			"retry":      map[string]any{"max": int64(5), "backoff": "exponential", "initial": "1m"},
			"on_error": map[string]any{"steps": []any{map[string]any{"id": lookup + "_failed", "type": "transform",
				"config": map[string]any{"output": map[string]any{"status": "pending"}}}}},
		})
	}
	branches := []any{
		map[string]any{"name": "paid", "steps": []any{map[string]any{
			"id": id + "__paid", "type": "signal", "name": capability.EventTitle("payment.received") + " (" + r.Name + ")",
			"config": map[string]any{"event": capability.SignalName(r.Ref, "payment.received"), "correlation": corr, "timeout": timeout},
		}}},
		map[string]any{"name": "deadline", "steps": deadlineSteps},
	}
	race := map[string]any{"id": waitID, "type": "parallel", "name": capability.WaitTitle("await_payment") + " (" + r.Name + ")",
		"config": map[string]any{"branches": branches, "join": "any"}}
	for _, k := range []string{"needs", "when", "on_error"} {
		if v, ok := st[k]; ok {
			race[k] = v
		}
	}
	name, _ := st["name"].(string)
	if name == "" {
		name = capability.WaitTitle("await_payment")
	}
	w := "steps." + waitID + ".output"
	field := func(f string) string { return w + ".?paid.?" + id + "__paid.?fields.?" + f }
	look := func(f string) string { return w + ".?deadline.?" + lookup + ".?" + f }
	lstatus := look("status") + ".orValue('pending')"
	out := "={'status': " + w + ".?paid.hasValue() ? 'paid' : " + lstatus + " == 'paid' ? 'paid' : " + lstatus + " == 'failed' ? 'failed' : 'unpaid', " +
		"?'amount_kobo': " + field("amount_kobo") + ".or(" + look("amount_kobo") + "), " +
		"?'paid_at': " + field("paid_at") + ".or(" + look("paid_at") + ")}"
	result := map[string]any{"id": id, "type": "transform", "name": name, "needs": []any{waitID}, "config": map[string]any{"output": out}}
	return []any{race, result}, nil
}

// collectFor is the collect_link or collect_transfer step a payment_id
// reads, if it reads exactly one.
func (x *expander) collectFor(payment any) *wd.Step {
	src, ok := payment.(string)
	if !ok || !expr.IsExpr(src) {
		return nil
	}
	refs, err := wdEngine.References(src)
	if err != nil {
		return nil
	}
	var found *wd.Step
	for sid := range refs.Steps {
		if s := x.def.Step(sid); s != nil && s.Type == "capability" && (s.Uses == "collect_link" || s.Uses == "collect_transfer") {
			if found != nil {
				return nil
			}
			found = s
		}
	}
	return found
}

// lookup is the get_payout step asked at the deadline. A lookup that fails
// leaves the payout unconfirmed rather than failing the wait.
func (x *expander) lookup(id, after, payStep string, r Rail, corr string) (map[string]any, error) {
	cm, ok := r.Manifest.Capabilities["get_payout"]
	if !ok || len(cm.Plan()) != 1 {
		return nil, &Refusal{Step: payStep, Message: fmt.Sprintf("%s cannot look a payout up", r.Name)}
	}
	ps := cm.Plan()[0]
	in := map[string]any{"payout_id": corr}
	return map[string]any{
		"id": id, "type": "connector", "connector": r.Ref, "action": ps.Action, "connection": r.Connection,
		"name": "Look the payout up (" + r.Name + ")", "needs": []any{after}, "input": in,
		"capability": map[string]any{"uses": "get_payout", "input": dropAbsent(ps.Input, in), "output": cm.Output},
		"retry":      map[string]any{"max": int64(5), "backoff": "exponential", "initial": "1m"},
		"on_error": map[string]any{"steps": []any{map[string]any{"id": id + "_failed", "type": "transform",
			"config": map[string]any{"output": map[string]any{"status": "pending", "reason": "lookup_failed"}}}}},
	}, nil
}

// outcome is the wait's output: how the race ended. Only the winning
// branch is in the race's output, so every read is optional.
func outcome(uses, race, id, lookup string) string {
	won := func(branch string) string { return race + ".?" + branch }
	field := func(branch, f string) string {
		return race + ".?" + branch + ".?" + id + "__" + branch + ".?fields.?" + f
	}
	look := func(f string) string { return race + ".?deadline.?" + lookup + ".?" + f }
	if uses == "await_reversal" {
		return "={'status': " + won("reversed") + ".hasValue() || " + look("status") + ".orValue('') == 'reversed' ? 'reversed' : 'none', " +
			"?'reason': " + field("reversed", "reason") + ".or(" + look("reason") + ")}"
	}
	lstatus := look("status") + ".orValue('pending')"
	return "={'status': " + won("settled") + ".hasValue() ? 'settled' : (" + won("failed") + ".hasValue() || " + won("reversed") + ".hasValue()) ? 'failed' : " +
		lstatus + " == 'settled' ? 'settled' : " + lstatus + " in ['failed', 'reversed'] ? 'failed' : 'unconfirmed', " +
		"?'settled_at': " + field("settled", "settled_at") + ".or(" + look("settled_at") + "), " +
		"?'reason': " + field("failed", "reason") + ".or(" + won("reversed") + ".hasValue() ? optional.of('reversed') : " + look("reason") + ")}"
}

// paymentFor is the payment step a wait follows: the capability step its
// payout_id reads.
func (x *expander) paymentFor(id string, payout any) (*wd.Step, error) {
	src, ok := payout.(string)
	if !ok || !expr.IsExpr(src) {
		return nil, &Refusal{Step: id, Message: "payout_id must read the payment step's output (=steps.<id>.output.payout_id)"}
	}
	refs, err := wdEngine.References(src)
	if err != nil {
		return nil, &Refusal{Step: id, Message: err.Error()}
	}
	var found []*wd.Step
	for sid := range refs.Steps {
		s := x.def.Step(sid)
		if s == nil || s.Type != "capability" || wd.IsWait(s.Uses) {
			continue
		}
		if spec, ok := capability.Get(s.Uses); ok && spec.IsPayout() {
			found = append(found, s)
		}
	}
	if len(found) != 1 {
		return nil, &Refusal{Step: id, Message: "payout_id must read the output of one payment step (pay_bank, pay_wallet or pay_mobile_money)"}
	}
	return found[0], nil
}

var wdEngine = expr.MustNew()

// readsStep reports whether an expression reads a step's output.
func readsStep(v any) bool {
	src, _ := v.(string)
	refs, err := wdEngine.References(src)
	return err == nil && len(refs.Steps) > 0
}

func asExpr(v any) string {
	if s, ok := v.(string); ok {
		if expr.IsExpr(s) {
			return s
		}
		return "=" + strconv.Quote(s)
	}
	b, _ := json.Marshal(v)
	return "=" + string(b)
}

// describe names a capability input for a person: its description, then
// its name ("the wallet or account to pay from (source)").
func describe(spec *capability.Spec, name string) string {
	var p struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(spec.Input, &p)
	if d := p.Properties[name].Description; d != "" {
		d, _, _ = strings.Cut(d, ",")
		return lowerFirst(d) + " (" + name + ")"
	}
	return name
}

func titleOf(uses string) string {
	if uses == "await_payment" {
		return "wait for a customer's payment"
	}
	if wd.IsWait(uses) {
		return "follow a payout to its end"
	}
	if s, ok := capability.Get(uses); ok {
		return s.Title
	}
	return uses
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// renamed copies an on_error flow for a composite's earlier call: every
// step in it gets the call's suffix (step ids are unique in a definition),
// and its expressions read the call (steps.<call>.error) where they read
// the capability step.
func renamed(onError any, from, to string) any {
	suffix := strings.TrimPrefix(to, from)
	names := map[string]string{from: to}
	var collect func(v any)
	collect = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if id, ok := t["id"].(string); ok && t["type"] != nil {
				names[id] = id + suffix
			}
			for _, c := range t {
				collect(c)
			}
		case []any:
			for _, c := range t {
				collect(c)
			}
		}
	}
	raw, _ := json.Marshal(onError)
	cp, _ := expr.DecodeJSON(raw)
	collect(cp)
	var rewrite func(v any, key string) any
	rewrite = func(v any, key string) any {
		switch t := v.(type) {
		case map[string]any:
			for k, c := range t {
				t[k] = rewrite(c, k)
			}
			return t
		case []any:
			for i, c := range t {
				if key == "needs" {
					if s, ok := c.(string); ok && names[s] != "" {
						t[i] = names[s]
						continue
					}
				}
				t[i] = rewrite(c, "")
			}
			return t
		case string:
			if key == "id" && names[t] != "" {
				return names[t]
			}
			if expr.IsExpr(t) {
				for old, nu := range names {
					q := regexp.QuoteMeta(old)
					t = regexp.MustCompile(`\bsteps\.`+q+`\b`).ReplaceAllLiteralString(t, "steps."+nu)
					t = regexp.MustCompile(`\bsteps\[\s*(["'])`+q+`(["'])\s*\]`).ReplaceAllString(t, "steps[${1}"+nu+"${2}]")
				}
			}
			return t
		}
		return v
	}
	return rewrite(cp, "")
}
