// Package pii seals declared personal data in run history (spec 4.9, 9.3).
// A sealed value is an envelope encrypted under its data subject's own key,
// so destroying that key erases the subject from every event, archive, and
// backup at once (crypto-shredding, spec 9.4).
package pii

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Erased replaces a value whose subject key was destroyed.
const Erased = "[erased]"

// ErrErased means a subject has been erased.
var ErrErased = errors.New("subject erased")

// Cipher seals and opens values inside the caller's tenant transaction (no
// second connection is taken). secrets.Vault implements it.
type Cipher interface {
	SealTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, category string, value any) (map[string]any, error)
	OpenTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, envelope map[string]any) (any, error)
}

// IsEnvelope reports whether v is a sealed value.
func IsEnvelope(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, a := m["$pii"]
	_, b := m["ct"]
	return a && b
}

// Path is a location in a JSON value: object keys and "*" for every array
// element, with the PII category found there.
type Path struct {
	Segments []string
	Category string
	// Owner, for free text ("other"), is the location of the person it
	// belongs to (an email's sender), from the value's root, and
	// OwnerCategory that person's category: the text is sealed under
	// their subject, so erasing them erases it, and it adds no subject
	// key of its own.
	Owner         []string
	OwnerCategory string
}

// owner is a free text's owner: a personal value and its category.
type owner struct {
	cat   string
	value any
}

// valueAt reads the scalar at segs (no "*") in v.
func valueAt(v any, segs []string) (any, bool) {
	for _, s := range segs {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = m[s]; !ok {
			return nil, false
		}
	}
	_, ok := scalarKey(v)
	return v, ok
}

// Unattributed is the value whose subject every "other" value is sealed
// under (secrets.Vault): free text is no one's, so a subject derived from
// the text itself is one nobody can name for erasure, and one a text grew
// subject_keys without bound (F62b). Text that names known people or has
// an owner is sealed under them as well. The value is no one's: it starts
// with a NUL byte.
const Unattributed = "\x00taskiem:unattributed text"

// SchemaPaths finds fields marked "x-pii": "<category>" in a JSON Schema,
// resolving local $ref against defs (the WD's types).
func SchemaPaths(schema any, defs map[string]any) []Path {
	var out []Path
	var walk func(s any, prefix []string, depth int)
	walk = func(s any, prefix []string, depth int) {
		m, ok := s.(map[string]any)
		if !ok || depth > 32 {
			return
		}
		if ref, ok := m["$ref"].(string); ok {
			if name, ok := strings.CutPrefix(ref, "#/types/"); ok {
				walk(defs[name], prefix, depth+1)
			}
			return
		}
		if cat, ok := m["x-pii"].(string); ok && cat != "" {
			out = append(out, Path{Segments: append([]string(nil), prefix...), Category: cat})
		}
		if props, ok := m["properties"].(map[string]any); ok {
			for k, sub := range props {
				walk(sub, append(prefix, k), depth+1)
			}
		}
		if items, ok := m["items"]; ok {
			walk(items, append(prefix, "*"), depth+1)
		}
	}
	walk(schema, nil, 0)
	return out
}

// Taint records plaintext values known to be personal data in a run, so
// copies of them elsewhere (an approval subject, a transform) are sealed too.
type Taint map[string]string // canonical scalar -> category

func scalarKey(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		if t == "" {
			return "", false
		}
		return "s:" + t, true
	case int64:
		return "n:" + strconv.FormatInt(t, 10), true
	case float64:
		return "n:" + strconv.FormatFloat(t, 'g', -1, 64), true
	}
	return "", false
}

// Add records a personal value.
func (t Taint) Add(v any, category string) {
	if k, ok := scalarKey(v); ok {
		t[k] = category
	}
}

// MinRedact is the shortest personal value RedactText looks for: shorter
// strings ("Ada", a two-digit number) would mask unrelated text.
const MinRedact = 5

// RedactText replaces, in free text such as a provider's error message,
// every exact occurrence of a personal value known in the run with its
// category ("[name]", "[bvn]"). It matches whole values only, never parts
// or look-alikes, so it never masks text that is not a known value; values
// shorter than MinRedact are left to the pattern masks in Redact
// (self-review S22).
func (t Taint) RedactText(s string) string {
	if len(t) == 0 || s == "" {
		return s
	}
	type hit struct{ val, cat string }
	var hits []hit
	for k, cat := range t {
		v := k[2:] // "s:" or "n:" and the canonical value
		if len(v) < MinRedact || !strings.Contains(s, v) {
			continue
		}
		hits = append(hits, hit{v, cat})
	}
	if len(hits) == 0 {
		return s
	}
	// Longest first, so a value that contains another is masked whole.
	sort.Slice(hits, func(i, j int) bool {
		if len(hits[i].val) != len(hits[j].val) {
			return len(hits[i].val) > len(hits[j].val)
		}
		return hits[i].val < hits[j].val
	})
	pairs := make([]string, 0, 2*len(hits))
	for _, h := range hits {
		cat := h.cat
		if cat == "" {
			cat = "personal data"
		}
		pairs = append(pairs, h.val, "["+cat+"]")
	}
	return strings.NewReplacer(pairs...).Replace(s)
}

