package s3

import (
	"context"
	"crypto/hmac"
	"crypto/md5" //nolint:gosec // Content-MD5 check in the fake server.
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/egress"
)

const (
	testAK     = "AKIATESTEXAMPLE"
	testSK     = "testsecret/abc+def"
	testToken  = "session-token-example"
	testRegion = "eu-west-2"
)

var fixedNow = time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)

// ---- an independent SigV4 verifier, as S3 would run it ----

func vEncode(s string, slash bool) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || strings.IndexByte("-._~", c) >= 0 || (slash && c == '/') {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func vHMAC(k []byte, s string) []byte {
	m := hmac.New(sha256.New, k)
	m.Write([]byte(s))
	return m.Sum(nil)
}

func vQuery(raw string, drop string) string {
	var pairs []string
	for _, p := range strings.Split(raw, "&") {
		if p == "" {
			continue
		}
		k, v, _ := strings.Cut(p, "=")
		dk, _ := url.PathUnescape(k)
		dv, _ := url.PathUnescape(v)
		if dk == drop {
			continue
		}
		pairs = append(pairs, vEncode(dk, false)+"="+vEncode(dv, false))
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "&")
}

// verify recomputes the signature on r. It returns the access key and an
// error message (empty when the signature is good).
func verify(r *http.Request, secrets map[string]string, region string) (string, string) {
	var cred, signed, sig, payload, amzDate string
	presigned := r.URL.Query().Get("X-Amz-Signature") != ""
	if presigned {
		q := r.URL.Query()
		cred, signed, sig, amzDate, payload = q.Get("X-Amz-Credential"), q.Get("X-Amz-SignedHeaders"), q.Get("X-Amz-Signature"), q.Get("X-Amz-Date"), "UNSIGNED-PAYLOAD"
		exp, _ := strconv.Atoi(q.Get("X-Amz-Expires"))
		t, _ := time.Parse("20060102T150405Z", amzDate)
		if exp < 1 || exp > 604800 || fixedNow.After(t.Add(time.Duration(exp)*time.Second)) {
			return "", "expired"
		}
	} else {
		auth := r.Header.Get("Authorization")
		rest, ok := strings.CutPrefix(auth, "AWS4-HMAC-SHA256 ")
		if !ok {
			return "", "no sigv4 authorization"
		}
		for _, part := range strings.Split(rest, ", ") {
			k, v, _ := strings.Cut(part, "=")
			switch k {
			case "Credential":
				cred = v
			case "SignedHeaders":
				signed = v
			case "Signature":
				sig = v
			}
		}
		payload, amzDate = r.Header.Get("X-Amz-Content-Sha256"), r.Header.Get("X-Amz-Date")
	}
	parts := strings.Split(cred, "/")
	if len(parts) != 5 || parts[2] != region || parts[3] != "s3" || parts[4] != "aws4_request" || !strings.HasPrefix(amzDate, parts[1]) {
		return "", "bad credential scope " + cred
	}
	secret, ok := secrets[parts[0]]
	if !ok {
		return "", "InvalidAccessKeyId"
	}
	var ch strings.Builder
	names := strings.Split(signed, ";")
	if !sort.StringsAreSorted(names) || names[0] != "host" && !strings.Contains(signed, "host") {
		return "", "signed headers must be sorted and include host"
	}
	for _, n := range names {
		v := r.Header.Values(n)
		if n == "host" {
			v = []string{r.Host}
		}
		if len(v) == 0 {
			return "", "signed header missing: " + n
		}
		for i := range v {
			v[i] = strings.Join(strings.Fields(v[i]), " ")
		}
		ch.WriteString(n + ":" + strings.Join(v, ",") + "\n")
	}
	if !presigned && !strings.Contains(signed, "x-amz-content-sha256") {
		return "", "x-amz-content-sha256 not signed"
	}
	path, rawQuery, _ := strings.Cut(r.RequestURI, "?")
	cr := strings.Join([]string{r.Method, path, vQuery(rawQuery, "X-Amz-Signature"), ch.String(), signed, payload}, "\n")
	h := sha256.Sum256([]byte(cr))
	sts := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + strings.Join(parts[1:], "/") + "\n" + hex.EncodeToString(h[:])
	k := vHMAC(vHMAC(vHMAC(vHMAC([]byte("AWS4"+secret), parts[1]), parts[2]), parts[3]), "aws4_request")
	if hex.EncodeToString(vHMAC(k, sts)) != sig {
		return "", "SignatureDoesNotMatch"
	}
	return parts[0], ""
}

// ---- an in-memory S3 that speaks the documented XML ----

type object struct {
	body       []byte
	ctype      string
	meta       http.Header
	cacheCtl   string
	versionSeq int
}

type fakeS3 struct {
	mu        sync.Mutex
	objects   map[string]*object // bucket/key
	requests  []string
	tokens    []string
	copyError string // embedded error code for the next copy
}

func xmlErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>%s</Message><RequestId>REQ123</RequestId></Error>`, code, msg)
}

func etag(b []byte) string {
	s := md5.Sum(b) //nolint:gosec // S3 ETag shape
	return `"` + hex.EncodeToString(s[:]) + `"`
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, msg := verify(r, map[string]string{testAK: testSK}, testRegion); msg != "" {
		xmlErr(w, http.StatusForbidden, "SignatureDoesNotMatch", msg)
		return
	}
	f.tokens = append(f.tokens, r.Header.Get("X-Amz-Security-Token")+r.URL.Query().Get("X-Amz-Security-Token"))
	body, _ := io.ReadAll(r.Body)
	if r.URL.Query().Get("X-Amz-Signature") == "" {
		if got := r.Header.Get("X-Amz-Content-Sha256"); got != sha256Hex(body) {
			xmlErr(w, http.StatusBadRequest, "XAmzContentSHA256Mismatch", "payload hash")
			return
		}
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	bucket, key, _ := strings.Cut(path, "/")
	f.requests = append(f.requests, r.Method+" "+r.URL.EscapedPath())
	switch bucket {
	case "missing":
		xmlErr(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist")
		return
	case "denied":
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		xmlErr(w, http.StatusForbidden, "AccessDenied", "Access Denied")
		return
	case "slow":
		xmlErr(w, http.StatusServiceUnavailable, "SlowDown", "Please reduce your request rate.")
		return
	case "broken":
		xmlErr(w, http.StatusInternalServerError, "InternalError", "We encountered an internal error. Please try again.")
		return
	case "elsewhere":
		w.Header().Set("X-Amz-Bucket-Region", "eu-west-1")
		xmlErr(w, http.StatusMovedPermanently, "PermanentRedirect", "The bucket you are attempting to access must be addressed using the specified endpoint.")
		return
	}
	id := bucket + "/" + key
	switch {
	case key == "" && r.Method == http.MethodGet:
		f.list(w, r, bucket)
	case r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") != "":
		f.copy(w, r, id)
	case r.Method == http.MethodPut:
		if md := r.Header.Get("Content-Md5"); md != "" {
			s := md5.Sum(body) //nolint:gosec // as S3 does
			if md != base64.StdEncoding.EncodeToString(s[:]) {
				xmlErr(w, http.StatusBadRequest, "BadDigest", "md5")
				return
			}
		}
		o := &object{body: body, ctype: r.Header.Get("Content-Type"), meta: http.Header{}, cacheCtl: r.Header.Get("Cache-Control")}
		if old := f.objects[id]; old != nil {
			o.versionSeq = old.versionSeq + 1
		}
		for k, v := range r.Header {
			if strings.HasPrefix(strings.ToLower(k), "x-amz-meta-") {
				o.meta[k] = v
			}
		}
		f.objects[id] = o
		w.Header().Set("ETag", etag(body))
		w.Header().Set("X-Amz-Version-Id", fmt.Sprintf("v%d", o.versionSeq))
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		o := f.objects[id]
		if o == nil {
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			xmlErr(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.")
			return
		}
		for k, v := range o.meta {
			w.Header()[k] = v
		}
		w.Header().Set("Content-Type", o.ctype)
		w.Header().Set("Cache-Control", o.cacheCtl)
		w.Header().Set("ETag", etag(o.body))
		w.Header().Set("Last-Modified", "Tue, 06 Oct 2026 09:00:00 GMT")
		w.Header().Set("Content-Length", strconv.Itoa(len(o.body)))
		w.Header().Set("X-Amz-Storage-Class", "STANDARD")
		if r.Method == http.MethodGet {
			_, _ = w.Write(o.body)
		}
	case r.Method == http.MethodDelete:
		delete(f.objects, id)
		w.WriteHeader(http.StatusNoContent)
	default:
		xmlErr(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "")
	}
}

func (f *fakeS3) copy(w http.ResponseWriter, r *http.Request, id string) {
	if f.copyError != "" {
		code := f.copyError
		f.copyError = ""
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>`+"\n"+`<Error><Code>%s</Code><Message>during copy</Message><RequestId>R2</RequestId></Error>`, code)
		return
	}
	src, err := url.PathUnescape(strings.TrimPrefix(r.Header.Get("X-Amz-Copy-Source"), "/"))
	if err != nil {
		xmlErr(w, http.StatusBadRequest, "InvalidArgument", "copy source")
		return
	}
	o := f.objects[src]
	if o == nil {
		xmlErr(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.")
		return
	}
	n := *o
	if r.Header.Get("X-Amz-Metadata-Directive") == "REPLACE" {
		n.ctype, n.meta = r.Header.Get("Content-Type"), http.Header{}
		for k, v := range r.Header {
			if strings.HasPrefix(strings.ToLower(k), "x-amz-meta-") {
				n.meta[k] = v
			}
		}
	}
	f.objects[id] = &n
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>`+"\n"+`<CopyObjectResult><LastModified>2026-10-06T09:00:00.000Z</LastModified><ETag>%s</ETag></CopyObjectResult>`, etag(n.body))
}

type xmlContents struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int    `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}

