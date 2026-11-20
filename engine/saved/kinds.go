package saved

import (
	"fmt"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	phonenum "github.com/israel-duff/taskiem/engine/phone"
)

// The kinds other than the bank account (saved-objects §4.1). None of their
// fields is sealed in S1: a phone or an email is the owner's to mark
// sensitive, which comes with the kinds an owner makes. Their details are
// kept as they are cleaned here, and Hint is what a list shows.

// Field limits.
const (
	// MaxText is the longest text object (§8).
	MaxText = 4000
	// MaxLine is the longest line of an address, a provider's name and the
	// like.
	MaxLine = 200
	// MaxNumber is the longest number at a provider (a meter, a smartcard).
	MaxNumber = 30
)

// ProviderTypes are what a provider an account is with may be, in the order
// a form offers them (§4.1: an electricity company, DSTV, a phone network, a
// utility, a school).
var ProviderTypes = []string{"electricity", "tv", "phone", "internet", "water", "school", "other"}

// providerWords are the provider types as a list shows them.
var providerWords = map[string]string{
	"electricity": "Electricity", "tv": "TV", "phone": "Phone", "internet": "Internet", "water": "Water", "school": "School", "other": "Account",
}

// Cleaned is an object of a kind other than the bank account, made ready
// to keep: its details and its hint.
type Cleaned struct {
	Details map[string]any
	Hint    string
}

// field reads a text field of in: spaces folded (the line breaks of a text
// kept), up to max characters; required ones refused when empty.
func field(in map[string]any, key, what string, max int, required bool) (string, error) {
	v, ok := in[key]
	if !ok || v == nil {
		if required {
			return "", invalid("Give it " + what + ".")
		}
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", invalid("The " + what + " is words.")
	}
	s = strings.Join(strings.Fields(s), " ")
	switch {
	case required && s == "":
		return "", invalid("Give it " + what + ".")
	case utf8.RuneCountInString(s) > max:
		return "", invalid("The " + what + " is too long.")
	}
	return s, nil
}

// put sets key in d when v is not empty.
func put(d map[string]any, key, v string) {
	if v != "" {
		d[key] = v
	}
}

var day = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// Clean checks the details of an object of kind (other than the bank
// account), refusing what breaks a rule, in plain words. Unknown fields are
// left out.
func Clean(kind string, in map[string]any) (Cleaned, error) {
	if in == nil {
		in = map[string]any{}
	}
	d := map[string]any{}
	note, err := field(in, "note", "note", MaxNote, false)
	if err != nil {
		return Cleaned{}, err
	}
	put(d, "note", note)
	switch kind {
	case KindFile:
		// The file itself is kept apart (the API's saved_files): here, what
		// it is, as the API found it when it was uploaded.
		f, err := FileOf(in)
		if err != nil {
			return Cleaned{}, err
		}
		d["file"], d["file_name"], d["file_type"], d["file_size"] = f.ID, f.Name, f.Type, f.Size
		return Cleaned{Details: d, Hint: clip(f.Hint(), 120)}, nil
	case KindPerson:
		ph, err := phone(in, "phone", "phone number")
		if err != nil {
			return Cleaned{}, err
		}
		email, err := field(in, "email", "email", 254, false)
		if err != nil {
			return Cleaned{}, err
		}
		if email != "" {
			if a, err := mail.ParseAddress(email); err != nil || a.Address != email {
				return Cleaned{}, invalid("That email does not look right.")
			}
		}
		address, err := field(in, "address", "address", MaxLine, false)
		if err != nil {
			return Cleaned{}, err
		}
		birthday, err := field(in, "birthday", "birthday", 10, false)
		if err != nil {
			return Cleaned{}, err
		}
		if birthday != "" {
			if t, err := time.Parse(time.DateOnly, birthday); !day.MatchString(birthday) || err != nil || t.After(time.Now()) {
				return Cleaned{}, invalid("A birthday is a day, like 1970-03-21.")
			}
		}
		put(d, "phone", ph)
		put(d, "email", email)
		put(d, "address", address)
		put(d, "birthday", birthday)
		return Cleaned{Details: d, Hint: firstOf(ph, email, address, "A person")}, nil
	case KindPlace:
		line, err := field(in, "line", "street and number", MaxLine, true)
		if err != nil {
			return Cleaned{}, err
		}
		area, err := field(in, "area", "area", 80, false)
		if err != nil {
			return Cleaned{}, err
		}
		landmark, err := field(in, "landmark", "landmark", MaxLine, false)
		if err != nil {
			return Cleaned{}, err
		}
		ph, err := phone(in, "phone", "phone for the rider")
		if err != nil {
			return Cleaned{}, err
		}
		put(d, "line", line)
		put(d, "area", area)
		put(d, "landmark", landmark)
		put(d, "phone", ph)
		hint := line
		if area != "" {
			hint = area
		}
		return Cleaned{Details: d, Hint: clip(hint, 120)}, nil
	case KindText:
		body, err := textBody(in)
		if err != nil {
			return Cleaned{}, err
		}
		d["body"] = body
		lines := strings.Count(body, "\n") + 1
		hint := "1 line of text"
		if lines > 1 {
			hint = fmt.Sprintf("%d lines of text", lines)
		}
		return Cleaned{Details: d, Hint: hint}, nil
	case KindProviderAccount:
		typ, err := field(in, "type", "kind of provider", 20, true)
		if err != nil {
			return Cleaned{}, err
		}
		if _, ok := providerWords[typ]; !ok {
			return Cleaned{}, invalid("Choose what the provider is: electricity, TV, phone, internet, water, a school or another.")
		}
		provider, err := field(in, "provider", "provider's name, like Ikeja Electric or DSTV", 80, true)
		if err != nil {
			return Cleaned{}, err
		}
		number, err := field(in, "number", "number at the provider (a meter, a smartcard, a phone or a student number)", MaxNumber, true)
		if err != nil {
			return Cleaned{}, err
		}
		name, err := field(in, "name_on_it", "name on the account", 120, false)
		if err != nil {
			return Cleaned{}, err
		}
		put(d, "type", typ)
		put(d, "provider", provider)
		put(d, "number", number)
		put(d, "name_on_it", name)
		return Cleaned{Details: d, Hint: clip(provider+" "+number, 120)}, nil
	}
	return Cleaned{}, invalid("That is not a kind of saved thing.")
}

