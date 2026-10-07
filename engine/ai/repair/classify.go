// Package repair is the AI side of self-repair (spec 12.2): it classifies
// a failed run deterministically where it can, asks the model only when
// the rules are unsure or a patch is needed, has every patch checked by
// the publishing checks and by a shadow run the caller provides, and
// feeds failures back for up to three attempts.
//
// Like the builder, the package is powerless. It sees a redacted view of
// the failed run (sealed values replaced, free text masked) and the
// workflow's definition; it holds no database, vault, runtime or connector
// handler (imports_test.go), and its output is a proposal. The shadow run
// over the run's real recorded data is a function the repair service
// passes in: the data stays on the service's side, and what comes back
// for the model is sanitised there and redacted again here.
package repair

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Class is a failure class (spec 12.2's table).
type Class string

const (
	Transient      Class = "transient"
	Credential     Class = "credential"
	Data           Class = "data"
	SchemaDrift    Class = "schema_drift"
	Logic          Class = "logic"
	UnknownOutcome Class = "unknown_outcome"
)

// Classes lists every class.
var Classes = []Class{Transient, Credential, Data, SchemaDrift, Logic, UnknownOutcome}

// Patches reports whether the class's typical proposal changes the
// workflow (data, schema drift, logic) rather than asking a person to act.
func (c Class) Patches() bool { return c == Data || c == SchemaDrift || c == Logic }

// Drift is a contract-drift finding for one of the run's connector actions.
type Drift struct {
	Connector string `json:"connector"`
	Action    string `json:"action"`
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	Expected  string `json:"expected"`
	Observed  string `json:"observed"`
}

// Failure is what classification reads: redacted, no values.
type Failure struct {
	// Reason the repair started: run_failed, needs_reconciliation, drift.
	Reason string `json:"reason"`
	// RunStatus is the run's status when analysed.
	RunStatus string `json:"run_status"`
	// Step is the failing (or parked) step instance; StepType its type.
	Step     string `json:"step,omitempty"`
	StepType string `json:"step_type,omitempty"`
	// Connector, Action and ActionClass describe a connector step.
	Connector   string `json:"connector,omitempty"`
	Action      string `json:"action,omitempty"`
	ActionClass string `json:"action_class,omitempty"`
	// Error is the step's failure; RunError the run's.
	Error    *history.Error `json:"error,omitempty"`
	RunError *history.Error `json:"run_error,omitempty"`
	// Drift: findings for connector actions this run used.
	Drift []Drift `json:"drift,omitempty"`
}

// Classification is a class with how sure the rules are.
type Classification struct {
	Class Class `json:"class"`
	// Certain: the rules decided; otherwise the model is asked, and Class
	// is the rules' best guess if it cannot be.
	Certain bool   `json:"certain"`
	Why     string `json:"why"`
}

var (
	reHTTP       = regexp.MustCompile(`\bhttp (\d{3})\b`)
	reCredential = regexp.MustCompile(`(?i)unauthori[sz]ed|forbidden|invalid[ _-]?(api[ _-]?)?key|invalid[ _-]?token|expired[ _-]?token|token[ _-]?expired|` +
		`invalid_grant|invalid_client|authentication failed|no active \S+ connection|connection is missing|credentials? (are |is )?(invalid|expired|missing)`)
	reTransient = regexp.MustCompile(`(?i)service unavailable|temporarily unavailable|timed? ?out|timeout|connection (refused|reset)|too many requests|rate limit|` +
		`bad gateway|gateway timeout|try again`)
	reMissing = regexp.MustCompile(`(?i)no such key|no such attribute|missing|required|null|undefined|not found in|unsupported (type|overload)|no matching overload`)
)

// Classify classifies a failure by rules: error kinds, HTTP classes,
// connector error classes, drift records, and what the failing step's
// expressions read. def is the version the run failed on (nil when it
// cannot be loaded).
func Classify(f Failure, def *wd.Definition) Classification {
	e := f.Error
	if e == nil {
		e = f.RunError
	}
	msg := ""
	kind := ""
	if e != nil {
		msg, kind = e.Message, e.Kind
	}
	status := 0
	if m := reHTTP.FindStringSubmatch(msg); m != nil {
		_, _ = fmt.Sscan(m[1], &status)
	}
	switch {
	case f.RunStatus == "needs_reconciliation" || f.Reason == "needs_reconciliation" || (e != nil && (e.Next == "park" || e.MaybeApplied)) ||
		kind == "unknown_outcome" || kind == "indeterminate" || kind == "fork_mismatch":
		return Classification{UnknownOutcome, true, "a write's outcome is uncertain, so the step is parked for a person"}
	case status == 401 || status == 403 || reCredential.MatchString(msg):
		return Classification{Credential, true, "the provider refused the credentials, or the connection is missing"}
	case f.Reason == "drift" || (len(f.Drift) > 0 && (kind == "expression" || kind == "fatal" || kind == "policy")):
		return Classification{SchemaDrift, true, "a connector's output no longer matches its declared schema"}
	case kind == "retryable" || kind == "not_sent" || status == 429 || status >= 500 || (kind != "expression" && reTransient.MatchString(msg)):
		return Classification{Transient, true, "the provider failed temporarily; retries ran out"}
	case kind == "timeout":
		return Classification{Transient, false, "the run exceeded its timeout"}
	case kind == "expression":
		if reMissing.MatchString(msg) && readsTrigger(def, f.Step) {
			return Classification{Data, true, "an expression over the trigger's data failed: a field is missing or of another type"}
		}
		return Classification{Logic, false, "an expression failed"}
	case kind == "fatal" && (status == 400 || status == 422):
		return Classification{Data, reMissing.MatchString(msg) || strings.Contains(strings.ToLower(msg), "invalid"), "the provider rejected the request's data"}
	}
	return Classification{Logic, false, "no rule matched"}
}

// readsTrigger reports whether a step's definition reads the trigger.
func readsTrigger(def *wd.Definition, inst string) bool {
	if def == nil || inst == "" {
		return false
	}
	_, id := history.SplitInstance(inst)
	st := def.Step(id)
	if st == nil {
		return false
	}
	raw, _ := json.Marshal(st)
	for _, part := range []json.RawMessage{raw, st.RawInput, st.RawConfig} {
		if strings.Contains(string(part), "trigger.") || strings.Contains(string(part), "item.") {
			return true
		}
	}
	return strings.Contains(st.When, "trigger.")
}
