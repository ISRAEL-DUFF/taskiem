package publicweb

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/storefront"
)

func kobo(n int64) *int64 { return &n }

func samplePage(template string, items int) StorefrontPage {
	card := storefront.Card{Name: "Ada's <Kitchen>", Tagline: "Jollof & small chops", About: "Cooked fresh every day.", Area: "Yaba, Lagos",
		Address: "12 Herbert Macaulay Way", Phone: "+2348031234567", WhatsApp: "+2348031234567", Template: template, Accent: "orange", Language: "en"}
	card.Hours.Days[0] = []storefront.Span{{Open: "08:00", Close: "18:00"}}
	p := StorefrontPage{Status: "published", Model: storefront.Model{Version: 3, Slug: "ada-kitchen", Card: card, MapLink: card.MapLink(), WhatsLink: card.WhatsAppLink(),
		Logo: storefront.Image{"320": "https://ref.xb.test/storage/v1/public/taskiem-public/images/l/320.jpg", "800": "https://ref.xb.test/l/800.jpg"}, HasCatalog: true}}
	for i := range items {
		it := storefront.Item{ID: fmt.Sprint(i + 1), Name: fmt.Sprintf("Dish number %d", i+1), PriceKobo: kobo(int64(150000 + i*50)), Available: i%7 != 0,
			Description: "Rice, plantain and chicken, with a bottle of water.", Category: []string{"Rice", "Soups", "Drinks"}[i%3],
			Photo: storefront.Image{"320": fmt.Sprintf("https://ref.xb.test/storage/v1/public/taskiem-public/images/h%03d/320.jpg", i), "800": fmt.Sprintf("https://ref.xb.test/storage/v1/public/taskiem-public/images/h%03d/800.jpg", i)}}
		p.Items = append(p.Items, it)
	}
	return p
}

func storefrontServer(t *testing.T, p StorefrontPage, ok bool) (*Server, *fakeHosts) {
	t.Helper()
	id := uuid.New()
	f := &fakeHosts{sites: map[string]Site{"ada-kitchen.shops.test": {Tenant: uuid.New(), Kind: "storefront", ID: id}}}
	s, _ := newServer(f)
	s.Storefronts = func(_ context.Context, got uuid.UUID) (StorefrontPage, bool, error) {
		if got != id {
			t.Errorf("asked for storefront %s", got)
		}
		return p, ok, nil
	}
	monday10 := time.Date(2026, 10, 19, 10, 0, 0, 0, storefront.Lagos)
	s.now = func() time.Time { return monday10 }
	return s, f
}

func TestStorefrontPage(t *testing.T) {
	for _, tmpl := range []string{"classic", "bold"} {
		s, _ := storefrontServer(t, samplePage(tmpl, 4), true)
		w := get(s.Handler(), "GET", "ada-kitchen.shops.test", "/")
		body := w.Body.String()
		for _, want := range []string{
			"<title>Ada&#39;s &lt;Kitchen&gt;</title>", // escaped
			`class="sf sf-` + tmpl + ` accent-orange"`,
			"Open now", "₦1,500", "Dish number 2", "<h2>Soups</h2>", "Unavailable",
			`href="https://wa.me/2348031234567"`, `href="tel:&#43;2348031234567"`, "Directions",
			`property="og:image" content="https://ref.xb.test/l/800.jpg"`,
			`srcset="https://ref.xb.test/storage/v1/public/taskiem-public/images/h001/320.jpg 320w`,
			`loading="lazy"`, "Opening hours", "12 Herbert Macaulay Way",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: no %q", tmpl, want)
			}
		}
		if w.Code != 200 || strings.Contains(body, "<Kitchen>") {
			t.Fatalf("%s: %d", tmpl, w.Code)
		}
	}
}

func TestStorefrontClosedAndDraft(t *testing.T) {
	p := samplePage("classic", 3)
	p.Status, p.ClosedMessage = "closed", "Back on Monday"
	s, _ := storefrontServer(t, p, true)
	body := get(s.Handler(), "GET", "ada-kitchen.shops.test", "/").Body.String()
	if !strings.Contains(body, "Back on Monday") || strings.Contains(body, "Dish number 1") || !strings.Contains(body, "Chat on WhatsApp") {
		t.Fatal("a closed storefront shows its message and card, not its catalogue")
	}
	s, _ = storefrontServer(t, StorefrontPage{}, false)
	if body := get(s.Handler(), "GET", "ada-kitchen.shops.test", "/").Body.String(); !strings.Contains(body, "Coming soon") {
		t.Fatal("an unpublished storefront says it is coming")
	}
}

// TestStorefrontBudget: a big catalogue's page. Pages are always served
// compressed (Caddy's encode, the ingress), so what travels over 3G is
// the compressed size; the raw HTML has a ceiling of its own.
func TestStorefrontBudget(t *testing.T) {
	s, _ := storefrontServer(t, samplePage("bold", 100), true)
	body := get(s.Handler(), "GET", "ada-kitchen.shops.test", "/").Body.Bytes()
	var gz bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&gz, gzip.DefaultCompression)
	_, _ = zw.Write(body)
	_ = zw.Close()
	if gz.Len() > maxPageHTML || len(body) > 3*maxPageHTML {
		t.Fatalf("a 100-item storefront is %d bytes, %d compressed (budget %d compressed, %d raw)", len(body), gz.Len(), maxPageHTML, 3*maxPageHTML)
	}
	t.Logf("100 items: %d bytes, %d compressed", len(body), gz.Len())
}

func TestStorefrontQR(t *testing.T) {
	s, f := storefrontServer(t, samplePage("classic", 1), true)
	f.sites["adakitchen.ng"] = f.sites["ada-kitchen.shops.test"]
	want, _ := storefront.QRPNG("https://ada-kitchen.shops.test")
	h := s.Handler()
	// Without a preview domain, the address it was asked on.
	w := get(h, "GET", "ada-kitchen.shops.test", "/qr.png")
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" || !bytes.Equal(w.Body.Bytes(), want) || !strings.Contains(w.Header().Get("Cache-Control"), "public") ||
		w.Header().Get("Set-Cookie") != "" {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
	// With one, always the preview address, whichever host serves it.
	s.PreviewDomain = "shops.test"
	if w := get(h, "GET", "adakitchen.ng", "/qr.png"); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), want) {
		t.Fatalf("own domain: %d", w.Code)
	}
	if w := get(h, "HEAD", "ada-kitchen.shops.test", "/qr.png"); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatalf("HEAD: %d %d", w.Code, w.Body.Len())
	}
	for _, path := range []string{"/embed/qr.png", "/qr.svg", "/qr.png/x"} {
		if w := get(h, "GET", "ada-kitchen.shops.test", path); w.Code == 200 && w.Header().Get("Content-Type") == "image/png" {
			t.Errorf("%s served a QR code", path)
		}
	}
	if w := get(h, "POST", "ada-kitchen.shops.test", "/qr.png"); w.Code != 405 {
		t.Errorf("POST: %d", w.Code)
	}
	// A draft has none.
	s, _ = storefrontServer(t, StorefrontPage{}, false)
	if w := get(s.Handler(), "GET", "ada-kitchen.shops.test", "/qr.png"); w.Code != 404 {
		t.Errorf("draft: %d", w.Code)
	}
}
