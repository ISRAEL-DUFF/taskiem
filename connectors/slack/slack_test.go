package slack

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/expr"
)

const token = "xoxb-test-token"

// seen is one request as Slack received it.
type seen struct {
	Path, ContentType, Auth string
	JSON                    map[string]any
	Form                    url.Values
	Raw                     []byte
}

type step struct {
	path    string // "/chat.postMessage", or "/upload" for the upload URL
	fixture string
}

// fake replays testdata/fixtures responses in order and records requests.
type fake struct {
	*httptest.Server
	t     *testing.T
	mu    sync.Mutex
	steps []step
	got   []seen
}

func newFake(t *testing.T, steps ...step) *fake {
	t.Helper()
	f := &fake{t: t, steps: steps}
	f.Server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(func() {
		f.Close()
		if len(f.steps) > 0 {
			t.Errorf("%d expected request(s) never arrived, next %s", len(f.steps), f.steps[0].path)
		}
	})
	return f
}

func (f *fake) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, _ := io.ReadAll(r.Body)
	s := seen{Path: r.URL.Path, ContentType: r.Header.Get("Content-Type"), Auth: r.Header.Get("Authorization"), Raw: raw}
	if strings.HasPrefix(s.ContentType, "application/json") {
		_ = json.Unmarshal(raw, &s.JSON)
	} else if strings.HasPrefix(s.ContentType, "application/x-www-form-urlencoded") {
		s.Form, _ = url.ParseQuery(string(raw))
	}
	f.got = append(f.got, s)
	if len(f.steps) == 0 {
		f.t.Errorf("unexpected request %s", r.URL.Path)
		w.WriteHeader(http.StatusTeapot)
		return
	}
	st := f.steps[0]
	f.steps = f.steps[1:]
	if r.Method != http.MethodPost || r.URL.Path != st.path {
		f.t.Errorf("got %s %s, want POST %s", r.Method, r.URL.Path, st.path)
	}
	if st.path != "/upload" && s.Auth != "Bearer "+token {
		f.t.Errorf("%s: authorization %q", st.path, s.Auth)
	}
	if st.fixture == "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	b, err := os.ReadFile(filepath.Join("testdata", "fixtures", st.fixture+".json"))
	if err != nil {
		f.t.Fatal(err)
	}
	var fx struct {
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers"`
		Body    json.RawMessage   `json:"body"`
	}
	if err := json.Unmarshal(b, &fx); err != nil {
		f.t.Fatal(err)
	}
	body := strings.ReplaceAll(string(fx.Body), "UPLOAD_URL", f.URL+"/upload")
	for k, v := range fx.Headers {
		w.Header().Set(k, v)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(fx.Status)
	if strings.HasPrefix(body, `"`) { // a text body
		var s string
		_ = json.Unmarshal([]byte(body), &s)
		body = s
	}
	_, _ = io.WriteString(w, body)
}

func run(t *testing.T, action string, in map[string]any, steps ...step) (map[string]any, []seen, error) {
	t.Helper()
	f := newFake(t, steps...)
	c := New(Options{BaseURL: f.URL})
	r, err := c.Actions[action].Execute(context.Background(), connector.Request{Input: in, HTTP: f.Client(), Attempt: 1,
		Credentials: map[string]string{"bot_token": token, "signing_secret": "8f742231b10e8888abcd99yyyzzz85a5"}})
	out, _ := r.Output.(map[string]any)
	return out, f.got, err
}

func TestRegisters(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	c, _ := reg.Get("slack@1")
	if h := c.Manifest.Hosts(); len(h) != 2 || h[0] != "slack.com" || h[1] != "files.slack.com" {
		t.Errorf("hosts %v", h)
	}
}

