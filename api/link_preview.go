package api

// Link previews in every chat (docs/spec/chat-previews-and-editing.md §2;
// business links L3, §5.5). The first link in a message is fetched once,
// by the server, when the message is sent (or, for the composer, while it
// is typed: POST /v1/link-preview), and the page's host, site, title,
// description and picture go with the message to every copy.
//
// Only when the server has an egress guard. Only http and https on their
// usual ports; each address's own host allowed, private addresses
// refused; at most three redirects, each checked again; four seconds and
// 512 KB of HTML at most; the picture at most 2 MB, a JPEG, PNG or WebP
// by its own bytes, cleaned and resized as a chat photo is and kept as
// Taskiem's own file, so phones and browsers never load anything from
// the page's site. Ten pages a minute per person; what was fetched is
// remembered per business, or per person for their own chats, never
// shared between them. Taskiem's own links are not fetched.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/net/html"

	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/linkify"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/lru"
	"github.com/israel-duff/taskiem/engine/media"
)

const (
	previewTimeout = 4 * time.Second
	previewMax     = 512 << 10
	// previewRedirects is how many redirects a page or its picture may
	// take, each to an address the guard checks again.
	previewRedirects = 3
	// previewHold is how long a preview made for the composer, and its
	// picture, wait to be sent; never kept unless a message is sent with it.
	previewHold = 30 * time.Minute
	// previewUA is all a page learns of who asked: nothing of the sender.
	previewUA = "TaskiemBot/1.0 (+https://taskiem.com/bot)"
)

// previewURL is the first link in text worth a preview (the shared rule,
// engine/linkify), or "".
func previewURL(text string, anyPort bool) string {
	raw := linkify.First(text)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || !okPreviewURL(u, anyPort) {
		return ""
	}
	return u.String()
}

// okPreviewURL is an address a preview may fetch: http or https, a host,
// no user name, and the usual ports.
func okPreviewURL(u *url.URL, anyPort bool) bool {
	if u == nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	if p := u.Port(); p != "" && !anyPort && p != "80" && p != "443" {
		return false
	}
	return true
}

// previewsPerMinute is how many pages one person may have fetched for
// previews in a minute (a burst, then one every six seconds); beyond it
// a message goes without one.
const previewsPerMinute = 10

// madePreview is a link's preview as fetched, before any chat keeps it:
// its words, and its picture cleaned (nil without one).
type madePreview struct {
	links.Preview
	img *media.LinkImage
}

// view is the preview as a message carries it, its picture's size only.
func (m *madePreview) view() *links.Preview {
	if m == nil {
		return nil
	}
	p := m.Preview
	if m.img != nil {
		p.Image = &links.PreviewImage{W: m.img.Width, H: m.img.Height}
	}
	return &p
}

// previewCache remembers what was fetched, by whose it is and the
// address: at most 256 (they hold pictures), for an hour.
func (s *Server) previewCache() *lru.Cache[string, *madePreview] {
	s.previewsMu.Lock()
	defer s.previewsMu.Unlock()
	if s.previews == nil {
		s.previews = lru.New[string, *madePreview](256, time.Hour)
	}
	return s.previews
}

// makePreview makes the preview of raw for scope (a business, "t:…", or a
// person, "p:…"), counted against actor; label names whose fetch it is in
// the egress log. Nil: no link, no guard, this person past their previews
// for the minute, or a page that did not answer with a title. What was
// fetched is remembered per business or person, so one's fetch never
// answers another's.
func (s *Server) makePreview(ctx context.Context, scope, actor, label, raw string) *madePreview {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	if kind := s.taskiemLink(u); kind != "" {
		// Taskiem's own: never fetched as a page.
		return &madePreview{Preview: links.Preview{URL: raw, Host: strings.TrimPrefix(u.Hostname(), "www."), Site: "Taskiem", Title: taskiemLinkWords[kind], Taskiem: kind}}
	}
	if s.Egress == nil {
		return nil
	}
	key := scope + " " + raw
	if pv, ok := s.previewCache().Get(key); ok {
		return pv
	}
	if !s.limiter("link-preview:"+actor, time.Minute/previewsPerMinute, previewsPerMinute).Allow() {
		return nil
	}
	pv := s.fetchPreview(ctx, label, raw)
	if pv != nil {
		s.previewCache().Put(key, pv) // a page that did not answer is asked again next time
	}
	return pv
}

