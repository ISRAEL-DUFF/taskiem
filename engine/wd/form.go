package wd

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/expr"
)

// Forms (docs/spec/forms.md): a form trigger puts a page in front of a
// workflow whose fields are its inputs schema; a form step asks someone
// for answers mid-run. Both are checked here when a definition is
// validated: pages, conditions, and the fields a form never collects.

// FormTrigger is a form trigger's config (forms 6.1).
type FormTrigger struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	// Access is public (anyone with the link), link (one-time links) or
	// members (signed in to Taskiem, optionally in Roles).
	Access string   `json:"access,omitempty"`
	Roles  []string `json:"roles,omitempty"`
	// Verify is phone or email: a code sent there before submitting.
	Verify string     `json:"verify,omitempty"`
	Pages  []FormPage `json:"pages,omitempty"`
	After  FormAfter  `json:"after,omitempty"`
	// OncePer refuses a second submission from the same phone, email or
	// member.
	OncePer string `json:"once_per,omitempty"`
}

// FormPage groups fields; ShowIf (CEL over answers so far) hides it.
type FormPage struct {
	Title  string   `json:"title,omitempty"`
	Fields []string `json:"fields"`
	ShowIf string   `json:"show_if,omitempty"`
}

// FormAfter is what the respondent sees after submitting (forms 6.6).
type FormAfter struct {
	Mode    string `json:"mode,omitempty"` // message (default), redirect or result
	Message string `json:"message,omitempty"`
	URL     string `json:"url,omitempty"`
	Status  bool   `json:"status,omitempty"`
}

// Form access and modes.
var (
	FormAccess  = []string{"public", "link", "members"}
	FormVerify  = []string{"", "phone", "email"}
	FormModes   = []string{"", "message", "redirect", "result"}
	FormOncePer = []string{"", "phone", "email", "member"}
	FormVia     = []string{"sms", "email"}
)

// FormTriggerConfig parses a form trigger's config, with defaults filled.
func (d *Definition) FormTriggerConfig() (FormTrigger, error) {
	var f FormTrigger
	if d.Trigger.Type != "form" {
		return f, fmt.Errorf("not a form trigger")
	}
	if len(d.Trigger.RawCfg) > 0 {
		if err := json.Unmarshal(d.Trigger.RawCfg, &f); err != nil {
			return f, err
		}
	}
	if f.Access == "" {
		f.Access = "public"
	}
	if f.After.Mode == "" {
		f.After.Mode = "message"
	}
	return f, nil
}

// FormPages are a form's pages over a schema: the trigger's own, or one
// page with every property in schema order.
func FormPages(pages []FormPage, schema map[string]any) []FormPage {
	if len(pages) > 0 {
		return pages
	}
	return []FormPage{{Fields: SchemaOrder(schema)}}
}

