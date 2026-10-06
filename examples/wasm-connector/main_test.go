package main

import (
	"encoding/json"
	"strings"
	"testing"

	tc "github.com/israel-duff/taskiem/sdk/connectorsdk"
)

// Handlers run natively under go test; connectorsdk.TestHTTP plays the
// provider.
func TestCreatePaymentReportsARepeat(t *testing.T) {
	seen := map[string]bool{}
	tc.TestHTTP = func(r tc.HTTPRequest) (*tc.HTTPResponse, error) {
		if r.Headers["Authorization"] != "Bearer k" {
			return &tc.HTTPResponse{Status: 401}, nil
		}
		if r.Method == "POST" {
			var b map[string]any
			_ = json.Unmarshal(r.Body, &b)
			ref := b["reference"].(string)
			if seen[ref] {
				return &tc.HTTPResponse{Status: 409}, nil
			}
			seen[ref] = true
			return &tc.HTTPResponse{Status: 201, Body: []byte(`{"id":"p1","reference":"` + ref + `","status":"pending"}`)}, nil
		}
		return &tc.HTTPResponse{Status: 200, Body: []byte(`{"id":"p1","reference":"tsk_1","status":"completed"}`)}, nil
	}
	defer func() { tc.TestHTTP = nil }()
	call := `{"action":"create_payment","base_url":"https://ledger.test","credentials":{"api_key":"k"},
	  "input":{"amount":100,"account_number":"0123456789","reference":"tsk_1"}}`
	if out := string(tc.Run([]byte(call))); !strings.Contains(out, `"status":"pending"`) {
		t.Fatalf("first: %s", out)
	}
	if out := string(tc.Run([]byte(call))); !strings.Contains(out, `"status":"completed"`) {
		t.Fatalf("repeat: %s", out)
	}
	bad := `{"action":"create_payment","credentials":{"api_key":"wrong"},"input":{"reference":"tsk_2"}}`
	if out := string(tc.Run([]byte(bad))); !strings.Contains(out, `"kind":"fatal"`) {
		t.Errorf("401: %s", out)
	}
	if out := string(tc.Run([]byte(`{"action":"nope"}`))); !strings.Contains(out, "no handler") {
		t.Errorf("unknown action: %s", out)
	}
}
