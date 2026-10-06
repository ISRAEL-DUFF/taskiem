// Package s3 is the Amazon S3 connector, also for S3-compatible stores
// (Cloudflare R2, MinIO, DigitalOcean Spaces, Wasabi). Requests are signed
// with AWS Signature Version 4, implemented here from AWS's public
// documentation (docs/integrations/s3.md).
//
// Classes: put, copy and delete are idempotent_write because repeating them
// leaves the bucket as one call would (same body to the same key, same
// source to the same key, a key that stays deleted). S3 takes no
// idempotency key, so the engine's key is only logged.
package s3

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // Content-MD5 is S3's integrity check, not a security control.
	_ "embed"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

//go:embed manifest.yaml
var manifest []byte

const (
	// MaxPutBytes caps put_object's decoded body (workflow inputs live in
	// the run's history).
	MaxPutBytes = 10 << 20
	// MaxGetBytes is the largest max_bytes get_object accepts.
	MaxGetBytes      = 10 << 20
	defaultGetBytes  = 1 << 20
	defaultRegion    = "us-east-1"
	maxErrorBodySize = 64 << 10
	maxListBodySize  = 8 << 20
)

// Options configure the connector.
type Options struct {
	// BaseURL sends every request to this endpoint with path-style
	// addressing and makes its host the only one the connector may reach
	// (tests, a platform-run MinIO). Platform configuration, never tenant
	// input; http is allowed here only.
	BaseURL string
	// Now overrides the clock (tests).
	Now func() time.Time
}

// New returns the S3 connector.
func New(o Options) *connector.Connector {
	m := connector.MustParse(manifest)
	c := &client{now: o.Now}
	if c.now == nil {
		c.now = time.Now
	}
	if o.BaseURL != "" {
		u, err := url.Parse(strings.TrimRight(o.BaseURL, "/"))
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			panic("s3: bad base URL " + o.BaseURL)
		}
		c.base = u
		m.OverrideBaseURL(o.BaseURL)
	}
	return &connector.Connector{Manifest: m, Actions: map[string]connector.Action{
		"put_object":    connector.ActionFunc(c.putObject),
		"get_object":    connector.ActionFunc(c.getObject),
		"head_object":   connector.ActionFunc(c.headObject),
		"list_objects":  connector.ActionFunc(c.listObjects),
		"delete_object": connector.ActionFunc(c.deleteObject),
		"copy_object":   connector.ActionFunc(c.copyObject),
		"presign_url":   connector.ActionFunc(c.presignURL),
	}}
}

type client struct {
	base *url.URL
	now  func() time.Time
}

// endpoint is one connection's resolved addressing.
type endpoint struct {
	signer    Signer
	scheme    string
	host      string // host[:port]
	pathStyle bool   // false: virtual-hosted (bucket.host)
	bucket    string // the connection's default
}