// SchemaOrder lists a schema's properties in the order written, which a
// map loses: x-form.order when given, else required first, then the rest
// by name.
func SchemaOrder(schema map[string]any) []string {
	props, _ := schema["properties"].(map[string]any)
	var names []string
	for k := range props {
		names = append(names, k)
	}
	order := func(k string) (int, bool) {
		p, _ := props[k].(map[string]any)
		h, _ := p["x-form"].(map[string]any)
		switch n := h["order"].(type) {
		case int64:
			return int(n), true
		case float64:
			return int(n), true
		}
		return 0, false
	}
	req := map[string]bool{}
	if r, ok := schema["required"].([]any); ok {
		for _, x := range r {
			if s, ok := x.(string); ok {
				req[s] = true
			}
		}
	}
	slices.SortStableFunc(names, func(a, b string) int {
		oa, ha := order(a)
		ob, hb := order(b)
		switch {
		case ha && hb && oa != ob:
			return oa - ob
		case ha != hb:
			if ha {
				return -1
			}
			return 1
		case req[a] != req[b]:
			if req[a] {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	})
	return names
}

// answerRef finds the fields a condition reads: answers.x, answers["x"].
var answerRef = regexp.MustCompile(`answers(?:\.([A-Za-z_][A-Za-z0-9_]*)|\[["']([^"']+)["']\])`)

// formEngine compiles show_if conditions: they read answers only.
var formEngine = expr.MustNewWithRoots("answers")

// FormCondition is a show_if without its optional leading "=".
func FormCondition(s string) string { return strings.TrimPrefix(strings.TrimSpace(s), "=") }

// Field names and x-pii categories a form never collects (forms 7.2):
// credentials belong in the vault.
var (
	refusedName = regexp.MustCompile(`(?i)(^|_)(password|passwd|passcode|pin|cvv|cvc|otp|token|secret)($|_)|card_?number|card_?no|pan$|internet_?banking|bank_?login`)
	refusedPII  = []string{"card", "card_number", "cvv", "pin", "password", "otp", "credential", "bank_login"}
	// sensitivePII may be asked only when marked (so it is sealed), and
	// not on a public form without a verification code.
	sensitivePII  = []string{"bvn", "nin", "account_number", "passport", "drivers_licence"}
	sensitiveName = regexp.MustCompile(`(?i)(^|_)(bvn|nin|account_?number|acct_?no|nuban)($|_)`)
)

// FormFieldProblems checks a form's fields: none a form never collects,
// identifiers only when marked x-pii and the form is not anonymous, and
// file fields well formed. anonymous is a public form with no code.
func FormFieldProblems(schema map[string]any, anonymous bool, path string) []Problem {
	var out []Problem
	props, _ := schema["properties"].(map[string]any)
	for _, name := range SchemaOrder(schema) {
		p, _ := props[name].(map[string]any)
		at := path + "/properties/" + name
		cat, _ := p["x-pii"].(string)
		if refusedName.MatchString(name) || slices.Contains(refusedPII, cat) {
			out = append(out, Problem{Path: at, Message: fmt.Sprintf("a form never asks for passwords, PINs, card details or one-time codes (%q)", name)})
			continue
		}
		if slices.Contains(sensitivePII, cat) || sensitiveName.MatchString(name) {
			switch {
			case cat == "":
				out = append(out, Problem{Path: at, Message: fmt.Sprintf("%q looks like an identifier: mark it with its x-pii category so it is sealed", name)})
			case anonymous:
				out = append(out, Problem{Path: at, Message: fmt.Sprintf("%q may be asked only on a link or members form, or a public form with verify on", name)})
			}
		}
		if t, _ := p["type"].(string); t == "object" || (t == "array" && !isEnumArray(p)) {
			out = append(out, Problem{Path: at, Message: fmt.Sprintf("%q: nested fields and lists are not in forms yet; use plain fields or a list of choices", name)})
		}
		if f, _ := p["format"].(string); f == "file" {
			h, _ := p["x-form"].(map[string]any)
			if mb, ok := num(h["max_mb"]); ok && (mb < 1 || mb > 25) {
				out = append(out, Problem{Path: at + "/x-form/max_mb", Message: "a file is at most 25 MB"})
			}
		}
	}
	return out
}

func isEnumArray(p map[string]any) bool {
	items, _ := p["items"].(map[string]any)
	_, ok := items["enum"]
	return ok
}

func num(v any) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// HasFileFields reports whether a schema asks for files.
func HasFileFields(schema map[string]any) bool {
	props, _ := schema["properties"].(map[string]any)
	for _, v := range props {
		p, _ := v.(map[string]any)
		if f, _ := p["format"].(string); f == "file" {
			return true
		}
	}
	return false
}

func checkForms(d *Definition) []Problem {
	var out []Problem
	if d.Trigger.Type == "form" {
		out = append(out, checkFormTrigger(d)...)
	}
	var walk func([]*Step, string)
	walk = func(steps []*Step, path string) {
		for i, s := range steps {
			at := fmt.Sprintf("%s/%d", path, i)
			if s.Type == "form" && s.Form != nil {
				out = append(out, checkFormStep(s.Form, at+"/config")...)
			}
			for _, c := range s.Children() {
				walk(c, at+"/config")
			}
		}
	}
	walk(d.Steps, "/steps")
	return out
}

func checkFormTrigger(d *Definition) []Problem {
	f, err := d.FormTriggerConfig()
	if err != nil {
		return []Problem{{Path: "/trigger/config", Message: err.Error()}}
	}
	var out []Problem
	add := func(path, msg string, args ...any) {
		out = append(out, Problem{Path: "/trigger/config" + path, Message: fmt.Sprintf(msg, args...)})
	}
	if strings.TrimSpace(f.Title) == "" {
		add("/title", "a form needs a title")
	}
	if !slices.Contains(FormAccess, f.Access) {
		add("/access", "access is public, link or members")
	}
	if !slices.Contains(FormVerify, f.Verify) || (f.Verify != "" && f.Access == "members") {
		add("/verify", "verify is phone or email, and members forms need none")
	}
	if !slices.Contains(FormModes, f.After.Mode) {
		add("/after/mode", "after.mode is message, redirect or result")
	}
	if f.After.Mode == "redirect" && !strings.HasPrefix(f.After.URL, "https://") {
		add("/after/url", "a redirect needs an https URL on one of the organisation's verified websites")
	}
	if !slices.Contains(FormOncePer, f.OncePer) {
		add("/once_per", "once_per is phone, email or member")
	}
	raw, _ := d.InputsSchema()
	schema, _ := raw.(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		add("", "a form needs fields: give the workflow an inputs schema")
	}
	seen := map[string]int{}
	var earlier []string
	for i, pg := range FormPages(f.Pages, schema) {
		for _, name := range pg.Fields {
			if _, ok := props[name]; !ok {
				add(fmt.Sprintf("/pages/%d/fields", i), "%q is not in the inputs schema", name)
				continue
			}
			if j, dup := seen[name]; dup {
				add(fmt.Sprintf("/pages/%d/fields", i), "%q is on page %d already", name, j+1)
			}
			seen[name] = i
		}
		if c := FormCondition(pg.ShowIf); c != "" {
			if i == 0 {
				add(fmt.Sprintf("/pages/%d/show_if", i), "the first page is always shown")
			} else if err := formEngine.Check(c); err != nil {
				add(fmt.Sprintf("/pages/%d/show_if", i), "the condition does not compile: %v", err)
			} else {
				for _, m := range answerRef.FindAllStringSubmatch(c, -1) {
					if r := m[1] + m[2]; !slices.Contains(earlier, r) {
						add(fmt.Sprintf("/pages/%d/show_if", i), "the condition reads %q, which is not on an earlier page", r)
					}
				}
			}
		}
		earlier = append(earlier, pg.Fields...)
	}
	for name := range props {
		p, _ := props[name].(map[string]any)
		h, _ := p["x-form"].(map[string]any)
		if _, ok := seen[name]; !ok && h["hidden"] != true {
			add("/pages", "%q is on no page (put it on one, or hide it)", name)
		}
	}
	anonymous := f.Access == "public" && f.Verify == ""
	for _, p := range FormFieldProblems(schema, anonymous, "/inputs/schema") {
		out = append(out, p)
	}
	return out
}

func checkFormStep(f *FormConfig, path string) []Problem {
	var out []Problem
	add := func(sub, msg string) { out = append(out, Problem{Path: path + sub, Message: msg}) }
	if strings.TrimSpace(f.Title) == "" {
		add("/title", "a form step needs a title")
	}
	n := 0
	for _, v := range []string{f.To.Phone, f.To.Email, f.To.Role} {
		if v != "" {
			n++
		}
	}
	if n != 1 {
		add("/to", "to is exactly one of phone, email or role")
	}
	for _, v := range f.Via {
		if !slices.Contains(FormVia, v) {
			add("/via", "via is sms and email")
		}
	}
	if f.Verify && f.To.Role != "" {
		add("/verify", "members are signed in: verify is for a phone or email")
	}
	if f.OnTimeout != "" && f.OnTimeout != "fail" && f.OnTimeout != "continue" {
		add("/on_timeout", "on_timeout is fail or continue")
	}
	timeout := 7 * 24 * time.Hour
	if f.Timeout != "" {
		if d, err := ParseDuration(f.Timeout); err == nil {
			timeout = d
		}
	}
	for i, r := range f.Remind {
		d, err := ParseDuration(r)
		if err != nil || d <= 0 || d >= timeout {
			add(fmt.Sprintf("/remind/%d", i), "a reminder is a duration shorter than the timeout")
		}
	}
	if props, _ := f.Schema["properties"].(map[string]any); len(props) == 0 {
		add("/schema", "a form step needs questions: a schema with properties")
	}
	// A one-time link to a person named in the run's data: not anonymous.
	out = append(out, FormFieldProblems(f.Schema, false, path+"/schema")...)
	return out
}
