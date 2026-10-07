// Package container runs container steps (spec 7.5,
// docs/container-steps.md): a pinned image, once per attempt, in a sandbox
// the platform controls. The Runner interface has three implementations:
//
//   - Kubernetes: a Pod per attempt in a dedicated namespace under gVisor
//     (runsc), non-root, read-only, no service account token, no network
//     but the worker's egress proxy. The production sandbox (boundary B14).
//   - Local: a process on the worker's host, in a temporary directory with
//     rlimits and a timeout. For development and tests only; it is NOT a
//     security boundary and refuses to start unless explicitly enabled.
//   - Fake: canned results, for tests.
//
// Inside the sandbox the program is supervised by taskiem-shim (shim.go),
// which feeds it its input, collects its output and logs within their caps
// and reports one result line; the Local runner supervises the process the
// same way, in-process.
package container

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/effects"
)

// Spec is one attempt of a container step.
type Spec struct {
	// Name identifies the attempt; unique per claim of the task, at most 40
	// characters of [a-z0-9-] (it names the Pod).
	Name    string
	Tenant  string
	Run     string
	Step    string
	Attempt int

	Image   string
	Command []string
	Args    []string
	// Input is the step's input as JSON.
	Input      []byte
	InputMode  string // "stdin" (default) or "file"
	OutputMode string // "stdout" (default) or "file"
	// Secrets are the declared secrets' values by name, as environment
	// variables (SecretsMode "env", the default) or files ("file").
	Secrets     map[string]string
	SecretsMode string
	// Env is platform-provided, non-secret environment
	// (TASKIEM_IDEMPOTENCY_KEY, TASKIEM_RUN_ID, ...).
	Env map[string]string

	CPUMillis   int
	MemoryMB    int
	Timeout     time.Duration
	OutputBytes int

	// Proxy is the egress proxy URL with its credentials
	// (http://taskiem:<token>@host:port), or "" for no network. It is a
	// secret: runners pass it like one.
	Proxy string
}

// Result is what a finished program produced.
type Result struct {
	Output json.RawMessage
	Logs   string
	// Elapsed is how long the program ran, measured by the runner (not by
	// anything inside the sandbox); container minutes are counted from it.
	Elapsed time.Duration
}

// Runner runs container steps. Run blocks until the program has finished,
// failed, or ctx ends; whatever it started is removed before it returns.
//
// Errors wrap an effects sentinel: effects.ErrNotSent when the program
// provably never started (an image that cannot be pulled, a refused Pod),
// effects.ErrFatal when it ran and failed in a way a retry would repeat
// (a non-zero exit, bad or oversized output, a refused image), and
// effects.ErrUnknownOutcome when it may have done part of its work (a
// timeout, out of memory, a lost node, cancellation).
type Runner interface {
	Run(ctx context.Context, s Spec) (Result, error)
}

// Sweeper is implemented by runners that can leave work behind when a
// worker dies (Kubernetes Pods); the worker calls Sweep periodically.
type Sweeper interface {
	Sweep(ctx context.Context) error
}

// LogBytes caps the logs kept from one run, as for code steps.
const LogBytes = 16 << 10

// Errors, by what a retry can do about them.
func notStarted(format string, args ...any) error {
	return fmt.Errorf("container did not start: %s: %w", fmt.Sprintf(format, args...), effects.ErrNotSent)
}

func fatal(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), effects.ErrFatal)
}

func unknown(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), effects.ErrUnknownOutcome)
}

// ErrRefused is wrapped by refusals of a spec itself (an image not on the
// registry allow-list).
var ErrRefused = errors.New("container refused")

// Outcome is what the supervisor (the shim, or the local runner) reports
// about one run of a program.
type Outcome struct {
	Exit   int             `json:"exit"`
	Output json.RawMessage `json:"output,omitempty"`
	Logs   string          `json:"logs,omitempty"`
	// Error is "" when the program finished and its output is valid:
	// "timeout", "output_too_large", "bad_output", "start: <why>", or
	// "killed: <signal>".
	Error   string        `json:"error,omitempty"`
	Elapsed time.Duration `json:"elapsed_ns"`
}

// ResultPrefix starts the shim's one result line.
const ResultPrefix = "TASKIEM-RESULT "

// Encode is the outcome as the shim's result line.
func (o Outcome) Encode() string {
	raw, _ := json.Marshal(o)
	return ResultPrefix + base64.StdEncoding.EncodeToString(raw)
}

// ParseOutcome finds the result line in a container's log.
func ParseOutcome(log string) (Outcome, bool) {
	var o Outcome
	lines := strings.Split(strings.TrimRight(log, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		rest, ok := strings.CutPrefix(lines[i], ResultPrefix)
		if !ok {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rest))
		if err != nil || json.Unmarshal(raw, &o) != nil {
			return o, false
		}
		return o, true
	}
	return o, false
}

// Settle turns an outcome into the step's result or error.
func Settle(o Outcome, elapsed time.Duration) (Result, error) {
	res := Result{Output: o.Output, Logs: o.Logs, Elapsed: elapsed}
	tail := func() string {
		l := strings.TrimSpace(o.Logs)
		if len(l) > 1024 {
			l = "…" + l[len(l)-1024:]
		}
		if l == "" {
			return ""
		}
		return ": " + l
	}
	switch {
	case o.Error == "timeout":
		return res, unknown("container step timed out after %s%s", elapsed.Round(time.Second), tail())
	case o.Error == "output_too_large":
		return res, fatal("container output is larger than the step's limit")
	case o.Error == "bad_output":
		return res, fatal("container output is not JSON")
	case strings.HasPrefix(o.Error, "start: "):
		return res, fatal("container program could not start: %s", strings.TrimPrefix(o.Error, "start: "))
	case strings.HasPrefix(o.Error, "killed: "):
		return res, unknown("container program was %s%s", o.Error, tail())
	case o.Error != "":
		return res, unknown("container program failed: %s", o.Error)
	case o.Exit != 0:
		return res, fatal("container program exited with status %d%s", o.Exit, tail())
	}
	if len(o.Output) == 0 {
		res.Output = json.RawMessage("null")
	}
	return res, nil
}