var (
	regionRE   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)
	hostnameRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	// Bucket names: AWS's rules for new buckets allow 3-63 of [a-z0-9.-];
	// S3-compatible stores and old us-east-1 buckets may also use
	// upper case and underscores, which work with path-style only.
	bucketRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{1,254}$`)
	dnsBucketRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
	metaNameRE  = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

func fatalf(format string, args ...any) error {
	return fmt.Errorf("s3: "+format+": %w", append(args, effects.ErrFatal)...)
}

func (c *client) endpoint(req connector.Request) (*endpoint, error) {
	cr := req.Credentials
	ak, sk := strings.TrimSpace(cr["access_key_id"]), strings.TrimSpace(cr["secret_access_key"])
	if ak == "" || sk == "" {
		return nil, fatalf("the connection needs access_key_id and secret_access_key")
	}
	region := strings.ToLower(strings.TrimSpace(cr["region"]))
	if region == "" {
		region = defaultRegion
	}
	if !regionRE.MatchString(region) {
		return nil, fatalf("region %q is not a region code (for example eu-west-2, or auto for R2)", region)
	}
	e := &endpoint{
		signer: Signer{AccessKeyID: ak, SecretAccessKey: sk, SessionToken: strings.TrimSpace(cr["session_token"]), Region: region, Service: "s3"},
		bucket: strings.TrimSpace(cr["bucket"]),
	}
	host := strings.TrimSpace(cr["host"])
	switch {
	case c.base != nil:
		e.scheme, e.host, e.pathStyle = c.base.Scheme, c.base.Host, true
	case host != "":
		if strings.Contains(host, "://") || !hostnameRE.MatchString(host) {
			return nil, fatalf("host %q must be a bare host name such as minio.example.com (no scheme, path or port; the port has its own field)", host)
		}
		e.scheme, e.host, e.pathStyle = "https", host, true
		if p := strings.TrimSpace(cr["port"]); p != "" && p != "443" {
			n, err := strconv.Atoi(p)
			if err != nil || n < 1 || n > 65535 {
				return nil, fatalf("port %q is not a port number", p)
			}
			e.host = net.JoinHostPort(host, p)
		}
	default:
		domain := "amazonaws.com"
		if strings.HasPrefix(region, "cn-") {
			domain = "amazonaws.com.cn"
		}
		e.scheme, e.host = "https", "s3."+region+"."+domain
		e.pathStyle = strings.EqualFold(strings.TrimSpace(cr["path_style"]), "true")
	}
	return e, nil
}

func str(in map[string]any, k string) string {
	s, _ := in[k].(string)
	return s
}

func intArg(in map[string]any, k string, def int64) int64 {
	switch v := in[k].(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	case interface{ Int64() (int64, error) }:
		if n, err := v.Int64(); err == nil {
			return n
		}
	}
	return def
}

func (e *endpoint) bucketOf(in map[string]any, field string) (string, error) {
	b := strings.TrimSpace(str(in, field))
	if b == "" {
		b = e.bucket
	}
	if b == "" {
		return "", fatalf("no %s: name one in the step or set the connection's default bucket", field)
	}
	if !bucketRE.MatchString(b) {
		return "", fatalf("bucket %q is not a valid bucket name", b)
	}
	return b, nil
}

func checkKey(key string) error {
	if key == "" || len(key) > 1024 {
		return fatalf("object key must be 1 to 1024 bytes")
	}
	if !utf8.ValidString(key) {
		return fatalf("object key must be UTF-8")
	}
	// "." and ".." segments may be normalised away by proxies and HTTP
	// libraries, which would sign one key and address another.
	for _, seg := range strings.Split(key, "/") {
		if seg == "." || seg == ".." {
			return fatalf("object key %q has a . or .. segment, which this connector refuses", key)
		}
	}
	return nil
}

// url addresses bucket (and key, when not empty) with q as the query.
func (e *endpoint) url(bucket, key string, q url.Values) *url.URL {
	u := &url.URL{Scheme: e.scheme, Host: e.host}
	// Virtual-hosted style needs a DNS-safe bucket without dots (dots
	// break TLS wildcard certificates).
	virtual := !e.pathStyle && dnsBucketRE.MatchString(bucket)
	var p string
	if virtual {
		u.Host = bucket + "." + e.host
		p = "/" + key
	} else {
		p = "/" + bucket
		if key != "" {
			p += "/" + key
		}
	}
	u.Path, u.RawPath = p, uriEncode(p, true)
	if len(q) > 0 {
		u.RawQuery = canonicalQuery(q)
	}
	return u
}

// noRedirects is the egress-guarded client with redirects off: a signed
// request is valid for one host only, and S3's redirects signal a wrong
// region, which is reported instead.
func noRedirects(hc *http.Client) *http.Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	c := *hc
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

// send signs and sends one request.
func (c *client) send(ctx context.Context, req connector.Request, e *endpoint, method string, u *url.URL, h http.Header, body []byte) (*http.Response, error) {
	var rd io.Reader
	payload := EmptyPayloadHash
	if body != nil {
		rd = bytes.NewReader(body)
		payload = sha256Hex(body)
	}
	r, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, fatalf("%v", err)
	}
	for k, vs := range h {
		r.Header[k] = vs
	}
	r.Header.Set("X-Amz-Content-Sha256", payload)
	e.signer.Sign(r, payload, c.now())
	resp, err := noRedirects(req.HTTP).Do(r)
	if err != nil {
		return nil, connector.ClassifyTransport(err)
	}
	return resp, nil
}

// Error is an error response from S3.
type Error struct {
	Status    int
	Code      string
	Message   string
	RequestID string
	Region    string // the bucket's region, when S3 says so
	kind      error
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "s3 %d", e.Status)
	if e.Code != "" {
		b.WriteString(" " + e.Code)
	}
	if e.Message != "" {
		b.WriteString(": " + e.Message)
	}
	if e.Region != "" {
		fmt.Fprintf(&b, " (the bucket is in region %s: set the connection's region to it)", e.Region)
	}
	if e.RequestID != "" {
		b.WriteString(" [request id " + e.RequestID + "]")
	}
	return b.String()
}

func (e *Error) Unwrap() error { return e.kind }

type xmlError struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	RequestID string   `xml:"RequestId"`
	Region    string   `xml:"Region"`
	Endpoint  string   `xml:"Endpoint"`
}

// classify decides what an S3 error means for the engine. Throttling and
// 503 are refusals before processing; 500-class errors may have acted, which
// only matters for writes; everything else (AccessDenied, NoSuchBucket,
// NoSuchKey, SignatureDoesNotMatch, wrong region) needs a person.
func classify(status int, code string, write bool) error {
	switch code {
	case "SlowDown", "ServiceUnavailable", "Throttling", "ThrottlingException", "RequestLimitExceeded",
		"TooManyRequests", "RequestThrottled", "RequestTimeout", "OperationAborted", "TemporaryRedirect":
		// RequestTimeout: S3 did not receive the whole request, so wrote
		// nothing. OperationAborted: a conflicting operation is in progress.
		// TemporaryRedirect: a new bucket's DNS is still updating.
		return effects.ErrRetryable
	case "InternalError":
		if write {
			return effects.ErrUnknownOutcome
		}
		return effects.ErrRetryable
	}
	switch {
	case status == http.StatusServiceUnavailable, status == http.StatusTooManyRequests:
		return effects.ErrRetryable
	case status >= 500:
		if write {
			return effects.ErrUnknownOutcome
		}
		return effects.ErrRetryable
	}
	return effects.ErrFatal
}

// apiError reads an error response.
func apiError(resp *http.Response, write bool) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
	e := &Error{Status: resp.StatusCode, RequestID: resp.Header.Get("X-Amz-Request-Id"), Region: resp.Header.Get("X-Amz-Bucket-Region")}
	var x xmlError
	if xml.Unmarshal(raw, &x) == nil {
		e.Code, e.Message = x.Code, x.Message
		if x.RequestID != "" {
			e.RequestID = x.RequestID
		}
		if e.Region == "" {
			e.Region = x.Region
		}
	}
	if e.Code == "" {
		switch resp.StatusCode {
		case http.StatusNotFound:
			e.Code = "NotFound"
		case http.StatusForbidden:
			e.Code = "Forbidden"
		case http.StatusMovedPermanently:
			e.Code = "PermanentRedirect"
		}
		if e.Message == "" && len(raw) > 0 && !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("<")) {
			e.Message = strings.TrimSpace(string(raw[:min(len(raw), 300)]))
		}
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 && e.Message == "" {
		e.Message = "S3 redirected the request; the bucket is in another region or needs another endpoint"
	}
	e.kind = classify(resp.StatusCode, e.Code, write)
	return e
}

func closeBody(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}

func trimETag(s string) string { return strings.Trim(s, `"`) }

