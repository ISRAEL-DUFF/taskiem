package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/wdcheck"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Your tools in the chat (made tools M1): on WhatsApp, Telegram, Slack and
// the web's Taskiem conversation a member calls a tool by "/command" (on
// Slack, whose "/" commands are Slack's own, by the plain command), by the
// plain command where it clashes with nothing (Taskiem's own words cannot
// be one), or by a phrase it says. Its inputs are asked as run asks them
// (a WhatsApp form, else one by one); a tool with one input takes the rest
// of the message as it. Then its care: a tool that only reads starts at
// once, with an echo of every value read; any other shows what will run
// and waits for yes; one that moves money is confirmed in the app or on
// the web, never by a typed yes. The run is the member's, held to the
// workflow's roles, and its answer comes back in the chat when it ends.

// chatTools are the business's tools in prod, for members.
func (s *Server) chatToolsOf(ctx context.Context, tenant uuid.UUID) ([]toolRow, error) {
	var out []toolRow
	err := db0(ctx, s, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+toolCols+` FROM workflow_tools WHERE environment = 'prod' AND audience = 'members' ORDER BY name`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (toolRow, error) { return scanTool(row) })
		return err
	})
	return out, err
}

// toolCalled finds the tool a message calls, and what follows its words:
// "/expense 4500 diesel", "expense 4500 diesel", or a phrase it says at the
// start of the message. A phrase is folded as the reader folds words.
func toolCalled(tools []toolRow, text string) (toolRow, string, bool) {
	words := strings.Fields(text)
	if len(words) == 0 {
		return toolRow{}, "", false
	}
	first := strings.ToLower(strings.TrimPrefix(words[0], "/"))
	rest := strings.TrimSpace(strings.Join(words[1:], " "))
	for _, t := range tools {
		if first == t.Command {
			return t, rest, true
		}
	}
	folded := lang.Fold(text)
	for _, t := range tools {
		for _, p := range t.Says {
			fp := lang.Fold(p)
			if fp != "" && (folded == fp || strings.HasPrefix(folded, fp+" ")) {
				n := len(strings.Fields(p))
				return t, strings.TrimSpace(strings.Join(words[min(n, len(words)):], " ")), true
			}
		}
	}
	return toolRow{}, "", false
}

// waMadeTool calls a tool from the chat; false when the message calls
// none.
func (s *Server) waMadeTool(ctx context.Context, c *chat, text string) (bool, error) {
	if c.state != stateIdle || c.in.Reply != "" || !c.p.Can(PermRunStart) {
		return false, nil
	}
	tools, err := s.chatToolsOf(ctx, c.tenant.ID)
	if err != nil || len(tools) == 0 {
		return false, err
	}
	t, rest, ok := toolCalled(tools, text)
	if !ok {
		return false, nil
	}
	if !mayStart(c.p, t.Roles) {
		if err := db0(ctx, s, c.tenant.ID, func(tx pgx.Tx) error {
			return auditStartRefused(ctx, tx, c.ch.Name(), c.p, "tool:"+t.Command, t.Roles)
		}); err != nil {
			return true, err
		}
		return true, c.say(ctx, s, c.t("wa.run.not_yours", "workflow", t.Name, "roles", startRolesWords(t.Roles)))
	}
	if t.Asks() == wdcheck.CareStepUp {
		// Money moves only on a passkey: in the app or on the web.
		msg := c.t("wa.tool.step_up", "tool", t.Name)
		if s.PublicURL != "" {
			msg += "\n" + strings.TrimRight(s.PublicURL, "/") + "/tools/" + t.Command
		}
		return true, c.say(ctx, s, msg)
	}
	fields, err := s.waFieldsOf(ctx, c.tenant.ID, t.Workflow, t.Version)
	if errors.Is(err, whatsapp.ErrInputsUnsupported) {
		return true, c.say(ctx, s, c.t("wa.run.unsupported_inputs", "workflow", t.Name))
	}
	if err != nil {
		return true, err
	}
	data := chatData{Workflow: t.Workflow, Name: t.Name, Version: t.Version, Environment: t.Environment, Inputs: map[string]any{}, Tool: t.Command}
	if len(fields) == 0 {
		return true, s.waAskConfirm(ctx, c, data)
	}
	if len(fields) == 1 && rest != "" {
		// One input: the rest of the message is it, read as an answer.
		c.state, c.data = stateCollecting, data
		return true, s.waCollect(ctx, c, rest)
	}
	if sent, err := s.waSendInputsFlow(ctx, c, data, fields); err != nil || sent {
		return true, err
	}
	if err := s.saveSession(ctx, c, stateCollecting, data, waPendingTTL); err != nil {
		return true, err
	}
	return true, c.say(ctx, s, c.t("wa.run.needs_inputs", "workflow", t.Name, "count", fmt.Sprint(len(fields)), "prompt", fields[0].PromptIn(c.lang.Tag)))
}