// Within reports whether free text contains a known personal value (at
// least MinRedact long), and its category: text built from personal data,
// such as an AI step's prompt, is sealed whole.
func (t Taint) Within(s string) (string, bool) {
	if len(t) == 0 || len(s) < MinRedact {
		return "", false
	}
	best, cat := "", ""
	for k, c := range t {
		v := k[2:]
		if len(v) >= MinRedact && len(v) > len(best) && strings.Contains(s, v) {
			best, cat = v, c
		}
	}
	if best == "" {
		return "", false
	}
	if cat == "" {
		cat = "other"
	}
	return cat, true
}

// Seal replaces values at the declared paths, and anywhere a tainted value
// appears, with envelopes. It returns the new value; v is not modified.
func Seal(ctx context.Context, c Cipher, tx pgx.Tx, tenant uuid.UUID, v any, paths []Path, taint Taint) (any, error) {
	if c == nil {
		return v, nil
	}
	v = deepCopy(v)
	// Free text last, so the people it may name are known (taint) by then.
	paths = slices.Clone(paths)
	slices.SortStableFunc(paths, func(a, b Path) int {
		return cmp.Compare(boolInt(a.Category == "other"), boolInt(b.Category == "other"))
	})
	// Owners are read before anything is sealed (an owner is usually
	// sealed by an earlier path).
	owners := make([]*owner, len(paths))
	for i, p := range paths {
		if len(p.Owner) == 0 {
			continue
		}
		if ov, ok := valueAt(v, p.Owner); ok && !IsEnvelope(ov) {
			owners[i] = &owner{cat: p.OwnerCategory, value: ov}
		}
	}
	for i, p := range paths {
		var err error
		v, err = sealAt(ctx, c, tx, tenant, v, p.Segments, p.Category, owners[i], taint)
		if err != nil {
			return nil, err
		}
	}
	if len(taint) == 0 {
		return v, nil
	}
	return sealTainted(ctx, c, tx, tenant, v, taint)
}

func sealAt(ctx context.Context, c Cipher, tx pgx.Tx, tenant uuid.UUID, v any, segs []string, cat string, own *owner, taint Taint) (any, error) {
	if len(segs) == 0 {
		if v == nil || IsEnvelope(v) {
			return v, nil
		}
		if list, ok := v.([]any); ok {
			// A declared list of values (recipients' numbers): each is
			// sealed; an object in it, every value under it. A list too
			// large to seal value by value is sealed whole (sealObject).
			if leaves(list, MaxSealedLeaves+1) > MaxSealedLeaves {
				return sealWhole(ctx, c, tx, tenant, list, cat, taint)
			}
			for i := range list {
				s, err := sealAt(ctx, c, tx, tenant, list[i], nil, cat, own, taint)
				if err != nil {
					return nil, err
				}
				list[i] = s
			}
			return list, nil
		}
		if m, ok := v.(map[string]any); ok {
			// A declared object (a provider's identity record): every
			// value under it is sealed. Declaring the place used to seal
			// nothing, and the whole record was stored in the clear.
			return sealObject(ctx, c, tx, tenant, m, cat, own, taint)
		}
		if _, ok := scalarKey(v); !ok {
			return v, nil
		}
		return sealScalar(ctx, c, tx, tenant, v, cat, own, taint)
	}
	switch t := v.(type) {
	case map[string]any:
		child, ok := t[segs[0]]
		if !ok {
			return v, nil
		}
		s, err := sealAt(ctx, c, tx, tenant, child, segs[1:], cat, own, taint)
		if err != nil {
			return nil, err
		}
		t[segs[0]] = s
	case []any:
		if segs[0] != "*" {
			return v, nil
		}
		for i := range t {
			s, err := sealAt(ctx, c, tx, tenant, t[i], segs[1:], cat, own, taint)
			if err != nil {
				return nil, err
			}
			t[i] = s
		}
	}
	return v, nil
}

// maxSealDepth bounds how deep a declared object is walked.
const maxSealDepth = 64

