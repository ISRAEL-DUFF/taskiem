package whatsapp

import (
	"errors"
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
	// Examples are sample values for Vars, in order: Meta asks for one per
	// variable when a template is submitted.
	Examples []string
	// Purpose says when it is sent, for the submission list.
	Purpose string
	// Tell marks a template a workflow's Tell me step (notify@1 tell) may
	// carry its message as outside the window, in place of taskiem_alert:
	// one for the business's own people, filled from the step's one-line
	// variables alone (TellTemplate).
	Tell bool
}

// TemplateButton is a button defined on a template: a quick reply (its
// payload is given at each send), a copy-code button (authentication
// templates), or a URL button. A URL button opens Host (the web app or
// the public form host, named at submission) followed by Path; a Path
// ending in {{1}} is dynamic, and the value of Var (a full link under
// that host and path) supplies the suffix at each send.
type TemplateButton struct {
	Type string // quick_reply, copy_code or url
	Text string
	// Host names the deployment host a url button opens: HostApp (the
	// web app, TASKIEM_PUBLIC_URL) or HostForms (the public form host,
	// where invoice, receipt and family pages are served).
	Host string
	// Path is the button's path on Host, ending in {{1}} when dynamic.
	Path string
	// Var is the variable holding the full link a dynamic url button
	// opens (given with the body's Vars at each send, not one of them);
	// the suffix after Host and Path is what is sent.
	Var string
	// Example is the sample suffix Meta asks for on a dynamic url button.
	Example string
}

// Hosts a url button may open.
const (
	HostApp   = "app"
	HostForms = "forms"
)

// Hosts are the deployment's addresses url buttons are built from: App
// is the web app (TASKIEM_PUBLIC_URL), Forms the public form host
// (https://<form host>), each without a trailing slash; "" when unset.
type Hosts struct {
	App, Forms string
}

// Of is the address a host name stands for.
func (h Hosts) Of(host string) string {
	switch host {
	case HostApp:
		return strings.TrimRight(h.App, "/")
	case HostForms:
		return strings.TrimRight(h.Forms, "/")
	}
	return ""
}

