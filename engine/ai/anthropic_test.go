package ai_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/israel-duff/taskiem/engine/ai"
)

// fakeAPI is a stand-in for the Messages API: it records request bodies and
// headers, and answers with a canned message (JSON or a stream).
type fakeAPI struct {
	mu      sync.Mutex
	bodies  []map[string]any
	headers []http.Header
	answer  string // text content
	stop    string
}

func (f *fakeAPI) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path %s", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("body: %v", err)
		}
		f.mu.Lock()
		f.bodies = append(f.bodies, body)
		f.headers = append(f.headers, r.Header.Clone())
		f.mu.Unlock()
		stop := f.stop
		if stop == "" {
			stop = "end_turn"
		}
		usage := `{"input_tokens":120,"output_tokens":40,"cache_creation_input_tokens":900,"cache_read_input_tokens":3000}`
		text, _ := json.Marshal(f.answer)
		if body["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			ev := func(name, data string) { _, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data) }
			ev("message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],"stop_reason":null,"usage":`+usage+`}}`)
			ev("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
			ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":`+string(text)+`}}`)
			ev("content_block_stop", `{"type":"content_block_stop","index":0}`)
			ev("message_delta", `{"type":"message_delta","delta":{"stop_reason":"`+stop+`"},"usage":{"output_tokens":40}}`)
			ev("message_stop", `{"type":"message_stop"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		content := `[{"type":"thinking","thinking":"","signature":"sig"},{"type":"text","text":` + string(text) + `}]`
		_, _ = fmt.Fprintf(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":%s,"stop_reason":%q,"usage":%s}`, content, stop, usage)
	}
}

func schema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "required": []any{"summary"},
		"properties": map[string]any{"summary": map[string]any{"type": "string"}}}
}

func TestAnthropicRequestShape(t *testing.T) {
	api := &fakeAPI{answer: `{"summary":"ok"}`}
	ts := httptest.NewServer(api.handler(t))
	defer ts.Close()
	p := ai.NewAnthropic(ai.AnthropicOptions{APIKey: "test-key", BaseURL: ts.URL, Fallbacks: true})
	resp, err := p.Complete(context.Background(), ai.Request{
		System:    []ai.SystemBlock{{Text: "instructions", Cache: true}, {Text: "catalogue", Cache: true}},
		Messages:  []ai.Message{{Role: "user", Text: "build it"}, {Role: "assistant", Text: "{}"}, {Role: "user", Text: "fix it"}},
		Schema:    schema(),
		MaxTokens: 8000,
		Effort:    "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.JSON) != `{"summary":"ok"}` || resp.Model != "claude-opus-5-5" || resp.StopReason != "end_turn" {
		t.Fatalf("response: %+v", resp)
	}
	if resp.Usage != (ai.Usage{InputTokens: 120, OutputTokens: 40, CacheCreationTokens: 900, CacheReadTokens: 3000}) {
		t.Errorf("usage: %+v", resp.Usage)
	}
	b := api.bodies[0]
	if b["model"] != "claude-opus-5-5" || b["max_tokens"] != float64(8000) {
		t.Errorf("model/max_tokens: %v %v", b["model"], b["max_tokens"])
	}
	if _, ok := b["thinking"]; ok {
		t.Errorf("thinking must be left unset on Opus 5.5: %v", b["thinking"])
	}
	if b["fallbacks"] != "default" {
		t.Errorf("fallbacks: %v", b["fallbacks"])
	}
	if !strings.Contains(api.headers[0].Get("Anthropic-Beta"), "server-side-fallback-2026-07-01") {
		t.Errorf("beta header: %q", api.headers[0].Get("Anthropic-Beta"))
	}
	if api.headers[0].Get("X-Api-Key") != "test-key" {
		t.Errorf("api key header missing")
	}
	oc, _ := b["output_config"].(map[string]any)
	if oc["effort"] != "high" {
		t.Errorf("effort: %v", oc)
	}
	format, _ := oc["format"].(map[string]any)
	if format["type"] != "json_schema" || format["schema"] == nil {
		t.Errorf("format: %v", format)
	}
	sys, _ := b["system"].([]any)
	if len(sys) != 2 {
		t.Fatalf("system: %v", b["system"])
	}
	for i, s := range sys {
		cc, _ := s.(map[string]any)["cache_control"].(map[string]any)
		if cc["type"] != "ephemeral" {
			t.Errorf("system[%d] cache_control: %v", i, s)
		}
	}
	msgs, _ := b["messages"].([]any)
	if len(msgs) != 3 || msgs[1].(map[string]any)["role"] != "assistant" {
		t.Errorf("messages: %v", msgs)
	}
	if b["stream"] == true {
		t.Errorf("small requests do not stream")
	}
}

func TestAnthropicStreamsLargeRequestsAndHandlesRefusal(t *testing.T) {
	api := &fakeAPI{answer: `partial`, stop: "refusal"}
	ts := httptest.NewServer(api.handler(t))
	defer ts.Close()
	p := ai.NewAnthropic(ai.AnthropicOptions{APIKey: "k", BaseURL: ts.URL})
	resp, err := p.Complete(context.Background(), ai.Request{Messages: []ai.Message{{Role: "user", Text: "x"}}, Schema: schema(), MaxTokens: 32000, Effort: "high"})
	if err != nil {
		t.Fatal(err)
	}
	if api.bodies[0]["stream"] != true {
		t.Errorf("large requests stream")
	}
	if _, ok := api.bodies[0]["fallbacks"]; ok {
		t.Errorf("fallbacks off: %v", api.bodies[0]["fallbacks"])
	}
	if resp.StopReason != ai.StopRefusal || resp.Text != "" || resp.JSON != nil {
		t.Errorf("a refusal's partial output is discarded: %+v", resp)
	}
	if resp.Usage.InputTokens != 120 || resp.Usage.OutputTokens != 40 {
		t.Errorf("usage: %+v", resp.Usage)
	}
}

func TestSelfHosted(t *testing.T) {
	var got map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("request: %s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = io.WriteString(w, `{"model":"llama","choices":[{"message":{"role":"assistant","content":"{\"summary\":\"x\"}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
	}))
	defer ts.Close()
	p := ai.NewSelfHosted(ai.SelfHostedOptions{BaseURL: ts.URL + "/v1/", Model: "llama", APIKey: "tok"})
	resp, err := p.Complete(context.Background(), ai.Request{System: []ai.SystemBlock{{Text: "s"}}, Messages: []ai.Message{{Role: "user", Text: "u"}}, Schema: schema(), MaxTokens: 2000})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.JSON) != `{"summary":"x"}` || resp.Usage.Total() != 15 {
		t.Errorf("response: %+v", resp)
	}
	if rf, _ := got["response_format"].(map[string]any); rf["type"] != "json_schema" {
		t.Errorf("response_format: %v", got["response_format"])
	}
}

