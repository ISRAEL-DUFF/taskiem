package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/media"
)

// photoLinkTimeout is how long fetching a picture from a link may take.
const photoLinkTimeout = 15 * time.Second

// errNoLinkFetch: this deployment fetches nothing from the web.
var errNoLinkFetch = errors.New("pictures from a link are not available here")

// photoLinkHops is how many redirects a link may take to its picture: a
// short link or a shared file usually takes one or two.
const photoLinkHops = 3

// photoLinksPerMinute is how many pictures one person may have fetched
// from links in a minute.
const photoLinksPerMinute = 20

// errPhotoLinksTooFast: too many links fetched in a minute by one person.
var errPhotoLinksTooFast = errors.New("too many photos from links at once: wait a minute")

// imageFromLink fetches the picture at a link a member pasted, once: http
// or https only, through the egress guard, never a private address. A
// redirect is followed (up to photoLinkHops), each hop to its own host
// and checked again, so a short link or a shared file works while a
// redirect to a private address is still refused. Within a time and the
// upload size limit; whether it is a picture is decided by its bytes
// (media.PutImage), not by its address or its Content-Type.
func (s *Server) imageFromLink(ctx context.Context, p *Principal, raw string) ([]byte, error) {
	u, err := linkURL(raw)
	if err != nil {
		return nil, err
	}
	if s.Egress == nil {
		return nil, fmt.Errorf("%w: %w", errConflict, errNoLinkFetch)
	}
	if !s.limiter("photo-link:"+p.Actor(), time.Minute/photoLinksPerMinute, photoLinksPerMinute).Allow() {
		return nil, errPhotoLinksTooFast
	}
	ctx, cancel := context.WithTimeout(ctx, photoLinkTimeout)
	defer cancel()
	for hop := 0; ; hop++ {
		c := s.Egress.Client(egress.Policy{Tenant: p.TenantID.String(), Hosts: []string{u.Hostname()}, Purpose: "photo_link"}, photoLinkTimeout)
		// Redirects are followed here, one host at a time, not by the client.
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil) //nolint:gosec // a link a member pasted, fetched only through the egress guard (its own host, no private addresses)
		if err != nil {
			return nil, fmt.Errorf("%w: we could not open that link", errBadRequest)
		}
		req.Header.Set("User-Agent", "TaskiemPhoto/1 (+https://taskiem.ng)")
		req.Header.Set("Accept", "image/*")
		resp, err := c.Do(req) //nolint:gosec // the link's own host only, through the egress guard (no private addresses)
		if err != nil {
			return nil, fmt.Errorf("%w: we could not open that link", errBadRequest)
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 && resp.Header.Get("Location") != "" {
			loc := resp.Header.Get("Location")
			_ = resp.Body.Close()
			if hop >= photoLinkHops {
				return nil, fmt.Errorf("%w: that link goes round too many places; open the picture and copy its own address", errBadRequest)
			}
			next, err := u.Parse(loc)
			if err != nil {
				return nil, fmt.Errorf("%w: we could not open that link", errBadRequest)
			}
			if u, err = linkURL(next.String()); err != nil {
				return nil, fmt.Errorf("%w: we could not open that link", errBadRequest)
			}
			continue
		}
		defer func() { _ = resp.Body.Close() }()
		return pictureBody(resp)
	}
}

// linkURL checks a pasted link: http or https, a host, no user name.
func linkURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return nil, fmt.Errorf("%w: paste a link that starts with https://", errBadRequest)
	}
	return u, nil
}

// pictureBody reads a fetched picture, within the upload limit.
func pictureBody(resp *http.Response) ([]byte, error) {
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: we could not open that link (it answered %d)", errBadRequest, resp.StatusCode)
	}
	if ct := strings.ToLower(resp.Header.Get("Content-Type")); strings.HasPrefix(ct, "text/") {
		return nil, fmt.Errorf("%w: that link is a page, not a picture: open the picture itself and copy its address", errBadRequest)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, media.MaxImageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: we could not open that link", errBadRequest)
	}
	if len(data) > media.MaxImageBytes {
		return nil, fmt.Errorf("%w: that picture is over %d MB", errBadRequest, media.MaxImageBytes>>20)
	}
	return data, nil
}
