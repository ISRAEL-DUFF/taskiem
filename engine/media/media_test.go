package media

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/egress"
)

// fakeFiles is Xenibase's management file API for one project.
type fakeFiles struct {
	mu      sync.Mutex
	buckets map[string]bucketInfo
	objects map[string]string // bucket/path -> content type
	calls   []string
	patchOK bool
	putErr  *Error
	t       *testing.T
}

func (f *fakeFiles) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer xb_tok" {
		w.WriteHeader(401)
		return
	}
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	const root = "/api/v1/projects/prj_1/files"
	p := strings.TrimPrefix(r.URL.Path, root)
	body, _ := io.ReadAll(r.Body)
	switch {
	case r.Method == "GET" && p == "":
		list := []bucketInfo{}
		for _, b := range f.buckets {
			list = append(list, b)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"buckets": list})
	case r.Method == "POST" && p == "/buckets":
		var b bucketInfo
		_ = json.Unmarshal(body, &b)
		if _, ok := f.buckets[b.ID]; ok {
			w.WriteHeader(409)
			_, _ = io.WriteString(w, `{"code":"conflict","message":"exists"}`)
			return
		}
		f.buckets[b.ID] = b
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(b)
	case r.Method == "PATCH" && strings.HasPrefix(p, "/buckets/"):
		if !f.patchOK {
			w.WriteHeader(403)
			_, _ = io.WriteString(w, `{"code":"forbidden","message":"no"}`)
			return
		}
		id := strings.TrimPrefix(p, "/buckets/")
		b := f.buckets[id]
		_ = json.Unmarshal(body, &b)
		f.buckets[id] = b
		_ = json.NewEncoder(w).Encode(b)
	case r.Method == "PUT" && strings.HasSuffix(p, "/object"):
		if f.putErr != nil {
			w.WriteHeader(f.putErr.Status)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": f.putErr.Code, "message": f.putErr.Message})
			return
		}
		bucket := strings.TrimSuffix(strings.TrimPrefix(p, "/buckets/"), "/object")
		f.objects[bucket+"/"+r.URL.Query().Get("path")] = r.Header.Get("Content-Type")
		_ = json.NewEncoder(w).Encode(storedObject{Bucket: bucket, Path: r.URL.Query().Get("path"), Size: int64(len(body)), MIMEType: r.Header.Get("Content-Type")})
	case r.Method == "POST" && strings.HasSuffix(p, "/sign"):
		var in struct {
			Path      string `json:"path"`
			ExpiresIn int    `json:"expires_in"`
		}
		_ = json.Unmarshal(body, &in)
		_ = json.NewEncoder(w).Encode(map[string]any{"signed_url": "https://ref.xb.test/storage/v1/object/sign/taskiem-private/" + in.Path + "?token=t",
			"expires_at": time.Now().Add(time.Duration(in.ExpiresIn) * time.Second)})
	case r.Method == "DELETE" && strings.HasSuffix(p, "/objects"):
		var in struct {
			Paths []string `json:"paths"`
		}
		_ = json.Unmarshal(body, &in)
		bucket := strings.TrimSuffix(strings.TrimPrefix(p, "/buckets/"), "/objects")
		for _, x := range in.Paths {
			delete(f.objects, bucket+"/"+x)
		}
		_ = json.NewEncoder(w).Encode(map[string]int{"deleted": len(in.Paths)})
	default:
		f.t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		w.WriteHeader(404)
	}
}

func newService(t *testing.T) (*Service, *fakeFiles) {
	f := &fakeFiles{buckets: map[string]bucketInfo{}, objects: map[string]string{}, patchOK: true, t: t}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	s := &Service{
		Credentials: func(context.Context, uuid.UUID) (map[string]string, error) {
			return map[string]string{"token": "xb_tok", "project_id": "prj_1", "base_url": srv.URL, "api_url": "https://ref.xb.test"}, nil
		},
		Guard: &egress.Guard{Blocked: func(netip.Addr) bool { return false }},
	}
	return s, f
}

func TestPutImageMakesBuckets(t *testing.T) {
	s, f := newService(t)
	ctx, tenant := context.Background(), uuid.New()
	img, err := s.PutImage(ctx, tenant, encodeJPEG(t, halves(1200, 600)))
	if err != nil {
		t.Fatal(err)
	}
	pub, priv := f.buckets[PublicBucket], f.buckets[PrivateBucket]
	if !pub.Public || priv.Public || *priv.FileSizeLimit != MaxFileBytes || len(pub.AllowedMIMETypes) != 2 {
		t.Fatalf("buckets %+v %+v", pub, priv)
	}
	if img.Width != 1200 || len(img.Paths) != 3 || img.URLs[800] != "https://ref.xb.test/storage/v1/public/taskiem-public/images/"+img.Hash+"/800.jpg" {
		t.Fatalf("%+v", img)
	}
	if f.objects[PublicBucket+"/images/"+img.Hash+"/320.jpg"] != "image/jpeg" {
		t.Fatalf("objects %v", f.objects)
	}
	// The buckets are checked once, then remembered; the same image keeps
	// the same paths.
	n := len(f.calls)
	again, err := s.PutImage(ctx, tenant, encodeJPEG(t, halves(1200, 600)))
	if err != nil || again.Hash != img.Hash {
		t.Fatal(err)
	}
	for _, c := range f.calls[n:] {
		if !strings.HasPrefix(c, "PUT ") {
			t.Fatalf("buckets checked again: %v", f.calls[n:])
		}
	}
}

