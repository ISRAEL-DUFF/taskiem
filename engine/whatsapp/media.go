package whatsapp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/egress"
)

// Media download, from Meta's public Cloud API documentation (Media:
// "Retrieve media URL" and "Download media"): GET /{media-id} with the
// number's token answers {url, mime_type, sha256, file_size, id}; a GET of
// that url with the same token returns the bytes. The URL is short-lived.
// Only voice notes and audio are fetched (spec 11.6), capped in size.

// MediaHosts are where Meta serves media from. A URL anywhere else is not
// fetched (the Graph API's own host is allowed too, for test fakes).
var MediaHosts = []string{"lookaside.fbsbx.com"}

// ErrMediaTooLarge: the file is bigger than the caller takes.
var ErrMediaTooLarge = errors.New("whatsapp: the media file is too large")

// MediaInfo is what the Graph API says about a media id.
type MediaInfo struct {
	URL      string
	MimeType string
	SHA256   string
	Size     int64 // 0 when not reported
}

// MediaInfo looks a media id up.
func (c *Client) MediaInfo(ctx context.Context, id string) (MediaInfo, error) {
	var mi MediaInfo
	if c.Token == "" || id == "" || strings.ContainsAny(id, "/?#") {
		return mi, errors.New("whatsapp: no media id or token")
	}
	hc, err := c.client()
	if err != nil {
		return mi, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"/"+url.PathEscape(id), nil)
	if err != nil {
		return mi, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return mi, fmt.Errorf("whatsapp: media lookup: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error *GraphError `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != nil {
			e.Error.Status = resp.StatusCode
			return mi, e.Error
		}
		return mi, fmt.Errorf("whatsapp: media lookup answered %s", resp.Status)
	}
	var out struct {
		URL      string `json:"url"`
		MimeType string `json:"mime_type"`
		SHA256   string `json:"sha256"`
		FileSize any    `json:"file_size"` // a number or a string
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.URL == "" {
		return mi, errors.New("whatsapp: media lookup without a URL")
	}
	mi = MediaInfo{URL: out.URL, MimeType: out.MimeType, SHA256: out.SHA256}
	switch v := out.FileSize.(type) {
	case float64:
		mi.Size = int64(v)
	case string:
		mi.Size, _ = strconv.ParseInt(v, 10, 64)
	}
	return mi, nil
}

// Download fetches a media file, refusing one larger than max bytes and
// one whose digest does not match what Meta reported.
func (c *Client) Download(ctx context.Context, mi MediaInfo, max int64) ([]byte, error) {
	if mi.Size > max {
		return nil, ErrMediaTooLarge
	}
	u, err := url.Parse(mi.URL)
	if err != nil || u.Hostname() == "" {
		return nil, errors.New("whatsapp: bad media URL")
	}
	graph, _ := url.Parse(c.base())
	allowed := u.Scheme == "https"
	hostOK := false
	for _, h := range MediaHosts {
		hostOK = hostOK || strings.EqualFold(u.Hostname(), h)
	}
	if graph != nil && strings.EqualFold(u.Hostname(), graph.Hostname()) && u.Scheme == graph.Scheme {
		hostOK, allowed = true, true
	}
	if !allowed || !hostOK {
		return nil, fmt.Errorf("whatsapp: media URL on an unexpected host %q", u.Hostname())
	}
	hc := c.HTTP
	if hc == nil {
		g := c.Egress
		if g == nil {
			g = &egress.Guard{}
		}
		hc = g.Client(egress.Policy{Tenant: "platform", Hosts: []string{u.Hostname()}, Purpose: "whatsapp_media"}, 20*time.Second)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("whatsapp: media download: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("whatsapp: media download answered %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("whatsapp: media download: %w", err)
	}
	if int64(len(data)) > max {
		return nil, ErrMediaTooLarge
	}
	if mi.SHA256 != "" {
		sum := sha256.Sum256(data)
		want := strings.TrimSpace(mi.SHA256)
		if !strings.EqualFold(want, hex.EncodeToString(sum[:])) && want != base64.StdEncoding.EncodeToString(sum[:]) {
			return nil, errors.New("whatsapp: the media file does not match its digest")
		}
	}
	return data, nil
}
