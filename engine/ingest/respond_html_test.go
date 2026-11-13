package ingest_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/httpsec"
	"github.com/israel-duff/taskiem/engine/wdcheck"
)

// A respond step's answer comes from Taskiem's own address, so it is never
// a page or a script there: the step cannot change the security headers,
// HTML and JavaScript go out as plain text, and the answer is sandboxed.
// (The world publishes without the checks made when a workflow is saved,
// as a definition stored before them would be.)
func TestRespondNeverServesHTMLOrScript(t *testing.T) {
	w := newWorld(t)
	base := "/hooks/" + w.Tenant.String()
	page := `{"schema":"wd/v1","id":"wf_page","version":1,"name":"page",
  "trigger":{"type":"webhook","config":{"path":"/page","auth":"none","respond":true,"methods":["GET"]}},
  "steps":[{"id":"answer","type":"respond","config":{"status":200,
    "headers":{"Content-Type":"text/html; charset=utf-8","X-Frame-Options":"ALLOWALL","X-Content-Type-Options":"off","Content-Security-Policy":"default-src *",
      "Set-Cookie":"tk_session=x","X-Taskiem-Run":"mine","x-accel-redirect":"/internal","Location":"https://example.com/login","X-Shop-Ref":"ok-1"},
    "body":"<script src=\"` + base + `/js\"></script>"}}]}`
	js := `{"schema":"wd/v1","id":"wf_js","version":1,"name":"js",
  "trigger":{"type":"webhook","config":{"path":"/js","auth":"none","respond":true,"methods":["GET"]}},
  "steps":[{"id":"answer","type":"respond","config":{"status":200,
    "headers":{"Content-Type":"text/javascript"},
    "body":"fetch('/v1/me',{credentials:'include'})"}}]}`
	w.publish(t, page)
	w.publish(t, js)

	p := w.call(t, "GET", "/page", nil)
	if p.status != 200 || !strings.Contains(p.body, "<script src=") {
		t.Fatalf("the answer itself must still arrive, as text: %d %q", p.status, p.body)
	}
	if got := p.header("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("an HTML answer must be sent as plain text, got Content-Type %q", got)
	}
	if got := p.header("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options is the server's, got %q", got)
	}
	if got := p.header("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options is the server's, got %q", got)
	}
	if got := p.header("Content-Security-Policy"); got != httpsec.AnswerCSP || !strings.HasPrefix(got, "sandbox;") || !strings.Contains(got, "default-src 'none'") {
		t.Errorf("an answer is sandboxed and may load nothing, got Content-Security-Policy %q", got)
	}
	if got := p.header("Set-Cookie"); got != "" {
		t.Errorf("an answer may not set a cookie, got %q", got)
	}
	if got := p.header("X-Taskiem-Run"); got == "" || got == "mine" {
		t.Errorf("X-Taskiem-Run is the server's, got %q", got)
	}
	if got := p.header("X-Accel-Redirect"); got != "" {
		t.Errorf("a header a proxy acts on must not be sent, got %q", got)
	}
	if got := p.header("Location"); got != "" {
		t.Errorf("Location goes only with a redirect status, got %q with 200", got)
	}
	if got := p.header("X-Shop-Ref"); got != "ok-1" {
		t.Errorf("other X- headers are still sent, got %q", got)
	}

	s := w.call(t, "GET", "/js", nil)
	if s.status != 200 || !strings.Contains(s.body, "fetch(") {
		t.Fatalf("the answer itself must still arrive, as text: %d %q", s.status, s.body)
	}
	if got := s.header("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("a JavaScript answer must be sent as plain text, got Content-Type %q", got)
	}
	if got := s.header("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("a browser must not guess the type, got X-Content-Type-Options %q", got)
	}
	if got := s.header("Content-Security-Policy"); got != httpsec.AnswerCSP {
		t.Errorf("an answer is sandboxed, got Content-Security-Policy %q", got)
	}
}

