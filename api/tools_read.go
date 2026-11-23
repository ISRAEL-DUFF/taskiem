package api

// The reader knows your tools (made tools M3, §6 of
// docs/spec/made-tools.md). A message read in this order: a question
// Taskiem is waiting on; an exact /command; Taskiem's own word lists
// (paying, saving, money, stock, orders, building…); a tool's command or a
// phrase it says; then the model, which is told Taskiem's commands, the
// saved things' names and kinds, and after them the business's tools the
// person may use (command, name, about, inputs; at most 40, the nearest by
// words), as data. It may answer with one tool and the values it heard,
// or none. It never starts a run and never says yes: the values are
// always shown first, through the tool's own way in (the echo for a tool
// that only reads, yes for one that writes, the app or the web for one
// that moves money), and two tools that fit as well are a numbered pick.
//
// A value written @name (proposed by the model, typed after a command, or
// given as an answer) is one of the business's saved things, looked up
// here by its name as "@name" is everywhere else: the model is told names
// and kinds only, never what they hold. What fills the input is the
// thing's own words for it (a place's address, a person's name or phone, a
// text's body); never a bank account or anything sealed, and never a
// person's own saved thing, which is no business's input.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/understand"
	"github.com/israel-duff/taskiem/engine/billing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/saved"
	"github.com/israel-duff/taskiem/engine/wdcheck"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// chatToolPick is two tools that fit a message, waiting for a number:
// their commands, and what was heard for them (the model's values, or
// the words after a phrase both say).
type chatToolPick struct {
	Commands []string          `json:"commands"`
	Values   map[string]string `json:"values,omitempty"`
	Rest     string            `json:"rest,omitempty"`
}

// toolInfoOf is a tool as the model is told it: its command, name, about
// and the inputs its form asks (fields).
func toolInfoOf(t toolRow, fields []whatsapp.Field) understand.ToolInfo {
	info := understand.ToolInfo{Command: t.Command, Name: t.Name, About: t.About}
	for _, f := range fields {
		in := understand.ToolInput{Name: f.Name, Title: f.Title, Type: f.Type, Required: f.Required}
		for _, e := range f.Enum {
			in.Choices = append(in.Choices, fmt.Sprint(e))
		}
		info.Inputs = append(info.Inputs, in)
	}
	return info
}

// modelTools are the business's tools the person in the chat may use, as
// the model is told them; none without run.start.
func (s *Server) modelTools(ctx context.Context, c *chat) []understand.ToolInfo {
	if c.p == nil || !c.p.Can(PermRunStart) {
		return nil
	}
	tools, err := s.chatToolsOf(ctx, c.tenant.ID)
	if err != nil {
		s.Logger.Warn("tools: naming the tools for the model", "err", err)
		return nil
	}
	var out []understand.ToolInfo
	for _, t := range tools {
		if !mayStart(c.p, t.Roles) {
			continue
		}
		fields, err := s.waFieldsOf(ctx, c.tenant.ID, t.Workflow, t.Version)
		if err != nil {
			continue // inputs a chat cannot ask: not offered here
		}
		out = append(out, toolInfoOf(t, fields))
	}
	return out
}

// hasModelTools reports whether the person has tools of the business's to
// be read by the model (understand), which then reads messages the
// shorter router (intent) would have.
func (s *Server) hasModelTools(ctx context.Context, c *chat) bool {
	if c.p == nil || !c.p.Can(PermRunStart) || s.chatModel() == nil {
		return false
	}
	tools, err := s.chatToolsOf(ctx, c.tenant.ID)
	if err != nil {
		return false
	}
	for _, t := range tools {
		if mayStart(c.p, t.Roles) {
			return true
		}
	}
	return false
}

// knownTool reports whether the model's tool (and its pick) are the
// person's own to use.
func (s *Server) knownTool(ctx context.Context, c *chat, res understand.Result) bool {
	_, ok := s.chatToolNamed(ctx, c, res.ID)
	return ok
}

// chatToolNamed is the tool of the business by command that the person
// may start.
func (s *Server) chatToolNamed(ctx context.Context, c *chat, command string) (toolRow, bool) {
	if command == "" || c.p == nil || !c.p.Can(PermRunStart) {
		return toolRow{}, false
	}
	tools, err := s.chatToolsOf(ctx, c.tenant.ID)
	if err != nil {
		return toolRow{}, false
	}
	for _, t := range tools {
		if t.Command == command && mayStart(c.p, t.Roles) {
			return t, true
		}
	}
	return toolRow{}, false
}

