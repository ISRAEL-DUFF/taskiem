package api

// Link previews in the conversation between linked businesses (business
// links L3, §5.5): the first link in a message is fetched once, by the
// sender's side, when it is sent, and its page's host, title and
// description go with the message to both sides. Only when the server
// has an egress guard; only http and https on their usual ports; the one
// host allowed, private addresses refused, redirects kept to it; four
// seconds and 512 KB at most; HTML only; nothing of the page but its
// title and description is kept (no image: the console shows images
// from its own origin only).

import (
	"context"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/net/html"

	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/links"
)

const (
	previewTimeout = 4 * time.Second
	previewMax     = 512 << 10
)

var previewLinkRe = regexp.MustCompile(`https?://[^\s<>"]+`)

// previewURL is the first link in text worth a preview, or "".
func previewURL(text string, anyPort bool) string {
	raw := previewLinkRe.FindString(text)
	raw = strings.TrimRight(raw, ".,;:!?)'")
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	if p := u.Port(); p != "" && !anyPort && p != "80" && p != "443" {
		return ""
	}
	return u.String()
}

// linkPreview is the preview of the first link in text, or nil (no link,
// no guard, or a page that did not answer with a title).
func (s *Server) linkPreview(ctx context.Context, tenant uuid.UUID, text string) *links.Preview {
	raw := previewURL(text, s.previewAnyPort)
	if raw == "" || s.Egress == nil {
		return nil
	}
	if p, ok := s.previews.Get(raw); ok {
		return p
	}
	p := s.fetchPreview(ctx, tenant, raw)
	if p != nil {
		s.previews.Put(raw, p) // a page that did not answer is asked again next time
	}
	return p
}

func (s *Server) fetchPreview(ctx context.Context, tenant uuid.UUID, raw string) *links.Preview {
	u, _ := url.Parse(raw)
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	c := s.Egress.Client(egress.Policy{Tenant: tenant.String(), Hosts: []string{u.Hostname()}, Purpose: "link_preview"}, previewTimeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil) //nolint:gosec // a link a member sent, fetched only through the egress guard (its own host, no private addresses)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "TaskiemLinkPreview/1 (+https://taskiem.ng)")
	req.Header.Set("Accept", "text/html")
	resp, err := c.Do(req) //nolint:gosec // the link's own host only, through the egress guard (no private addresses)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	if ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); resp.StatusCode != http.StatusOK || (ct != "text/html" && ct != "application/xhtml+xml") {
		return nil
	}
	title, desc := pageMeta(io.LimitReader(resp.Body, previewMax))
	if title == "" {
		return nil
	}
	return &links.Preview{URL: raw, Host: strings.TrimPrefix(u.Hostname(), "www."), Title: clipText(title, 120), Description: clipText(desc, 200)}
}

// pageMeta is a page's title and description: Open Graph's when it has
// them, else its <title> and description meta.
func pageMeta(r io.Reader) (title, desc string) {
	z := html.NewTokenizer(r)
	var ogTitle, ogDesc, docTitle, metaDesc string
	inTitle := false
	for {
		switch z.Next() {
		case html.ErrorToken:
			return firstOf(ogTitle, docTitle), firstOf(ogDesc, metaDesc)
		case html.StartTagToken, html.SelfClosingTagToken:
			name, has := z.TagName()
			switch string(name) {
			case "title":
				inTitle = docTitle == ""
			case "meta":
				var prop, content string
				for has {
					var k, v []byte
					k, v, has = z.TagAttr()
					switch strings.ToLower(string(k)) {
					case "property", "name":
						prop = strings.ToLower(string(v))
					case "content":
						content = string(v)
					}
				}
				switch prop {
				case "og:title":
					ogTitle = content
				case "og:description":
					ogDesc = content
				case "description":
					metaDesc = content
				}
			case "body":
				if ogTitle != "" || docTitle != "" {
					return firstOf(ogTitle, docTitle), firstOf(ogDesc, metaDesc)
				}
			}
		case html.TextToken:
			if inTitle {
				docTitle += string(z.Text())
			}
		case html.EndTagToken:
			if name, _ := z.TagName(); string(name) == "title" {
				inTitle = false
			}
		}
	}
}

func firstOf(a, b string) string {
	if a = strings.TrimSpace(a); a != "" {
		return a
	}
	return strings.TrimSpace(b)
}

// clipText is s on one line, cut to n characters.
func clipText(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > n {
		return string([]rune(s)[:n-1]) + "…"
	}
	return s
}
