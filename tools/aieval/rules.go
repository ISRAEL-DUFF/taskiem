package main

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/capability"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/wd"
)

// A rule checks one property of a definition (spec 12.4: "required
// properties"); it returns whether it holds and, when not, why. Rules
// read the definition only: they are deterministic and free.
type rule func(def *wd.Definition, reg connector.Lookup) (bool, string)

// rules are the properties cases may require. Hygiene rules run on every
// case whether or not the case names them.
var rules = map[string]rule{
	"approval_before_payment": ruleApprovalBeforePayment,
	"idempotency":             ruleIdempotency,
	"webhook_auth":            ruleWebhookAuth,
	"dedup":                   ruleDedup,
	"bounded_concurrency":     ruleBoundedConcurrency,
	"approval_timeout":        ruleApprovalTimeout,
	"error_handling":          ruleErrorHandling,
	"reads_before_paying":     ruleReadsBeforePaying,
	"inputs_declared":         ruleInputsDeclared,
	"no_hardcoded_contacts":   ruleNoHardcodedContacts,
	"no_inline_secrets":       ruleNoInlineSecrets,
	"reach_by_phone":          ruleReachByPhone,
	"capability_when_unnamed": ruleCapabilityWhenUnnamed,
	"no_payment_with_product": ruleNoPaymentWithProduct,
	"reply_in_window":         ruleReplyInWindow,
	"opted_in_only":           ruleOptedInOnly,
	"invoice_copied_once":     ruleInvoiceCopiedOnce,
	"books_by_capability":     ruleBooksByCapability,
}

// hygiene rules apply to every case.
var hygiene = []string{"no_inline_secrets"}

