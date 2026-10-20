package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	formconn "github.com/israel-duff/taskiem/connectors/form"
	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/media"
)

type okScanner struct{}

func (okScanner) Scan(context.Context, []byte) (string, error) { return "", nil }

type noFiles struct{}

func (noFiles) PutFile(context.Context, uuid.UUID, []byte) (media.File, error) {
	return media.File{Path: "files/x.pdf", Type: "application/pdf", Size: 1}, nil
}
func (noFiles) SignURL(_ context.Context, _ uuid.UUID, p string, ttl time.Duration) (string, time.Time, error) {
	return "https://storage.test/" + p, time.Now().Add(ttl), nil
}

func formsWorld(t *testing.T) *world {
	w := newWorld(t)
	keys, _ := forms.NewKeys(bytes.Repeat([]byte{5}, 32))
	w.srv.Forms = &forms.Service{Pool: w.env.DB.App, Vault: w.env.Vault, Keys: keys, Files: noFiles{}, Start: w.env.Store.Start,
		Signal: w.env.Store.DeliverSignalOnce, FormHost: "forms.shops.taskiem.test", AppURL: "https://app.taskiem.test"}
	if err := w.env.Registry.Register(formconn.New(w.srv.Forms)); err != nil {
		t.Fatal(err)
	}
	return w
}

const membersForm = `{"schema":"wd/v1","id":"wf_leave","version":1,"name":"Leave",
 "trigger":{"type":"form","config":{"title":"Leave request","access":"members","after":{"mode":"message","message":"Sent to your manager."}}},
 "inputs":{"schema":{"type":"object","required":["days","reason"],"properties":{
  "who":{"type":"string","x-form":{"from":"member.email"}},"days":{"type":"integer","minimum":1},"reason":{"type":"string","maxLength":200}}}},
 "steps":[{"id":"note","type":"transform","config":{"output":{"who":"=trigger.body.who","kind":"=trigger.form.respondent.kind"}}}]}`

const linkForm = `{"schema":"wd/v1","id":"wf_vendor","version":1,"name":"Vendor",
 "trigger":{"type":"form","config":{"title":"Vendor bank details","access":"link"}},
 "inputs":{"schema":{"type":"object","required":["bank"],"properties":{"bank":{"type":"string"},"account_number":{"type":"string","x-pii":"account_number"}}}},
 "steps":[{"id":"n","type":"transform","config":{"output":{"b":"=trigger.body.bank"}}}]}`

const filesForm = `{"schema":"wd/v1","id":"wf_cv","version":1,"name":"CV",
 "trigger":{"type":"form","config":{"title":"Send your CV","access":"public"}},
 "inputs":{"schema":{"type":"object","properties":{"cv":{"type":"string","format":"file"}}}},
 "steps":[{"id":"n","type":"transform","config":{"output":{"x":1}}}]}`

const askRole = `{"schema":"wd/v1","id":"wf_expense","version":1,"name":"Expense","trigger":{"type":"manual"},
 "steps":[{"id":"check","type":"form","config":{"title":"Check the receipt","to":{"role":"finance"},
   "schema":{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"},"comment":{"type":"string"}}},"prefill":{"comment":"looks fine"}}},
  {"id":"done","type":"transform","needs":["check"],"config":{"output":{"ok":"=steps.check.output.ok"}}}]}`

