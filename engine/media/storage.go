package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/effects"
)

// The Xenibase management file API (docs/integrations/xenibase-storage-api.md,
// section 2): it acts as the platform with the project's API token, so
// storage policies do not apply, and its answers and errors are
// documented. Taskiem uses it for everything but public downloads, which
// go to the project's own host.

// Error is a refusal from Xenibase's file API.
type Error struct {
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	s := fmt.Sprintf("xenibase storage: http %d", e.Status)
	if e.Code != "" {
		s += " " + e.Code
	}
	if e.Message != "" {
		s += ": " + e.Message
	}
	return s
}

// Temporary reports whether the same request may succeed later: a paused
// project resuming, a rate limit, a server error.
func (e *Error) Temporary() bool {
	switch e.Code {
	case "project_resuming", "project_restoring", "rate_limited":
		return true
	}
	return e.Status == http.StatusTooManyRequests || e.Status >= 500
}

// IsQuota reports whether the organisation's storage or the month's
// allowance is used up.
func (e *Error) IsQuota() bool {
	return e.Code == "quota_exceeded" || e.Code == "storage_egress_limit" || e.Code == "plan_limit_reached"
}

type storage struct {
	base, project, token string
	hc                   *http.Client
}

type bucketInfo struct {
	ID               string   `json:"id"`
	Public           bool     `json:"public"`
	FileSizeLimit    *int64   `json:"file_size_limit"`
	AllowedMIMETypes []string `json:"allowed_mime_types"`
	CacheSeconds     *int64   `json:"cache_seconds"`
}

type storedObject struct {
	ID       string `json:"id"`
	Bucket   string `json:"bucket"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	MIMEType string `json:"mime_type"`
	ETag     string `json:"etag"`
}

func (c *storage) files() string {
	return c.base + "/api/v1/projects/" + url.PathEscape(c.project) + "/files"
}

func (c *storage) buckets(ctx context.Context) ([]bucketInfo, error) {
	var out struct {
		Buckets []bucketInfo `json:"buckets"`
	}
	err := c.call(ctx, http.MethodGet, c.files(), "", nil, &out)
	return out.Buckets, err
}

func (c *storage) createBucket(ctx context.Context, b bucketInfo) error {
	return c.call(ctx, http.MethodPost, c.files()+"/buckets", "application/json", mustJSON(b), nil)
}

func (c *storage) updateBucket(ctx context.Context, b bucketInfo) error {
	body := mustJSON(map[string]any{"public": b.Public, "file_size_limit": b.FileSizeLimit, "allowed_mime_types": b.AllowedMIMETypes, "cache_seconds": b.CacheSeconds})
	return c.call(ctx, http.MethodPatch, c.files()+"/buckets/"+url.PathEscape(b.ID), "application/json", body, nil)
}

func (c *storage) put(ctx context.Context, bucket, path, ctype string, data []byte) (storedObject, error) {
	var o storedObject
	u := c.files() + "/buckets/" + url.PathEscape(bucket) + "/object?path=" + url.QueryEscape(path)
	err := c.call(ctx, http.MethodPut, u, ctype, data, &o)
	return o, err
}

func (c *storage) sign(ctx context.Context, bucket, path string, ttl time.Duration) (string, time.Time, error) {
	var out struct {
		SignedURL string    `json:"signed_url"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	body := mustJSON(map[string]any{"path": path, "expires_in": int(ttl.Seconds())})
	if err := c.call(ctx, http.MethodPost, c.files()+"/buckets/"+url.PathEscape(bucket)+"/sign", "application/json", body, &out); err != nil {
		return "", time.Time{}, err
	}
	if out.SignedURL == "" {
		return "", time.Time{}, fmt.Errorf("xenibase storage: no signed_url in the answer: %w", effects.ErrUnknownOutcome)
	}
	return out.SignedURL, out.ExpiresAt, nil
}

func (c *storage) remove(ctx context.Context, bucket string, paths []string) (int, error) {
	var out struct {
		Deleted int `json:"deleted"`
	}
	err := c.call(ctx, http.MethodDelete, c.files()+"/buckets/"+url.PathEscape(bucket)+"/objects", "application/json",
		mustJSON(map[string]any{"paths": paths}), &out)
	return out.Deleted, err
}

func (c *storage) call(ctx context.Context, method, u, ctype string, body []byte, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		if errors.Is(err, effects.ErrNotSent) {
			return err
		}
		return fmt.Errorf("xenibase storage: %w: %w", err, effects.ErrUnknownOutcome)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("xenibase storage: %w: %w", err, effects.ErrUnknownOutcome)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		e := &Error{Status: resp.StatusCode}
		var flat struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &flat) == nil {
			e.Code, e.Message = flat.Code, clip(flat.Message)
		}
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
			e.RetryAfter = time.Duration(s) * time.Second
		}
		return e
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("xenibase storage: unexpected answer: %w", err)
	}
	return nil
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func clip(s string) string {
	if len(s) > 300 {
		s = s[:300]
	}
	return strings.ToValidUTF8(s, "")
}
