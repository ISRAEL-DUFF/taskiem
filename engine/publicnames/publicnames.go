// Package publicnames decides whether a name a business shows the public
// is allowed: a storefront's slug (its address <slug>.<preview domain>),
// its display name, the title of a public form, and a domain the business
// brings (docs/spec/storefronts.md 6.6 and 7.2, docs/spec/forms.md 7.1).
//
// Pages made by businesses Taskiem has not met will be used to impersonate
// banks, payment providers, telcos, government agencies and Taskiem itself.
// So slugs that are platform words are reserved, and slugs, names and
// domains that carry a protected name are refused. Names are compared by
// their skeleton (see Skeleton), so 0pay, 0PAY and Cyrillic "оpay" all
// match opay. The lists are data in this file and are meant to grow.
//
// A refused display name is only a signal: the spec sends a business name
// that matches a protected one to review instead of refusing it outright,
// and that is the caller's decision.
package publicnames

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// The errors returned, usable with errors.Is. Each is wrapped with what
// was wrong or the name that matched.
var (
	ErrSlugFormat  = errors.New("publicnames: slug must be 3 to 40 of a-z, 0-9 and single inner hyphens")
	ErrReserved    = errors.New("publicnames: reserved name")
	ErrProtected   = errors.New("publicnames: protected name")
	ErrDisplayName = errors.New("publicnames: invalid display name")
	ErrDomain      = errors.New("publicnames: invalid domain")
)

// Slug length bounds, in characters.
const (
	MinSlugLen = 3
	MaxSlugLen = 40
)

// MaxDisplayNameLen bounds a display name, in characters.
const MaxDisplayNameLen = 80

// Reserved holds the platform words no slug may be. A slug is reserved
// when it, or its skeleton with hyphens removed, equals one of them, so
// "sign-in" and "4dmin" are reserved too. Words of fewer than three
// letters are left out, as no slug is that short.
var Reserved = []string{
	// Hosts and infrastructure.
	"www", "app", "api", "cdn", "static", "assets", "media", "files",
	"images", "edge", "gateway", "ftp", "smtp", "imap", "dns", "mail",
	"email", "webhook", "webhooks", "hooks", "embed", "health", "metrics",
	"status", "docs", "dev", "staging", "test", "preview", "internal",
	"root", "system",
	// Accounts and sign-in, the pages phishing copies.
	"admin", "login", "logout", "signin", "signup", "register", "secure",
	"security", "password", "account", "accounts", "auth", "oauth", "sso",
	"verify", "verification", "dashboard", "console", "portal",
	// Money.
	"billing", "pay", "payment", "payments", "checkout", "invoice",
	"invoices", "wallet",
	// Support and mail roles.
	"support", "help", "abuse", "postmaster", "hostmaster", "webmaster",
	"noreply",
	// The product's own surfaces and names.
	"forms", "form", "shop", "store", "taskiem", "xenibase",
}

// matchMode says how a protected name matches a slug.
type matchMode int

const (
	// affix matches the whole slug, a hyphen-delimited token, a run of
	// tokens, or a prefix or suffix of a token or of the whole slug.
	affix matchMode = iota
	// token matches only the whole slug, a token or a run of tokens.
	token
)

type protectedName struct {
	name string
	mode matchMode
}

