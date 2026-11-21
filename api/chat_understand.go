package api

// When nothing recognises a message in a business's chat with Taskiem, the
// model reads the commands the person may send (waHelp, the list they would
// otherwise have been sent) beside their message, and helps
// (engine/ai/understand): it rewrites the message as one command, asks for
// what is missing, or says what Taskiem can do nearest to it. The long list
// comes only when the person asks for help.
//
// A command the model gives is read as if the person had typed it, with
// their permissions and every confirmation the command has. One that would
// change something without asking first (a sale, a task, a message to a
// customer) is shown to the person, and done only on their Yes. The model
// never confirms: yes, no, cancel and a button's own words are refused.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/understand"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// understandPerHour bounds the model's help per chat.
const understandPerHour = 30

// chatUnderstand is the model's help waiting on the person: a command to
// confirm, or the message it asked a question about.
type chatUnderstand struct {
	Command string `json:"command,omitempty"`
	Said    string `json:"said,omitempty"`
}

// understoodReads are commands that only show something: done at once.
var understoodReads = map[string]bool{
	"help": true, "status": true, "approvals": true, "tables": true, "orders": true, "bookings": true, "today": true,
	"deliveries": true, "riders": true, "suppliers": true, "stock": true, "who owes me": true, "my tasks": true,
	"my page": true, "qr": true, "tools": true, "fronts": true, "plan": true,
}

// understoodReadPrefixes start commands that only show something.
var understoodReadPrefixes = []string{"find ", "how many ", "how much ", "who owes ", "how did we do", "what ", "which ", "total "}

// understoodAsking start commands that ask the person before they do
// anything (a money entry, an invoice, a build, a run, a row).
var understoodAsking = []string{"invoice ", "spent ", "cash in ", "cash out ", "build ", "run ", "add to "}

// understoodAtOnce reports whether a command from the model may be done
// without the person's Yes: it only shows something, or asks by itself.
func understoodAtOnce(command string) bool {
	cmd := normalizeCommand(command)
	if understoodReads[cmd] {
		return true
	}
	for _, p := range append(understoodReadPrefixes, understoodAsking...) {
		if strings.HasPrefix(cmd, p) {
			return true
		}
	}
	return false
}

// understoodRefused reports a command the model may not give: an answer
// to a question (yes, no, cancel), or a button's own words.
func understoodRefused(c *chat, command string) bool {
	if m, ok := lang.MatchIntent(command, c.lang.Match()); ok {
		switch m.Intent {
		case lang.IntentYes, lang.IntentNo, lang.IntentCancel:
			return true
		}
	}
	switch normalizeCommand(command) {
	case "yes", "no", "cancel", "stop", "confirm", "y", "n":
		return true
	}
	for _, p := range []string{linkPrefix, orderPrefix, billPairPrefix, taskPrefix} {
		if strings.HasPrefix(command, p) {
			return true
		}
	}
	return false
}

// notUnderstood is the short answer when nothing, the model included,
// could help: how to see what Taskiem can do, not all of it.
func notUnderstood(c *chat) string {
	return c.t("wa.not_understood") + " " + c.t("wa.not_understood_help")
}

