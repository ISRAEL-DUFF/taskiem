package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/media"
)

// projectStorage is a fake Xenibase storage holding each project's files
// apart: what was put, signed and fetched, by project, so a test can see
// each business got its own copy and read only its own.
type projectStorage struct {
	srv     *httptest.Server
	mu      sync.Mutex
	files   map[string][]byte // project/path
	fetched []string          // project/path, in order
	refuse  string            // a project whose writes fail
}

func newProjectStorage(t *testing.T) *projectStorage {
	ps := &projectStorage{files: map[string][]byte{}}
	project := func(p string) string { // /api/v1/projects/{p}/files/...
		parts := strings.Split(strings.TrimPrefix(p, "/api/v1/projects/"), "/")
		return parts[0]
	}
	ps.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ps.mu.Lock()
		defer ps.mu.Unlock()
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/files"):
			_, _ = io.WriteString(w, `{"buckets":[]}`)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/files/buckets"):
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{}`)
		case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/object"):
			p := project(r.URL.Path)
			if p == ps.refuse {
				w.WriteHeader(507)
				_, _ = io.WriteString(w, `{"error":"full"}`)
				return
			}
			body, _ := io.ReadAll(r.Body)
			ps.files[p+"/"+r.URL.Query().Get("path")] = body
			_ = json.NewEncoder(w).Encode(map[string]any{"path": r.URL.Query().Get("path"), "size": len(body), "mime_type": r.Header.Get("Content-Type")})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/sign"):
			var in struct {
				Path string `json:"path"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			_ = json.NewEncoder(w).Encode(map[string]any{"signed_url": ps.srv.URL + "/signed/" + project(r.URL.Path) + "/" + in.Path, "expires_at": "2099-01-01T00:00:00Z"})
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/signed/"):
			k := strings.TrimPrefix(r.URL.Path, "/signed/")
			data, ok := ps.files[k]
			if !ok {
				w.WriteHeader(404)
				return
			}
			ps.fetched = append(ps.fetched, k)
			_, _ = w.Write(data)
		case r.Method == "DELETE" && strings.HasSuffix(r.URL.Path, "/objects"):
			var in struct {
				Paths []string `json:"paths"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			for _, p := range in.Paths {
				delete(ps.files, project(r.URL.Path)+"/"+p)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"deleted": len(in.Paths)})
		default:
			t.Errorf("storage: unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(ps.srv.Close)
	return ps
}

// in is the paths a project holds.
func (ps *projectStorage) in(project string) []string {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	var out []string
	for k := range ps.files {
		if p, ok := strings.CutPrefix(k, project+"/"); ok {
			out = append(out, p)
		}
	}
	return out
}

func (ps *projectStorage) service() *media.Service {
	return &media.Service{
		Credentials: func(_ context.Context, tenant uuid.UUID) (map[string]string, error) {
			return map[string]string{"token": "xb_tok", "project_id": "prj_" + tenant.String(), "base_url": ps.srv.URL, "api_url": ps.srv.URL}, nil
		},
		Guard: &egress.Guard{Blocked: func(netip.Addr) bool { return false }},
	}
}

// sendFile posts a file to a conversation as a multipart form.
func (c *client) sendFile(t *testing.T, link, name string, data []byte, fields map[string]string) (int, map[string]any) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(data)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	_ = mw.Close()
	return c.rawPost(t, "/v1/link-chats/"+link+"/files", mw.FormDataContentType(), b.Bytes())
}

// getRaw fetches a path's bytes and headers.
func (c *client) getRaw(t *testing.T, path string) (int, http.Header, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, c.base+path, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, raw
}

// Photos, PDFs and voice notes in the conversation between linked
// businesses (L3, §5.5): each business keeps its own copy in its own
// storage, and reads only its own; the other side's phones are told what
// came; anything else is refused, and if the other side's copy cannot be
// kept, nothing is sent and our copy is removed.
func TestLinkConversationFiles(t *testing.T) {
	w := newWAWorld(t)
	store := newProjectStorage(t)
	w.srv.Media = store.service()
	seller, buyer, sellerID, buyerID := linkedPair(t, w)
	const buyerOn = "+2348010000393"
	w.bind(t, buyer, buyerOn)
	w.say(t, buyerOn, "links")
	viewer := addMember(t, w.world, buyer, "vin", "viewer")
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)
	sellerProject, buyerProject := "prj_"+sellerID, "prj_"+buyerID

	// A photo with a caption: a copy in each business's storage.
	before := len(w.graph.Sent(buyerOn))
	code, out := seller.sendFile(t, link, "bags.jpg", photo(t), map[string]string{"text": "The new bags"})
	if code != 201 {
		t.Fatalf("photo: %d %v", code, out)
	}
	m := out["message"].(map[string]any)
	att, _ := m["attachment"].(map[string]any)
	if att == nil || att["kind"] != "photo" || att["type"] != "image/jpeg" || att["path"] != nil || m["body"] != "The new bags" {
		t.Fatalf("photo sent: %v", m)
	}
	if s, b := store.in(sellerProject), store.in(buyerProject); len(s) != 1 || len(b) != 1 {
		t.Fatalf("a copy each: seller %v, buyer %v", s, b)
	}
	if told := w.lastTo(t, buyerOn, before); len(told) != 1 || !strings.Contains(told[0].Text, "Owner: (Photo) The new bags") {
		t.Errorf("the buyer's phone: %+v", told)
	}

	// Each side reads its own copy.
	id := m["id"].(string)
	theirs := buyer.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any)
	got := theirs[len(theirs)-1].(map[string]any)
	if a, _ := got["attachment"].(map[string]any); a == nil || a["kind"] != "photo" || a["path"] != nil || got["mine"] != false {
		t.Fatalf("the buyer's copy: %v", got)
	}
	code, hdr, data := buyer.getRaw(t, "/v1/link-chats/"+link+"/files/"+id)
	if code != 200 || hdr.Get("Content-Type") != "image/jpeg" || hdr.Get("X-Content-Type-Options") != "nosniff" || len(data) == 0 {
		t.Fatalf("the buyer's photo: %d %v %d", code, hdr, len(data))
	}
	if code, _, _ := seller.getRaw(t, "/v1/link-chats/"+link+"/files/"+id); code != 200 {
		t.Fatalf("the seller's photo: %d", code)
	}
	store.mu.Lock()
	fetched := append([]string(nil), store.fetched...)
	store.mu.Unlock()
	if len(fetched) != 2 || !strings.HasPrefix(fetched[0], buyerProject+"/") || !strings.HasPrefix(fetched[1], sellerProject+"/") {
		t.Errorf("each read its own copy: %v", fetched)
	}
	// A reply quoting it says it quotes a photo.
	r := buyer.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "How many?", "reply_to": id})["message"].(map[string]any)
	if q := r["reply"].(map[string]any); q["attachment"].(map[string]any)["kind"] != "photo" || q["attachment"].(map[string]any)["path"] != nil {
		t.Errorf("the quote: %v", q)
	}
	// A member who only reads sees it; a business outside the link does not.
	if code, _, _ := viewer.getRaw(t, "/v1/link-chats/"+link+"/files/"+id); code != 200 {
		t.Errorf("the viewer: %d", code)
	}
	stranger := w.tenant(t, "Stranger Ltd", "s@files.test")
	if code, _, _ := stranger.getRaw(t, "/v1/link-chats/"+link+"/files/"+id); code != 404 {
		t.Errorf("a stranger: %d", code)
	}

	// A voice note from the buyer, with no words: told as a voice note.
	voice := append([]byte("OggS\x00\x02"), bytes.Repeat([]byte{1}, 2000)...)
	code, out = buyer.sendFile(t, link, "voice.ogg", voice, nil)
	if code != 201 {
		t.Fatalf("voice: %d %v", code, out)
	}
	vm := out["message"].(map[string]any)
	if a := vm["attachment"].(map[string]any); a["kind"] != "voice" || a["type"] != "audio/ogg" || vm["body"] != nil && vm["body"] != "" {
		t.Fatalf("voice sent: %v", vm)
	}
	code, hdr, data = seller.getRaw(t, "/v1/link-chats/"+link+"/files/"+vm["id"].(string))
	if code != 200 || hdr.Get("Content-Type") != "audio/ogg" || !bytes.Equal(data, voice) {
		t.Errorf("the seller hears it: %d %v", code, hdr)
	}

	// Refused: a file that is not a photo, a PDF or a voice note; a
	// member who only reads; nothing at all.
	if code, out := seller.sendFile(t, link, "run.sh", []byte("#!/bin/sh\necho hi\n"), nil); code != 400 {
		t.Errorf("a script: %d %v", code, out)
	}
	if code, _ := viewer.sendFile(t, link, "bags.jpg", photo(t), nil); code != 403 {
		t.Errorf("the viewer sends: %d", code)
	}
	if code, _ := seller.rawPost(t, "/v1/link-chats/"+link+"/files", "multipart/form-data; boundary=x", []byte("--x--\r\n")); code != 400 {
		t.Errorf("no file: %d", code)
	}

	// The buyer's storage refuses: nothing is sent, and the seller's copy
	// is removed.
	store.mu.Lock()
	store.refuse = buyerProject
	store.mu.Unlock()
	held := len(store.in(sellerProject))
	n := len(seller.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any))
	if code, out := seller.sendFile(t, link, "bags.jpg", photo(t), nil); code != 409 || !strings.Contains(out["error"].(string), "nothing was sent") {
		t.Errorf("the other side's storage refused: %d %v", code, out)
	}
	if got := len(store.in(sellerProject)); got != held {
		t.Errorf("our copy removed: %d files, had %d", got, held)
	}
	if got := len(seller.must(200, "GET", "/v1/link-chats/"+link+"/messages", nil)["messages"].([]any)); got != n {
		t.Errorf("nothing sent: %d messages, had %d", got, n)
	}
}