// Templates (spec 11.3: every notification type has a pre-approved
// template). The tenant's name opens every tenant message: the platform
// number is shared (spec 11.4).
var (
	TplOTP = Template{
		Name: "taskiem_otp", Category: "AUTHENTICATION",
		Body: "{{1}} is your verification code. For your security, do not share this code. This code expires in 10 minutes.",
		Vars: []string{"code"}, Examples: []string{"123456"}, Buttons: []TemplateButton{{Type: "copy_code", Text: "Copy code"}},
		Purpose: "The code that binds a number to a Taskiem account (Account > WhatsApp).",
	}
	TplApprovalRequest = Template{
		Name: "taskiem_approval_request", Category: "UTILITY",
		Body:     "[{{1}}] Approval needed: {{2}}. {{3}} Environment: {{4}}. Tap Approve or Reject, or decide in Taskiem.",
		Vars:     []string{"tenant", "title", "summary", "environment"},
		Examples: []string{"Ada Stores", "Pay supplier", "Amount: 250,000; Supplier: K. Ltd", "prod"},
		Buttons:  []TemplateButton{{Type: "quick_reply", Text: "Approve"}, {Type: "quick_reply", Text: "Reject"}},
		Purpose:  "An approval step is waiting for an approver with a bound number; the buttons carry signed decision tokens.",
	}
	TplStepUpLink = Template{
		Name: "taskiem_stepup_link", Category: "UTILITY",
		Body:     "[{{1}}] This decision on {{2}} needs your passkey or authenticator code. Confirm it in Taskiem within 10 minutes: {{3}}",
		Vars:     []string{"tenant", "what", "link"},
		Examples: []string{"Ada Stores", "Pay supplier", "https://app.example.com/approvals/step-up"},
		Purpose:  "An approval policy needs step-up: a link to confirm the decision in the web app.",
	}
	TplRunFailed = Template{
		Name: "taskiem_run_failed", Category: "UTILITY",
		Body:     "[{{1}}] {{2}} failed in {{3}}. Details: {{4}}",
		Vars:     []string{"tenant", "workflow", "environment", "link"},
		Examples: []string{"Ada Stores", "Daily sales report", "prod", "https://app.example.com/runs/1"},
		Purpose:  "A run failed (alert rule run_failed, or a run started from WhatsApp).",
	}
	TplRunCompleted = Template{
		Name: "taskiem_run_completed", Category: "UTILITY",
		Body:     "[{{1}}] {{2}} completed in {{3}}. Details: {{4}}",
		Vars:     []string{"tenant", "workflow", "environment", "link"},
		Examples: []string{"Ada Stores", "Daily sales report", "prod", "https://app.example.com/runs/1"},
		Purpose:  "A run started from WhatsApp completed.",
	}
	TplNeedsReconciliation = Template{
		Name: "taskiem_needs_reconciliation", Category: "UTILITY",
		Body:     "[{{1}}] {{2}} needs reconciliation in {{3}}: a call's outcome is unknown and someone must check with the provider. Details: {{4}}",
		Vars:     []string{"tenant", "workflow", "environment", "link"},
		Examples: []string{"Ada Stores", "Pay supplier", "prod", "https://app.example.com/runs/1"},
		Purpose:  "Alert rule needs_reconciliation.",
	}
	TplApprovalWaiting = Template{
		Name: "taskiem_approval_waiting", Category: "UTILITY",
		Body:     "[{{1}}] {{2}} Details: {{3}}",
		Vars:     []string{"tenant", "title", "link"},
		Examples: []string{"Ada Stores", "An approval has waited 2 hours.", "https://app.example.com/approvals"},
		Purpose:  "Alert rule stuck_approval: an approval has waited longer than its threshold.",
	}
	TplAlert = Template{
		Name: "taskiem_alert", Category: "UTILITY",
		Body:     "[{{1}}] {{2}} Details: {{3}}",
		Vars:     []string{"tenant", "title", "link"},
		Examples: []string{"Ada Stores", "A run took longer than 10 minutes.", "https://app.example.com/runs/1"},
		Purpose:  "Any other alert rule (slow runs, drift, credentials, plan limits).",
	}
	TplUpdate = Template{
		Name: "taskiem_update", Category: "UTILITY",
		Body:     "[{{1}}] Update on {{2}}: {{3}}",
		Vars:     []string{"tenant", "about", "message"},
		Examples: []string{"Ada Stores", "your leave request", "Your leave is approved."},
		Purpose:  "A workflow's message to someone outside the business who asked for updates (notify@1 message): about says what they asked about.",
	}

	// Money and schools (docs/invoices.md, docs/schools.md): what goes to
	// customers, families and owners outside the window once approved.
	TplInvoice = Template{
		Name: "taskiem_invoice", Category: "UTILITY",
		Body:     "[{{1}}] Invoice {{2}}: {{3}} to pay. Tap Pay by transfer for an account number to pay into, or Open invoice to see the invoice and pay.",
		Vars:     []string{"tenant", "number", "amount"},
		Examples: []string{"Ada Stores", "INV-0104", "₦25,000"},
		Buttons: []TemplateButton{{Type: "quick_reply", Text: "Pay by transfer"},
			{Type: "url", Text: "Open invoice", Host: HostForms, Path: "/i/{{1}}", Var: "link", Example: "abc123"}},
		Purpose: "An invoice to a customer who gets the business's updates on WhatsApp, outside the window; the quick reply carries the signed invoice button, the url button opens the invoice's page.",
	}
	// Dispatch (docs/dispatch.md): an offer to a business's rider outside
	// the window; the quick replies carry the signed Accept and Can't.
	TplDeliveryOffer = Template{
		Name: "taskiem_delivery_offer", Category: "UTILITY",
		Body:     "[{{1}}] A delivery for you: {{2}}. Tap Accept to take it or Can't to pass it on.",
		Vars:     []string{"tenant", "summary"},
		Examples: []string{"Ada Stores", "AK-7QX4KD to Lekki, 2 × Jollof rice, your fee ₦1,500"},
		Buttons:  []TemplateButton{{Type: "quick_reply", Text: "Accept"}, {Type: "quick_reply", Text: "Can't"}},
		Purpose:  "An order offered to one of the business's riders outside the window; the quick replies carry the signed Accept and Can't.",
	}
	TplMatchQuestion = Template{
		Name: "taskiem_match_question", Category: "UTILITY",
		Body:     "[{{1}}] {{2}} came in on {{3}} through {{4}}. Is this for {{5}}, {{6}} due? {{7}} Tap Yes, match or No, or decide in Taskiem.",
		Vars:     []string{"tenant", "amount", "when", "channel", "number", "due", "reasons"},
		Examples: []string{"Ada Stores", "₦25,000", "23 Oct 14:05", "Paystack (main)", "INV-0104", "₦25,000", "Same amount as what is due on INV-0104."},
		Buttons:  []TemplateButton{{Type: "quick_reply", Text: "Yes, match"}, {Type: "quick_reply", Text: "No"}},
		Purpose:  "A payment to match to an invoice or order, to a member with invoice.manage outside the window; the quick replies carry the signed match buttons.",
	}
	TplFeeReminder = Template{
		Name: "taskiem_fee_reminder", Category: "UTILITY",
		Body:     "[{{1}}] School fees, {{2}}: {{3}} is outstanding on {{4}}'s fees, due {{5}}. Pay by transfer to the account number {{6}}. Your fees page has the details and lets you ask for a payment plan.",
		Vars:     []string{"tenant", "rung", "balance", "child", "due_date", "account_number"},
		Examples: []string{"Bright Stars School", "reminder 1 of 3", "₦45,000", "Ada", "15 Nov 2026", "9900112233"},
		Buttons:  []TemplateButton{{Type: "url", Text: "Open fees page", Host: HostForms, Path: "/s/{{1}}", Var: "link", Example: "abc123"}},
		Purpose:  "A fee reminder (any rung) to a family outside the window: fixed wording with the school's variables; the school's own wording goes inside the window and by SMS.",
	}
	TplReceipt = Template{
		Name: "taskiem_receipt", Category: "UTILITY",
		Body:     "[{{1}}] Receipt {{2}}: we received {{3}} from {{4}} towards {{5}}. {{6}} Tap Check receipt to confirm it is valid.",
		Vars:     []string{"tenant", "number", "amount", "who", "what", "rest"},
		Examples: []string{"Bright Stars School", "BSS/2026/1/0007", "₦20,000", "Ada", "INV-0104 (school fees)", "₦25,000 is still left on it."},
		Buttons:  []TemplateButton{{Type: "url", Text: "Check receipt", Host: HostForms, Path: "/r/{{1}}", Var: "link", Example: "abc123"}},
		Purpose:  "A receipt for a payment counted towards a child's or customer's invoice, outside the window.",
	}
	TplAccountNumber = Template{
		Name: "taskiem_account_number", Category: "UTILITY",
		Body:     "[{{1}}] {{2}}'s own account number for payments is {{3}} at {{4}}, in the name {{5}}. Pay by transfer to it: the money is matched by itself and a receipt follows.",
		Vars:     []string{"tenant", "who", "account_number", "bank", "account_name"},
		Examples: []string{"Bright Stars School", "Ada", "9900112233", "Wema Bank", "BSS - ADA OKAFOR"},
		Buttons:  []TemplateButton{{Type: "url", Text: "Open fees page", Host: HostForms, Path: "/s/{{1}}", Var: "link", Example: "abc123"}},
		Purpose:  "A student's or customer's own account number, outside the window.",
	}
	TplPlanDecision = Template{
		Name: "taskiem_plan_decision", Category: "UTILITY",
		Body:     "[{{1}}] Your payment plan request for {{2}}'s school fees: {{3}}. {{4}} Open your fees page for the details.",
		Vars:     []string{"tenant", "child", "decision", "detail"},
		Examples: []string{"Bright Stars School", "Ada", "approved", "2 instalments: ₦20,000 by 15 Nov 2026; ₦25,000 by 15 Dec 2026."},
		Buttons:  []TemplateButton{{Type: "url", Text: "Open fees page", Host: HostForms, Path: "/s/{{1}}", Var: "link", Example: "abc123"}},
		Purpose:  "A payment plan decided (approved, with the instalments in one line, or declined), outside the window.",
	}
	TplFamilyLink = Template{
		Name: "taskiem_family_link", Category: "UTILITY",
		Body:     "[{{1}}] {{2}} Tap Open fees page for your family's school fees page: each child's fees, payments and account number. The link works for {{3}} days.",
		Vars:     []string{"tenant", "message", "days"},
		Examples: []string{"Bright Stars School", "Here is a new link to your family's fees page.", "3"},
		Buttons:  []TemplateButton{{Type: "url", Text: "Open fees page", Host: HostForms, Path: "/s/{{1}}", Var: "link", Example: "abc123"}},
		Purpose:  "A fresh family page link (Send me a new link, a link sent by staff), outside the window.",
	}
	TplDailySummary = Template{
		Name: "taskiem_daily_summary", Category: "UTILITY",
		Body:     "[{{1}}] Yesterday's money, {{2}}: {{3}} Tap Open for the Money page.",
		Vars:     []string{"tenant", "day", "summary"},
		Examples: []string{"Ada Stores", "23 Oct 2026", "₦300,000 in — Paystack ₦250,000, POS ₦50,000; ₦12,000 out; 1 invoice paid; no leak opened."},
		Buttons:  []TemplateButton{{Type: "url", Text: "Open", Host: HostApp, Path: "/money"}},
		Purpose:  "An owner's daily money summary, outside the window (in place of taskiem_alert cut to one line).",
		Tell:     true,
	}
	TplDailyClose = Template{
		Name: "taskiem_daily_close", Category: "UTILITY",
		Body:     "[{{1}}] Daily close, {{2}}: {{3}} Tap Open for every item and where to fix it.",
		Vars:     []string{"tenant", "day", "summary"},
		Examples: []string{"Bright Stars School", "23 Oct 2026", "₦450,000 in on students (12 payments); matched ₦450,000; not matched ₦0; everything reconciles."},
		Buttons:  []TemplateButton{{Type: "url", Text: "Open", Host: HostApp, Path: "/schools/overview"}},
		Purpose:  "A school's daily close to the proprietor, outside the window (the school-daily-close template's notify@1 tell names it as its template).",
		Tell:     true,
	}
)