func httpTime(s string) string {
	if t, err := http.ParseTime(s); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return s
}

// objectInfo reads the headers GET and HEAD share.
func objectInfo(bucket, key string, h http.Header) map[string]any {
	meta := map[string]any{}
	for k, vs := range h {
		if name, ok := strings.CutPrefix(strings.ToLower(k), "x-amz-meta-"); ok && len(vs) > 0 {
			meta[name] = vs[0]
		}
	}
	size, _ := strconv.ParseInt(h.Get("Content-Length"), 10, 64)
	return map[string]any{
		"bucket": bucket, "key": key, "size": size,
		"content_type": h.Get("Content-Type"), "etag": trimETag(h.Get("ETag")),
		"last_modified": httpTime(h.Get("Last-Modified")), "version_id": h.Get("X-Amz-Version-Id"),
		"cache_control": h.Get("Cache-Control"), "content_disposition": h.Get("Content-Disposition"),
		"metadata": meta,
	}
}

// metadataHeaders validates user metadata and sets x-amz-meta-* headers.
func metadataHeaders(in map[string]any, h http.Header) error {
	m, _ := in["metadata"].(map[string]any)
	total := 0
	for k, v := range m {
		s, isStr := v.(string)
		if !isStr {
			return fatalf("metadata %q must be a string", k)
		}
		if !metaNameRE.MatchString(k) {
			return fatalf("metadata name %q may use only letters, digits, - and _", k)
		}
		for _, r := range s {
			if r < 0x20 || r > 0x7e {
				return fatalf("metadata %q must be printable ASCII (encode other text, for example as base64)", k)
			}
		}
		total += len(k) + len(s)
		h.Set("X-Amz-Meta-"+k, s)
	}
	if total > 2048 {
		return fatalf("user metadata is %d bytes; S3 allows 2 KB", total)
	}
	return nil
}

