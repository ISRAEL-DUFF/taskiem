package assistant

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/israel-duff/taskiem/engine/lang"
)

// The reply check (decision 0062 L6, A11, D6): before an assistant's reply
// is sent, it is checked for what a model must not say to a customer. A
// customer's message is instructions to the model as much as the owner's
// prompt, and outside material can carry instructions too, so a reply can
// be talked into a price, a link or a promise the business never made.
// What the reply may state comes from what the run gave the model: the
// material (the business's facts) and the customer's own message.

// CheckInput is what the check reads.
type CheckInput struct {
	Text     string   // the reply
	Material string   // what the model was given to answer from
	Customer string   // what the customer wrote (or said)
	Links    []string // links the business allows besides those in the material
	Never    []string // values never said to a customer (internal fields)
	Max      int      // the longest reply, in characters (default 4,000)
	Language string   // for the held answer
}

// Problem is one thing the check found.
type Problem struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// Problem kinds.
const (
	ProblemEmpty       = "empty"       // nothing to send
	ProblemPlaceholder = "placeholder" // a mask or a marker the model copied ([phone], {{...}}, <<material>>)
	ProblemTooLong     = "too_long"
	ProblemPrice       = "price"    // an amount not in the material or the customer's message
	ProblemLink        = "link"     // a link to a site not in the material nor allowed
	ProblemContact     = "contact"  // a phone number or email not in the material nor the customer's message
	ProblemSecret      = "secret"   // something shaped like a key or a token
	ProblemInternal    = "internal" // one of the values never to say
)

// CheckResult is the check's answer: OK, or the problems and the held
// answer to send instead (in the customer's language).
type CheckResult struct {
	OK       bool      `json:"ok"`
	Text     string    `json:"text"`
	Problems []Problem `json:"problems"`
}

var (
	placeholderRe = regexp.MustCompile(`(?i)\[(phone|email|name|bvn|nin|card|account|address|other|redacted|personal)\]|\{\{|\}\}|<<|>>|‹‹|››|<\s*/?\s*material`)
	urlRe         = regexp.MustCompile(`(?i)\b(?:https?://|www\.)[^\s<>"')\]]+`)
	moneyRe       = regexp.MustCompile(`(?i)(?:₦|\b(?:ngn|naira|n|usd)|\$|£|€)\s?\d[\d,]*(?:\.\d+)?\s?(?:k|m)?\b|\b\d[\d,]*(?:\.\d+)?\s?(?:k|m)?\s?(?:naira|ngn|dollars?|usd)\b|\b\d[\d,]*(?:\.\d+)?k\b`)
	phoneRe       = regexp.MustCompile(`\+?\d[\d\s-]{8,}\d`)
	emailRe       = regexp.MustCompile(`(?i)[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}`)
	secretRe      = regexp.MustCompile(`\b(?:sk|pk|rk)_(?:live|test)_[A-Za-z0-9]{8,}|\bsk-[A-Za-z0-9_-]{16,}|(?i)\bbearer\s+[A-Za-z0-9._-]{16,}|-----BEGIN [A-Z ]*PRIVATE KEY-----|\bAKIA[0-9A-Z]{16}\b`)
)