func TestAuthTest(t *testing.T) {
	out, got, err := run(t, "auth_test", nil, step{"/auth.test", "auth_test"})
	if err != nil || out["team_id"] != "T12345678" || out["bot_id"] != "B0123ABC" {
		t.Fatalf("%v %v", out, err)
	}
	if got[0].ContentType != "application/x-www-form-urlencoded" {
		t.Errorf("content type %q", got[0].ContentType)
	}
}

func TestPostMessage(t *testing.T) {
	blocks := []any{map[string]any{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": "*Payroll* approved"}}}
	out, got, err := run(t, "post_message", map[string]any{"channel": "C123ABC456", "text": "Payroll approved", "blocks": blocks, "unfurl_links": false},
		step{"/chat.postMessage", "post_message"})
	if err != nil {
		t.Fatal(err)
	}
	if out["ts"] != "1503435956.000247" || out["message_key"] != "C123ABC456:1503435956.000247" || out["thread_key"] != "C123ABC456:1503435956.000247" {
		t.Errorf("output %v", out)
	}
	j := got[0].JSON
	if got[0].ContentType != "application/json; charset=utf-8" || j["channel"] != "C123ABC456" || j["unfurl_links"] != false || len(j["blocks"].([]any)) != 1 {
		t.Errorf("request %s %v", got[0].ContentType, j)
	}
	if _, ok := j["thread_ts"]; ok {
		t.Error("absent inputs must not be sent")
	}

	// A reply's thread key is its parent's.
	out, got, err = run(t, "post_message", map[string]any{"channel": "C123ABC456", "text": "done", "thread_ts": "1503435000.000100"},
		step{"/chat.postMessage", "post_message"})
	if err != nil || out["thread_key"] != "C123ABC456:1503435000.000100" || got[0].JSON["thread_ts"] != "1503435000.000100" {
		t.Errorf("reply %v %v", out, err)
	}

	// Nothing to post never reaches Slack.
	if _, _, err := run(t, "post_message", map[string]any{"channel": "C1"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("empty message: %v", err)
	}
}

func TestUpdateEphemeralReaction(t *testing.T) {
	out, got, err := run(t, "update_message", map[string]any{"channel": "C123ABC456", "ts": "1401383885.000061", "text": "Updated", "blocks": []any{}, "request_key": "k"},
		step{"/chat.update", "update_message"})
	if err != nil || out["ts"] != "1401383885.000061" {
		t.Fatalf("%v %v", out, err)
	}
	if _, ok := got[0].JSON["request_key"]; ok || got[0].JSON["ts"] != "1401383885.000061" {
		t.Errorf("update body %v", got[0].JSON)
	}
	if b, ok := got[0].JSON["blocks"].([]any); !ok || len(b) != 0 {
		t.Errorf("an empty blocks array must be sent (it removes blocks): %v", got[0].JSON)
	}

	out, got, err = run(t, "post_ephemeral", map[string]any{"channel": "C1", "user": "U0BPQUNTA", "text": "Only you"}, step{"/chat.postEphemeral", "post_ephemeral"})
	if err != nil || out["message_ts"] != "1502210682.580145" || got[0].JSON["user"] != "U0BPQUNTA" {
		t.Errorf("ephemeral %v %v", out, err)
	}

	out, got, err = run(t, "add_reaction", map[string]any{"channel": "C1", "timestamp": "1.2", "name": ":white_check_mark:"}, step{"/reactions.add", "ok"})
	if err != nil || out["already_reacted"] != false || got[0].JSON["name"] != "white_check_mark" {
		t.Errorf("reaction %v %v %v", out, got[0].JSON, err)
	}
	// A repeat (say, after a lost response) is success.
	out, _, err = run(t, "add_reaction", map[string]any{"channel": "C1", "timestamp": "1.2", "name": "eyes"}, step{"/reactions.add", "already_reacted"})
	if err != nil || out["already_reacted"] != true {
		t.Errorf("repeat %v %v", out, err)
	}
}

func TestReads(t *testing.T) {
	out, got, err := run(t, "list_conversations", map[string]any{"types": "public_channel,private_channel", "exclude_archived": true, "limit": int64(200), "cursor": "abc"},
		step{"/conversations.list", "conversations_list"})
	if err != nil {
		t.Fatal(err)
	}
	f := got[0].Form
	if f.Get("types") != "public_channel,private_channel" || f.Get("exclude_archived") != "true" || f.Get("limit") != "200" || f.Get("cursor") != "abc" {
		t.Errorf("form %v", f)
	}
	chs := out["channels"].([]any)
	if len(chs) != 2 || chs[0].(map[string]any)["name"] != "general" || chs[0].(map[string]any)["num_members"] != int64(4) || out["next_cursor"] != "dGVhbTpDMDYxRkE1UEI=" {
		t.Errorf("channels %v", out)
	}

	out, got, err = run(t, "get_conversation", map[string]any{"channel": "C012AB3CD", "include_num_members": true}, step{"/conversations.info", "conversations_info"})
	if err != nil || out["channel"].(map[string]any)["name"] != "general" || got[0].Form.Get("include_num_members") != "true" {
		t.Errorf("info %v %v", out, err)
	}

	out, got, err = run(t, "lookup_user_by_email", map[string]any{"email": " spengler@ghostbusters.example.com "}, step{"/users.lookupByEmail", "lookup_user"})
	if err != nil || out["found"] != true || out["id"] != "W012A3CDE" || out["display_name"] != "spengler" || got[0].Form.Get("email") != "spengler@ghostbusters.example.com" {
		t.Errorf("lookup %v %v", out, err)
	}
	out, _, err = run(t, "lookup_user_by_email", map[string]any{"email": "nobody@example.com"}, step{"/users.lookupByEmail", "users_not_found"})
	if err != nil || out["found"] != false {
		t.Errorf("not found %v %v", out, err)
	}
}

func TestUploadFile(t *testing.T) {
	content := []byte("name,amount\nAda,100\n")
	out, got, err := run(t, "upload_file", map[string]any{"filename": "payroll.csv", "content_base64": base64.StdEncoding.EncodeToString(content),
		"channel_id": "C0NF841BK", "initial_comment": "September payroll", "alt_text": "a table"},
		step{"/files.getUploadURLExternal", "get_upload_url"}, step{"/upload", ""}, step{"/files.completeUploadExternal", "complete_upload"})
	if err != nil {
		t.Fatal(err)
	}
	if out["file_id"] != "F123ABC456" {
		t.Errorf("output %v", out)
	}
	if f := got[0].Form; f.Get("filename") != "payroll.csv" || f.Get("length") != strconv.Itoa(len(content)) || f.Get("alt_txt") != "a table" {
		t.Errorf("get url form %v", f)
	}
	if string(got[1].Raw) != string(content) || got[1].Auth != "" {
		t.Errorf("upload %q auth %q", got[1].Raw, got[1].Auth)
	}
	f := got[2].Form
	var files []map[string]string
	_ = json.Unmarshal([]byte(f.Get("files")), &files)
	if len(files) != 1 || files[0]["id"] != "F123ABC456" || files[0]["title"] != "payroll.csv" || f.Get("channel_id") != "C0NF841BK" || f.Get("initial_comment") != "September payroll" {
		t.Errorf("complete form %v", f)
	}

	// Failures before completion share nothing: even this unsafe write
	// may be retried.
	_, _, err = run(t, "upload_file", map[string]any{"filename": "a.txt", "content": "x"}, step{"/files.getUploadURLExternal", "internal_error"})
	if effects.Classify(err) != effects.KindNotSent {
		t.Errorf("get url failure: %v (%s)", err, effects.Classify(err))
	}
	_, _, err = run(t, "upload_file", map[string]any{"filename": "a.txt", "content": "x"}, step{"/files.getUploadURLExternal", "invalid_auth"})
	if effects.Classify(err) != effects.KindFatal {
		t.Errorf("fatal stays fatal: %v", err)
	}
	// Completion's outcome unknown: park.
	_, _, err = run(t, "upload_file", map[string]any{"filename": "a.txt", "content": "x"},
		step{"/files.getUploadURLExternal", "get_upload_url"}, step{"/upload", ""}, step{"/files.completeUploadExternal", "http_500"})
	if effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("complete 500: %v", err)
	}
	if _, _, err := run(t, "upload_file", map[string]any{"filename": "a.txt"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("no content: %v", err)
	}
	if _, _, err := run(t, "upload_file", map[string]any{"filename": "a.txt", "content_base64": "%%%"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("bad base64: %v", err)
	}
}

func TestErrorClasses(t *testing.T) {
	for _, tc := range []struct {
		fixture string
		kind    effects.ErrorKind
		code    string
		retry   time.Duration
	}{
		{"channel_not_found", effects.KindFatal, "channel_not_found", 0},
		{"invalid_auth", effects.KindFatal, "invalid_auth", 0},
		{"invalid_blocks", effects.KindFatal, "invalid_blocks", 0},
		// Rate limits prove nothing was posted: retried even for a post.
		{"ratelimited", effects.KindNotSent, "ratelimited", 7 * time.Second},
		{"http_429", effects.KindNotSent, "ratelimited", 30 * time.Second},
		{"service_unavailable", effects.KindRetryable, "service_unavailable", 0},
		// "It's possible some aspect of the operation succeeded."
		{"internal_error", effects.KindUnknownOutcome, "internal_error", 0},
		{"http_500", effects.KindUnknownOutcome, "", 0},
		{"http_503", effects.KindRetryable, "", 0},
		{"unknown_error", effects.KindUnknownOutcome, "something_new", 0},
	} {
		_, _, err := run(t, "post_message", map[string]any{"channel": "C1", "text": "hi"}, step{"/chat.postMessage", tc.fixture})
		var ae *APIError
		if effects.Classify(err) != tc.kind || !errors.As(err, &ae) || ae.Code != tc.code || ae.RetryAfter != tc.retry {
			t.Errorf("%s: %v (%s) %+v", tc.fixture, err, effects.Classify(err), ae)
		}
	}
	// No token: fatal before any request.
	c := New(Options{BaseURL: "http://127.0.0.1:1"})
	_, err := c.Actions["auth_test"].Execute(context.Background(), connector.Request{HTTP: http.DefaultClient, Credentials: map[string]string{}})
	if effects.Classify(err) != effects.KindFatal {
		t.Errorf("no token: %v", err)
	}
	// Connection refused: nothing sent.
	_, err = c.Actions["post_message"].Execute(context.Background(), connector.Request{Input: map[string]any{"channel": "C1", "text": "x"}, HTTP: http.DefaultClient, Credentials: map[string]string{"bot_token": token}})
	if effects.Classify(err) != effects.KindNotSent {
		t.Errorf("refused: %v", err)
	}
}

func TestClassifyByShape(t *testing.T) {
	for code, kind := range map[string]effects.ErrorKind{
		"restricted_action_read_only_channel": effects.KindFatal,
		"invalid_arguments":                   effects.KindFatal,
		"missing_scope":                       effects.KindFatal,
		"not_in_channel":                      effects.KindFatal,
		"msg_too_long":                        effects.KindFatal,
		"file_uploads_disabled":               effects.KindFatal,
		"message_not_found":                   effects.KindFatal,
		"rate_limited":                        effects.KindNotSent,
		"fatal_error":                         effects.KindUnknownOutcome,
		"team_added_to_org":                   effects.KindRetryable,
	} {
		if got := effects.Classify(classify(&APIError{Method: "m", Code: code})); got != kind {
			t.Errorf("%s: %s, want %s", code, got, kind)
		}
	}
}

// sign is Slack's v0 signature of a request.
func sign(secret, ts string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte("v0:" + ts + ":"))
	m.Write(body)
	return "v0=" + hex.EncodeToString(m.Sum(nil))
}

func TestSignatures(t *testing.T) {
	const secret = "8f742231b10e8888abcd99yyyzzz85a5"
	now := time.Unix(1_790_000_000, 0)
	connector.Now = func() time.Time { return now }
	t.Cleanup(func() { connector.Now = time.Now })
	m := New(Options{}).Manifest
	for _, name := range []string{"events", "interactions"} {
		spec := m.Triggers[name].Verify
		body := []byte(`{"type":"event_callback","event_id":"Ev1"}`)
		if name == "interactions" {
			body = []byte("payload=" + url.QueryEscape(`{"type":"block_actions"}`))
		}
		ts := strconv.FormatInt(now.Unix(), 10)
		h := http.Header{}
		h.Set("X-Slack-Signature", sign(secret, ts, body))
		h.Set("X-Slack-Request-Timestamp", ts)
		if err := connector.VerifyWebhook(spec, secret, h, body); err != nil {
			t.Errorf("%s valid: %v", name, err)
		}
		if connector.VerifyWebhook(spec, "other", h, body) == nil {
			t.Errorf("%s: wrong secret accepted", name)
		}
		if connector.VerifyWebhook(spec, secret, h, append(body, ' ')) == nil {
			t.Errorf("%s: tampered body accepted", name)
		}
		old := strconv.FormatInt(now.Add(-6*time.Minute).Unix(), 10)
		h.Set("X-Slack-Request-Timestamp", old)
		h.Set("X-Slack-Signature", sign(secret, old, body))
		if connector.VerifyWebhook(spec, secret, h, body) == nil {
			t.Errorf("%s: replay outside five minutes accepted", name)
		}
	}
}

// eval runs a trigger expression the way ingest does.
func eval(t *testing.T, src string, body any, headers map[string]any) any {
	t.Helper()
	e := expr.MustNewWithRoots("body", "headers", "query")
	v, err := e.Eval(src, map[string]any{"body": body, "headers": headers, "query": map[string]any{}})
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	return v
}

func decode(t *testing.T, s string) any {
	t.Helper()
	v, err := expr.DecodeJSON([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestEventsTrigger(t *testing.T) {
	tr := New(Options{}).Manifest.Triggers["events"]
	hs := tr.Handshake
	// url_verification is answered with the challenge, not delivered.
	challenge := decode(t, `{"token":"Jhj5dZrVaK7ZwHHjRyZWjbDl","challenge":"3eZbrw1aBm2rZgRNFdxV2595E9CY3gmdALWMmHkvFXO7tYXAYM8P","type":"url_verification"}`)
	if hs == nil || hs.Method != "POST" || eval(t, hs.When, challenge, nil) != true || eval(t, hs.Respond, challenge, nil) != "3eZbrw1aBm2rZgRNFdxV2595E9CY3gmdALWMmHkvFXO7tYXAYM8P" {
		t.Errorf("handshake %+v", hs)
	}
	headers := map[string]any{"x-slack-request-timestamp": "1531420618"}
	for _, c := range []struct {
		body                      string
		event, dedup, correlation any
	}{
		{`{"type":"event_callback","team_id":"T1","event":{"type":"message","channel":"C123ABC456","user":"U1","text":"approved","ts":"1503435999.000300","thread_ts":"1503435956.000247"},"event_id":"Ev123ABC456","event_time":1503435999}`,
			"message", "Ev123ABC456", "C123ABC456:1503435956.000247"},
		{`{"type":"event_callback","event":{"type":"reaction_added","user":"U1","reaction":"white_check_mark","item":{"type":"message","channel":"C123ABC456","ts":"1503435956.000247"},"event_ts":"1360782804.083113"},"event_id":"Ev2"}`,
			"reaction_added", "Ev2", "C123ABC456:1503435956.000247"},
		{`{"type":"event_callback","event":{"type":"app_mention","channel":"C1","text":"<@U0LAN0Z89> hi","ts":"1515449522.000016"},"event_id":"Ev3"}`,
			"app_mention", "Ev3", nil},
		{`{"token":"x","type":"app_rate_limited","team_id":"T123456","minute_rate_limited":1518467820,"api_app_id":"A123456"}`,
			"app_rate_limited", "app_rate_limited:1518467820", nil},
		{`{"type":"something_else"}`, "something_else", "something_else:1531420618", nil},
	} {
		b := decode(t, c.body)
		if eval(t, hs.When, b, headers) == true {
			t.Errorf("handshake would swallow %s", c.body)
		}
		for _, f := range []struct {
			name, src string
			want      any
		}{{"event_type", tr.EventType, c.event}, {"dedup", tr.Dedup, c.dedup}, {"correlation", tr.Correlation, c.correlation}} {
			if got := eval(t, f.src, b, headers); got != f.want {
				t.Errorf("%s of %s = %#v, want %#v", f.name, c.body, got, f.want)
			}
		}
	}
}

func TestInteractionsTrigger(t *testing.T) {
	tr := New(Options{}).Manifest.Triggers["interactions"]
	if tr.Handshake != nil {
		t.Error("interactions have no handshake")
	}
	// Payloads as Slack documents them (pretty-printed) and compact.
	blockActions := `{
    "type": "block_actions",
    "user": {"id": "UA8RXUSPL", "username": "jtorrance"},
    "container": {"type": "message", "message_ts": "1548261231.000200", "channel_id": "CBR2V3XEX"},
    "message": {"type": "message", "text": "Approve payroll?", "ts": "1548261231.000200",
      "blocks": [{"type": "actions", "block_id": "taskiem-corr:run-42", "elements": [
        {"type": "button", "action_id": "approve", "value": "taskiem-corr:run-42|approve"},
        {"type": "button", "action_id": "reject", "value": "taskiem-corr:run-42|reject"}]}]},
    "actions": [{"action_id": "reject", "block_id": "taskiem-corr:run-42", "value": "taskiem-corr:run-42|reject", "type": "button", "action_ts": "1548426417.840180"}]
}`
	compact := `{"type":"view_submission","view":{"type":"modal","private_metadata":"taskiem-corr:order-7","blocks":[{"type":"input"}]}}`
	plain := `{"type":"shortcut","callback_id":"open_dialog","trigger_id":"1.2.3"}`
	quoted := `{"type":"block_actions","message":{"text":"he typed \"type\": \"view_submission\""}}`
	for _, c := range []struct {
		payload     string
		event, corr any
	}{
		{blockActions, "block_actions", "run-42"},
		{compact, "view_submission", "order-7"},
		{plain, "shortcut", nil},
		{quoted, "block_actions", nil},
		{`{"type":"view_closed","view":{"type":"modal"}}`, "view_closed", nil},
		{`{"type":"something_new"}`, "interaction", nil},
	} {
		// What ingest makes of the form post.
		form, _ := url.ParseQuery("payload=" + url.QueryEscape(c.payload))
		body := map[string]any{"payload": form.Get("payload")}
		if got := eval(t, tr.EventType, body, nil); got != c.event {
			t.Errorf("event of %.40s = %v, want %v", c.payload, got, c.event)
		}
		if got := eval(t, tr.Correlation, body, nil); got != c.corr {
			t.Errorf("correlation of %.40s = %#v, want %#v", c.payload, got, c.corr)
		}
	}
	if got := eval(t, tr.EventType, map[string]any{"command": "/deploy"}, nil); got != "unknown" {
		t.Errorf("no payload: %v", got)
	}
	if tr.Dedup != "" {
		t.Error("interactions dedup on the body hash")
	}
}