// waToolProposed does what the model read: one tool, its values shown
// before anything runs; or two, to pick by number.
func (s *Server) waToolProposed(ctx context.Context, c *chat, res understand.Result) error {
	t, ok := s.chatToolNamed(ctx, c, res.ID)
	if !ok {
		return c.say(ctx, s, notUnderstood(c))
	}
	if p, ok := s.chatToolNamed(ctx, c, res.Pick); ok && p.Command != t.Command {
		return s.waToolPick(ctx, c, []toolRow{t, p}, res.Values, "")
	}
	return s.waToolChosen(ctx, c, t, "", res.Values)
}

// toolsSaying are the person's tools whose phrase starts text, when more
// than one does (two tools that say the same words), each with the words
// after it.
func toolsSaying(tools []toolRow, p *Principal, text string) []toolRow {
	folded := lang.Fold(text)
	var out []toolRow
	for _, t := range tools {
		if !mayStart(p, t.Roles) {
			continue
		}
		for _, ph := range t.Says {
			fp := lang.Fold(ph)
			if fp != "" && (folded == fp || strings.HasPrefix(folded, fp+" ")) {
				out = append(out, t)
				break
			}
		}
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

// waToolPick asks which of two (or more) tools, by number, keeping what
// was heard for them.
func (s *Server) waToolPick(ctx context.Context, c *chat, tools []toolRow, values map[string]string, rest string) error {
	tools = tools[:min(waPickMax, len(tools))]
	pick := chatToolPick{Values: values, Rest: rest}
	lines := make([]string, len(tools))
	for i, t := range tools {
		pick.Commands = append(pick.Commands, t.Command)
		cmd := "/" + t.Command
		if c.ch.Name() == "slack" {
			cmd = t.Command
		}
		lines[i] = fmt.Sprintf("%d. %s (%s): %s", i+1, whatsapp.SafeText(t.Name), cmd, whatsapp.SafeText(t.About))
	}
	if err := s.saveSession(ctx, c, stateCollecting, chatData{ToolPick: &pick}, waPendingTTL); err != nil {
		return err
	}
	return c.say(ctx, s, c.t("wa.tool.which", "list", strings.Join(lines, "\n")))
}

// waToolPickReply takes the number of a tool from the pick; anything else
// lets the pick go and is read as a new message (false).
func (s *Server) waToolPickReply(ctx context.Context, c *chat, cmd string) (bool, error) {
	pick := *c.data.ToolPick
	n, err := strconv.Atoi(strings.TrimSpace(cmd))
	if err != nil || n < 1 || n > len(pick.Commands) {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return true, err
		}
		c.state, c.data = stateIdle, chatData{}
		return false, nil
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return true, err
	}
	c.state, c.data = stateIdle, chatData{}
	t, ok := s.chatToolNamed(ctx, c, pick.Commands[n-1])
	if !ok {
		return true, c.say(ctx, s, c.t("wa.run.changed", "workflow", pick.Commands[n-1]))
	}
	return true, s.waToolChosen(ctx, c, t, pick.Rest, pick.Values)
}

// waToolChosen calls tool t with what was heard: values by input name
// (the model's), or the words after its command or phrase (rest). It is
// waMadeTool's way in, for the reader: held to the workflow's roles and to
// its care, every value shown before anything runs.
func (s *Server) waToolChosen(ctx context.Context, c *chat, t toolRow, rest string, values map[string]string) error {
	if !mayStart(c.p, t.Roles) {
		if err := db0(ctx, s, c.tenant.ID, func(tx pgx.Tx) error {
			return auditStartRefused(ctx, tx, c.ch.Name(), c.p, "tool:"+t.Command, t.Roles)
		}); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.run.not_yours", "workflow", t.Name, "roles", startRolesWords(t.Roles)))
	}
	if t.Asks() == wdcheck.CareStepUp {
		// Money moves only on a passkey: in the app or on the web.
		msg := c.t("wa.tool.step_up", "tool", t.Name)
		if s.PublicURL != "" {
			msg += "\n" + strings.TrimRight(s.PublicURL, "/") + "/tools/" + t.Command
		}
		return c.say(ctx, s, msg)
	}
	fields, err := s.waFieldsOf(ctx, c.tenant.ID, t.Workflow, t.Version)
	if errors.Is(err, whatsapp.ErrInputsUnsupported) {
		return c.say(ctx, s, c.t("wa.run.unsupported_inputs", "workflow", t.Name))
	}
	if err != nil {
		return err
	}
	data := chatData{Workflow: t.Workflow, Name: t.Name, Version: t.Version, Environment: t.Environment, Inputs: map[string]any{}, Tool: t.Command}
	if len(fields) == 0 {
		return s.waAskConfirm(ctx, c, data)
	}
	if values == nil && rest != "" {
		if len(fields) == 1 {
			// One input: the rest of the message is it, read as an answer.
			c.state, c.data = stateCollecting, data
			return s.waCollect(ctx, c, rest)
		}
		values = s.chatToolWords(ctx, c, t, fields, rest)
	}
	return s.waToolFill(ctx, c, data, fields, values)
}

// chatToolWords are the values the words after a tool's command give its
// inputs, read by the model within the business's AI budget; none without
// it (the inputs are then asked).
func (s *Server) chatToolWords(ctx context.Context, c *chat, t toolRow, fields []whatsapp.Field, words string) map[string]string {
	if !s.limiter("wa-understand:"+c.tenant.ID.String()+":"+c.addr, time.Hour/understandPerHour, understandPerHour).Allow() {
		return nil
	}
	return s.readToolWords(ctx, c.tenant.ID, c.p, c.lang.Tag, toolInfoOf(t, fields), words)
}

// readToolWords asks the model the values words give a tool's inputs,
// within the plan's AI feature and the business's budget; nil when it
// cannot be asked or its answer cannot be used.
func (s *Server) readToolWords(ctx context.Context, tenant uuid.UUID, p *Principal, l lang.Tag, tool understand.ToolInfo, words string) map[string]string {
	model := s.chatModel()
	if model == nil || strings.TrimSpace(words) == "" || s.Billing.Require(ctx, tenant, billing.FeatureAI) != nil {
		return nil
	}
	limit, used, err := s.aiBudget(ctx, tenant)
	if err != nil || limit > 0 && used >= limit {
		return nil
	}
	info, _ := lang.Lookup(l)
	if info.Tag == "" {
		info, _ = lang.Lookup(lang.EN)
	}
	release, err := s.reserveAI(ctx, tenant, "tool_values", understand.ValuesRequest(words, tool, info, 0).Estimate(), chatAIReservation)
	if err != nil {
		return nil
	}
	defer release()
	vals, resp, err := (&understand.Helper{Provider: model}).Values(ctx, words, tool, info)
	outcome, errText := "valid", ""
	switch {
	case errors.Is(err, understand.ErrUnusable):
		outcome = "unparseable"
	case err != nil:
		outcome, errText = "error", err.Error()
	case len(vals) == 0:
		outcome = "unknown"
	}
	s.recordModelCallFor(ctx, tenant, p.Actor(), "tool_values", "toolvalues", model, resp, outcome, errText)
	if err != nil && !errors.Is(err, understand.ErrUnusable) && !errors.Is(err, ai.ErrNotConfigured) && !errors.Is(err, context.Canceled) {
		s.Logger.Warn("tools: reading a tool's values", "err", err)
	}
	return vals
}

// waToolFill puts what was heard into a tool's inputs, each read as its
// question would read it (a saved thing by @name looked up here), then:
// every input filled, the tool's care (the echo, or what will run and
// yes); else what was read, and the first input still to give.
func (s *Server) waToolFill(ctx context.Context, c *chat, data chatData, fields []whatsapp.Field, values map[string]string) error {
	var notes []string
	for _, f := range fields {
		raw, ok := values[f.Name]
		if !ok {
			continue
		}
		v, note, err := s.toolValueOf(ctx, c.tenant.ID, c.p, c.lang.Tag, f, raw)
		if err != nil {
			return err
		}
		if note != "" {
			notes = append(notes, note)
		}
		if v != nil {
			data.Inputs[f.Name] = v
		}
	}
	missing := -1
	for i, f := range fields {
		if _, ok := data.Inputs[f.Name]; !ok {
			missing = i
			break
		}
	}
	if missing < 0 {
		if len(notes) > 0 {
			if err := c.say(ctx, s, strings.Join(notes, "\n")); err != nil {
				return err
			}
		}
		return s.waAskConfirm(ctx, c, data)
	}
	data.Index = missing
	if err := s.saveSession(ctx, c, stateCollecting, data, waPendingTTL); err != nil {
		return err
	}
	prompt := fields[missing].PromptIn(c.lang.Tag)
	msg := c.t("wa.run.needs_inputs", "workflow", data.Name, "count", strconv.Itoa(len(fields)), "prompt", prompt)
	if len(data.Inputs) > 0 {
		lines, err := s.maskedLines(ctx, c.tenant.ID, data.Inputs, 12)
		if err != nil {
			return err
		}
		msg = c.t("wa.tool.read", "tool", data.Name, "values", strings.Join(lines, "\n"), "prompt", prompt)
	}
	if len(notes) > 0 {
		msg = strings.Join(notes, "\n") + "\n\n" + msg
	}
	return c.say(ctx, s, msg)
}

// savedRefRe is a value that is only a saved thing's @name.
var savedRefRe = regexp.MustCompile(`^@([a-z0-9][a-z0-9_-]{0,31})$`)

// savedRef is the saved thing's name a value is, or "".
func savedRef(v string) string {
	m := savedRefRe.FindStringSubmatch(strings.ToLower(strings.TrimSpace(v)))
	if m == nil {
		return ""
	}
	n, err := saved.Name(m[1])
	if err != nil {
		return ""
	}
	return n
}

// toolNumberRe is an amount as people write it: "4,500", "20k", "₦1.5m".
var toolNumberRe = regexp.MustCompile(`(?i)^(?:₦|ngn|n)?\s*([0-9][0-9,]*(?:\.[0-9]+)?)\s*(k|m)?$`)

// toolNumber is an amount for a number input in digits: "20k" is 20000.
func toolNumber(f whatsapp.Field, v string) string {
	if f.Type != "number" && f.Type != "integer" {
		return v
	}
	m := toolNumberRe.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return v
	}
	n, err := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", ""), 64)
	if err != nil {
		return v
	}
	switch strings.ToLower(m[2]) {
	case "k":
		n *= 1e3
	case "m":
		n *= 1e6
	}
	return strconv.FormatFloat(n, 'f', -1, 64)
}

