package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/assistant"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/ingest"
)

// chatFake is a chat channel for tests: a trigger that reads customers'
// messages, a send that declares its turn, and the sends it was asked for.
type chatFake struct {
	mu   sync.Mutex
	sent []map[string]any
}

func (f *chatFake) connector() *connector.Connector {
	return &connector.Connector{Manifest: connector.MustParse([]byte(`
manifest: connector/v1
id: chatfake
version: 1.0.0
name: Chat
category: other
auth: { type: api_key, fields: [{ key: token, label: Token, secret: true }] }
triggers:
  messages:
    type: webhook
    verify: { scheme: header_secret, header: X-Chat-Secret, secret_field: token }
    events: [message]
    event_type: '="message"'
    dedup: =body.id
    pii:
      - { field: body.from, category: other }
      - { field: body.text, category: other }
    conversation:
      sender: =body.from
      sender_category: other
      private: '=true'
      kind: '="text"'
      text: =body.text
      reply: { action: send, to: to, text: body }
actions:
  send:
    title: Send a message
    class: unsafe_write
    compensate: null
    turn: { to: to, text: body, kind: text }
    input: { type: object, required: [to, body], properties: { to: { type: string }, body: { type: string } } }
    output: { type: object, properties: { message_id: { type: string } } }
    pii:
      - { field: to, category: other }
      - { field: body, category: other }
`)), Actions: map[string]connector.Action{"send": connector.ActionFunc(func(_ context.Context, req connector.Request) (connector.Response, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.sent = append(f.sent, req.Input)
		return connector.Response{Output: map[string]any{"message_id": "m" + uuid.NewString()[:8]}}, nil
	})}}
}

func (f *chatFake) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

