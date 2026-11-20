package api

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/saved"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// A question about a saved thing (saved objects S2; §6.3): "what's
// @generator's last service date?", "when is @car insurance due?",
// "@mum's phone?" (with its question word: "what is @mum's phone"). The
// field is found by its label, without the AI: a made-up kind's own
// labels, or the words a known kind's fields go by; the answer is that
// field, a sensitive one masked (in full only on the Saved page, with a
// passkey). No field named: the thing told whole, as @name alone is.
//
// The order of reading (build plan §1, fixed in S2): Taskiem's own
// commands first, then a saved thing's @name, then (later) tools. A
// question about a saved thing starts with its question word, so it is
// read before the words to a linked business ("@Dangote how much…", which
// starts with the business), and an @name no saved thing has passes on.

var savedQuestion = regexp.MustCompile(`(?i)^\s*(?:what(?:'s|’s| is| are| was)?|when(?:'s|’s| is| was| does| do)?|where(?:'s|’s| is)?|which|who(?:'s|’s| is)?|whose|how|is|does|do|show(?: me)?|tell me|give me)\b.*@([a-z0-9][a-z0-9_-]{0,31})`)

// savedFieldWords are the words a known kind's fields go by, each with
// the detail it names; longer phrases first, so "account number" is not
// read as "number".
var savedFieldWords = []struct{ words, key string }{
	{"account number", "account_number"}, {"account name", "account_name"},
	{"name on it", "name_on_it"}, {"name on the account", "name_on_it"},
	{"phone number", "phone"}, {"phone", "phone"}, {"number to call", "phone"},
	{"email address", "email"}, {"email", "email"},
	{"birthday", "birthday"}, {"date of birth", "birthday"},
	{"landmark", "landmark"}, {"street", "line"}, {"area", "area"},
	{"address", "address"}, {"where", "address"},
	{"bank", "bank_name"}, {"provider", "provider"},
	{"meter", "number"}, {"smartcard", "number"}, {"number", "number"},
	{"note", "note"},
}

// waSavedQuestion answers a question about a saved thing; false when the
// message is none, or names no saved thing.
func (s *Server) waSavedQuestion(ctx context.Context, c *chat, text string) (bool, error) {
	m := savedQuestion.FindStringSubmatch(text)
	if m == nil || c.p == nil || c.p.UserID == uuid.Nil {
		return false, nil
	}
	name, err := saved.Name(m[1])
	if err != nil {
		return false, nil //nolint:nilerr // not a saved thing's name: not this
	}
	got, err := s.savedFind(ctx, c, name)
	if err != nil || len(got) == 0 {
		return err != nil, err
	}
	// The words about the thing: what follows its name, and before it.
	asked := strings.ToLower(text)
	return true, s.sayFound(ctx, c, name, got, func(f savedFound) string {
		label, value, ok := savedFieldAsked(f.row, asked)
		switch {
		case !ok:
			return s.savedLines(c, f.row)
		case value == "":
			return c.t("wa.saved.field_empty", "name", f.row.Name, "label", label)
		}
		said := c.t("wa.saved.field", "name", f.row.Name, "label", label, "value", whatsapp.SafeText(value))
		if strings.Contains(value, "•") {
			said += "\n" + c.t("wa.saved.in_full")
		}
		return said
	})
}

// savedFieldAsked is the field of x the words ask about: its label and its
// value as the chat shows it (a sensitive one masked; "" when it holds
// none); ok false when they name no field of it.
func savedFieldAsked(x savedRow, asked string) (label, value string, ok bool) {
	str := func(k string) string { v, _ := x.Details[k].(string); return v }
	if x.Kind == saved.KindCustom {
		// The longest label the words hold: "last service date" before
		// "service".
		fields := append([]saved.Field(nil), x.Fields...)
		sort.SliceStable(fields, func(i, j int) bool { return len(fields[i].Label) > len(fields[j].Label) })
		values := x.view(savedOwner{}, false).Values
		for _, f := range fields {
			if !labelAsked(asked, f.Label) {
				continue
			}
			v := values[f.Key]
			if v != "" && !f.Sensitive {
				v = saved.ShownValue(f, v)
			}
			return f.Label, v, true
		}
		return "", "", false
	}
	for _, w := range savedFieldWords {
		if !labelAsked(asked, w.words) {
			continue
		}
		label := strings.ToUpper(w.words[:1]) + w.words[1:]
		switch w.key {
		case "account_number":
			if x.Kind != saved.KindBankAccount {
				continue
			}
			if l4 := str("last4"); l4 != "" {
				return label, "••" + l4, true
			}
			return label, "", true
		case "account_name":
			if x.Kind != saved.KindBankAccount {
				continue
			}
			if x.CheckedName != nil {
				return label, *x.CheckedName, true
			}
			return label, "", true
		case "address":
			// A place's address is its street and area; a person's their
			// address.
			if x.Kind == saved.KindPlace {
				return "Address", strings.Trim(strings.Join([]string{str("line"), str("area")}, ", "), ", "), true
			}
		case "number":
			// A person's or a place's number is its phone.
			switch x.Kind {
			case saved.KindProviderAccount:
			case saved.KindPerson, saved.KindPlace:
				return "Phone", savedChatValue("phone", str("phone")), true
			default:
				continue
			}
		}
		if !savedKindHas(x.Kind, w.key) {
			continue
		}
		return label, savedChatValue(w.key, str(w.key)), true
	}
	return "", "", false
}

// savedChatValue is a detail as the chat may say it: a phone or an email
// masked, since nothing Taskiem sends carries one in full (SafeText); the
// Saved page shows them.
func savedChatValue(key, v string) string {
	switch {
	case v == "":
		return ""
	case key == "phone":
		return saved.MaskValue(v)
	case key == "email":
		// "m••• at home.ng": an address SafeText would take for one is
		// sent as [email].
		if at := strings.LastIndex(v, "@"); at > 0 {
			return strings.Replace(saved.MaskEmail(v), "@", " at ", 1)
		}
		return "•••"
	}
	return v
}

// savedKindHas says whether a known kind has the detail key.
func savedKindHas(kind, key string) bool {
	has := map[string][]string{
		saved.KindPerson:          {"phone", "email", "address", "birthday", "note"},
		saved.KindPlace:           {"line", "area", "landmark", "phone", "note"},
		saved.KindBankAccount:     {"bank_name", "note"},
		saved.KindProviderAccount: {"provider", "number", "name_on_it", "note"},
		saved.KindText:            {"note"},
		saved.KindFile:            {"note"},
	}
	for _, k := range has[kind] {
		if k == key {
			return true
		}
	}
	return false
}

// labelAsked says whether the words hold a label as whole words, its
// spaces as any spacing: "last service" in "@generator's last  service?".
func labelAsked(asked, label string) bool {
	words := strings.Fields(strings.ToLower(label))
	if len(words) == 0 {
		return false
	}
	for i, w := range words {
		words[i] = regexp.QuoteMeta(w)
	}
	return regexp.MustCompile(`(^|[^a-z0-9])` + strings.Join(words, `\s+`) + `([^a-z0-9]|$)`).MatchString(asked)
}
