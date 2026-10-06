package s3

// AWS Signature Version 4, written from AWS's public description of the
// algorithm ("Create a signed AWS API request", IAM User Guide,
// docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv-create-signed-request.html,
// read 6 October 2026). No SDK code was consulted.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	algorithm = "AWS4-HMAC-SHA256"
	// EmptyPayloadHash is the hex SHA-256 of the empty string.
	EmptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	// UnsignedPayload is what S3 accepts in place of a payload hash, and what
	// presigned URLs sign.
	UnsignedPayload = "UNSIGNED-PAYLOAD"
	amzDateFormat   = "20060102T150405Z"
)

// Signer signs requests with SigV4.
type Signer struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string // temporary credentials only
	Region          string
	Service         string // "s3"
}

// unsignedHeaders are never signed: proxies and transports may change them
// (the IAM guide's list of hop-by-hop headers), or they are the signature.
var unsignedHeaders = map[string]bool{
	"authorization": true, "user-agent": true, "connection": true, "keep-alive": true,
	"transfer-encoding": true, "te": true, "trailer": true, "upgrade": true,
	"proxy-authorization": true, "proxy-authenticate": true, "expect": true,
	"x-amzn-trace-id": true, "accept-encoding": true, "content-length": true,
}

// uriEncode encodes every byte except the unreserved characters A-Z a-z 0-9
// - . _ ~, with upper-case hex; '/' is kept only when keepSlash (object key
// paths).
func uriEncode(s string, keepSlash bool) string {
	const hexUpper = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s) * 3)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		case c == '/' && keepSlash:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexUpper[c>>4])
			b.WriteByte(hexUpper[c&15])
		}
	}
	return b.String()
}

// canonicalQuery encodes each name and value, then sorts by name (and by
// value for repeated names). The result is also a valid raw query string,
// so callers send exactly what they sign.
func canonicalQuery(q url.Values) string {
	type kv struct{ k, v string }
	var pairs []kv
	for k, vs := range q {
		for _, v := range vs {
			pairs = append(pairs, kv{uriEncode(k, false), uriEncode(v, false)})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].k != pairs[j].k {
			return pairs[i].k < pairs[j].k
		}
		return pairs[i].v < pairs[j].v
	})
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = p.k + "=" + p.v
	}
	return strings.Join(parts, "&")
}

// trimValue trims a header value and collapses runs of spaces.
func trimValue(v string) string {
	return strings.Join(strings.Fields(v), " ")
}

// canonicalHeaders returns the canonical header block and the signed
// header list for host plus every signable header in h.
func canonicalHeaders(host string, h http.Header) (canon, signed string) {
	vals := map[string][]string{"host": {host}}
	for k, vs := range h {
		lk := strings.ToLower(k)
		if unsignedHeaders[lk] || lk == "host" {
			continue
		}
		for _, v := range vs {
			vals[lk] = append(vals[lk], trimValue(v))
		}
	}
	names := make([]string, 0, len(vals))
	for k := range vals {
		names = append(names, k)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n)
		b.WriteByte(':')
		b.WriteString(strings.Join(vals[n], ","))
		b.WriteByte('\n')
	}
	return b.String(), strings.Join(names, ";")
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// signingKey derives the key for one date, region and service.
func signingKey(secret, date, region, service string) []byte {
	k := hmacSHA256([]byte("AWS4"+secret), date)
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, service)
	return hmacSHA256(k, "aws4_request")
}

func (s Signer) scope(t time.Time) string {
	return t.Format("20060102") + "/" + s.Region + "/" + s.Service + "/aws4_request"
}

// signature computes the hex signature of a canonical request.
func (s Signer) signature(canonicalRequest string, t time.Time) string {
	sts := algorithm + "\n" + t.Format(amzDateFormat) + "\n" + s.scope(t) + "\n" + sha256Hex([]byte(canonicalRequest))
	return hex.EncodeToString(hmacSHA256(signingKey(s.SecretAccessKey, t.Format("20060102"), s.Region, s.Service), sts))
}

// canonicalRequest builds the canonical request for method, an already
// encoded path, a query and headers.
func canonicalRequest(method, escapedPath string, q url.Values, host string, h http.Header, payloadHash string) (string, string) {
	if escapedPath == "" {
		escapedPath = "/"
	}
	ch, signed := canonicalHeaders(host, h)
	return strings.Join([]string{method, escapedPath, canonicalQuery(q), ch, signed, payloadHash}, "\n"), signed
}

// Sign adds X-Amz-Date, X-Amz-Security-Token (temporary credentials) and
// Authorization to r. Every header already on r, except hop-by-hop ones,
// is signed, so set them (including X-Amz-Content-Sha256 for S3) first.
// The path signed is r.URL.EscapedPath(), which is what is sent.
func (s Signer) Sign(r *http.Request, payloadHash string, t time.Time) {
	t = t.UTC()
	r.Header.Set("X-Amz-Date", t.Format(amzDateFormat))
	if s.SessionToken != "" {
		r.Header.Set("X-Amz-Security-Token", s.SessionToken)
	}
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	cr, signed := canonicalRequest(r.Method, r.URL.EscapedPath(), r.URL.Query(), host, r.Header, payloadHash)
	r.Header.Set("Authorization", algorithm+" Credential="+s.AccessKeyID+"/"+s.scope(t)+
		", SignedHeaders="+signed+", Signature="+s.signature(cr, t))
}

// Presign returns u with SigV4 query authentication for method, valid for
// expires. headers are headers the eventual caller must send exactly (for
// example Content-Type on a PUT); host is always signed. The payload is
// unsigned, as S3 requires for presigned URLs.
func (s Signer) Presign(method string, u *url.URL, headers http.Header, expires time.Duration, t time.Time) *url.URL {
	t = t.UTC()
	q := u.Query()
	_, signed := canonicalHeaders(u.Host, headers)
	q.Set("X-Amz-Algorithm", algorithm)
	q.Set("X-Amz-Credential", s.AccessKeyID+"/"+s.scope(t))
	q.Set("X-Amz-Date", t.Format(amzDateFormat))
	q.Set("X-Amz-Expires", strconv.Itoa(int(expires/time.Second)))
	q.Set("X-Amz-SignedHeaders", signed)
	if s.SessionToken != "" {
		q.Set("X-Amz-Security-Token", s.SessionToken)
	}
	cr, _ := canonicalRequest(method, u.EscapedPath(), q, u.Host, headers, UnsignedPayload)
	sig := s.signature(cr, t)
	out := *u
	out.RawQuery = canonicalQuery(q) + "&X-Amz-Signature=" + sig
	return &out
}
