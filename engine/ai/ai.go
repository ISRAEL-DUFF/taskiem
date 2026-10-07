// Package ai is Taskiem's model layer (spec 12.3): one small interface in
// front of hosted frontier models (Anthropic's Claude by default) and
// self-hosted open models, so the builder (engine/ai/builder) never talks
// to a vendor SDK directly.
//
// The layer is deliberately powerless. It turns text into text: it holds
// no vault, no database handle, no connector handlers and no principal,
// and its import graph is tested to exclude the secrets vault, the
// runtime's workers, approvals and the API (see imports_test.go). Every
// request passes through Redacted, which masks personal data before
// anything leaves the process.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/israel-duff/taskiem/engine/pii"
)

// Message is one turn of a conversation.
type Message struct {
	Role string `json:"role"` // "user" | "assistant"
	Text string `json:"text"`
}

// SystemBlock is part of the system prompt. Cache marks the end of a
// prefix worth caching (the stable instructions, the connector catalogue).
type SystemBlock struct {
	Text  string `json:"text"`
	Cache bool   `json:"cache,omitempty"`
}

// Request is one completion.
type Request struct {
	System   []SystemBlock
	Messages []Message
	// Schema, when set, constrains the answer to JSON matching it
	// (structured output); Response.JSON then holds it.
	Schema map[string]any
	// MaxTokens bounds the answer (thinking included); 0 is the provider's
	// default.
	MaxTokens int
	// Effort is the thinking depth: low, medium, high, xhigh or max.
	// Providers without the notion ignore it.
	Effort string
}

// Usage is what one completion consumed.
type Usage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens"`
	CacheReadTokens     int64 `json:"cache_read_tokens"`
}

// Total is what counts against a tenant's monthly budget: every token
// processed, cache reads included (a conservative count).
func (u Usage) Total() int64 {
	return u.InputTokens + u.OutputTokens + u.CacheCreationTokens + u.CacheReadTokens
}

// Add sums usage.
func (u Usage) Add(o Usage) Usage {
	return Usage{u.InputTokens + o.InputTokens, u.OutputTokens + o.OutputTokens,
		u.CacheCreationTokens + o.CacheCreationTokens, u.CacheReadTokens + o.CacheReadTokens}
}

// Stop reasons a caller acts on. Others ("end_turn") mean a full answer.
const (
	StopRefusal   = "refusal"
	StopMaxTokens = "max_tokens"
)

// Response is a completion's result.
type Response struct {
	Text       string          // the answer's text
	JSON       json.RawMessage // the answer, when Request.Schema was set and it parsed
	Usage      Usage
	StopReason string
	Model      string // the model that answered (a fallback model, after a refusal)
}

// Provider completes requests. Implementations must not retain requests.
type Provider interface {
	// Name is the provider's kind ("anthropic", "selfhosted", "fake").
	Name() string
	// Model is the configured model id.
	Model() string
	Complete(ctx context.Context, req Request) (*Response, error)
}

var (
	// ErrNotConfigured means no provider is configured on this deployment.
	ErrNotConfigured = errors.New("ai: no model provider is configured")
	// ErrBudgetExhausted means the tenant's monthly AI budget is spent.
	ErrBudgetExhausted = errors.New("ai: this month's AI budget is used up")
)

// Redacted wraps p so every prompt passes through the PII redactor
// (engine/pii) before it leaves: system blocks and every message. The
// builder wraps its provider with it itself, so no caller can forget.
func Redacted(p Provider) Provider {
	if r, ok := p.(redacted); ok {
		return r
	}
	return redacted{p}
}

type redacted struct{ p Provider }

func (r redacted) Name() string  { return r.p.Name() }
func (r redacted) Model() string { return r.p.Model() }

func (r redacted) Complete(ctx context.Context, req Request) (*Response, error) {
	return r.p.Complete(ctx, RedactRequest(req))
}

// RedactRequest masks personal data in every text a request carries.
func RedactRequest(req Request) Request {
	out := req
	out.System = make([]SystemBlock, len(req.System))
	for i, b := range req.System {
		out.System[i] = SystemBlock{Text: pii.Redact(b.Text), Cache: b.Cache}
	}
	out.Messages = make([]Message, len(req.Messages))
	for i, m := range req.Messages {
		out.Messages[i] = Message{Role: m.Role, Text: pii.Redact(m.Text)}
	}
	return out
}

// Config selects and configures a provider (docs/ai.md).
type Config struct {
	// Provider: "anthropic", "selfhosted", "fake" (tests and evaluation
	// only), or "" for none (AI building is off).
	Provider string
	Model    string
	// BaseURL overrides the provider's endpoint: required for
	// "selfhosted" (an OpenAI-compatible server), optional for
	// "anthropic" (a gateway).
	BaseURL string
	// APIKey authenticates to the provider. For "anthropic" it comes from
	// ANTHROPIC_API_KEY only.
	APIKey string
	// Effort is the default thinking depth for drafting.
	Effort string
	// MaxTokens bounds each answer.
	MaxTokens int
	// Fallbacks opts Anthropic requests into server-side refusal
	// fallbacks ("default"); on unless TASKIEM_AI_FALLBACKS=off.
	Fallbacks bool
}

// Defaults.
const (
	DefaultModel     = "claude-opus-5-5"
	DefaultEffort    = "high"
	DefaultMaxTokens = 32000
)

