package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/internal/schemacheck"
	"github.com/israel-duff/taskiem/engine/pii"
)

// AI steps (decision 0050, wd-v1 AI steps) ask the deployment's model at
// run time, on the "ai" queue. Before anything leaves, the worker checks
// that the business allows AI steps and has budget left, and swaps the
// run's personal data in the prompt and input for placeholders, except in
// the input fields the step lets through. The answer must match the
// step's output_schema; one that does not is sent back once with its
// problems. The placeholders in the answer are swapped back, and the
// output is sealed like any output that repeats personal data. Every call
// is an ai_interactions row, counted against the monthly AI budget.

// AISteps is how a worker runs ai steps.
type AISteps struct {
	// Config is the deployment's AI configuration: its default model, the
	// models steps may name, and what the provider retains.
	Config ai.Config
	// For returns the provider for a model; nil uses ai.New with the
	// model put in Config.
	For func(model string) (ai.Provider, error)
	// Overage reports a plan's AI overage rate: a tenant whose plan bills
	// tokens beyond the budget is not stopped by it. Nil: no overage.
	Overage func(ctx context.Context, tenant uuid.UUID) (int64, error)
	// MaxTokens bounds each answer; default 8192.
	MaxTokens int
	// Timeout bounds each call; default 2 minutes.
	Timeout time.Duration
}

const aiStepSystem = `You answer one question inside an automated business workflow. Answer with JSON matching the schema you were given, and nothing else.
Personal data in the question has been replaced by placeholders such as [[name_1]] or [[phone_2]]. The same placeholder always means the same value. Where your answer needs one of these values, write its placeholder exactly as given; never guess or invent the value behind it.`

func (a *AISteps) provider(model string) (ai.Provider, string, error) {
	m, ok := a.Config.StepModel(model)
	if !ok {
		return nil, "", fmt.Errorf("the model %s is not offered for AI steps on this deployment", model)
	}
	if a.For != nil {
		p, err := a.For(m)
		return p, m, err
	}
	c := a.Config
	c.Model = m
	p, err := ai.New(c)
	return p, m, err
}

