// Package history defines run events: the append-only log that is the run
// (spec 4.1). The orchestrator's decide() reads them; the store writes them.
package history

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"time"
)

// Event types (spec 4.1).
const (
	RunStarted            = "RunStarted"
	RunAdmitted           = "RunAdmitted" // a queued run got its concurrency slot
	StepScheduled         = "StepScheduled"
	StepStarted           = "StepStarted"
	EffectIntent          = "EffectIntent"
	StepCompleted         = "StepCompleted"
	StepFailed            = "StepFailed"
	StepSkipped           = "StepSkipped"
	StepCancelled         = "StepCancelled" // a losing parallel branch's unfinished step
	RetryScheduled        = "RetryScheduled"
	TimerFired            = "TimerFired"
	SignalReceived        = "SignalReceived"
	ApprovalRequested     = "ApprovalRequested"
	ApprovalDecided       = "ApprovalDecided"
	CompensationStarted   = "CompensationStarted"
	CompensationCompleted = "CompensationCompleted"
	RunCompleted          = "RunCompleted"
	RunFailed             = "RunFailed"
	RunCancelled          = "RunCancelled"
)

// Origins say which component wrote an event. Only "decide" events must be
// reproducible by replaying decide() over the preceding history.
const (
	OriginDecide    = "decide"
	OriginWorker    = "worker"
	OriginIngest    = "ingest"
	OriginScheduler = "scheduler"
	OriginAPI       = "api"
	OriginSignal    = "signal"
)

// Event is one recorded event.
type Event struct {
	Seq        int64           `json:"seq"`
	Type       string          `json:"type"`
	StepID     string          `json:"step_id,omitempty"` // step instance id, see InstanceID
	Attempt    int             `json:"attempt,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	RecordedAt time.Time       `json:"recorded_at"`
	Origin     string          `json:"origin"`
}

// IsTerminal reports whether the event ends the run.
func (e Event) IsTerminal() bool {
	return e.Type == RunCompleted || e.Type == RunFailed || e.Type == RunCancelled
}

// RunStartedPayload is recorded by ingest.
type RunStartedPayload struct {
	Run        RunInfo           `json:"run"`
	Trigger    any               `json:"trigger"`
	Env        map[string]any    `json:"env,omitempty"`
	Connectors map[string]string `json:"connectors,omitempty"` // "paystack@1" -> "1.4.0"
	// Policies are the approval policies the workflow names, as active when
	// the run started (spec 9.1).
	Policies map[string]PolicySnapshot `json:"policies,omitempty"`
}

// PolicySnapshot is one approval policy version.
type PolicySnapshot struct {
	Version  int             `json:"version"`
	Document json.RawMessage `json:"document"`
}

type RunInfo struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenant_id"`
	WorkflowID  string `json:"workflow_id"`
	Version     int    `json:"version"`
	Environment string `json:"environment"`
	StartedAt   string `json:"started_at"`
	// CallbacksAt and CallbackKey are set for a workflow with callback
	// steps (wd-v1 callback steps): the address its callbacks share
	// (<hooks>/<tenant>/callbacks/<run>) and the key each step's token is
	// derived from (CallbackToken). The key is sealed in history, as
	// personal data is, and never shown to expressions.
	CallbacksAt string `json:"callbacks_at,omitempty"`
	CallbackKey string `json:"callback_key,omitempty"`
}

// CallbackEvent is the signal a callback step waits for, correlated by the
// run and step instance (CallbackCorrelation).
const CallbackEvent = "callback"

// CallbackCorrelation is the correlation a callback step instance waits on.
func CallbackCorrelation(run, inst string) string { return run + "/" + inst }

// CallbackToken is the token in a callback step instance's address: an
// HMAC of the instance under the run's key, so each step has its own and
// none reveals the key.
func CallbackToken(key, inst string) string {
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte(inst))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

// CallbackURL is a callback step instance's address.
func CallbackURL(at, key, inst string) string {
	return at + "/" + CallbackToken(key, inst) + "/" + url.PathEscape(inst)
}

// Kinds of StepScheduled.
const (
	KindTask   = "task"
	KindTimer  = "timer"
	KindSignal = "signal"
	// KindSubflow starts another workflow as a child run (wd-v1 subflow
	// steps) and, when the step waits, waits as a signal step does for
	// SubflowEvent correlated by SubflowCorrelation.
	KindSubflow = "subflow"
)

