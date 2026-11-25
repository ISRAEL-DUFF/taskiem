package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/notify"
	"github.com/israel-duff/taskiem/engine/sender"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// A saved file told to the owner on WhatsApp (H6 review): inside the
// window the message, the file's name and its link; outside it the alert
// template, whose link is the file's, so a long message cut to fit the
// template's line never cuts the link.
func TestSavedFileByChat(t *testing.T) {
	w := newWAWorld(t)
	ctx := context.Background()
	w.srv.Media = newProjectStorage(t).service()
	w.srv.Media.Scanner = chatScanner{}
	if err := w.env.Registry.Register(notify.New(&notify.Notifier{Pool: w.env.Store.Pool, Chat: []string{"whatsapp"},
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)},
		Files:    api.SavedFileLinks{Pool: w.env.Store.Pool, BaseURL: waPublicURL}})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada Stores", "ada@savedchat.test")
	const on = "+2348010000077"
	w.bind(t, owner, on)
	_, up := owner.postFile(t, "/v1/saved/uploads", "prices.png", pngPhoto(t, 70), nil)
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "price-list", "title": "Price list", "kind": "file",
		"details": map[string]any{"file": up["upload"].(map[string]any)["file"]}})
	long := strings.Repeat("Prices went up again this week. ", 20)
	def := `{"schema":"wd/v1","id":"wf_tellfile","version":1,"name":"Tell the prices","trigger":{"type":"manual"},
	 "steps":[{"id":"owner","type":"connector","connector":"notify@1","action":"tell",
	   "input":{"title":"This week's prices","message":"` + long + `","file":"=saved.price_list"}}]}`
	wf := publishFlow(t, owner, def)
	link := waPublicURL + "/v1/saved-files/"

	// Inside the window: the message, the file's name, its link.
	w.say(t, on, "status")
	owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{}})
	w.env.Drain(t)
	w.tick(t)
	got := w.graph.Last(t, on)
	if got.Template != "" || !strings.Contains(got.Text, "prices.png") || !strings.Contains(got.Text, link) {
		t.Fatalf("inside the window: %+v", got)
	}
	// Outside it: the template, the link whole in its own place.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE whatsapp_contacts SET last_inbound_at = now() - interval '25 hours' WHERE number = $1`, on); err != nil {
		t.Fatal(err)
	}
	owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{}})
	w.env.Drain(t)
	w.tick(t)
	got = w.graph.Last(t, on)
	i := strings.Index(got.Text, link)
	if got.Template == "" || i < 0 || len(strings.Fields(got.Text[i+len(link):])[0]) != 43 {
		t.Fatalf("outside the window: %+v", got)
	}
}

// flakyFiles fails its first n calls as a passing outage would, then
// makes links as Taskiem does.
type flakyFiles struct {
	n    int
	real notify.FileLinks
}

func (f *flakyFiles) FileLink(ctx context.Context, tx pgx.Tx, tenant, run uuid.UUID, ref string) (string, string, error) {
	if f.n > 0 {
		f.n--
		return "", "", errors.New("the database is away for a moment")
	}
	return f.real.FileLink(ctx, tx, tenant, run, ref)
}

