package storefront

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
)

// Kinds of front (docs/spec/fronts.md §6): presets over the model. Picking
// a kind makes an ordinary front: its table (a known table, made for the
// business), a list block of it, and its actions, off, each with its
// workflow template. Nothing about the front is special afterwards.

//go:embed kinds/*.json
var kindFiles embed.FS

// FrontKind is one preset.
type FrontKind struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Line  string `json:"line"`
	// Table is the known table the kind makes (templates/tables), and
	// Mapping its columns as a list shows them; "" for none.
	Table   string  `json:"table,omitempty"`
	Mapping Columns `json:"mapping"`
	// Calendar is the list's calendar, for a kind whose rows are booked
	// for times (Spaces).
	Calendar *Calendar    `json:"calendar,omitempty"`
	Actions  []KindAction `json:"actions"`
}

// KindAction is an action a kind adds.
type KindAction struct {
	Kind     string        `json:"kind"`
	Label    string        `json:"label"`
	Template string        `json:"template"`
	Options  ActionOptions `json:"options"`
}

var (
	kindsOnce sync.Once
	kindsList []FrontKind
	kindsErr  error
)

// kindOrder is the order owners are offered kinds in: Custom last.
var kindOrder = []string{"shop", "food", "hire", "spaces", "questions", "callbacks", "custom"}

func loadKinds() {
	names, err := fs.Glob(kindFiles, "kinds/*.json")
	if err != nil {
		kindsErr = err
		return
	}
	for _, n := range names {
		raw, err := fs.ReadFile(kindFiles, n)
		if err != nil {
			kindsErr = err
			return
		}
		var k FrontKind
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&k); err != nil {
			kindsErr = fmt.Errorf("%s: %w", n, err)
			return
		}
		if k.Name != strings.TrimSuffix(path.Base(n), ".json") || k.Title == "" || k.Line == "" {
			kindsErr = fmt.Errorf("%s: a kind names itself as its file does, with a title and a line", n)
			return
		}
		for _, a := range k.Actions {
			if !contains(Kinds, a.Kind) || a.Kind == "booking" || a.Label == "" || a.Template == "" {
				kindsErr = fmt.Errorf("%s: an action is an order, hire, space booking, question or call-back, with a label and a template", n)
				return
			}
		}
		if k.Calendar != nil {
			c, err := CleanCalendar(*k.Calendar)
			if err != nil {
				kindsErr = fmt.Errorf("%s: %w", n, err)
				return
			}
			k.Calendar = &c
		}
		kindsList = append(kindsList, k)
	}
	sort.SliceStable(kindsList, func(i, j int) bool {
		return indexOf(kindOrder, kindsList[i].Name) < indexOf(kindOrder, kindsList[j].Name)
	})
}

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return len(list)
}

// FrontKinds are the presets, in the order owners are offered them. It
// panics if the embedded files are broken, which the tests rule out.
func FrontKinds() []FrontKind {
	kindsOnce.Do(loadKinds)
	if kindsErr != nil {
		panic(kindsErr)
	}
	return kindsList
}

// FrontKindOf is a preset by name.
func FrontKindOf(name string) (FrontKind, bool) {
	for _, k := range FrontKinds() {
		if k.Name == name {
			return k, true
		}
	}
	return FrontKind{}, false
}