// The conversations page (decision 0062 V7): what a customer said and what
// was answered, masked until revealed; answering by hand through the same
// connection, once per request; pausing the assistant for one customer;
// the month's numbers; and Talk to it here, which sends nothing.
func TestConversations(t *testing.T) {
	w := newWorld(t)
	chat := &chatFake{}
	if err := w.env.Registry.Register(chat.connector()); err != nil {
		t.Fatal(err)
	}
	h := &ingest.Handler{Store: w.env.Store, Secrets: w.env.Vault, Connections: w.env.Vault, Registry: w.env.Registry,
		Guard: &assistant.Guard{}, Vault: w.env.Vault}
	edge := httptest.NewServer(http.StripPrefix("/hooks", h))
	t.Cleanup(edge.Close)
	owner := w.tenant(t, "Mama Put", "owner@mamaput.test")
	tenant := uuid.MustParse(tenantOf(t, owner))
	for _, env := range []string{"dev", "prod"} {
		if _, err := w.env.Vault.CreateConnection(context.Background(), tenant, env, "chatfake", "main", "api_key", map[string]string{"token": "s3cret"}, "test"); err != nil {
			t.Fatal(err)
		}
	}
	wf := publishFlow(t, owner, `{"schema":"wd/v1","id":"wf_assistant","version":1,"name":"Assistant",
	  "trigger":{"type":"connector_event","config":{"connector":"chatfake@1","trigger":"messages","guard":true}},
	  "steps":[{"id":"answer","type":"connector","connector":"chatfake@1","action":"send","when":"=trigger.guard.outcome != 'quiet'",
	    "input":{"to":"=trigger.guard.sender","body":"=trigger.guard.outcome == 'answer' ? 'Thanks: ' + trigger.guard.text : trigger.guard.reply"}}]}`)
	deliver := func(id, from, text string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"id": id, "from": from, "text": text})
		req, _ := http.NewRequest("POST", edge.URL+"/hooks/"+tenant.String()+"/connectors/chatfake@1/messages?env=prod", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Chat-Secret", "s3cret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 202 {
			t.Fatalf("delivery: %d", resp.StatusCode)
		}
		w.env.Drain(t)
	}
	deliver("in-1", "55501", "Do you deliver? Call me on 08031234567")
	if chat.count() != 1 {
		t.Fatalf("%d sent, want the answer", chat.count())
	}

	list := owner.must(200, "GET", "/v1/conversations", nil)
	if as := list["assistants"].([]any); len(as) != 2 {
		t.Fatalf("assistants: %v", list)
	}
	const channel = "chatfake@1/main/prod"
	var customer string
	for _, c := range list["customers"].([]any) {
		m := c.(map[string]any)
		if m["channel"] == channel {
			customer = m["customer"].(string)
		}
	}
	if customer == "" || strings.Contains(toJSON(list), "55501") {
		t.Fatalf("customers: %v", list)
	}
	q := "/v1/conversations/thread?channel=" + url.QueryEscape(channel) + "&customer=" + url.QueryEscape(customer)
	th := owner.must(200, "GET", q, nil)
	turns := th["turns"].([]any)
	if len(turns) != 2 {
		t.Fatalf("turns: %v", th)
	}
	in, out := turns[0].(map[string]any), turns[1].(map[string]any)
	if in["role"] != "customer" || in["status"] != "answer" || strings.Contains(in["text"].(string), "08031234567") || !strings.Contains(in["text"].(string), "Do you deliver?") {
		t.Fatalf("the customer's message: %v", in)
	}
	if out["role"] != "assistant" || !strings.HasPrefix(out["text"].(string), "Thanks: Do you deliver?") || out["run_id"] != in["run_id"] || out["run_id"] == nil {
		t.Fatalf("the answer: %v", out)
	}
	// Revealed: the number, the words as written, and an audit record.
	viewer := addMember(t, w, owner, "vic", "viewer")
	viewer.must(200, "GET", q, nil)
	viewer.must(403, "GET", q+"&reveal=true", nil)
	rv := owner.must(200, "GET", q+"&reveal=true", nil)
	if rv["customer"].(map[string]any)["number"] != "55501" || !strings.Contains(toJSON(rv["turns"]), "08031234567") {
		t.Fatalf("revealed: %v", rv)
	}
	// Answering by hand: once per request, recorded with who sent it.
	viewer.must(403, "POST", "/v1/conversations/reply", map[string]any{"channel": channel, "customer": customer, "text": "hi"})
	owner.must(204, "POST", "/v1/conversations/pause", map[string]any{"channel": channel, "customer": customer})
	owner.must(200, "POST", "/v1/conversations/reply", map[string]any{"channel": channel, "customer": customer, "text": "Yes, we deliver to Ajah.", "request_id": "r1"})
	if again := owner.must(200, "POST", "/v1/conversations/reply", map[string]any{"channel": channel, "customer": customer, "text": "Yes, we deliver to Ajah.", "request_id": "r1"}); again["duplicate"] != true {
		t.Fatalf("a repeated request: %v", again)
	}
	if chat.count() != 2 {
		t.Fatalf("%d sent, want the answer and one by hand", chat.count())
	}
	// Paused for them: their next message is kept, not answered.
	deliver("in-2", "55501", "Thank you!")
	if chat.count() != 2 {
		t.Fatal("the assistant answered over the person")
	}
	th = owner.must(200, "GET", q, nil)
	if c := th["customer"].(map[string]any); c["state"] != "waiting" {
		t.Fatalf("state: %v", c)
	}
	roles := []string{}
	for _, x := range th["turns"].([]any) {
		roles = append(roles, x.(map[string]any)["role"].(string))
	}
	if strings.Join(roles, ",") != "customer,assistant,member,customer" {
		t.Fatalf("turns: %v", roles)
	}
	owner.must(204, "POST", "/v1/conversations/resume", map[string]any{"channel": channel, "customer": customer})
	m := owner.must(200, "GET", "/v1/conversations/metrics?channel="+url.QueryEscape(channel), nil)["month"].(map[string]any)
	if m["messages"] != float64(2) || m["answers"] != float64(1) || m["by_hand"] != float64(1) || m["customers"] != float64(1) {
		t.Fatalf("metrics: %v", m)
	}

	// Talk to it here: the real workflow, nothing sent.
	started := owner.must(201, "POST", "/v1/conversations/test", map[string]any{"workflow": wf, "text": "Hello?"})
	w.env.Drain(t)
	var got map[string]any
	for i := 0; i < 50; i++ {
		got = owner.must(200, "GET", "/v1/conversations/test/"+started["run"].(string), nil)
		if got["ended"] == true {
			break
		}
		w.env.Drain(t)
		time.Sleep(50 * time.Millisecond)
	}
	if tt := got["turns"].([]any); got["status"] != "completed" || len(tt) != 1 || !strings.Contains(tt[0].(map[string]any)["text"].(string), "Thanks: Hello?") {
		t.Fatalf("test chat: %v", got)
	}
	if chat.count() != 2 {
		t.Fatalf("the test chat sent a message: %d", chat.count())
	}
	viewer.must(404, "GET", "/v1/conversations/test/"+started["run"].(string), nil) // another member's test chat
	if mine := toJSON(owner.must(200, "GET", "/v1/conversations", nil)); !strings.Contains(mine, `"test/`) {
		t.Fatalf("the member's own test chat is not listed: %s", mine)
	}
	if theirs := toJSON(viewer.must(200, "GET", "/v1/conversations", nil)); strings.Contains(theirs, `"test/`) {
		t.Fatalf("another member's test chat is listed: %s", theirs)
	}
	viewer.must(404, "GET", "/v1/conversations/thread?channel="+url.QueryEscape(started["channel"].(string))+"&customer="+url.QueryEscape(started["customer"].(string)), nil)
	owner.must(204, "DELETE", "/v1/conversations/test", nil)
	if mine := toJSON(owner.must(200, "GET", "/v1/conversations", nil)); strings.Contains(mine, `"test/`) {
		t.Fatalf("the test chat was not started again: %s", mine)
	}

	// WhatsApp after the 24-hour window: text is refused, a template is needed.
	err := db.InTenantTx(context.Background(), w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := (&assistant.Guard{}).Decide(context.Background(), tx, w.env.Vault, tenant, assistant.Defaults(),
			assistant.Message{Channel: "whatsapp@1/main/prod", Dedup: "wamid.OLD", Sender: "2348031234567", SenderCategory: "phone", Private: true, Kind: "text", Text: "hello"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE assistant_contacts SET last_inbound_at = now() - interval '2 days' WHERE channel = 'whatsapp@1/main/prod'`); err != nil {
		t.Fatal(err)
	}
	var waCustomer string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT subject FROM assistant_contacts WHERE channel = 'whatsapp@1/main/prod'`).Scan(&waCustomer); err != nil {
		t.Fatal(err)
	}
	st, body := owner.do("POST", "/v1/conversations/reply", map[string]any{"channel": "whatsapp@1/main/prod", "customer": waCustomer, "text": "Are you still there?"})
	if st != 409 || !strings.Contains(toJSON(body), "approved template") {
		t.Fatalf("outside the window: %d %v", st, body)
	}
	var actions []string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT array_agg(action ORDER BY chain_seq) FROM audit_log WHERE tenant_id = $1 AND action LIKE 'conversation.%'`, tenant).Scan(&actions); err != nil {
		t.Fatal(err)
	}
	if strings.Join(actions, ",") != "conversation.reveal,conversation.pause,conversation.reply" {
		t.Fatalf("audit: %v", actions)
	}
}
