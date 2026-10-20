package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/embedsites"
)

func TestEmbedSites(t *testing.T) {
	w := newWorld(t)
	txt := map[string][]string{}
	w.srv.SiteProver = &embedsites.Prover{LookupTXT: func(_ context.Context, name string) ([]string, error) { return txt[name], nil }}
	w.srv.PublicURL = "https://app.taskiem.test"
	w.srv.PublicHosts = []string{"shops.taskiem.test"}
	owner := w.tenant(t, "Ada's Kitchen", "ada@ada.test")

	for _, bad := range []string{"http://adakitchen.ng", "https://102.89.1.2", "https://ada.shops.taskiem.test", "https://app.taskiem.test", "https://gtbank-support.com"} {
		owner.must(400, "POST", "/v1/embed-sites", map[string]any{"origin": bad})
	}
	owner.must(400, "POST", "/v1/embed-sites", map[string]any{"origin": "adakitchen.ng", "method": "email"})
	site := owner.must(201, "POST", "/v1/embed-sites", map[string]any{"origin": "https://www.AdaKitchen.ng/shop"})
	id, token := site["id"].(string), site["token"].(string)
	prove := site["prove"].(map[string]any)
	if site["origin"] != "https://www.adakitchen.ng" || site["verified_at"] != nil || prove["record_name"] != "_taskiem-verify.www.adakitchen.ng" || prove["record_value"] != token {
		t.Fatalf("%v", site)
	}
	owner.must(409, "POST", "/v1/embed-sites", map[string]any{"origin": "www.adakitchen.ng"})

	// Not proven yet: 409, and nothing changes.
	owner.must(409, "POST", "/v1/embed-sites/"+id+"/verify", nil)
	txt["_taskiem-verify.www.adakitchen.ng"] = []string{"taskiem-site-verify=someone-elses"}
	owner.must(409, "POST", "/v1/embed-sites/"+id+"/verify", nil)
	txt["_taskiem-verify.www.adakitchen.ng"] = []string{token}
	if v := owner.must(200, "POST", "/v1/embed-sites/"+id+"/verify", nil); v["verified"] != true {
		t.Fatalf("%v", v)
	}
	list := owner.must(200, "GET", "/v1/embed-sites", nil)
	sites := list["sites"].([]any)
	if len(sites) != 1 || sites[0].(map[string]any)["verified_at"] == nil || list["max"] != float64(embedsites.MaxSites) {
		t.Fatalf("%v", list)
	}

	// A file proof shows where the file goes.
	file := owner.must(201, "POST", "/v1/embed-sites", map[string]any{"origin": "adakitchen.ng", "method": "file"})
	if !strings.HasSuffix(file["prove"].(map[string]any)["file_url"].(string), "adakitchen.ng/.well-known/taskiem-site.txt") {
		t.Fatalf("%v", file)
	}

	// Another organisation sees none of it and cannot verify or remove it.
	other := w.tenant(t, "Bola Bakes", "bola@bola.test")
	if s := other.must(200, "GET", "/v1/embed-sites", nil)["sites"].([]any); len(s) != 0 {
		t.Fatalf("%v", s)
	}
	other.must(404, "POST", "/v1/embed-sites/"+id+"/verify", nil)
	other.must(404, "DELETE", "/v1/embed-sites/"+id, nil)

	// Builders cannot manage the sites (storefront.manage is owners' and admins').
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "bld@ada.test", "password": "correct horse battery", "roles": []string{"builder"}})
	builder := w.login(t, "bld@ada.test", "correct horse battery")
	builder.must(403, "GET", "/v1/embed-sites", nil)
	builder.must(403, "POST", "/v1/embed-sites", map[string]any{"origin": "bld.ng"})

	owner.must(204, "DELETE", "/v1/embed-sites/"+id, nil)
	owner.must(404, "DELETE", "/v1/embed-sites/"+id, nil)
	audit := toJSON(owner.must(200, "GET", "/v1/audit", nil))
	for _, a := range []string{"embed_site.add", "embed_site.verify", "embed_site.remove"} {
		if !strings.Contains(audit, a) {
			t.Errorf("no %s in the audit log", a)
		}
	}
}