func TestForms(t *testing.T) {
	w := formsWorld(t)
	ctx := context.Background()
	owner := w.tenant(t, "Acme", "owner@acme.test")

	// A members form: listed with its address in the app; rendered and
	// filled in there, the member's email locked in, the run theirs.
	publishFlow(t, owner, membersForm)
	prod := func(access string) map[string]any {
		t.Helper()
		for _, x := range owner.must(200, "GET", "/v1/forms", nil)["forms"].([]any) {
			if m := x.(map[string]any); m["access"] == access && m["environment"] == "prod" {
				return m
			}
		}
		t.Fatalf("no %s form in prod", access)
		return nil
	}
	f := prod("members")
	id := f["id"].(string)
	if f["access"] != "members" || f["title"] != "Leave request" || f["status"] != "live" || !strings.HasPrefix(f["url"].(string), "https://app.taskiem.test/forms/") {
		t.Fatalf("form: %v", f)
	}
	view := owner.must(200, "GET", "/v1/forms/"+id+"/view", nil)
	if view["title"] != "Leave request" || view["member"].(map[string]any)["email"] != "owner@acme.test" {
		t.Fatalf("view: %v", view)
	}
	owner.must(400, "POST", "/v1/forms/"+id+"/submissions", map[string]any{"answers": map[string]any{"days": 0, "reason": "x"}})
	sub := owner.must(201, "POST", "/v1/forms/"+id+"/submissions", map[string]any{"answers": map[string]any{"days": 2, "reason": "Wedding", "who": "someone@else.test"}})["submission"].(map[string]any)
	if sub["status"] != "started" || sub["run_id"] == nil || sub["channel"] != "app" {
		t.Fatalf("submitted: %v", sub)
	}
	w.env.Drain(t)
	var out []byte
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT payload FROM run_events WHERE run_id = $1 AND step_id = 'note' AND type = 'StepCompleted'`, sub["run_id"]).Scan(&out); err != nil {
		t.Fatal(err)
	}
	// The email is personal data: sealed in the run's history too.
	if bytes.Contains(out, []byte("acme.test")) || !bytes.Contains(out, []byte(`"$pii": "email"`)) || !bytes.Contains(out, []byte(`"kind": "member"`)) {
		t.Fatalf("the run read: %s", out)
	}
	subs := owner.must(200, "GET", "/v1/form-submissions?form="+id, nil)["submissions"].([]any)
	if len(subs) != 1 || strings.Contains(toJSON(subs), "acme.test") {
		t.Fatalf("submissions: %v", subs)
	}
	// Revealed (pii.reveal, audited): the member's own email, not the one typed.
	one := owner.must(200, "GET", "/v1/form-submissions/"+sub["id"].(string)+"?reveal=true", nil)["submission"].(map[string]any)
	if one["answers"].(map[string]any)["who"] != "owner@acme.test" || one["answers"].(map[string]any)["reason"] != "Wedding" {
		t.Fatalf("revealed: %v", one)
	}

	// Pausing and closing; links only for link forms; no links in messages.
	if p := owner.must(200, "PATCH", "/v1/forms/"+id, map[string]any{"status": "paused", "closed_message": "Back on Monday"}); p["status"] != "paused" {
		t.Fatalf("paused: %v", p)
	}
	owner.must(409, "POST", "/v1/forms/"+id+"/submissions", map[string]any{"answers": map[string]any{"days": 1, "reason": "x"}})
	owner.must(400, "PATCH", "/v1/forms/"+id, map[string]any{"closed_message": "see https://evil.test"})
	owner.must(400, "PATCH", "/v1/forms/"+id, map[string]any{"status": "suspended"})
	owner.must(409, "POST", "/v1/forms/"+id+"/links", map[string]any{"count": 1})

	// A link form: one-time links, bound to a person or not, shown once.
	publishFlow(t, owner, linkForm)
	linkID := prod("link")["id"].(string)
	owner.must(400, "POST", "/v1/forms/"+linkID+"/links", map[string]any{"count": 2, "to": []any{map[string]any{"phone": "08031234567"}}})
	owner.must(400, "POST", "/v1/forms/"+linkID+"/links", map[string]any{"to": []any{map[string]any{"phone": "12345"}}})
	links := owner.must(201, "POST", "/v1/forms/"+linkID+"/links", map[string]any{"to": []any{map[string]any{"phone": "08031234567"}, map[string]any{"email": "Vendor@Example.com"}}})["links"].([]any)
	l0 := links[0].(map[string]any)
	if len(links) != 2 || l0["to"] != "•••4567" || !strings.HasPrefix(l0["url"].(string), "https://forms.shops.taskiem.test/r/") || links[1].(map[string]any)["to"] != "v•••@example.com" {
		t.Fatalf("links: %v", links)
	}
	token := strings.TrimPrefix(l0["url"].(string), "https://forms.shops.taskiem.test/r/")
	var usable bool
	if err := w.env.DB.PublicPool(t, 1).QueryRow(ctx, `SELECT usable FROM taskiem_public_form_link($1)`, forms.TokenHash(token)).Scan(&usable); err != nil || !usable {
		t.Fatalf("the link: %v %v", usable, err)
	}
	approver := addMember(t, w, owner, "app", "approver")
	approver.must(403, "POST", "/v1/forms/"+linkID+"/links", map[string]any{"count": 1})
	approver.must(409, "GET", "/v1/forms/"+id+"/view", nil) // paused

	// Files need a scanner: without one, the form is not deployed.
	dup := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "cv", "definition": json.RawMessage(filesForm)})
	if r := owner.must(409, "POST", "/v1/workflows/"+dup["id"].(string)+"/versions/1/publish", nil); !strings.Contains(toJSON(r), "malware scanner") {
		t.Fatalf("no scanner: %v", r)
	}
	w.srv.Forms.Scanner = okScanner{}
	owner.must(200, "POST", "/v1/workflows/"+dup["id"].(string)+"/versions/1/publish", nil)

	// A form step for a role: members of it see it, answer it, the run goes on.
	wf := publishFlow(t, owner, askRole)
	run := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{}})
	w.env.Drain(t)
	finance := addMember(t, w, owner, "fin", "finance")
	reqs := finance.must(200, "GET", "/v1/form-requests", nil)["requests"].([]any)
	if len(reqs) != 1 {
		t.Fatalf("requests: %v", reqs)
	}
	rid := reqs[0].(map[string]any)["id"].(string)
	if len(approver.must(200, "GET", "/v1/form-requests", nil)["requests"].([]any)) != 0 {
		t.Fatal("an approver sees finance's request")
	}
	approver.must(404, "GET", "/v1/form-requests/"+rid, nil)
	if r := finance.must(200, "GET", "/v1/form-requests/"+rid, nil); r["prefill"].(map[string]any)["comment"] != "looks fine" {
		t.Fatalf("request: %v", r)
	}
	finance.must(201, "POST", "/v1/form-requests/"+rid+"/answers", map[string]any{"answers": map[string]any{"ok": true}})
	w.env.Drain(t)
	if st := owner.must(200, "GET", "/v1/runs/"+run["run_id"].(string), nil)["run"].(map[string]any)["status"]; st != "completed" {
		t.Fatalf("run: %v", st)
	}
	finance.must(409, "POST", "/v1/form-requests/"+rid+"/answers", map[string]any{"answers": map[string]any{"ok": false}})

	audit := toJSON(owner.must(200, "GET", "/v1/audit", nil))
	for _, a := range []string{"form.paused", "form.links_minted", "form_submission:"} {
		if !strings.Contains(audit, a) {
			t.Errorf("no %s in the audit log", a)
		}
	}
}