// AllTemplates lists the library, for documentation, submission and tests.
var AllTemplates = []Template{TplOTP, TplApprovalRequest, TplStepUpLink, TplRunFailed, TplRunCompleted, TplNeedsReconciliation, TplApprovalWaiting, TplAlert, TplUpdate,
	TplInvoice, TplDeliveryOffer, TplMatchQuestion, TplFeeReminder, TplReceipt, TplAccountNumber, TplPlanDecision, TplFamilyLink, TplDailySummary, TplDailyClose}

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

// ErrTemplateLink: a dynamic url button's link is not under the host and
// path the template was submitted with, so the template cannot carry it.
var ErrTemplateLink = errors.New("whatsapp: the link is not one the template's button can open")

// ErrNoHost: a url button's host (the web app's address or the public
// form host) is not configured on this deployment, so the template
// cannot be submitted.
var ErrNoHost = errors.New("whatsapp: the deployment has no address for the template's button")

// Payload is the Cloud API's template object for a send: the body
// variables, the quick replies' payloads in order, and the suffix of each
// dynamic url button taken from its Var under hosts.
func (t Template) Payload(lang string, vars map[string]string, payloads []string, hosts Hosts) (map[string]any, error) {
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
		case "url":
			if !b.Dynamic() {
				continue
			}
			link, ok := vars[b.Var]
			if !ok {
				return nil, fmt.Errorf("whatsapp: template %s needs %s for button %q", t.Name, b.Var, b.Text)
			}
			suffix, err := b.Suffix(hosts, link)
			if err != nil {
				return nil, fmt.Errorf("template %s, button %q: %w", t.Name, b.Text, err)
			}
			comps = append(comps, map[string]any{"type": "button", "sub_type": "url", "index": strconv.Itoa(i),
				"parameters": []any{map[string]any{"type": "text", "text": suffix}}})
		}
	}
	return map[string]any{"name": t.Name, "language": map[string]any{"code": lang}, "components": comps}, nil
}