type xmlPrefix struct {
	Prefix string `xml:"Prefix"`
}

type xmlList struct {
	XMLName               xml.Name      `xml:"ListBucketResult"`
	Name                  string        `xml:"Name"`
	Prefix                string        `xml:"Prefix"`
	KeyCount              int           `xml:"KeyCount"`
	MaxKeys               int           `xml:"MaxKeys"`
	IsTruncated           bool          `xml:"IsTruncated"`
	EncodingType          string        `xml:"EncodingType,omitempty"`
	NextContinuationToken string        `xml:"NextContinuationToken,omitempty"`
	Contents              []xmlContents `xml:"Contents"`
	CommonPrefixes        []xmlPrefix   `xml:"CommonPrefixes"`
}

func (f *fakeS3) list(w http.ResponseWriter, r *http.Request, bucket string) {
	q := r.URL.Query()
	if q.Get("list-type") != "2" {
		xmlErr(w, http.StatusBadRequest, "InvalidArgument", "want ListObjectsV2")
		return
	}
	prefix, delim := q.Get("prefix"), q.Get("delimiter")
	maxKeys, _ := strconv.Atoi(q.Get("max-keys"))
	start := 0
	if t := q.Get("continuation-token"); t != "" {
		start, _ = strconv.Atoi(strings.TrimPrefix(t, "tok-"))
	}
	var keys []string
	for id := range f.objects {
		if b, k, _ := strings.Cut(id, "/"); b == bucket && strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	enc := func(s string) string {
		if q.Get("encoding-type") == "url" {
			return url.QueryEscape(s)
		}
		return s
	}
	res := xmlList{Name: bucket, Prefix: prefix, MaxKeys: maxKeys, EncodingType: q.Get("encoding-type")}
	seen := map[string]bool{}
	i := start
	for ; i < len(keys) && res.KeyCount < maxKeys; i++ {
		k := keys[i]
		if delim != "" {
			if j := strings.Index(k[len(prefix):], delim); j >= 0 {
				cp := k[:len(prefix)+j+len(delim)]
				if !seen[cp] {
					seen[cp] = true
					res.CommonPrefixes = append(res.CommonPrefixes, xmlPrefix{enc(cp)})
					res.KeyCount++
				}
				continue
			}
		}
		o := f.objects[bucket+"/"+k]
		res.Contents = append(res.Contents, xmlContents{Key: enc(k), LastModified: "2026-10-06T09:00:00.000Z", ETag: etag(o.body), Size: len(o.body), StorageClass: "STANDARD"})
		res.KeyCount++
	}
	if i < len(keys) {
		res.IsTruncated, res.NextContinuationToken = true, fmt.Sprintf("tok-%d", i)
	}
	w.Header().Set("Content-Type", "application/xml")
	_, _ = io.WriteString(w, xml.Header)
	_ = xml.NewEncoder(w).Encode(res)
}

// ---- harness ----

type harness struct {
	t    *testing.T
	fake *fakeS3
	srv  *httptest.Server
	c    *connector.Connector
	hc   *http.Client
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	f := &fakeS3{objects: map[string]*object{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c := New(Options{BaseURL: srv.URL, Now: func() time.Time { return fixedNow }})
	if err := connector.NewRegistry().Register(c); err != nil {
		t.Fatal(err)
	}
	// The engine's egress-guarded client, loosened only to reach loopback.
	guard := &egress.Guard{Blocked: func(a netip.Addr) bool { return !a.IsLoopback() && egress.BlockedAddr(a) }}
	hc := guard.Client(egress.Policy{Hosts: c.Manifest.Hosts()}, 10*time.Second)
	return &harness{t: t, fake: f, srv: srv, c: c, hc: hc}
}

func creds() map[string]string {
	return map[string]string{"access_key_id": testAK, "secret_access_key": testSK, "region": testRegion, "bucket": "reports"}
}

func (h *harness) call(action string, in map[string]any) (map[string]any, error) {
	return h.callWith(creds(), action, in)
}

func (h *harness) callWith(cr map[string]string, action string, in map[string]any) (map[string]any, error) {
	h.t.Helper()
	r, err := h.c.Actions[action].Execute(context.Background(), connector.Request{Input: in, Credentials: cr, HTTP: h.hc, IdempotencyKey: "tsk_abcdefghijklmnopqrstuvwxyz", Attempt: 1})
	if err != nil {
		return nil, err
	}
	return r.Output.(map[string]any), nil
}

func TestPutGetHeadDeleteRoundTrip(t *testing.T) {
	h := newHarness(t)
	out, err := h.call("put_object", map[string]any{
		"key": "2026/10/payroll summary+final.csv", "body": "name,amount\nAda,5000\n", "content_type": "text/csv",
		"metadata": map[string]any{"run-id": "r-1"}, "cache_control": "no-cache",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["etag"] != strings.Trim(etag([]byte("name,amount\nAda,5000\n")), `"`) || out["size"] != int64(21) || out["bucket"] != "reports" {
		t.Errorf("put output %v", out)
	}
	// The key is sent exactly as signed: spaces and '+' percent-encoded.
	if last := h.fake.requests[len(h.fake.requests)-1]; last != "PUT /reports/2026/10/payroll%20summary%2Bfinal.csv" {
		t.Errorf("request %s", last)
	}
	got, err := h.call("get_object", map[string]any{"key": "2026/10/payroll summary+final.csv"})
	if err != nil {
		t.Fatal(err)
	}
	if got["body"] != "name,amount\nAda,5000\n" || got["content_type"] != "text/csv" || got["encoding"] != "text" ||
		got["metadata"].(map[string]any)["run-id"] != "r-1" || got["last_modified"] != "2026-10-06T09:00:00Z" || got["cache_control"] != "no-cache" {
		t.Errorf("get output %v", got)
	}
	head, err := h.call("head_object", map[string]any{"key": "2026/10/payroll summary+final.csv"})
	if err != nil || head["exists"] != true || head["size"] != int64(21) || head["storage_class"] != "STANDARD" {
		t.Errorf("head %v %v", head, err)
	}
	del, err := h.call("delete_object", map[string]any{"key": "2026/10/payroll summary+final.csv"})
	if err != nil || del["deleted"] != true {
		t.Errorf("delete %v %v", del, err)
	}
	// Deleting again is fine: delete is idempotent.
	if _, err := h.call("delete_object", map[string]any{"key": "2026/10/payroll summary+final.csv"}); err != nil {
		t.Errorf("second delete: %v", err)
	}
	head, err = h.call("head_object", map[string]any{"key": "2026/10/payroll summary+final.csv"})
	if err != nil || head["exists"] != false {
		t.Errorf("head after delete %v %v", head, err)
	}
	if _, err := h.call("get_object", map[string]any{"key": "2026/10/payroll summary+final.csv"}); effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "NoSuchKey") {
		t.Errorf("get missing: %v", err)
	}
}

func TestBinaryAndSizeCap(t *testing.T) {
	h := newHarness(t)
	bin := []byte{0x89, 'P', 'N', 'G', 0, 0xff, 0xfe}
	if _, err := h.call("put_object", map[string]any{"key": "logo.png", "body": base64.StdEncoding.EncodeToString(bin), "body_encoding": "base64"}); err != nil {
		t.Fatal(err)
	}
	if h.fake.objects["reports/logo.png"].ctype != "application/octet-stream" || string(h.fake.objects["reports/logo.png"].body) != string(bin) {
		t.Errorf("stored %+v", h.fake.objects["reports/logo.png"])
	}
	out, err := h.call("get_object", map[string]any{"key": "logo.png", "encoding": "base64"})
	if err != nil || out["body"] != base64.StdEncoding.EncodeToString(bin) {
		t.Errorf("base64 get %v %v", out, err)
	}
	if _, err := h.call("get_object", map[string]any{"key": "logo.png"}); effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "base64") {
		t.Errorf("binary as text must be refused: %v", err)
	}
	if _, err := h.call("get_object", map[string]any{"key": "logo.png", "encoding": "base64", "max_bytes": int64(4)}); effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "max_bytes") {
		t.Errorf("over cap: %v", err)
	}
	if _, err := h.call("put_object", map[string]any{"key": "x", "body": "%%%", "body_encoding": "base64"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("bad base64: %v", err)
	}
	big := strings.Repeat("a", MaxPutBytes+1)
	if _, err := h.call("put_object", map[string]any{"key": "big", "body": big}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("oversize put: %v", err)
	}
}

func TestPutIsIdempotentByKey(t *testing.T) {
	h := newHarness(t)
	in := map[string]any{"key": "exports/a.json", "body": `{"ok":true}`, "content_type": "application/json"}
	a, err := h.call("put_object", in)
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.call("put_object", in)
	if err != nil {
		t.Fatal(err)
	}
	if a["etag"] != b["etag"] || len(h.fake.objects) != 1 {
		t.Errorf("a retry must leave the same object: %v %v", a, b)
	}
}

func TestListPagesPrefixesAndEncodedKeys(t *testing.T) {
	h := newHarness(t)
	for _, k := range []string{"in/a.csv", "in/b c.csv", "in/sub/d.csv", "in/sub/e.csv", "out/x.csv"} {
		if _, err := h.call("put_object", map[string]any{"key": k, "body": "1"}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := h.call("list_objects", map[string]any{"prefix": "in/", "delimiter": "/"})
	if err != nil {
		t.Fatal(err)
	}
	objs := out["objects"].([]any)
	if len(objs) != 2 || objs[1].(map[string]any)["key"] != "in/b c.csv" || objs[0].(map[string]any)["size"] != int64(1) {
		t.Errorf("objects %v", objs)
	}
	if cp := out["common_prefixes"].([]any); len(cp) != 1 || cp[0] != "in/sub/" {
		t.Errorf("prefixes %v", cp)
	}
	page1, err := h.call("list_objects", map[string]any{"max_keys": int64(2)})
	if err != nil || page1["is_truncated"] != true || page1["next_continuation_token"] == "" || page1["key_count"] != int64(2) {
		t.Fatalf("page 1 %v %v", page1, err)
	}
	page2, err := h.call("list_objects", map[string]any{"max_keys": int64(10), "continuation_token": page1["next_continuation_token"]})
	if err != nil || page2["is_truncated"] != false || len(page2["objects"].([]any)) != 3 {
		t.Errorf("page 2 %v %v", page2, err)
	}
}

func TestCopyAndEmbeddedCopyError(t *testing.T) {
	h := newHarness(t)
	if _, err := h.call("put_object", map[string]any{"key": "src.txt", "body": "hello", "metadata": map[string]any{"a": "1"}}); err != nil {
		t.Fatal(err)
	}
	out, err := h.call("copy_object", map[string]any{"source_key": "src.txt", "key": "archive/dst.txt", "bucket": "archive"})
	if err != nil {
		t.Fatal(err)
	}
	if out["etag"] != strings.Trim(etag([]byte("hello")), `"`) || out["last_modified"] != "2026-10-06T09:00:00.000Z" {
		t.Errorf("copy %v", out)
	}
	if o := h.fake.objects["archive/archive/dst.txt"]; o == nil || o.meta.Get("X-Amz-Meta-A") != "1" {
		t.Errorf("copied object %+v", o)
	}
	if _, err := h.call("copy_object", map[string]any{"source_key": "src.txt", "key": "re.txt", "content_type": "text/markdown", "metadata": map[string]any{"b": "2"}}); err != nil {
		t.Fatal(err)
	}
	if o := h.fake.objects["reports/re.txt"]; o.ctype != "text/markdown" || o.meta.Get("X-Amz-Meta-A") != "" || o.meta.Get("X-Amz-Meta-B") != "2" {
		t.Errorf("replaced metadata %+v", o)
	}
	h.fake.copyError = "InternalError"
	if _, err := h.call("copy_object", map[string]any{"source_key": "src.txt", "key": "z.txt"}); effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("an InternalError inside a 200 must be an unknown outcome: %v", err)
	}
	h.fake.copyError = "SlowDown"
	if _, err := h.call("copy_object", map[string]any{"source_key": "src.txt", "key": "z.txt"}); effects.Classify(err) != effects.KindRetryable {
		t.Errorf("SlowDown inside a 200 is retryable: %v", err)
	}
	if _, err := h.call("copy_object", map[string]any{"source_key": "src.txt", "key": "src.txt"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("copy onto itself without changes: %v", err)
	}
}

func TestErrorClassification(t *testing.T) {
	h := newHarness(t)
	cases := []struct {
		bucket, action string
		want           effects.ErrorKind
		text           string
	}{
		{"missing", "get_object", effects.KindFatal, "NoSuchBucket"},
		{"denied", "put_object", effects.KindFatal, "AccessDenied"},
		{"denied", "head_object", effects.KindFatal, "Forbidden"},
		{"slow", "get_object", effects.KindRetryable, "SlowDown"},
		{"slow", "put_object", effects.KindRetryable, "SlowDown"},
		{"broken", "get_object", effects.KindRetryable, "InternalError"},
		{"broken", "put_object", effects.KindUnknownOutcome, "InternalError"},
		{"broken", "delete_object", effects.KindUnknownOutcome, "InternalError"},
		{"elsewhere", "list_objects", effects.KindFatal, "region eu-west-1"},
	}
	for _, c := range cases {
		_, err := h.call(c.action, map[string]any{"bucket": c.bucket, "key": "k", "body": "x"})
		if effects.Classify(err) != c.want || !strings.Contains(fmt.Sprint(err), c.text) {
			t.Errorf("%s %s: %v (%v), want %v containing %q", c.bucket, c.action, err, effects.Classify(err), c.want, c.text)
		}
	}
	bad := creds()
	bad["secret_access_key"] = "wrong"
	if _, err := h.callWith(bad, "get_object", map[string]any{"key": "k"}); effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "SignatureDoesNotMatch") {
		t.Errorf("bad secret: %v", err)
	}
	if _, err := h.callWith(map[string]string{"access_key_id": testAK}, "get_object", map[string]any{"key": "k"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("missing secret: %v", err)
	}
	noBucket := creds()
	delete(noBucket, "bucket")
	if _, err := h.callWith(noBucket, "get_object", map[string]any{"key": "k"}); effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "bucket") {
		t.Errorf("no bucket: %v", err)
	}
	for _, k := range []string{"", "a/../b", "./x", strings.Repeat("k", 1025)} {
		if _, err := h.call("get_object", map[string]any{"key": k}); effects.Classify(err) != effects.KindFatal {
			t.Errorf("key %q accepted: %v", k, err)
		}
	}
	if _, err := h.call("put_object", map[string]any{"key": "m", "body": "x", "metadata": map[string]any{"note": "naïve"}}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("non-ASCII metadata accepted: %v", err)
	}
	// Transport: nothing listening proves nothing was sent. Pooled
	// connections are dropped first: a write on a connection the server
	// closed after accepting it is (rightly) an unknown outcome instead.
	h.srv.Close()
	if tr, ok := h.hc.Transport.(interface{ CloseIdleConnections() }); ok {
		tr.CloseIdleConnections()
	}
	h.hc.CloseIdleConnections()
	if _, err := h.call("put_object", map[string]any{"key": "k", "body": "x"}); effects.Classify(err) != effects.KindNotSent {
		t.Errorf("connection refused: %v (%v)", err, effects.Classify(err))
	}
}

func TestSessionTokenIsSigned(t *testing.T) {
	h := newHarness(t)
	cr := creds()
	cr["session_token"] = testToken
	if _, err := h.callWith(cr, "put_object", map[string]any{"key": "t", "body": "x"}); err != nil {
		t.Fatal(err)
	}
	if h.fake.tokens[len(h.fake.tokens)-1] != testToken {
		t.Errorf("token %v", h.fake.tokens)
	}
}

func TestPresignedURLsWork(t *testing.T) {
	h := newHarness(t)
	cr := creds()
	cr["session_token"] = testToken
	out, err := h.callWith(cr, "presign_url", map[string]any{"key": "up/new file.txt", "method": "PUT", "content_type": "text/plain", "expires_in": int64(600)})
	if err != nil {
		t.Fatal(err)
	}
	if out["expires_at"] != "2026-10-06T09:40:00Z" || out["headers"].(map[string]any)["Content-Type"] != "text/plain" {
		t.Errorf("presign output %v", out)
	}
	// Anyone with the link can upload, with no credentials of their own.
	r, _ := http.NewRequest(http.MethodPut, out["url"].(string), strings.NewReader("uploaded"))
	r.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(h.fake.objects["reports/up/new file.txt"].body) != "uploaded" {
		t.Fatalf("presigned PUT: %d", resp.StatusCode)
	}
	out, err = h.callWith(cr, "presign_url", map[string]any{"key": "up/new file.txt"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.Get(out["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(b) != "uploaded" {
		t.Errorf("presigned GET: %d %s", resp.StatusCode, b)
	}
	// Tampering with the link breaks it.
	resp, err = http.Get(strings.Replace(out["url"].(string), "new%20file", "other", 1))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("tampered link: %d", resp.StatusCode)
	}
	if _, err := h.call("presign_url", map[string]any{"key": "k", "expires_in": int64(604801)}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("over 7 days: %v", err)
	}
	if _, err := h.call("presign_url", map[string]any{"key": "k", "method": "DELETE"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("DELETE link: %v", err)
	}
}

// policyFor is the egress policy the worker builds from the manifest.
func policyFor(c *connector.Connector, cr map[string]string) egress.Policy {
	var p egress.Policy
	for _, h := range c.Manifest.Hosts() {
		if h == "${connection.host}" {
			h = cr["host"]
		}
		if h != "" {
			p.Hosts = append(p.Hosts, h)
		}
	}
	return p
}

func TestAddressingAndEgress(t *testing.T) {
	c := New(Options{})
	cl := &client{now: time.Now}
	cases := []struct {
		creds   map[string]string
		bucket  string
		wantURL string
	}{
		{map[string]string{"region": "eu-west-2"}, "my-bucket", "https://my-bucket.s3.eu-west-2.amazonaws.com/a%20b/c.txt"},
		{map[string]string{}, "my-bucket", "https://my-bucket.s3.us-east-1.amazonaws.com/a%20b/c.txt"},
		{map[string]string{"region": "eu-west-2"}, "my.dotted.bucket", "https://s3.eu-west-2.amazonaws.com/my.dotted.bucket/a%20b/c.txt"},
		{map[string]string{"region": "eu-west-2", "path_style": "true"}, "my-bucket", "https://s3.eu-west-2.amazonaws.com/my-bucket/a%20b/c.txt"},
		{map[string]string{"region": "cn-north-1"}, "my-bucket", "https://my-bucket.s3.cn-north-1.amazonaws.com.cn/a%20b/c.txt"},
		{map[string]string{"region": "auto", "host": "acct123.r2.cloudflarestorage.com"}, "my-bucket", "https://acct123.r2.cloudflarestorage.com/my-bucket/a%20b/c.txt"},
		{map[string]string{"host": "minio.example.com", "port": "9000"}, "My_Bucket", "https://minio.example.com:9000/My_Bucket/a%20b/c.txt"},
	}
	for _, tc := range cases {
		tc.creds["access_key_id"], tc.creds["secret_access_key"] = "a", "b"
		e, err := cl.endpoint(connector.Request{Credentials: tc.creds})
		if err != nil {
			t.Fatal(err)
		}
		u := e.url(tc.bucket, "a b/c.txt", nil)
		if u.String() != tc.wantURL {
			t.Errorf("%v: url %s, want %s", tc.creds, u, tc.wantURL)
		}
		if !policyFor(c, tc.creds).Allows(u.Hostname()) {
			t.Errorf("%v: egress would refuse %s", tc.creds, u.Hostname())
		}
	}
	for _, bad := range []map[string]string{
		{"host": "https://minio.example.com"}, {"host": "minio.example.com/path"}, {"host": "minio.example.com:9000"},
		{"host": "minio.example.com", "port": "http"}, {"region": "us-east-1.evil.com"}, {"region": "x/y"},
	} {
		bad["access_key_id"], bad["secret_access_key"] = "a", "b"
		if _, err := cl.endpoint(connector.Request{Credentials: bad}); effects.Classify(err) != effects.KindFatal {
			t.Errorf("%v accepted", bad)
		}
	}
	// The default policy must not reach arbitrary hosts.
	if policyFor(c, map[string]string{}).Allows("example.com") || policyFor(c, map[string]string{}).Allows("amazonaws.com.evil.io") {
		t.Error("policy too wide")
	}
}

func TestClassify(t *testing.T) {
	for _, c := range []struct {
		status int
		code   string
		write  bool
		want   error
	}{
		{503, "SlowDown", true, effects.ErrRetryable},
		{503, "", false, effects.ErrRetryable},
		{429, "", true, effects.ErrRetryable},
		{500, "InternalError", false, effects.ErrRetryable},
		{500, "InternalError", true, effects.ErrUnknownOutcome},
		{502, "", true, effects.ErrUnknownOutcome},
		{400, "RequestTimeout", true, effects.ErrRetryable},
		{307, "TemporaryRedirect", false, effects.ErrRetryable},
		{301, "PermanentRedirect", false, effects.ErrFatal},
		{403, "AccessDenied", false, effects.ErrFatal},
		{403, "RequestTimeTooSkewed", false, effects.ErrFatal},
		{400, "ExpiredToken", false, effects.ErrFatal},
		{404, "NoSuchBucket", true, effects.ErrFatal},
	} {
		if got := classify(c.status, c.code, c.write); !errors.Is(got, c.want) {
			t.Errorf("%d %s write=%v: %v, want %v", c.status, c.code, c.write, got, c.want)
		}
	}
}
