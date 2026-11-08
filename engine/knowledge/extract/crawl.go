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
const UserAgent = "TaskiemBot/1.0 (+https://taskiem.com/bot)"

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
// One exception to staying on start's origin: the start page may redirect
// to its http/https or www/bare twin (example.com to https://www.example.com),
// as most sites do, and the crawl then stays on that origin instead.
//
// hc does the requests, and should be the caller's egress-guarded client:
// Crawl never dials on its own. Redirects are followed within the site
// only, and only to pages the rules above allow, on top of hc's own
// redirect policy.
func Crawl(ctx context.Context, hc *http.Client, start string, maxPages int) ([]Page, []error) {
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
	first, found, err := fetchPage(ctx, guardRedirects(hc, su, rules, true), su)
	if err == nil && first == nil {
		err = errors.New("the start page is not an HTML page")
	}
	if err != nil {
		return nil, []error{fmt.Errorf("%s: %w", su, err)}
	}
	fu, _ := url.Parse(first.URL)
	if originOf(fu) != originOf(su) {
		// The site lives at its twin: its robots.txt is the one to obey.
		rules = fetchRobots(ctx, hc, fu)
		if !rules.allowed(fu) {
			return nil, []error{fmt.Errorf("%s: robots.txt does not allow TaskiemBot to read it", fu)}
		}
	}
	hc = guardRedirects(hc, fu, rules, false)

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
// link to. With twins set it also allows site's http/https and www/bare
// twins. The copy shares hc's transport, so the caller's egress guard
// still applies.
func guardRedirects(hc *http.Client, site *url.URL, rules robots, twins bool) *http.Client {
	c := *hc
	next := hc.CheckRedirect
	origin := originOf(site)
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		o := originOf(req.URL)
		switch {
		case o != origin && (!twins || !isTwin(req.URL, site)):
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
// winning a tie, as Google and RFC 9309 do. No matching rule allows.
func (r robots) allowed(u *url.URL) bool {
	target := u.EscapedPath()
	if target == "" {
		target = "/"
	}
	if u.RawQuery != "" {
		target += "?" + u.RawQuery
	}
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
// else those for every bot (*). Consecutive User-agent lines share a group.
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
			rule := robotsRule{allow: key == "allow", pattern: val}
			for _, a := range agents {
				switch {
				case a == "*":
					all = append(all, rule)
				case isTaskiemBot(a):
					ours = append(ours, rule)
				}
			}
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