func logKey(req connector.Request, action, bucket, key string) {
	if req.Logger != nil {
		req.Logger.Info("s3 write", "action", action, "bucket", bucket, "key", key, "idempotency_key", req.IdempotencyKey)
	}
}

func (c *client) putObject(ctx context.Context, req connector.Request) (connector.Response, error) {
	e, err := c.endpoint(req)
	if err != nil {
		return connector.Response{}, err
	}
	in := req.Input
	bucket, err := e.bucketOf(in, "bucket")
	if err != nil {
		return connector.Response{}, err
	}
	key := str(in, "key")
	if err := checkKey(key); err != nil {
		return connector.Response{}, err
	}
	raw, ok := in["body"].(string)
	if !ok {
		return connector.Response{}, fatalf("body must be a string")
	}
	var body []byte
	ct := str(in, "content_type")
	switch enc := str(in, "body_encoding"); enc {
	case "", "text":
		body = []byte(raw)
		if ct == "" {
			ct = "text/plain; charset=utf-8"
		}
	case "base64":
		if body, err = base64.StdEncoding.DecodeString(raw); err != nil {
			return connector.Response{}, fatalf("body is not valid base64: %v", err)
		}
		if ct == "" {
			ct = "application/octet-stream"
		}
	default:
		return connector.Response{}, fatalf("body_encoding %q must be text or base64", enc)
	}
	if len(body) > MaxPutBytes {
		return connector.Response{}, fatalf("body is %d bytes; put_object takes at most %d", len(body), MaxPutBytes)
	}
	h := http.Header{}
	h.Set("Content-Type", ct)
	sum := md5.Sum(body) //nolint:gosec // see import
	h.Set("Content-Md5", base64.StdEncoding.EncodeToString(sum[:]))
	if v := str(in, "cache_control"); v != "" {
		h.Set("Cache-Control", v)
	}
	if v := str(in, "content_disposition"); v != "" {
		h.Set("Content-Disposition", v)
	}
	if err := metadataHeaders(in, h); err != nil {
		return connector.Response{}, err
	}
	logKey(req, "put_object", bucket, key)
	resp, err := c.send(ctx, req, e, http.MethodPut, e.url(bucket, key, nil), h, body)
	if err != nil {
		return connector.Response{}, err
	}
	defer closeBody(resp)
	if !ok200(resp) {
		return connector.Response{}, apiError(resp, true)
	}
	return connector.Response{Output: map[string]any{
		"bucket": bucket, "key": key, "etag": trimETag(resp.Header.Get("ETag")),
		"version_id": resp.Header.Get("X-Amz-Version-Id"), "size": int64(len(body)),
	}}, nil
}

func ok200(resp *http.Response) bool { return resp.StatusCode >= 200 && resp.StatusCode < 300 }