// waUnderstand asks the model about a message nothing recognised; false
// when the model is not there, not on the plan, out of budget or limit,
// or its answer cannot be used, so the short answer follows.
func (s *Server) waUnderstand(ctx context.Context, c *chat, text string) (bool, error) {
	model := s.chatModel()
	if model == nil || c.p == nil || strings.TrimSpace(text) == "" || !s.aiOnPlan(ctx, c) {
		return false, nil
	}
	limit, used, err := s.aiBudget(ctx, c.tenant.ID)
	if err != nil {
		return false, err
	}
	if limit > 0 && used >= limit {
		return false, nil
	}
	if !s.limiter("wa-understand:"+c.tenant.ID.String()+":"+c.addr, time.Hour/understandPerHour, understandPerHour).Allow() {
		return false, nil
	}
	info, _ := lang.Lookup(c.lang.Tag)
	if info.Tag == "" {
		info, _ = lang.Lookup(lang.EN)
	}
	commands := s.modelCatalogue(ctx, c)
	release, err := s.reserveAI(ctx, c.tenant.ID, "understand", understand.Request(text, commands, info, 0).Estimate(), chatAIReservation)
	if err != nil {
		return false, nil //nolint:nilerr // over the budget: the short answer follows
	}
	defer release()
	res, resp, err := (&understand.Helper{Provider: model}).Understand(ctx, text, commands, info)
	outcome, errText := "valid", ""
	switch {
	case errors.Is(err, understand.ErrUnusable):
		outcome = "unparseable"
	case err != nil:
		outcome, errText = "error", err.Error()
	case res.Kind == understand.Command && understoodRefused(c, res.Command):
		outcome = "refused"
	case (res.Kind == understand.Action || res.Kind == understand.Topic) && !s.knownToModel(ctx, c, res):
		outcome = "refused"
	}
	s.recordModelCall(ctx, c, "understand", "understand", model, resp, outcome, errText)
	if err != nil {
		if !errors.Is(err, understand.ErrUnusable) && !errors.Is(err, ai.ErrNotConfigured) {
			s.Logger.Warn("chat: asking the model about a message", "err", err)
		}
		return false, nil
	}
	switch res.Kind {
	case understand.Action, understand.Topic:
		if outcome == "refused" {
			return false, nil
		}
		return true, s.modelPicked(ctx, c, res)
	case understand.Ask:
		// The answer comes next, read with this message.
		if err := s.saveSession(ctx, c, stateCollecting, chatData{Understand: &chatUnderstand{Said: text}}, waPendingTTL); err != nil {
			return true, err
		}
		return true, c.say(ctx, s, whatsapp.SafeText(res.Reply))
	case understand.Cannot:
		if c.cards() {
			if err := c.say(ctx, s, whatsapp.SafeText(res.Reply)); err != nil {
				return true, err
			}
			return true, s.sayMenu(ctx, c, chatMenuMsg{Line: c.t("menu.here_help")})
		}
		return true, c.say(ctx, s, whatsapp.SafeText(res.Reply)+"\n\n"+c.t("wa.not_understood_help"))
	}
	if understoodRefused(c, res.Command) {
		return false, nil
	}
	if understoodAtOnce(res.Command) {
		return true, s.waUnderstood(ctx, c, res.Command)
	}
	if err := s.saveSession(ctx, c, stateConfirming, chatData{Understand: &chatUnderstand{Command: res.Command}, Nonce: uuid.NewString()}, waPendingTTL); err != nil {
		return true, err
	}
	return true, c.say(ctx, s, c.t("wa.understood.confirm", "command", whatsapp.SafeText(res.Command)), understoodButtons(c)...)
}

func understoodButtons(c *chat) []whatsapp.Button {
	return []whatsapp.Button{{ID: "yes", Title: c.t("button.yes")}, {ID: "no", Title: c.t("button.no")}}
}

// waUnderstood reads a command from the model as if typed, once: what it
// does not recognise either gets the short answer, not the model again.
func (s *Server) waUnderstood(ctx context.Context, c *chat, command string) error {
	c.in.Text, c.in.Reply, c.in.Quoted = command, "", ""
	c.understood = true
	return s.waCommand(ctx, c)
}

// waUnderstandReply answers what the model's help left waiting: Yes or No
// to a command, or the answer to its question. Any other message lets the
// command go and is read as a new one.
func (s *Server) waUnderstandReply(ctx context.Context, c *chat, text, cmd string) error {
	u := *c.data.Understand
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	if u.Said != "" {
		if done, err := s.waUnderstand(ctx, c, u.Said+"\n"+text); done || err != nil {
			return err
		}
		return c.say(ctx, s, notUnderstood(c))
	}
	switch cmd {
	case "yes", "y", "confirm":
		return s.waUnderstood(ctx, c, u.Command)
	case "no", "n":
		return c.say(ctx, s, c.t("wa.understood.left"))
	}
	return s.waCommand(ctx, c)
}

