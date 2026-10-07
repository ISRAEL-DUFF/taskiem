// Package ussd is the USSD fast path's menu model (spec 8.4): a declarative
// menu carried in a workflow's ussd trigger config, its publish-time checks,
// and a pure walk that turns the path a caller has typed so far into the
// next screen. Menu steps never touch the durable engine: the edge walks
// the menu inline from the aggregator's request, and only the final
// confirmation hands the collected inputs to a run (docs/ussd.md).
package ussd

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/israel-duff/taskiem/engine/expr"
)

// Screen types.
const (
	TypeMenu    = "menu"    // numbered options, each leading to a screen
	TypeInput   = "input"   // free input, validated, stored under Input
	TypeConfirm = "confirm" // the collected inputs and "1. Yes / 2. Cancel"
	TypeEnd     = "end"     // a final message; nothing is started
)

// Limits of a menu.
const (
	// MaxChars is the most a screen may hold. 182 characters is the
	// USSD protocol's ceiling; operators often cut lower (Safaricom and
	// Nigerian networks at 160, per Africa's Talking's help centre), so a
	// menu may lower it with max_chars.
	MaxChars = 182
	// MinChars is the lowest max_chars a menu may set.
	MinChars = 60
	// MaxScreens bounds a menu; MaxOptions is the most options a screen
	// shows (single digits, 0 is Back).
	MaxScreens = 50
	MaxOptions = 9
	// MaxDepth bounds how many inputs one session may type.
	MaxDepth = 60
	// Back and Home are navigation inputs on every screen but the first:
	// "0" returns to the previous screen, "00" to the start.
	Back = "0"
	Home = "00"
)

// Default texts.
const (
	DefaultChoiceError = "Invalid choice. Try again."
	DefaultInputError  = "Invalid input. Try again."
	DefaultConfirm     = "Yes"
	DefaultCancel      = "Cancel"
	DefaultDone        = "Thank you. Your request has been received. Ref: {{reference}}"
	DefaultCancelled   = "Cancelled. Nothing was done."
	DefaultCompleted   = "Your request {{reference}} is complete."
	DefaultFailed      = "Your request {{reference}} could not be completed."
	backLine           = "0. Back"
)

// Menu is a ussd trigger's config.
type Menu struct {
	ServiceCode string    `json:"service_code"`
	Start       string    `json:"start,omitempty"`
	MaxChars    int       `json:"max_chars,omitempty"`
	Screens     []*Screen `json:"screens"`
	Notify      *Notify   `json:"notify,omitempty"`

	// Personal names inputs whose values are masked on screens and in
	// messages (fields marked x-pii in the workflow's inputs schema; values
	// pii.Classify recognises are masked whatever their name).
	Personal map[string]bool `json:"-"`

	byID map[string]*Screen
}

// Screen is one USSD page.
type Screen struct {
	ID       string    `json:"id"`
	Type     string    `json:"type"`
	Text     string    `json:"text"`
	Options  []Option  `json:"options,omitempty"`
	Input    string    `json:"input,omitempty"`
	Next     string    `json:"next,omitempty"`
	Validate *Validate `json:"validate,omitempty"`
	Error    string    `json:"error,omitempty"`
	// Confirm screens.
	ConfirmLabel string `json:"confirm_label,omitempty"`
	CancelLabel  string `json:"cancel_label,omitempty"`
	Done         string `json:"done,omitempty"`
}

// Option is a numbered choice on a menu screen.
type Option struct {
	Label string `json:"label"`
	Next  string `json:"next"`
	// Value is stored under the screen's input when chosen (the label when
	// absent).
	Value any `json:"value,omitempty"`
	// When, a CEL expression over trigger.body (the inputs so far), shows
	// the option only when true.
	When string `json:"when,omitempty"`
}

// Validate is what an input screen accepts.
type Validate struct {
	Type      string   `json:"type,omitempty"` // text (default), number, integer
	Pattern   string   `json:"pattern,omitempty"`
	Min       *float64 `json:"min,omitempty"`
	Max       *float64 `json:"max,omitempty"`
	MinLength *int     `json:"min_length,omitempty"`
	MaxLength *int     `json:"max_length,omitempty"`
	// When, a CEL expression over trigger.body with the new value in
	// place, must be true for the input to be accepted.
	When string `json:"when,omitempty"`

	re *regexp.Regexp
}

