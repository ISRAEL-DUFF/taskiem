// Package linkify finds the web addresses in a chat message's words
// (docs/spec/chat-previews-and-editing.md §2.2): the one rule the web
// (web/src/lib/linkify.ts), the app (mobile/lib/chat/linkify.dart) and the
// server's link previews share, tested against the same cases
// (web/src/lib/linkify.cases.json).
//
// What counts: http:// and https:// addresses, and bare ones such as
// jumia.com.ng/phones or www.example.com, taken as https. Nothing else:
// no other scheme, no email address, no address with a user name in it.
// A full stop, comma, semicolon, colon, !, ?, closing bracket or quote at
// the end is not part of it, unless the bracket closes one opened inside.
package linkify

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Link is one web address found in words.
type Link struct {
	// Start and End are byte offsets of Text in the words.
	Start, End int
	// Text is the address as written.
	Text string
	// URL is what it opens: Text with https:// in front of a bare one.
	URL string
}

// The candidates: an http(s) address, or a bare host name with an
// optional port and path. Every language that shares the rule uses the
// same expression, without look-behind (Go's RE2 has none): what comes
// before and after a candidate is checked by hand.
var candidate = regexp.MustCompile(`(?i)https?://[^\s<>"]+|(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,24}(?::[0-9]{1,5})?(?:[/?#][^\s<>"]*)?`)

// bareTLDs are the endings a bare address (without www.) must have, so
// "file.txt" or "index.html" stay words.
var bareTLDs = map[string]bool{
	"com": true, "net": true, "org": true, "ng": true, "io": true, "co": true, "app": true, "dev": true,
	"ai": true, "info": true, "biz": true, "me": true, "shop": true, "store": true, "online": true, "site": true,
	"xyz": true, "tech": true, "africa": true, "edu": true, "gov": true, "uk": true, "us": true, "ca": true,
	"za": true, "ke": true, "gh": true, "de": true, "fr": true, "ly": true, "gl": true, "tv": true,
	"link": true, "page": true, "blog": true, "news": true,
}

const trailing = `.,;:!?)"'`

// Find is every web address in text, in order.
func Find(text string) []Link {
	var out []Link
	for _, m := range candidate.FindAllStringIndex(text, -1) {
		start, end := m[0], m[1]
		if start > 0 {
			// Part of a longer word, an email address or another address.
			if prev, _ := utf8.DecodeLastRuneInString(text[:start]); isWordRune(prev) || strings.ContainsRune("@._/-:", prev) {
				continue
			}
		}
		if end < len(text) {
			if next, _ := utf8.DecodeRuneInString(text[end:]); next == '@' || isWordRune(next) {
				continue
			}
		}
		raw := text[start:end]
		// Trailing punctuation is the sentence's, not the address's.
		for raw != "" && strings.IndexByte(trailing, raw[len(raw)-1]) >= 0 {
			if raw[len(raw)-1] == ')' && strings.Count(raw, "(") >= strings.Count(raw, ")") {
				break
			}
			raw = raw[:len(raw)-1]
		}
		lower := strings.ToLower(raw)
		scheme := strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
		host := raw
		if scheme {
			host = raw[strings.Index(raw, "://")+3:]
		}
		if i := strings.IndexAny(host, "/?#"); i >= 0 {
			host = host[:i]
		}
		if host == "" || strings.Contains(host, "@") {
			continue
		}
		name := strings.ToLower(host)
		if i := strings.LastIndexByte(name, ':'); i >= 0 {
			name = name[:i]
		}
		if !scheme {
			if !strings.HasPrefix(name, "www.") && !bareTLDs[name[strings.LastIndexByte(name, '.')+1:]] {
				continue
			}
		} else if !validHost(name) {
			continue
		}
		u := "https://" + raw
		if scheme {
			i := strings.Index(raw, "://")
			u = lower[:i] + raw[i:]
		}
		out = append(out, Link{Start: start, End: start + len(raw), Text: raw, URL: u})
	}
	return out
}

// First is the URL of the first web address in text, or "".
func First(text string) string {
	if l := Find(text); len(l) > 0 {
		return l[0].URL
	}
	return ""
}

// Shown is how a long address is shown, shortened in the middle
// (jumia.com.ng/…/phones); it opens in full.
func Shown(text string) string {
	const most = 40
	if utf8.RuneCountInString(text) <= most {
		return text
	}
	rest := text
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	host, path := rest, ""
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		host, path = rest[:i], rest[i+1:]
	}
	segs := strings.FieldsFunc(path, func(r rune) bool { return r == '/' })
	if len(segs) > 0 {
		last := segs[len(segs)-1]
		if utf8.RuneCountInString(last) > 20 {
			last = string([]rune(last)[:19]) + "…"
		}
		if utf8.RuneCountInString(host) <= 30 {
			if len(segs) == 1 {
				return host + "/" + last
			}
			return host + "/…/" + last
		}
	}
	r := []rune(rest)
	if len(r) <= most {
		return rest
	}
	return string(r[:24]) + "…" + string(r[len(r)-15:])
}

func isWordRune(r rune) bool {
	return r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// validHost is a host name or IPv4 address with an optional port, as an
// http(s) address must have.
func validHost(h string) bool {
	if h == "" || strings.HasPrefix(h, ".") || strings.HasSuffix(h, ".") || strings.Contains(h, "..") {
		return false
	}
	for _, r := range h {
		if r == '_' || (!isWordRune(r) && r != '.' && r != '-') {
			return false
		}
	}
	return true
}
