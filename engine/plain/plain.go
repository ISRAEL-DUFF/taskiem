// Package plain says the platform's problems and errors in plain words
// (no-technical-steps N5): one map from what the checks and the runtime
// report to sentences a business owner can act on, naming the step by its
// name, with where to fix it. The web app, the AI review and chat show
// these; the technical message stays for Advanced and Details.
package plain

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// Fix is where a problem is put right, as a web app path: the
// connections page, settings, or the step in the editor ("" when none).
type Fix struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

// Steps names a definition's steps: by id, and by position among the top
// level (the contract's checks point at /steps/<n>).
type Steps struct {
	byID  map[string]string
	order []string
}

// StepsOf reads the step names of a definition (nested ones too). A
// definition that does not parse names nothing.
func StepsOf(doc []byte) Steps {
	st := Steps{byID: map[string]string{}}
	var d struct {
		Steps []json.RawMessage `json:"steps"`
	}
	if json.Unmarshal(doc, &d) != nil {
		return st
	}
	var walk func(raw json.RawMessage, top bool)
	walk = func(raw json.RawMessage, top bool) {
		var s map[string]any
		if json.Unmarshal(raw, &s) != nil {
			return
		}
		id, _ := s["id"].(string)
		name, _ := s["name"].(string)
		if name == "" {
			name = strings.ReplaceAll(id, "_", " ")
		}
		if id != "" {
			st.byID[id] = name
		}
		if top {
			st.order = append(st.order, id)
		}
		var nested func(v any)
		nested = func(v any) {
			switch t := v.(type) {
			case map[string]any:
				if ss, ok := t["steps"].([]any); ok {
					for _, x := range ss {
						b, _ := json.Marshal(x)
						walk(b, false)
					}
				}
				for k, c := range t {
					if k != "steps" {
						nested(c)
					}
				}
			case []any:
				for _, c := range t {
					nested(c)
				}
			}
		}
		for k, v := range s {
			if k == "config" || k == "on_error" {
				nested(v)
			}
		}
	}
	for _, raw := range d.Steps {
		walk(raw, true)
	}
	return st
}

// Name is a step's name by id ("" for none).
func (s Steps) Name(id string) string { return s.byID[id] }

var stepPath = regexp.MustCompile(`^/steps/([^/]+)`)

// stepOf is the id and name of the step a JSON pointer is in.
func (s Steps) stepOf(path string) (id, name string) {
	m := stepPath.FindStringSubmatch(path)
	if m == nil {
		return "", ""
	}
	id = m[1]
	if n, err := strconv.Atoi(id); err == nil && n >= 0 && n < len(s.order) {
		id = s.order[n]
	}
	return id, s.byID[id]
}

type rule struct {
	re   *regexp.Regexp
	say  func(m []string, step string) string
	fix  func(m []string, stepID string) Fix
	none bool // the sentence names no step
}

func connectFix(connector string) Fix {
	return Fix{Label: "Connect it", Path: "/connections?connect=" + strings.TrimSuffix(strings.Split(connector, "@")[0], " ")}
}

func stepFix(id string) Fix {
	if id == "" {
		return Fix{}
	}
	return Fix{Label: "Open the step", Path: "#step=" + id}
}

// ServiceName is a connector's name as people know it: "paystack" is
// "Paystack".
func ServiceName(id string) string { return service(id) }

func service(id string) string {
	id = strings.Split(id, "@")[0]
	if n, ok := serviceNames[id]; ok {
		return n
	}
	if id == "" {
		return "the service"
	}
	return strings.ToUpper(id[:1]) + id[1:]
}

var serviceNames = map[string]string{"paystack": "Paystack", "googlesheets": "Google Sheets", "gmail": "Gmail", "termii": "Termii", "flutterwave": "Flutterwave", "opay": "OPay",
	"xenibase": "your tables", "moniepoint": "Moniepoint", "mono": "Mono", "dojah": "Dojah", "slack": "Slack", "telegram": "Telegram", "whatsapp": "WhatsApp",
	"africastalking": "Africa's Talking", "notify": "Tell me", "storefront": "your storefront"}

