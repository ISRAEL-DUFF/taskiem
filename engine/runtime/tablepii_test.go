package runtime_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/history"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// A stand-in for xenibase@1's row actions, as a workflow calls them.
const tablesManifest = `
manifest: connector/v1
id: xenibase
version: 1.0.0
name: Xenibase
category: database
auth: { type: none }
actions:
  insert_rows:
    title: Insert rows
    class: idempotent_write
    idempotency: { field: "header:Idempotency-Key", encoding: base32_lower, length: 32 }
    compensate: null
    input: { type: object, properties: { table: { type: string }, rows: { type: array } } }
  read_rows:
    title: Read rows
    class: read
    input: { type: object, properties: { table: { type: string }, filters: { type: array }, order: { type: array }, limit: { type: integer } } }
    output: { type: object, properties: { rows: { type: array, items: { type: object } } } }
    pii:
      - { field: output.rows, category: other }
`

// A conversation kept in a data table (decision 0062 V5, A5): what the
// table marks personal is sealed cell by cell in the run's history, both
// written and read; the turns read back reach the model with their words
// (the step lets history through, D6) but not the customer's number; and
// the record of the call keeps the words redacted.
func TestConversationTableIsSealedAndHeard(t *testing.T) {
	e := rt.New(t)
	table := uuid.New()
	if _, err := e.DB.Admin.Exec(context.Background(), `INSERT INTO data_tables (id, tenant_id, name, title, created_by) VALUES ($1, $2, 'conversations', 'Conversations', 'test')`, table, e.Tenant); err != nil {
		t.Fatal(err)
	}
	for i, c := range []struct {
		name, typ string
		personal  bool
	}{{"conversation", "text", true}, {"sender", "text", true}, {"role", "text", false}, {"text", "text", true}, {"language", "text", false}} {
		if _, err := e.DB.Admin.Exec(context.Background(), `INSERT INTO data_table_columns (tenant_id, table_id, name, type, personal, position) VALUES ($1, $2, $3, $4, $5, $6)`,
			e.Tenant, table, c.name, c.typ, c.personal, i); err != nil {
			t.Fatal(err)
		}
	}
	older := []any{
		map[string]any{"id": int64(2), "role": "assistant", "text": "Good morning! We have rice and beans.", "sender": "2348031234567"},
		map[string]any{"id": int64(1), "role": "user", "text": "Morning o. My number na 08031234567", "sender": "2348031234567"},
	}
	conn := &connector.Connector{Manifest: connector.MustParse([]byte(tablesManifest)), Actions: map[string]connector.Action{
		"insert_rows": connector.ActionFunc(func(_ context.Context, req connector.Request) (connector.Response, error) {
			return connector.Response{Output: map[string]any{"inserted": int64(1)}}, nil
		}),
		"read_rows": connector.ActionFunc(func(_ context.Context, req connector.Request) (connector.Response, error) {
			return connector.Response{Output: map[string]any{"rows": older}}, nil
		}),
	}}
	if err := e.Registry.Register(conn); err != nil {
		t.Fatal(err)
	}
	fake := &ai.Fake{Script: []ai.Response{aiAnswer(`{"reply":"Yes o, plenty rice."}`)}}
	e.AI = fake
	wf := e.Publish(t, `{"schema":"wd/v1","id":"wf_mem","version":1,"name":"memory","trigger":{"type":"manual"},
	  "steps":[
	    {"id":"save","type":"connector","connector":"xenibase@1","action":"insert_rows",
	     "input":{"table":"conversations","rows":[{"conversation":"='whatsapp:' + trigger.body.from","sender":"=trigger.body.from","role":"user","text":"=trigger.body.text"}]}},
	    {"id":"read","type":"connector","connector":"xenibase@1","action":"read_rows","needs":["save"],
	     "input":{"table":"conversations","filters":[{"column":"sender","op":"eq","value":"=trigger.body.from"},{"column":"conversation","op":"in","values":["='whatsapp:' + trigger.body.from"]}],"order":[{"column":"created_at","desc":true}],"limit":20}},
	    {"id":"reply","type":"ai","needs":["read"],
	     "config":{"prompt":"Answer the customer's last message.","output_schema":{"type":"object","required":["reply"],"properties":{"reply":{"type":"string"}}},
	       "history":"=steps.read.output.rows.reverse().map(r, {'role': r.role, 'text': r.text})","personal_data":["history"]},
	     "input":{}}]}`)
	ref := e.Start(t, wf, map[string]any{"body": map[string]any{"from": "2348031234567", "text": "Una get rice today?"}})
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("run: %s %s", st, types(events(t, e, ref)))
	}
	for _, ev := range events(t, e, ref) {
		raw := string(ev.Payload)
		if (ev.StepID == "save" || ev.StepID == "read") && (ev.Type == history.StepScheduled || ev.Type == history.StepCompleted) {
			for _, clear := range []string{"Una get rice today?", "Good morning! We have rice", "Morning o", "2348031234567", "whatsapp:2348"} {
				if strings.Contains(raw, clear) {
					t.Errorf("%s(%s) keeps %q in the clear: %s", ev.Type, ev.StepID, clear, raw)
				}
			}
		}
	}
	msgs := fake.Requests()[0].Messages
	if len(msgs) != 3 || !strings.Contains(msgs[0].Text, "Morning o. My number na") || strings.Contains(msgs[0].Text, "08031234567") || !strings.Contains(msgs[0].Text, "[[phone_") ||
		msgs[1].Text != "Good morning! We have rice and beans." {
		t.Fatalf("the model read %+v", msgs)
	}
	var record string
	if err := e.DB.Admin.QueryRow(ctx, `SELECT messages::text FROM ai_interactions WHERE run_id = $1`, ref.ID).Scan(&record); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(record, "Morning o") || strings.Contains(record, "rice and beans") {
		t.Fatalf("the record keeps the words: %s", record)
	}
}