// waToolReady is where a tool's inputs end: a tool that only reads starts
// at once, with the echo; any other shows what will run and waits for yes
// (waAskConfirm). True when it was started here.
func (s *Server) waToolReady(ctx context.Context, c *chat, data chatData) (bool, error) {
	t, err := s.chatToolNow(ctx, c, data)
	if err != nil || t == nil {
		return t == nil && err == nil, err
	}
	if t.Asks() != wdcheck.CareAnswers {
		return false, nil
	}
	data.Nonce = uuid.NewString()
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return true, err
	}
	lines, err := s.maskedLines(ctx, c.tenant.ID, data.Inputs, 12)
	if err != nil {
		return true, err
	}
	echo := c.t("wa.tool.echo", "tool", t.Name)
	if len(lines) > 0 {
		echo = c.t("wa.tool.echo_values", "tool", t.Name, "values", strings.Join(lines, "\n"))
	}
	if err := c.say(ctx, s, echo); err != nil {
		return true, err
	}
	return true, s.waToolRun(ctx, c, *t, data)
}

// chatToolNow is the tool a session's data names as it is deployed now:
// nil (and said why) when it is gone, changed, or no longer the person's.
func (s *Server) chatToolNow(ctx context.Context, c *chat, data chatData) (*toolRow, error) {
	var t toolRow
	err := db0(ctx, s, c.tenant.ID, func(tx pgx.Tx) error {
		var err error
		t, err = s.toolFor(ctx, tx, c.p, data.Environment, data.Tool)
		return err
	})
	switch {
	case errors.Is(err, errForbidden):
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return nil, err
		}
		return nil, c.say(ctx, s, c.t("wa.run.not_yours", "workflow", t.Name, "roles", startRolesWords(t.Roles)))
	case errors.Is(err, errNotFound) || err == nil && t.Version != data.Version:
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return nil, err
		}
		return nil, c.say(ctx, s, c.t("wa.run.changed", "workflow", data.Name))
	case err != nil:
		return nil, err
	}
	return &t, nil
}

// waToolRun starts a tool's run from the chat with the session's values,
// once (data.Nonce), and says it started; the answer comes by the run
// watch when it ends.
func (s *Server) waToolRun(ctx context.Context, c *chat, t toolRow, data chatData) error {
	if !s.limiter("wa-run:"+c.addr, 20*time.Second, 3).Allow() {
		return c.say(ctx, s, c.t("wa.run.too_many"))
	}
	var input map[string]any
	if err := db0(ctx, s, c.tenant.ID, func(tx pgx.Tx) error {
		v, err := pii.Open(ctx, s.Store.PII, tx, c.tenant.ID, map[string]any(data.Inputs), pii.Taint{})
		input, _ = v.(map[string]any)
		return err
	}); err != nil {
		return err
	}
	_, _, err := s.runTool(ctx, c.p, t, input, c.ch.Name(), data.Nonce)
	if le, ok := runtime.IsLimit(err); ok {
		return c.say(ctx, s, c.t("wa.run.limit", "reason", le.Message))
	}
	if err != nil {
		return err
	}
	if t.AnswerType == "" {
		return c.say(ctx, s, c.t("wa.run.started", "workflow", t.Name))
	}
	return nil // the answer is the reply
}

// toolAnswerWords is a tool's answer as a chat message: the text; a card's
// title, lines and amount; a list's title and first ten rows.
func toolAnswerWords(l lang.Tag, a madeAnswer) string {
	switch a.Type {
	case "text":
		return a.Text
	case "card":
		lines := []string{"*" + a.Title + "*"}
		if a.Ref != "" {
			lines[0] += " · " + a.Ref
		}
		if a.State != nil && a.State.Text != "" {
			lines = append(lines, a.State.Text)
		}
		for _, x := range a.Lines {
			lines = append(lines, x.Label+": "+x.Value)
		}
		if a.Amount != nil {
			lines = append(lines, invoicing.Naira(*a.Amount))
		}
		return strings.Join(lines, "\n")
	case "list":
		var lines []string
		if a.Title != "" {
			lines = append(lines, "*"+a.Title+"*")
		}
		if len(a.Rows) == 0 {
			empty := a.Empty
			if empty == "" {
				empty = tr(l, "wa.tool.list_empty")
			}
			return strings.Join(append(lines, empty), "\n")
		}
		for i, r := range a.Rows {
			if i == 10 {
				lines = append(lines, tr(l, "wa.tool.list_more", "count", fmt.Sprint(len(a.Rows)-10)))
				break
			}
			row := "• " + r.Title
			if r.Sub != "" {
				row += " · " + r.Sub
			}
			lines = append(lines, row)
		}
		return strings.Join(lines, "\n")
	}
	return ""
}

// madeToolLines are the business's tools the person may use, as the chat
// lists them: "• /expense: Log an expense. Record money spent".
func (s *Server) madeToolLines(ctx context.Context, c *chat) ([]string, error) {
	if !c.p.Can(PermRunStart) {
		return nil, nil
	}
	tools, err := s.chatToolsOf(ctx, c.tenant.ID)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, t := range tools {
		if !mayStart(c.p, t.Roles) {
			continue
		}
		cmd := "/" + t.Command
		if c.ch.Name() == "slack" {
			cmd = t.Command // Slack keeps "/" for its own commands
		}
		out = append(out, fmt.Sprintf("• %s: %s. %s", cmd, whatsapp.SafeText(t.Name), whatsapp.SafeText(t.About)))
	}
	return out, nil
}