// protected lists the names no slug, display name or domain may carry:
// Nigerian banks, fintechs and payment schemes, government agencies,
// telcos, large brands, and Taskiem's own names.
//
// The policy, for a reviewer:
//   - Distinctive names (gtbank, paystack, opay) match as affixes, so
//     gtbankng and my-opay are refused.
//   - Names of three letters or fewer (uba, cbn, glo, mtn) always match
//     only as whole tokens, so "global" and "cabana" stay allowed.
//   - Names that are common words, or begin common words, also match only
//     as whole tokens: sterling, zenith, fidelity, polaris, keystone and
//     apple refuse "zenith-motors" but not "zenithal"; firs, jamb, remita,
//     visa and verve would otherwise refuse "firstclass", "jambalaya",
//     "remittance", "visage" and "verves".
//   - Plain common words are not protected; the bank's full name is.
//     "union" and "access" are left out (unionbank and accessbank are in),
//     so "union-street-bakery" and "access-hair-salon" are allowed, and
//     "first" is not protected (firstbank is), so "firstclass-tailors" is.
//     Runs of tokens still match, so "access-bank" and "first-bank-ng" are
//     refused.
var protected = []protectedName{
	// Banks.
	{"gtbank", affix}, {"gtco", affix}, {"guarantytrust", affix},
	{"zenith", token}, {"zenithbank", affix},
	{"firstbank", affix}, {"fbn", token},
	{"uba", token},
	{"accessbank", affix},
	{"fidelity", token}, {"fidelitybank", affix},
	{"fcmb", affix},
	{"sterling", token}, {"sterlingbank", affix},
	{"wema", affix}, {"alat", token},
	{"unionbank", affix},
	{"stanbic", affix}, {"stanbicibtc", affix},
	{"ecobank", affix},
	{"polaris", token}, {"polarisbank", affix},
	{"keystone", token}, {"keystonebank", affix},
	{"providus", affix}, {"jaizbank", affix}, {"globusbank", affix},
	{"titantrust", affix}, {"heritagebank", affix}, {"unitybank", affix},
	{"citibank", affix}, {"standardchartered", affix},

	// Fintechs, payment providers and schemes.
	{"opay", affix}, {"moniepoint", affix}, {"palmpay", affix},
	{"kuda", affix}, {"paystack", affix}, {"flutterwave", affix},
	{"interswitch", affix}, {"quickteller", affix}, {"remita", token},
	{"nibss", affix}, {"paga", token}, {"fairmoney", affix},
	{"piggyvest", affix}, {"cowrywise", affix}, {"paypal", affix},
	{"payoneer", affix}, {"mastercard", affix}, {"visa", token},
	{"verve", token},

	// Government agencies.
	{"cbn", token}, {"efcc", affix}, {"icpc", affix}, {"firs", token},
	{"nimc", affix}, {"frsc", affix}, {"jamb", token}, {"npf", token},
	{"ncc", token}, {"ndic", affix}, {"nafdac", affix}, {"ndlea", affix},
	{"nnpc", affix}, {"waec", affix}, {"neco", token}, {"nipost", affix},
	{"nirsal", affix},

	// Telcos.
	{"mtn", token}, {"airtel", affix}, {"glo", token}, {"9mobile", affix},
	{"etisalat", affix},

	// Large brands.
	{"whatsapp", affix}, {"facebook", affix}, {"instagram", affix},
	{"google", affix}, {"gmail", affix}, {"apple", token}, {"icloud", affix},
	{"microsoft", affix}, {"outlook", token}, {"tiktok", affix},
	{"telegram", affix},

	// Taskiem itself.
	{"taskiem", affix}, {"xenibase", affix},
}

// skeletons are the protected and reserved names by their skeleton.
var (
	protectedSkel = func() map[string]protectedName {
		m := make(map[string]protectedName, len(protected))
		for _, p := range protected {
			if len(p.name) <= 3 {
				p.mode = token
			}
			m[squash(p.name)] = p
		}
		return m
	}()
	reservedSkel = func() map[string]string {
		m := make(map[string]string, len(Reserved))
		for _, r := range Reserved {
			m[squash(r)] = r
		}
		return m
	}()
)

// homoglyphs maps letters from other scripts that look like Latin ones,
// in both cases, and other confusable runes, to the Latin letter. Digits
// are here too. i, l, 1 and | all become l, as they are hard to tell
// apart in many fonts.
var homoglyphs = map[rune]rune{
	'0': 'o', '1': 'l', '3': 'e', '4': 'a', '5': 's', '7': 't', '8': 'b',
	'i': 'l', '|': 'l', 'ı': 'l', 'ɡ': 'g',

	// Cyrillic.
	'а': 'a', 'А': 'a', 'в': 'b', 'В': 'b', 'е': 'e', 'Е': 'e', 'ё': 'e',
	'Ё': 'e', 'к': 'k', 'К': 'k', 'м': 'm', 'М': 'm', 'н': 'h', 'Н': 'h',
	'о': 'o', 'О': 'o', 'р': 'p', 'Р': 'p', 'с': 'c', 'С': 'c', 'т': 't',
	'Т': 't', 'у': 'y', 'У': 'y', 'х': 'x', 'Х': 'x', 'і': 'l', 'І': 'l',
	'ї': 'l', 'Ї': 'l', 'ј': 'j', 'Ј': 'j', 'ѕ': 's', 'Ѕ': 's', 'һ': 'h',
	'Һ': 'h', 'ԁ': 'd', 'ԛ': 'q', 'Ԛ': 'q', 'ԝ': 'w', 'Ԝ': 'w', 'ӏ': 'l',
	'Ӏ': 'l',

	// Greek.
	'α': 'a', 'Α': 'a', 'β': 'b', 'Β': 'b', 'ε': 'e', 'Ε': 'e', 'Ζ': 'z',
	'η': 'n', 'Η': 'h', 'ι': 'l', 'Ι': 'l', 'κ': 'k', 'Κ': 'k', 'Μ': 'm',
	'ν': 'v', 'Ν': 'n', 'ο': 'o', 'Ο': 'o', 'ρ': 'p', 'Ρ': 'p', 'τ': 't',
	'Τ': 't', 'υ': 'u', 'Υ': 'y', 'χ': 'x', 'Χ': 'x', 'ω': 'w', 'γ': 'y',
}

