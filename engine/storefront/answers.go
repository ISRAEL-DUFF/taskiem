package storefront

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/wd"
)

// An action's own questions (docs/spec/fronts.md §5.3, §5.5): its form
// (storefront_actions.form) is a JSON Schema object whose properties are
// the questions, with x-form hints. A submission's answers to them reach
// the run as trigger.answers, apart from Taskiem's own record of the
// submission (trigger.submission), so no answer can pass for a total or a
// reference.

// ReservedAnswers are the names a question may not take: Taskiem's own
// fields of a submission, in trigger.submission and trigger.body.
var ReservedAnswers = []string{"action", "basket", "booking", "customer", "delivery_fee", "delivery_kobo", "event", "front", "fulfilment",
	"hire", "kind", "lines", "payment", "reference", "space", "status_link", "summary", "total", "total_kobo", "zone"}

var answerName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// Widgets are the fields that draw on the front's lists (§5.4): their
// values are Taskiem's (the basket's lines, the booking's time), part of
// trigger.submission and trigger.body, never answers. F1 has the basket
// (today's order form) and time (today's booking form).
var Widgets = []string{"basket", "time"}

// BuiltinForm is a built-in action's form when it has none of its own:
// the order form's basket from the catalogue, the booking form's time from
// the services, and no questions for a question or a call-back, whose
// message and best time are Taskiem's details.
func BuiltinForm(kind string) []byte {
	switch kind {
	case "order":
		return []byte(`{"type":"object","properties":{"basket":{"type":"array","title":"Your order","x-form":{"widget":"basket","list":"catalogue","min":1}}}}`)
	case "booking":
		return []byte(`{"type":"object","properties":{"time":{"type":"object","title":"Your time","x-form":{"widget":"time","list":"services"}}}}`)
	}
	return []byte(`{"type":"object","properties":{}}`)
}

// EffectiveForm is an action's form: its own, or its kind's.
func EffectiveForm(form []byte, kind string) []byte {
	if len(form) == 0 || string(form) == "null" {
		return BuiltinForm(kind)
	}
	return form
}

// FormWidget is the widget a form's field is drawn with ("" for a
// question).
func FormWidget(form []byte, name string) string {
	var f struct {
		Properties map[string]struct {
			XForm struct {
				Widget string `json:"widget"`
			} `json:"x-form"`
		} `json:"properties"`
	}
	if json.Unmarshal(form, &f) != nil {
		return ""
	}
	if w := f.Properties[name].XForm.Widget; slices.Contains(Widgets, w) {
		return w
	}
	return ""
}

// HasWidget reports whether a form draws a widget.
func HasWidget(form []byte, widget string) bool {
	for _, k := range allFields(form) {
		if FormWidget(form, k) == widget {
			return true
		}
	}
	return false
}

// FormFields are a form's questions by name, in order: its fields but the
// widgets.
func FormFields(form []byte) []string {
	var out []string
	for _, k := range allFields(form) {
		if FormWidget(form, k) == "" {
			out = append(out, k)
		}
	}
	return out
}

