package lang

import (
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
)

// Catalogues live in messages/<tag>.json. English (en.json) is the source:
// every id it has is a message Taskiem sends, with a note on where. Every
// other file is a translation of it, as a whole marked reviewed or not, and
// each entry marked too. Placeholders are {name}; a translation must use
// the same ones.

//go:embed messages/*.json
var messageFiles embed.FS

// File is one catalogue file.
type File struct {
	Language Tag `json:"language"`
	// Reviewed is true only when a native speaker has reviewed every
	// entry; drafts are false.
	Reviewed bool `json:"reviewed"`
	// Status says what the file is, for people reading it.
	Status string `json:"status,omitempty"`
	// Reviewers name who reviewed it, and when (docs/languages.md).
	Reviewers []string         `json:"reviewers"`
	Messages  map[string]Entry `json:"messages"`
}

// Entry is one message.
type Entry struct {
	Text string `json:"text"`
	// Reviewed is set by a native speaker who checked this entry.
	Reviewed bool `json:"reviewed"`
	// Context says where English sends it (en.json only).
	Context string `json:"context,omitempty"`
}

// Catalog is every catalogue, loaded.
type Catalog struct {
	files map[Tag]*File

	mu        sync.Mutex
	missing   map[string]bool // "tag/id" already reported
	onMissing func(t Tag, id string)
}

var (
	defaultOnce sync.Once
	defaultCat  *Catalog
	defaultErr  error
)

// Default is the embedded catalogues. It panics if they do not parse (a
// test checks that they do).
func Default() *Catalog {
	defaultOnce.Do(func() { defaultCat, defaultErr = Load() })
	if defaultErr != nil {
		panic(defaultErr)
	}
	return defaultCat
}

// Load parses the embedded catalogues.
func Load() (*Catalog, error) {
	c := &Catalog{files: map[Tag]*File{}, missing: map[string]bool{}}
	ents, err := messageFiles.ReadDir("messages")
	if err != nil {
		return nil, err
	}
	for _, e := range ents {
		raw, err := messageFiles.ReadFile(path.Join("messages", e.Name()))
		if err != nil {
			return nil, err
		}
		var f File
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, fmt.Errorf("lang: %s: %w", e.Name(), err)
		}
		if string(f.Language)+".json" != e.Name() || !Valid(f.Language) {
			return nil, fmt.Errorf("lang: %s declares language %q", e.Name(), f.Language)
		}
		c.files[f.Language] = &f
	}
	if c.files[EN] == nil {
		return nil, fmt.Errorf("lang: no English catalogue")
	}
	return c, nil
}

// FromFiles builds a catalogue from files (tests, and checking a reviewed
// file before it is committed); English is required.
func FromFiles(files ...*File) (*Catalog, error) {
	c := &Catalog{files: map[Tag]*File{}, missing: map[string]bool{}}
	for _, f := range files {
		if !Valid(f.Language) {
			return nil, fmt.Errorf("lang: unknown language %q", f.Language)
		}
		c.files[f.Language] = f
	}
	if c.files[EN] == nil {
		return nil, fmt.Errorf("lang: no English catalogue")
	}
	return c, nil
}

// File returns a language's catalogue file (nil if none).
func (c *Catalog) File(t Tag) *File { return c.files[t] }

// Has reports whether English has the id.
func (c *Catalog) Has(id string) bool {
	_, ok := c.files[EN].Messages[id]
	return ok
}

