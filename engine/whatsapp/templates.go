package whatsapp

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Template is a message template, defined here and submitted to Meta for
// approval under the same name (docs/whatsapp.md#templates). Outside a
// person's 24-hour customer service window only approved templates may be
// sent, so every notification has one. Body is the text as submitted, with
// {{1}}, {{2}} ... for Vars in order; Render fills it in, for messages sent
// inside the window and for the fake Graph API in tests.
type Template struct {
	Name     string
	Category string // AUTHENTICATION or UTILITY
	Body     string
	Vars     []string
	Buttons  []TemplateButton
	// Purpose says when it is sent, for the submission list.
	Purpose string
}

// TemplateButton is a button defined on a template.
type TemplateButton struct {
	Type string // quick_reply or copy_code
	Text string
}

// Templates (spec 11.3: every notification type has a pre-approved
// template). The tenant's name opens every tenant message: the platform
// number is shared (spec 11.4).
var (
	TplOTP = Template{
		Name: "taskiem_otp", Category: "AUTHENTICATION",
		Body: "{{1}} is your verification code. For your security, do not share this code. This code expires in 10 minutes.",
		Vars: []string{"code"}, Buttons: []TemplateButton{{Type: "copy_code", Text: "Copy code"}},
		Purpose: "The code that binds a number to a Taskiem account (Account > WhatsApp).",
	}
	TplApprovalRequest = Template{
		Name: "taskiem_approval_request", Category: "UTILITY",
		Body:    "[{{1}}] Approval needed: {{2}}. {{3}} Environment: {{4}}. Tap Approve or Reject, or decide in Taskiem.",
		Vars:    []string{"tenant", "title", "summary", "environment"},
		Buttons: []TemplateButton{{Type: "quick_reply", Text: "Approve"}, {Type: "quick_reply", Text: "Reject"}},
		Purpose: "An approval step is waiting for an approver with a bound number; the buttons carry signed decision tokens.",
	}
	TplStepUpLink = Template{
		Name: "taskiem_stepup_link", Category: "UTILITY",
		Body:    "[{{1}}] This decision on {{2}} needs your passkey or authenticator code. Confirm it in Taskiem within 10 minutes: {{3}}",
		Vars:    []string{"tenant", "what", "link"},
		Purpose: "An approval policy needs step-up: a link to confirm the decision in the web app.",
	}
	TplRunFailed = Template{
		Name: "taskiem_run_failed", Category: "UTILITY",
		Body:    "[{{1}}] {{2}} failed in {{3}}. Details: {{4}}",
		Vars:    []string{"tenant", "workflow", "environment", "link"},
		Purpose: "A run failed (alert rule run_failed, or a run started from WhatsApp).",
	}
	TplRunCompleted = Template{
		Name: "taskiem_run_completed", Category: "UTILITY",
		Body:    "[{{1}}] {{2}} completed in {{3}}. Details: {{4}}",
		Vars:    []string{"tenant", "workflow", "environment", "link"},
		Purpose: "A run started from WhatsApp completed.",
	}
	TplNeedsReconciliation = Template{
		Name: "taskiem_needs_reconciliation", Category: "UTILITY",
		Body:    "[{{1}}] {{2}} needs reconciliation in {{3}}: a call's outcome is unknown and someone must check with the provider. Details: {{4}}",
		Vars:    []string{"tenant", "workflow", "environment", "link"},
		Purpose: "Alert rule needs_reconciliation.",
	}
	TplApprovalWaiting = Template{
		Name: "taskiem_approval_waiting", Category: "UTILITY",
		Body:    "[{{1}}] {{2}} Details: {{3}}",
		Vars:    []string{"tenant", "title", "link"},
		Purpose: "Alert rule stuck_approval: an approval has waited longer than its threshold.",
	}
	TplAlert = Template{
		Name: "taskiem_alert", Category: "UTILITY",
		Body:    "[{{1}}] {{2}} Details: {{3}}",
		Vars:    []string{"tenant", "title", "link"},
		Purpose: "Any other alert rule (slow runs, drift, credentials, plan limits).",
	}
)

// AllTemplates lists the library, for documentation and tests.
var AllTemplates = []Template{TplOTP, TplApprovalRequest, TplStepUpLink, TplRunFailed, TplRunCompleted, TplNeedsReconciliation, TplApprovalWaiting, TplAlert}

var placeholder = regexp.MustCompile(`\{\{(\d+)\}\}`)

// Render fills the body with vars.
func (t Template) Render(vars map[string]string) string {
	return placeholder.ReplaceAllStringFunc(t.Body, func(m string) string {
		i, _ := strconv.Atoi(m[2 : len(m)-2])
		if i < 1 || i > len(t.Vars) {
			return m
		}
		return vars[t.Vars[i-1]]
	})
}

// param makes a variable acceptable as a template parameter: Meta refuses
// new lines, tabs and runs of more than four spaces in them.
func param(s string) string {
	s = strings.NewReplacer("\r\n", " · ", "\n", " · ", "\t", " ").Replace(s)
	for strings.Contains(s, "     ") {
		s = strings.ReplaceAll(s, "     ", " ")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		s = "-"
	}
	return clip(s, 1000)
}

// Payload is the Cloud API's template object for a send.
func (t Template) Payload(lang string, vars map[string]string, payloads []string) (map[string]any, error) {
	params := make([]any, len(t.Vars))
	for i, v := range t.Vars {
		val, ok := vars[v]
		if !ok {
			return nil, fmt.Errorf("whatsapp: template %s needs %s", t.Name, v)
		}
		params[i] = map[string]any{"type": "text", "text": param(val)}
	}
	comps := []any{map[string]any{"type": "body", "parameters": params}}
	quick := 0
	for i, b := range t.Buttons {
		switch b.Type {
		case "copy_code":
			// Authentication templates: the code again, for the copy button.
			comps = append(comps, map[string]any{"type": "button", "sub_type": "url", "index": strconv.Itoa(i),
				"parameters": []any{map[string]any{"type": "text", "text": param(vars[t.Vars[0]])}}})
		case "quick_reply":
			if quick >= len(payloads) {
				return nil, fmt.Errorf("whatsapp: template %s needs a payload for button %q", t.Name, b.Text)
			}
			comps = append(comps, map[string]any{"type": "button", "sub_type": "quick_reply", "index": strconv.Itoa(i),
				"parameters": []any{map[string]any{"type": "payload", "payload": payloads[quick]}}})
			quick++
		}
	}
	return map[string]any{"name": t.Name, "language": map[string]any{"code": lang}, "components": comps}, nil
}

// TemplateNamed finds a template in the library.
func TemplateNamed(name string) (Template, bool) {
	i := slices.IndexFunc(AllTemplates, func(t Template) bool { return t.Name == name })
	if i < 0 {
		return Template{}, false
	}
	return AllTemplates[i], true
}