func ruleNames() []string {
	names := make([]string, 0, len(rules))
	for n := range rules {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// walk visits every step, nested ones included.
func walk(steps []*wd.Step, fn func(st *wd.Step)) {
	for _, st := range steps {
		fn(st)
		for _, sub := range st.Children() {
			walk(sub, fn)
		}
	}
}

func isPaymentWrite(reg connector.Lookup, st *wd.Step) bool {
	if st.Type == "capability" {
		// A payment or refund on whichever rail the connection is.
		spec, ok := capability.Get(st.Uses)
		return ok && (spec.IsPayout() || st.Uses == "refund")
	}
	if st.Type != "connector" {
		return false
	}
	c, ok := reg.Get(st.Connector)
	if !ok {
		return false
	}
	a, ok := c.Manifest.Actions[st.Action]
	return ok && c.Manifest.Category == "payments" && a.Class.IsWrite()
}

// A payment write must exist and every one must be preceded by an
// approval (the builder's own policy rule, applied to the final draft).
func ruleApprovalBeforePayment(def *wd.Definition, reg connector.Lookup) (bool, string) {
	n := 0
	walk(def.Steps, func(st *wd.Step) {
		if isPaymentWrite(reg, st) {
			n++
		}
	})
	if n == 0 {
		return false, "no step moves money"
	}
	if f := builder.PolicyFindings(def, reg); len(f) > 0 {
		return false, f[0].Message
	}
	return true, ""
}

// Every payment write carries an idempotency seed, and every http write
// declared idempotent names its idempotency header.
func ruleIdempotency(def *wd.Definition, reg connector.Lookup) (bool, string) {
	why := ""
	n := 0
	walk(def.Steps, func(st *wd.Step) {
		switch {
		case isPaymentWrite(reg, st):
			n++
			if st.Effect == nil || st.Effect.IdempotencySeed == "" {
				why = "step " + st.ID + " moves money without effect.idempotency_seed"
			}
		case st.Type == "http" && st.HTTP != nil && st.HTTP.Class == "idempotent_write":
			n++
			if st.HTTP.IdempotencyHeader == "" && (st.Effect == nil || st.Effect.IdempotencySeed == "") {
				why = "http step " + st.ID + " is an idempotent write without an idempotency key"
			}
		}
	})
	if n == 0 {
		return false, "no write that needs an idempotency key"
	}
	return why == "", why
}

// A webhook trigger checks who is calling (connector events are verified
// by their connector).
func ruleWebhookAuth(def *wd.Definition, _ connector.Lookup) (bool, string) {
	switch def.Trigger.Type {
	case "connector_event", "capability_event":
		return true, ""
	case "webhook":
		if a, _ := def.Trigger.Config["auth"].(string); a != "" && a != "none" {
			return true, ""
		}
		return false, "the webhook accepts unauthenticated calls"
	}
	return false, "the trigger is not a webhook"
}

// The same event delivered twice starts one run: a webhook dedup
// expression, a connector event (deduplicated by its connector), a form
// submission (on its submission id), an email (on its Message-ID) or an
// invoice paid (once per invoice).
func ruleDedup(def *wd.Definition, _ connector.Lookup) (bool, string) {
	switch def.Trigger.Type {
	case "connector_event", "capability_event", "form", "email", "invoice":
		return true, ""
	case "webhook":
		if d, _ := def.Trigger.Config["dedup"].(string); d != "" {
			return true, ""
		}
		return false, "the webhook has no dedup expression"
	}
	// Other triggers deduplicate in a step: an idempotency seed on writes.
	ok := false
	walk(def.Steps, func(st *wd.Step) {
		if st.Effect != nil && st.Effect.IdempotencySeed != "" {
			ok = true
		}
	})
	if !ok {
		return false, "nothing stops a repeated event from acting twice"
	}
	return true, ""
}

func ruleBoundedConcurrency(def *wd.Definition, _ connector.Lookup) (bool, string) {
	n, why := 0, ""
	walk(def.Steps, func(st *wd.Step) {
		if st.Type == "foreach" {
			n++
			if st.Foreach == nil || st.Foreach.MaxConcurrency <= 0 {
				why = "foreach " + st.ID + " sets no max_concurrency"
			}
		}
	})
	if n == 0 {
		return false, "no foreach"
	}
	return why == "", why
}

func ruleApprovalTimeout(def *wd.Definition, _ connector.Lookup) (bool, string) {
	n, why := 0, ""
	walk(def.Steps, func(st *wd.Step) {
		if st.Type == "approval" {
			n++
			if st.Approval == nil || (st.Approval.Timeout == "" && st.Approval.Policy == "") {
				why = "approval " + st.ID + " can wait forever"
			}
		}
	})
	if n == 0 {
		return false, "no approval step"
	}
	return why == "", why
}

func ruleErrorHandling(def *wd.Definition, _ connector.Lookup) (bool, string) {
	ok := false
	walk(def.Steps, func(st *wd.Step) {
		if (st.OnError != nil && len(st.OnError.Steps) > 0) || (st.Compensate != nil && st.Compensate.Action != "") {
			ok = true
		}
	})
	if !ok {
		return false, "no on_error path or compensation"
	}
	return true, ""
}

// A read of a payments connector (balance, name enquiry, status) runs
// before the first payment write it guards.
func ruleReadsBeforePaying(def *wd.Definition, reg connector.Lookup) (bool, string) {
	reads := map[string]bool{}
	var payments []*wd.Step
	walk(def.Steps, func(st *wd.Step) {
		if st.Type == "capability" {
			if spec, ok := capability.Get(st.Uses); ok && spec.Allows(effects.Read) {
				reads[st.ID] = true
			}
			if isPaymentWrite(reg, st) {
				payments = append(payments, st)
			}
			return
		}
		if st.Type != "connector" {
			return
		}
		c, ok := reg.Get(st.Connector)
		if !ok || c.Manifest.Category != "payments" {
			return
		}
		if a, ok := c.Manifest.Actions[st.Action]; ok && !a.Class.IsWrite() {
			reads[st.ID] = true
		}
		if isPaymentWrite(reg, st) {
			payments = append(payments, st)
		}
	})
	if len(payments) == 0 {
		return false, "no step moves money"
	}
	for _, p := range payments {
		if !dependsOnAny(def, p, reads) {
			return false, "step " + p.ID + " pays without a read of the provider before it"
		}
	}
	return true, ""
}

// dependsOnAny reports whether st (or a control step enclosing it)
// transitively needs one of ids.
func dependsOnAny(def *wd.Definition, st *wd.Step, ids map[string]bool) bool {
	seen := map[string]bool{}
	var visit func(s *wd.Step) bool
	visit = func(s *wd.Step) bool {
		if s == nil || seen[s.ID] {
			return false
		}
		seen[s.ID] = true
		for _, n := range s.Needs {
			if ids[n] || visit(def.Step(n)) {
				return true
			}
		}
		return visit(def.Parent(s.ID))
	}
	return visit(st)
}

func ruleInputsDeclared(def *wd.Definition, _ connector.Lookup) (bool, string) {
	if def.RawInputs == nil || len(def.RawInputs.Schema) == 0 {
		return false, "the workflow declares no inputs schema"
	}
	return true, ""
}

var (
	rePhone = regexp.MustCompile(`(^|[^0-9])(\+?234|0)[789][01][0-9]{8}([^0-9]|$)`)
	reEmail = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	// Literal secret shapes: provider keys, bearer tokens, long opaque
	// strings outside expressions.
	reSecret = regexp.MustCompile(`(sk|pk|rk)_(live|test)_[A-Za-z0-9]{8,}|FLWSECK[A-Za-z0-9_-]{8,}|Bearer [A-Za-z0-9._-]{16,}|xox[abp]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16}`)
)

// literals returns every string literal in steps (expressions excluded).
func literals(def *wd.Definition) []string {
	raw, _ := json.Marshal(def.Steps)
	var v any
	_ = json.Unmarshal(raw, &v)
	var out []string
	var visit func(any)
	visit = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			for _, e := range t {
				visit(e)
			}
		case []any:
			for _, e := range t {
				visit(e)
			}
		case string:
			if !strings.HasPrefix(t, "=") {
				out = append(out, t)
			}
		}
	}
	visit(v)
	return out
}