// IDs are English's message ids, sorted.
func (c *Catalog) IDs() []string {
	ids := make([]string, 0, len(c.files[EN].Messages))
	for id := range c.files[EN].Messages {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Text is message id in language t with its placeholders filled from kv
// (name, value, name, value...). A message t lacks comes in English and is
// reported once (SetOnMissing); an id English lacks is returned as is, so a
// typo shows rather than vanishing.
func (c *Catalog) Text(t Tag, id string, kv ...string) string {
	e, ok := c.lookup(t, id)
	if !ok {
		return id
	}
	return fill(e.Text, kv)
}

// Reviewed reports whether message id in t has been reviewed by a native
// speaker (English always has).
func (c *Catalog) Reviewed(t Tag, id string) bool {
	if t == EN {
		return true
	}
	f := c.files[t]
	if f == nil {
		return false
	}
	e, ok := f.Messages[id]
	return ok && (e.Reviewed || f.Reviewed)
}

func (c *Catalog) lookup(t Tag, id string) (Entry, bool) {
	if f := c.files[t]; f != nil {
		if e, ok := f.Messages[id]; ok && strings.TrimSpace(e.Text) != "" {
			return e, true
		}
	}
	e, ok := c.files[EN].Messages[id]
	if t != EN && ok {
		c.report(t, id)
	}
	return e, ok
}

func (c *Catalog) report(t Tag, id string) {
	key := string(t) + "/" + id
	c.mu.Lock()
	seen := c.missing[key]
	c.missing[key] = true
	hook := c.onMissing
	c.mu.Unlock()
	if !seen && hook != nil {
		hook(t, id)
	}
}

// SetOnMissing sets what is called, once per language and id, when a
// message falls back to English.
func (c *Catalog) SetOnMissing(fn func(t Tag, id string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onMissing = fn
}

// Missing lists the "tag/id" pairs that fell back to English so far.
func (c *Catalog) Missing() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.missing))
	for k := range c.missing {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func fill(s string, kv []string) string {
	if len(kv) == 0 {
		return s
	}
	pairs := make([]string, 0, len(kv))
	for i := 0; i+1 < len(kv); i += 2 {
		pairs = append(pairs, "{"+kv[i]+"}", kv[i+1])
	}
	return strings.NewReplacer(pairs...).Replace(s)
}

var placeholderRe = regexp.MustCompile(`\{[a-z_]+\}`)

// Placeholders are the {names} a text uses, sorted, without repeats.
func Placeholders(s string) []string {
	ph := placeholderRe.FindAllString(s, -1)
	sort.Strings(ph)
	return slices.Compact(ph)
}

// Problem is something wrong with a catalogue.
type Problem struct {
	Language Tag
	ID       string
	Kind     string // missing | extra | placeholders | empty
	Detail   string
}

func (p Problem) String() string {
	s := fmt.Sprintf("%s: %s: %s", p.Language, p.ID, p.Kind)
	if p.Detail != "" {
		s += " (" + p.Detail + ")"
	}
	return s
}

// Check compares every catalogue with English: ids missing (they fall back
// to English), ids English does not have, and placeholders that differ.
func (c *Catalog) Check() []Problem {
	var out []Problem
	en := c.files[EN]
	for _, info := range all {
		if info.Tag == EN {
			for id, e := range en.Messages {
				if strings.TrimSpace(e.Text) == "" {
					out = append(out, Problem{EN, id, "empty", ""})
				}
			}
			continue
		}
		f := c.files[info.Tag]
		if f == nil {
			out = append(out, Problem{info.Tag, "*", "missing", "no catalogue file"})
			continue
		}
		for id, e := range en.Messages {
			t, ok := f.Messages[id]
			switch {
			case !ok || strings.TrimSpace(t.Text) == "":
				out = append(out, Problem{info.Tag, id, "missing", "falls back to English"})
			case !slices.Equal(Placeholders(e.Text), Placeholders(t.Text)):
				out = append(out, Problem{info.Tag, id, "placeholders", fmt.Sprintf("English has %v, this has %v", Placeholders(e.Text), Placeholders(t.Text))})
			}
		}
		for id := range f.Messages {
			if _, ok := en.Messages[id]; !ok {
				out = append(out, Problem{info.Tag, id, "extra", "English has no such message"})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Language != out[j].Language {
			return out[i].Language < out[j].Language
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Progress is how much of a catalogue a native speaker has reviewed.
type Progress struct {
	Language Tag  `json:"language"`
	Reviewed bool `json:"reviewed"` // the whole file
	Entries  int  `json:"entries"`
	Done     int  `json:"reviewed_entries"`
}

// Progress reports each language's review state.
func (c *Catalog) Progress() []Progress {
	var out []Progress
	for _, info := range all {
		f := c.files[info.Tag]
		if f == nil {
			continue
		}
		p := Progress{Language: info.Tag, Reviewed: info.Tag == EN || f.Reviewed, Entries: len(f.Messages)}
		for _, e := range f.Messages {
			if e.Reviewed || p.Reviewed {
				p.Done++
			}
		}
		out = append(out, p)
	}
	return out
}