// Dynamic reports whether a url button takes a suffix at each send.
func (b TemplateButton) Dynamic() bool { return b.Type == "url" && strings.HasSuffix(b.Path, "{{1}}") }

// URL is the button's address as submitted: the host's address and the
// path, with {{1}} left for a dynamic suffix; "" when the host is unset.
func (b TemplateButton) URL(hosts Hosts) string {
	h := hosts.Of(b.Host)
	if h == "" {
		return ""
	}
	return h + b.Path
}

// Suffix is what a dynamic url button is sent: the rest of link after
// the button's host and path; ErrTemplateLink when link is not under it.
func (b TemplateButton) Suffix(hosts Hosts, link string) (string, error) {
	base := strings.TrimSuffix(b.URL(hosts), "{{1}}")
	if base == "" {
		return "", fmt.Errorf("%w: the %s host is not configured", ErrTemplateLink, b.Host)
	}
	rest, ok := strings.CutPrefix(link, base)
	if !ok || rest == "" || strings.ContainsAny(rest, "/?#") {
		return "", fmt.Errorf("%w: not under %s", ErrTemplateLink, base)
	}
	return rest, nil
}

// Submission is the template as submitted to the Business Management API
// (POST /{waba-id}/message_templates): its name, language, category and
// components, with an example for each variable and dynamic button.
// Authentication templates are submitted in Meta's fixed format (a body
// with the security recommendation, a footer with the expiry, a copy-code
// button). URL buttons need their hosts.
func (t Template) Submission(lang string, hosts Hosts) (map[string]any, error) {
	if t.Category == "AUTHENTICATION" {
		return map[string]any{"name": t.Name, "language": lang, "category": t.Category, "components": []any{
			map[string]any{"type": "BODY", "add_security_recommendation": true},
			map[string]any{"type": "FOOTER", "code_expiration_minutes": 10},
			map[string]any{"type": "BUTTONS", "buttons": []any{map[string]any{"type": "OTP", "otp_type": "COPY_CODE"}}},
		}}, nil
	}
	if len(t.Examples) != len(t.Vars) {
		return nil, fmt.Errorf("whatsapp: template %s has %d examples for %d variables", t.Name, len(t.Examples), len(t.Vars))
	}
	body := map[string]any{"type": "BODY", "text": t.Body}
	if len(t.Vars) > 0 {
		body["example"] = map[string]any{"body_text": []any{t.Examples}}
	}
	comps := []any{body}
	var buttons []any
	for _, b := range t.Buttons {
		switch b.Type {
		case "quick_reply":
			buttons = append(buttons, map[string]any{"type": "QUICK_REPLY", "text": b.Text})
		case "url":
			u := b.URL(hosts)
			if u == "" {
				return nil, fmt.Errorf("%w: template %s: the %s host is not configured, so the %q button has no address", ErrNoHost, t.Name, b.Host, b.Text)
			}
			btn := map[string]any{"type": "URL", "text": b.Text, "url": u}
			if b.Dynamic() {
				btn["example"] = []any{b.Example}
			}
			buttons = append(buttons, btn)
		}
	}
	if len(buttons) > 0 {
		comps = append(comps, map[string]any{"type": "BUTTONS", "buttons": buttons})
	}
	return map[string]any{"name": t.Name, "language": lang, "category": t.Category, "components": comps}, nil
}

