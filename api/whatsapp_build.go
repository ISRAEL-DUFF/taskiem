package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdtext"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/templates"
)

// Building over WhatsApp (spec 11.1 Build, SME tier; docs/whatsapp.md#build).
// "build every Friday text my customers who owe me" (or the same words
// without "build") runs the AI builder, templates first, in the
// background, under the person's own workflow.edit, the tenant's AI
// budget and the same rate limits as the web app. The draft comes back as
// plain numbered steps, never JSON; the template parameters the goal did
// not state are asked one by one; and it ends in an explicit "Save as
// draft? yes/no" (spec 11.5). Saving creates a draft version only:
// publishing still goes through the web app and its policies.

// waBuild is a build in progress in a chat session.
type waBuild struct {
	ID uuid.UUID `json:"id"`
	// Phase: building (the model is drafting), params (asking the
	// template's missing parameters), confirm (waiting for yes or no).
	Phase    string         `json:"phase"`
	Template string         `json:"template,omitempty"`
	Params   map[string]any `json:"params,omitempty"`
	Missing  []string       `json:"missing,omitempty"`
	// Asking is how many parameters were missing when the draft came.
	Asking int `json:"asking,omitempty"`
}

const (
	buildBuilding = "building"
	buildParams   = "params"
	buildConfirm  = "confirm"
)

// waBuildPhrase recognises a goal written without "build": a schedule or
// an event followed by something to do ("every Friday text my customers
// who owe me", "when a customer pays send them a thank you").
var waBuildPhrase = regexp.MustCompile(`^(every|each|whenever|when|daily|weekly|monthly|once a|on the|at the end of|remind (me|my)|automate|automatically|text my|sms my|whatsapp my|message my|email my|send my|notify me|alert me|let me know|tell me when)\b`)

// waBuildGoal reports whether a message asks to build, and the goal.
func waBuildGoal(text, cmd string) (string, bool) {
	verb, _, _ := strings.Cut(cmd, " ")
	if verb == "build" || verb == "create" || verb == "automate" && cmd != "automate" {
		_, goal, _ := strings.Cut(strings.TrimSpace(text), " ")
		if verb == "automate" {
			goal = strings.TrimSpace(text)
		}
		return strings.TrimSpace(goal), true
	}
	if waBuildPhrase.MatchString(cmd) && len(strings.Fields(cmd)) >= 4 {
		return strings.TrimSpace(text), true
	}
	return "", false
}

func confirmSaveButtons(c *chat) []whatsapp.Button {
	return []whatsapp.Button{{ID: "yes", Title: c.t("button.yes_save")}, {ID: "no", Title: c.t("button.no")}}
}

// waStartBuild answers a build request.
func (s *Server) waStartBuild(ctx context.Context, c *chat, goal string) error {
	if !c.p.Can(PermWorkflowEdit) {
		return c.say(ctx, s, c.t("wa.build.denied", "org", c.tenant.Name))
	}
	if s.AI == nil || s.AI.Provider == nil {
		return c.say(ctx, s, c.t("wa.build.off"))
	}
	if goal == "" {
		return c.say(ctx, s, c.t("wa.build.what"))
	}
	if len(goal) > aiGoalMax {
		return c.say(ctx, s, c.t("wa.build.too_long", "max", strconv.Itoa(aiGoalMax)))
	}
	limit, used, err := s.aiBudget(ctx, c.tenant.ID)
	if err != nil {
		return err
	}
	if limit > 0 && used >= limit {
		s.Store.LimitHit(ctx, c.tenant.ID, "ai_monthly_tokens")
		return c.say(ctx, s, c.t("wa.build.budget", "org", c.tenant.Name))
	}
	// Per number and per tenant (shared with the web app): a phone cannot
	// spend the tenant's budget in a burst.
	if !s.limiter("wa-build:"+c.number, 20*time.Second, 3).Allow() || !s.limiter("ai-build:"+c.tenant.ID.String(), 20*time.Second, 5).Allow() {
		return c.say(ctx, s, c.t("wa.build.too_many"))
	}
	who := aiActor{tenant: c.tenant.ID, actor: c.p.Actor(), actorType: c.p.ActorType(), ip: "whatsapp"}
	wa, number, tenant, user := c.wa, c.number, c.tenant, c.p.UserID
	ready := make(chan struct{})
	req := aiBuildReq{Goal: goal}
	if c.lang.Tag != lang.EN {
		// The goal may be in the person's language: the model is told which.
		info, _ := lang.Lookup(c.lang.Tag)
		req.language = info.English
	}
	id, err := s.startAIBuild(ctx, who, req, "whatsapp", func(id uuid.UUID, prop *builder.Proposal, err error) {
		<-ready
		s.waBuildDone(wa, number, tenant, user, id, prop, err)
	})
	if err != nil {
		return err
	}
	defer close(ready)
	if err := s.saveSession(ctx, c, stateCollecting, chatData{Build: &waBuild{ID: id, Phase: buildBuilding}}, waPendingTTL); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.build.working"))
}

