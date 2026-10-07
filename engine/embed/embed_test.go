package embed

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

func TestUses(t *testing.T) {
	doc := `{"schema":"wd/v1","trigger":{"type":"webhook","config":{"connector":"paystack@2"}},
	  "types":{"T":{"type":"object","properties":{"connector":{"type":"string"},"x":{"id":"y","type":"code"}}}},
	  "steps":[
	    {"id":"a","type":"connector","connector":"fakepay@1","action":"notify","compensate":{"action":"refund"}},
	    {"id":"b","type":"parallel","config":{"branches":[{"name":"x","steps":[{"id":"c","type":"http","config":{"url":"https://x"}}]}]}},
	    {"id":"d","type":"foreach","config":{"items":"=[]","steps":[{"id":"e","type":"code","config":{"language":"javascript","source":"1"}}]}},
	    {"id":"f","type":"transform","on_error":{"steps":[{"id":"g","type":"ai","config":{}}]}}]}`
	got, err := Uses([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	// Schemas in types are not steps: their "connector" or "code" is data.
	if want := []string{"ai", "code", "fakepay", "http", "paystack"}; !slices.Equal(got, want) {
		t.Errorf("Uses = %v, want %v", got, want)
	}
	if bad := Disallowed(got, []string{"fakepay", "http"}); !slices.Equal(bad, []string{"ai", "code", "paystack"}) {
		t.Errorf("Disallowed = %v", bad)
	}
	if _, err := Uses([]byte(`[1]`)); err == nil {
		t.Error("a non-object definition was read")
	}
}

func TestOrigin(t *testing.T) {
	for in, want := range map[string]string{
		"https://App.Example.com":       "https://app.example.com",
		"https://app.example.com:8443/": "https://app.example.com:8443",
		"http://localhost:5173":         "http://localhost:5173",
		"http://127.0.0.1:3000":         "http://127.0.0.1:3000",
	} {
		if got, err := Origin(in); err != nil || got != want {
			t.Errorf("Origin(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "*", "https://*.example.com", "http://example.com", "https://example.com/path", "https://u:p@example.com",
		"https://example.com?x=1", "javascript:alert(1)", "example.com", "null"} {
		if got, err := Origin(bad); err == nil {
			t.Errorf("Origin(%q) = %q, want an error", bad, got)
		}
	}
}

func TestBranding(t *testing.T) {
	ok := `{"colours":{"primary":"#0a7","background":"#ffffffcc"},"font_family":"Inter, sans-serif","logo_url":"https://cdn.example.com/l.svg","radius":"8px","mode":"dark"}`
	if _, err := ParseBranding(json.RawMessage(ok)); err != nil {
		t.Errorf("valid branding: %v", err)
	}
	for _, bad := range []string{
		`{"colours":{"primary":"red"}}`,
		`{"colours":{"primary":"#fff;background:url(x)"}}`,
		`{"colours":{"evil":"#fff"}}`,
		`{"font_family":"Inter\"; } body { display:none"}`,
		`{"logo_url":"http://cdn.example.com/l.svg"}`,
		`{"logo_url":"javascript:alert(1)"}`,
		`{"radius":"calc(1px)"}`,
		`{"mode":"neon"}`,
		`{"css":"body{}"}`,
	} {
		if _, err := ParseBranding(json.RawMessage(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestBackoff(t *testing.T) {
	if Backoff(1) != 30*time.Second || Backoff(2) != 2*time.Minute || Backoff(MaxAttempts) != 6*time.Hour || Backoff(40) != 6*time.Hour {
		t.Errorf("backoff: %v %v %v", Backoff(1), Backoff(2), Backoff(MaxAttempts))
	}
	var total time.Duration
	for i := 1; i < MaxAttempts; i++ {
		total += Backoff(i)
	}
	if total < 12*time.Hour || total > 36*time.Hour {
		t.Errorf("retries span %v", total)
	}
}