// toolValueOf is a value heard for a tool's input, as the input takes it:
// a saved thing's own words for @name, then read as the field reads an
// answer; personal data sealed, as it waits in the chat. nil with a note
// (in the person's language) when it cannot fill the input, which is then
// asked.
func (s *Server) toolValueOf(ctx context.Context, tenant uuid.UUID, p *Principal, l lang.Tag, f whatsapp.Field, raw string) (any, string, error) {
	category := f.PII
	name := savedRef(raw)
	if name != "" {
		v, cat, note, err := s.savedToolValue(ctx, tenant, p, l, f, name)
		if err != nil || note != "" {
			return nil, note, err
		}
		raw = v
		if category == "" {
			category = cat
		}
	}
	v, err := f.ParseIn(toolNumber(f, raw), l)
	if err != nil {
		if name != "" {
			return nil, tr(l, "wa.tool.saved_not_fit", "name", name, "label", inputLabel(f)), nil
		}
		return nil, "", nil //nolint:nilerr // a value that does not fit: the input is asked
	}
	if category != "" {
		err := db0(ctx, s, tenant, func(tx pgx.Tx) error {
			env, err := s.Store.PII.SealTx(ctx, tx, tenant, category, v)
			v = env
			return err
		})
		if err != nil {
			return nil, "", err
		}
	}
	return v, "", nil
}