// MaxSealedLeaves is how many values under one declared object or list
// are sealed each on their own. Every envelope is a key lookup and some
// 150 bytes that do not compress, so a listing of hundreds of records
// sealed value by value would be thousands of lookups in the event's
// transaction and an event several times its size. Beyond it the place
// is sealed whole, as one envelope: as unreadable, opened to the same
// value, without the shape.
const MaxSealedLeaves = 512

// leaves counts the scalar values under v, stopping once past limit.
func leaves(v any, limit int) int {
	switch t := v.(type) {
	case nil:
		return 0
	case map[string]any:
		if IsEnvelope(t) {
			return 0
		}
		n := 0
		for _, x := range t {
			if n += leaves(x, limit-n); n > limit {
				break
			}
		}
		return n
	case []any:
		n := 0
		for _, x := range t {
			if n += leaves(x, limit-n); n > limit {
				break
			}
		}
		return n
	case string:
		if t == "" {
			return 0
		}
	}
	return 1
}

// sealObject seals a declared object: every value under it on its own
// (sealUnder), or, past MaxSealedLeaves, the object whole.
func sealObject(ctx context.Context, c Cipher, tx pgx.Tx, tenant uuid.UUID, m map[string]any, cat string, own *owner, taint Taint) (any, error) {
	if leaves(m, MaxSealedLeaves+1) > MaxSealedLeaves {
		return sealWhole(ctx, c, tx, tenant, m, cat, taint)
	}
	return sealUnder(ctx, c, tx, tenant, m, cat, own, taint, 0)
}

// sealWhole seals a large declared object or list as one envelope under
// the category's subject (for free text, the organisation's). Its text
// values are remembered, here and whenever it is opened, so copies of them
// elsewhere in the run are sealed as for a place sealed value by value.
func sealWhole(ctx context.Context, c Cipher, tx pgx.Tx, tenant uuid.UUID, v any, cat string, taint Taint) (any, error) {
	if cat != "other" {
		// A subject is one person's value; a whole object has none.
		cat = "other"
	}
	env, err := c.SealTx(ctx, tx, tenant, cat, v)
	if errors.Is(err, ErrErased) {
		return Erased, nil
	}
	if err != nil {
		return nil, err
	}
	followStrings(v, cat, taint)
	return env, nil
}

// sealUnder seals every scalar leaf under a declared object or a list in
// one, each as the declared category (so each under the subject a value
// of that category has: its own, or, for free text, its owner's, the
// people it names or the organisation's). The object keeps its shape and
// its keys, so what reads a field of it finds an envelope there, opens it
// like any other, and erasure and replay work leaf by leaf as they do for
// a declared scalar. Strings are remembered (taint), so copies elsewhere
// in the run are sealed too; numbers and booleans are sealed but not
// remembered, since a record's 1, 34 or true would seal every equal
// value in the run.
func sealUnder(ctx context.Context, c Cipher, tx pgx.Tx, tenant uuid.UUID, v any, cat string, own *owner, taint Taint, depth int) (any, error) {
	if depth > maxSealDepth {
		return nil, errors.New("pii: a declared object is nested too deeply to seal")
	}
	switch t := v.(type) {
	case nil:
		return v, nil
	case map[string]any:
		if IsEnvelope(t) {
			return t, nil
		}
		for k, x := range t {
			s, err := sealUnder(ctx, c, tx, tenant, x, cat, own, taint, depth+1)
			if err != nil {
				return nil, err
			}
			t[k] = s
		}
		return t, nil
	case []any:
		for i, x := range t {
			s, err := sealUnder(ctx, c, tx, tenant, x, cat, own, taint, depth+1)
			if err != nil {
				return nil, err
			}
			t[i] = s
		}
		return t, nil
	case string:
		if t == "" {
			return v, nil
		}
		return sealScalar(ctx, c, tx, tenant, v, cat, own, taint)
	}
	// A number, a boolean: sealed, not remembered. The envelope says so
	// (NoFollow), so opening it later does not remember the value either:
	// what is followed is the same whenever the history is read.
	env, err := c.SealTx(ctx, tx, tenant, cat, v)
	if errors.Is(err, ErrErased) {
		return Erased, nil
	}
	if err != nil {
		return nil, err
	}
	env[NoFollow] = true
	return env, nil
}

// NoFollow marks an envelope whose value is not remembered when it is
// opened: a number or boolean under a declared object.
const NoFollow = "nofollow"

