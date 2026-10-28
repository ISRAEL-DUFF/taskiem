package archive

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/md5" //nolint:gosec // checking S3's Content-MD5
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/connectors/s3"
	"github.com/israel-duff/taskiem/engine/history"
)

var (
	tenant = uuid.MustParse("0193f0c2-7a40-7c1d-9e10-4f0a2b3c4d5e")
	run    = uuid.MustParse("0193f0c2-8b51-7d2e-8f21-5a1b2c3d4e6f")
	events = []history.Event{
		{Seq: 1, Type: history.RunStarted, Payload: json.RawMessage(`{"run":{"id":"r"}}`), Origin: history.OriginIngest},
		{Seq: 2, Type: history.RunCompleted, Origin: history.OriginDecide},
	}
)

type put struct {
	method, host, path string
	header             http.Header
	body               []byte
}

func server(t *testing.T, status int, reply string) (*httptest.Server, *[]put) {
	t.Helper()
	var got []put
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, put{r.Method, r.Host, r.URL.EscapedPath(), r.Header.Clone(), b})
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func archiver(t *testing.T, endpoint string, pathStyle bool) *S3 {
	t.Helper()
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	a := &S3{Endpoint: u, Bucket: "taskiem-archive", Prefix: "runs/", PathStyle: pathStyle,
		Signer: s3.Signer{AccessKeyID: "AKID", SecretAccessKey: "secret", Region: "us-east-1", Service: "s3"},
		Now:    func() time.Time { return time.Date(2026, 10, 28, 3, 0, 0, 0, time.UTC) }}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestArchivePathStyle(t *testing.T) {
	srv, got := server(t, http.StatusOK, "")
	a := archiver(t, srv.URL, true)
	a.SSE = "AES256"
	if err := a.Archive(context.Background(), tenant, run, events); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 {
		t.Fatalf("%d requests", len(*got))
	}
	p := (*got)[0]
	if p.method != http.MethodPut || p.path != "/taskiem-archive/runs/"+tenant.String()+"/"+run.String()+".jsonl.gz" {
		t.Errorf("%s %s", p.method, p.path)
	}
	sum := sha256.Sum256(p.body)
	md := md5.Sum(p.body) //nolint:gosec // see the import
	if p.header.Get("X-Amz-Content-Sha256") != hex.EncodeToString(sum[:]) || p.header.Get("Content-Md5") != base64.StdEncoding.EncodeToString(md[:]) {
		t.Error("the body's hashes do not match its headers")
	}
	if p.header.Get("X-Amz-Server-Side-Encryption") != "AES256" || p.header.Get("X-Amz-Meta-Taskiem-Events") != "2" {
		t.Errorf("headers: %v", p.header)
	}
	auth := p.header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKID/20261028/us-east-1/s3/aws4_request") {
		t.Errorf("authorization: %s", auth)
	}
	for _, h := range []string{"content-md5", "content-type", "host", "x-amz-content-sha256", "x-amz-date", "x-amz-server-side-encryption"} {
		if !strings.Contains(auth, h) {
			t.Errorf("%s is not signed: %s", h, auth)
		}
	}
	// The object is the events, one JSON line each, gzipped.
	zr, err := gzip.NewReader(strings.NewReader(string(p.body)))
	if err != nil {
		t.Fatal(err)
	}
	var lines []history.Event
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		var e history.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, e)
	}
	if len(lines) != 2 || lines[0].Type != history.RunStarted || lines[1].Seq != 2 {
		t.Errorf("archived %+v", lines)
	}
}

func TestArchiveVirtualHosted(t *testing.T) {
	a := archiver(t, "https://s3.af-south-1.amazonaws.com", false)
	u := a.objectURL(a.Key(tenant, run))
	if u.Host != "taskiem-archive.s3.af-south-1.amazonaws.com" || u.Path != "/runs/"+tenant.String()+"/"+run.String()+".jsonl.gz" {
		t.Errorf("%s", u)
	}
	a.PathStyle = true
	if u := a.objectURL("k"); u.Host != "s3.af-south-1.amazonaws.com" || u.Path != "/taskiem-archive/k" {
		t.Errorf("path style: %s", u)
	}
}

// A refused write is an error that says why, so the run is not purged.
func TestArchiveRefused(t *testing.T) {
	srv, _ := server(t, http.StatusForbidden, `<?xml version="1.0"?><Error><Code>AccessDenied</Code><Message>Access Denied</Message></Error>`)
	err := archiver(t, srv.URL, true).Archive(context.Background(), tenant, run, events)
	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "AccessDenied") {
		t.Errorf("err %v", err)
	}
}

func TestArchiveValidate(t *testing.T) {
	ok := func() *S3 { return archiver(t, "https://s3.amazonaws.com", false) }
	for name, change := range map[string]func(*S3){
		"no endpoint":     func(a *S3) { a.Endpoint = nil },
		"ftp endpoint":    func(a *S3) { a.Endpoint = &url.URL{Scheme: "ftp", Host: "x"} },
		"no bucket":       func(a *S3) { a.Bucket = "" },
		"bucket path":     func(a *S3) { a.Bucket = "a/b" },
		"odd prefix":      func(a *S3) { a.Prefix = "runs?x=" },
		"absolute prefix": func(a *S3) { a.Prefix = "/runs/" },
		"no secret":       func(a *S3) { a.Signer.SecretAccessKey = "" },
		"no region":       func(a *S3) { a.Signer.Region = "" },
		"unknown sse":     func(a *S3) { a.SSE = "rot13" },
		"kms key, no kms": func(a *S3) { a.KMSKeyID = "k" },
	} {
		a := ok()
		change(a)
		if a.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
