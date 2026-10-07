package httpsec

import (
	"net/http"
	"strings"
	"testing"
)

func TestFrameHeaders(t *testing.T) {
	h := http.Header{}
	Set(h)
	if !strings.Contains(h.Get("Content-Security-Policy"), "frame-ancestors 'none'") || h.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("defaults: %v", h)
	}
	SetFrame(h, []string{"https://a.example", "https://b.example"})
	csp := h.Get("Content-Security-Policy")
	if h.Get("X-Frame-Options") != "" || !strings.HasSuffix(csp, "frame-ancestors https://a.example https://b.example") ||
		!strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-eval") {
		t.Errorf("frame: %v", h)
	}
	if csp := FrameCSP(nil); !strings.HasSuffix(csp, "frame-ancestors 'none'") {
		t.Errorf("no ancestors: %s", csp)
	}
}