// Skeleton normalises s for look-alike comparison. It decomposes s and
// drops accents (é is e, fullwidth Ａ is a), lowercases it, maps
// homoglyphs and digits to Latin letters, rewrites rn as m and vv as w,
// and collapses runs of the same character, so "paystaack" and "paystack"
// share a skeleton. Hyphens and other characters are kept; whole-string
// comparisons remove hyphens first. The result is for comparison only and
// is never shown.
func Skeleton(s string) string {
	var b strings.Builder
	for _, r := range norm.NFKD.String(s) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if m, ok := homoglyphs[r]; ok {
			r = m
		} else if r = unicode.ToLower(r); homoglyphs[r] != 0 {
			r = homoglyphs[r]
		}
		b.WriteRune(r)
	}
	out := strings.NewReplacer("rn", "m", "vv", "w").Replace(b.String())

	b.Reset()
	last := rune(-1)
	for _, r := range out {
		if r != last {
			b.WriteRune(r)
		}
		last = r
	}
	return b.String()
}

// squash is s's skeleton with hyphens removed, for whole-string
// comparison.
func squash(s string) string {
	return Skeleton(strings.ReplaceAll(s, "-", ""))
}

// NormalizeSlug trims and lowercases s and checks its form: 3 to 40 of
// a-z, 0-9 and hyphens, with no hyphen at either end and none doubled.
// It does not check reserved or protected names; CheckSlug does.
func NormalizeSlug(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return "", fmt.Errorf("%w: character %q", ErrSlugFormat, r)
		}
	}
	if n := len(s); n < MinSlugLen || n > MaxSlugLen {
		return "", fmt.Errorf("%w: length %d", ErrSlugFormat, n)
	}
	if s[0] == '-' || s[len(s)-1] == '-' {
		return "", fmt.Errorf("%w: starts or ends with a hyphen", ErrSlugFormat)
	}
	if strings.Contains(s, "--") {
		return "", fmt.Errorf("%w: double hyphen", ErrSlugFormat)
	}
	return s, nil
}

// CheckSlug normalises slug and refuses it if it is malformed
// (ErrSlugFormat), a reserved platform word (ErrReserved), or carries a
// protected name (ErrProtected). The error names the matched term.
func CheckSlug(slug string) error {
	s, err := NormalizeSlug(slug)
	if err != nil {
		return err
	}
	if r, ok := reservedSkel[squash(s)]; ok {
		return fmt.Errorf("%w: %q", ErrReserved, r)
	}
	if p, ok := matchProtected(s); ok {
		return fmt.Errorf("%w: %q", ErrProtected, p)
	}
	return nil
}

// matchProtected reports the protected name that the hyphenated name s
// carries, if any, by the rules of each name's mode.
func matchProtected(s string) (string, bool) {
	tokens := strings.Split(Skeleton(s), "-")
	whole := squash(s)
	if p, ok := protectedSkel[whole]; ok {
		return p.name, true
	}
	// Every run of tokens, which includes each single token.
	for i := range tokens {
		for j := i + 1; j <= len(tokens); j++ {
			if p, ok := protectedSkel[squash(strings.Join(tokens[i:j], ""))]; ok {
				return p.name, true
			}
		}
	}
	for sk, p := range protectedSkel {
		if p.mode != affix {
			continue
		}
		if strings.HasPrefix(whole, sk) || strings.HasSuffix(whole, sk) {
			return p.name, true
		}
		for _, t := range tokens {
			if strings.HasPrefix(t, sk) || strings.HasSuffix(t, sk) {
				return p.name, true
			}
		}
	}
	return "", false
}

// bidi holds the bidirectional controls and invisible characters that can
// reorder or hide part of a name.
var bidi = map[rune]bool{
	0x061C: true, 0x200B: true, 0x200E: true, 0x200F: true,
	0x202A: true, 0x202B: true, 0x202C: true, 0x202D: true,
	0x202E: true, 0x2066: true, 0x2067: true, 0x2068: true,
	0x2069: true, 0xFEFF: true,
}

// Links in a display name: a scheme, www., an email address, or a word
// ending in a common top-level domain.
var (
	urlRe   = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://|\bwww\.|\b[a-z0-9-]+\.(com|net|org|ng|io|co|app|dev|xyz|info|biz|me|shop|store|online|site|link|ly|to)\b)`)
	emailRe = regexp.MustCompile(`[^\s@]+@[^\s@]+\.[^\s@]+`)
)