// SubflowEvent is the signal a child run delivers to the subflow step
// that started it when it ends (or at once, for a step that does not
// wait): {run_id, status, outputs, error}.
const SubflowEvent = "subflow"

// SubflowCorrelation is the correlation a subflow step instance waits on.
func SubflowCorrelation(run, inst string) string { return run + "/" + inst }

// SubflowScheduled is what a subflow step starts: the workflow, the
// version (0: the one published when the step starts) and whether the
// step waits for the run to end.
type SubflowScheduled struct {
	Workflow string `json:"workflow"`
	Version  int    `json:"version,omitempty"`
	Wait     bool   `json:"wait"`
}

// ScheduledPayload is written by decide when a step starts.
type ScheduledPayload struct {
	Kind        string `json:"kind"`
	Queue       string `json:"queue,omitempty"`
	Connector   string `json:"connector,omitempty"`
	Connection  string `json:"connection,omitempty"`
	Action      string `json:"action,omitempty"`
	Input       any    `json:"input,omitempty"`
	Seed        string `json:"seed,omitempty"`     // idempotency seed; empty means the run id
	KeyStep     string `json:"key_step,omitempty"` // step part of the idempotency key
	Compensates string `json:"compensates,omitempty"`
	AvailableAt string `json:"available_at,omitempty"`
	FireAt      string `json:"fire_at,omitempty"`
	Event       string `json:"event,omitempty"`
	Correlation string `json:"correlation,omitempty"`
	TimeoutAt   string `json:"timeout_at,omitempty"`
	// Subflow is set for KindSubflow: Input is then the child's run inputs.
	Subflow *SubflowScheduled `json:"subflow,omitempty"`
	// Form is a form step's request, waited for as a signal (FormEvent,
	// correlated by Form.Request).
	Form *FormScheduled `json:"form,omitempty"`
	// Capability is set for a step expanded from a capability step: the
	// capability's input and the earlier steps' outputs the rail's mapping
	// read, kept so the worker maps the action's output back the same way.
	Capability *CapabilityScheduled `json:"capability,omitempty"`
}

// CapabilityScheduled is what an expanded step's output mapping reads
// besides the action's output (wd-v1 capability steps).
type CapabilityScheduled struct {
	Uses  string         `json:"uses"`
	Input any            `json:"input"`
	Steps map[string]any `json:"steps,omitempty"`
}

// FormEvent is the signal a form step waits for: its answers.
const FormEvent = "form.submitted"

// FormScheduled is what a form step asks, and of whom (docs/spec/forms.md
// 6.7). The recipient and prefill are sealed in history like any value
// derived from personal data.
type FormScheduled struct {
	Request     string         `json:"request"`
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	To          map[string]any `json:"to"`
	Via         []string       `json:"via,omitempty"`
	Verify      bool           `json:"verify,omitempty"`
	Schema      map[string]any `json:"schema"`
	Prefill     map[string]any `json:"prefill,omitempty"`
	Remind      []string       `json:"remind,omitempty"`
}

// Error is a classified failure.
type Error struct {
	Kind    string `json:"kind"` // retryable | fatal | unknown_outcome | not_sent | timeout | expression | child_failed | subflow | unsupported
	Message string `json:"message"`
	Next    string `json:"next"` // retry | reconcile | park | fail
	// MaybeApplied is set by the worker when a write failed in a way that
	// may still have taken effect. A step with such a failure parks instead
	// of failing when its retries run out.
	MaybeApplied bool `json:"maybe_applied,omitempty"`
	// RetryAfterMS is how long the provider asked to wait (Retry-After) on
	// a refusal; the next attempt waits at least this long.
	RetryAfterMS int64 `json:"retry_after_ms,omitempty"`
}

// Failure kinds for a tenant key that could not be unwrapped (decision
// 0019): the worker parks the step with KeyUnavailable; the key job
// resumes it with KeyRestored (nothing was sent: retry) or
// KeyRestoredReconcile (an earlier attempt may have been: reconcile).
// A resumption never spends the step's retry budget.
const (
	KindKeyUnavailable       = "key_unavailable"
	KindKeyRestored          = "key_restored"
	KindKeyRestoredReconcile = "key_restored_reconcile"
)

// Resumed reports whether a failure is a resumption after a parked key
// failure, which does not count against the retry budget.
func (e Error) Resumed() bool {
	return e.Kind == KindKeyRestored || e.Kind == KindKeyRestoredReconcile
}

type FailedPayload struct {
	Error Error `json:"error"`
}