// taskiemLinkWords are the plain cards of Taskiem's own links.
var taskiemLinkWords = map[string]string{
	"order":   "Taskiem · Order link",
	"invoice": "Taskiem · Invoice link",
	"payment": "Taskiem · Payment link",
	"form":    "Taskiem · Form link",
	"chat":    "Taskiem · Chat link",
	"store":   "Taskiem · Store link",
	"link":    "Taskiem link",
}

// taskiemLink is what a link to Taskiem itself is (order, invoice,
// payment, form, chat, store or link), or "" for any other site.
func (s *Server) taskiemLink(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	ours := []string{"taskiem.com", "taskiem.ng"}
	if pu, err := url.Parse(s.PublicURL); err == nil && pu.Hostname() != "" {
		ours = append(ours, pu.Hostname())
	}
	ours = append(ours, s.PublicHosts...)
	if s.Forms != nil && s.Forms.FormHost != "" {
		ours = append(ours, s.Forms.FormHost)
	}
	mine := false
	for _, h := range ours {
		h = strings.ToLower(h)
		if h != "" && (host == h || strings.HasSuffix(host, "."+h)) {
			mine = true
			break
		}
	}
	if !mine {
		return ""
	}
	path := "/" + strings.ToLower(strings.Trim(u.Path, "/")) + "/"
	for _, k := range []struct{ kind, words string }{
		{"order", "/o/ /order/ /orders/"},
		{"invoice", "/i/ /inv/ /invoice/ /invoices/"},
		{"payment", "/pay/ /p/ /payment/ /payments/"},
		{"form", "/f/ /form/ /forms/"},
		{"chat", "/c/ /chat/ /g/ /join/"},
		{"store", "/s/ /shop/ /store/"},
	} {
		for _, w := range strings.Fields(k.words) {
			if strings.HasPrefix(path, w) {
				return k.kind
			}
		}
	}
	if path == "//" {
		return "link"
	}
	if s.Forms != nil && host == strings.ToLower(s.Forms.FormHost) {
		return "form"
	}
	if len(s.PublicHosts) > 0 && strings.HasSuffix(host, strings.ToLower(s.PublicHosts[0])) {
		return "store"
	}
	return "link"
}

// guardedGet fetches raw through the egress guard, following at most
// previewRedirects redirects itself, each to an http or https address
// the guard checks again (its own host allowed, private addresses
// refused). It answers the last response and the address it came from.
func (s *Server) guardedGet(ctx context.Context, label, raw, accept string) (*http.Response, *url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, nil, err
	}
	for hop := 0; ; hop++ {
		if !okPreviewURL(u, s.previewAnyPort) {
			return nil, nil, fmt.Errorf("%w: %s is not an address a preview may fetch", egress.ErrDenied, u.Redacted())
		}
		c := s.Egress.Client(egress.Policy{Tenant: label, Hosts: []string{u.Hostname()}, Purpose: "link_preview"}, previewTimeout)
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil) //nolint:gosec // a link someone sent, fetched only through the egress guard (its own host, no private addresses)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("User-Agent", previewUA)
		req.Header.Set("Accept", accept)
		resp, err := c.Do(req) //nolint:gosec // the link's own host only, through the egress guard (no private addresses)
		if err != nil {
			return nil, nil, err
		}
		switch resp.StatusCode {
		case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
			loc := resp.Header.Get("Location")
			_ = resp.Body.Close()
			if hop >= previewRedirects || loc == "" {
				return nil, nil, fmt.Errorf("%w: too many redirects", egress.ErrDenied)
			}
			next, err := u.Parse(loc)
			if err != nil {
				return nil, nil, err
			}
			u = next
			continue
		}
		return resp, u, nil
	}
}

// fetchPreview fetches a page and its picture; nil when the page did not
// answer with a title. A picture that cannot be had leaves the card
// without one.
func (s *Server) fetchPreview(ctx context.Context, label, raw string) *madePreview {
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	resp, at, err := s.guardedGet(ctx, label, raw, "text/html")
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	if ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); resp.StatusCode != http.StatusOK || (ct != "text/html" && ct != "application/xhtml+xml") {
		return nil
	}
	m := pageMeta(io.LimitReader(resp.Body, previewMax))
	if m.title == "" {
		return nil
	}
	u, _ := url.Parse(raw)
	out := &madePreview{Preview: links.Preview{URL: raw, Host: strings.TrimPrefix(u.Hostname(), "www."), Title: clipText(m.title, 120),
		Description: clipText(m.desc, 200), Site: clipText(m.site, 60)}}
	if m.image != "" {
		if iu, err := at.Parse(m.image); err == nil {
			ictx, icancel := context.WithTimeout(context.WithoutCancel(ctx), previewTimeout)
			out.img, _ = s.fetchPreviewImage(ictx, label, iu.String())
			icancel()
		}
	}
	return out
}

