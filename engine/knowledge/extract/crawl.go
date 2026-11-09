package extract

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// UserAgent is what Crawl sends, naming the bot and where to read about it.
const UserAgent = "TaskiemBot/1.0 (reads a business's own website for its assistant)"

const (
	// maxPageBytes is the most of one page's body read; the rest is cut.
	maxPageBytes = 2 << 20
	// maxRobotsBytes is the most of a robots.txt read.
	maxRobotsBytes = 512 << 10
	// maxQuery is the longest query string followed: longer ones are
	// search results, tracking or calendars that never end.
	maxQuery = 200
)

// nonContent are path segments of pages that are not about the business:
// sign-in, shopping-cart and admin pages.
var nonContent = map[string]bool{
	"login": true, "logout": true, "signin": true, "sign-in": true, "signup": true,
	"sign-up": true, "register": true, "cart": true, "basket": true, "checkout": true,
	"account": true, "my-account": true, "myaccount": true, "wp-admin": true,
	"wp-login.php": true, "wp-json": true, "admin": true, "feed": true,
}

// nonHTML are file extensions of links that are not web pages. Documents a
// business wants read are uploaded as files instead.
var nonHTML = map[string]bool{
	".pdf": true, ".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
	".svg": true, ".ico": true, ".bmp": true, ".tif": true, ".tiff": true, ".avif": true,
	".zip": true, ".gz": true, ".tgz": true, ".tar": true, ".rar": true, ".7z": true,
	".mp3": true, ".mp4": true, ".m4a": true, ".wav": true, ".ogg": true, ".mov": true,
	".avi": true, ".webm": true, ".mkv": true, ".css": true, ".js": true, ".json": true,
	".xml": true, ".rss": true, ".txt": true, ".csv": true, ".doc": true, ".docx": true,
	".xls": true, ".xlsx": true, ".ppt": true, ".pptx": true, ".odt": true, ".exe": true,
	".dmg": true, ".apk": true, ".msi": true, ".iso": true, ".woff": true, ".woff2": true,
	".ttf": true, ".otf": true, ".eot": true,
}

// Crawl reads a website breadth first from start, following only links on
// start's own origin (scheme, host and port), and returns the pages read
// in that order, at most maxPages of them. Each body is read up to 2 MB;
// only HTML pages are read and other content types are passed over. It
// honours robots.txt (the "TaskiemBot" group, else "*"), skips links with
// long query strings and sign-in, cart and admin pages, and stops when ctx
// ends. A page that fails is skipped and its error recorded in the second
// return value; the crawl goes on. If the start page itself cannot be
// read, that is the only error and no pages are returned.
//
// Crawl never leaves start's origin, not even for the start page: a start
// page that redirects to its http/https or www/bare twin is an error. Use
// CrawlWith to allow such a redirect.
//
// hc does the requests, and should be the caller's egress-guarded client:
// Crawl never dials on its own. Redirects are followed within the site
// only, and only to pages the rules above allow, on top of hc's own
// redirect policy.
func Crawl(ctx context.Context, hc *http.Client, start string, maxPages int) ([]Page, []error) {
	return CrawlWith(ctx, hc, start, maxPages, CrawlOptions{})
}

// CrawlOptions widens what Crawl does.
type CrawlOptions struct {
	// AllowOrigins are origins, such as "https://www.example.com", that
	// the start page may redirect to, as many sites send example.com to
	// https://www.example.com. Only start's http/https and www/bare twins
	// on the default port count; any other origin listed is ignored. A
	// redirect from https to http is never followed, whatever is listed.
	// When the start page does redirect to an allowed twin, the crawl
	// reads that origin (and obeys its robots.txt) and nothing else.
	AllowOrigins []string
}

