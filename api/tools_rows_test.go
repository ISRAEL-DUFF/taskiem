package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// publishTicketTools publishes "assign-ticket" (takes a ref, answers
// "Yours: <ref>") and "open-tickets", a list of n tickets whose rows each
// have a once "Assign to me".
func publishTicketTools(t *testing.T, owner *client, n int) {
	t.Helper()
	var tickets []map[string]any
	for i := 1; i <= n; i++ {
		tickets = append(tickets, map[string]any{"ref": fmt.Sprintf("T-%d", i), "title": fmt.Sprintf("Ticket %d", i)})
	}
	list, _ := json.Marshal(tickets)
	for _, def := range []string{
		`{"schema":"wd/v1","id":"wf_assign","version":1,"name":"Assign a ticket","trigger":{"type":"manual"},
		"inputs":{"schema":{"type":"object","required":["ref"],"properties":{"ref":{"type":"string","title":"Ticket"}}}},
		"outputs":[{"name":"ref","type":"string","title":"Ticket","description":"The ticket","value":"=steps.take.output.ref"}],
		"tool":{"name":"Assign a ticket","command":"assign-ticket","about":"Take a ticket","answer":{"type":"text","text":"='Yours: ' + outputs.ref"}},
		"steps":[{"id":"take","type":"transform","config":{"output":{"ref":"=trigger.body.ref"}}}]}`,
		`{"schema":"wd/v1","id":"wf_open","version":1,"name":"Open tickets","trigger":{"type":"manual"},
		"inputs":{"schema":{"type":"object","properties":{}}},
		"outputs":[{"name":"tickets","type":"list","title":"Tickets","description":"Open ones","value":"=steps.find.output.tickets"}],
		"tool":{"name":"Open tickets","command":"open-tickets","about":"The open tickets",
		        "answer":{"type":"list","from":"tickets","title":"Open tickets","item":{"title":"=item.title","sub":"=item.ref"},
		                  "actions":[{"label":"Assign to me","tool":"assign-ticket","inputs":{"ref":"=item.ref"},"once":true}]}},
		"steps":[{"id":"find","type":"transform","config":{"output":{"tickets":` + string(list) + `}}}]}`,
	} {
		var head struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal([]byte(def), &head)
		wf := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": head.Name, "definition": json.RawMessage(def)})["id"].(string)
		owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	}
}

