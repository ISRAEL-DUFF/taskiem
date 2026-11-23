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

// Only the photo reader's files may compile WebAssembly: the app's own
// pages may not.
func TestOCRPolicy(t *testing.T) {
	if strings.Contains(CSP, "wasm") || strings.Contains(AppCSP([]string{"https://forms.example"}), "wasm") {
		t.Errorf("the app's policy compiles WebAssembly: %s", CSP)
	}
	if !strings.Contains(OCRCSP, "script-src 'self' 'wasm-unsafe-eval';") || strings.Contains(OCRCSP, "'unsafe-eval'") {
		t.Errorf("the reader's policy: %s", OCRCSP)
	}
	if strings.Replace(OCRCSP, " 'wasm-unsafe-eval'", "", 1) != CSP {
		t.Errorf("the reader's policy is the app's with WebAssembly: %s", OCRCSP)
	}
}