// Notify asks for the run's outcome by SMS to the caller.
type Notify struct {
	SMS bool `json:"sms"`
	// Connection names the tenant's Africa's Talking connection in the
	// channel's environment (the only one when empty).
	Connection string `json:"connection,omitempty"`
	Completed  string `json:"completed,omitempty"`
	Failed     string `json:"failed,omitempty"`
}

// Problem is one thing wrong with a menu; Path is a JSON pointer under the
// trigger's config.
type Problem struct {
	Path    string
	Message string
}

func (p Problem) String() string { return p.Path + ": " + p.Message }

// Conditions are CEL over trigger.body only: no env, steps, or secrets.
var conditions = expr.MustNewWithRoots("trigger")

// Parse reads a ussd trigger config. It does not check it (Check does).
func Parse(raw []byte) (*Menu, error) {
	var m Menu
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("ussd menu: %w", err)
	}
	m.index()
	for _, s := range m.Screens {
		for i := range s.Options {
			s.Options[i].Value = normal(s.Options[i].Value)
		}
		if s.Validate != nil && s.Validate.Pattern != "" {
			// A pattern that does not compile refuses every input; Check
			// reports it.
			s.Validate.re, _ = regexp.Compile(anchor(s.Validate.Pattern))
		}
	}
	return &m, nil
}

// ParseConfig is Parse for a decoded config.
func ParseConfig(cfg map[string]any) (*Menu, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	return Parse(raw)
}

func normal(v any) any {
	if n, ok := v.(json.Number); ok {
		if i, err := n.Int64(); err == nil {
			return i
		}
		f, _ := n.Float64()
		return f
	}
	return v
}

// anchor makes a pattern match the whole input.
func anchor(p string) string { return `^(?:` + p + `)$` }

func (m *Menu) index() {
	m.byID = make(map[string]*Screen, len(m.Screens))
	for _, s := range m.Screens {
		if _, dup := m.byID[s.ID]; !dup {
			m.byID[s.ID] = s
		}
	}
}

// Screen returns a screen by id.
func (m *Menu) Screen(id string) *Screen { return m.byID[id] }

// StartScreen is where a session begins.
func (m *Menu) StartScreen() *Screen {
	if m.Start != "" {
		return m.byID[m.Start]
	}
	if len(m.Screens) == 0 {
		return nil
	}
	return m.Screens[0]
}

// Limit is the menu's screen size.
func (m *Menu) Limit() int {
	if m.MaxChars > 0 {
		return m.MaxChars
	}
	return MaxChars
}

