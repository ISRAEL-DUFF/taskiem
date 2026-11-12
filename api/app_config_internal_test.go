package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/israel-duff/taskiem/engine/webauthn"
)

func TestAppConfig(t *testing.T) {
	read := func(s *Server) map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		s.appConfig(rec, httptest.NewRequest(http.MethodGet, "/v1/app/config", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	// Nothing configured: the first version, no passkeys, no signup.
	got := read(&Server{})
	if got["min_version"] != "0.1.0" || got["passkeys"] != false || got["signup"] != false {
		t.Fatalf("defaults: %v", got)
	}
	got = read(&Server{
		AppMinVersion: "0.4.2",
		AllowSignup:   true,
		WebAuthn:      webauthn.Config{RPID: "taskiem.test", Origins: []string{"https://taskiem.test"}},
	})
	if got["min_version"] != "0.4.2" || got["passkeys"] != true || got["signup"] != true {
		t.Fatalf("configured: %v", got)
	}
}
