// Package canary is the synthetic end-to-end probe (Phase 4, P4-4;
// decision 0023, docs/reliability.md). It does what a customer's system
// does: it signs a webhook for a canary workflow in an internal tenant,
// sends it through the public hooks URL (load balancer, edge, database),
// then follows the run through the public API until a worker has run its
// code step and the run has finished with the expected output. It holds
// only that webhook's signing key and an API key that can read runs.
package canary

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/telemetry"
)

// WorkflowID is the canary workflow's definition id; its webhook signing
// key is the environment secret "webhook_" + WorkflowID.
const WorkflowID = "wf_taskiemcanary"

// HookPath is the canary workflow's webhook path.
const HookPath = "/taskiem-canary"

// Definition is the canary workflow: a signed webhook, a code step on the
// sandbox queue (so a worker must claim and run it) and a transform. The
// run's output is n * 2 + 1, which the probe checks.
const Definition = `{
  "schema": "wd/v1",
  "id": "` + WorkflowID + `",
  "version": 1,
  "name": "Taskiem canary",
  "description": "Synthetic end-to-end probe (docs/reliability.md). Do not edit: the canary checks its output.",
  "trigger": {"type": "webhook", "config": {"path": "` + HookPath + `", "auth": "hmac", "dedup": "=trigger.body.probe"}},
  "steps": [
    {"id": "double", "type": "code", "config": {"language": "typescript", "source": "export default (input: {n: number}) => ({ n: input.n * 2 })"}, "input": {"n": "=trigger.body.n"}},
    {"id": "out", "type": "transform", "needs": ["double"], "config": {"output": "=steps.double.output.n + 1"}}
  ],
  "settings": {"retention": "1d"}
}`

// Prober runs probes.
type Prober struct {
	// HookURL is the canary workflow's full webhook URL, as a provider
	// would call it: https://hooks.example.com/hooks/<tenant>/taskiem-canary?env=prod.
	HookURL string
	// Secret signs the webhook (HMAC-SHA256, X-Taskiem-Signature).
	Secret string
	// APIURL and APIKey read the run (GET /v1/runs/{id}); the key needs
	// run.read only.
	APIURL, APIKey string
	// Timeout bounds one probe from send to finished run; default 60s.
	Timeout time.Duration
	// Poll is how often the run is read; default 250ms.
	Poll   time.Duration
	Client *http.Client
	Logger *slog.Logger
	// Record, when set, stores each result (the status page's automatic
	// signal, status.RecordProbe).
	Record func(ctx context.Context, r Result) error
}

// Result is one probe's outcome.
type Result struct {
	OK bool
	// Stage is where a failed probe failed: accept (the webhook was not
	// answered 202), complete (the run did not finish in time, or failed),
	// output (it finished with the wrong output).
	Stage  string
	Detail string
	RunID  string
	// Accept is how long the webhook took to be answered; Complete is send
	// to finished run.
	Accept, Complete time.Duration
}

// Code is the short form stored for the status page: "" for success,
// otherwise stage:detail.
func (r Result) Code() string {
	if r.OK {
		return ""
	}
	return r.Stage + ":" + r.Detail
}

func (p *Prober) defaults() {
	if p.Timeout <= 0 {
		p.Timeout = 60 * time.Second
	}
	if p.Poll <= 0 {
		p.Poll = 250 * time.Millisecond
	}
	if p.Client == nil {
		p.Client = &http.Client{Timeout: 15 * time.Second}
	}
	if p.Logger == nil {
		p.Logger = slog.Default()
	}
}