// CrawlWith is Crawl with options: see CrawlOptions.
func CrawlWith(ctx context.Context, hc *http.Client, start string, maxPages int, opts CrawlOptions) ([]Page, []error) {
	su, err := url.Parse(strings.TrimSpace(start))
	if err != nil || originOf(su) == "" {
		return nil, []error{fmt.Errorf("start %q: not an http or https URL", start)}
	}
	if hc == nil {
		return nil, []error{errors.New("no HTTP client")}
	}
	if maxPages <= 0 {
		return nil, nil
	}
	normalise(su)

	rules := fetchRobots(ctx, hc, su)
	if !rules.allowed(su) {
		return nil, []error{fmt.Errorf("%s: robots.txt does not allow TaskiemBot to read it", su)}
	}
	twins := map[string]bool{}
	for _, o := range opts.AllowOrigins {
		if ou, err := url.Parse(strings.TrimSpace(o)); err == nil && isTwin(ou, su) {
			twins[originOf(ou)] = true
		}
	}
	first, found, err := fetchPage(ctx, guardRedirects(hc, su, rules, twins), su)
	if err == nil && first == nil {
		err = errors.New("the start page is not an HTML page")
	}
	if err != nil {
		return nil, []error{fmt.Errorf("%s: %w", su, err)}
	}
	fu, _ := url.Parse(first.URL)
	if originOf(fu) != originOf(su) {
		// Checked again, though the redirect guard has already refused
		// anything else: the crawl must never go to an origin not allowed.
		if !twins[originOf(fu)] || downgrade(su, fu) {
			return nil, []error{fmt.Errorf("%s: redirected off the site, to %s", su, fu.Redacted())}
		}
		// The site lives at its twin: its robots.txt is the one to obey.
		rules = fetchRobots(ctx, hc, fu)
		if !rules.allowed(fu) {
			return nil, []error{fmt.Errorf("%s: robots.txt does not allow TaskiemBot to read it", fu)}
		}
	}
	hc = guardRedirects(hc, fu, rules, nil)

	var (
		errs  []error
		pages []Page
		queue []*url.URL
		seen  = map[string]bool{su.String(): true, fu.String(): true}
	)
	add := func(p *Page, found []*url.URL) {
		pages = append(pages, *p)
		for _, l := range found {
			k := l.String()
			// The queue is bounded so that a site of endless generated
			// links cannot grow it without limit.
			if seen[k] || len(queue) >= maxPages*20 || !crawlable(l) || !rules.allowed(l) {
				continue
			}
			seen[k] = true
			queue = append(queue, l)
		}
	}
	add(first, found)
	for len(queue) > 0 && len(pages) < maxPages {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		u := queue[0]
		queue = queue[1:]
		page, found, err := fetchPage(ctx, hc, u)
		if err != nil {
			if ctx.Err() == nil {
				errs = append(errs, fmt.Errorf("%s: %w", u, err))
			}
			continue
		}
		if page == nil {
			continue // not HTML
		}
		if page.URL != u.String() {
			if seen[page.URL] {
				continue // a redirect to a page already read or queued
			}
			seen[page.URL] = true
		}
		add(page, found)
	}
	return pages, errs
}

// guardRedirects returns a copy of hc that refuses, before requesting it,
// a redirect off site's origin or to a page the crawl would not follow a
// link to. It also allows the origins in twins that are site's http/https
// or www/bare twins, but never a step from https down to http. The copy
// shares hc's transport, so the caller's egress guard still applies.
func guardRedirects(hc *http.Client, site *url.URL, rules robots, twins map[string]bool) *http.Client {
	c := *hc
	next := hc.CheckRedirect
	origin := originOf(site)
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		o := originOf(req.URL)
		from := site
		if len(via) > 0 {
			from = via[len(via)-1].URL
		}
		switch {
		case downgrade(site, req.URL) || downgrade(from, req.URL):
			return fmt.Errorf("redirected from https to http, to %s", req.URL.Redacted())
		case o != origin && (!twins[o] || !isTwin(req.URL, site)):
			return fmt.Errorf("redirected off the site, to %s", req.URL.Redacted())
		case o == origin && (!crawlable(req.URL) || !rules.allowed(req.URL)):
			return fmt.Errorf("redirected to %s, which is not crawled", req.URL.Redacted())
		case next != nil:
			return next(req, via)
		case len(via) >= 10:
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &c
}

// downgrade reports whether going from a to b leaves https for http.
func downgrade(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, "https") && strings.EqualFold(b.Scheme, "http")
}

