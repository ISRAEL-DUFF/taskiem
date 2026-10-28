// Package archive keeps ended runs' histories after retention purges them
// from the database (spec 4.11, 9.4): in an S3-compatible bucket, so every
// scheduler writes to the same place and the archive outlives any one
// machine. runtime.FileArchiver remains for single-node installs with a
// mounted volume.
package archive

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5" //nolint:gosec // Content-MD5 is S3's integrity check, not a security control
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/connectors/s3"
	"github.com/israel-duff/taskiem/engine/history"
)

// S3 writes each run's history to a bucket as gzipped JSON lines, one event
// per line, at <Prefix><tenant>/<run>.jsonl.gz: the format and layout
// runtime.FileArchiver writes under its directory.
//
// Personal data in the history is already sealed under each data subject's
// key (spec 4.9), so erasing a subject by crypto-shredding reaches the
// archive too. SSE asks the bucket to encrypt every object at rest as well.
type S3 struct {
	// Endpoint is the service's base URL: https://s3.af-south-1.amazonaws.com,
	// or a self-hosted service such as http://seaweedfs:8333.
	Endpoint *url.URL
	Bucket   string
	// Prefix starts every key ("runs/"); letters, digits and / _ . - only.
	Prefix string
	// PathStyle addresses the bucket in the path (endpoint/bucket/key), as
	// SeaweedFS and MinIO expect; otherwise as bucket.endpoint/key, as AWS
	// requires for buckets made since 2020.
	PathStyle bool
	Signer    s3.Signer
	// SSE is the x-amz-server-side-encryption header: "", "AES256" or
	// "aws:kms" (with KMSKeyID, or the bucket's default key).
	SSE      string
	KMSKeyID string
	// HTTP sends the requests; nil uses a client with a one-minute timeout.
	HTTP *http.Client
	// Now is the signing clock; nil is time.Now.
	Now func() time.Time
}

var prefixRe = regexp.MustCompile(`^[A-Za-z0-9/_.-]*$`)

// Validate reports a setting the archiver cannot work with.
func (a *S3) Validate() error {
	switch {
	case a.Endpoint == nil || (a.Endpoint.Scheme != "https" && a.Endpoint.Scheme != "http") || a.Endpoint.Host == "":
		return errors.New("archive: the S3 endpoint must be an http or https URL")
	case a.Bucket == "" || strings.ContainsAny(a.Bucket, "/ "):
		return fmt.Errorf("archive: bucket %q is not a bucket name", a.Bucket)
	case !prefixRe.MatchString(a.Prefix) || strings.HasPrefix(a.Prefix, "/"):
		return fmt.Errorf("archive: prefix %q may hold only letters, digits and / _ . -, and may not start with /", a.Prefix)
	case a.Signer.AccessKeyID == "" || a.Signer.SecretAccessKey == "":
		return errors.New("archive: the S3 access key id and secret are required")
	case a.Signer.Region == "":
		return errors.New("archive: the S3 region is required (us-east-1 for most self-hosted services)")
	case a.SSE != "" && a.SSE != "AES256" && a.SSE != "aws:kms":
		return fmt.Errorf("archive: server-side encryption %q is not AES256 or aws:kms", a.SSE)
	case a.KMSKeyID != "" && a.SSE != "aws:kms":
		return errors.New("archive: a KMS key id needs server-side encryption aws:kms")
	}
	return nil
}

// Key is where a run's history is stored in the bucket.
func (a *S3) Key(tenant, run uuid.UUID) string {
	return a.Prefix + tenant.String() + "/" + run.String() + ".jsonl.gz"
}

// objectURL addresses key in the bucket.
func (a *S3) objectURL(key string) *url.URL {
	u := *a.Endpoint
	base := strings.TrimRight(u.Path, "/")
	if a.PathStyle {
		u.Path = base + "/" + a.Bucket + "/" + key
	} else {
		u.Host = a.Bucket + "." + u.Host
		u.Path = base + "/" + key
	}
	u.RawPath, u.RawQuery, u.Fragment = "", "", ""
	return &u
}

// Archive stores a run's history. Writing it again (a purge that failed
// after its archive succeeded) stores the same bytes: histories are
// append-only and the run has ended.
func (a *S3) Archive(ctx context.Context, tenant, run uuid.UUID, events []history.Event) error {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	enc := json.NewEncoder(zw)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	body := buf.Bytes()
	sum := sha256.Sum256(body)
	md := md5.Sum(body) //nolint:gosec // see the import

	key := a.Key(tenant, run)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, a.objectURL(key).String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/gzip")
	req.Header.Set("Content-MD5", base64.StdEncoding.EncodeToString(md[:]))
	req.Header.Set("X-Amz-Content-Sha256", hex.EncodeToString(sum[:]))
	req.Header.Set("X-Amz-Meta-Taskiem-Events", fmt.Sprint(len(events)))
	if a.SSE != "" {
		req.Header.Set("X-Amz-Server-Side-Encryption", a.SSE)
		if a.KMSKeyID != "" {
			req.Header.Set("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id", a.KMSKeyID)
		}
	}
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	a.Signer.Sign(req, hex.EncodeToString(sum[:]), now())

	hc := a.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: time.Minute}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("archive: put %s: %w", key, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 == 2 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return fmt.Errorf("archive: put %s: %s", key, s3Error(resp))
}

// s3Error says what an S3 error response says: its status, and the code
// and message of the XML error body when there is one.
func s3Error(resp *http.Response) string {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	var e struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	if xml.Unmarshal(raw, &e) == nil && e.Code != "" {
		return fmt.Sprintf("%s: %s %s", resp.Status, e.Code, e.Message)
	}
	return resp.Status
}