// followStrings remembers every text value under v (the content of a
// place sealed whole), as sealing them one by one would have.
func followStrings(v any, cat string, taint Taint) {
	switch t := v.(type) {
	case map[string]any:
		for _, x := range t {
			followStrings(x, cat, taint)
		}
	case []any:
		for _, x := range t {
			followStrings(x, cat, taint)
		}
	case string:
		taint.Add(t, cat)
	}
}

func sealTainted(ctx context.Context, c Cipher, tx pgx.Tx, tenant uuid.UUID, v any, taint Taint) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		if IsEnvelope(t) {
			return t, nil
		}
		for k, x := range t {
			s, err := sealTainted(ctx, c, tx, tenant, x, taint)
			if err != nil {
				return nil, err
			}
			t[k] = s
		}
		return t, nil
	case []any:
		for i, x := range t {
			s, err := sealTainted(ctx, c, tx, tenant, x, taint)
			if err != nil {
				return nil, err
			}
			t[i] = s
		}
		return t, nil
	}
	if k, ok := scalarKey(v); ok {
		if cat, hit := taint[k]; hit {
			return sealScalar(ctx, c, tx, tenant, v, cat, nil, taint)
		}
	}
	if str, ok := v.(string); ok {
		return sealDerived(ctx, c, tx, tenant, str, taint)
	}
	return v, nil
}

// sealScalar seals a declared (or copied) value under its own subject,
// and, when it is text naming other known people (an email's body with
// its sender's address), under each of them too, as sealDerived does: so
// erasing any of them erases it, and the text is not left under a subject
// derived from itself that nobody can name. Free text with an owner (an
// email's sender) is sealed under the owner and anyone it names; free text
// naming no one, without an owner, under the tenant's one subject for
// "other" (Unattributed).
func sealScalar(ctx context.Context, c Cipher, tx pgx.Tx, tenant uuid.UUID, v any, cat string, own *owner, taint Taint) (any, error) {
	if str, ok := v.(string); ok && cat == "other" {
		if ss, ok := c.(SubjectSealer); ok && own != nil {
			return sealOwned(ctx, c, ss, tx, tenant, str, own, taint)
		}
		// Free text's own subject would be one nobody can name: text that
		// names known people is sealed under them alone.
		d, err := sealDerived(ctx, c, tx, tenant, str, taint)
		if err != nil {
			return nil, err
		}
		if d, ok := d.(map[string]any); ok {
			taint.Add(v, cat)
			return d, nil
		}
		if d == Erased {
			return d, nil
		}
	}
	env, err := c.SealTx(ctx, tx, tenant, cat, v)
	if err != nil {
		return nil, err
	}
	str, ok := v.(string)
	taint.Add(v, cat)
	if !ok {
		return env, nil
	}
	self, _ := env["subject"].(string)
	return nestUnder(ctx, c, tx, tenant, str, env, self, taint)
}

// sealOwned seals free text under its owner's subject (made if new: the
// owner's value is sealed), then under each known person it names.
func sealOwned(ctx context.Context, c Cipher, ss SubjectSealer, tx pgx.Tx, tenant uuid.UUID, text string, own *owner, taint Taint) (any, error) {
	oenv, err := c.SealTx(ctx, tx, tenant, own.cat, own.value)
	if errors.Is(err, ErrErased) {
		return Erased, nil
	}
	if err != nil {
		return nil, err
	}
	subject, _ := oenv["subject"].(string)
	env, err := ss.SealUnderTx(ctx, tx, tenant, subject, "other", text)
	if err != nil {
		return nil, err
	}
	taint.Add(text, "other")
	return nestUnder(ctx, c, tx, tenant, text, env, subject, taint)
}

// SealText seals free text under subject, the person it belongs to (the
// member who wrote or was sent a message), and under the subject of each
// personal value found in it word by word (a phone number, an email, a
// BVN), so erasing its owner or anyone it names erases it. One owner is
// one subject key, however many texts they have.
func SealText(ctx context.Context, c Cipher, tx pgx.Tx, tenant uuid.UUID, subject, category, text string) (any, error) {
	ss, ok := c.(SubjectSealer)
	if !ok {
		return nil, errors.New("pii: this cipher cannot seal under a subject")
	}
	env, err := ss.SealUnderTx(ctx, tx, tenant, subject, category, text)
	if err != nil {
		return nil, err
	}
	taint := Taint{}
	for _, w := range strings.FieldsFunc(text, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(",;!?()[]<>\"'", r) }) {
		w = strings.TrimRight(w, ".:")
		if cat, ok := Classify("", w, nil); ok {
			taint.Add(w, cat)
		}
	}
	return nestUnder(ctx, c, tx, tenant, text, env, subject, taint)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// SubjectSealer seals under the subject of another value, so that erasing