// Phone numbers and email addresses come from tenant variables or the
// run's data, not literals in the definition.
func ruleNoHardcodedContacts(def *wd.Definition, _ connector.Lookup) (bool, string) {
	for _, s := range literals(def) {
		if rePhone.MatchString(s) || reEmail.MatchString(s) {
			return false, "a phone number or email address is written into the workflow"
		}
	}
	return true, ""
}

// reChatID is a field asking for a chat app's id or username, its name or
// title in lower case with _ and - as spaces.
var reChatID = regexp.MustCompile(`\b(chat ?id|telegram (chat |user ?)?(id|name|handle)|telegram username|tg (chat |user ?)?id|whatsapp id)\b`)

// People outside the business are messaged by their phone (notify@1
// message), never by a chat id they were asked to type, and a chat app's
// send never takes its recipient from someone's answers (N11).
func ruleReachByPhone(def *wd.Definition, _ connector.Lookup) (bool, string) {
	fields := func(schema any) string {
		m, _ := schema.(map[string]any)
		props, _ := m["properties"].(map[string]any)
		for n, p := range props {
			pm, _ := p.(map[string]any)
			title, _ := pm["title"].(string)
			if reChatID.MatchString(strings.ToLower(strings.NewReplacer("_", " ", "-", " ").Replace(n + " " + title))) {
				return n
			}
		}
		return ""
	}
	if def.Trigger.Type == "form" {
		schema, _ := def.InputsSchema()
		if f := fields(schema); f != "" {
			return false, "the form asks for " + f + ", a chat id people do not know"
		}
	}
	bad := ""
	walk(def.Steps, func(st *wd.Step) {
		if st.Form != nil && bad == "" {
			if f := fields(st.Form.Schema); f != "" {
				bad = "a form step asks for " + f + ", a chat id people do not know"
			}
		}
		if (st.Connector == "telegram@1" || st.Connector == "whatsapp@1") && bad == "" {
			to, _ := st.Input["chat_id"].(string)
			if st.Connector == "whatsapp@1" {
				to, _ = st.Input["to"].(string)
			}
			if strings.Contains(to, "trigger.body") || strings.Contains(to, "trigger.form") {
				bad = st.Connector + " sends to someone from their answers: use notify@1 message with their phone"
			}
		}
	})
	if bad != "" {
		return false, bad
	}
	return true, ""
}

func ruleNoInlineSecrets(def *wd.Definition, _ connector.Lookup) (bool, string) {
	raw, _ := json.Marshal(def)
	if reSecret.Match(raw) {
		return false, "a credential is written into the workflow"
	}
	return true, ""
}

