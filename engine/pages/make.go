package pages

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/israel-duff/taskiem/engine/datatables"
)

// What a page brings with it (G5): a starter, or a page Describe it drafts,
// is a Bundle: the data tables it needs, the tools it presses, and the
// page. The tools are made tools of two plain kinds over one table (made
// tools §13's "add a record", widened to the table's own columns, and its
// "list and act" button's change of a row), or one of the recipes
// themselves: nothing is written as free steps, so what a page brings is
// always something Taskiem can read back in plain words and check.

// Bundle is a page and what it brings.
type Bundle struct {
	Tables []TableSpec     `json:"tables"`
	Tools  []ToolSpec      `json:"tools"`
	Page   json.RawMessage `json:"page"`
}

// TableSpec is a data table a bundle makes.
type TableSpec struct {
	Name    string       `json:"name"`
	Title   string       `json:"title"`
	Columns []ColumnSpec `json:"columns"`
}

// ColumnSpec is one of its columns (the data tables' types).
type ColumnSpec struct {
	Name     string `json:"name"`
	Title    string `json:"title"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
}

// Table is the spec as the business will have it, for checking a page
// before the table is made: phones and emails are personal, as a table
// made with no word on it has them.
func (t TableSpec) Table() datatables.Table {
	out := datatables.Table{Name: t.Name, Title: t.Title}
	for _, c := range t.Columns {
		out.Columns = append(out.Columns, datatables.Column{Name: c.Name, Title: c.Title, Type: c.Type, Required: c.Required,
			Personal: c.Type == "phone" || c.Type == "email"})
	}
	return out
}

// ToolSpec is a tool a bundle makes.
//
//   - kind "add": adds a row to Table, asking for the columns in Asks (every
//     column not in Sets when none are named) and writing Sets as they are
//     ("status": "open");
//   - kind "change": changes one row, named by its number (the input ref),
//     to Sets and to the columns in Asks, each asked ("status", set by a
//     board's move to the column a card goes to);
//   - kind "recipe": one of the template library's recipes (Recipe, with
//     Params), its tool renamed to Name.
type ToolSpec struct {
	Command string         `json:"command"`
	Name    string         `json:"name"`
	About   string         `json:"about,omitempty"`
	Kind    string         `json:"kind"`
	Table   string         `json:"table,omitempty"`
	Asks    []string       `json:"asks,omitempty"`
	Sets    map[string]any `json:"sets,omitempty"`
	Icon    string         `json:"icon,omitempty"`
	Roles   []string       `json:"roles,omitempty"`
	Recipe  string         `json:"recipe,omitempty"`
	Params  map[string]any `json:"params,omitempty"`
}

// toolCommandRe is a tool's command (made tools §5).
var toolCommandRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,23}$`)

// toolIcons are the pictures a tool may carry (wd/v1).
var toolIcons = []string{"receipt", "box", "cart", "money", "people", "person", "calendar", "clock", "truck", "list", "search", "chat", "bell", "check", "chart", "wrench", "server", "tag", "store", "file"}

// Asked is the columns an add tool asks for, in the table's order.
func (s ToolSpec) Asked(t TableSpec) []ColumnSpec {
	var out []ColumnSpec
	for _, c := range t.Columns {
		if _, fixed := s.Sets[c.Name]; fixed {
			continue
		}
		if len(s.Asks) == 0 && s.Kind == "add" || slices.Contains(s.Asks, c.Name) {
			out = append(out, c)
		}
	}
	return out
}