// that value's subject also erases what was sealed (secrets.Vault).
type SubjectSealer interface {
	SubjectForTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, category string, value any) (string, error)
	SealUnderTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, subject, category string, value any) (map[string]any, error)
}

// sealDerived seals a string built from known personal values ("ref-" +
// a BVN, "Dear " + a name): an exact match would miss it, and it would be
// stored in the clear where erasing the person cannot reach it. It is
// sealed under the subject of each value it contains, longest first, one
// envelope inside another, so erasing any of those people erases it.
// Values shorter than MinRedact are not looked for, as in RedactText.
func sealDerived(ctx context.Context, c Cipher, tx pgx.Tx, tenant uuid.UUID, s string, taint Taint) (any, error) {
	return nestUnder(ctx, c, tx, tenant, s, s, "", taint)
}

// nestUnder seals out under the subject of each known value s contains
// (but the subject skip, out's own), longest first, one envelope inside
// another; out is returned as it is when s contains none.
func nestUnder(ctx context.Context, c Cipher, tx pgx.Tx, tenant uuid.UUID, s string, out any, skip string, taint Taint) (any, error) {
	ss, ok := c.(SubjectSealer)
	if !ok || len(taint) == 0 {
		return out, nil
	}
	type hit struct {
		value any
		text  string
		cat   string
	}
	var hits []hit
	for k, cat := range taint {
		text := k[2:]
		if len(text) < MinRedact || !strings.Contains(s, text) {
			continue
		}
		var value any = text
		if k[0] == 'n' {
			f, err := strconv.ParseFloat(text, 64)
			if err != nil {
				continue
			}
			value = f
		}
		hits = append(hits, hit{value, text, cat})
	}
	if len(hits) == 0 {
		return out, nil
	}
	sort.Slice(hits, func(i, j int) bool {
		if len(hits[i].text) != len(hits[j].text) {
			return len(hits[i].text) > len(hits[j].text)
		}
		return hits[i].text < hits[j].text
	})
	seen := map[string]bool{skip: true}
	for _, h := range hits {
		subject, err := ss.SubjectForTx(ctx, tx, tenant, h.cat, h.value)
		if err != nil {
			return nil, err
		}
		if seen[subject] {
			continue
		}
		seen[subject] = true
		// Sealing the value itself makes sure its subject has a key; one
		// already erased leaves nothing of this string to keep.
		if _, err := c.SealTx(ctx, tx, tenant, h.cat, h.value); errors.Is(err, ErrErased) {
			return Erased, nil
		} else if err != nil {
			return nil, err
		}
		if out, err = ss.SealUnderTx(ctx, tx, tenant, subject, h.cat, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Open replaces every envelope with its plaintext (or Erased), recording
// the plaintext values in taint.
func Open(ctx context.Context, c Cipher, tx pgx.Tx, tenant uuid.UUID, v any, taint Taint) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		if IsEnvelope(t) {
			if c == nil {
				return nil, fmt.Errorf("sealed value but no cipher configured")
			}
			pt, err := c.OpenTx(ctx, tx, tenant, t)
			if errors.Is(err, ErrErased) {
				return Erased, nil
			}
			if err != nil {
				return nil, err
			}
			if IsEnvelope(pt) {
				// A derived string sealed under several people (sealDerived).
				return Open(ctx, c, tx, tenant, pt, taint)
			}
			cat, _ := t["$pii"].(string)
			switch pt.(type) {
			case map[string]any, []any:
				// A place sealed whole: its text values are personal.
				followStrings(pt, cat, taint)
			default:
				if nf, _ := t[NoFollow].(bool); !nf {
					taint.Add(pt, cat)
				}
			}
			return pt, nil
		}
		out := make(map[string]any, len(t))
		for k, x := range t {
			o, err := Open(ctx, c, tx, tenant, x, taint)
			if err != nil {
				return nil, err
			}
			out[k] = o
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			o, err := Open(ctx, c, tx, tenant, x, taint)
			if err != nil {
				return nil, err
			}
			out[i] = o
		}
		return out, nil
	}
	return v, nil
}

// HasEnvelopes reports whether v contains any sealed value.
func HasEnvelopes(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		if IsEnvelope(t) {
			return true
		}
		for _, x := range t {
			if HasEnvelopes(x) {
				return true
			}
		}
	case []any:
		for _, x := range t {
			if HasEnvelopes(x) {
				return true
			}
		}
	}
	return false
}

func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = deepCopy(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = deepCopy(x)
		}
		return out
	}
	return v
}