// waBuildDone answers when a build finishes, if the person is still
// waiting for it.
func (s *Server) waBuildDone(wa *whatsapp.Platform, number string, tenant tenantRef, user, id uuid.UUID, prop *builder.Proposal, buildErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := &chat{wa: wa, number: number, tenant: tenant}
	var err error
	if c.p, err = s.principalOf(ctx, tenant.ID, user); err != nil {
		return // no longer a member: nothing to tell
	}
	c.lang = s.chooseLang(ctx, tenant.ID, number, "")
	if err := s.loadSession(ctx, c); err != nil {
		s.Logger.Error("whatsapp build: loading the session", "err", err)
		return
	}
	if c.data.Build == nil || c.data.Build.ID != id || c.data.Build.Phase != buildBuilding {
		return // cancelled, or replaced by another command
	}
	if err := s.waBuildAnswer(ctx, c, prop, buildErr); err != nil {
		s.Logger.Error("whatsapp build: answering", "build", id, "err", err)
	}
}

func (s *Server) waBuildAnswer(ctx context.Context, c *chat, prop *builder.Proposal, buildErr error) error {
	fail := func(msg string) error {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, msg)
	}
	switch {
	case errors.Is(buildErr, ai.ErrBudgetExhausted):
		return fail(c.t("wa.build.budget_ran_out"))
	case errors.Is(buildErr, builder.ErrRefused):
		return fail(c.t("wa.build.refused"))
	case buildErr != nil || prop == nil:
		return fail(c.t("wa.build.failed", "link", s.waLink(c, "/workflows")))
	case !prop.Valid():
		return fail(c.t("wa.build.invalid", "link", s.waLink(c, "/workflows")))
	}
	b := &waBuild{ID: c.data.Build.ID, Params: map[string]any{}}
	var head strings.Builder
	headID, headKV := "wa.build.head", []string{}
	if prop.Template != nil {
		b.Template = prop.Template.ID
		headID, headKV = "wa.build.head_template", []string{"template", whatsapp.SafeText(prop.Template.Title)}
		for k, v := range prop.Template.Params {
			b.Params[k] = v
		}
		if prop.Template.Instantiated {
			b.Missing = append(b.Missing, prop.Template.Missing...)
		}
	}
	head.WriteString(c.t(headID, headKV...) + "\n\n")
	steps, err := plainSteps(prop.Definition, s, c)
	if err != nil {
		return fail(c.t("wa.build.unreadable", "link", s.waLink(c, "/workflows")))
	}
	head.WriteString(steps)
	if notes := buildNotes(c, prop); notes != "" {
		head.WriteString("\n\n" + notes)
	}
	if len(b.Missing) > 0 {
		tpl, _ := templates.Default().Get(b.Template)
		b.Phase, b.Asking = buildParams, len(b.Missing)
		if err := s.saveSession(ctx, c, stateCollecting, chatData{Build: b}, waPendingTTL); err != nil {
			return err
		}
		head.WriteString("\n\n" + c.t("wa.build.need_details", "count", strconv.Itoa(len(b.Missing))) + "\n\n")
		head.WriteString(paramPrompt(tpl, b.Missing[0], 1, len(b.Missing)))
		return c.say(ctx, s, head.String())
	}
	b.Phase = buildConfirm
	if err := s.saveSession(ctx, c, stateConfirming, chatData{Build: b}, waPendingTTL); err != nil {
		return err
	}
	head.WriteString("\n\n" + c.t("wa.build.save_question"))
	return c.say(ctx, s, head.String(), confirmSaveButtons(c)...)
}