// isTwin reports whether u is on site's host or its www/bare twin, over
// http or https, on the default port.
func isTwin(u, site *url.URL) bool {
	if originOf(u) == "" || u.Port() != "" && u.Port() != "80" && u.Port() != "443" {
		return false
	}
	bare := func(h string) string { return strings.TrimPrefix(strings.ToLower(h), "www.") }
	return bare(u.Hostname()) == bare(site.Hostname())
}

// crawlable rules out links that are not content pages.
func crawlable(u *url.URL) bool {
	if len(u.RawQuery) > maxQuery {
		return false
	}
	p := strings.ToLower(u.Path)
	if nonHTML[path.Ext(p)] {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if nonContent[seg] {
			return false
		}
	}
	return true
}

// fetchPage reads one page. It returns a nil page, without an error, for
// a response that is not HTML.
func fetchPage(ctx context.Context, hc *http.Client, u *url.URL) (*Page, []*url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	// hc's redirect guard has kept the final URL on the site.
	final := resp.Request.URL
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("status %s", resp.Status)
	}
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mt != "text/html" && mt != "application/xhtml+xml" {
		return nil, nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes))
	if err != nil {
		return nil, nil, err
	}
	// A body cut at the limit is still parsed: HTML parsing recovers from a
	// truncated document and the first 2 MB holds the page's content.
	fu := *final
	normalise(&fu)
	page, links, err := HTMLText(body, &fu)
	if err != nil {
		return nil, nil, err
	}
	return &page, links, nil
}

// robots is the part of a robots.txt that applies to Taskiem's bot.
type robots struct {
	rules []robotsRule
}

type robotsRule struct {
	allow   bool
	pattern string
}

// allowed applies the most specific (longest) matching rule, Allow
// winning a tie, as Google and RFC 9309 do. No matching rule allows. The
// path and the rules are compared in one spelling (see robotsEscape), so
// that /caf%C3%A9, /caf%c3%a9 and /café are the same path, and %7E is ~.
func (r robots) allowed(u *url.URL) bool {
	target := u.EscapedPath()
	if target == "" {
		target = "/"
	}
	if u.RawQuery != "" {
		target += "?" + u.RawQuery
	}
	target = robotsEscape(target, false)
	best, allow := -1, true
	for _, rule := range r.rules {
		if !robotsMatch(rule.pattern, target) {
			continue
		}
		if l := len(rule.pattern); l > best || l == best && rule.allow {
			best, allow = l, rule.allow
		}
	}
	return allow
}

