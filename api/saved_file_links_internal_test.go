package api

import "testing"

// A saved file link's token never reaches a log line (H6 review).
func TestLoggedPath(t *testing.T) {
	for in, want := range map[string]string{
		"/v1/saved-files/abcDEF123":    "/v1/saved-files/•",
		"/v1/saved-files/":             "/v1/saved-files/",
		"/v1/runs/x":                   "/v1/runs/x",
		"/v1/pay-receipts/abc.def/pdf": "/v1/pay-receipts/•",
	} {
		if got := loggedPath(in); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
}