// ConfigFromEnv reads TASKIEM_AI_* (and ANTHROPIC_API_KEY). Without
// TASKIEM_AI_PROVIDER, Anthropic is used when ANTHROPIC_API_KEY is set,
// and AI building is off otherwise.
func ConfigFromEnv(lookup func(string) (string, bool)) (Config, error) {
	get := func(k string) string { v, _ := lookup(k); return strings.TrimSpace(v) }
	c := Config{
		Provider:  strings.ToLower(get("TASKIEM_AI_PROVIDER")),
		Model:     get("TASKIEM_AI_MODEL"),
		BaseURL:   get("TASKIEM_AI_BASE_URL"),
		Effort:    get("TASKIEM_AI_EFFORT"),
		MaxTokens: DefaultMaxTokens,
		Fallbacks: !strings.EqualFold(get("TASKIEM_AI_FALLBACKS"), "off"),
	}
	key := get("ANTHROPIC_API_KEY")
	if c.Provider == "" && key != "" {
		c.Provider = "anthropic"
	}
	switch c.Provider {
	case "", "off", "none":
		c.Provider = ""
		return c, nil
	case "anthropic":
		c.APIKey = key
		if c.APIKey == "" {
			return c, fmt.Errorf("TASKIEM_AI_PROVIDER=anthropic needs ANTHROPIC_API_KEY")
		}
		if c.Model == "" {
			c.Model = DefaultModel
		}
	case "selfhosted":
		c.APIKey = get("TASKIEM_AI_API_KEY")
		if c.BaseURL == "" || c.Model == "" {
			return c, fmt.Errorf("TASKIEM_AI_PROVIDER=selfhosted needs TASKIEM_AI_BASE_URL and TASKIEM_AI_MODEL")
		}
	case "fake":
	default:
		return c, fmt.Errorf("TASKIEM_AI_PROVIDER: unknown provider %q (anthropic, selfhosted)", c.Provider)
	}
	if c.Effort == "" {
		c.Effort = DefaultEffort
	}
	switch c.Effort {
	case "low", "medium", "high", "xhigh", "max":
	default:
		return c, fmt.Errorf("TASKIEM_AI_EFFORT: %q is not low, medium, high, xhigh or max", c.Effort)
	}
	if s := get("TASKIEM_AI_MAX_TOKENS"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1024 || n > 128000 {
			return c, fmt.Errorf("TASKIEM_AI_MAX_TOKENS: %q is not between 1024 and 128000", s)
		}
		c.MaxTokens = n
	}
	return c, nil
}

// New returns the provider c describes, or nil when AI is off.
func New(c Config) (Provider, error) {
	switch c.Provider {
	case "":
		return nil, nil
	case "anthropic":
		return NewAnthropic(AnthropicOptions{APIKey: c.APIKey, BaseURL: c.BaseURL, Model: c.Model, Fallbacks: c.Fallbacks}), nil
	case "selfhosted":
		return NewSelfHosted(SelfHostedOptions{BaseURL: c.BaseURL, Model: c.Model, APIKey: c.APIKey}), nil
	case "fake":
		return &Fake{}, nil
	}
	return nil, fmt.Errorf("ai: unknown provider %q", c.Provider)
}

// Fake is a deterministic provider for tests and offline evaluation. It
// answers with Respond, or else the next of Script (the last repeats), and
// records every request it receives (after redaction, when wrapped).
type Fake struct {
	Respond func(req Request) (*Response, error)
	Script  []Response
	Models  string // reported model; "fake" by default

	mu       sync.Mutex
	requests []Request
	n        int
}

func (f *Fake) Name() string { return "fake" }

func (f *Fake) Model() string {
	if f.Models != "" {
		return f.Models
	}
	return "fake"
}

func (f *Fake) Complete(ctx context.Context, req Request) (*Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.requests = append(f.requests, req)
	i := f.n
	f.n++
	f.mu.Unlock()
	var resp *Response
	switch {
	case f.Respond != nil:
		r, err := f.Respond(req)
		if err != nil {
			return nil, err
		}
		resp = r
	case len(f.Script) > 0:
		r := f.Script[min(i, len(f.Script)-1)]
		resp = &r
	default:
		resp = &Response{Text: "{}"}
	}
	out := *resp
	if out.Model == "" {
		out.Model = f.Model()
	}
	if out.StopReason == "" {
		out.StopReason = "end_turn"
	}
	if out.Usage == (Usage{}) {
		out.Usage = Usage{InputTokens: int64(len(promptText(req)) / 4), OutputTokens: int64(len(out.Text) / 4)}
	}
	if req.Schema != nil && out.JSON == nil && out.StopReason != StopRefusal && json.Valid([]byte(out.Text)) {
		out.JSON = json.RawMessage(out.Text)
	}
	return &out, nil
}

// Requests returns what the fake received.
func (f *Fake) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.requests...)
}

// PromptText is everything a request sends, for tests and logs.
func PromptText(req Request) string { return promptText(req) }

func promptText(req Request) string {
	var b strings.Builder
	for _, s := range req.System {
		b.WriteString(s.Text)
		b.WriteByte('\n')
	}
	for _, m := range req.Messages {
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(m.Text)
		b.WriteByte('\n')
	}
	return b.String()
}