func allFields(form []byte) []string {
	if len(form) == 0 {
		return nil
	}
	var f struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(form, &f) != nil {
		return nil
	}
	out := make([]string, 0, len(f.Properties))
	for k := range f.Properties {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// CheckForm refuses a form that is not an object of questions, and a
// question named like one of Taskiem's fields of a submission.
func CheckForm(form []byte) error {
	if len(form) == 0 {
		return nil
	}
	var f struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(form, &f); err != nil || f.Type != "object" {
		return fmt.Errorf("%w: an action's form is an object of questions", ErrInvalid)
	}
	var props map[string]struct {
		Format string `json:"format"`
	}
	_ = json.Unmarshal(mustProps(form), &props)
	for _, w := range Widgets {
		n := 0
		for _, k := range allFields(form) {
			if FormWidget(form, k) == w {
				n++
			}
		}
		if n > 1 {
			return fmt.Errorf("%w: a form has one %s field at most", ErrInvalid, w)
		}
	}
	for _, k := range FormFields(form) {
		if props[k].Format == "file" {
			return fmt.Errorf("%w: a storefront form cannot ask for a file (%q)", ErrInvalid, k)
		}
		if slices.Contains(ReservedAnswers, k) {
			return fmt.Errorf("%w: a question cannot be called %q: Taskiem gives every workflow the submission's %s itself; name the question something else", ErrInvalid, k, k)
		}
		if !answerName.MatchString(k) {
			return fmt.Errorf("%w: a question's name is lower-case letters, digits and _, up to 40, starting with a letter: %q", ErrInvalid, k)
		}
	}
	return nil
}

// QuestionInUse refuses a storefront publish whose form no longer asks a
// question a live workflow reads (§5.3).
type QuestionInUse struct {
	Action   string // the action's slug
	Question string
	Workflow uuid.UUID
	Name     string
}

func (e *QuestionInUse) Error() string {
	return fmt.Sprintf("%s: your %s form no longer asks %q, which workflow %q reads: keep the question, or change the workflow first", ErrInvalid, e.Action, e.Question, e.Name)
}

func (e *QuestionInUse) Unwrap() error { return ErrInvalid }

// checkAnswersRead refuses a publish when an action's form refuses (a
// reserved or bad name) or no longer asks a question a workflow deployed
// for it, in any environment, reads.
func checkAnswersRead(ctx context.Context, tx pgx.Tx, sf Storefront) error {
	rows, err := tx.Query(ctx, `SELECT a.slug, a.kind, a.form, w.id, w.name, v.definition FROM storefront_actions a
		JOIN triggers t ON t.type = 'storefront' AND (t.storefront_action_id = a.id OR (t.storefront_action_id IS NULL AND a.builtin AND t.storefront_action = a.kind))
		JOIN workflow_versions v ON v.workflow_id = t.workflow_id AND v.version = t.version
		JOIN workflows w ON w.id = t.workflow_id
		WHERE a.storefront_id = $1 ORDER BY a.position, a.slug, w.name, t.environment`, sf.ID)
	if err != nil {
		return err
	}
	type held struct {
		action string
		kind   string
		form   []byte
		wf     uuid.UUID
		name   string
		def    []byte
	}
	var list []held
	for rows.Next() {
		var h held
		if err := rows.Scan(&h.action, &h.kind, &h.form, &h.wf, &h.name, &h.def); err != nil {
			rows.Close()
			return err
		}
		list = append(list, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, h := range list {
		d, err := wd.Load(h.def)
		if err != nil {
			continue // a live definition is valid; nothing to read from one that is not
		}
		asked := FormFields(EffectiveForm(h.form, h.kind))
		for _, q := range wd.AnswerFields(d) {
			if !slices.Contains(asked, q) {
				return &QuestionInUse{Action: h.action, Question: q, Workflow: h.wf, Name: h.name}
			}
		}
	}
	forms, err := tx.Query(ctx, `SELECT form FROM storefront_actions WHERE storefront_id = $1 AND form IS NOT NULL`, sf.ID)
	if err != nil {
		return err
	}
	var all [][]byte
	for forms.Next() {
		var f []byte
		if err := forms.Scan(&f); err != nil {
			forms.Close()
			return err
		}
		all = append(all, f)
	}
	forms.Close()
	if err := forms.Err(); err != nil {
		return err
	}
	for _, f := range all {
		if err := CheckForm(f); err != nil {
			return err
		}
	}
	return nil
}

// AnswersAsked are the questions of the action a storefront trigger names
// (nil when it names none the storefront has yet).
func AnswersAsked(ctx context.Context, tx pgx.Tx, action *uuid.UUID) ([]string, error) {
	if action == nil {
		return nil, nil
	}
	var form []byte
	var kind string
	err := tx.QueryRow(ctx, `SELECT form, kind FROM storefront_actions WHERE id = $1`, *action).Scan(&form, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return FormFields(EffectiveForm(form, kind)), err
}

// keepAsked keeps the answers to the action's questions, and only those.
func keepAsked(answers map[string]any, form []byte) map[string]any {
	if len(answers) == 0 {
		return nil
	}
	asked := FormFields(form)
	out := map[string]any{}
	for k, v := range answers {
		if slices.Contains(asked, k) {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// maskName leaves a name's first word.
func maskName(n string) string {
	if f := strings.Fields(n); len(f) > 0 {
		return f[0]
	}
	return ""
}

// submissionRecord is what a run is given besides trigger.body: Taskiem's
// record of the submission and the customer's answers, kept apart (§5.5).
type submissionRecord struct {
	front, action map[string]any
	statusLink    string
	answers       map[string]any
}

// recordOf reads an order's front, action, status link and answers.
func (o *Orders) recordOf(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ord Order) (submissionRecord, error) {
	r := submissionRecord{answers: map[string]any{}}
	var fid, aid uuid.UUID
	var fslug, fname, aslug, akind string
	var env map[string]any
	if err := tx.QueryRow(ctx, `SELECT f.id, f.slug, f.name, a.id, a.slug, a.kind, o.answers FROM storefront_orders o
		JOIN storefront_actions a ON a.id = o.action_id JOIN storefront_fronts f ON f.id = o.front_id
		WHERE o.tenant_id = $1 AND o.id = $2`, tenant, ord.ID).Scan(&fid, &fslug, &fname, &aid, &aslug, &akind, &env); err != nil {
		return r, err
	}
	r.front = map[string]any{"id": fid.String(), "slug": fslug, "name": fname}
	r.action = map[string]any{"id": aid.String(), "slug": aslug, "kind": akind}
	if env != nil {
		v, err := o.Vault.OpenTx(ctx, tx, tenant, env)
		switch {
		case errors.Is(err, pii.ErrErased):
		case err != nil:
			return r, err
		default:
			if m, ok := v.(map[string]any); ok {
				r.answers = m
			}
		}
	}
	link, err := o.linkOf(ctx, tx, tenant, ord)
	if err != nil {
		return r, err
	}
	r.statusLink = link
	return r, nil
}

// submission is trigger.submission: reference, front, action, total,
// payment, the customer masked, the status link and the lines.
func (r submissionRecord) submission(ord Order, paymentStatus string) map[string]any {
	lines := make([]any, 0, len(ord.Basket))
	for _, l := range ord.Basket {
		b, _ := json.Marshal(l)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		lines = append(lines, m)
	}
	phone := ""
	if p := ord.Customer.Phone; len(p) > 4 {
		phone = "•••" + p[len(p)-4:]
	}
	out := map[string]any{"reference": ord.Reference, "front": r.front, "action": r.action, "total_kobo": ord.TotalKobo,
		"payment": map[string]any{"mode": ord.Payment, "status": paymentStatus}, "due_kobo": ord.DueKobo(), "paid_kobo": ord.PaidKobo, "customer": map[string]any{"name": maskName(ord.Customer.Name), "phone": phone},
		"status_link": r.statusLink, "lines": lines}
	if k, v := reservedTime(ord); k != "" {
		out[k] = v
	}
	return out
}

func mustProps(form []byte) []byte {
	var f struct {
		Properties json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(form, &f) != nil || f.Properties == nil {
		return []byte("{}")
	}
	return f.Properties
}

// reservedTime is what a hire or a space booking holds, for trigger.body
// and trigger.submission (fronts F5): "hire" with its item, quantity and
// days, due back at the end of its last day; or "space" with the space
// and its times, by the day or the hour, with a reminder a day before.
func reservedTime(ord Order) (string, map[string]any) {
	// A hire's item, quantity and days (fronts F5): due back at the end of
	// its last day.
	if ord.Kind == "hire" && ord.StartsAt != nil && ord.EndsAt != nil {
		qty := 0
		for _, l := range ord.Basket {
			qty += l.Quantity
		}
		days := int(ord.EndsAt.Sub(*ord.StartsAt).Hours()/24 + 0.5)
		if days > 0 {
			qty /= days
		}
		return "hire", map[string]any{"item": ord.ServiceName, "qty": qty, "days": days, "from": Day(*ord.StartsAt),
			"when":     Day(*ord.StartsAt) + " to " + Day(ord.EndsAt.Add(-time.Minute)),
			"due_back": Day(ord.EndsAt.Add(-time.Minute)), "due_back_at": ord.EndsAt.UTC().Format(time.RFC3339),
			// The morning of the last day, 08:00 in Lagos: the reminder.
			"remind_at": ord.EndsAt.Add(-16 * time.Hour).UTC().Format(time.RFC3339)}
	}
	// A space booking's space and times (fronts F5): from its start to its
	// end, by the day or the hour, with a reminder a day before.
	if ord.Kind == "space" && ord.StartsAt != nil && ord.EndsAt != nil {
		sp := map[string]any{"space": ord.ServiceName, "from": When(*ord.StartsAt), "until": When(*ord.EndsAt),
			"starts_at": ord.StartsAt.UTC().Format(time.RFC3339), "ends_at": ord.EndsAt.UTC().Format(time.RFC3339), "remind_at": ord.StartsAt.Add(-24 * time.Hour).UTC().Format(time.RFC3339)}
		// By the day, from the first day's opening to the last's closing;
		// by the hour, on one day.
		if len(ord.Basket) > 0 && ord.Basket[0].Unit == "day" {
			sp["days"] = ord.Basket[0].Quantity
			sp["when"] = Day(*ord.StartsAt)
			if Day(*ord.StartsAt) != Day(*ord.EndsAt) {
				sp["when"] = Day(*ord.StartsAt) + " to " + Day(*ord.EndsAt)
			}
		} else {
			sp["hours"] = int(ord.EndsAt.Sub(*ord.StartsAt) / time.Hour)
			sp["when"] = When(*ord.StartsAt) + " to " + ord.EndsAt.In(Lagos).Format("15:04")
		}
		return "space", sp
	}
	return "", nil
}
