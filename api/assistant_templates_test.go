package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	speechconn "github.com/israel-duff/taskiem/connectors/speech"
	telegramconn "github.com/israel-duff/taskiem/connectors/telegram"

	"github.com/israel-duff/taskiem/engine/assistant"
	"github.com/israel-duff/taskiem/engine/connector"
)

// The assistant's recipe (decision 0062 V8): instantiating the WhatsApp
// assistant makes its draft, with the guard on its chat trigger, and the
// two tables it keeps: the conversations (kept as long as conversations
// are) and the allowed customers.
func TestAssistantTemplates(t *testing.T) {
	tw := newTablesWorld(t)
	w := tw.world
	owner := w.tenant(t, "Mama Put", "owner@mamaput.test")
	out := owner.must(201, "POST", "/v1/templates/assistant-whatsapp/instantiate", map[string]any{"params": map[string]any{"business_name": "Mama Put Kitchen"}})
	made := map[string]string{}
	for _, x := range out["tables"].([]any) {
		m := x.(map[string]any)
		made[m["name"].(string)] = m["status"].(string)
	}
	if made["conversations"] != "created" || made["assistant_allowed"] != "created" {
		t.Fatalf("tables: %v", out["tables"])
	}
	for _, x := range owner.must(200, "GET", "/v1/tables", nil)["tables"].([]any) {
		m := x.(map[string]any)
		if m["name"] == "conversations" && m["keep_with_conversations"] != true {
			t.Errorf("the conversations table is not kept with conversations: %v", m)
		}
	}
	wf := owner.must(200, "GET", "/v1/workflows/"+out["id"].(string)+"/versions/1", nil)
	if s := toJSON(wf); !strings.Contains(s, `"guard":true`) || !strings.Contains(s, `"template":"assistant-answer"`) {
		t.Fatalf("the draft: %s", s)
	}
	// Where Taskiem lets no assistant answer this business, they are not
	// offered, and cannot be made.
	w.srv.AssistantMode = assistant.ModeBeta
	if list := toJSON(owner.must(200, "GET", "/v1/templates", nil)); strings.Contains(list, "assistant-whatsapp") {
		t.Fatal("an assistant offered outside the beta")
	}
	owner.must(409, "POST", "/v1/templates/assistant-telegram/instantiate", map[string]any{"params": map[string]any{"business_name": "Mama Put Kitchen"}})
	w.srv.AssistantMode = assistant.ModeOn
	if list := toJSON(owner.must(200, "GET", "/v1/templates", nil)); !strings.Contains(list, "assistant-whatsapp") {
		t.Fatal("an assistant not offered")
	}
	// The Telegram assistant uses the same tables: they are there already.
	out = owner.must(201, "POST", "/v1/templates/assistant-telegram/instantiate", map[string]any{"params": map[string]any{"business_name": "Mama Put Kitchen"}})
	for _, x := range out["tables"].([]any) {
		if m := x.(map[string]any); m["status"] != "exists" {
			t.Errorf("a second assistant: %v", m)
		}
	}
}

