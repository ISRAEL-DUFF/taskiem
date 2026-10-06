package s3

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Example values AWS publishes for SigV4: the S3 "signature calculation"
// examples (bucket examplebucket, 24 May 2013) and the generic SigV4 test
// suite's get-vanilla case (30 August 2015). The credentials are AWS's
// documented example keys, not real ones.
var (
	s3Example = Signer{AccessKeyID: "AKIAIOSFODNN7EXAMPLE", SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", Region: "us-east-1", Service: "s3"}
	s3Time    = time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
)

func sigOf(t *testing.T, r *http.Request) string {
	t.Helper()
	_, sig, ok := strings.Cut(r.Header.Get("Authorization"), "Signature=")
	if !ok {
		t.Fatalf("no signature in %q", r.Header.Get("Authorization"))
	}
	return sig
}

func TestSigV4TestSuiteGetVanilla(t *testing.T) {
	s := Signer{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", Region: "us-east-1", Service: "service"}
	r, _ := http.NewRequest("GET", "https://example.amazonaws.com/", nil)
	s.Sign(r, EmptyPayloadHash, time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC))
	want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"
	if got := r.Header.Get("Authorization"); got != want {
		t.Errorf("get-vanilla\n got %s\nwant %s", got, want)
	}
}

func TestS3ExampleGetObject(t *testing.T) {
	r, _ := http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	r.Header.Set("Range", "bytes=0-9")
	r.Header.Set("X-Amz-Content-Sha256", EmptyPayloadHash)
	s3Example.Sign(r, EmptyPayloadHash, s3Time)
	if !strings.Contains(r.Header.Get("Authorization"), "SignedHeaders=host;range;x-amz-content-sha256;x-amz-date,") {
		t.Errorf("signed headers: %s", r.Header.Get("Authorization"))
	}
	if got := sigOf(t, r); got != "f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41" {
		t.Errorf("GET object signature %s", got)
	}
}

func TestS3ExamplePutObject(t *testing.T) {
	body := "Welcome to Amazon S3."
	u := &url.URL{Scheme: "https", Host: "examplebucket.s3.amazonaws.com", Path: "/test$file.text", RawPath: "/" + uriEncode("test$file.text", true)}
	r, _ := http.NewRequest("PUT", u.String(), strings.NewReader(body))
	r.Header.Set("Date", "Fri, 24 May 2013 00:00:00 GMT")
	r.Header.Set("X-Amz-Storage-Class", "REDUCED_REDUNDANCY")
	h := sha256Hex([]byte(body))
	if h != "44ce7dd67c959e0d3524ffac1771dfbba87d2b6b4b4e99e42034a8b803f8b072" {
		t.Fatalf("payload hash %s", h)
	}
	r.Header.Set("X-Amz-Content-Sha256", h)
	s3Example.Sign(r, h, s3Time)
	if r.URL.EscapedPath() != "/test%24file.text" {
		t.Errorf("path sent %s", r.URL.EscapedPath())
	}
	if got := sigOf(t, r); got != "98ad721746da40c64f1a55b78f14c238d841ea1380cd77a1b5971af0ece108bd" {
		t.Errorf("PUT object signature %s", got)
	}
}

func TestS3ExampleBucketRequests(t *testing.T) {
	for _, c := range []struct{ query, want string }{
		{"lifecycle=", "fea454ca298b7da1c68078a5d1bdbfbbe0d65c699e0f91ac7a200a0136783543"},
		{"max-keys=2&prefix=J", "34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7"},
	} {
		r, _ := http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com/?"+c.query, nil)
		r.Header.Set("X-Amz-Content-Sha256", EmptyPayloadHash)
		s3Example.Sign(r, EmptyPayloadHash, s3Time)
		if got := sigOf(t, r); got != c.want {
			t.Errorf("%s: signature %s", c.query, got)
		}
	}
}

func TestS3ExamplePresignedGet(t *testing.T) {
	u, _ := url.Parse("https://examplebucket.s3.amazonaws.com/test.txt")
	p := s3Example.Presign("GET", u, nil, 86400*time.Second, s3Time)
	if got := p.Query().Get("X-Amz-Signature"); got != "aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404" {
		t.Errorf("presigned signature %s (url %s)", got, p)
	}
	if p.Query().Get("X-Amz-Credential") != "AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request" || p.Query().Get("X-Amz-SignedHeaders") != "host" {
		t.Errorf("presigned query %s", p.RawQuery)
	}
}

func TestURIEncode(t *testing.T) {
	cases := map[string]string{
		"photos/Jan/sample.jpg": "photos/Jan/sample.jpg",
		"a b+c~d*e":             "a%20b%2Bc~d%2Ae",
		"naïve.txt":             "na%C3%AFve.txt",
	}
	for in, want := range cases {
		if got := uriEncode(in, true); got != want {
			t.Errorf("uriEncode(%q) = %q, want %q", in, got, want)
		}
	}
	if uriEncode("a/b", false) != "a%2Fb" {
		t.Error("slash must be encoded outside keys")
	}
	if got := canonicalQuery(url.Values{"prefix": {"somePrefix"}, "marker": {"someMarker"}, "max-keys": {"20"}, "acl": {""}}); got != "acl=&marker=someMarker&max-keys=20&prefix=somePrefix" {
		t.Errorf("canonical query %s", got)
	}
}