// aiEvent runs an ai step's attempt.
func (w *Worker) aiEvent(ctx context.Context, p *plan, in any) history.Event {
	fail := func(kind, format string, args ...any) history.Event {
		return failed(p.c, kind, fmt.Sprintf(format, args...), "fail")
	}
	if p.step == nil || p.step.AI == nil {
		return fail("fatal", "the step has no ai configuration")
	}
	if w.AI == nil || w.AI.Config.Provider == "" {
		return fail("disabled", "AI steps are not set up on this deployment: it has no AI model")
	}
	cfg := p.step.AI
	var allowed bool
	var limit, used int64
	err := db.InTenantTx(ctx, w.Store.Pool, []uuid.UUID{p.c.tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT ai_steps FROM governance_settings), true)`).Scan(&allowed); err != nil {
			return err
		}
		lim, err := w.Store.limitsTx(ctx, tx, p.c.tenant)
		if err != nil {
			return err
		}
		limit = lim.AIMonthlyTokens
		used, err = AITokensThisMonth(ctx, tx, p.c.tenant)
		return err
	})
	if err != nil {
		return failed(p.c, "retryable", "reading the business's AI settings: "+err.Error(), "retry")
	}
	if !allowed {
		return fail("disabled", "the business has turned AI steps off (Secrets & settings, AI steps)")
	}
	if limit > 0 && used >= limit && w.AI.Overage != nil {
		if rate, err := w.AI.Overage(ctx, p.c.tenant); err == nil && rate > 0 {
			limit = 0
		}
	}
	if limit > 0 && used >= limit {
		return fail("limit", "this month's AI budget is used up (%d tokens); the step runs again once the budget allows", limit)
	}
	prov, model, err := w.AI.provider(cfg.Model)
	if err != nil || prov == nil {
		if err == nil {
			err = errors.New("no AI provider")
		}
		return fail("disabled", "%v", err)
	}

	// The question, with personal data masked.
	q, _ := in.(map[string]any)
	masker := pii.NewMasker(p.taint)
	prompt := masker.Mask(q["prompt"])
	input, _ := q["input"].(map[string]any)
	masked := map[string]any{}
	var through map[string]any
	for k, v := range input {
		if containsString(cfg.PersonalData, k) {
			if through == nil {
				through = map[string]any{}
			}
			through[k] = v
			continue
		}
		masked[k] = v
	}
	maskedIn, _ := masker.Mask(masked).(map[string]any)
	// The record of the call is built from the same masked question, so it
	// shows what the model was sent; only the fields let through are
	// redacted in it.
	logIn := make(map[string]any, len(maskedIn)+len(through))
	for k, v := range maskedIn {
		logIn[k] = v
	}
	for k, v := range through {
		maskedIn[k] = v
		logIn[k] = redactThrough(p.taint, v)
	}
	question, logged := aiQuestion(prompt, maskedIn), aiQuestion(prompt, logIn)
	var schema map[string]any
	if err := json.Unmarshal(cfg.OutputSchema, &schema); err != nil {
		return fail("fatal", "output_schema: %v", err)
	}
	check := schemacheck.New("https://schemas.taskiem.dev/ai-steps/"+p.c.run.String()+"/"+p.c.step+".json", cfg.OutputSchema)
	maxTokens, timeout := w.AI.MaxTokens, w.AI.Timeout
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	req := ai.Request{
		System:    []ai.SystemBlock{{Text: aiStepSystem, Cache: true}},
		Messages:  []ai.Message{{Role: "user", Text: question}},
		Schema:    closeObjects(schema),
		MaxTokens: maxTokens,
	}
	sent := []ai.Message{{Role: "user", Text: logged}}
	sum := sha256.Sum256([]byte(aiStepSystem))
	digest := hex.EncodeToString(sum[:])

	for round := 1; round <= 2; round++ {
		cctx, cancel := context.WithTimeout(ctx, timeout)
		resp, err := prov.Complete(cctx, req)
		cancel()
		outcome, problems := "valid", []string(nil)
		var answer any
		switch {
		case err != nil:
			outcome = "error"
		case resp.StopReason == ai.StopRefusal:
			outcome = "refused"
		case resp.StopReason == ai.StopMaxTokens:
			outcome = "truncated"
		default:
			raw := resp.JSON
			if raw == nil {
				raw = json.RawMessage(strings.TrimSpace(resp.Text))
			}
			if !json.Valid(raw) {
				outcome, problems = "unparseable", []string{"the answer is not JSON"}
			} else if problems = check.Validate(raw); len(problems) > 0 {
				outcome = "problems"
			} else {
				answer, _ = decodeAnswer(raw)
			}
		}
		w.recordAIStep(ctx, p, model, prov.Name(), digest, round, sent, resp, outcome, err)
		switch outcome {
		case "valid":
			out := masker.Unmask(answer)
			// What was put back is personal: the output carrying it is
			// sealed whole when it is written.
			if p.taint == nil {
				p.taint = pii.Taint{}
			}
			for k, cat := range masker.Restored() {
				p.taint[k] = cat
			}
			return completedEvent(p.c, out, false)
		case "error":
			if ai.Transient(err) {
				return failed(p.c, "retryable", "the AI model could not be reached: "+pii.Redact(err.Error()), "retry")
			}
			return fail("fatal", "the AI model refused the request: %s", pii.Redact(err.Error()))
		case "refused":
			return fail("refused", "the AI model declined to answer")
		case "truncated":
			return fail("invalid_output", "the AI model's answer was longer than %d tokens", maxTokens)
		}
		if round == 2 {
			return fail("invalid_output", "the AI model's answer did not match the output schema: %s", strings.Join(problems, "; "))
		}
		again := ai.Message{Role: "user", Text: "That answer does not match the schema: " + strings.Join(problems, "; ") + ". Answer again with JSON that matches it."}
		req.Messages = append(req.Messages, ai.Message{Role: "assistant", Text: resp.Text}, again)
		sent = append(sent, ai.Message{Role: "assistant", Text: redactAnswer(p.taint, resp.Text)}, again)
	}
	return fail("invalid_output", "no answer")
}

// aiQuestion is the question an ai step sends: its prompt, then its input
// as JSON.
func aiQuestion(prompt any, input map[string]any) string {
	q := fmt.Sprint(prompt)
	if s, ok := prompt.(string); ok {
		q = s
	}
	if len(input) > 0 {
		raw, _ := json.MarshalIndent(input, "", "  ")
		q += "\n\nInput (JSON):\n" + string(raw)
	}
	return q
}

// redactThrough is an input field let through to the model, as the record
// of the call keeps it: its personal values replaced by their categories.
func redactThrough(taint pii.Taint, v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return "[personal data]"
	}
	s := taint.RedactText(pii.Redact(string(raw)))
	if json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	return s
}

// redactAnswer is a model's answer as its record keeps it: it may repeat
// the fields let through.
func redactAnswer(taint pii.Taint, s string) string { return taint.RedactText(pii.Redact(s)) }

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func decodeAnswer(raw json.RawMessage) (any, error) {
	var v any
	err := json.Unmarshal(raw, &v)
	return normalizeNumbers(v), err
}

// normalizeNumbers makes whole JSON numbers int64, as history does.
func normalizeNumbers(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			t[k] = normalizeNumbers(x)
		}
	case []any:
		for i, x := range t {
			t[i] = normalizeNumbers(x)
		}
	case float64:
		if t == float64(int64(t)) && t < 1<<53 && t > -(1<<53) {
			return int64(t)
		}
	}
	return v
}

// closeObjects is the schema as structured output needs it: every object
// closed to properties it does not name. The author's schema is still
// what the answer is checked against.
func closeObjects(schema map[string]any) map[string]any {
	raw, _ := json.Marshal(schema)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if t["type"] == "object" {
				if _, set := t["additionalProperties"]; !set {
					t["additionalProperties"] = false
				}
			}
			for _, x := range t {
				walk(x)
			}
		case []any:
			for _, x := range t {
				walk(x)
			}
		}
	}
	walk(out)
	return out
}

// recordAIStep writes one call of an ai step to ai_interactions, where it
// counts against the monthly budget: the messages as they left, built from
// the same masked question (only the input fields let through are
// redacted, so the record never holds them in the clear), the answer, and
// the tokens.
func (w *Worker) recordAIStep(ctx context.Context, p *plan, model, provider, digest string, round int, msgs []ai.Message, resp *ai.Response, outcome string, callErr error) {
	ctx = context.WithoutCancel(ctx)
	rawMsgs, _ := json.Marshal(msgs)
	var text *string
	var stop string
	var u ai.Usage
	if resp != nil {
		t := redactAnswer(p.taint, resp.Text)
		text, stop, u = &t, resp.StopReason, resp.Usage
		if resp.Model != "" {
			model = resp.Model
		}
	}
	errText := ""
	if callErr != nil {
		errText = pii.Redact(callErr.Error())
	}
	kind := "step"
	if round > 1 {
		kind = "step_correct"
	}
	err := db.InTenantTx(ctx, w.Store.Pool, []uuid.UUID{p.c.tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO ai_interactions (id, tenant_id, run_id, step_id, round, kind, actor, provider, model, system_digest, messages,
			response, stop_reason, outcome, error, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, total_tokens)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NULLIF($13, ''), $14, NULLIF($15, ''), $16, $17, $18, $19, $20)`,
			uuid.Must(uuid.NewV7()), p.c.tenant, p.c.run, p.c.step, round, kind, "run:"+p.c.run.String(), provider, model, digest, rawMsgs,
			text, stop, outcome, errText, u.InputTokens, u.OutputTokens, u.CacheCreationTokens, u.CacheReadTokens, u.Total())
		return err
	})
	if err != nil {
		w.Logger.Error("recording an ai step's call", "run", p.c.run, "step", p.c.step, "err", err)
	}
}