// Check says what is wrong with a tool spec over its table, in plain words
// ("" when nothing is). A recipe is checked by the library.
func (s ToolSpec) Check(t TableSpec) string {
	if !toolCommandRe.MatchString(s.Command) {
		return fmt.Sprintf("the tool's word %q is 2 to 24 small letters, digits and dashes, starting with a letter", s.Command)
	}
	if n := len([]rune(strings.TrimSpace(s.Name))); n == 0 || n > 40 {
		return fmt.Sprintf("the tool /%s needs a name of up to 40 letters", s.Command)
	}
	if len([]rune(s.About)) > 120 {
		return fmt.Sprintf("the line about /%s is at most 120 letters", s.Command)
	}
	if s.Icon != "" && !slices.Contains(toolIcons, s.Icon) {
		return fmt.Sprintf("the tool /%s has a picture Taskiem does not have", s.Command)
	}
	switch s.Kind {
	case "recipe":
		if s.Recipe == "" {
			return fmt.Sprintf("the tool /%s is made from a recipe: say which", s.Command)
		}
		return ""
	case "add", "change":
	default:
		return fmt.Sprintf("the tool /%s adds a row or changes one", s.Command)
	}
	if s.Table != t.Name {
		return fmt.Sprintf("the tool /%s works on the table %s, which is not there", s.Command, s.Table)
	}
	has := func(c string) bool {
		return slices.ContainsFunc(t.Columns, func(x ColumnSpec) bool { return x.Name == c })
	}
	for _, c := range s.Asks {
		if !has(c) {
			return fmt.Sprintf("the tool /%s asks for %s, which the table %s does not have", s.Command, c, t.Name)
		}
		if c == "ref" && s.Kind == "change" {
			return fmt.Sprintf("the tool /%s cannot ask for a column called ref: it names the row it changes", s.Command)
		}
	}
	for c := range s.Sets {
		if !has(c) {
			return fmt.Sprintf("the tool /%s sets %s, which the table %s does not have", s.Command, c, t.Name)
		}
	}
	if s.Kind == "change" && len(s.Asks)+len(s.Sets) == 0 {
		return fmt.Sprintf("the tool /%s changes a row: say what it changes", s.Command)
	}
	if s.Kind == "add" && len(s.Asked(t)) == 0 {
		return fmt.Sprintf("the tool /%s adds a row: it asks for at least one column", s.Command)
	}
	return ""
}

// inputSchema is a column as a tool's form asks for it.
func inputSchema(c ColumnSpec) map[string]any {
	title := c.Title
	if title == "" {
		title = c.Name
	}
	m := map[string]any{"title": title}
	switch c.Type {
	case "integer":
		m["type"] = "integer"
	case "number":
		m["type"] = "number"
	case "boolean":
		m["type"] = "boolean"
	case "date":
		m["type"], m["format"] = "string", "date"
	case "datetime":
		m["type"], m["format"] = "string", "date-time"
	case "email":
		m["type"], m["format"] = "string", "email"
	case "phone":
		m["type"], m["format"], m["maxLength"] = "string", "phone", 20
	default:
		m["type"], m["maxLength"] = "string", 500
	}
	if c.Required && c.Type == "text" {
		m["minLength"] = 1
	}
	return m
}

// workflowID is a workflow's id made from a command ("new-ticket" is
// wf_newTicket).
func workflowID(command string) string {
	var b strings.Builder
	b.WriteString("wf_")
	up := false
	for _, r := range command {
		switch {
		case r == '-':
			up = true
		case up && r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
			up = false
		default:
			b.WriteRune(r)
			up = false
		}
	}
	return b.String()
}

// Definition is the wd/v1 workflow an add or change tool is: started by
// hand, its form the columns it asks for, one data tables step, a
// sentence back. It writes, so it asks Yes before it runs.
func (s ToolSpec) Definition(t TableSpec) (json.RawMessage, error) {
	if why := s.Check(t); why != "" {
		return nil, fmt.Errorf("%s", why)
	}
	if s.Kind == "recipe" {
		return nil, fmt.Errorf("the tool /%s is a recipe's: the library makes it", s.Command)
	}
	title := t.Title
	if title == "" {
		title = t.Name
	}
	props := map[string]any{}
	var required []string
	var step map[string]any
	answer := "Added to " + title + "."
	out := map[string]any{"name": "added", "type": "integer", "title": "Added", "description": "Rows added", "value": "=steps.write.output.affected"}
	switch s.Kind {
	case "add":
		row := map[string]any{}
		for _, c := range s.Asked(t) {
			props[c.Name] = inputSchema(c)
			if c.Required {
				required = append(required, c.Name)
				row[c.Name] = "=trigger.body." + c.Name
			} else {
				row[c.Name] = "=has(trigger.body." + c.Name + ") ? trigger.body." + c.Name + " : null"
			}
		}
		for k, v := range s.Sets {
			row[k] = Literal(v)
		}
		step = map[string]any{"id": "write", "type": "connector", "name": "Add the row to " + title, "connector": "xenibase@1", "action": "insert_rows",
			"connection": "data_tables", "input": map[string]any{"table": t.Name, "row": row, "return": "minimal"}}
	case "change":
		props["ref"] = map[string]any{"type": "string", "title": "Which one", "minLength": 1, "maxLength": 40}
		required = append(required, "ref")
		set := map[string]any{}
		for _, c := range t.Columns {
			if slices.Contains(s.Asks, c.Name) {
				props[c.Name] = inputSchema(c)
				required = append(required, c.Name)
				set[c.Name] = "=trigger.body." + c.Name
			}
		}
		for k, v := range s.Sets {
			set[k] = Literal(v)
		}
		step = map[string]any{"id": "write", "type": "connector", "name": "Change the row in " + title, "connector": "xenibase@1", "action": "update_rows",
			"connection": "data_tables", "input": map[string]any{"table": t.Name, "key": "=trigger.body.ref", "set": set, "max_affected": 1, "return": "minimal"}}
		answer = "Changed in " + title + "."
		out = map[string]any{"name": "changed", "type": "integer", "title": "Changed", "description": "Rows changed", "value": "=steps.write.output.affected"}
	}
	icon := s.Icon
	if icon == "" {
		icon = map[string]string{"add": "list", "change": "check"}[s.Kind]
	}
	inputs := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		inputs["required"] = required
	}
	about := s.About
	if about == "" {
		about = map[string]string{"add": "Adds a row to " + title, "change": "Changes a row of " + title}[s.Kind]
	}
	tool := map[string]any{"name": s.Name, "command": s.Command, "about": about, "icon": icon, "for": "members",
		"answer": map[string]any{"type": "text", "text": answer}}
	def := map[string]any{"schema": "wd/v1", "id": workflowID(s.Command), "version": 1, "name": s.Name, "description": about,
		"trigger": map[string]any{"type": "manual"}, "inputs": map[string]any{"schema": inputs},
		"outputs": []any{out}, "tool": tool, "steps": []any{step}, "settings": map[string]any{"timeout": "5m"}}
	if len(s.Roles) > 0 {
		def["roles"] = s.Roles
	}
	return json.Marshal(def)
}

