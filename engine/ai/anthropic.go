package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
)

// AnthropicOptions configure the Claude provider.
type AnthropicOptions struct {
	APIKey  string // from ANTHROPIC_API_KEY; never stored anywhere else
	BaseURL string // optional: a gateway, or a test server
	Model   string // default claude-opus-5-5
	// Fallbacks sends `fallbacks: "default"` (beta
	// server-side-fallback-2026-07-01): a request a safety classifier
	// declines is re-served by Anthropic's recommended fallback model in
	// the same call, instead of returning a refusal.
	Fallbacks  bool
	HTTPClient *http.Client
}

// Anthropic is the default provider: Claude through the official Go SDK.
type Anthropic struct {
	client anthropic.Client
	model  string
	o      AnthropicOptions
}

// NewAnthropic returns a Claude provider.
func NewAnthropic(o AnthropicOptions) *Anthropic {
	if o.Model == "" {
		o.Model = DefaultModel
	}
	opts := []option.RequestOption{option.WithAPIKey(o.APIKey), option.WithMaxRetries(2)}
	if o.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(o.BaseURL))
	}
	if o.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(o.HTTPClient))
	}
	return &Anthropic{client: anthropic.NewClient(opts...), model: o.Model, o: o}
}

func (a *Anthropic) Name() string  { return "anthropic" }
func (a *Anthropic) Model() string { return a.model }

// streamAbove is the max_tokens beyond which requests stream: long
// non-streaming requests risk HTTP timeouts.
const streamAbove = 16000

// Params builds the Messages API request for req (exported for tests).
//
//   - System blocks keep their order; a block marked Cache gets an
//     ephemeral cache_control breakpoint, so the stable instructions and
//     the connector catalogue are a cached prefix.
//   - Thinking is left unset: Claude Opus 5.5 always thinks adaptively, and
//     depth is set with output_config.effort instead.
//   - A schema becomes output_config.format (structured output).
//   - Fallbacks add `fallbacks: "default"` with its beta header.
func (a *Anthropic) Params(req Request) anthropic.BetaMessageNewParams {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	p := anthropic.BetaMessageNewParams{
		Model:     a.model,
		MaxTokens: int64(maxTokens),
	}
	for _, b := range req.System {
		tb := anthropic.BetaTextBlockParam{Text: b.Text}
		if b.Cache {
			tb.CacheControl = anthropic.NewBetaCacheControlEphemeralParam()
		}
		p.System = append(p.System, tb)
	}
	for _, m := range req.Messages {
		block := anthropic.NewBetaTextBlock(m.Text)
		if m.Role == "assistant" {
			p.Messages = append(p.Messages, anthropic.BetaMessageParam{Role: anthropic.BetaMessageParamRoleAssistant, Content: []anthropic.BetaContentBlockParamUnion{block}})
		} else {
			p.Messages = append(p.Messages, anthropic.NewBetaUserMessage(block))
		}
	}
	if req.Effort != "" {
		p.OutputConfig.Effort = anthropic.BetaOutputConfigEffort(req.Effort)
	}
	if req.Schema != nil {
		p.OutputConfig.Format = anthropic.BetaJSONOutputFormatParam{Schema: req.Schema}
	}
	if a.o.Fallbacks {
		p.Fallbacks = anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()}
		p.Betas = append(p.Betas, anthropic.AnthropicBetaServerSideFallback2026_07_01)
	}
	return p
}

// Complete sends req to Claude.
func (a *Anthropic) Complete(ctx context.Context, req Request) (*Response, error) {
	p := a.Params(req)
	var msg *anthropic.BetaMessage
	if p.MaxTokens > streamAbove {
		stream := a.client.Beta.Messages.NewStreaming(ctx, p)
		acc := anthropic.BetaMessage{}
		for stream.Next() {
			if err := acc.Accumulate(stream.Current()); err != nil {
				_ = stream.Close()
				return nil, fmt.Errorf("anthropic: %w", err)
			}
		}
		if err := stream.Err(); err != nil {
			_ = stream.Close()
			return nil, fmt.Errorf("anthropic: %w", err)
		}
		_ = stream.Close()
		msg = &acc
	} else {
		m, err := a.client.Beta.Messages.New(ctx, p)
		if err != nil {
			return nil, fmt.Errorf("anthropic: %w", err)
		}
		msg = m
	}
	out := &Response{
		StopReason: string(msg.StopReason),
		Model:      msg.Model,
		Usage: Usage{
			InputTokens:         msg.Usage.InputTokens,
			OutputTokens:        msg.Usage.OutputTokens,
			CacheCreationTokens: msg.Usage.CacheCreationInputTokens,
			CacheReadTokens:     msg.Usage.CacheReadInputTokens,
		},
	}
	// Check the stop reason before reading content: a refusal may carry
	// partial output, which is discarded.
	if out.StopReason == StopRefusal {
		return out, nil
	}
	var text strings.Builder
	for _, block := range msg.Content {
		if tb, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			text.WriteString(tb.Text)
		}
	}
	out.Text = text.String()
	if req.Schema != nil && out.StopReason != StopMaxTokens && json.Valid([]byte(out.Text)) {
		out.JSON = json.RawMessage(out.Text)
	}
	return out, nil
}
