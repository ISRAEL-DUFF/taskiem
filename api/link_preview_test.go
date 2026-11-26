package api_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/egress"
)

// pictureJPEG is a w by h JPEG.
func pictureJPEG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 120, 255})
		}
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: 80})
	return b.Bytes()
}

// previewSite is a site whose pages and pictures the tests' previews
// fetch: hits counts every request; "pics.test" names an address the
// guard refuses (a private one).
type previewSite struct {
	*httptest.Server
	hits atomic.Int32
	port string
}

func newPreviewSite(t *testing.T) *previewSite {
	t.Helper()
	ps := &previewSite{}
	pic := pictureJPEG(1200, 630)
	head := func(w http.ResponseWriter, title, extra string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<html><head><title>`+title+`</title>`+extra+`</head><body>Hello</body></html>`)
	}
	ps.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ps.hits.Add(1)
		if ua := r.Header.Get("User-Agent"); ua != "TaskiemBot/1.0 (+https://taskiem.com/bot)" || r.Header.Get("Cookie") != "" {
			t.Errorf("asked as %q with cookies %q", ua, r.Header.Get("Cookie"))
		}
		switch p := r.URL.Path; {
		case p == "/img.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(pic)
		case p == "/big.jpg":
			_, _ = w.Write(append(append([]byte{}, pic...), make([]byte, 2<<20)...))
		case p == "/notimg":
			head(w, "Not a picture", "")
		case p == "/plain":
			head(w, "Plain page", `<meta property="og:site_name" content="Plain Site"><meta name="description" content="No picture here">`)
		case p == "/far":
			head(w, "Far picture", `<meta property="og:image" content="http://pics.test:`+ps.port+`/img.jpg">`)
		case p == "/bigpic":
			head(w, "Big picture", `<meta property="og:image" content="/big.jpg">`)
		case p == "/htmlpic":
			head(w, "Not a picture", `<meta name="twitter:image" content="/notimg">`)
		case p == "/redir":
			http.Redirect(w, r, "http://pics.test:"+ps.port+"/page", http.StatusFound)
		case strings.HasPrefix(p, "/r"):
			// /r4 → /r3 → /r2 → /r1 → /page: four redirects.
			n := 0
			_, _ = fmt.Sscanf(p, "/r%d", &n)
			next := "/page"
			if n > 1 {
				next = fmt.Sprintf("/r%d", n-1)
			}
			http.Redirect(w, r, next, http.StatusFound)
		default:
			head(w, "Phones "+p, `<meta property="og:title" content="Phones at Jumia `+p+`"><meta property="og:site_name" content="Jumia">
				<meta property="og:description" content="The best phones"><meta property="og:image" content="/img.jpg">`)
		}
	}))
	t.Cleanup(ps.Close)
	_, ps.port, _ = net.SplitHostPort(strings.TrimPrefix(ps.URL, "http://"))
	return ps
}

// previewGuard reaches the test's site and refuses pics.test, which
// resolves to a private address.
func previewGuard() *egress.Guard {
	return &egress.Guard{
		Resolver: fakeResolver{"pics.test": "10.1.2.3"},
		Blocked:  func(a netip.Addr) bool { return a.Is4() && a.As4()[0] == 10 },
	}
}

type fakeResolver map[string]string

func (f fakeResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	if a, ok := f[host]; ok {
		return []netip.Addr{netip.MustParseAddr(a)}, nil
	}
	return net.DefaultResolver.LookupNetIP(ctx, network, host)
}

// The picture of a link's page in a conversation between businesses
// (docs/spec/chat-previews-and-editing.md §2): fetched by the server
// through the guard, cleaned, kept by each business in its own storage
// and answered from Taskiem's own address; refused from a private
// address, after a redirect to one, over 2 MB or not a picture; a page
// without one still gives a card; its site's name read; at most three
// redirects; Taskiem's own links not fetched; preview: false sends none.
func TestLinkPreviewPictures(t *testing.T) {
	w := newWAWorld(t)
	store := newProjectStorage(t)
	w.srv.Media = store.service()
	w.srv.Egress = previewGuard()
	w.srv.PreviewAnyPort()
	w.srv.PublicURL = "https://app.taskiem.test"
	seller, buyer, _, _ := linkedPair(t, w)
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	site := newPreviewSite(t)
	send := func(text string, extra ...any) map[string]any {
		t.Helper()
		body := map[string]any{"text": text}
		if len(extra) == 2 {
			body[extra[0].(string)] = extra[1]
		}
		return seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", body)["message"].(map[string]any)
	}
	previewOf := func(m map[string]any) map[string]any {
		p, _ := m["preview"].(map[string]any)
		return p
	}

	m := send("Look: " + site.URL + "/page.")
	p := previewOf(m)
	img, _ := p["image"].(map[string]any)
	if p == nil || p["site"] != "Jumia" || p["title"] != "Phones at Jumia /page" || img == nil || img["w"] != float64(800) || img["h"] != float64(420) {
		t.Fatalf("preview: %v", m)
	}
	if _, has := img["path"]; has {
		t.Fatalf("the picture's path is shown: %v", img)
	}
	// From Taskiem's own address, for each side, each its own copy.
	for _, c := range []*client{seller, buyer} {
		code, hdr, data := c.getRaw(t, "/v1/link-chats/"+link+"/previews/"+m["id"].(string))
		if code != 200 || hdr.Get("Content-Type") != "image/jpeg" || hdr.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("the picture: %d %v", code, hdr)
		}
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil || cfg.Width != 800 {
			t.Fatalf("the picture: %v %v", cfg, err)
		}
		code, _, data = c.getRaw(t, "/v1/link-chats/"+link+"/previews/"+m["id"].(string)+"?size=small")
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); code != 200 || err != nil || cfg.Width != 320 {
			t.Fatalf("the small picture: %d %v %v", code, cfg, err)
		}
	}
	// Not someone else's conversation's picture.
	if code, _, _ := buyer.getRaw(t, "/v1/link-chats/"+uuid.NewString()+"/previews/"+m["id"].(string)); code != 404 {
		t.Fatalf("another conversation: %d", code)
	}

	// A page with no picture still gives a card, and its site's name.
	if p := previewOf(send(site.URL + "/plain")); p == nil || p["image"] != nil || p["site"] != "Plain Site" || p["description"] != "No picture here" {
		t.Fatalf("a page without a picture: %v", p)
	}
	// The picture refused: on a private address, over 2 MB, not a picture.
	for _, path := range []string{"/far", "/bigpic", "/htmlpic"} {
		if p := previewOf(send(site.URL + path)); p == nil || p["image"] != nil {
			t.Fatalf("%s: %v", path, p)
		}
	}
	// A redirect to a private address, and four redirects: no card.
	for _, path := range []string{"/redir", "/r4"} {
		if p := previewOf(send(site.URL + path)); p != nil {
			t.Fatalf("%s: %v", path, p)
		}
	}
	// Three redirects are followed.
	if p := previewOf(send(site.URL + "/r3")); p == nil || p["title"] != "Phones at Jumia /page" {
		t.Fatalf("three redirects: %v", p)
	}
	w.srv.ResetLimiters()

	// Taskiem's own link: never fetched as a page.
	before := site.hits.Load()
	if p := previewOf(send("Your order: https://app.taskiem.test/o/ABC123")); p == nil || p["taskiem"] != "order" || p["title"] != "Taskiem · Order link" {
		t.Fatalf("a Taskiem link: %v", p)
	}
	// Closed with ✕: none.
	if p := previewOf(send(site.URL+"/closed", "preview", false)); p != nil {
		t.Fatalf("preview: false: %v", p)
	}
	if site.hits.Load() != before {
		t.Fatalf("fetched %d times", site.hits.Load()-before)
	}
}

// The composer's "preview this link" (§2.2): for a chat the person can
// write in, refused elsewhere; its picture answered from Taskiem; sent
// with preview: {id} as it was shown, without fetching again; held 30
// minutes and dropped after when unused; counted with sending.
func TestLinkPreviewComposer(t *testing.T) {
	w := newWAWorld(t)
	store := newProjectStorage(t)
	w.srv.Media = store.service()
	w.srv.Egress = previewGuard()
	w.srv.PreviewAnyPort()
	now := time.Now()
	w.srv.SetPreviewClock(func() time.Time { return now })
	seller, buyer, _, _ := linkedPair(t, w)
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	site := newPreviewSite(t)

	if code, _ := seller.do("POST", "/v1/link-preview", map[string]any{"url": site.URL + "/a", "chat": "link:" + uuid.NewString()}); code != 403 {
		t.Fatalf("a chat not ours: %d", code)
	}
	if code, _ := seller.do("POST", "/v1/link-preview", map[string]any{"url": site.URL + "/a", "chat": "people:" + uuid.NewString()}); code != 403 {
		t.Fatalf("a person's chat from a business: %d", code)
	}
	if code, _ := seller.do("POST", "/v1/link-preview", map[string]any{"url": "javascript:alert(1)", "chat": "link:" + link}); code != 204 {
		t.Fatalf("not a link: %d", code)
	}
	p := seller.must(200, "POST", "/v1/link-preview", map[string]any{"url": site.URL + "/a", "chat": "link:" + link})
	img, _ := p["image"].(map[string]any)
	if p["title"] != "Phones at Jumia /a" || img == nil || img["id"] != p["id"] || img["w"] != float64(800) {
		t.Fatalf("composer preview: %v", p)
	}
	if code, hdr, _ := seller.getRaw(t, "/v1/link-preview/"+p["id"].(string)+"/image"); code != 200 || hdr.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("its picture: %d", code)
	}
	if code, _, _ := buyer.getRaw(t, "/v1/link-preview/"+p["id"].(string)+"/image"); code != 404 {
		t.Fatalf("someone else's picture: %d", code)
	}
	hits := site.hits.Load()
	m := seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "See " + site.URL + "/a", "preview": map[string]any{"id": p["id"]}})["message"].(map[string]any)
	if pv, _ := m["preview"].(map[string]any); pv == nil || pv["title"] != "Phones at Jumia /a" || pv["image"] == nil || site.hits.Load() != hits {
		t.Fatalf("sent with the card shown: %v (%d fetches)", m, site.hits.Load()-hits)
	}
	// Kept by each side (the big and the small picture each): only once sent.
	store.mu.Lock()
	kept := 0
	for k := range store.files {
		if strings.Contains(k, "/chat/link-") {
			kept++
		}
	}
	store.mu.Unlock()
	if kept != 4 {
		t.Fatalf("pictures kept: %d", kept)
	}

	// One not sent: held 30 minutes, then gone.
	q := seller.must(200, "POST", "/v1/link-preview", map[string]any{"url": site.URL + "/b", "chat": "link:" + link})
	now = now.Add(29 * time.Minute)
	if code, _, _ := seller.getRaw(t, "/v1/link-preview/"+q["id"].(string)+"/image"); code != 200 {
		t.Fatalf("after 29 minutes: %d", code)
	}
	now = now.Add(2 * time.Minute)
	if code, _, _ := seller.getRaw(t, "/v1/link-preview/"+q["id"].(string)+"/image"); code != 404 || w.srv.HeldPreviews() != 0 {
		t.Fatalf("after 31 minutes: %d, %d held", code, w.srv.HeldPreviews())
	}

	// The same ten pages a minute as sending: the rest have no card.
	got204 := false
	for i := range 12 {
		code, _ := seller.do("POST", "/v1/link-preview", map[string]any{"url": fmt.Sprintf("%s/many%d", site.URL, i), "chat": "link:" + link})
		if code == 204 {
			got204 = true
			break
		}
	}
	if !got204 {
		t.Fatal("the composer's previews are not limited")
	}
}

// People chats (§2.2): the preview is made as the words are sealed and
// kept sealed with them, its picture a sealed file of each copy; both
// read it; delete for everyone removes both.
func TestLinkPreviewPeople(t *testing.T) {
	ctx := context.Background()
	w := newPeopleWorld(t)
	w.srv.Egress = previewGuard()
	w.srv.PreviewAnyPort()
	ada, _ := w.person(t, "+2348010000771", "Ada")
	bola, bolaID := w.person(t, "+2348010000772", "Bola")
	site := newPreviewSite(t)
	chat := chatWith(t, ada, bolaID)
	m := send(t, ada, chat, "Phones "+site.URL+"/people")
	pv, _ := m["preview"].(map[string]any)
	if pv == nil || pv["title"] != "Phones at Jumia /people" || pv["image"] == nil {
		t.Fatalf("preview: %v", m)
	}
	id := m["id"].(string)
	// Sealed: nothing of the page in the clear.
	var leaked bool
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT bool_or(position(convert_to('Jumia', 'UTF8') in sealed) > 0) FROM people_messages WHERE id = $1`, id).Scan(&leaked); err != nil || leaked {
		t.Fatalf("the preview is not sealed: %v %v", leaked, err)
	}
	var files int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM people_files WHERE message_id = $1 AND kind = 'link_image'`, id).Scan(&files); err != nil || files != 2 {
		t.Fatalf("link pictures: %d %v", files, err)
	}
	bolaChat := requestOf(t, bola)
	if theirs := messagesOf(t, bola, bolaChat); len(theirs) != 1 || theirs[0]["preview"] == nil {
		t.Fatalf("their copy: %v", theirs)
	}
	for _, c := range []struct {
		who  *client
		chat string
	}{{ada, chat}, {bola, bolaChat}} {
		code, hdr, data := c.who.getRaw(t, "/v1/me/people/"+c.chat+"/previews/"+id)
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); code != 200 || hdr.Get("Content-Type") != "image/jpeg" || err != nil || cfg.Width != 800 {
			t.Fatalf("the picture: %d %v %v", code, cfg, err)
		}
	}
	// Closed with ✕: none.
	if m := ada.must(201, "POST", "/v1/me/people/"+chat+"/messages", map[string]any{"text": site.URL + "/x", "preview": false}); m["preview"] != nil {
		t.Fatalf("preview: false: %v", m)
	}
	// Delete for everyone: the preview and its pictures go.
	ada.must(204, "DELETE", "/v1/me/people/"+chat+"/messages/"+id+"?for=everyone", nil)
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM people_files WHERE message_id = $1`, id).Scan(&files); err != nil || files != 0 {
		t.Fatalf("pictures after delete for everyone: %d %v", files, err)
	}
	if code, _, _ := bola.getRaw(t, "/v1/me/people/"+bolaChat+"/previews/"+id); code != 404 {
		t.Fatalf("the picture after delete for everyone: %d", code)
	}
	for _, x := range messagesOf(t, bola, bolaChat) {
		if x["id"] == id && x["preview"] != nil {
			t.Fatalf("the preview after delete for everyone: %v", x)
		}
	}
}