// TellTemplate is a template a workflow's Tell me step (notify@1 tell)
// may carry its message as outside the window, by name: one marked Tell
// (taskiem_daily_close, taskiem_daily_summary), a utility template for
// the business's own people whose body is filled from one-line text
// alone, the tenant's name by Taskiem and the rest by the step
// (template_vars), and whose buttons need nothing at each send (no quick
// reply, no dynamic link). An invoice's or a family's template, with its
// signed buttons and page links, is not one, nor are the platform's own
// (approvals, run outcomes).
func TellTemplate(name string) (Template, bool) {
	t, ok := TemplateNamed(name)
	if !ok || !t.Tell || t.Category != "UTILITY" {
		return Template{}, false
	}
	for _, b := range t.Buttons {
		if b.Type != "url" || b.Dynamic() {
			return Template{}, false
		}
	}
	return t, true
}

// TellVars are the variables a Tell me step gives a template: its Vars
// but the tenant's name, in order.
func TellVars(t Template) []string {
	var out []string
	for _, v := range t.Vars {
		if v != "tenant" {
			out = append(out, v)
		}
	}
	return out
}

// TemplateNamed finds a template in the library.
func TemplateNamed(name string) (Template, bool) {
	i := slices.IndexFunc(AllTemplates, func(t Template) bool { return t.Name == name })
	if i < 0 {
		return Template{}, false
	}
	return AllTemplates[i], true
}