// waLink is " at <url>" when the deployment has a public URL.
func (s *Server) waLink(c *chat, path string) string {
	if s.PublicURL == "" {
		return ""
	}
	return c.t("wa.link_at", "url", s.PublicURL+path)
}

// plainSteps reads a definition aloud for a message, bounded to fit one.
func plainSteps(doc []byte, s *Server, c *chat) (string, error) {
	def, err := wd.Load(doc)
	if err != nil {
		return "", err
	}
	reg, err := s.Registry.For(context.Background(), c.tenant.ID.String())
	if err != nil {
		return "", err
	}
	lines := wdtext.Describe(def, reg)
	const maxLines = 25
	extra := 0
	if len(lines) > maxLines {
		extra = len(lines) - maxLines
		lines = lines[:maxLines]
	}
	out := "*" + whatsapp.SafeText(wdtext.Text([]wdtext.Line{{Text: def.Name}})) + "*\n" + whatsapp.SafeText(wdtext.Text(lines))
	if extra > 0 {
		out += "\n" + c.t("wa.build.more_steps", "count", strconv.Itoa(extra))
	}
	if len(out) > 3200 {
		out = out[:3200] + "…"
	}
	return out, nil
}

// buildNotes are the warnings a person should read before saving, and
// the tenant variables a template reads, in plain words.
func buildNotes(c *chat, prop *builder.Proposal) string {
	var notes []string
	for _, w := range prop.Warnings {
		switch w.Kind {
		case "policy":
			notes = append(notes, c.t("wa.build.warn_policy"))
		case "test":
			notes = append(notes, c.t("wa.build.warn_test"))
		}
	}
	if prop.Template != nil {
		if tpl, ok := templates.Default().Get(prop.Template.ID); ok && len(tpl.Variables) > 0 {
			var names []string
			for _, v := range tpl.Variables {
				names = append(names, v.Name)
			}
			notes = append(notes, c.t("wa.build.variables", "names", strings.Join(names, ", ")))
		}
	}
	if len(notes) > 4 {
		notes = notes[:4]
	}
	return strings.Join(dedupe(notes), "\n")
}

