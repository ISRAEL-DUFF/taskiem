package assistant

import (
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/israel-duff/taskiem/engine/lang"
)

// The reply check (decision 0062 L6, A11, D6): before an assistant's reply
// is sent, it is checked for what a model must not say to a customer. A
// customer's message is instructions to the model as much as the owner's
// prompt, and outside material can carry instructions too, so a reply can
// be talked into a price, a link or a promise the business never made.
// What the reply may state comes from what the run gave the model: prices,
// links and promises from the material (the business's facts) only; phone
// numbers and emails from the material or the customer's own message.
// Words the check cannot read (an amount spelled out, another customer's
// name) are a limit: the recipe offers a person, and members see every
// conversation.

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
	ProblemPrice       = "price"    // an amount the material does not state
	ProblemLink        = "link"     // a link to a site not in the material nor allowed
	ProblemContact     = "contact"  // a phone number or email not in the material nor the customer's message
	ProblemSecret      = "secret"   // something shaped like a key or a token
	ProblemPromise     = "promise"  // something free, a discount or a refund the material does not offer
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
	// Taskiem's masks ([[phone_1]], the masker's; [phone], the redactor's),
	// template markers and the material's quotes.
	placeholderRe = regexp.MustCompile(`(?i)\[\[[a-z0-9_]+_[0-9]+\]\]|\[(phone|email|name|bvn|nin|card|account|address|other|redacted|personal)\]|\{\{|\}\}|<<|‹‹|material \d+\s*(?:>>|››)|<\s*/?\s*material`)
	// Links: with a scheme or www., or a bare domain on a common ending
	// ("evil.example/pay" needs one of them; "bit.ly/x", "wa.me/…" do).
	urlRe = regexp.MustCompile(`(?i)\b(?:https?://|www\.)[^\s<>"')\]]+|\b(?:[a-z0-9-]+\.)+(?:com|ng|net|org|ly|me|io|co|link|app|shop|store|xyz|info|biz|online|site|africa|gl|to|click|page|top)\b(?:/[^\s<>"')\]]*)?`)
	// Amounts: with a currency, or 5k / 2m, or "naira" after.
	moneyRe = regexp.MustCompile(`(?i)(?:₦|\b(?:ngn|naira|n|usd|gbp|eur)|\$|£|€)\s?\d[\d,]*(?:\.\d+)?\s?(?:k|m)?\b|\b\d[\d,]*(?:\.\d+)?\s?(?:k|m)?\s?(?:naira|ngn|dollars?|usd|pounds?|euros?)\b|\b\d[\d,]*(?:\.\d+)?k\b`)
	// Phone numbers: Nigerian (0 or 234, then 7, 8 or 9) and international
	// with a +, separated by spaces, dots or dashes.
	phoneRe  = regexp.MustCompile(`(?:\+\s?234|\b234|\b0)[\s.-]?[789][01](?:[\s.-]?\d){8}\b|\+\d(?:[\s.-]?\d){9,13}\b`)
	emailRe  = regexp.MustCompile(`(?i)[a-z0-9._%+-]+\s?@\s?[a-z0-9-]+(?:\.[a-z0-9-]+)*\.[a-z]{2,}|[a-z0-9._%+-]+\s?(?:\(at\)|\[at\]|\sat\s)\s?[a-z0-9-]+\s?(?:\.|\(dot\)|\[dot\]|\sdot\s)\s?(?:com|ng|net|org|co)\b`)
	secretRe = regexp.MustCompile(`\b(?:sk|pk|rk)_(?:live|test)_[A-Za-z0-9]{8,}|\bsk-[A-Za-z0-9_-]{16,}|(?i)\bbearer\s+[A-Za-z0-9._-]{16,}|-----BEGIN [A-Z ]*PRIVATE KEY-----|\bAKIA[0-9A-Z]{16}\b|\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)
	// Promises a reply must not make unless the material does: something
	// free, a discount, a refund (A11), in the five languages (drafts).
	promiseRe = regexp.MustCompile(`(?i)\b(?:free of charge|for free|free|no charge|discount(?:ed)?|\d+\s?% off|percent off|refund(?:ed|s)?|money back|cash ?back|waive[ds]?|na free|ofe|lofe|kyauta|n'?efu|efu)\b`)
	// A line of the material that states prices: its plain numbers are
	// prices too ("Jollof rice 3,500").
	priceLineRe = regexp.MustCompile(`(?i)₦|\bngn\b|\bnaira\b|\bprice|\bcost|\bfee|\bcharge|\bper\b|\beach\b|\bdelivery\b`)
	numberRe    = regexp.MustCompile(`\d[\d,]*(?:\.\d+)?\s?(?:k|m)?\b`)
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
	// Prices: every amount must be a price the business's material states,
	// by value (₦3,500, 3500 naira and 3.5k are one price). The customer's
	// own message never makes an amount allowed: "can I pay ₦100?" is
	// exactly what a reply must not agree to.
	prices := materialPrices(in.Material)
	for _, m := range moneyRe.FindAllString(text, -1) {
		v, ok := amountValue(m)
		if !ok {
			continue
		}
		low := strings.ToLower(strings.TrimSpace(m))
		if strings.HasPrefix(low, "n") && !strings.HasPrefix(low, "naira") && !strings.HasPrefix(low, "ngn") && v < 100*100 {
			continue // "table N12": a bare N with a small number is not a price
		}
		if !prices[v] {
			add(ProblemPrice, strings.TrimSpace(m))
		}
	}
	// Promises (free, a discount, a refund) the material does not make.
	foldMaterial := lang.Fold(in.Material)
	for _, m := range promiseRe.FindAllString(lang.Fold(text), -1) {
		if !strings.Contains(foldMaterial, strings.ToLower(m)) {
			add(ProblemPromise, m)
		}
	}
	// Links: to a host the material names, or one allowed.
	hosts := map[string]bool{}
	for _, u := range append(findLinks(in.Material), in.Links...) {
		if h := hostOf(u); h != "" {
			hosts[strings.TrimPrefix(h, "www.")] = true
		}
	}
	for _, u := range findLinks(text) {
		h := strings.TrimPrefix(hostOf(u), "www.")
		if h == "" || !hosts[h] {
			add(ProblemLink, strings.TrimRight(u, ".,;:!?"))
		}
	}
	// Contact details: the business's (in the material) or the customer's
	// own, never anyone else's.
	known := in.Material + "\n" + in.Customer
	phones := map[string]bool{}
	for _, m := range phoneRe.FindAllString(known, -1) {
		phones[phoneKey(m)] = true
	}
	for _, m := range phoneRe.FindAllString(text, -1) {
		if !phones[phoneKey(m)] {
			add(ProblemContact, maskTail(m))
		}
	}
	lowKnown := strings.ToLower(known)
	for _, m := range emailRe.FindAllString(text, -1) {
		if !strings.Contains(lowKnown, strings.ToLower(strings.ReplaceAll(m, " ", ""))) {
			add(ProblemContact, m)
		}
	}
	for _, m := range secretRe.FindAllString(text, -1) {
		add(ProblemSecret, maskTail(m))
	}
	for _, v := range in.Never {
		v = strings.TrimSpace(v)
		if utf8.RuneCountInString(v) < 3 {
			continue
		}
		if regexp.MustCompile(`(?i)(?:^|[^\pL\pN])` + regexp.QuoteMeta(v) + `(?:[^\pL\pN]|$)`).MatchString(text) {
			add(ProblemInternal, maskTail(v))
		}
	}
	if len(probs) == 0 {
		return CheckResult{OK: true, Text: text, Problems: []Problem{}}
	}
	return CheckResult{OK: false, Text: HeldText(in.Language), Problems: probs}
}

// findLinks are the links in a text, leaving out an email's domain.
func findLinks(s string) []string {
	var out []string
	for _, ix := range urlRe.FindAllStringIndex(s, -1) {
		if ix[0] > 0 && (s[ix[0]-1] == '@' || s[ix[0]-1] == '.') {
			continue
		}
		out = append(out, s[ix[0]:ix[1]])
	}
	return out
}

// materialPrices are the amounts the material states, in kobo: every
// amount with a currency, and the plain numbers on lines about prices.
func materialPrices(material string) map[int64]bool {
	out := map[int64]bool{}
	for _, m := range moneyRe.FindAllString(material, -1) {
		if v, ok := amountValue(m); ok {
			out[v] = true
		}
	}
	for _, line := range strings.Split(material, "\n") {
		if !priceLineRe.MatchString(line) {
			continue
		}
		// Not a time (8:30) nor a phone number's parts.
		line = phoneRe.ReplaceAllString(line, " ")
		line = regexp.MustCompile(`\d{1,2}:\d{2}`).ReplaceAllString(line, " ")
		for _, m := range numberRe.FindAllString(line, -1) {
			if v, ok := amountValue(m); ok {
				out[v] = true
			}
		}
	}
	return out
}

// amountValue is an amount in kobo: its number, with 1.5k as 1,500 and 2m
// as 2,000,000.
func amountValue(m string) (int64, bool) {
	n := regexp.MustCompile(`\d[\d,]*(?:\.\d+)?`).FindString(m)
	if n == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(n, ",", ""), 64)
	if err != nil {
		return 0, false
	}
	low := strings.ToLower(strings.TrimSpace(m))
	switch {
	case regexp.MustCompile(`\d\s?k\b`).MatchString(low):
		f *= 1000
	case regexp.MustCompile(`\d\s?m\b`).MatchString(low):
		f *= 1_000_000
	}
	return int64(math.Round(f * 100)), true
}

// phoneKey is a phone number's digits as a Nigerian number reads them
// (234 803… and 0803… are one number).
func phoneKey(m string) string {
	d := onlyDigits(m)
	if strings.HasPrefix(d, "234") && len(d) == 13 {
		return "0" + d[3:]
	}
	return d
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

func maskTail(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= 4 {
		return strings.Repeat("•", len(r))
	}
	return strings.Repeat("•", len(r)-4) + string(r[len(r)-4:])
}
