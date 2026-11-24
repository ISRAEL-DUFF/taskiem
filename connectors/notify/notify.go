// Package notify is the Tell me connector (notify@1): a message to the
// business's own people, on the WhatsApp or Telegram they linked to
// Taskiem, or by email (no-technical-steps N4). Like storefront@1 it acts
// for the run's tenant only (connector.CallerFrom) and needs no
// connection, and it only ever reaches the tenant's members.
package notify

import (
	"context"
	_ "embed"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

//go:embed manifest.yaml
var manifest []byte

// Message is one Tell me step's message.
type Message struct {
	Tenant      uuid.UUID
	Run         uuid.UUID
	Step        string
	Environment string
	// To is owner, role or members; Role and Members say which.
	To      string
	Role    string
	Members []string
	Title   string
	Text    string
	// Key makes it idempotent: a retry reaches nobody twice.
	Key string
}

// Result is who it went to.
type Result struct {
	People, Chat, Email int
}

// Text is one SMS to a customer.
type Text struct {
	Tenant uuid.UUID
	To     string
	Text   string
	Key    string
}

// Outside is one message to someone outside the business (message).
type Outside struct {
	Tenant      uuid.UUID
	Run         uuid.UUID
	Step        string
	Environment string
	Phone       string
	Email       string
	Title       string
	Text        string
	About       string
	// PayrollaEmployee: the Payrolla employee it is about; a staff member
	// linked to them hears it on their own chat instead.
	PayrollaEmployee string
	Key              string
}

// Delivery is how a message to someone outside goes.
type Delivery struct {
	// Via is whatsapp, telegram, sms, email or nobody.
	Via           string
	OverAllowance bool
}

// Teller delivers messages: Notifier in the platform.
type Teller interface {
	Tell(ctx context.Context, m Message) (Result, error)
	// Text queues an SMS to a customer; over is true when the month's
	// texts are used up (nothing is sent, the step still succeeds).
	Text(ctx context.Context, t Text) (over bool, err error)
	// Message queues a message to someone outside the business.
	Message(ctx context.Context, m Outside) (Delivery, error)
}

// CheckTell checks a tell step's template, if it names one: at publish
// (the input as written, so the name must be written out, not computed)
// and again when the step runs. The template must be one a Tell me
// message may carry (whatsapp.TellTemplate) and template_vars must hold
// exactly its variables; their values may be expressions.
func CheckTell(input map[string]any) []string {
	out := checkButtons(input)
	if t := checkTellTemplate(input); len(t) > 0 {
		out = append(out, t...)
	}
	return out
}

var (
	buttonTool = regexp.MustCompile(`^[a-z][a-z0-9-]{1,23}$`)
	// buttonPage is a page's short name (page/v1's name).
	buttonPage = regexp.MustCompile(`^[a-z][a-z0-9-]{1,23}$`)
	// filterID is a page's filter's id (page/v1).
	filterID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,23}$`)
)

// MaxPageFilters is how many filters a page has, so a page button sets.
const MaxPageFilters = 4

// checkButtons holds a Tell me message's buttons: three at most, each a
// label of twenty characters and either a tool's command written out (its
// form filled in) or a page's name written out with its filters set
// (pages §9: "Disk alerts" filtered to db-2).
func checkButtons(input map[string]any) []string {
	raw, has := input["buttons"]
	if !has {
		return nil
	}
	bs, ok := raw.([]any)
	if !ok {
		return []string{"buttons is a list of {label, tool, inputs, once} or {label, page, filters}"}
	}
	var out []string
	if len(bs) > 3 {
		out = append(out, "a message carries at most 3 buttons")
	}
	for i, x := range bs {
		b, _ := x.(map[string]any)
		label, _ := b["label"].(string)
		tool, isTool := b["tool"].(string)
		page, isPage := b["page"].(string)
		if n := len([]rune(label)); n == 0 || n > 20 {
			out = append(out, fmt.Sprintf("buttons[%d]: a label of 1 to 20 characters", i))
		}
		switch {
		case isTool && isPage:
			out = append(out, fmt.Sprintf("buttons[%d]: a button opens a tool or a page, not both", i))
		case isPage:
			if !buttonPage.MatchString(page) {
				out = append(out, fmt.Sprintf("buttons[%d]: page is a page's short name, written out (disk-alerts)", i))
			}
			if _, has := b["inputs"]; has {
				out = append(out, fmt.Sprintf("buttons[%d]: a page's button sets its filters, not inputs", i))
			}
			if _, has := b["once"]; has {
				out = append(out, fmt.Sprintf("buttons[%d]: a page's button opens the page each time: no once", i))
			}
			if f, has := b["filters"]; has {
				m, ok := f.(map[string]any)
				switch {
				case !ok:
					out = append(out, fmt.Sprintf("buttons[%d]: filters are the page's filters by id, each a value", i))
				case len(m) > MaxPageFilters:
					out = append(out, fmt.Sprintf("buttons[%d]: a page has at most %d filters", i, MaxPageFilters))
				default:
					for k := range m {
						if !filterID.MatchString(k) {
							out = append(out, fmt.Sprintf("buttons[%d]: filters.%s is not a filter's id", i, k))
						}
					}
				}
			}
		default:
			if !buttonTool.MatchString(tool) {
				out = append(out, fmt.Sprintf("buttons[%d]: tool is a tool's command, written out (clean-disk)", i))
			}
			if _, has := b["filters"]; has {
				out = append(out, fmt.Sprintf("buttons[%d]: a tool's button fills its inputs, not filters", i))
			}
		}
	}
	return out
}

func checkTellTemplate(input map[string]any) []string {
	name, has := input["template"]
	vars, hasVars := input["template_vars"]
	if !has && !hasVars {
		return nil
	}
	if !has {
		return []string{"template_vars needs template, the template they fill"}
	}
	s, _ := name.(string)
	if s == "" || strings.HasPrefix(s, "=") {
		return []string{"template is a template's name, written out (taskiem_daily_close): not an expression"}
	}
	t, ok := whatsapp.TellTemplate(s)
	if !ok {
		return []string{fmt.Sprintf("template %q is not one a Tell me message can carry: %s", s, strings.Join(tellTemplateNames(), ", "))}
	}
	want := whatsapp.TellVars(t)
	given, _ := vars.(map[string]any)
	if len(want) > 0 && given == nil {
		return []string{fmt.Sprintf("template %s needs template_vars with %s", s, strings.Join(want, ", "))}
	}
	var out []string
	for _, v := range want {
		if _, ok := given[v]; !ok {
			out = append(out, fmt.Sprintf("template %s needs template_vars.%s", s, v))
		}
	}
	for k := range given {
		if !slices.Contains(want, k) {
			out = append(out, fmt.Sprintf("template %s has no variable %q (its variables: %s)", s, k, strings.Join(want, ", ")))
		}
	}
	slices.Sort(out)
	return out
}

// tellTemplateNames are the templates a Tell me message may carry.
func tellTemplateNames() []string {
	var out []string
	for _, t := range whatsapp.AllTemplates {
		if _, ok := whatsapp.TellTemplate(t.Name); ok {
			out = append(out, t.Name)
		}
	}
	return out
}

// checkInput is the connector's publish-time check (connector.CheckInput).
func checkInput(action string, input map[string]any) []string {
	var out []string
	if action == "tell" {
		out = CheckTell(input)
	}
	if action == "tell" || action == "message" {
		out = append(out, checkFile(input)...)
	}
	return out
}

// checkFile holds a message's file to a saved file, written as an
// expression (=saved.price_list): a link written out is not one of the
// business's saved files.
func checkFile(input map[string]any) []string {
	raw, has := input["file"]
	if !has {
		return nil
	}
	if s, ok := raw.(string); ok && strings.HasPrefix(s, "=") {
		return nil
	}
	if m, ok := raw.(map[string]any); ok {
		if s, ok := m["file"].(string); ok && strings.HasPrefix(s, "=") {
			return nil
		}
	}
	return []string{"file is one of the business's saved files, written =saved.price_list"}
}

// FileLine is the line a message's file adds at its end: the file's name
// and its link, from the saved file the step was given (its file and
// file_name, read when the run started); "" when it has none. A link that
// is not a web address is no file.
func FileLine(input map[string]any) (string, error) {
	raw, has := input["file"]
	if !has || raw == nil {
		return "", nil
	}
	// The saved file (=saved.price_list), or its link alone
	// (=saved.price_list.file).
	m, _ := raw.(map[string]any)
	link, _ := m["file"].(string)
	if s, ok := raw.(string); ok {
		link = s
	}
	link = strings.TrimSpace(link)
	if link == "" {
		return "", fmt.Errorf("the saved file could not be read for this run (is the business's storage set up?): %w", effects.ErrFatal)
	}
	if !strings.HasPrefix(link, "https://") && !strings.HasPrefix(link, "http://") {
		return "", fmt.Errorf("file is one of the business's saved files, written =saved.price_list: %w", effects.ErrFatal)
	}
	name, _ := m["file_name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		name = "The file"
	}
	return name + ": " + link, nil
}

// WithFile is a message with its file's line at its end, if it has one.
func WithFile(message string, input map[string]any) string {
	line, err := FileLine(input)
	if err != nil || line == "" {
		return message
	}
	return strings.TrimRight(message, "\n") + "\n\n" + line
}

// New returns the connector; t nil makes it fail plainly.
func New(t Teller) *connector.Connector {
	m := connector.MustParse(manifest)
	return &connector.Connector{Manifest: m, CheckInput: checkInput, Actions: map[string]connector.Action{
		"tell": connector.ActionFunc(func(ctx context.Context, req connector.Request) (connector.Response, error) {
			c, ok := connector.CallerFrom(ctx)
			if !ok {
				return connector.Response{}, fmt.Errorf("Tell me runs only in a workflow run: %w", effects.ErrFatal)
			}
			if t == nil {
				return connector.Response{}, fmt.Errorf("Tell me is not available on this deployment: %w", effects.ErrFatal)
			}
			msg := Message{Tenant: c.Tenant, Run: c.Run, Step: c.Step, Environment: c.Environment, Key: req.IdempotencyKey}
			msg.To, _ = req.Input["to"].(string)
			msg.Role, _ = req.Input["role"].(string)
			msg.Title, _ = req.Input["title"].(string)
			msg.Text, _ = req.Input["message"].(string)
			if list, ok := req.Input["members"].([]any); ok {
				for _, v := range list {
					if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
						msg.Members = append(msg.Members, strings.TrimSpace(s))
					}
				}
			}
			if msg.To == "" {
				msg.To = "owner"
			}
			switch {
			case strings.TrimSpace(msg.Text) == "":
				return connector.Response{}, fmt.Errorf("a message is required: %w", effects.ErrFatal)
			case msg.To == "role" && msg.Role == "":
				return connector.Response{}, fmt.Errorf("to: role needs the role: %w", effects.ErrFatal)
			case msg.To == "members" && len(msg.Members) == 0:
				return connector.Response{}, fmt.Errorf("to: members needs their emails: %w", effects.ErrFatal)
			}
			// The template, if any, was checked at publish; the step's
			// values are checked now (the outbox renders from this input).
			if msgs := CheckTell(req.Input); len(msgs) > 0 {
				return connector.Response{}, fmt.Errorf("%s: %w", strings.Join(msgs, "; "), effects.ErrFatal)
			}
			// A saved file: its name and link at the end (by email here;
			// the chat outbox renders it from this input too).
			if _, err := FileLine(req.Input); err != nil {
				return connector.Response{}, err
			}
			msg.Text = WithFile(msg.Text, req.Input)
			res, err := t.Tell(ctx, msg)
			if err != nil {
				return connector.Response{}, err
			}
			return connector.Response{Output: map[string]any{"people": res.People, "chat": res.Chat, "email": res.Email, "nobody": res.People == 0}}, nil
		}),
		"message": connector.ActionFunc(func(ctx context.Context, req connector.Request) (connector.Response, error) {
			c, ok := connector.CallerFrom(ctx)
			if !ok {
				return connector.Response{}, fmt.Errorf("messaging a person runs only in a workflow run: %w", effects.ErrFatal)
			}
			if t == nil {
				return connector.Response{}, fmt.Errorf("messaging people is not available on this deployment: %w", effects.ErrFatal)
			}
			m := Outside{Tenant: c.Tenant, Run: c.Run, Step: c.Step, Environment: c.Environment, Key: req.IdempotencyKey}
			m.Phone, _ = req.Input["phone"].(string)
			m.Email, _ = req.Input["email"].(string)
			m.Title, _ = req.Input["title"].(string)
			m.Text, _ = req.Input["message"].(string)
			m.About, _ = req.Input["about"].(string)
			m.PayrollaEmployee, _ = req.Input["payrolla_employee"].(string)
			m.Phone, m.Email, m.PayrollaEmployee = strings.TrimSpace(m.Phone), strings.TrimSpace(m.Email), strings.TrimSpace(m.PayrollaEmployee)
			switch {
			case strings.TrimSpace(m.Text) == "":
				return connector.Response{}, fmt.Errorf("a message is required: %w", effects.ErrFatal)
			case m.Phone == "" && m.Email == "" && m.PayrollaEmployee == "":
				return connector.Response{}, fmt.Errorf("their phone number or email is required: %w", effects.ErrFatal)
			}
			// A saved file: its name and link at the end (by SMS or email
			// here; the chat outbox renders it from this input too).
			if _, err := FileLine(req.Input); err != nil {
				return connector.Response{}, err
			}
			m.Text = WithFile(m.Text, req.Input)
			d, err := t.Message(ctx, m)
			if err != nil {
				return connector.Response{}, err
			}
			return connector.Response{Output: map[string]any{"via": d.Via, "over_allowance": d.OverAllowance}}, nil
		}),
		"text": connector.ActionFunc(func(ctx context.Context, req connector.Request) (connector.Response, error) {
			c, ok := connector.CallerFrom(ctx)
			if !ok {
				return connector.Response{}, fmt.Errorf("texting a customer runs only in a workflow run: %w", effects.ErrFatal)
			}
			if t == nil {
				return connector.Response{}, fmt.Errorf("texting is not available on this deployment: %w", effects.ErrFatal)
			}
			to, _ := req.Input["to"].(string)
			msg, _ := req.Input["message"].(string)
			if strings.TrimSpace(to) == "" || strings.TrimSpace(msg) == "" {
				return connector.Response{}, fmt.Errorf("a phone number and a message are required: %w", effects.ErrFatal)
			}
			over, err := t.Text(ctx, Text{Tenant: c.Tenant, To: to, Text: msg, Key: req.IdempotencyKey})
			if err != nil {
				return connector.Response{}, err
			}
			return connector.Response{Output: map[string]any{"queued": !over, "over_allowance": over}}, nil
		}),
	}}
}