// Info is the tool as a page names it, before it is made: it writes, so
// it asks Yes first; its inputs by name.
func (s ToolSpec) Info(t TableSpec) ToolInfo {
	info := ToolInfo{Name: s.Name, Care: "yes", Outputs: map[string]string{}, Roles: s.Roles}
	switch s.Kind {
	case "add":
		info.Outputs["added"] = "integer"
		for _, c := range s.Asked(t) {
			info.Inputs = append(info.Inputs, c.Name)
		}
	case "change":
		info.Outputs["changed"] = "integer"
		info.Inputs = append(info.Inputs, "ref")
		info.Inputs = append(info.Inputs, s.Asks...)
	}
	return info
}

// Words say what a tool spec does, in plain words: "/new-ticket: adds a
// row to Tickets, asking for Title and Customer".
func (s ToolSpec) Words(t TableSpec) string {
	title := t.Title
	if title == "" {
		title = s.Table
	}
	switch s.Kind {
	case "add":
		var asks []string
		for _, c := range s.Asked(t) {
			asks = append(asks, columnSpecTitle(c))
		}
		return fmt.Sprintf("/%s (%s): adds a row to %s, asking for %s", s.Command, s.Name, title, andList(asks))
	case "change":
		var what []string
		for _, c := range t.Columns {
			if v, ok := s.Sets[c.Name]; ok {
				what = append(what, fmt.Sprintf("%s to %v", columnSpecTitle(c), v))
			} else if slices.Contains(s.Asks, c.Name) {
				what = append(what, columnSpecTitle(c))
			}
		}
		return fmt.Sprintf("/%s (%s): changes a row of %s: %s", s.Command, s.Name, title, andList(what))
	}
	return fmt.Sprintf("/%s (%s): the ready-made tool %s", s.Command, s.Name, s.Recipe)
}

func columnSpecTitle(c ColumnSpec) string {
	if c.Title != "" {
		return c.Title
	}
	return c.Name
}

// RenameTable makes a page read table new in place of old.
func (p *Page) RenameTable(old, new string) {
	for i := range p.Blocks {
		if s := p.Blocks[i].Source; s != nil && s.Table == old {
			s.Table = new
		}
	}
}

// RenameTool makes a page press, or read, the tool new in place of old.
func (p *Page) RenameTool(old, new string) {
	ren := func(a *Action) {
		if a.Tool == old {
			a.Tool = new
		}
	}
	for i := range p.Blocks {
		b := &p.Blocks[i]
		for j := range b.Actions {
			ren(&b.Actions[j])
		}
		if b.Board != nil && b.Board.Move != nil {
			ren(b.Board.Move)
		}
		if b.Source != nil && b.Source.Tool == old {
			b.Source.Tool = new
		}
	}
}

// ToolsPressed are the tools a page presses or reads, each once.
func (p *Page) ToolsPressed() []string {
	var out []string
	add := func(c string) {
		if c != "" && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	for _, b := range p.Blocks {
		for _, a := range b.Actions {
			add(a.Tool)
		}
		if b.Board != nil && b.Board.Move != nil {
			add(b.Board.Move.Tool)
		}
		if b.Source != nil {
			add(b.Source.Tool)
		}
	}
	return out
}