// CheckDisplayName checks a business's display name or a form's title: 1
// to 80 characters after trimming, valid UTF-8, with no control, bidi or
// invisible characters and no URL or email address (ErrDisplayName). It
// refuses a name with a protected name as a whole word, or as two or three
// adjacent words run together, such as "GT Bank" (ErrProtected).
func CheckDisplayName(name string) error {
	if !utf8.ValidString(name) {
		return fmt.Errorf("%w: not valid UTF-8", ErrDisplayName)
	}
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > MaxDisplayNameLen {
		return fmt.Errorf("%w: length %d", ErrDisplayName, n)
	}
	for _, r := range name {
		if unicode.IsControl(r) || bidi[r] {
			return fmt.Errorf("%w: character %U", ErrDisplayName, r)
		}
	}
	if urlRe.MatchString(name) || emailRe.MatchString(name) {
		return fmt.Errorf("%w: contains a link or email address", ErrDisplayName)
	}
	words := strings.FieldsFunc(Skeleton(name), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for i := range words {
		for j := i + 1; j <= len(words) && j <= i+3; j++ {
			if p, ok := protectedSkel[Skeleton(strings.Join(words[i:j], ""))]; ok {
				return fmt.Errorf("%w: %q", ErrProtected, p.name)
			}
		}
	}
	return nil
}

// multiSuffixes are the public suffixes of two labels that matter here.
// Any other domain's suffix is its last label.
var multiSuffixes = map[string]bool{
	"com.ng": true, "org.ng": true, "gov.ng": true, "edu.ng": true,
	"net.ng": true, "name.ng": true, "sch.ng": true, "mil.ng": true,
	"co.uk": true, "com.gh": true, "co.ke": true, "co.za": true,
}

// publicSuffix returns the number of trailing labels that are the public
// suffix of a domain split into labels.
func publicSuffix(labels []string) int {
	if n := len(labels); n >= 2 && multiSuffixes[labels[n-2]+"."+labels[n-1]] {
		return 2
	}
	return 1
}

// CheckDomain lowercases domain, drops a trailing dot and returns it if a
// business may bring it. It must be a hostname of at least two labels of
// a-z, 0-9 and inner hyphens, not an IP address, localhost, punycode or a
// Nigerian government or military domain, and with something before its
// public suffix (ErrDomain). It is refused if any label before the suffix,
// the registrable label or a subdomain, carries a protected name
// (ErrProtected); a subdomain like gtbank.example.com impersonates as well
// as a registrable one.
func CheckDomain(domain string) (string, error) {
	d := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	if d == "" || len(d) > 253 {
		return "", fmt.Errorf("%w: length %d", ErrDomain, len(d))
	}
	if net.ParseIP(d) != nil {
		return "", fmt.Errorf("%w: IP address", ErrDomain)
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("%w: needs at least two labels", ErrDomain)
	}
	for _, l := range labels {
		if err := checkLabel(l); err != nil {
			return "", err
		}
	}
	tld := labels[len(labels)-1]
	if strings.Trim(tld, "0123456789") == "" {
		return "", fmt.Errorf("%w: numeric top-level label", ErrDomain)
	}
	if tld == "localhost" || tld == "local" {
		return "", fmt.Errorf("%w: local name", ErrDomain)
	}
	n := publicSuffix(labels)
	suffix := strings.Join(labels[len(labels)-n:], ".")
	if suffix == "gov.ng" || suffix == "mil.ng" {
		return "", fmt.Errorf("%w: government domain", ErrDomain)
	}
	if len(labels) == n {
		return "", fmt.Errorf("%w: public suffix only", ErrDomain)
	}
	for _, l := range labels[:len(labels)-n] {
		if p, ok := matchProtected(l); ok {
			return "", fmt.Errorf("%w: %q", ErrProtected, p)
		}
	}
	return d, nil
}

// checkLabel checks one label of a hostname.
func checkLabel(l string) error {
	if l == "" || len(l) > 63 {
		return fmt.Errorf("%w: label length %d", ErrDomain, len(l))
	}
	if strings.HasPrefix(l, "xn--") {
		return fmt.Errorf("%w: punycode label %q", ErrDomain, l)
	}
	if l[0] == '-' || l[len(l)-1] == '-' {
		return fmt.Errorf("%w: label %q starts or ends with a hyphen", ErrDomain, l)
	}
	for _, r := range l {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return fmt.Errorf("%w: character %q", ErrDomain, r)
		}
	}
	return nil
}