// Readiness for an assistant (V8): this month's AI and voice allowances
// used up, and a Telegram bot whose messages are not delivered, are said
// (never blocking).
func TestAssistantReadiness(t *testing.T) {
	w := newWorld(t)
	for _, c := range []*connector.Connector{speechconn.New(nil), telegramconn.New(telegramconn.Options{})} {
		if err := w.env.Registry.Register(c); err != nil {
			t.Fatal(err)
		}
	}
	owner := w.tenant(t, "Mama Put", "owner@mamaput.test")
	tenant := tenantOf(t, owner)
	doc := `{"schema":"wd/v1","id":"wf_answer","version":1,"name":"Answer","trigger":{"type":"connector_event","config":{"connector":"telegram@1","trigger":"update","events":["message"]}},
	  "steps":[{"id":"hear","type":"connector","connector":"speech@1","action":"transcribe","input":{"file":"=trigger.item.file"}},
	    {"id":"reply","type":"ai","needs":["hear"],"config":{"prompt":"Answer","output_schema":{"type":"object","required":["reply"],"properties":{"reply":{"type":"string"}}},
	      "fallback":{"reply":"Someone will get back to you."}},"input":{"q":"=steps.hear.output.text"}}]}`
	ready := func() map[string]map[string]any {
		return needsOf(t, owner.must(200, "POST", "/v1/readiness", map[string]any{"definition": json.RawMessage(doc)})["needs"])
	}
	n := ready()
	for _, k := range []string{"setup/ai_allowance/prod", "setup/voice_allowance/prod", "setup/delivery/prod"} {
		if n[k] != nil {
			t.Fatalf("%s before anything is used: %v", k, n[k])
		}
	}
	ctx := context.Background()
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO ai_interactions (id, tenant_id, round, kind, actor, provider, model, system_digest, messages, outcome, total_tokens)
		VALUES (gen_random_uuid(), $1, 1, 'step', 'run', 'fake', 'fake', 'x', '[]', 'valid', 1000000000000)`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO speech_usage (tenant_id, month, seconds_heard) VALUES ($1, date_trunc('month', now() AT TIME ZONE 'UTC')::date, 100000000)`, tenant); err != nil {
		t.Fatal(err)
	}
	// The bot's webhook as deploying declares it (remote.DeclareInUse): the
	// connection's own subscription, named by the only bot, which the
	// trigger does not name.
	if _, err := w.env.Vault.CreateConnection(ctx, uuid.MustParse(tenant), "prod", "telegram", "main", "api_key", map[string]string{"bot_token": "123:abc"}, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO remote_subscriptions (id, tenant_id, environment, connector, trigger_name, connection, version, desired, state, last_error)
		VALUES (gen_random_uuid(), $1, 'prod', 'telegram@1', 'update', 'main', 0, 'present', 'failed', 'Telegram said: Unauthorized')`, tenant); err != nil {
		t.Fatal(err)
	}
	n = ready()
	for _, k := range []string{"setup/ai_allowance/prod", "setup/voice_allowance/prod", "setup/delivery/prod"} {
		if n[k] == nil || n[k]["blocking"] != false {
			t.Fatalf("%s: %v (all %v)", k, n[k], n)
		}
	}
	if n["setup/ai_allowance/prod"]["problem"] != "limit" || !strings.Contains(n["setup/delivery/prod"]["message"].(string), "Unauthorized") {
		t.Fatalf("needs: %v", n)
	}
}

// Readiness says when a step needs a part of Taskiem the business has
// turned off (I4): an assistant reading invoices needs Money.
func TestReadinessAreaOff(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Mama Put", "owner@mamaput.test")
	tenant := tenantOf(t, owner)
	doc := `{"schema":"wd/v1","id":"wf_owed","version":1,"name":"Owed","trigger":{"type":"manual"},
	  "steps":[{"id":"owed","type":"connector","connector":"invoices@1","action":"find_by_phone","input":{"phone":"=trigger.body.phone"}}]}`
	ready := func() map[string]map[string]any {
		return needsOf(t, owner.must(200, "POST", "/v1/readiness", map[string]any{"definition": json.RawMessage(doc)})["needs"])
	}
	if n := ready()["setting/area_money/prod"]; n != nil {
		t.Fatalf("money is on: %v", n)
	}
	if _, err := w.env.DB.Admin.Exec(context.Background(), `INSERT INTO business_areas (tenant_id, area, enabled, updated_by) VALUES ($1, 'money', false, 'test')
		ON CONFLICT (tenant_id, area) DO UPDATE SET enabled = false`, tenant); err != nil {
		t.Fatal(err)
	}
	n := ready()["setting/area_money/prod"]
	if n == nil || n["blocking"] != true || !strings.Contains(n["message"].(string), "is not turned on") {
		t.Fatalf("money off: %v", n)
	}
}