func dedupe(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// paramPrompt asks for one template parameter.
func paramPrompt(tpl *templates.Template, name string, i, n int) string {
	p, ok := tpl.Param(name)
	if !ok {
		return name + "?"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d/%d. *%s*: %s", i, n, p.Title, p.Description)
	switch p.Type {
	case "time":
		b.WriteString(" (a time, like 9:30 or 5pm)")
	case "weekday":
		b.WriteString(" (a day, like Friday)")
	case "boolean":
		b.WriteString(" (yes or no)")
	}
	if len(p.Enum) > 0 {
		b.WriteString(" One of: " + strings.Join(p.Enum, ", ") + ".")
	}
	if p.Example != nil && p.Type != "boolean" {
		fmt.Fprintf(&b, "\nFor example: %v", p.Example)
	}
	return b.String()
}

// waBuildReply handles a message while a build is in progress.
func (s *Server) waBuildReply(ctx context.Context, c *chat, text, cmd string) error {
	b := c.data.Build
	switch b.Phase {
	case buildBuilding:
		return c.say(ctx, s, c.t("wa.build.still_drafting"))
	case buildParams:
		return s.waBuildParam(ctx, c, text)
	case buildConfirm:
		switch cmd {
		case "yes", "y", "save", "yes, save draft", "ok", "okay":
			return s.waBuildSave(ctx, c)
		case "no", "n":
			if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
				return err
			}
			return c.say(ctx, s, c.t("wa.build.not_saved"))
		}
		return c.say(ctx, s, c.t("wa.build.save_reminder"), confirmSaveButtons(c)...)
	}
	return s.saveSession(ctx, c, stateIdle, chatData{}, 0)
}

// waBuildParam takes the answer for the next missing parameter; with the
// last one, the template is filled again and checked, the proposal
// updated, and the steps shown for confirmation.
func (s *Server) waBuildParam(ctx context.Context, c *chat, text string) error {
	b := c.data.Build
	tpl, ok := templates.Default().Get(b.Template)
	if !ok || len(b.Missing) == 0 {
		return s.saveSession(ctx, c, stateIdle, chatData{}, 0)
	}
	if !c.p.Can(PermWorkflowEdit) {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.build.denied_now", "org", c.tenant.Name))
	}
	total := max(b.Asking, len(b.Missing))
	name := b.Missing[0]
	p, _ := tpl.Param(name)
	v, err := p.Coerce(text)
	if err != nil {
		return c.say(ctx, s, c.t("wa.run.does_not_fit", "reason", err.Error(), "prompt", paramPrompt(tpl, name, total-len(b.Missing)+1, total)))
	}
	if b.Params == nil {
		b.Params = map[string]any{}
	}
	b.Params[name] = v
	b.Missing = b.Missing[1:]
	if len(b.Missing) > 0 {
		if err := s.saveSession(ctx, c, stateCollecting, chatData{Build: b}, waPendingTTL); err != nil {
			return err
		}
		return c.say(ctx, s, paramPrompt(tpl, b.Missing[0], total-len(b.Missing)+1, total))
	}
	doc, err := tpl.Instantiate(b.Params, "")
	if err != nil {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.build.details_misfit", "reason", whatsapp.SafeText(err.Error())))
	}
	if probs := s.check(ctx, c.tenant.ID, doc); len(probs) > 0 {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.build.details_invalid", "problem", whatsapp.SafeText(probs[0].Message), "link", s.waLink(c, "/workflows")))
	}
	params, _ := json.Marshal(b.Params)
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE ai_builds SET proposal = jsonb_set(jsonb_set(jsonb_set(proposal, '{definition}', $2::jsonb), '{template,params}', $3::jsonb),
			'{template,missing}', '[]'::jsonb) WHERE id = $1 AND status = 'proposed'`, b.ID, doc, params)
		if err == nil && tag.RowsAffected() != 1 {
			err = pgx.ErrNoRows
		}
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.build.gone"))
	}
	if err != nil {
		return err
	}
	b.Phase = buildConfirm
	if err := s.saveSession(ctx, c, stateConfirming, chatData{Build: b}, waPendingTTL); err != nil {
		return err
	}
	steps, err := plainSteps(doc, s, c)
	if err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.build.with_details", "steps", steps), confirmSaveButtons(c)...)
}

// waBuildSave saves the confirmed proposal as a draft in the person's
// name: workflow.edit is checked again, nothing is published.
func (s *Server) waBuildSave(ctx context.Context, c *chat) error {
	b := c.data.Build
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	if !c.p.Can(PermWorkflowEdit) {
		return c.say(ctx, s, c.t("wa.build.denied_save", "org", c.tenant.Name))
	}
	var wf uuid.UUID
	var name string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		var err error
		var doc []byte
		wf, _, doc, err = s.saveAIBuildTx(ctx, tx, c.p, b.ID, "", func(action, target string, detail map[string]any) error {
			return waAudit(ctx, tx, c.p, action, target, detail)
		})
		if err == nil {
			var d struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(doc, &d)
			name = d.Name
		}
		return err
	})
	if le, ok := runtime.IsLimit(err); ok {
		return c.say(ctx, s, c.t("wa.build.limit", "reason", le.Message))
	}
	if errors.Is(err, errConflict) || errors.Is(err, pgx.ErrNoRows) {
		return c.say(ctx, s, c.t("wa.build.gone"))
	}
	if err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.build.saved", "name", whatsapp.SafeText(name), "link", s.waLink(c, "/workflows/"+wf.String())))
}
