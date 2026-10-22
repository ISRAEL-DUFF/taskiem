package publicweb

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/brand"
)

// TestWritePreview draws the main pages in every theme into the folder
// TASKIEM_PREVIEW_DIR names, with the files they load, for a person to
// look at: serve the folder (python3 -m http.server) and open
// /index.html. Skipped without it.
func TestWritePreview(t *testing.T) {
	dir := os.Getenv("TASKIEM_PREVIEW_DIR")
	if dir == "" {
		t.Skip("TASKIEM_PREVIEW_DIR is not set")
	}
	color := os.Getenv("TASKIEM_PREVIEW_COLOR")
	write := func(name string, body []byte) {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, a := range assets {
		write("_static/"+name, a.body)
	}
	links := regexp.MustCompile(`/_brand/1/([0-9a-f]{6})\.css`)
	var index strings.Builder
	index.WriteString("<!doctype html><meta charset=utf-8><title>Themes</title><h1>Themes</h1>")
	for _, theme := range brand.Themes {
		b := brand.Brand{Theme: theme, Color: color}
		pages := map[string]string{}

		p := previewShop()
		e := formServer(t, p)
		withBrand(e.s, b)
		e.s.Storefronts = func(context.Context, uuid.UUID) (StorefrontPage, bool, error) { return p, true, nil }
		pages["storefront"] = get(e.h, "GET", sfHost, "/").Body.String()
		pages["order"], _ = e.form(t, "/a/order?item=2")
		pages["enquiry"], _ = e.form(t, "/a/enquiry")

		be, _ := bookingEnv(t, "after_accept", 0)
		withBrand(be.s, b)
		pages["services"] = get(be.h, "GET", sfHost, "/").Body.String()
		pages["booking"], _ = be.form(t, "/a/booking?service=7")

		h := newFormHost(t)
		withBrand(h.s, b)
		h.add("loan00000001", loanTrigger, loanSchema)
		h.add("widgets00001", `{"title":"Everything"}`, widgetSchema)
		pages["hosted-form"] = h.get("/f/loan00000001").Body.String()
		pages["hosted-widgets"] = h.get("/f/widgets00001").Body.String()
		pages["notfound"] = get(e.h, "GET", "nobody.shops.test", "/").Body.String()

		index.WriteString("<h2>" + theme + "</h2><ul>")
		for name, body := range pages {
			for _, m := range links.FindAllStringSubmatch(body, -1) {
				write("_brand/1/"+m[1]+".css", []byte(brand.PaletteOf("#"+m[1]).CSS()))
			}
			write(theme+"/"+name+".html", []byte(body))
			index.WriteString(`<li><a href="` + theme + "/" + name + `.html">` + name + "</a>")
		}
		index.WriteString("</ul>")
	}
	write("index.html", []byte(index.String()))
}

// previewShop is the sample shop with every form, as a real one would
// look.
func previewShop() StorefrontPage {
	p := SampleShop("Adunni Bakes")
	p.Model.Actions = allForms()
	return p
}

func TestPreviewPage(t *testing.T) {
	e := newFormHost(t)
	for _, page := range PreviewPages {
		path, err := PreviewPath(*e.s.FormKeys, Preview{Theme: "bold", Color: "#123456", Name: "Glow <Studio>", Logo: "https://img.test/l.jpg",
			Expires: e.now.Add(PreviewTTL)}, page)
		if err != nil {
			t.Fatal(err)
		}
		w := e.get(path)
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, "theme-bold.") || !strings.Contains(body, "/_brand/1/123456.css") || !strings.Contains(body, "Glow &lt;Studio&gt;") {
			t.Fatalf("%s: %d\n%s", page, w.Code, body)
		}
		if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors https://app.taskiem.test") || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: headers %v", page, w.Header())
		}
		if page == "storefront" && !strings.Contains(body, `src="https://img.test/l.jpg"`) {
			t.Error("the preview has no logo")
		}
	}
	// A preview is made by the api role only, and does not last.
	for _, path := range []string{"/_preview/storefront?p=" + "eyJ0aGVtZSI6ImJvbGQifQ", "/_preview/storefront", "/_preview/admin?p=x"} {
		if w := e.get(path); w.Code != 404 {
			t.Errorf("%s: %d", path, w.Code)
		}
	}
	old, _ := PreviewPath(*e.s.FormKeys, Preview{Theme: "bold", Name: "Old", Expires: e.now.Add(PreviewTTL)}, "storefront")
	e.tick(PreviewTTL + time.Minute)
	if w := e.get(old); w.Code != 404 {
		t.Errorf("an expired preview: %d", w.Code)
	}
}