func (c *client) getObject(ctx context.Context, req connector.Request) (connector.Response, error) {
	e, err := c.endpoint(req)
	if err != nil {
		return connector.Response{}, err
	}
	in := req.Input
	bucket, err := e.bucketOf(in, "bucket")
	if err != nil {
		return connector.Response{}, err
	}
	key := str(in, "key")
	if err := checkKey(key); err != nil {
		return connector.Response{}, err
	}
	enc := str(in, "encoding")
	if enc == "" {
		enc = "text"
	}
	if enc != "text" && enc != "base64" {
		return connector.Response{}, fatalf("encoding %q must be text or base64", enc)
	}
	limit := intArg(in, "max_bytes", defaultGetBytes)
	if limit < 1 || limit > MaxGetBytes {
		return connector.Response{}, fatalf("max_bytes must be 1 to %d", MaxGetBytes)
	}
	var q url.Values
	if v := str(in, "version_id"); v != "" {
		q = url.Values{"versionId": {v}}
	}
	resp, err := c.send(ctx, req, e, http.MethodGet, e.url(bucket, key, q), nil, nil)
	if err != nil {
		return connector.Response{}, err
	}
	defer closeBody(resp)
	if !ok200(resp) {
		return connector.Response{}, apiError(resp, false)
	}
	if resp.ContentLength > limit {
		return connector.Response{}, fatalf("object %s is %d bytes, over max_bytes %d", key, resp.ContentLength, limit)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return connector.Response{}, fmt.Errorf("s3: reading object: %w: %w", err, effects.ErrRetryable)
	}
	if int64(len(data)) > limit {
		return connector.Response{}, fatalf("object %s is over max_bytes %d", key, limit)
	}
	out := objectInfo(bucket, key, resp.Header)
	out["size"] = int64(len(data))
	out["encoding"] = enc
	if enc == "text" {
		if !utf8.Valid(data) {
			return connector.Response{}, fatalf("object %s is not UTF-8 text; get it with encoding base64", key)
		}
		out["body"] = string(data)
	} else {
		out["body"] = base64.StdEncoding.EncodeToString(data)
	}
	return connector.Response{Output: out}, nil
}

func (c *client) headObject(ctx context.Context, req connector.Request) (connector.Response, error) {
	e, err := c.endpoint(req)
	if err != nil {
		return connector.Response{}, err
	}
	in := req.Input
	bucket, err := e.bucketOf(in, "bucket")
	if err != nil {
		return connector.Response{}, err
	}
	key := str(in, "key")
	if err := checkKey(key); err != nil {
		return connector.Response{}, err
	}
	var q url.Values
	if v := str(in, "version_id"); v != "" {
		q = url.Values{"versionId": {v}}
	}
	resp, err := c.send(ctx, req, e, http.MethodHead, e.url(bucket, key, q), nil, nil)
	if err != nil {
		return connector.Response{}, err
	}
	defer closeBody(resp)
	if resp.StatusCode == http.StatusNotFound {
		// HEAD has no body, so NoSuchKey and NoSuchBucket look the same;
		// a missing bucket is also reported here as a missing object.
		return connector.Response{Output: map[string]any{"exists": false, "bucket": bucket, "key": key}}, nil
	}
	if !ok200(resp) {
		return connector.Response{}, apiError(resp, false)
	}
	out := objectInfo(bucket, key, resp.Header)
	out["exists"] = true
	out["storage_class"] = resp.Header.Get("X-Amz-Storage-Class")
	return connector.Response{Output: out}, nil
}

type listResult struct {
	IsTruncated           bool   `xml:"IsTruncated"`
	KeyCount              int64  `xml:"KeyCount"`
	NextContinuationToken string `xml:"NextContinuationToken"`
	EncodingType          string `xml:"EncodingType"`
	Contents              []struct {
		Key          string `xml:"Key"`
		LastModified string `xml:"LastModified"`
		ETag         string `xml:"ETag"`
		Size         int64  `xml:"Size"`
		StorageClass string `xml:"StorageClass"`
	} `xml:"Contents"`
	CommonPrefixes []struct {
		Prefix string `xml:"Prefix"`
	} `xml:"CommonPrefixes"`
}