// CheckReply checks a reply.
func CheckReply(in CheckInput) CheckResult {
	text := strings.TrimSpace(in.Text)
	var probs []Problem
	add := func(kind, detail string) {
		for _, p := range probs {
			if p.Kind == kind && p.Detail == detail {
				return
			}
		}
		probs = append(probs, Problem{Kind: kind, Detail: detail})
	}
	if text == "" {
		add(ProblemEmpty, "")
	}
	max := in.Max
	if max <= 0 {
		max = 4000
	}
	if n := utf8.RuneCountInString(text); n > max {
		add(ProblemTooLong, "")
	}
	for _, m := range placeholderRe.FindAllString(text, -1) {
		add(ProblemPlaceholder, m)
	}
	known := in.Material + "\n" + in.Customer
	knownDigits := digitRuns(known)
	// Prices: every amount must be one the business's material (or the
	// customer) gave.
	for _, m := range moneyRe.FindAllString(text, -1) {
		d := amountDigits(m)
		if d == "" || len(expandK(m, d)) < 2 {
			continue // "N1" and the like: not an amount worth stopping a reply for
		}
		if !knownDigits[d] && !knownDigits[expandK(m, d)] {
			add(ProblemPrice, strings.TrimSpace(m))
		}
	}
	// Links: to a host the material names, or one allowed.
	hosts := map[string]bool{}
	for _, u := range append(urlRe.FindAllString(in.Material, -1), in.Links...) {
		if h := hostOf(u); h != "" {
			hosts[h] = true
		}
	}
	for _, u := range urlRe.FindAllString(text, -1) {
		h := hostOf(u)
		if h == "" || !hosts[h] && !hosts["www."+h] && !hosts[strings.TrimPrefix(h, "www.")] {
			add(ProblemLink, strings.TrimRight(u, ".,;:!?"))
		}
	}
	// Contact details: the business's (in the material) or the customer's
	// own, never anyone else's.
	for _, m := range phoneRe.FindAllString(text, -1) {
		d := onlyDigits(m)
		if len(d) >= 10 && !knownPhone(d, knownDigits) {
			add(ProblemContact, maskTail(m))
		}
	}
	lowKnown := strings.ToLower(known)
	for _, m := range emailRe.FindAllString(text, -1) {
		if !strings.Contains(lowKnown, strings.ToLower(m)) {
			add(ProblemContact, m)
		}
	}
	for _, m := range secretRe.FindAllString(text, -1) {
		add(ProblemSecret, maskTail(m))
	}
	low := strings.ToLower(text)
	for _, v := range in.Never {
		v = strings.TrimSpace(v)
		if utf8.RuneCountInString(v) >= 3 && strings.Contains(low, strings.ToLower(v)) {
			add(ProblemInternal, maskTail(v))
		}
	}
	if len(probs) == 0 {
		return CheckResult{OK: true, Text: text, Problems: []Problem{}}
	}
	return CheckResult{OK: false, Text: HeldText(in.Language), Problems: probs}
}

// HeldText is the answer sent instead of a reply that failed the check.
func HeldText(language string) string {
	t := lang.Tag(language)
	if !lang.Valid(t) {
		t = lang.EN
	}
	return lang.Default().Text(t, "assistant.held")
}

func hostOf(raw string) string {
	raw = strings.TrimRight(raw, ".,;:!?")
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// digitRuns are the numbers in a text, digits only (1,500.00 → 150000 and
// 1500), to compare amounts however they are written.
func digitRuns(s string) map[string]bool {
	out := map[string]bool{}
	for _, m := range regexp.MustCompile(`\d[\d,\s]*(?:\.\d+)?`).FindAllString(s, -1) {
		m = strings.TrimSpace(m)
		whole := m
		if i := strings.Index(m, "."); i >= 0 {
			whole = m[:i]
			if strings.Trim(m[i+1:], "0") != "" {
				out[onlyDigits(m)] = true
			}
		}
		out[onlyDigits(whole)] = true
		// Numbers run together by spaces ("1 500") and the parts.
		for _, f := range strings.FieldsFunc(whole, func(r rune) bool { return r == ' ' || r == '\n' }) {
			out[onlyDigits(f)] = true
		}
	}
	return out
}

// amountDigits is an amount's digits (its whole part).
func amountDigits(m string) string {
	n := regexp.MustCompile(`\d[\d,]*(?:\.\d+)?`).FindString(m)
	if i := strings.Index(n, "."); i >= 0 {
		if strings.Trim(n[i+1:], "0") == "" {
			n = n[:i]
		}
	}
	return onlyDigits(n)
}

// expandK is "5k" as 5000 (and 2m as 2000000), to find it written out.
func expandK(m, d string) string {
	low := strings.ToLower(strings.TrimSpace(m))
	switch {
	case strings.HasSuffix(low, "k"):
		return d + "000"
	case strings.HasSuffix(low, "m"):
		return d + "000000"
	}
	return d
}

// knownPhone: a number in the material or the customer's message, written
// with 0 or 234.
func knownPhone(d string, known map[string]bool) bool {
	forms := []string{d}
	if strings.HasPrefix(d, "234") && len(d) > 3 {
		forms = append(forms, "0"+d[3:])
	}
	if strings.HasPrefix(d, "0") {
		forms = append(forms, "234"+d[1:])
	}
	for k := range known {
		for _, f := range forms {
			if len(k) >= 10 && (k == f || strings.HasSuffix(k, f) || strings.HasSuffix(f, k)) {
				return true
			}
		}
	}
	return slices.ContainsFunc(forms, func(f string) bool { return known[f] })
}

func maskTail(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= 4 {
		return strings.Repeat("•", len(r))
	}
	return strings.Repeat("•", len(r)-4) + string(r[len(r)-4:])
}
