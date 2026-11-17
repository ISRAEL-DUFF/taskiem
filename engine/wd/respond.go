package wd

import (
	"fmt"
	"mime"
	"net/url"
	"sort"
	"strings"
)

// A respond step's answer is sent from Taskiem's own address, where
// people may be signed in. So the step chooses little about how a browser
// treats it: the rules here are enforced when the answer is sent
// (engine/ingest) and told to the author when the workflow is saved.

// respondNamed are the headers a respond step may set besides X- headers.
var respondNamed = map[string]bool{"content-type": true, "location": true, "cache-control": true}

const (
	respondBrowser = "Taskiem sets browser protections itself"
	respondProxy   = "a server in front of Taskiem could act on it"
)

// respondServerOwned are X- headers that are the server's own, or that a
// proxy in front of Taskiem would act on: a respond step may not set them.
var respondServerOwned = map[string]string{
	"x-content-type-options":            respondBrowser,
	"x-frame-options":                   respondBrowser,
	"x-xss-protection":                  respondBrowser,
	"x-content-security-policy":         respondBrowser,
	"x-webkit-csp":                      respondBrowser,
	"x-permitted-cross-domain-policies": respondBrowser,
	"x-download-options":                respondBrowser,
	"x-ua-compatible":                   respondBrowser,
	"x-dns-prefetch-control":            respondBrowser,
	"x-sendfile":                        respondProxy,
	"x-lighttpd-send-file":              respondProxy,
}

// RespondHeader reports whether a respond step may set the header name.
// When it may not, why is a plain reason an author can read.
func RespondHeader(name string) (ok bool, why string) {
	lk := strings.ToLower(strings.TrimSpace(name))
	switch {
	case respondNamed[lk]:
		return true, ""
	case respondServerOwned[lk] != "":
		return false, respondServerOwned[lk]
	case strings.HasPrefix(lk, "x-taskiem-"):
		return false, "headers that start with X-Taskiem- are Taskiem's own"
	case strings.HasPrefix(lk, "x-accel-"):
		return false, respondProxy
	case strings.HasPrefix(lk, "x-"):
		return true, ""
	case lk == "set-cookie":
		return false, "an answer may not set cookies"
	case lk == "content-security-policy", lk == "content-security-policy-report-only", lk == "strict-transport-security", lk == "referrer-policy":
		return false, respondBrowser
	}
	return false, "only Content-Type, Location, Cache-Control and X- headers are sent"
}

// respondTypes are the content types a respond step's answer may have,
// and whether a browser that opens the answer shows it (true) or saves it
// as a file (false). Types a browser would treat as a page, a script or a
// style sheet are not here.
var respondTypes = map[string]bool{
	"application/json":                  true,
	"text/plain":                        true,
	"text/csv":                          false,
	"application/xml":                   false,
	"text/xml":                          false,
	"application/x-www-form-urlencoded": false,
	"application/pdf":                   false,
	"image/png":                         false,
	"image/jpeg":                        false,
	"image/gif":                         false,
	"image/webp":                        false,
	"application/octet-stream":          false,
}

// RespondTypeNames lists the content types an answer may have, for
// messages.
func RespondTypeNames() string {
	names := make([]string, 0, len(respondTypes))
	for k := range respondTypes {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ") + ", or a JSON type such as application/problem+json"
}

// RespondPlainText is what an answer is sent as when its own content type
// is not allowed.
const RespondPlainText = "text/plain; charset=utf-8"

// RespondContentType is the Content-Type an answer is sent with, given the
// one the step asked for: the same type when it is allowed (keeping only
// its charset), and plain text otherwise. kept is false when the type was
// changed. inline is whether a browser may show the answer rather than
// save it as a file.
func RespondContentType(asked string) (sent string, kept, inline bool) {
	mt, params, err := mime.ParseMediaType(asked)
	if err != nil {
		return RespondPlainText, false, true
	}
	show, ok := respondTypes[mt]
	if !ok && strings.HasPrefix(mt, "application/") && strings.HasSuffix(mt, "+json") {
		show, ok = true, true
	}
	if !ok {
		return RespondPlainText, false, true
	}
	keep := map[string]string{}
	if cs := params["charset"]; cs != "" {
		keep["charset"] = cs
	} else if strings.HasPrefix(mt, "text/") {
		keep["charset"] = "utf-8"
	}
	if sent = mime.FormatMediaType(mt, keep); sent == "" {
		return RespondPlainText, false, true
	}
	return sent, true, show
}

// RespondLocation reports whether a respond step may send the Location
// header: only with a redirect status, and only to a full http or https
// address. Anything else (javascript:, data:, an address without its
// scheme, one with a user name in it) is not sent.
func RespondLocation(status int, location string) bool {
	return status >= 300 && status <= 399 && respondURL(location)
}

func respondURL(location string) bool {
	if location == "" || strings.ContainsAny(location, "\\\r\n\t") || strings.TrimSpace(location) != location {
		return false
	}
	lower := strings.ToLower(location)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return false
	}
	u, err := url.Parse(location)
	return err == nil && u.Hostname() != "" && u.User == nil
}

// RespondProblems tells the author about a respond step's literal headers
// that would not be sent as written. Values that are expressions are
// decided when the answer is sent. It is checked when a workflow is saved
// (engine/wdcheck), not when a stored one is loaded: a workflow published
// before these rules still runs, and its answer is made safe as it is sent.
func RespondProblems(s *Step) []string {
	if s.Respond == nil {
		return nil
	}
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	names := make([]string, 0, len(s.Respond.Headers))
	for k := range s.Respond.Headers {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		if ok, why := RespondHeader(name); !ok {
			add("%s cannot be set by an answer: %s. Remove it", name, why)
			continue
		}
		val, isText := s.Respond.Headers[name].(string)
		if !isText || strings.HasPrefix(val, "=") {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "content-type":
			if _, kept, _ := RespondContentType(val); !kept {
				add("an answer cannot be sent as %s, because a browser could open it as a page on Taskiem's own address; it would go out as plain text. Use one of: %s", val, RespondTypeNames())
			}
		case "location":
			if !respondURL(val) {
				add("Location must be a full address that starts with http:// or https://")
			} else if st := s.Respond.Status; st < 300 || st > 399 {
				add("Location is only sent with a redirect status (300 to 399): set status to 302, or remove Location")
			}
		}
	}
	return out
}