func TestRedactedMasksPersonalData(t *testing.T) {
	f := &ai.Fake{}
	p := ai.Redacted(f)
	_, err := p.Complete(context.Background(), ai.Request{
		System:   []ai.SystemBlock{{Text: "contact ada@example.com"}},
		Messages: []ai.Message{{Role: "user", Text: "pay BVN 22123456789 and call 08031234567"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	sent := ai.PromptText(f.Requests()[0])
	for _, leak := range []string{"ada@example.com", "22123456789", "08031234567"} {
		if strings.Contains(sent, leak) {
			t.Errorf("%q reached the provider: %s", leak, sent)
		}
	}
	if !strings.Contains(sent, "[bvn]") || !strings.Contains(sent, "[email]") {
		t.Errorf("not masked: %s", sent)
	}
}

func TestConfigFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) (string, bool) {
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	c, err := ai.ConfigFromEnv(env(nil))
	if err != nil || c.Provider != "" {
		t.Fatalf("no key: %+v %v", c, err)
	}
	c, err = ai.ConfigFromEnv(env(map[string]string{"ANTHROPIC_API_KEY": "k"}))
	if err != nil || c.Provider != "anthropic" || c.Model != "claude-opus-5-5" || c.Effort != "high" || !c.Fallbacks {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	c, err = ai.ConfigFromEnv(env(map[string]string{"ANTHROPIC_API_KEY": "k", "TASKIEM_AI_MODEL": "claude-sonnet-5-5", "TASKIEM_AI_EFFORT": "medium", "TASKIEM_AI_FALLBACKS": "off"}))
	if err != nil || c.Model != "claude-sonnet-5-5" || c.Effort != "medium" || c.Fallbacks {
		t.Fatalf("overrides: %+v %v", c, err)
	}
	if _, err := ai.ConfigFromEnv(env(map[string]string{"TASKIEM_AI_PROVIDER": "anthropic"})); err == nil {
		t.Error("anthropic without a key")
	}
	if _, err := ai.ConfigFromEnv(env(map[string]string{"TASKIEM_AI_PROVIDER": "selfhosted"})); err == nil {
		t.Error("selfhosted without a base URL")
	}
	if _, err := ai.ConfigFromEnv(env(map[string]string{"ANTHROPIC_API_KEY": "k", "TASKIEM_AI_EFFORT": "huge"})); err == nil {
		t.Error("bad effort")
	}
}