type CompletedPayload struct {
	Output any `json:"output"`
	// ProviderOutput is what the rail's action answered, for a step
	// expanded from a capability step, whose Output is the capability's.
	ProviderOutput any      `json:"provider_output,omitempty"`
	Reconciled     bool     `json:"reconciled,omitempty"`
	Logs           []string `json:"logs,omitempty"` // code steps' console output, bounded
}

type IntentPayload struct {
	Key          string `json:"key"`
	Seed         string `json:"seed"`
	KeyStep      string `json:"key_step"`
	AttemptGroup int    `json:"attempt_group"`
	Class        string `json:"class"`
	Digest       string `json:"request_digest"`
}

type TimerPayload struct {
	Kind    string `json:"kind"` // wait | retry | approval_timeout | signal_timeout | run_timeout
	TimerID string `json:"timer_id,omitempty"`
}

type RetryPayload struct {
	At string `json:"at"`
}

type ApprovalRequestedPayload struct {
	Policy    string         `json:"policy,omitempty"`
	Role      string         `json:"role,omitempty"`
	Count     int            `json:"count,omitempty"`
	Subject   map[string]any `json:"subject,omitempty"`
	TimeoutAt string         `json:"timeout_at,omitempty"`
	Escalated bool           `json:"escalated,omitempty"`
	// From a policy: the levels approved in order (Role and Count are the
	// first level's), the step-up required to vote, and the constraints.
	PolicyVersion int                  `json:"policy_version,omitempty"`
	Levels        []ApprovalLevel      `json:"levels,omitempty"`
	StepUp        string               `json:"step_up,omitempty"`
	Constraints   *ApprovalConstraints `json:"constraints,omitempty"`
}

// ApprovalLevel is one stage of a multi-level approval.
type ApprovalLevel struct {
	Role  string `json:"role"`
	Count int    `json:"count"`
}

// ApprovalConstraints are a policy's separation-of-duties rules.
type ApprovalConstraints struct {
	ForbidSelfApproval bool `json:"forbid_self_approval"`
	DistinctApprovers  bool `json:"distinct_approvers"`
	// ForbidSoleApprover holds four-eyes even for the only person who can
	// decide (who then cannot approve what they made).
	ForbidSoleApprover bool `json:"forbid_sole_approver,omitempty"`
}

type ApprovalDecidedPayload struct {
	Decision  string `json:"decision"` // approved | rejected
	DecidedBy string `json:"decided_by"`
	Channel   string `json:"channel,omitempty"`
	// SelfApproved: a maker approved, as the only person who could.
	SelfApproved bool `json:"self_approved,omitempty"`
}

type SignalPayload struct {
	Event   string `json:"event"`
	Payload any    `json:"payload"`
}

type ControlStartedPayload struct {
	Path  *string `json:"path,omitempty"`  // branch
	Count *int    `json:"count,omitempty"` // foreach and parallel (branches)
	// Winner is recorded once by a parallel step with join "any", in a
	// second StepStarted, when its first branch finishes.
	Winner *string `json:"winner,omitempty"`
}

type RunFailedPayload struct {
	Error Error `json:"error"`
}

// RunCompletedPayload carries the workflow's outputs (wd-v1 callable
// workflows), evaluated when the run completed, for the subflow step
// that started it.
type RunCompletedPayload struct {
	Outputs map[string]any `json:"outputs,omitempty"`
}

// CompensationPrefix marks the instance id of a compensating action.
const CompensationPrefix = "compensate:"

// SplitInstance splits an instance id such as "pay_all[3].pay_employee" into
// its scope prefix ("pay_all[3].") and step id ("pay_employee").
func SplitInstance(inst string) (prefix, stepID string) {
	inst = strings.TrimPrefix(inst, CompensationPrefix)
	i := strings.LastIndexByte(inst, '.')
	if i < 0 {
		return "", inst
	}
	return inst[:i+1], inst[i+1:]
}

// TimeFormat is how times are written into payloads.
const TimeFormat = time.RFC3339Nano

func FormatTime(t time.Time) string { return t.UTC().Format(TimeFormat) }

func ParseTime(s string) (time.Time, error) { return time.Parse(TimeFormat, s) }

// InputDigest is how a resolved step input is compared across runs (a
// fork and its parent, a shadow run and the recording): SHA-256 of its
// JSON form, so 1, 1.0 and int64(1) agree.
func InputDigest(v any) string {
	raw, _ := json.Marshal(v)
	var norm any
	_ = json.Unmarshal(raw, &norm)
	raw, _ = json.Marshal(norm)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