// errPreviewImage refuses a page's picture.
var errPreviewImage = errors.New("link preview: not a usable picture")

// fetchPreviewImage fetches and cleans a page's picture: through the
// guard, at most 2 MB, a JPEG, PNG or WebP by its own bytes.
func (s *Server) fetchPreviewImage(ctx context.Context, label, raw string) (*media.LinkImage, error) {
	resp, _, err := s.guardedGet(ctx, label, raw, "image/webp,image/png,image/jpeg")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: it answered %d", errPreviewImage, resp.StatusCode)
	}
	if n, err := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64); err == nil && n > media.MaxLinkImageBytes {
		return nil, fmt.Errorf("%w: larger than 2 MB", errPreviewImage)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, media.MaxLinkImageBytes+1))
	if err != nil {
		return nil, err
	}
	img, err := media.CleanLinkImage(data)
	if err != nil {
		return nil, err
	}
	return &img, nil
}

// page is what a page's head says of it.
type page struct {
	title, desc, site, image string
}

// pageMeta is a page's title, description, site name and picture: Open
// Graph's when it has them, then Twitter's, else its <title> and
// description meta. Its icon is never used.
func pageMeta(r io.Reader) page {
	z := html.NewTokenizer(r)
	var og, tw, doc page
	inTitle := false
	done := func() page {
		return page{
			title: firstOf(firstOf(og.title, tw.title), doc.title),
			desc:  firstOf(firstOf(og.desc, tw.desc), doc.desc),
			site:  strings.TrimSpace(og.site),
			image: firstOf(og.image, tw.image),
		}
	}
	for {
		switch z.Next() {
		case html.ErrorToken:
			return done()
		case html.StartTagToken, html.SelfClosingTagToken:
			name, has := z.TagName()
			switch string(name) {
			case "title":
				inTitle = doc.title == ""
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
				set := func(p *string) {
					if *p == "" {
						*p = content
					}
				}
				switch prop {
				case "og:title":
					set(&og.title)
				case "og:description":
					set(&og.desc)
				case "og:site_name":
					set(&og.site)
				case "og:image", "og:image:url", "og:image:secure_url":
					set(&og.image)
				case "twitter:title":
					set(&tw.title)
				case "twitter:description":
					set(&tw.desc)
				case "twitter:image", "twitter:image:src":
					set(&tw.image)
				case "description":
					set(&doc.desc)
				}
			case "body":
				if og.title != "" || tw.title != "" || doc.title != "" {
					return done()
				}
			}
		case html.TextToken:
			if inTitle {
				doc.title += string(z.Text())
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

// --- what a message is sent with ---

// previewChoice is what a send says of its preview: the card the
// composer showed ({id}), none (false: closed with ✕), or nothing said
// (made as the message is sent).
type previewChoice struct {
	off bool
	id  string
}

// UnmarshalJSON reads false or {"id": "…"}.
func (c *previewChoice) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch s {
	case "null", "true":
		*c = previewChoice{}
		return nil
	case "false":
		*c = previewChoice{off: true}
		return nil
	}
	var v struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return fmt.Errorf("preview is false or {id}")
	}
	*c = previewChoice{id: v.ID}
	return nil
}

type previewChoiceKey struct{}

// withPreviewChoice carries what a send said of its preview to where the
// message is written.
func withPreviewChoice(ctx context.Context, c previewChoice) context.Context {
	return context.WithValue(ctx, previewChoiceKey{}, c)
}

func previewChoiceFrom(ctx context.Context) previewChoice {
	c, _ := ctx.Value(previewChoiceKey{}).(previewChoice)
	return c
}

// previewHeld is a preview made for the composer, waiting to be sent.
type previewHeld struct {
	owner, chat string
	made        *madePreview
	until       time.Time
}

// previewHolds keeps the composer's previews in memory only: a picture
// never sent is never stored, and is gone after previewHold.
type previewHolds struct {
	mu sync.Mutex
	m  map[string]previewHeld
}

func (h *previewHolds) put(owner, chat string, made *madePreview, now time.Time) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.m == nil {
		h.m = map[string]previewHeld{}
	}
	for k, v := range h.m {
		if now.After(v.until) {
			delete(h.m, k)
		}
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	h.m[id] = previewHeld{owner: owner, chat: chat, made: made, until: now.Add(previewHold)}
	return id
}

func (h *previewHolds) get(id, owner string, now time.Time) (previewHeld, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.m[id]
	if !ok || v.owner != owner {
		return previewHeld{}, false
	}
	if now.After(v.until) {
		delete(h.m, id)
		return previewHeld{}, false
	}
	return v, true
}

// Len is how many previews are held (tests).
func (h *previewHolds) Len(now time.Time) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, v := range h.m {
		if !now.After(v.until) {
			n++
		}
	}
	return n
}

// previewForSend is the preview a message from p in chat (its key, as the
// composer named it) is sent with: the one the composer showed, none, or
// made now from its words. scope and label are as makePreview's.
func (s *Server) previewForSend(ctx context.Context, p *Principal, chat, scope, label, text string, c previewChoice) *madePreview {
	if c.off {
		return nil
	}
	raw := previewURL(text, s.previewAnyPort)
	if raw == "" {
		return nil
	}
	if c.id != "" {
		if h, ok := s.holds.get(c.id, p.Actor(), s.now()); ok && h.chat == chat && h.made != nil && h.made.URL == raw {
			return h.made
		}
	}
	return s.makePreview(ctx, scope, p.Actor(), label, raw)
}

// now is the server's clock (tests set it through previewClock).
func (s *Server) now() time.Time {
	if s.previewClock != nil {
		return s.previewClock()
	}
	return time.Now()
}

// keepBusinessPreview stores a made preview's picture in a business's
// own storage: the preview a message keeps, with where its picture is.
// Without storage, or when the picture cannot be kept, the card goes
// without it.
func (s *Server) keepBusinessPreview(ctx context.Context, tenant uuid.UUID, m *madePreview) *links.Preview {
	if m == nil {
		return nil
	}
	p := m.Preview
	if m.img != nil && s.Media != nil {
		if img, err := s.keepBusinessImage(ctx, tenant, m.img); err == nil {
			p.Image = img
		} else {
			s.Logger.Warn("link preview: the picture not kept", "tenant", tenant, "err", err)
		}
	}
	return &p
}

func (s *Server) keepBusinessImage(ctx context.Context, tenant uuid.UUID, img *media.LinkImage) (*links.PreviewImage, error) {
	big, err := s.Media.PutLinkImage(ctx, tenant, img.Big, img.Type)
	if err != nil {
		return nil, err
	}
	small := big
	if !bytes.Equal(img.Small, img.Big) {
		if small, err = s.Media.PutLinkImage(ctx, tenant, img.Small, img.Type); err != nil {
			s.dropChatFile(ctx, tenant, big)
			return nil, err
		}
	}
	return &links.PreviewImage{W: img.Width, H: img.Height, Path: big, Small: small}, nil
}

// businessPreviewImage reads a business's copy of a preview's picture
// back (to forward it), or nil.
func (s *Server) businessPreviewImage(ctx context.Context, tenant uuid.UUID, pi *links.PreviewImage) *media.LinkImage {
	big, ctype, err := s.Media.GetFile(ctx, tenant, pi.Path)
	if err != nil {
		return nil
	}
	img := &media.LinkImage{Big: big, Small: big, Type: ctype, Width: pi.W, Height: pi.H}
	if pi.Small != "" && pi.Small != pi.Path {
		if small, _, err := s.Media.GetFile(ctx, tenant, pi.Small); err == nil {
			img.Small = small
		}
	}
	return img
}

// dropBusinessImage removes a business's copy of a preview's picture
// whose message was not sent.
func (s *Server) dropBusinessImage(ctx context.Context, tenant uuid.UUID, p *links.Preview) {
	if p == nil || p.Image == nil || s.Media == nil {
		return
	}
	paths := []string{p.Image.Path}
	if p.Image.Small != "" && p.Image.Small != p.Image.Path {
		paths = append(paths, p.Image.Small)
	}
	if err := s.Media.Delete(context.WithoutCancel(ctx), tenant, media.PrivateBucket, paths); err != nil {
		s.Logger.Warn("link preview: a picture not removed", "tenant", tenant, "err", err)
	}
}

// writePreviewImage answers a preview's picture: Taskiem's own copy,
// never the page's site's.
func writePreviewImage(w http.ResponseWriter, data []byte) {
	ctype := http.DetectContentType(data)
	if ctype != "image/jpeg" && ctype != "image/png" {
		ctype = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}