func respondDoc(id, path string, status int, headers, body string) string {
	st, _ := json.Marshal(status)
	return `{"schema":"wd/v1","id":"` + id + `","version":1,"name":"answer",
  "trigger":{"type":"webhook","config":{"path":"` + path + `","auth":"none","respond":true,"methods":["GET"]}},
  "steps":[{"id":"answer","type":"respond","config":{"status":` + string(st) + `,"headers":` + headers + `,"body":` + body + `}}]}`
}

// Saving tells the author when a literal header or content type would not
// be sent as written; expressions and allowed values pass.
func TestRespondLiteralHeadersCheckedAtSave(t *testing.T) {
	w := newWorld(t)
	for _, c := range []struct {
		headers string
		status  int
		want    string // part of the problem; "" for none
	}{
		{`{"Content-Type":"text/html"}`, 200, "cannot be sent as text/html"},
		{`{"Content-Type":"image/svg+xml"}`, 200, "cannot be sent as image/svg+xml"},
		{`{"X-Frame-Options":"ALLOWALL"}`, 200, "X-Frame-Options cannot be set"},
		{`{"X-Taskiem-Run":"x"}`, 200, "Taskiem's own"},
		{`{"Set-Cookie":"a=b"}`, 200, "may not set cookies"},
		{`{"Location":"https://example.com/"}`, 200, "only sent with a redirect status"},
		{`{"Location":"javascript:alert(1)"}`, 302, "must be a full address"},
		{`{"Location":"https://example.com/"}`, 302, ""},
		{`{"Content-Type":"application/problem+json","X-Shop-Ref":"1","Cache-Control":"no-store"}`, 200, ""},
		{`{"Content-Type":"=trigger.query.type"}`, 200, ""},
	} {
		var probs []string
		for _, p := range wdcheck.Check([]byte(respondDoc("wf_check", "/check", c.status, c.headers, `"x"`)), w.Registry) {
			probs = append(probs, p.String())
		}
		got := strings.Join(probs, "\n")
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s at %d: problems %q, want %q", c.headers, c.status, got, c.want)
		}
	}
}

// A redirect is a fair answer to a webhook, to a full web address only.
// Files and images are saved by a browser, not shown.
func TestRespondRedirectAndFiles(t *testing.T) {
	w := newWorld(t)
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for i, c := range []struct{ loc, want string }{
		{"https://shop.example/thanks?o=1", "https://shop.example/thanks?o=1"},
		{"javascript:alert(1)", ""},
		{"//evil.example/x", ""},
		{"/v1/me", ""},
		{"data:text/html,x", ""},
		{"https://taskiem.com@evil.example/", ""},
	} {
		n := string(rune('a' + i))
		loc, _ := json.Marshal(c.loc)
		w.publish(t, respondDoc("wf_go"+n, "/go"+n, 302, `{"Location":`+string(loc)+`}`, `""`))
		resp, err := noFollow.Get(w.srv.URL + "/hooks/" + w.Tenant.String() + "/go" + n)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 302 || resp.Header.Get("Location") != c.want {
			t.Errorf("Location %q: got %d %q, want 302 %q", c.loc, resp.StatusCode, resp.Header.Get("Location"), c.want)
		}
	}

	w.publish(t, respondDoc("wf_xml", "/xml", 200, `{"Content-Type":"application/xml"}`, `"<a/>"`))
	if r := w.call(t, "GET", "/xml", nil); r.header("Content-Type") != "application/xml" || r.header("Content-Disposition") != "attachment" {
		t.Errorf("xml: %q %q", r.header("Content-Type"), r.header("Content-Disposition"))
	}
	w.publish(t, respondDoc("wf_json", "/json", 200, `{}`, `{"ok":true}`))
	if r := w.call(t, "GET", "/json", nil); r.header("Content-Type") != "application/json" || r.header("Content-Disposition") != "" || !strings.Contains(r.body, `"ok":true`) {
		t.Errorf("json: %q %q %q", r.header("Content-Type"), r.header("Content-Disposition"), r.body)
	}
}