func (c *client) listObjects(ctx context.Context, req connector.Request) (connector.Response, error) {
	e, err := c.endpoint(req)
	if err != nil {
		return connector.Response{}, err
	}
	in := req.Input
	bucket, err := e.bucketOf(in, "bucket")
	if err != nil {
		return connector.Response{}, err
	}
	maxKeys := intArg(in, "max_keys", 1000)
	if maxKeys < 1 || maxKeys > 1000 {
		return connector.Response{}, fatalf("max_keys must be 1 to 1000")
	}
	// encoding-type=url lets S3 return keys XML 1.0 cannot carry.
	q := url.Values{"list-type": {"2"}, "encoding-type": {"url"}, "max-keys": {strconv.FormatInt(maxKeys, 10)}}
	for field, param := range map[string]string{"prefix": "prefix", "delimiter": "delimiter", "start_after": "start-after", "continuation_token": "continuation-token"} {
		if v := str(in, field); v != "" {
			q.Set(param, v)
		}
	}
	resp, err := c.send(ctx, req, e, http.MethodGet, e.url(bucket, "", q), nil, nil)
	if err != nil {
		return connector.Response{}, err
	}
	defer closeBody(resp)
	if !ok200(resp) {
		return connector.Response{}, apiError(resp, false)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxListBodySize))
	if err != nil {
		return connector.Response{}, fmt.Errorf("s3: reading list: %w: %w", err, effects.ErrRetryable)
	}
	var lr listResult
	if err := xml.Unmarshal(raw, &lr); err != nil {
		// ListObjectsV2 documents that a 200 can carry invalid XML.
		return connector.Response{}, fmt.Errorf("s3: unreadable list response: %w: %w", err, effects.ErrRetryable)
	}
	decode := func(s string) string {
		if lr.EncodingType != "url" {
			return s
		}
		if d, err := url.QueryUnescape(s); err == nil {
			return d
		}
		return s
	}
	objs := make([]any, 0, len(lr.Contents))
	for _, o := range lr.Contents {
		objs = append(objs, map[string]any{
			"key": decode(o.Key), "size": o.Size, "etag": trimETag(o.ETag),
			"last_modified": o.LastModified, "storage_class": o.StorageClass,
		})
	}
	prefixes := make([]any, 0, len(lr.CommonPrefixes))
	for _, p := range lr.CommonPrefixes {
		prefixes = append(prefixes, decode(p.Prefix))
	}
	return connector.Response{Output: map[string]any{
		"objects": objs, "common_prefixes": prefixes, "is_truncated": lr.IsTruncated,
		"next_continuation_token": lr.NextContinuationToken, "key_count": lr.KeyCount,
	}}, nil
}

func (c *client) deleteObject(ctx context.Context, req connector.Request) (connector.Response, error) {
	e, err := c.endpoint(req)
	if err != nil {
		return connector.Response{}, err
	}
	in := req.Input
	bucket, err := e.bucketOf(in, "bucket")
	if err != nil {
		return connector.Response{}, err
	}
	key := str(in, "key")
	if err := checkKey(key); err != nil {
		return connector.Response{}, err
	}
	var q url.Values
	if v := str(in, "version_id"); v != "" {
		q = url.Values{"versionId": {v}}
	}
	logKey(req, "delete_object", bucket, key)
	resp, err := c.send(ctx, req, e, http.MethodDelete, e.url(bucket, key, q), nil, nil)
	if err != nil {
		return connector.Response{}, err
	}
	defer closeBody(resp)
	if !ok200(resp) {
		return connector.Response{}, apiError(resp, true)
	}
	return connector.Response{Output: map[string]any{
		"bucket": bucket, "key": key, "deleted": true,
		"version_id": resp.Header.Get("X-Amz-Version-Id"), "delete_marker": resp.Header.Get("X-Amz-Delete-Marker") == "true",
	}}, nil
}

type copyResult struct {
	XMLName      xml.Name
	ETag         string `xml:"ETag"`
	LastModified string `xml:"LastModified"`
	Code         string `xml:"Code"`
	Message      string `xml:"Message"`
	RequestID    string `xml:"RequestId"`
}