// Sign is the X-Taskiem-Signature header value for body.
func Sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// Probe runs one probe and reports it in metrics (and Record).
func (p *Prober) Probe(ctx context.Context) Result {
	p.defaults()
	r := p.probe(ctx)
	result := "ok"
	if !r.OK {
		result = r.Stage
	} else {
		telemetry.CanaryLastSuccess.SetToCurrentTime()
	}
	telemetry.CanaryProbes.WithLabelValues(result).Inc()
	if r.Accept > 0 {
		telemetry.CanarySeconds.WithLabelValues("accept").Observe(r.Accept.Seconds())
	}
	if r.OK {
		telemetry.CanarySeconds.WithLabelValues("complete").Observe(r.Complete.Seconds())
	}
	if p.Record != nil && ctx.Err() == nil {
		if err := p.Record(ctx, r); err != nil {
			p.Logger.Warn("canary: cannot record the result", "err", err)
		}
	}
	if !r.OK && ctx.Err() == nil {
		p.Logger.Warn("canary probe failed", "stage", r.Stage, "detail", r.Detail, "run", r.RunID)
	}
	return r
}

func (p *Prober) probe(ctx context.Context) Result {
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	nBig, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	n := nBig.Int64()
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	body, _ := json.Marshal(map[string]any{"probe": hex.EncodeToString(id), "n": n, "sent_at": time.Now().UTC().Format(time.RFC3339Nano)})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.HookURL, bytes.NewReader(body))
	if err != nil {
		return Result{Stage: "accept", Detail: "bad_url"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "taskiem-canary")
	req.Header.Set("X-Taskiem-Signature", Sign(p.Secret, body))
	start := time.Now()
	resp, err := p.Client.Do(req)
	if err != nil {
		return Result{Stage: "accept", Detail: "unreachable"}
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	accept := time.Since(start)
	if resp.StatusCode != http.StatusAccepted {
		return Result{Stage: "accept", Detail: fmt.Sprintf("http_%d", resp.StatusCode), Accept: accept}
	}
	var accepted struct {
		RunID  string `json:"run_id"`
		Queued bool   `json:"queued"`
	}
	if json.Unmarshal(raw, &accepted) != nil || accepted.RunID == "" {
		return Result{Stage: "accept", Detail: "no_run_id", Accept: accept}
	}
	r := Result{RunID: accepted.RunID, Accept: accept}
	for {
		st, output, err := p.run(ctx, accepted.RunID)
		switch {
		case err != nil && ctx.Err() != nil:
			r.Stage, r.Detail = "complete", "timeout"
			return r
		case err != nil:
			// The API may be the part that is down; keep trying until the
			// deadline, then report where it stopped.
			r.Detail = err.Error()
		case st == "completed":
			r.Complete = time.Since(start)
			if want := float64(n*2 + 1); output != want {
				r.Stage, r.Detail = "output", fmt.Sprintf("got_%v", output)
				return r
			}
			r.OK = true
			return r
		case st == "failed" || st == "cancelled":
			r.Stage, r.Detail = "complete", "run_"+st
			return r
		}
		select {
		case <-ctx.Done():
			r.Stage = "complete"
			if r.Detail == "" {
				r.Detail = "timeout"
			}
			return r
		case <-time.After(p.Poll):
		}
	}
}

var errAPI = errors.New("api")

// run reads a run's status and output through the public API.
func (p *Prober) run(ctx context.Context, id string) (string, any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.APIURL, "/")+"/v1/runs/"+id, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	req.Header.Set("User-Agent", "taskiem-canary")
	resp, err := p.Client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("%w_unreachable", errAPI)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("%w_http_%d", errAPI, resp.StatusCode)
	}
	var got struct {
		Run struct {
			Status string `json:"status"`
		} `json:"run"`
		Events []struct {
			Type    string `json:"type"`
			StepID  string `json:"step_id"`
			Payload struct {
				Output any `json:"output"`
			} `json:"payload"`
		} `json:"events"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&got); err != nil {
		return "", nil, fmt.Errorf("%w_bad_json", errAPI)
	}
	var output any
	for _, e := range got.Events {
		if e.Type == "StepCompleted" && e.StepID == "out" {
			output = e.Payload.Output
		}
	}
	return got.Run.Status, output, nil
}

// Run probes every interval until ctx ends.
func (p *Prober) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		p.Probe(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
