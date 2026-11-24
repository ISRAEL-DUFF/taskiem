package pages

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"
)

// Starters (G5, pages §10, 3): Tickets desk, Expenses this month, Leave,
// Stock count, Deliveries today, Waiting list and Service log, each a page
// with the table and the tools it presses, installed together
// (api/pages_starters.go). They are files in starters/, read once.

// Starter is one starter: what it is called and says, and its bundle.
type Starter struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	About string `json:"about"`
	Icon  string `json:"icon"`
	Bundle
}

//go:embed starters/*.json
var starterFiles embed.FS

// starterOrder is the order the plan names them in.
var starterOrder = []string{"tickets-desk", "expenses-month", "leave", "stock-count", "deliveries-today", "waiting-list", "service-log"}

var starters = func() []Starter {
	var out []Starter
	for _, id := range starterOrder {
		raw, err := starterFiles.ReadFile(path.Join("starters", id+".json"))
		if err != nil {
			panic(err)
		}
		var st struct {
			ID     string          `json:"id"`
			Title  string          `json:"title"`
			About  string          `json:"about"`
			Icon   string          `json:"icon"`
			Tables []TableSpec     `json:"tables"`
			Tools  []ToolSpec      `json:"tools"`
			Page   json.RawMessage `json:"page"`
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&st); err != nil {
			panic(fmt.Sprintf("starter %s: %v", id, err))
		}
		var page bytes.Buffer
		if err := json.Compact(&page, st.Page); err != nil {
			panic(err)
		}
		out = append(out, Starter{ID: st.ID, Title: st.Title, About: st.About, Icon: st.Icon,
			Bundle: Bundle{Tables: st.Tables, Tools: st.Tools, Page: page.Bytes()}})
	}
	return out
}()

// Starters are the starters, in order. Each call gives its own copy.
func Starters() []Starter {
	out := make([]Starter, len(starters))
	for i, s := range starters {
		out[i] = s.clone()
	}
	return out
}

// StarterNamed is a starter by its id.
func StarterNamed(id string) (Starter, bool) {
	i := slices.IndexFunc(starters, func(s Starter) bool { return s.ID == id })
	if i < 0 {
		return Starter{}, false
	}
	return starters[i].clone(), true
}

func (s Starter) clone() Starter {
	raw, _ := json.Marshal(s.Bundle)
	var b Bundle
	_ = json.Unmarshal(raw, &b)
	s.Bundle = b
	return s
}

// TableNamed is the bundle's table by name.
func (b Bundle) TableNamed(name string) (TableSpec, bool) {
	i := slices.IndexFunc(b.Tables, func(t TableSpec) bool { return t.Name == name })
	if i < 0 {
		return TableSpec{}, false
	}
	return b.Tables[i], true
}

// Words say what a bundle makes, a line each: "The table Tickets: Title,
// Customer, Email, Priority, Status", "/new-ticket (New ticket): adds a
// row to Tickets, asking for …".
func (b Bundle) Words() []string {
	var out []string
	for _, t := range b.Tables {
		var cols []string
		for _, c := range t.Columns {
			cols = append(cols, columnSpecTitle(c))
		}
		title := t.Title
		if title == "" {
			title = t.Name
		}
		out = append(out, "The table "+title+": "+strings.Join(cols, ", "))
	}
	for _, s := range b.Tools {
		t, _ := b.TableNamed(s.Table)
		out = append(out, s.Words(t))
	}
	return out
}