func (c *client) copyObject(ctx context.Context, req connector.Request) (connector.Response, error) {
	e, err := c.endpoint(req)
	if err != nil {
		return connector.Response{}, err
	}
	in := req.Input
	bucket, err := e.bucketOf(in, "bucket")
	if err != nil {
		return connector.Response{}, err
	}
	srcBucket, err := e.bucketOf(in, "source_bucket")
	if err != nil {
		return connector.Response{}, err
	}
	key, srcKey := str(in, "key"), str(in, "source_key")
	if err := checkKey(key); err != nil {
		return connector.Response{}, err
	}
	if err := checkKey(srcKey); err != nil {
		return connector.Response{}, err
	}
	h := http.Header{}
	src := "/" + srcBucket + "/" + uriEncode(srcKey, true)
	if v := str(in, "source_version_id"); v != "" {
		src += "?versionId=" + uriEncode(v, false)
	}
	h.Set("X-Amz-Copy-Source", src)
	ct := str(in, "content_type")
	if _, hasMeta := in["metadata"].(map[string]any); hasMeta || ct != "" {
		h.Set("X-Amz-Metadata-Directive", "REPLACE")
		if ct != "" {
			h.Set("Content-Type", ct)
		}
		if err := metadataHeaders(in, h); err != nil {
			return connector.Response{}, err
		}
	} else if srcBucket == bucket && srcKey == key {
		return connector.Response{}, fatalf("copying an object onto itself needs content_type or metadata to change")
	}
	logKey(req, "copy_object", bucket, key)
	resp, err := c.send(ctx, req, e, http.MethodPut, e.url(bucket, key, nil), h, nil)
	if err != nil {
		return connector.Response{}, err
	}
	defer closeBody(resp)
	if !ok200(resp) {
		return connector.Response{}, apiError(resp, true)
	}
	// CopyObject can fail after answering 200: the error is then the body.
	// Only a complete CopyObjectResult proves the copy happened.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
	if err != nil {
		return connector.Response{}, fmt.Errorf("s3: reading copy result: %w: %w", err, effects.ErrUnknownOutcome)
	}
	var cr copyResult
	if err := xml.Unmarshal(raw, &cr); err != nil {
		return connector.Response{}, fmt.Errorf("s3: unreadable copy result: %w: %w", err, effects.ErrUnknownOutcome)
	}
	if cr.XMLName.Local == "Error" {
		ae := &Error{Status: resp.StatusCode, Code: cr.Code, Message: cr.Message, RequestID: cr.RequestID}
		ae.kind = classify(resp.StatusCode, cr.Code, true)
		return connector.Response{}, ae
	}
	if cr.XMLName.Local != "CopyObjectResult" || cr.ETag == "" {
		return connector.Response{}, fmt.Errorf("s3: copy answered 200 without a CopyObjectResult: %w", effects.ErrUnknownOutcome)
	}
	return connector.Response{Output: map[string]any{
		"bucket": bucket, "key": key, "etag": trimETag(cr.ETag), "last_modified": cr.LastModified,
		"version_id": resp.Header.Get("X-Amz-Version-Id"),
	}}, nil
}

func (c *client) presignURL(_ context.Context, req connector.Request) (connector.Response, error) {
	e, err := c.endpoint(req)
	if err != nil {
		return connector.Response{}, err
	}
	in := req.Input
	bucket, err := e.bucketOf(in, "bucket")
	if err != nil {
		return connector.Response{}, err
	}
	key := str(in, "key")
	if err := checkKey(key); err != nil {
		return connector.Response{}, err
	}
	method := strings.ToUpper(str(in, "method"))
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodPut {
		return connector.Response{}, fatalf("method %q must be GET or PUT", method)
	}
	secs := intArg(in, "expires_in", 3600)
	if secs < 1 || secs > 604800 {
		return connector.Response{}, fatalf("expires_in must be 1 to 604800 seconds")
	}
	h := http.Header{}
	if ct := str(in, "content_type"); ct != "" {
		if method != http.MethodPut {
			return connector.Response{}, fatalf("content_type applies to PUT links only")
		}
		h.Set("Content-Type", ct)
	}
	now := c.now().UTC()
	u := e.signer.Presign(method, e.url(bucket, key, nil), h, time.Duration(secs)*time.Second, now)
	headers := map[string]any{}
	for k := range h {
		headers[k] = h.Get(k)
	}
	return connector.Response{Output: map[string]any{
		"url": u.String(), "method": method, "headers": headers,
		"expires_at": now.Add(time.Duration(secs) * time.Second).Format(time.RFC3339),
	}}, nil
}