func TestPrivateBucketMadePublicIsPutBack(t *testing.T) {
	s, f := newService(t)
	f.buckets[PrivateBucket] = bucketInfo{ID: PrivateBucket, Public: true}
	f.buckets[PublicBucket] = wantBuckets[0]
	pdf := []byte("%PDF-1.7\n1 0 obj << /Type /Catalog >> endobj\n%%EOF")
	file, err := s.PutFile(context.Background(), uuid.New(), pdf)
	if err != nil || f.buckets[PrivateBucket].Public || !strings.HasPrefix(file.Path, "files/") || file.Type != "application/pdf" {
		t.Fatalf("%v %+v %+v", err, file, f.buckets[PrivateBucket])
	}

	// If it cannot be put back, nothing is written.
	s, f = newService(t)
	f.buckets[PrivateBucket] = bucketInfo{ID: PrivateBucket, Public: true}
	f.patchOK = false
	if _, err := s.PutFile(context.Background(), uuid.New(), pdf); !errors.Is(err, ErrBucket) {
		t.Fatalf("%v", err)
	}
	if len(f.objects) != 0 {
		t.Fatal("wrote to a public bucket meant to be private")
	}
}

func TestPutFileRefusals(t *testing.T) {
	s, f := newService(t)
	for name, data := range map[string][]byte{
		"a scripted PDF": []byte("%PDF-1.7\n<< /OpenAction << /S /JavaScript /JS (x) >> >>"),
		"an executable":  append([]byte("MZ\x90\x00"), make([]byte, 64)...),
		"html":           []byte("<!doctype html><p>hi"),
		"too large":      append([]byte("%PDF-1.7\n"), make([]byte, MaxFileBytes)...),
	} {
		if _, err := s.PutFile(context.Background(), uuid.New(), data); !errors.Is(err, ErrFile) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("refused files reached Xenibase: %v", f.calls)
	}
	// A photo is cleaned and kept at full size.
	file, err := s.PutFile(context.Background(), uuid.New(), withExif(encodeJPEG(t, halves(3000, 2000)), 1))
	if err != nil || file.Type != "image/jpeg" || !strings.HasSuffix(file.Path, ".jpg") {
		t.Fatalf("%v %+v", err, file)
	}
}

func TestSignDeleteAndErrors(t *testing.T) {
	s, f := newService(t)
	ctx, tenant := context.Background(), uuid.New()
	u, exp, err := s.SignURL(ctx, tenant, "files/a.pdf", time.Hour)
	if err != nil || !strings.Contains(u, "files/a.pdf?token=") || time.Until(exp) < 50*time.Minute {
		t.Fatalf("%q %v %v", u, exp, err)
	}
	if _, _, err := s.SignURL(ctx, tenant, "files/a.pdf", 8*24*time.Hour); err == nil {
		t.Fatal("longer than Xenibase allows")
	}
	f.objects[PrivateBucket+"/files/a.pdf"] = "application/pdf"
	if err := s.Delete(ctx, tenant, PrivateBucket, []string{"files/a.pdf"}); err != nil || len(f.objects) != 0 {
		t.Fatalf("%v %v", err, f.objects)
	}
	if err := s.Delete(ctx, tenant, "other", []string{"x"}); err == nil {
		t.Fatal("an unknown bucket")
	}
	f.putErr = &Error{Status: 413, Code: "quota_exceeded", Message: "storage quota"}
	_, err = s.PutImage(ctx, tenant, encodeJPEG(t, halves(100, 100)))
	var e *Error
	if !errors.As(err, &e) || !e.IsQuota() || e.Temporary() {
		t.Fatalf("%v", err)
	}
}

func TestPublicURL(t *testing.T) {
	if got := PublicURL("https://ref.xb.test/", "images/ab/800.jpg"); got != "https://ref.xb.test/storage/v1/public/taskiem-public/images/ab/800.jpg" {
		t.Fatal(got)
	}
	if got := PublicURL("https://ref.xb.test", "images/a b/#1.jpg"); !strings.HasSuffix(got, "/images/a%20b/%231.jpg") {
		t.Fatal(got)
	}
}