// savedToolValue is what the business's saved thing @name gives a tool's
// input f: a place's address (or phone), a person's name (or phone or
// email), a text's words, a provider account's number, another kind's
// title. A phone or an email comes with its personal-data category, so it
// is shown masked. A note says why not: no such thing of the business's,
// or a kind that cannot fill an input (a bank account, a file).
func (s *Server) savedToolValue(ctx context.Context, tenant uuid.UUID, p *Principal, l lang.Tag, f whatsapp.Field, name string) (value, category, note string, err error) {
	if p == nil || p.UserID == uuid.Nil || tenant == uuid.Nil {
		return "", "", tr(l, "wa.tool.saved_none", "name", name), nil
	}
	var x savedRow
	found := false
	o := savedOwner{tenant: tenant, by: p.UserID}
	err = s.savedTx(ctx, o, func(tx pgx.Tx) error {
		where, owner := ownerWhere(o)
		var err error
		x, err = scanSaved(tx.QueryRow(ctx, `SELECT `+savedCols+` FROM saved_objects WHERE `+where+` AND name = $2`, owner, name))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		found = err == nil
		return err
	})
	if err != nil {
		return "", "", "", err
	}
	if !found {
		return "", "", tr(l, "wa.tool.saved_none", "name", name), nil
	}
	label := inputLabel(f)
	str := func(k string) string { v, _ := x.Details[k].(string); return strings.TrimSpace(v) }
	words := strings.ToLower(f.Name + " " + f.Title)
	wants := func(ws ...string) bool {
		for _, w := range ws {
			if strings.Contains(words, w) {
				return true
			}
		}
		return false
	}
	switch x.Kind {
	case saved.KindBankAccount, saved.KindFile:
		return "", "", tr(l, "wa.tool.saved_kind", "name", name, "kind", savedKindWords(x), "label", label), nil
	case saved.KindPlace:
		switch {
		case wants("phone", "number", "tel"):
			value, category = str("phone"), "phone"
		default:
			_, value = placeDeliverTo(x)
			value = strings.TrimSpace(value)
		}
	case saved.KindPerson:
		switch {
		case wants("phone", "number", "tel", "whatsapp"):
			value, category = str("phone"), "phone"
		case wants("email", "mail"):
			value, category = str("email"), "email"
		case wants("address", "where", "place", "location"):
			value = str("address")
		default:
			value = x.Title
		}
	case saved.KindText:
		value = str("body")
	case saved.KindProviderAccount:
		value = str("number")
	default:
		value = x.Title
	}
	if value == "" {
		return "", "", tr(l, "wa.tool.saved_kind", "name", name, "kind", savedKindWords(x), "label", label), nil
	}
	return value, category, "", nil
}