// textBody is a text object's words: each line's spaces folded, blank
// lines at the ends dropped, up to MaxText characters.
func textBody(in map[string]any) (string, error) {
	v, _ := in["body"].(string)
	lines := strings.Split(strings.ReplaceAll(v, "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.Join(strings.Fields(l), " ")
	}
	body := strings.Trim(strings.Join(lines, "\n"), "\n")
	switch {
	case body == "":
		return "", invalid("Write the text.")
	case utf8.RuneCountInString(body) > MaxText:
		return "", invalid(fmt.Sprintf("A text is up to %d characters.", MaxText))
	}
	return body, nil
}

// phone is an optional phone number, as E.164.
func phone(in map[string]any, key, what string) (string, error) {
	s, err := field(in, key, what, 30, false)
	if err != nil || s == "" {
		return "", err
	}
	n, err := phonenum.Normalise(s)
	if err != nil {
		return "", invalid("The " + what + " does not look like a phone number.")
	}
	return n, nil
}

func firstOf(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return clip(x, 120)
		}
	}
	return ""
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// ProviderWord is a provider type as a list shows it.
func ProviderWord(typ string) string { return providerWords[typ] }

// FileMeta is a saved file as lists and cards name it.
type FileMeta struct {
	ID   string `json:"file"`
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

// FileOf is a file object's details (file, file_name, file_type,
// file_size).
func FileOf(in map[string]any) (FileMeta, error) {
	id, _ := in["file"].(string)
	if _, err := uuid.Parse(id); err != nil {
		return FileMeta{}, invalid("Choose the photo or document to save.")
	}
	f := FileMeta{ID: id}
	f.Name, _ = in["file_name"].(string)
	f.Type, _ = in["file_type"].(string)
	switch n := in["file_size"].(type) {
	case float64:
		f.Size = int64(n)
	case int64:
		f.Size = n
	case int:
		f.Size = int64(n)
	}
	return f, nil
}

// Hint is what a list shows for a file: "PDF · 240 KB", "Photo · 1.2 MB".
func (f FileMeta) Hint() string {
	what := "File"
	switch {
	case strings.HasPrefix(f.Type, "image/"):
		what = "Photo"
	case f.Type == "application/pdf":
		what = "PDF"
	}
	return what + " · " + SizeWords(f.Size)
}

// SizeWords is a size as people read it: 240 KB, 1.2 MB.
func SizeWords(n int64) string {
	switch {
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " MB"
	case n >= 1<<10:
		return strconv.FormatInt((n+512)>>10, 10) + " KB"
	}
	return strconv.FormatInt(n, 10) + " bytes"
}
