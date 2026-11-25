package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/areas"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/templates"
)

// Sell something with this form (docs/spec/forms-that-sell.md §5.1): a
// workflow's hosted form becomes an order form of the storefront, selling
// one product in packages or a basket of a few. The order form is an
// action of the home page (or a front the owner picks) with the hosted
// form's own questions; the workflow becomes the action's workflow, its
// storefront trigger naming the action by id, reading what the buyer is
// and typed where the storefront gives them (trigger.body.customer,
// trigger.answers). The form keeps its address: /f/<id> sends buyers to
// /a/<action> on the storefront. It is refused while the Storefront (or
// Orders) area is off, with the area named so the owner is offered to
// turn it on.

// sellForm is the request.
type sellForm struct {
	Front  *uuid.UUID          `json:"front_id"`
	Label  string              `json:"label"`
	Sells  storefront.SellSpec `json:"sells"`
	Notice string              `json:"notice"`
}

// sellFormOut is the answer: the order form made, where it is, and what
// its packages say against the list's prices.
type sellFormOut struct {
	Action     storefront.Action `json:"action"`
	Front      sellFront         `json:"front"`
	WorkflowID uuid.UUID         `json:"workflow_id"`
	Version    int               `json:"version"`
	URL        string            `json:"url"`
	FormURL    string            `json:"form_url"`
	Warnings   []string          `json:"warnings"`
}

type sellFront struct {
	ID   uuid.UUID `json:"id"`
	Slug string    `json:"slug"`
	Name string    `json:"name"`
}

// bodyRef is a reference to the submission's answers in a definition.
var bodyRef = regexp.MustCompile(`\btrigger\.body\b(\.[A-Za-z_][A-Za-z0-9_]*|\[['"][A-Za-z_][A-Za-z0-9_]*['"]\])?`)

// customerOf is where a hosted form's field is on an order: the buyer's
// name, phone, email or address (the order form asks them itself), or
// "" for one of the form's own questions.
func customerOf(name string, p map[string]any) string {
	pii, _ := p["x-pii"].(string)
	format, _ := p["format"].(string)
	switch {
	case pii == "name" || slices.Contains([]string{"name", "full_name", "your_name", "customer_name"}, name):
		return "name"
	case pii == "phone" || format == "phone-ng" || slices.Contains([]string{"phone", "phone_number", "mobile"}, name):
		return "phone"
	case pii == "email" || format == "email" || name == "email":
		return "email"
	case pii == "address" || slices.Contains([]string{"address", "delivery_address"}, name):
		return "address"
	}
	return ""
}

// sellingDefinition is a hosted form's workflow as an order form's: its
// trigger the storefront's naming the action, its inputs the order's, its
// reads of the form's answers moved to the buyer and the answers, and the
// form's own questions (extra) for the action.
func sellingDefinition(def []byte, action uuid.UUID) ([]byte, map[string]json.RawMessage, []string, error) {
	d, err := wd.Load(def)
	if err != nil || d.Trigger.Type != "form" {
		return nil, nil, nil, fmt.Errorf("%w: only a form that starts its workflow can sell", errConflict)
	}
	raw, _ := d.InputsSchema()
	schema, _ := raw.(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	required := map[string]bool{}
	if req, ok := schema["required"].([]any); ok {
		for _, x := range req {
			if s, ok := x.(string); ok {
				required[s] = true
			}
		}
	}
	extra := map[string]json.RawMessage{}
	moved := map[string]string{}
	var reqd []string
	for name, v := range props {
		p, _ := v.(map[string]any)
		if c := customerOf(name, p); c != "" {
			moved[name] = "trigger.body.customer." + c
			continue
		}
		if f, _ := p["format"].(string); f == "file" || wd.DeviceKind(p) != "" {
			return nil, nil, nil, fmt.Errorf("%w: this form asks for a file (%s), which an order form cannot: remove that question first", errConflict, name)
		}
		if slices.Contains(storefront.ReservedAnswers, name) {
			return nil, nil, nil, fmt.Errorf("%w: this form has a question called %q, which an order form gives itself: rename it first", errConflict, name)
		}
		b, _ := json.Marshal(p)
		extra[name] = b
		moved[name] = "trigger.answers." + name
		if required[name] {
			reqd = append(reqd, name)
		}
	}
	if strings.Contains(string(def), "trigger.form") {
		return nil, nil, nil, fmt.Errorf("%w: this workflow reads who sent the form (trigger.form), which an order does not have: change it first", errConflict)
	}
	var doc map[string]any
	if err := json.Unmarshal(def, &doc); err != nil {
		return nil, nil, nil, err
	}
	steps := rewriteRefs(doc["steps"], moved)
	doc["steps"] = steps
	if out, ok := doc["output"]; ok {
		doc["output"] = rewriteRefs(out, moved)
	}
	doc["trigger"] = map[string]any{"type": "storefront", "config": map[string]any{"action": "order", "action_id": action.String()}}
	// The order's own inputs, as the order template has them.
	if tpl, ok := templates.Default().Get("storefront-order"); ok {
		var t map[string]any
		if json.Unmarshal(tpl.Definition, &t) == nil {
			doc["inputs"] = t["inputs"]
		}
	}
	delete(doc, "types")
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, nil, nil, err
	}
	// The form's questions keep which are required.
	slices.Sort(reqd)
	return out, extra, reqd, nil
}