// checkProperties evaluates the case's properties plus the hygiene rules.
func checkProperties(def *wd.Definition, reg connector.Lookup, want []string) (map[string]bool, []string) {
	names := append([]string{}, want...)
	for _, h := range hygiene {
		if !contains(names, h) {
			names = append(names, h)
		}
	}
	out := map[string]bool{}
	var why []string
	for _, n := range names {
		ok, reason := rules[n](def, reg)
		out[n] = ok
		if !ok {
			why = append(why, n+": "+reason)
		}
	}
	return out, why
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// For a goal that names no provider (the cases that ask for this
// property): every payment is a capability step, so it runs on whichever
// payments account the business connected (decision 0044).
func ruleCapabilityWhenUnnamed(def *wd.Definition, reg connector.Lookup) (bool, string) {
	n, why := 0, ""
	walk(def.Steps, func(st *wd.Step) {
		if !isPaymentWrite(reg, st) {
			return
		}
		n++
		if st.Type != "capability" {
			why = "step " + st.ID + " pays with " + st.Connector + ", a provider the goal does not name; use a capability step"
		}
	})
	if n == 0 {
		return false, "no step moves money"
	}
	return why == "", why
}

// When the business's payroll (or another process) runs in a product it
// connected (Payrolla: decision 0044), the product pays: the workflow uses
// the product's events or reads, and moves no money itself.
func ruleNoPaymentWithProduct(def *wd.Definition, reg connector.Lookup) (bool, string) {
	product := false
	if ref, ok := def.Trigger.Config["connector"].(string); ok {
		if c, ok := reg.Get(ref); ok && c.Manifest.KindOf() == "product" {
			product = true
		}
	}
	why := ""
	walk(def.Steps, func(st *wd.Step) {
		if c, ok := reg.Get(st.Connector); ok && c.Manifest.KindOf() == "product" {
			product = true
		}
		if isPaymentWrite(reg, st) {
			why = "step " + st.ID + " moves money; the product pays, so the workflow only tells, records or links"
		}
	})
	if !product {
		return false, "the workflow uses no product connector (its events or reads)"
	}
	return why == "", why
}

// windowed reports whether the step sends on a channel that allows a reply
// only for a while after the person's last message (Messenger and
// Instagram's 24 hours): its action says when the window ends.
func windowed(reg connector.Lookup, st *wd.Step) bool {
	if st.Type != "connector" {
		return false
	}
	c, ok := reg.Get(st.Connector)
	if !ok {
		return false
	}
	a, ok := c.Manifest.Actions[st.Action]
	if !ok || !a.Class.IsWrite() {
		return false
	}
	var out struct {
		Properties map[string]any `json:"properties"`
	}
	_ = json.Unmarshal(a.Output, &out)
	_, ends := out.Properties["window_ends_at"]
	return ends
}

// A reply after the window is never planned (spec business-connectors
// §10): a message on Messenger or Instagram answers the person who wrote,
// on the channel's own messages trigger, with nothing before it that waits
// (a wait, or a person deciding with no deadline within the day), and
// without the tag a person replying by hand uses.
func ruleReplyInWindow(def *wd.Definition, reg connector.Lookup) (bool, string) {
	n, why := 0, ""
	waits := map[string]bool{}
	walk(def.Steps, func(st *wd.Step) {
		switch {
		case st.Type == "wait":
			waits[st.ID] = true
		case st.Type == "approval" && (st.Approval == nil || !withinDay(st.Approval.Timeout)):
			waits[st.ID] = true
		case st.Type == "form":
			waits[st.ID] = true
		}
	})
	walk(def.Steps, func(st *wd.Step) {
		if !windowed(reg, st) {
			return
		}
		n++
		on, _ := def.Trigger.Config["connector"].(string)
		switch {
		case def.Trigger.Type != "connector_event" || on != st.Connector:
			why = "step " + st.ID + " messages someone on " + st.Connector + " the workflow did not hear from: a reply is only allowed within 24 hours of their message"
		case dependsOnAny(def, st, waits):
			why = "step " + st.ID + " replies after a step that waits, which may be after the 24 hours"
		case st.Input["tag"] != nil:
			why = "step " + st.ID + " uses the tag for a person replying by hand"
		}
	})
	if n == 0 {
		return false, "no step replies on Messenger or Instagram"
	}
	return why == "", why
}

// withinDay reports whether a timeout ends within 24 hours.
func withinDay(timeout string) bool {
	d, err := time.ParseDuration(timeout)
	return err == nil && d > 0 && d <= 24*time.Hour
}

// optWords name a person's marketing consent.
var optWords = regexp.MustCompile(`(?i)opt(ed)?[_ -]?in|consent|subscrib`)

// Marketing only to opted-in contacts (§10): every step that puts someone
// on a marketing list (an action taking consent) says they opted in, and
// runs only when the trigger or a condition before it checks so.
func ruleOptedInOnly(def *wd.Definition, reg connector.Lookup) (bool, string) {
	n, why := 0, ""
	walk(def.Steps, func(st *wd.Step) {
		if !takesConsent(reg, st) {
			return
		}
		n++
		consent, _ := st.Input["consent"].(map[string]any)
		if consent["opted_in"] != true {
			why = "step " + st.ID + " does not say the person opted in"
			return
		}
		if !consentChecked(def, st) {
			why = "step " + st.ID + " adds people without checking they opted in"
		}
	})
	if n == 0 {
		return false, "no step adds anyone to marketing"
	}
	return why == "", why
}

// takesConsent reports whether the step's action takes a person's consent
// to marketing (Brevo's lists, a Mailchimp audience).
func takesConsent(reg connector.Lookup, st *wd.Step) bool {
	if st.Type != "connector" {
		return false
	}
	c, ok := reg.Get(st.Connector)
	if !ok {
		return false
	}
	a, ok := c.Manifest.Actions[st.Action]
	if !ok {
		return false
	}
	var in struct {
		Properties map[string]any `json:"properties"`
	}
	_ = json.Unmarshal(a.Input, &in)
	_, ok = in.Properties["consent"]
	return ok
}

// consentChecked reports whether the step runs only for people who opted
// in: its condition, the trigger's or one of a step it follows names the
// consent.
func consentChecked(def *wd.Definition, st *wd.Step) bool {
	if optWords.MatchString(st.When) || optWords.Match(def.Trigger.RawCfg) {
		return true
	}
	seen := map[string]bool{}
	var visit func(s *wd.Step) bool
	visit = func(s *wd.Step) bool {
		if s == nil || seen[s.ID] {
			return false
		}
		seen[s.ID] = true
		if s != st && (optWords.MatchString(s.When) || (s.Type == "branch" && optWords.Match(s.RawConfig))) {
			return true
		}
		for _, id := range s.Needs {
			if visit(def.Step(id)) {
				return true
			}
		}
		return visit(def.Parent(s.ID))
	}
	return visit(st)
}

// Invoices copied once, with their reference (§10): the invoice is
// recorded in the books by one books_invoice step, whose number is the
// invoice's own, so a retry or a second event finds it instead of making
// another.
func ruleInvoiceCopiedOnce(def *wd.Definition, _ connector.Lookup) (bool, string) {
	n, why := 0, ""
	walk(def.Steps, func(st *wd.Step) {
		if st.Type != "capability" || st.Uses != "books_invoice" {
			return
		}
		n++
		num, _ := st.Input["number"].(string)
		if !strings.HasPrefix(num, "=") {
			why = "step " + st.ID + " records the invoice without its own number"
		}
	})
	switch {
	case n == 0:
		return false, "no step records the invoice in the books"
	case n > 1:
		return false, "the invoice is recorded in the books by more than one step"
	}
	return why == "", why
}

// Books work through the books capabilities (§10): no step calls a books
// provider's own action where a capability does the same, so the
// workflow runs on QuickBooks or Zoho Books, whichever is connected. A
// case asking for one provider's own feature does not ask for this.
func ruleBooksByCapability(def *wd.Definition, reg connector.Lookup) (bool, string) {
	n, why := 0, ""
	walk(def.Steps, func(st *wd.Step) {
		if st.Type == "capability" && strings.HasPrefix(st.Uses, "books_") {
			n++
		}
		if st.Type != "connector" {
			return
		}
		c, ok := reg.Get(st.Connector)
		if !ok {
			return
		}
		for name, m := range c.Manifest.Capabilities {
			if !strings.HasPrefix(name, "books_") {
				continue
			}
			mapped := m.Action == st.Action
			for _, cs := range m.Steps {
				mapped = mapped || cs.Action == st.Action
			}
			if mapped {
				why = "step " + st.ID + " calls " + st.Connector + "'s " + st.Action + "; use the " + name + " capability"
			}
		}
	})
	if n == 0 {
		return false, "no step uses a books capability"
	}
	return why == "", why
}