var rules = []rule{
	{re: regexp.MustCompile(`connector "?([a-z0-9_@]+)"? is not available`), say: func(m []string, step string) string {
		return step + " uses " + service(m[1]) + ", which is not available in your organisation."
	}},
	{re: regexp.MustCompile(`connection is missing "([a-z0-9_]+)"`), say: func(m []string, step string) string {
		return step + ": the connection it uses is missing its " + strings.ReplaceAll(m[1], "_", " ") + ". Connect it again."
	}, fix: func([]string, string) Fix { return Fix{Label: "Open connections", Path: "/connections"} }},
	{re: regexp.MustCompile(`no active ([a-z0-9_]+) connection in ([a-z]+)`), say: func(m []string, step string) string {
		return step + ": " + service(m[1]) + " is not connected."
	}, fix: func(m []string, _ string) Fix { return connectFix(m[1]) }},
	{re: regexp.MustCompile(`(\d+) active ([a-z0-9_]+) connections in [a-z]+; name one`), say: func(m []string, step string) string {
		return step + ": you have more than one " + service(m[2]) + " connection. Choose which one it uses."
	}, fix: func(_ []string, id string) Fix { return stepFix(id) }},
	{re: regexp.MustCompile(`steps\.([A-Za-z0-9_]+) may not have run before ([A-Za-z0-9_]+)`), say: func(m []string, step string) string {
		return step + " uses what " + strings.ReplaceAll(m[1], "_", " ") + " gives before it has run. Make it come after " + strings.ReplaceAll(m[1], "_", " ") + "."
	}, fix: func(_ []string, id string) Fix { return stepFix(id) }},
	{re: regexp.MustCompile(`unknown step "?([A-Za-z0-9_]+)"?`), say: func(m []string, step string) string {
		return step + " refers to a step that is not in the workflow (" + strings.ReplaceAll(m[1], "_", " ") + ")."
	}, fix: func(_ []string, id string) Fix { return stepFix(id) }},
	{re: regexp.MustCompile(`approval policy "?([a-z0-9_]+)"? (has no active version|was not active)`), say: func(m []string, _ string) string {
		return "The approval rules \"" + strings.ReplaceAll(m[1], "_", " ") + "\" are not switched on yet."
	}, fix: func([]string, string) Fix { return Fix{Label: "Open approval rules", Path: "/policies"} }, none: true},
	{re: regexp.MustCompile(`missing propert(y|ies):? ['"]?([A-Za-z0-9_, '"]+)`), say: func(m []string, step string) string {
		return step + " needs " + strings.Trim(strings.ReplaceAll(m[2], "_", " "), " '\"") + "."
	}, fix: func(_ []string, id string) Fix { return stepFix(id) }},
	{re: regexp.MustCompile(`(undeclared reference|Syntax error|ERROR: <input>|no such key|found no matching overload)`), say: func(_ []string, step string) string {
		return step + " has a formula Taskiem cannot work out. Open the step to set it again."
	}, fix: func(_ []string, id string) Fix { return stepFix(id) }},
	{re: regexp.MustCompile(`(?i)\b(401|unauthori[sz]ed|invalid (api )?key|invalid_token|forbidden)\b`), say: func(_ []string, step string) string {
		return step + ": the service refused the connection's key. Connect it again with the right key."
	}, fix: func([]string, string) Fix { return Fix{Label: "Open connections", Path: "/connections"} }},
	{re: regexp.MustCompile(`(?i)(context deadline exceeded|timed? ?out|i/o timeout)`), say: func(_ []string, step string) string {
		return step + ": the service did not answer in time."
	}},
	{re: regexp.MustCompile(`(?i)(connection refused|no such host|egress: .* (refused|not allowed))`), say: func(_ []string, step string) string {
		return step + ": Taskiem could not reach the service."
	}},
}

var technical = regexp.MustCompile(`/steps/[^\s:]+|wd-test/v1|wd/v1|\$\.[A-Za-z0-9_.\[\]]+`)

// Problem says a check's problem at path in plain words, with its fix.
func Problem(path, message string, steps Steps) (string, Fix) {
	id, name := steps.stepOf(path)
	if name == "" && id != "" {
		name = strings.ReplaceAll(id, "_", " ")
	}
	step := "This workflow"
	if name != "" {
		step = "“" + name + "”"
	}
	for _, r := range rules {
		m := r.re.FindStringSubmatch(message)
		if m == nil {
			continue
		}
		var f Fix
		if r.fix != nil {
			f = r.fix(m, id)
		}
		return upper(r.say(m, step)), f
	}
	// Anything else: the message without paths and contract names.
	text := strings.TrimSpace(technical.ReplaceAllString(message, ""))
	text = strings.TrimPrefix(text, ": ")
	if name != "" {
		text = step + ": " + text
	}
	if !strings.HasSuffix(text, ".") {
		text += "."
	}
	return upper(text), stepFix(id)
}

// RunError says why a step failed, in plain words.
func RunError(stepName, message string) string {
	text, _ := Problem("", message, Steps{})
	if stepName != "" {
		text = strings.Replace(text, "This workflow", "“"+stepName+"”", 1)
	}
	return text
}

func upper(s string) string {
	if s == "" {
		return s
	}
	if strings.HasPrefix(s, "“") {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