// rewriteRefs moves a definition's reads of trigger.body.<field> to where
// an order has them; trigger.body alone becomes the answers.
func rewriteRefs(v any, moved map[string]string) any {
	switch x := v.(type) {
	case string:
		return bodyRef.ReplaceAllStringFunc(x, func(m string) string {
			field := strings.Trim(strings.TrimPrefix(m, "trigger.body"), ".[]'\"")
			if field == "" {
				return "trigger.answers"
			}
			if to, ok := moved[field]; ok {
				return to
			}
			return "trigger.answers." + field
		})
	case map[string]any:
		for k, e := range x {
			x[k] = rewriteRefs(e, moved)
		}
		return x
	case []any:
		for i, e := range x {
			x[i] = rewriteRefs(e, moved)
		}
		return x
	}
	return v
}

// areaOff refuses with the area that is off, so the web and the app offer
// to turn it on.
type areaOff struct{ area areas.Area }

func (e areaOff) Error() string { return errConflict.Error() + ": " + areaOffSentence(e.area) }
func (e areaOff) Unwrap() error { return errConflict }

// sellFormHandler answers POST /v1/forms/{id}/sell.
func (s *Server) sellFormHandler(w http.ResponseWriter, r *http.Request) {
	var req sellForm
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	endpoint, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	p := principalFrom(r.Context())
	var out sellFormOut
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = s.sellFormTx(r, tx, p, endpoint, req)
		return err
	})
	var off areaOff
	if errors.As(err, &off) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": areaOffSentence(off.area), "code": "area_off", "area_off": string(off.area)})
		return
	}
	var notReady *errNotReady
	if errors.As(err, &notReady) {
		writeNotReady(w, notReady.needs)
		return
	}
	if err != nil {
		s.fail(w, r, storefrontErr(err))
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) sellFormTx(r *http.Request, tx pgx.Tx, p *Principal, endpoint uuid.UUID, req sellForm) (sellFormOut, error) {
	ctx := r.Context()
	var out sellFormOut
	for _, a := range []areas.Area{areas.Storefront, areas.Orders} {
		on, err := areas.Enabled(ctx, tx, p.TenantID, a)
		if err != nil {
			return out, err
		}
		if !on {
			return out, areaOff{a}
		}
	}
	sf, err := storefront.Get(ctx, tx, p.TenantID)
	if errors.Is(err, storefront.ErrNotFound) {
		return out, fmt.Errorf("%w: set up your storefront page first: the order form is on it", errConflict)
	}
	if err != nil {
		return out, err
	}
	if sf.PublishedVersion == 0 || sf.Status != "published" {
		return out, fmt.Errorf("%w: publish your storefront page first: the order form is on it", errConflict)
	}
	var wf uuid.UUID
	var env, publicID string
	var version int
	var sold *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT workflow_id, environment, version, public_id, action_id FROM form_endpoints WHERE id = $1`, endpoint).
		Scan(&wf, &env, &version, &publicID, &sold); err != nil {
		return out, err
	}
	if sold != nil {
		return out, fmt.Errorf("%w: this form sells already", errConflict)
	}
	if env != "prod" {
		return out, fmt.Errorf("%w: only a form that is live can sell", errConflict)
	}
	if err := s.refuseGitManaged(ctx, tx, wf); err != nil {
		return out, err
	}
	var def, layout []byte
	var latest int
	if err := tx.QueryRow(ctx, `SELECT definition, layout, (SELECT max(version) FROM workflow_versions WHERE workflow_id = $1) FROM workflow_versions
		WHERE workflow_id = $1 AND version = $2`, wf, version).Scan(&def, &layout, &latest); err != nil {
		return out, err
	}
	front := uuid.Nil
	if req.Front != nil {
		front = *req.Front
	} else if err := tx.QueryRow(ctx, `SELECT id FROM storefront_fronts WHERE storefront_id = $1 AND slug = ''`, sf.ID).Scan(&front); err != nil {
		return out, err
	}
	label := req.Label
	if label == "" {
		f, _ := wd.Load(def)
		if f != nil {
			if c, err := f.FormTriggerConfig(); err == nil {
				label = c.Title
			}
		}
	}
	if len([]rune(label)) > 40 {
		label = string([]rune(label)[:40])
	}
	// The action, off, with a placeholder id for the trigger; the real
	// one names it below.
	id := uuid.Must(uuid.NewV7())
	next, extra, required, err := sellingDefinition(def, id)
	if err != nil {
		return out, err
	}
	a, warnings, err := storefront.AddOrderAction(ctx, tx, p.TenantID, front, storefront.NewOrderForm{Label: label, Sells: req.Sells, Notice: req.Notice, Extra: extra, Required: required})
	if err != nil {
		return out, err
	}
	next = []byte(strings.ReplaceAll(string(next), id.String(), a.ID.String()))
	v := latest + 1
	if err := insertVersion(r, tx, wf, v, next, layout); err != nil {
		return out, err
	}
	if err := s.checkReady(ctx, tx, p.TenantID, wf, v); err != nil {
		return out, err
	}
	if err := publishMayRead(ctx, tx, p, wf, v); err != nil {
		return out, err
	}
	g, err := governanceTx(ctx, tx)
	if err != nil {
		return out, err
	}
	if wait, err := publishNeedsApproval(ctx, tx, g, nil); err != nil {
		return out, err
	} else if wait {
		return out, fmt.Errorf("%w: with four-eyes publishing, a second person approves each publish: ask for the order form's workflow to be published instead", errConflict)
	}
	// The form's address now leads to the order form.
	if _, err := tx.Exec(ctx, `UPDATE form_endpoints SET action_id = $2, updated_at = now() WHERE id = $1`, endpoint, *a.ID); err != nil {
		return out, err
	}
	by := p.id()
	probs, _, _, err := s.publishTx(ctx, tx, p.TenantID, wf, v, &by, "")
	if errors.Is(err, errInvalid) {
		msgs := make([]string, 0, len(probs))
		for _, pr := range probs {
			msgs = append(msgs, pr.Message)
		}
		return out, fmt.Errorf("%w: the order form's workflow does not check: %s", errConflict, strings.Join(msgs, "; "))
	}
	if err != nil {
		return out, err
	}
	if _, err := tx.Exec(ctx, `UPDATE storefront_actions SET enabled = true, updated_at = now() WHERE id = $1`, *a.ID); err != nil {
		return out, err
	}
	if _, err := s.publishGated(ctx, tx, p.TenantID, p.Actor()); err != nil {
		return out, err
	}
	if err := auditTx(r, tx, "form.sell", endpoint.String(), map[string]any{"action": a.ID.String(), "workflow": wf.String(), "version": v,
		"sells": req.Sells}); err != nil {
		return out, err
	}
	var fr sellFront
	if err := tx.QueryRow(ctx, `SELECT id, slug, name FROM storefront_fronts WHERE id = $1`, front).Scan(&fr.ID, &fr.Slug, &fr.Name); err != nil {
		return out, err
	}
	sf, err = storefront.Get(ctx, tx, p.TenantID)
	if err != nil {
		return out, err
	}
	a.Enabled = true
	out = sellFormOut{Action: a, Front: fr, WorkflowID: wf, Version: v, URL: actionURL(sf.URL(s.previewDomain()), fr.Slug, a.Slug, false), Warnings: nonNilStrings(warnings)}
	if s.Forms != nil && s.Forms.FormHost != "" {
		out.FormURL = "https://" + s.Forms.FormHost + "/f/" + publicID
	}
	return out, nil
}

// actionURL is an action's address on the storefront at base, framed or
// not.
func actionURL(base, front, slug string, embed bool) string {
	if base == "" {
		return ""
	}
	base = strings.TrimRight(base, "/")
	if embed {
		base += "/embed"
	}
	if front != "" {
		base += "/" + front
	}
	return base + "/a/" + slug
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