// A list answer in WhatsApp (made tools §7, M2): a list message of its
// first ten rows and "and N more" with the link; a row chosen offers its
// buttons; pressed, the assign tool opens filled in and runs. Another
// person choosing the row is refused.
func TestToolListRowsWhatsApp(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Acme", "owner@rows.test")
	publishTicketTools(t, owner, 12)
	ada := w.member(t, owner, "ada@rows.test", "operator")
	bo := w.member(t, owner, "bo@rows.test", "operator")
	const adaOn, boOn = "+2348010000061", "+2348010000062"
	w.bind(t, ada, adaOn)
	w.bind(t, bo, boOn)

	if r := w.say(t, adaOn, "open-tickets"); !strings.Contains(r, "Starting Open tickets") {
		t.Fatalf("echo: %q", r)
	}
	w.env.Drain(t)
	w.tick(t)
	last := w.graph.Last(t, adaOn)
	if last.Type != "interactive" || len(last.List) != 10 || last.List[1].Title != "Ticket 2" || last.List[1].Description != "T-2" ||
		!strings.HasPrefix(last.List[1].ID, "tr:") || last.ListButton != "Choose" {
		t.Fatalf("list message: %+v", last)
	}
	if !strings.Contains(last.Text, "Open tickets") || !strings.Contains(last.Text, "and 2 more: see them all in the app") || !strings.Contains(last.Text, waPublicURL+"/runs/") {
		t.Errorf("list words: %q", last.Text)
	}

	// Another person choosing a row of it: refused.
	if got := w.send(t, boOn, whatsapptest.ListReply(last.List[1].ID, "Ticket 2")); len(got) != 1 || !strings.Contains(got[0].Text, "sent to someone else") {
		t.Fatalf("someone else's row: %+v", got)
	}
	// A row that is not there.
	if got := w.send(t, adaOn, whatsapptest.ListReply("tr:nonsense", "x")); len(got) != 1 || !strings.Contains(got[0].Text, "not available") {
		t.Fatalf("no row: %+v", got)
	}

	// The row chosen: its buttons.
	got := w.send(t, adaOn, whatsapptest.ListReply(last.List[1].ID, "Ticket 2"))
	if len(got) != 1 || !strings.Contains(got[0].Text, "Ticket 2") || len(got[0].Buttons) != 1 ||
		got[0].Buttons[0].Title != "Assign to me" || !strings.HasPrefix(got[0].Buttons[0].ID, "tb:") {
		t.Fatalf("row chosen: %+v", got)
	}
	press := got[0].Buttons[0].ID
	// Chosen again, the same button.
	if again := w.send(t, adaOn, whatsapptest.ListReply(last.List[1].ID, "Ticket 2")); len(again) != 1 || len(again[0].Buttons) != 1 || again[0].Buttons[0].ID != press {
		t.Fatalf("chosen twice: %+v", again)
	}

	// Pressed: the assign tool, filled in; it only reads, so it runs.
	got = w.send(t, adaOn, whatsapptest.ButtonReply(press, "Assign to me"))
	if len(got) != 1 || !strings.Contains(got[0].Text, "Starting Assign a ticket") || !strings.Contains(got[0].Text, "T-2") {
		t.Fatalf("pressed: %+v", got)
	}
	w.env.Drain(t)
	w.tick(t)
	if last := w.graph.Last(t, adaOn); !strings.Contains(last.Text, "Yours: T-2") {
		t.Errorf("assigned: %+v", last)
	}
	// Once: the button, pressed by someone else, says who took it.
	if got := w.send(t, boOn, whatsapptest.ButtonReply(press, "Assign to me")); len(got) != 1 || !strings.Contains(got[0].Text, "Already done") {
		t.Errorf("taken: %+v", got)
	}

	// Outside the 24-hour window: the run-outcome template with Open, no
	// list.
	w.say(t, adaOn, "open-tickets")
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE whatsapp_contacts SET last_inbound_at = now() - interval '25 hours' WHERE number = $1`, adaOn); err != nil {
		t.Fatal(err)
	}
	w.env.Drain(t)
	w.tick(t)
	if last := w.graph.Last(t, adaOn); last.Template != "taskiem_run_completed" || len(last.List) != 0 || len(last.Buttons) != 0 {
		t.Errorf("outside the window: %+v", last)
	}
}

// A list answer in Telegram: one message, the rows as lines and a button
// for each row's action; pressed, the assign tool opens filled in and
// runs.
func TestToolListRowsTelegram(t *testing.T) {
	w := newTGWorld(t)
	owner := w.tenant(t, "Acme", "owner@tgrows.test")
	publishTicketTools(t, owner, 3)
	ada := w.member(t, owner, "ada@tgrows.test", "operator")
	const on = int64(7201)
	w.link(t, ada, on, "ada")

	if m := w.say(t, on, "open-tickets"); !strings.Contains(m.Text, "Starting Open tickets") {
		t.Fatalf("echo: %+v", m)
	}
	w.env.Drain(t)
	w.tick(t)
	m := w.bot.Last(t, on)
	if !strings.Contains(m.Text, "Ticket 3 · T-3") || len(m.Keyboard) != 3 || m.Keyboard[1][0].Text != "Ticket 2 · Assign to me" {
		t.Fatalf("list: %+v", m)
	}
	got := w.update(t, on, func(id int64) []byte { return telegramtest.Callback(id, on, m.MessageID, "q1", m.Keyboard[1][0].Data) })
	said := ""
	for _, c := range got {
		if c.Method == "sendMessage" {
			said += c.Text
		}
	}
	if !strings.Contains(said, "Starting Assign a ticket") || !strings.Contains(said, "T-2") {
		t.Fatalf("pressed: %+v", got)
	}
	w.env.Drain(t)
	w.tick(t)
	if last := w.bot.Last(t, on); !strings.Contains(last.Text, "Yours: T-2") {
		t.Errorf("assigned: %+v", last)
	}
}
