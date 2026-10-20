package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/embedsites"
	"github.com/israel-duff/taskiem/engine/storefront"
)

func TestStorefrontDomains(t *testing.T) {
	w := newWorld(t)
	txt := map[string][]string{}
	w.srv.SiteProver = &embedsites.Prover{LookupTXT: func(_ context.Context, name string) ([]string, error) { return txt[name], nil }}
	w.srv.PublicURL = "https://app.taskiem.test"
	w.srv.PublicHosts = []string{"shops.taskiem.test"}
	w.srv.DomainTarget = storefront.DomainTarget{Addresses: []string{"203.0.113.7"}}
	owner := w.tenant(t, "Ada's Kitchen", "ada@ada.test")
	verifyPhone(t, w, owner, "phone", "business") // publishing goes live at once (moderation.md)

	// A domain needs a storefront first.
	owner.must(409, "POST", "/v1/storefront/domains", map[string]any{"domain": "adakitchen.ng"})
	owner.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "ada-kitchen", "draft": map[string]any{"name": "Ada's Kitchen", "template": "classic", "accent": "green"}})
	pub := owner.must(200, "POST", "/v1/storefront/publish", nil)
	if pub["url"] != "https://ada-kitchen.shops.taskiem.test" || pub["domain"] != nil {
		t.Fatalf("%v", pub)
	}

	for _, bad := range []string{"http://adakitchen.ng", "102.89.1.2", "com.ng", "ada.shops.taskiem.test", "app.taskiem.test", "gtbank-support.com"} {
		owner.must(400, "POST", "/v1/storefront/domains", map[string]any{"domain": bad})
	}
	d := owner.must(201, "POST", "/v1/storefront/domains", map[string]any{"domain": "https://AdaKitchen.ng/"})
	id, token := d["id"].(string), d["token"].(string)
	if d["domain"] != "adakitchen.ng" || d["twin"] != "www.adakitchen.ng" || d["status"] != "pending" || !strings.HasPrefix(token, "taskiem-domain-verify=") {
		t.Fatalf("%v", d)
	}
	recs := toJSON(d["records"])
	for _, want := range []string{
		`"name":"_taskiem-verify.adakitchen.ng","purpose":"proof","type":"TXT","value":"` + token + `"`,
		`"name":"adakitchen.ng","purpose":"point","type":"A","value":"203.0.113.7"`,
		`"name":"www.adakitchen.ng","purpose":"point","type":"CNAME","value":"pages.shops.taskiem.test"`,
	} {
		if !strings.Contains(recs, want) {
			t.Errorf("no %s in %s", want, recs)
		}
	}
	owner.must(409, "POST", "/v1/storefront/domains", map[string]any{"domain": "www.adakitchen.ng"})

	// Not proven yet: 409; a primary needs a verified domain.
	owner.must(409, "POST", "/v1/storefront/domains/"+id+"/verify", nil)
	owner.must(409, "POST", "/v1/storefront/domains/"+id+"/primary", nil)
	txt["_taskiem-verify.adakitchen.ng"] = []string{"taskiem-domain-verify=someone-elses"}
	owner.must(409, "POST", "/v1/storefront/domains/"+id+"/verify", nil)

	// Another organisation adds it too and proves it first: it serves it.
	other := w.tenant(t, "Bola Bakes", "bola@bola.test")
	verifyPhone(t, w, other, "phone", "business") // publishing goes live at once (moderation.md)
	other.must(200, "PUT", "/v1/storefront", map[string]any{"slug": "bola-bakes", "draft": map[string]any{"name": "Bola Bakes", "template": "classic", "accent": "green"}})
	theirs := other.must(201, "POST", "/v1/storefront/domains", map[string]any{"domain": "adakitchen.ng"})
	txt["_taskiem-verify.adakitchen.ng"] = []string{token, theirs["token"].(string)}
	if v := owner.must(200, "POST", "/v1/storefront/domains/"+id+"/verify", nil); v["status"] != "live" || v["primary"] != true {
		t.Fatalf("%v", v)
	}
	other.must(409, "POST", "/v1/storefront/domains/"+theirs["id"].(string)+"/verify", nil)
	other.must(404, "DELETE", "/v1/storefront/domains/"+id, nil)
	if l := other.must(200, "GET", "/v1/storefront/domains", nil)["domains"].([]any); len(l) != 1 || l[0].(map[string]any)["status"] != "pending" {
		t.Fatalf("%v", l)
	}

	// The page's links use the domain now.
	if sf := owner.must(200, "GET", "/v1/storefront", nil); sf["url"] != "https://adakitchen.ng" || sf["domain"] != "adakitchen.ng" ||
		sf["preview_url"] != "https://ada-kitchen.shops.taskiem.test" {
		t.Fatalf("%v", sf)
	}

	// A second domain; the primary can move to it.
	sub := owner.must(201, "POST", "/v1/storefront/domains", map[string]any{"domain": "order.adakitchen.ng"})
	if sub["twin"] != nil || !strings.Contains(toJSON(sub["records"]), `"name":"order.adakitchen.ng","purpose":"point","type":"CNAME"`) {
		t.Fatalf("%v", sub)
	}
	txt["_taskiem-verify.order.adakitchen.ng"] = []string{sub["token"].(string)}
	if v := owner.must(200, "POST", "/v1/storefront/domains/"+sub["id"].(string)+"/verify", nil); v["primary"] != false {
		t.Fatalf("a second domain is not the primary: %v", v)
	}
	owner.must(200, "POST", "/v1/storefront/domains/"+sub["id"].(string)+"/primary", nil)
	list := owner.must(200, "GET", "/v1/storefront/domains", nil)
	if ds := list["domains"].([]any); len(ds) != 2 || ds[0].(map[string]any)["domain"] != "order.adakitchen.ng" || list["max"] != float64(storefront.MaxDomains) {
		t.Fatalf("%v", list)
	}
	if sf := owner.must(200, "GET", "/v1/storefront", nil); sf["url"] != "https://order.adakitchen.ng" {
		t.Fatalf("%v", sf["url"])
	}

	// Builders cannot manage domains.
	builder := addMember(t, w, owner, "bld", "builder")
	builder.must(403, "GET", "/v1/storefront/domains", nil)
	builder.must(403, "POST", "/v1/storefront/domains", map[string]any{"domain": "bld.ng"})

	// Removed: the other primary takes over; removing both, the preview serves.
	owner.must(204, "DELETE", "/v1/storefront/domains/"+sub["id"].(string), nil)
	if sf := owner.must(200, "GET", "/v1/storefront", nil); sf["url"] != "https://adakitchen.ng" {
		t.Fatalf("%v", sf["url"])
	}
	owner.must(204, "DELETE", "/v1/storefront/domains/"+id, nil)
	owner.must(404, "DELETE", "/v1/storefront/domains/"+id, nil)
	if sf := owner.must(200, "GET", "/v1/storefront", nil); sf["url"] != "https://ada-kitchen.shops.taskiem.test" {
		t.Fatalf("%v", sf["url"])
	}
	// Now the other organisation can prove it.
	if v := other.must(200, "POST", "/v1/storefront/domains/"+theirs["id"].(string)+"/verify", nil); v["status"] != "live" {
		t.Fatalf("%v", v)
	}
	audit := toJSON(owner.must(200, "GET", "/v1/audit", nil))
	for _, a := range []string{"storefront_domain.add", "storefront_domain.verify", "storefront_domain.primary", "storefront_domain.remove"} {
		if !strings.Contains(audit, a) {
			t.Errorf("no %s in the audit log", a)
		}
	}
}