// Inputs lists the names the menu collects, sorted.
func (m *Menu) Inputs() []string {
	seen := map[string]bool{}
	for _, s := range m.Screens {
		if s.Input != "" {
			seen[s.Input] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SMS reports whether the caller asked to hear the outcome by SMS.
func (m *Menu) SMS() bool { return m.Notify != nil && m.Notify.SMS }

var (
	screenID    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	inputName   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
	placeholder = regexp.MustCompile(`\{\{\s*([A-Za-z][A-Za-z0-9_]*)\s*\}\}`)
	serviceCode = regexp.MustCompile(`^\*[0-9]{1,6}(\*[0-9]{1,6}){0,4}#$`)
	// gsm is what every handset shows: printable ASCII and newlines.
	// Other characters may arrive as question marks or double a screen's
	// encoded size, so menus stay within it.
	gsm = regexp.MustCompile(`^[\x20-\x7E\n]*$`)
)

// Fields is what Check knows of the workflow's inputs schema.
type Fields struct {
	// Known is false when the schema says nothing usable (no schema, or
	// not a plain object): names are then not checked.
	Known    bool
	Names    map[string]string // property -> JSON type ("" when unknown)
	Required []string
}

// Check reports what is wrong with a menu: structure the JSON Schema does
// not cover, screens over the size limit (worst case, with every
// placeholder at its longest), screens the start cannot reach, options and
// inputs leading nowhere, patterns and conditions that do not compile,
// placeholders naming nothing, and inputs the workflow does not take.
func Check(m *Menu, f Fields) []Problem {
	c := &checker{m: m}
	c.check(f)
	sort.SliceStable(c.out, func(i, j int) bool { return c.out[i].Path < c.out[j].Path })
	return c.out
}

type checker struct {
	m   *Menu
	out []Problem
}

func (c *checker) add(path, format string, args ...any) {
	c.out = append(c.out, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
}

func (c *checker) check(f Fields) {
	m := c.m
	if m.byID == nil {
		m.index()
	}
	if !serviceCode.MatchString(m.ServiceCode) {
		c.add("/service_code", "service code %q is not a USSD code such as *384*123#", m.ServiceCode)
	}
	if m.MaxChars != 0 && (m.MaxChars < MinChars || m.MaxChars > MaxChars) {
		c.add("/max_chars", "max_chars must be between %d and %d", MinChars, MaxChars)
	}
	if len(m.Screens) == 0 {
		c.add("/screens", "a menu needs at least one screen")
		return
	}
	if len(m.Screens) > MaxScreens {
		c.add("/screens", "at most %d screens", MaxScreens)
	}
	ids := map[string]int{}
	for i, s := range m.Screens {
		p := fmt.Sprintf("/screens/%d", i)
		if !screenID.MatchString(s.ID) {
			c.add(p+"/id", "screen id %q must be lower case letters, digits and _ (up to 32)", s.ID)
		}
		if j, dup := ids[s.ID]; dup {
			c.add(p+"/id", "screen id %q is already used by screen %d", s.ID, j)
		} else {
			ids[s.ID] = i
		}
	}
	if m.Start != "" && m.byID[m.Start] == nil {
		c.add("/start", "no screen %q", m.Start)
		return
	}
	collected := map[string]bool{}
	types := map[string]string{} // input name -> value type collected
	for _, s := range m.Screens {
		if s.Input != "" {
			collected[s.Input] = true
			t := "string"
			switch {
			case s.Type == TypeInput && s.Validate != nil && s.Validate.Type == "integer":
				t = "integer"
			case s.Type == TypeInput && s.Validate != nil && s.Validate.Type == "number":
				t = "number"
			case s.Type == TypeMenu:
				t = optionType(s.Options)
			}
			if prev, ok := types[s.Input]; ok && prev != t {
				t = "mixed"
			}
			types[s.Input] = t
		}
	}
	confirms := 0
	for i, s := range m.Screens {
		c.screen(fmt.Sprintf("/screens/%d", i), s, collected)
		if s.Type == TypeConfirm {
			confirms++
		}
	}
	c.reach()
	if confirms == 0 {
		c.add("/screens", "no confirm screen: the menu could never start the workflow")
	}
	if m.Notify != nil {
		for k, t := range map[string]string{"completed": m.Notify.Completed, "failed": m.Notify.Failed} {
			c.text("/notify/"+k, t, collected, true)
			if utf8.RuneCountInString(t) > 160 {
				c.add("/notify/"+k, "an SMS here is at most 160 characters")
			}
		}
		if !m.Notify.SMS && (m.Notify.Connection != "" || m.Notify.Completed != "" || m.Notify.Failed != "") {
			c.add("/notify/sms", "set sms: true to send the outcome by SMS, or remove notify")
		}
	}
	if f.Known {
		names := m.Inputs()
		for _, n := range names {
			jt, ok := f.Names[n]
			if !ok {
				c.add("/screens", "input %q is not a property of the workflow's inputs schema", n)
				continue
			}
			got := types[n]
			switch {
			case jt == "" || got == "mixed":
			case (jt == "integer" || jt == "number") && got == "string":
				c.add("/screens", "input %q is a %s in the inputs schema but the menu collects text: validate it with type %s", n, jt, jt)
			case jt == "string" && (got == "integer" || got == "number"):
				c.add("/screens", "input %q is a string in the inputs schema but the menu collects a number", n)
			case jt == "integer" && got == "number":
				c.add("/screens", "input %q is an integer in the inputs schema: validate it with type integer", n)
			}
		}
		for _, r := range f.Required {
			if !collected[r] {
				c.add("/screens", "the inputs schema requires %q but no screen collects it", r)
			}
		}
	}
}

func optionType(opts []Option) string {
	t := ""
	for _, o := range opts {
		var ot string
		switch v := o.Value.(type) {
		case nil, string:
			ot = "string"
		case int64:
			ot = "integer"
		case float64:
			if v == math.Trunc(v) {
				ot = "integer"
			} else {
				ot = "number"
			}
		case bool:
			ot = "boolean"
		default:
			ot = "mixed"
		}
		if t != "" && t != ot {
			return "mixed"
		}
		t = ot
	}
	return t
}

func (c *checker) screen(p string, s *Screen, collected map[string]bool) {
	m := c.m
	notFor := func(field string, set bool) {
		if set {
			c.add(p+"/"+field, "%s screens do not take %s", s.Type, field)
		}
	}
	link := func(path, id string) {
		if id != "" && m.byID[id] == nil {
			c.add(path, "no screen %q", id)
		}
	}
	c.text(p+"/text", s.Text, collected, false)
	switch s.Type {
	case TypeMenu:
		notFor("next", s.Next != "")
		notFor("validate", s.Validate != nil)
		notFor("done", s.Done != "")
		notFor("confirm_label", s.ConfirmLabel != "")
		notFor("cancel_label", s.CancelLabel != "")
		if len(s.Options) == 0 || len(s.Options) > MaxOptions {
			c.add(p+"/options", "a menu screen has 1 to %d options", MaxOptions)
		}
		for i, o := range s.Options {
			op := fmt.Sprintf("%s/options/%d", p, i)
			link(op+"/next", o.Next)
			if !gsm.MatchString(o.Label) || strings.Contains(o.Label, "\n") {
				c.add(op+"/label", "labels are one line of plain ASCII (what every handset shows)")
			}
			if strings.Contains(o.Label, "{{") {
				c.add(op+"/label", "labels are fixed text")
			}
			if o.When != "" {
				c.condition(op+"/when", o.When)
			}
		}
		if s.Input != "" && !inputName.MatchString(s.Input) {
			c.add(p+"/input", "input name %q must be a letter then letters, digits and _", s.Input)
		}
	case TypeInput:
		notFor("options", len(s.Options) > 0)
		notFor("done", s.Done != "")
		notFor("confirm_label", s.ConfirmLabel != "")
		notFor("cancel_label", s.CancelLabel != "")
		if !inputName.MatchString(s.Input) {
			c.add(p+"/input", "input name %q must be a letter then letters, digits and _", s.Input)
		}
		if s.Next == "" {
			c.add(p+"/next", "an input screen needs next")
		}
		link(p+"/next", s.Next)
		if v := s.Validate; v != nil {
			c.validate(p+"/validate", v)
		}
	case TypeConfirm:
		notFor("options", len(s.Options) > 0)
		notFor("input", s.Input != "")
		notFor("next", s.Next != "")
		notFor("validate", s.Validate != nil)
		c.text(p+"/done", s.Done, collected, true)
		for k, l := range map[string]string{"confirm_label": s.ConfirmLabel, "cancel_label": s.CancelLabel} {
			if !gsm.MatchString(l) || strings.Contains(l, "\n") || strings.Contains(l, "{{") {
				c.add(p+"/"+k, "labels are one line of plain ASCII")
			}
		}
	case TypeEnd:
		notFor("options", len(s.Options) > 0)
		notFor("input", s.Input != "")
		notFor("next", s.Next != "")
		notFor("validate", s.Validate != nil)
		notFor("done", s.Done != "")
		notFor("error", s.Error != "")
	default:
		c.add(p+"/type", "unknown screen type %q", s.Type)
		return
	}
	if s.Error != "" {
		c.text(p+"/error", s.Error, collected, false)
	}
	// Size: the worst case of what the caller can be shown here.
	if n := c.worst(s); n > m.Limit() {
		c.add(p+"/text", "the screen can reach %d characters (options, Back and the error line included, placeholders at their longest); the limit is %d", n, m.Limit())
	}
	if s.Type == TypeConfirm {
		if n := c.width(orDefault(s.Done, DefaultDone)); n > m.Limit() {
			c.add(p+"/done", "the final message can reach %d characters; the limit is %d", n, m.Limit())
		}
	}
}

func (c *checker) validate(p string, v *Validate) {
	switch v.Type {
	case "", "text", "number", "integer":
	default:
		c.add(p+"/type", "type is text, number or integer")
	}
	if v.Pattern != "" {
		if _, err := regexp.Compile(anchor(v.Pattern)); err != nil {
			c.add(p+"/pattern", "pattern does not compile: %v", err)
		}
	}
	if v.Min != nil && v.Max != nil && *v.Min > *v.Max {
		c.add(p+"/min", "min is above max")
	}
	if (v.Min != nil || v.Max != nil) && (v.Type == "" || v.Type == "text") {
		c.add(p+"/type", "min and max need type number or integer")
	}
	if v.MinLength != nil && v.MaxLength != nil && *v.MinLength > *v.MaxLength {
		c.add(p+"/min_length", "min_length is above max_length")
	}
	if v.When != "" {
		c.condition(p+"/when", v.When)
	}
}

// condition checks a CEL condition: an expression over trigger only.
func (c *checker) condition(p, src string) {
	if !strings.HasPrefix(src, "=") {
		c.add(p, "a condition is an expression starting with =")
		return
	}
	if err := conditions.Check(src); err != nil {
		c.add(p, "%v (conditions may read only trigger.body, the inputs so far)", err)
	}
}

// text checks a screen or message text: plain ASCII, placeholders naming
// collected inputs (and {{reference}} where allowed), never an expression.
func (c *checker) text(p, t string, collected map[string]bool, reference bool) {
	if t == "" {
		return
	}
	if strings.HasPrefix(t, "=") {
		c.add(p, "texts are fixed text with {{input}} placeholders, not expressions")
	}
	if !gsm.MatchString(t) {
		c.add(p, "texts are plain ASCII and newlines (what every handset shows)")
	}
	for _, mm := range placeholder.FindAllStringSubmatch(t, -1) {
		n := mm[1]
		switch {
		case n == "reference" && reference:
		case collected[n]:
		case n == "reference":
			c.add(p, "{{reference}} exists only once the request is confirmed")
		default:
			c.add(p, "{{%s}} is not an input this menu collects", n)
		}
	}
}

// reach reports screens the start cannot lead to.
func (c *checker) reach() {
	m := c.m
	start := m.StartScreen()
	if start == nil {
		return
	}
	seen := map[string]bool{start.ID: true}
	queue := []*Screen{start}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		var next []string
		for _, o := range s.Options {
			next = append(next, o.Next)
		}
		if s.Next != "" {
			next = append(next, s.Next)
		}
		for _, id := range next {
			if t := m.byID[id]; t != nil && !seen[id] {
				seen[id] = true
				queue = append(queue, t)
			}
		}
	}
	for i, s := range m.Screens {
		if !seen[s.ID] && m.byID[s.ID] == s {
			c.add(fmt.Sprintf("/screens/%d", i), "screen %q cannot be reached from the start", s.ID)
		}
	}
}

// worst is the longest a screen can render.
func (c *checker) worst(s *Screen) int {
	m := c.m
	n := c.width(s.Text)
	first := s == m.StartScreen()
	switch s.Type {
	case TypeMenu:
		for i, o := range s.Options {
			n += 1 + len(strconv.Itoa(i+1)) + 2 + utf8.RuneCountInString(o.Label)
		}
	case TypeConfirm:
		n += len("\n1. ") + utf8.RuneCountInString(orDefault(s.ConfirmLabel, DefaultConfirm)) + len("\n2. ") + utf8.RuneCountInString(orDefault(s.CancelLabel, DefaultCancel))
	case TypeEnd:
		return n
	}
	if !first {
		n += 1 + len(backLine)
	}
	e := DefaultChoiceError
	if s.Type == TypeInput {
		e = DefaultInputError
	}
	if s.Error != "" {
		e = s.Error
	}
	return n + c.width(e) + 1
}

// width is a text's length with every placeholder at its longest.
func (c *checker) width(t string) int {
	n := utf8.RuneCountInString(t)
	for _, mm := range placeholder.FindAllStringSubmatch(t, -1) {
		n += c.longest(mm[1]) - utf8.RuneCountInString(mm[0])
	}
	return n
}

// longest is how long a placeholder's value can be once rendered: masked
// values are 8 characters (****1234); otherwise the max_length an input
// screen allows, the longest option value of a menu, 15 for numbers, and 20
// for text without a max_length.
func (c *checker) longest(name string) int {
	if name == "reference" {
		return ReferenceLen
	}
	best := 0
	for _, s := range c.m.Screens {
		if s.Input != name {
			continue
		}
		n := 20
		switch s.Type {
		case TypeMenu:
			n = 0
			for _, o := range s.Options {
				n = max(n, utf8.RuneCountInString(display(o.Value, o.Label)))
			}
		case TypeInput:
			if v := s.Validate; v != nil {
				switch {
				case v.MaxLength != nil:
					n = *v.MaxLength
				case v.Type == "number" || v.Type == "integer":
					n = 15
				}
			}
		}
		best = max(best, n)
	}
	return max(best, maskedLen)
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
