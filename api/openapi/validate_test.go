package openapi

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateResponse(t *testing.T) {
	v, err := NewValidator()
	if err != nil {
		t.Fatal(err)
	}
	const wf = `{"workflows":[{"id":"0190a5d2-7e1c-7f00-8000-000000000001","name":"loan","active_version":null,"latest_version":1,
		"created_at":"2026-10-07T10:00:00Z","key":"wf_loan","git_path":null}]}`
	cases := []struct {
		method, path string
		status       int
		ctype, body  string
		fails        string // "" passes
	}{
		{"GET", "/v1/workflows", 200, "application/json", wf, ""},
		{"GET", "/v1/workflows", 200, "application/json", `{"workflows":[{"id":"x"}]}`, "missing propert"},
		{"GET", "/v1/workflows", 200, "application/json", `{"workflows":[]}`, ""},
		{"GET", "/v1/workflows", 200, "text/plain", `hi`, "media type"},
		{"GET", "/v1/workflows", 201, "application/json", `{}`, "status not declared"},
		{"GET", "/v1/workflows", 403, "application/json", `{"error":"requires workflow.read"}`, ""},
		{"GET", "/v1/workflows", 403, "application/json", `{"message":"no"}`, "missing propert"},
		{"POST", "/v1/workflows/{wf}/runs", 201, "application/json",
			`{"run_id":"0190a5d2-7e1c-7f00-8000-000000000002","workflow_id":"0190a5d2-7e1c-7f00-8000-000000000001","version":1,"environment":"prod","status":"bogus","created":true}`, "bogus"},
		{"PUT", "/v1/workflows/{wf}/versions/{v}/layout", 204, "", "", ""},
		{"GET", "/v1/nowhere", 200, "application/json", `{}`, "path not in the document"},
	}
	for _, c := range cases {
		err := v.ValidateResponse(c.method, c.path, c.status, c.ctype, []byte(c.body))
		switch {
		case c.fails == "" && err != nil:
			t.Errorf("%s %s %d: %v", c.method, c.path, c.status, err)
		case c.fails != "" && (err == nil || !strings.Contains(err.Error(), c.fails)):
			t.Errorf("%s %s %d: error %v, want one containing %q", c.method, c.path, c.status, err, c.fails)
		}
	}
	if err := v.ValidateResponse("GET", "/v1/workflows", 201, "application/json", nil); !errors.Is(err, ErrUndeclared) {
		t.Errorf("undeclared status: %v", err)
	}
}

func TestValidateRequest(t *testing.T) {
	v, err := NewValidator()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		method, path string
		ctype, body  string
		fails        string // "" passes
	}{
		{"POST", "/v1/environments", "application/json", `{"name":"staging"}`, ""},
		{"POST", "/v1/environments", "application/json", `{"name":"Bad Name"}`, "does not match pattern"},
		{"POST", "/v1/environments", "application/json", `{}`, "missing propert"},
		{"POST", "/v1/environments", "application/json", ``, "requires a request body"},
		{"POST", "/v1/environments", "text/plain", `name=x`, "media type"},
		{"POST", "/v1/environments", "application/json", `{`, "not JSON"},
		{"GET", "/v1/workflows", "application/json", `{}`, "declares no request body"},
		{"GET", "/v1/workflows", "", ``, ""},
		{"GET", "/v1/nowhere", "", ``, "path not in the document"},
	}
	for _, c := range cases {
		err := v.ValidateRequest(c.method, c.path, c.ctype, []byte(c.body))
		switch {
		case c.fails == "" && err != nil:
			t.Errorf("%s %s %s: %v", c.method, c.path, c.body, err)
		case c.fails != "" && (err == nil || !strings.Contains(err.Error(), c.fails)):
			t.Errorf("%s %s %s: error %v, want one containing %q", c.method, c.path, c.body, err, c.fails)
		}
	}
}

func TestDocumentShape(t *testing.T) {
	d, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if d.OpenAPI != "3.1.0" || d.Info.Title == "" || len(d.Operations()) < 200 {
		t.Fatalf("document: openapi %q, title %q, %d operations", d.OpenAPI, d.Info.Title, len(d.Operations()))
	}
	for _, op := range d.Operations() {
		for code, r := range op.Responses {
			if d.Response(r) == nil {
				t.Errorf("%s %s %s: unresolved %s", op.Method, op.Path, code, r.Ref)
			}
		}
	}
}
