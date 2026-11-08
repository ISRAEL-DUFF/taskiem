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

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/media"
)

// photoLinkTimeout is how long fetching a picture from a link may take.
const photoLinkTimeout = 15 * time.Second

// errNoLinkFetch: this deployment fetches nothing from the web.
var errNoLinkFetch = errors.New("pictures from a link are not available here")

// imageFromLink fetches the picture at a link a member pasted, once: http
// or https only, through the egress guard (the link's own host, never a
// private address, and so no redirect to one), within a time and the
// upload size limit. Whether it is a picture is decided by its bytes
// (media.PutImage), not by its address or its Content-Type.
func (s *Server) imageFromLink(ctx context.Context, tenant uuid.UUID, raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return nil, fmt.Errorf("%w: paste a link that starts with https://", errBadRequest)
	}
	if s.Egress == nil {
		return nil, fmt.Errorf("%w: %w", errConflict, errNoLinkFetch)
	}
	ctx, cancel := context.WithTimeout(ctx, photoLinkTimeout)
	defer cancel()
	c := s.Egress.Client(egress.Policy{Tenant: tenant.String(), Hosts: []string{u.Hostname()}, Purpose: "photo_link"}, photoLinkTimeout)
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
	defer func() { _ = resp.Body.Close() }()
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