// robotsEscape spells a path (or, with pattern set, a robots.txt rule) in
// one canonical way: a percent-escape stays escaped, in upper case, only
// where decoding it would change what the path means or how it matches
// (/ ? # % * $, and anything not printable ASCII); every other escape is
// decoded; and bytes that are not printable ASCII are escaped. In a path,
// * and $ are escaped too, since they are wildcards only in a rule; in a
// rule, * stays a wildcard and $ an anchor at its end.
func robotsEscape(s string, pattern bool) string {
	const hex = "0123456789ABCDEF"
	keep := func(c byte) bool { // stays escaped
		return c <= ' ' || c >= 0x7f || strings.IndexByte("/?#%*$", c) >= 0
	}
	unhex := func(c byte) (byte, bool) {
		switch {
		case c >= '0' && c <= '9':
			return c - '0', true
		case c >= 'a' && c <= 'f':
			return c - 'a' + 10, true
		case c >= 'A' && c <= 'F':
			return c - 'A' + 10, true
		}
		return 0, false
	}
	var b strings.Builder
	esc := func(c byte) { b.WriteByte('%'); b.WriteByte(hex[c>>4]); b.WriteByte(hex[c&15]) }
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '%' {
			if i+2 < len(s) {
				hi, ok1 := unhex(s[i+1])
				lo, ok2 := unhex(s[i+2])
				if ok1 && ok2 {
					if v := hi<<4 | lo; keep(v) {
						esc(v)
					} else {
						b.WriteByte(v)
					}
					i += 2
					continue
				}
			}
			esc('%') // a stray %
			continue
		}
		switch {
		case pattern && c == '*', pattern && c == '$' && i == len(s)-1:
			b.WriteByte(c)
		case c <= ' ' || c >= 0x7f || c == '*' || c == '$':
			esc(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// robotsMatch matches a robots.txt path pattern: a prefix, where * matches
// any run of characters and a trailing $ anchors the end.
func robotsMatch(pattern, target string) bool {
	if strings.HasSuffix(pattern, "$") {
		pattern = strings.TrimSuffix(pattern, "$")
	} else {
		pattern += "*"
	}
	// Wildcard matching with one backtrack point.
	p, t, star, mark := 0, 0, -1, 0
	for t < len(target) {
		switch {
		case p < len(pattern) && pattern[p] == '*':
			star, mark = p, t
			p++
		case p < len(pattern) && pattern[p] == target[t]:
			p++
			t++
		case star >= 0:
			p = star + 1
			mark++
			t = mark
		default:
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}

// fetchRobots reads the site's robots.txt. A missing or unreadable one
// allows everything, as crawlers conventionally treat it; a server error
// is also read as allowing, since this crawl is one-off and asked for by
// the site's owner.
func fetchRobots(ctx context.Context, hc *http.Client, site *url.URL) robots {
	ru := url.URL{Scheme: site.Scheme, Host: site.Host, Path: "/robots.txt"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ru.String(), nil)
	if err != nil {
		return robots{}
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := hc.Do(req)
	if err != nil {
		return robots{}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return robots{}
	}
	return parseRobots(io.LimitReader(resp.Body, maxRobotsBytes))
}

// parseRobots keeps the rules of the group for TaskiemBot if there is one,
// else those for every bot (*). Consecutive User-agent lines share a group;
// as RFC 9309 has it, any other line after them (a rule, or a Crawl-delay,
// Sitemap or unknown line) ends the list of agents, so a User-agent line
// after it starts a new group rather than joining that one.
func parseRobots(r io.Reader) robots {
	var (
		ours, all []robotsRule
		hasOurs   bool
		agents    []string
		inRules   bool
	)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 64<<10)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.TrimSpace(val)
		switch key {
		case "user-agent":
			if inRules {
				agents, inRules = nil, false
			}
			agents = append(agents, strings.ToLower(val))
		case "allow", "disallow":
			inRules = true
			if val == "" {
				continue // "Disallow:" with nothing allows everything
			}
			rule := robotsRule{allow: key == "allow", pattern: robotsEscape(val, true)}
			for _, a := range agents {
				switch {
				case a == "*":
					all = append(all, rule)
				case isTaskiemBot(a):
					ours = append(ours, rule)
				}
			}
		default:
			inRules = true
		}
		for _, a := range agents {
			if isTaskiemBot(a) {
				hasOurs = true
			}
		}
	}
	// A scanner error (a line over 64 KB) ends the file there: the rules
	// read so far still apply.
	if hasOurs {
		return robots{rules: ours}
	}
	return robots{rules: all}
}

// isTaskiemBot reports whether a User-agent line names Taskiem's bot.
func isTaskiemBot(agent string) bool {
	return agent == "taskiembot" || strings.HasPrefix(agent, "taskiembot/")
}
