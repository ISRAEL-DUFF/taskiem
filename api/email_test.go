package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	emailconn "github.com/israel-duff/taskiem/connectors/email"
	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/inmail"
)

const emailFlow = `{"schema":"wd/v1","id":"wf_alerts","version":1,"name":"Bank alerts",
 "trigger":{"type":"email","config":{"senders":["firstbank.example"]}},
 "steps":[{"id":"n","type":"transform","config":{"output":{"s":"=trigger.body.subject"}}}]}`

func TestEmailTrigger(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")

	// Without a mail domain, an email trigger cannot go live.
	wfID := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "alerts", "definition": json.RawMessage(emailFlow)})["id"].(string)
	owner.must(409, "POST", "/v1/workflows/"+wfID+"/versions/1/publish", nil)

	keys, _ := forms.NewKeys(bytes.Repeat([]byte{5}, 32))
	w.srv.Email = &inmail.Service{Pool: w.env.DB.App, Keys: keys, Start: w.env.Store.Start, Domain: "in.taskiem.test"}
	w.srv.MailDomain = "in.taskiem.test"
	if err := w.env.Registry.Register(emailconn.New(w.srv.Email)); err != nil {
		t.Fatal(err)
	}
	owner.must(200, "POST", "/v1/workflows/"+wfID+"/versions/1/publish", nil)

	got := owner.must(200, "GET", "/v1/workflows/"+wfID+"/email", nil)
	eps := got["endpoints"].([]any)
	if got["receiving"] != true || len(eps) == 0 {
		t.Fatalf("email: %v", got)
	}
	var prod map[string]any
	for _, e := range eps {
		if m := e.(map[string]any); m["environment"] == "prod" {
			prod = m
		}
	}
	addr, _ := prod["address"].(string)
	if !strings.HasSuffix(addr, "@in.taskiem.test") || len(addr) != len("@in.taskiem.test")+12 || prod["status"] != "live" ||
		len(prod["messages"].([]any)) != 0 || prod["senders"].([]any)[0] != "firstbank.example" || prod["attachments"] != "ignore" {
		t.Fatalf("prod endpoint: %v", prod)
	}

	// The web addresses tab lists it.
	trig := owner.must(200, "GET", "/v1/workflows/"+wfID+"/triggers", nil)["triggers"].([]any)
	found := false
	for _, x := range trig {
		if m := x.(map[string]any); m["type"] == "email" && m["environment"] == "prod" && m["email"] == addr {
			found = true
		}
	}
	if !found {
		t.Fatalf("triggers: %v", trig)
	}

	// A sample email starts a run; it is listed with what came of it.
	sample := owner.must(200, "POST", "/v1/workflows/"+wfID+"/email/prod/test", nil)
	if sample["run_id"] == nil || sample["note"] != "" {
		t.Fatalf("sample: %v", sample)
	}
	got = owner.must(200, "GET", "/v1/workflows/"+wfID+"/email", nil)
	for _, e := range got["endpoints"].([]any) {
		m := e.(map[string]any)
		if m["environment"] != "prod" {
			continue
		}
		msgs := m["messages"].([]any)
		if len(msgs) != 1 || msgs[0].(map[string]any)["status"] != "started" || msgs[0].(map[string]any)["run_id"] != sample["run_id"] {
			t.Fatalf("messages: %v", msgs)
		}
	}

	// A new address replaces the old one, which stops taking mail.
	fresh := owner.must(200, "POST", "/v1/workflows/"+wfID+"/email/prod/address", nil)["address"].(string)
	if fresh == addr || !strings.HasSuffix(fresh, "@in.taskiem.test") {
		t.Fatalf("new address: %s (was %s)", fresh, addr)
	}
	st := inmail.DBStore{Pool: w.env.DB.PublicPool(t, 1)}
	if _, ok, _ := st.Recipient(t.Context(), strings.TrimSuffix(addr, "@in.taskiem.test")); ok {
		t.Error("the old address still takes mail")
	}
	if _, ok, _ := st.Recipient(t.Context(), strings.TrimSuffix(fresh, "@in.taskiem.test")); !ok {
		t.Error("the new address takes no mail")
	}

	// Attachments kept need a malware scanner.
	keep := strings.Replace(emailFlow, `"senders":["firstbank.example"]`, `"attachments":"keep"`, 1)
	v2 := owner.must(201, "POST", "/v1/workflows/"+wfID+"/versions", map[string]any{"definition": json.RawMessage(keep)})
	owner.must(409, "POST", "/v1/workflows/"+wfID+"/versions/"+fmt.Sprint(v2["version"])+"/publish", nil)

	// Undeployed, the address stops taking mail.
	owner.must(200, "DELETE", "/v1/workflows/"+wfID+"/deployments/prod", nil)
	if _, ok, _ := st.Recipient(t.Context(), strings.TrimSuffix(fresh, "@in.taskiem.test")); ok {
		t.Error("an undeployed workflow's address still takes mail")
	}

	// Members who may not publish cannot change the address.
	approver := addMember(t, w, owner, "app", "approver")
	approver.must(403, "POST", "/v1/workflows/"+wfID+"/email/prod/address", nil)
	approver.must(200, "GET", "/v1/workflows/"+wfID+"/email", nil)
}