// A link that cannot be made for a moment keeps the message to try again,
// never drops it; one that can never be made is nothing to send (H6
// review). A saved file is only ever one the run read: a made-up kind's
// field named file is its own (field_file), and a reference to another
// of the business's files is refused; a template of a step's own and a
// file are not taken together.
func TestSavedFileLinkFailures(t *testing.T) {
	w := newWAWorld(t)
	ctx := context.Background()
	w.srv.Media = newProjectStorage(t).service()
	w.srv.Media.Scanner = chatScanner{}
	flaky := &flakyFiles{n: 1, real: api.SavedFileLinks{Pool: w.env.Store.Pool, BaseURL: waPublicURL}}
	w.srv.SavedFiles = flaky
	if err := w.env.Registry.Register(notify.New(&notify.Notifier{Pool: w.env.Store.Pool, Chat: []string{"whatsapp"},
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)},
		Files:    api.SavedFileLinks{Pool: w.env.Store.Pool, BaseURL: waPublicURL}})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada Stores", "ada@savedflaky.test")
	const on = "+2348010000078"
	w.bind(t, owner, on)
	_, up := owner.postFile(t, "/v1/saved/uploads", "prices.png", pngPhoto(t, 70), nil)
	file := owner.must(201, "POST", "/v1/saved", map[string]any{"name": "price-list", "title": "Price list", "kind": "file",
		"details": map[string]any{"file": up["upload"].(map[string]any)["file"]}})["saved"].(map[string]any)
	def := `{"schema":"wd/v1","id":"wf_flaky","version":1,"name":"Tell the prices","trigger":{"type":"manual"},
	 "steps":[{"id":"owner","type":"connector","connector":"notify@1","action":"tell",
	   "input":{"title":"Prices","message":"This week's prices","file":"=saved.price_list"}}]}`
	wf := publishFlow(t, owner, def)
	w.say(t, on, "status")
	before := len(w.graph.Sent(on))
	owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{}})
	w.env.Drain(t)
	w.tick(t)
	var status string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status FROM whatsapp_outbox WHERE kind = 'notice'`).Scan(&status); err != nil || status != "pending" || len(w.graph.Sent(on)) != before {
		t.Fatalf("after a passing failure: %q %v, %d sent", status, err, len(w.graph.Sent(on))-before)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE whatsapp_outbox SET next_attempt_at = now() WHERE kind = 'notice'`); err != nil {
		t.Fatal(err)
	}
	w.tick(t)
	if got := w.graph.Last(t, on); !strings.Contains(got.Text, waPublicURL+"/v1/saved-files/") {
		t.Fatalf("tried again: %+v", got)
	}

	// Never a file the run did not read, by its id.
	if _, _, err := (api.SavedFileLinks{Pool: w.env.Store.Pool, BaseURL: waPublicURL}).FileLink(ctx, nil, tenantID(t, owner), uuid.New(), notify.FileRefPrefix+file["id"].(string)); !errors.Is(err, notify.ErrNoFile) {
		t.Errorf("another run's file: %v", err)
	}
	// Nor from a Taskiem with no address for its links.
	if _, _, err := (api.SavedFileLinks{Pool: w.env.Store.Pool}).FileLink(ctx, nil, tenantID(t, owner), uuid.New(), notify.FileRefPrefix+file["id"].(string)); !errors.Is(err, notify.ErrFilesOff) {
		t.Errorf("no address: %v", err)
	}
	// A made-up kind's field named file is its own, never a saved file.
	k := owner.must(201, "POST", "/v1/saved-kinds", map[string]any{"name": "Supplier", "fields": []map[string]any{{"label": "File", "type": "text"}}})["kind"].(map[string]any)
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "dangote", "title": "Dangote", "kind": "custom", "kind_id": k["id"],
		"values": map[string]any{"file": notify.FileRefPrefix + file["id"].(string)}})
	sneaky := strings.NewReplacer(`"wf_flaky"`, `"wf_sneaky"`, `"=saved.price_list"`, `"=saved.dangote.file"`).Replace(def)
	swf := publishFlow(t, owner, sneaky)
	run := owner.must(201, "POST", "/v1/workflows/"+swf+"/runs", map[string]any{"input": map[string]any{}})["run_id"].(string)
	w.env.Drain(t)
	got := owner.must(200, "GET", "/v1/runs/"+run, nil)
	if st := got["run"].(map[string]any)["status"]; st != "failed" || !strings.Contains(toJSON(got["events"]), "field_file") {
		t.Fatalf("a made-up kind's file: %v %s", st, toJSON(got["events"]))
	}
	// A template of the step's own has no place for the link.
	withTemplate := strings.Replace(def, `"title":"Prices",`, `"template":"taskiem_daily_close","template_vars":{"day":"Monday","summary":"x"},"title":"Prices",`, 1)
	if out := owner.must(200, "POST", "/v1/validate", map[string]any{"definition": json.RawMessage(withTemplate)}); out["valid"] != false || !strings.Contains(toJSON(out), "cannot send a saved file") {
		t.Fatalf("a template and a file: %v", out)
	}
}
