package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/notify"
	"github.com/israel-duff/taskiem/engine/sender"
)

const tellFlow = `{"schema":"wd/v1","id":"wf_tell","version":1,"name":"Tell","trigger":{"type":"manual"},
  "inputs":{"schema":{"type":"object","properties":{"order":{"type":"string"},"customer":{"type":"string","x-pii":"name"}}}},
  "steps":[
    {"id":"owner","type":"connector","name":"Tell the owner","connector":"notify@1","action":"tell",
     "input":{"title":"New order","message":"=trigger.body.order + ' from ' + trigger.body.customer"}},
    {"id":"team","type":"connector","name":"Tell the packers","connector":"notify@1","action":"tell","needs":["owner"],
     "input":{"to":"role","role":"packer","message":"Pack order now"}}]}`

// Tell me (no-technical-steps N4): a workflow tells the owner on the
// WhatsApp they linked, with no connection or account elsewhere; a member
// who linked no chat hears by email. The outbox holds no text: it is read
// from the run when sent, and nothing is sent twice.
func TestTellMe(t *testing.T) {
	w := newWAWorld(t)
	ctx := context.Background()
	if err := w.env.Registry.Register(notify.New(&notify.Notifier{Pool: w.env.Store.Pool, Chat: []string{"whatsapp"},
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada Stores", "ada@ada.test")
	const on = "+2348010000071"
	w.bind(t, owner, on)
	w.member(t, owner, "pat@ada.test", "packer") // no chat linked

	wf := publishFlow(t, owner, tellFlow)
	owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"order": "AD-1", "customer": "Bola Ade"}})
	w.env.Drain(t)
	var queued string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT string_agg(kind || ':' || status, ',') FROM whatsapp_outbox WHERE kind = 'notice'`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != "notice:pending" {
		t.Fatalf("queued: %q", queued)
	}
	w.tick(t)
	got := w.graph.Last(t, on)
	if !strings.Contains(got.Text, "New order") || !strings.Contains(got.Text, "AD-1 from Bola Ade") {
		t.Fatalf("the owner's message: %+v", got)
	}
	// The packer, with no chat, by email.
	var emails int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM platform_messages WHERE channel = 'email' AND purpose = 'notice'`).Scan(&emails); err != nil || emails != 1 {
		t.Errorf("emails: %d %v", emails, err)
	}
	// The outbox holds no text, and a second tick sends nothing again.
	var text int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM whatsapp_outbox WHERE dedup_key LIKE '%Bola%'`).Scan(&text); err != nil || text != 0 {
		t.Errorf("text in the outbox: %d %v", text, err)
	}
	before := len(w.graph.Sent(on))
	w.tick(t)
	if len(w.graph.Sent(on)) != before {
		t.Error("sent twice")
	}
	// The run's output says who heard.
	runs := owner.must(200, "GET", "/v1/runs?workflow="+wf, nil)
	if !strings.Contains(toJSON(runs), "completed") {
		t.Errorf("run: %s", toJSON(runs))
	}
}
