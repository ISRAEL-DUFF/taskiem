package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// SelfHostedOptions configure a self-hosted open model behind an
// OpenAI-compatible chat completions endpoint (vLLM, llama.cpp server,
// Ollama, TGI): what tenants with strict residency require (spec 12.3).
type SelfHostedOptions struct {
	BaseURL    string // e.g. http://llm.internal:8000/v1
	Model      string
	APIKey     string // optional bearer token
	HTTPClient *http.Client
	// Retries is how many times a request is tried again after a rate limit
	// (429) or a server that is briefly unavailable (500, 502, 503, 504):
	// 3 by default, as hosted gateways such as OpenRouter answer 429 under
	// load; -1 tries once.
	Retries int
	// sleep waits between tries (tests replace it).
	sleep func(ctx context.Context, d time.Duration) error
}

// SelfHosted is the open-model provider. It speaks the de facto standard
// `POST {base}/chat/completions` with `response_format: json_schema`;
// servers without schema support still answer, and the builder validates
// the result like any other. Prompt caching, effort and refusal fallbacks
// are Anthropic features and are not sent.
type SelfHosted struct{ o SelfHostedOptions }

// NewSelfHosted returns an open-model provider.
func NewSelfHosted(o SelfHostedOptions) *SelfHosted {
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: 10 * time.Minute}
	}
	o.BaseURL = strings.TrimRight(o.BaseURL, "/")
	if o.Retries == 0 {
		o.Retries = 3
	}
	if o.sleep == nil {
		o.sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	return &SelfHosted{o}
}

// post sends a request, trying again after a rate limit or a briefly
// unavailable server, and returns the answer's body.
func (s *SelfHosted) post(ctx context.Context, raw []byte) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.o.BaseURL+"/chat/completions", bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		hreq.Header.Set("Content-Type", "application/json")
		if s.o.APIKey != "" {
			hreq.Header.Set("Authorization", "Bearer "+s.o.APIKey)
		}
		resp, err := s.o.HTTPClient.Do(hreq)
		if err != nil {
			return nil, fmt.Errorf("selfhosted: %w", err)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("selfhosted: %w", err)
		}
		if resp.StatusCode/100 == 2 {
			return data, nil
		}
		fail := fmt.Errorf("selfhosted: status %d%s", resp.StatusCode, providerMessage(data))
		if !retryable[resp.StatusCode] || attempt >= s.o.Retries {
			return nil, fail
		}
		if err := s.o.sleep(ctx, retryDelay(resp.Header.Get("Retry-After"), attempt)); err != nil {
			return nil, fail
		}
	}
}

// retryable are the statuses tried again: a rate limit, and a server
// briefly unavailable.
var retryable = map[int]bool{http.StatusTooManyRequests: true, http.StatusInternalServerError: true, http.StatusBadGateway: true,
	http.StatusServiceUnavailable: true, http.StatusGatewayTimeout: true}

// retryDelay is how long to wait before the next try: what the server
// asked for (Retry-After, in seconds, at most 30), else 1, 2, 4 seconds.
func retryDelay(header string, attempt int) time.Duration {
	if n, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && n >= 0 {
		return min(time.Duration(n)*time.Second, 30*time.Second)
	}
	return time.Second << min(attempt, 4)
}

// providerMessage is the error message in an OpenAI-compatible error body
// ({"error": {"message": ...}}), for the log: why the provider refused.
func providerMessage(data []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &e) != nil || strings.TrimSpace(e.Error.Message) == "" {
		return ""
	}
	msg := strings.Join(strings.Fields(e.Error.Message), " ")
	if r := []rune(msg); len(r) > 300 {
		msg = string(r[:300]) + "…"
	}
	return ": " + msg
}

func (s *SelfHosted) Name() string  { return "selfhosted" }
func (s *SelfHosted) Model() string { return s.o.Model }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (s *SelfHosted) Complete(ctx context.Context, req Request) (*Response, error) {
	var sys strings.Builder
	for _, b := range req.System {
		sys.WriteString(b.Text)
		sys.WriteString("\n\n")
	}
	msgs := []chatMessage{{Role: "system", Content: sys.String()}}
	for _, m := range req.Messages {
		msgs = append(msgs, chatMessage{Role: m.Role, Content: m.Text})
	}
	body := map[string]any{"model": s.o.Model, "messages": msgs}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.Schema != nil {
		body["response_format"] = map[string]any{"type": "json_schema",
			"json_schema": map[string]any{"name": "answer", "schema": req.Schema, "strict": true}}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	data, err := s.post(ctx, raw)
	if err != nil {
		return nil, err
	}
	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message      chatMessage `json:"message"`
			FinishReason string      `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			Prompt     int64 `json:"prompt_tokens"`
			Completion int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("selfhosted: %w", err)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("selfhosted: no choices in the answer")
	}
	r := &Response{
		Text:  out.Choices[0].Message.Content,
		Model: out.Model,
		Usage: Usage{InputTokens: out.Usage.Prompt, OutputTokens: out.Usage.Completion},
	}
	switch out.Choices[0].FinishReason {
	case "length":
		r.StopReason = StopMaxTokens
	case "content_filter":
		r.StopReason = StopRefusal
		r.Text = ""
	default:
		r.StopReason = "end_turn"
	}
	if r.Model == "" {
		r.Model = s.o.Model
	}
	if req.Schema != nil && r.StopReason == "end_turn" && json.Valid([]byte(r.Text)) {
		r.JSON = json.RawMessage(r.Text)
	}
	return r, nil
}
