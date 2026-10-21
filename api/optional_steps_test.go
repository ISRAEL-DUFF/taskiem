package api_test

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/embedsites"
	"github.com/israel-duff/taskiem/engine/storefront"
)

type failingProver struct{}

func (failingProver) Prove(context.Context, embedsites.Site) (bool, bool, error) {
	return false, false, nil
}

// The optional technical steps, guided (no-technical-steps N9): Check now
// says which record is missing, a domain that stops being proven reaches
// the owner in chat, and the web designer gets the code and the proof by
// email.
func TestOptionalStepsGuided(t *testing.T) {
	w := shopWorld(t)
	ctx := context.Background()
	txt, hosts, cnames := map[string][]string{}, map[string][]string{}, map[string]string{}
	nx := &net.DNSError{Err: "no such host", IsNotFound: true}
	oldTXT, oldCNAME, oldHost := api.LookupTXT, api.LookupCNAME, api.LookupHost
	api.LookupTXT = func(_ context.Context, n string) ([]string, error) {
		if v, ok := txt[n]; ok {
			return v, nil
		}
		return nil, nx
	}
	api.LookupCNAME = func(_ context.Context, n string) (string, error) {
		if v, ok := cnames[n]; ok {
			return v, nil
		}
		return "", nx
	}
	api.LookupHost = func(_ context.Context, n string) ([]string, error) {
		if v, ok := hosts[n]; ok {
			return v, nil
		}
		return nil, nx
	}
	t.Cleanup(func() { api.LookupTXT, api.LookupCNAME, api.LookupHost = oldTXT, oldCNAME, oldHost })
	w.srv.SiteProver = &embedsites.Prover{LookupTXT: api.LookupTXT}
	w.srv.PublicURL = "https://app.taskiem.test"
	w.srv.DomainTarget = storefront.DomainTarget{Addresses: []string{"203.0.113.7"}}
	owner := w.tenant(t, "Ada's Kitchen", "ada@ada.test")
	verifyPhone(t, w.world, owner, "phone", "business")
	const on = "+2348010000091"
	w.bind(t, owner, on)
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-kitchen", "draft": map[string]any{"name": "Ada's Kitchen", "template": "classic", "accent": "green"}})
	owner.must(200, "POST", "/v1/storefront/publish", nil)

	// Check now: each record, what is wrong with it, in the names
	// registrars ask for.
	d := owner.must(201, "POST", "/v1/storefront/domains", map[string]any{"domain": "adakitchen.ng"})
	id, token := d["id"].(string), d["token"].(string)
	if recs := toJSON(d["records"]); !strings.Contains(recs, `"short":"_taskiem-verify"`) || !strings.Contains(recs, `"short":"@"`) || !strings.Contains(recs, `"short":"www"`) {
		t.Errorf("short names: %s", recs)
	}
	hosts["adakitchen.ng"] = []string{"198.51.100.1"}
	c := owner.must(200, "POST", "/v1/storefront/domains/"+id+"/check", nil)
	says := toJSON(c["checks"])
	if c["all_ok"] != false || c["proven"] != false || !strings.Contains(says, "The TXT record named _taskiem-verify is not there yet") ||
		!strings.Contains(says, "adakitchen.ng points somewhere else (198.51.100.1)") || !strings.Contains(says, "The CNAME record for www is not there yet") {
		t.Fatalf("check: %s", toJSON(c))
	}
	txt["_taskiem-verify.adakitchen.ng"] = []string{token}
	hosts["adakitchen.ng"] = []string{"203.0.113.7"}
	cnames["www.adakitchen.ng"] = "pages." + shopDomain + "."
	if c := owner.must(200, "POST", "/v1/storefront/domains/"+id+"/check", nil); c["all_ok"] != true || c["proven"] != true {
		t.Fatalf("all there: %s", toJSON(c))
	}
	owner.must(200, "POST", "/v1/storefront/domains/"+id+"/verify", nil)

	// The record goes missing for a day: the domain stops, and the owner
	// hears of it on WhatsApp with what to check.
	now := time.Now()
	checker := &storefront.DomainChecker{Pool: w.env.DB.App, Prover: failingProver{}, Every: time.Nanosecond, Failures: 2, Grace: time.Hour,
		PublicURL: "https://app.taskiem.test", Now: func() time.Time { return now }}
	var tenant uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id FROM tenants WHERE name = 'Ada''s Kitchen'`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		now = now.Add(2 * time.Hour)
		if err := checker.Check(ctx, tenant); err != nil {
			t.Fatal(err)
		}
	}
	if l := owner.must(200, "GET", "/v1/storefront/domains", nil)["domains"].([]any); l[0].(map[string]any)["status"] != "unverified" {
		t.Fatalf("not unverified: %v", l)
	}
	var kind string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT kind FROM alerts WHERE tenant_id = $1`, tenant).Scan(&kind); err != nil || kind != alerts.DomainUnverified {
		t.Fatalf("alert: %s %v", kind, err)
	}
	w.tick(t)
	if got := toJSON(w.graph.Last(t, on)); !strings.Contains(got, "Your page stopped showing on adakitchen.ng") {
		t.Errorf("the owner was not told in chat: %s", got)
	}

	// Send to my web designer: the code and the proof, by email.
	site := owner.must(201, "POST", "/v1/embed-sites", map[string]any{"origin": "adakitchen.ng", "method": "dns"})
	sid := site["id"].(string)
	if toJSON(site["prove"]) == "" || site["prove"].(map[string]any)["record_short"] != "_taskiem-verify" {
		t.Errorf("prove: %v", site["prove"])
	}
	owner.must(409, "POST", "/v1/embed-sites/"+sid+"/send-to-designer", map[string]any{"email": "tunde@webdesign.test"})
	mail := &fakeMail{}
	w.srv.Alerts = &alerts.Alerter{Mailer: mail, From: "no-reply@taskiem.test"}
	owner.must(400, "POST", "/v1/embed-sites/"+sid+"/send-to-designer", map[string]any{"email": "tunde"})
	owner.must(200, "POST", "/v1/embed-sites/"+sid+"/send-to-designer", map[string]any{"email": "Tunde@WebDesign.test"})
	sent := mail.all()
	if len(sent) != 1 || !strings.Contains(sent[0], "tunde@webdesign.test") || !strings.Contains(sent[0], "_taskiem-verify.adakitchen.ng") ||
		!strings.Contains(sent[0], site["token"].(string)) || !strings.Contains(sent[0], "ada-kitchen."+shopDomain+"/embed") {
		t.Fatalf("the email: %v", sent)
	}
	if audit := toJSON(owner.must(200, "GET", "/v1/audit", nil)); !strings.Contains(audit, "embed_site.sent_to_designer") || strings.Contains(audit, "tunde@") {
		t.Errorf("audit: %s", audit)
	}

	// The website loses its proof: the owner hears of that too.
	txt["_taskiem-verify.adakitchen.ng"] = []string{site["token"].(string)}
	owner.must(200, "POST", "/v1/embed-sites/"+sid+"/verify", nil)
	sites := &embedsites.Checker{Pool: w.env.DB.App, Prover: failingProver{}, Every: time.Nanosecond, Failures: 2, Grace: time.Hour, Now: func() time.Time { return now }}
	for range 2 {
		now = now.Add(2 * time.Hour)
		if err := sites.Check(ctx, tenant); err != nil {
			t.Fatal(err)
		}
	}
	w.tick(t)
	if got := toJSON(w.graph.Last(t, on)); !strings.Contains(got, "Your forms stopped showing on adakitchen.ng") {
		t.Errorf("the owner was not told of the website: %s", got)
	}
}