// modelCatalogue is what the model is told the person can do (spec §8.1):
// their own catalogue, each action with its id, what it does, the values
// it takes and its command as an example; then the other commands, as the
// help gives them.
func (s *Server) modelCatalogue(ctx context.Context, c *chat) string {
	var b strings.Builder
	for _, t := range s.topicsFor(ctx, c) {
		fmt.Fprintf(&b, "Topic %s: %s\n", t.ID, t.Name)
		for _, a := range s.actionsOf(ctx, c, t.ID) {
			fmt.Fprintf(&b, "- %s: %s (%s).", a.ID, a.Title, a.Line)
			if len(a.Fields) > 0 {
				var fs []string
				for _, f := range a.Fields {
					x := f.Key + " (" + f.Kind
					if len(f.Choices) > 0 {
						x += ": " + strings.Join(f.Choices, ", ")
					}
					if f.Optional {
						x += ", may be left out"
					}
					fs = append(fs, x+")")
				}
				b.WriteString(" Values: " + strings.Join(fs, ", ") + ".")
			}
			if a.Shortcut != "" {
				b.WriteString(" Example: " + a.Shortcut)
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\nOther commands:\n" + s.waHelp(c))
	// The person's saved things, by name and kind only (saved objects).
	if line := s.savedNamesLine(ctx, c); line != "" {
		b.WriteString("\n" + line + "\n")
	}
	return b.String()
}

// knownToModel reports whether the model's action or topic is the
// person's own to use.
func (s *Server) knownToModel(ctx context.Context, c *chat, res understand.Result) bool {
	if res.Kind == understand.Topic {
		_, ok := chatTopicByID(res.ID)
		return ok && len(s.actionsOf(ctx, c, res.ID)) > 0
	}
	a, ok := chatActionByID(res.ID)
	return ok && s.mayAct(c, s.chatAreas(ctx, c), a)
}

// modelPicked does what the model picked (spec §8.1): a topic's menu, with
// the action it leans to first; or the action, at once with every value it
// needs, else asking for the rest with those given taken.
func (s *Server) modelPicked(ctx context.Context, c *chat, res understand.Result) error {
	if res.Kind == understand.Topic {
		t, _ := chatTopicByID(res.ID)
		m := chatMenuMsg{Topic: t.ID, Line: c.t("menu.about", "topic", word(c.lang.Tag, topicWordID(t), t.Name))}
		if p, ok := chatActionByID(res.Pick); ok && p.Topic == t.ID && s.mayAct(c, s.chatAreas(ctx, c), p) {
			m.Pick, m.Tip = p.ID, p.Shortcut
		}
		return s.sayMenu(ctx, c, m)
	}
	a, _ := chatActionByID(res.ID)
	values := takenValues(a, res.Values)
	s.actionUsed(ctx, c, a.ID)
	if len(a.Fields) == 0 {
		if a.Opens == opensShow || a.Opens == opensQuestions {
			return s.stepsDone(ctx, c, a, &chatSteps{Action: a.ID, Values: map[string]string{}})
		}
		// A form or a page: its row, picked, in its topic's menu here; its
		// link elsewhere.
		if c.cards() {
			return s.sayMenu(ctx, c, chatMenuMsg{Topic: a.Topic, Pick: a.ID, Tip: a.Shortcut})
		}
		return s.menuAct(ctx, c, a.ID)
	}
	missing := false
	for _, f := range a.Fields {
		if _, ok := values[f.Key]; !ok && !f.Optional {
			missing = true
		}
	}
	if !missing {
		for _, f := range a.Fields {
			if _, ok := values[f.Key]; !ok {
				values[f.Key] = ""
			}
		}
		return s.stepsDone(ctx, c, a, &chatSteps{Action: a.ID, Values: values})
	}
	// In the app and on the web: the action's row, picked and filled in,
	// opens its form there (M4).
	if c.cards() {
		return s.sayMenu(ctx, c, chatMenuMsg{Topic: a.Topic, Pick: a.ID, Values: values, Tip: a.Shortcut,
			Line: c.t("menu.about", "topic", word(c.lang.Tag, topicWordID(mustTopic(a.Topic)), mustTopic(a.Topic).Name))})
	}
	st := &chatSteps{Action: a.ID, Values: values}
	before := ""
	if len(values) > 0 {
		var given []string
		for _, f := range a.Fields {
			if v := values[f.Key]; v != "" {
				given = append(given, whatsapp.SafeText(v))
			}
		}
		before = c.t("menu.taken", "title", word(c.lang.Tag, titleWordID(a), a.Title), "values", strings.Join(given, ", "))
	}
	return s.stepsNext(ctx, c, a, st, before)
}

// takenValues are the model's values an action can take: its own fields',
// each as its question would take it (a choice one of its choices; a
// phone, an amount or an order's number of the right shape). Any other is
// left to be asked.
func takenValues(a chatAction, given map[string]string) map[string]string {
	out := map[string]string{}
	for _, f := range a.Fields {
		v, ok := given[f.Key]
		if !ok || strings.TrimSpace(v) == "" {
			continue
		}
		v = strings.TrimSpace(v)
		// An order or a booking chosen as "TK-A1B2C3 · Ada": its number.
		if ref, _, ok := strings.Cut(v, refSep); ok && (f.Kind == "order" || f.Kind == "booking") {
			v = strings.TrimSpace(ref)
		}
		if f.Kind == "choice" {
			match := ""
			for _, ch := range f.Choices {
				if strings.EqualFold(ch, v) {
					match = ch
				}
			}
			if match == "" {
				continue
			}
			v = match
		}
		if stepsCheck(f.Kind, v) != "" {
			continue
		}
		out[f.Key] = v
	}
	return out
}

func mustTopic(id string) chatTopic {
	t, _ := chatTopicByID(id)
	return t
}