// inputLabel is what an input is called in a message: its title, else its
// name.
func inputLabel(f whatsapp.Field) string {
	if f.Title != "" {
		return f.Title
	}
	return f.Name
}

// --- the API ---

// readToolRoute is POST /v1/tools/{command}/read {text}: the reader for the
// web's and the app's sheets, words in and proposed values out ("/expense
// 4500 diesel" opens the form filled in). A tool with one input takes the
// words as it, with no model; any other is read by the model within the
// business's AI budget. A saved thing by @name is looked up here, as the
// chat does. Nothing is started: the sheet shows the values and starts
// the tool as it always does.
func (s *Server) readToolRoute(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var req struct {
		Text        string `json:"text"`
		Environment string `json:"environment,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	text := strings.TrimSpace(req.Text)
	if len([]rune(text)) > 300 {
		s.fail(w, r, fmt.Errorf("%w: the words are at most 300 characters", errBadRequest))
		return
	}
	env, err := environment(p, req.Environment)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var t toolRow
	if err := s.tx(r, func(tx pgx.Tx) error {
		t, err = s.toolFor(r.Context(), tx, p, env, chi.URLParam(r, "command"))
		return err
	}); err != nil {
		s.toolRefused(w, r, err, chi.URLParam(r, "command"))
		return
	}
	out := map[string]any{"command": t.Command, "values": map[string]any{}, "notes": []string{}}
	fields, err := s.waFieldsOf(r.Context(), p.TenantID, t.Workflow, t.Version)
	if err != nil || text == "" || len(fields) == 0 {
		// Inputs a form reads but words cannot: nothing proposed.
		writeJSON(w, http.StatusOK, out)
		return
	}
	if p.UserID != uuid.Nil && !s.limiter("tool-read:"+p.UserID.String(), time.Minute, 20).Allow() {
		writeErr(w, http.StatusTooManyRequests, "slow down: a moment between requests")
		return
	}
	heard := map[string]string{}
	if len(fields) == 1 {
		heard[fields[0].Name] = text
	} else {
		heard = s.readToolWords(r.Context(), p.TenantID, p, lang.EN, toolInfoOf(t, fields), text)
	}
	values := map[string]any{}
	var notes []string
	for _, f := range fields {
		raw, ok := heard[f.Name]
		if !ok {
			continue
		}
		v, note := raw, ""
		if name := savedRef(raw); name != "" {
			if v, _, note, err = s.savedToolValue(r.Context(), p.TenantID, p, lang.EN, f, name); err != nil {
				s.fail(w, r, err)
				return
			}
		}
		if note != "" {
			notes = append(notes, note)
			continue
		}
		if parsed, err := f.ParseIn(toolNumber(f, v), lang.EN); err == nil {
			values[f.Name] = parsed
		}
	}
	out["values"] = values
	if notes != nil {
		out["notes"] = notes
	}
	writeJSON(w, http.StatusOK, out)
}
