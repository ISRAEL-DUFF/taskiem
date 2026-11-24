package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/wdcheck"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// A question inside a run (made tools M4, §9): a form step `to:
// "starter"` asks the person who started the run, in the chat they started
// it from. A question of one choice (an enum, or a saved place: `x-saved:
// "place"` offers the business's saved places among the choices) is asked
// there with its choices as buttons (three or fewer) or as words to reply
// with; pressing one, or replying with one of them exactly, answers it.
// Anything else is answered in the app or on the web, as every form step
// is. A reply that is not one of the choices is read as an ordinary
// message, so a waiting question never takes a command.

// choiceReplyPrefix is a choice button's payload: "fq:<request>:<index>".
const choiceReplyPrefix = "fq:"

// chatChoice is a question of one choice field: its name and its choices.
type chatChoice struct {
	Field   string
	Choices []string
}

// choiceOf reads a question that is one choice: a single field, a string
// with an enum, or x-saved: place (the business's saved places, after the
// enum's). False for anything else.
func (s *Server) choiceOf(ctx context.Context, tx pgx.Tx, schema map[string]any) (chatChoice, bool, error) {
	props, _ := schema["properties"].(map[string]any)
	if len(props) != 1 {
		return chatChoice{}, false, nil
	}
	for name, raw := range props {
		p, _ := raw.(map[string]any)
		if typ, _ := p["type"].(string); typ != "string" {
			return chatChoice{}, false, nil
		}
		var choices []string
		if enum, ok := p["enum"].([]any); ok {
			for _, e := range enum {
				if str, ok := e.(string); ok && str != "" {
					choices = append(choices, str)
				}
			}
		}
		if kind, _ := p["x-saved"].(string); kind == "place" {
			rows, err := tx.Query(ctx, `SELECT title FROM saved_objects WHERE tenant_id IS NOT NULL AND kind = 'place' AND archived_at IS NULL ORDER BY title LIMIT 20`)
			if err != nil {
				return chatChoice{}, false, err
			}
			places, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				return chatChoice{}, false, err
			}
			for _, pl := range places {
				if !slices.Contains(choices, pl) {
					choices = append(choices, pl)
				}
			}
		}
		if len(choices) == 0 || len(choices) > 20 {
			return chatChoice{}, false, nil
		}
		return chatChoice{Field: name, Choices: choices}, true, nil
	}
	return chatChoice{}, false, nil
}

// askStarter asks a run's starter in the chat they started it from: true
// when it was said there (the rest of FormAsk is then not needed).
func (s *Server) askStarter(ctx context.Context, a forms.ChatAsk) bool {
	if !a.Starter || len(a.Users) != 1 {
		return false
	}
	channel := a.Channel
	switch channel {
	case "whatsapp", "telegram", "slack", "web":
	default:
		return false // started in the app, on the web's Run, by the API: asked there
	}
	var addr string
	var choice chatChoice
	var isChoice bool
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{a.Tenant}, func(tx pgx.Tx) error {
		addrs, err := chatAddrsTx(ctx, tx, channel, a.Users)
		if err != nil {
			return err
		}
		addr = addrs[a.Users[0]]
		choice, isChoice, err = s.choiceOf(ctx, tx, a.Schema)
		return err
	})
	if err != nil || addr == "" {
		return false
	}
	ch, err := s.chatChannelFor(ctx, channel, a.Tenant)
	if err != nil {
		return false
	}
	app := strings.TrimRight(s.formsAppURL(), "/") + "/forms/requests/" + a.Request.String()
	title := whatsapp.SafeText(a.Title)
	var body string
	var buttons []whatsapp.Button
	switch {
	case isChoice && len(choice.Choices) <= 3:
		body = "Choose one:"
		for _, c := range choice.Choices {
			label := c
			if r := []rune(label); len(r) > 20 {
				label = string(r[:20])
			}
			buttons = append(buttons, whatsapp.Button{ID: choiceReplyPrefix + a.Request.String() + ":" + choiceKey(c), Title: label})
		}
	case isChoice:
		lines := []string{"Reply with one of:"}
		for _, c := range choice.Choices {
			lines = append(lines, "• "+whatsapp.SafeText(c))
		}
		body = strings.Join(lines, "\n")
	default:
		body = formAskWords(a.Schema, app)
	}
	m := linkPhoneMessage(a.Tenant, ch.Prefix(""), title, body, buttons)
	if _, err := ch.Send(ctx, addr, m); err != nil {
		s.Logger.Warn("forms: asking a run's starter", "tenant", a.Tenant, "err", err)
		return false
	}
	if !isChoice {
		s.armCode(ctx, a, a.Users[0], channel)
	}
	return true
}

// formChoiceAnswer answers a waiting one-choice question from the chat: a
// choice's button, or a reply that is exactly one of its choices (the
// newest question first). False when the message answers none.
func (s *Server) formChoiceAnswer(ctx context.Context, c *chat, text string) (bool, error) {
	if s.Forms == nil || c.p == nil || c.p.UserID == uuid.Nil {
		return false, nil
	}
	var pressed uuid.UUID
	pick := ""
	if strings.HasPrefix(c.in.Reply, formButtonPrefix+"send:") {
		// A form step's Send as it is (forms_chat.go), not a choice.
		return false, nil
	}
	if rest, ok := strings.CutPrefix(c.in.Reply, choiceReplyPrefix); ok {
		id, key, _ := strings.Cut(rest, ":")
		var err error
		if pressed, err = uuid.Parse(id); err != nil || key == "" {
			return true, c.say(ctx, s, "That question is not waiting any more.")
		}
		pick = key
	} else if c.state != stateIdle || c.in.Reply != "" || !choiceWords(text) {
		return false, nil
	}
	qs, err := s.Forms.ChatQuestions(ctx, forms.ChatAsker{Tenant: c.tenant.ID, User: c.p.UserID, Roles: c.p.Roles})
	if err != nil {
		return false, err
	}
	// Newest first (ChatQuestions' order). A typed reply answers only a
	// question put to this person as choices in this very chat.
	for _, q := range qs {
		if pressed != uuid.Nil && q.Request != pressed {
			continue
		}
		if pressed == uuid.Nil && (!q.Starter || q.Channel != c.ch.Name()) {
			continue
		}
		var choice chatChoice
		var ok bool
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{q.Tenant}, func(tx pgx.Tx) error {
			var err error
			choice, ok, err = s.choiceOf(ctx, tx, q.Schema)
			return err
		}); err != nil {
			return false, err
		}
		if !ok {
			continue
		}
		answer := ""
		if pick != "" {
			// The choice the button showed, by what it said: a list that
			// changed meanwhile never answers another one.
			for _, ch := range choice.Choices {
				if choiceKey(ch) == pick {
					answer = ch
				}
			}
			if answer == "" {
				return true, c.say(ctx, s, "That choice is no longer offered: "+whatsapp.SafeText(q.Title)+" is waiting for one of the others.")
			}
		} else {
			for _, ch := range choice.Choices {
				if strings.EqualFold(strings.TrimSpace(text), ch) {
					answer = ch
				}
			}
		}
		if answer == "" {
			continue
		}
		// Slack's and the web's Taskiem chat are recorded as the app.
		channel := c.ch.Name()
		if channel != forms.AppWhatsApp && channel != forms.AppTelegram {
			channel = "app"
		}
		a := forms.ChatAnswer{Question: q, Channel: channel, User: c.p.UserID, Answers: map[string]any{choice.Field: answer}}
		if q.Through {
			a.Business = c.tenant.ID
		}
		status, note, err := s.Forms.ChatSubmit(ctx, a)
		if err != nil {
			return true, err
		}
		if status == "refused" {
			if note == forms.NoLongerTaking {
				return true, c.say(ctx, s, whatsapp.SafeText(q.Title)+" has been answered already.")
			}
			return true, c.say(ctx, s, "That could not be sent: "+note+".")
		}
		return true, c.say(ctx, s, fmt.Sprintf("%s: %s. Thank you.", whatsapp.SafeText(q.Title), whatsapp.SafeText(answer)))
	}
	if pressed != uuid.Nil {
		return true, c.say(ctx, s, "That question is not waiting any more.")
	}
	return false, nil
}

// choiceKey names a choice in a button's payload by what it says.
func choiceKey(choice string) string {
	h := sha256.Sum256([]byte(choice))
	return hex.EncodeToString(h[:6])
}

// choiceWords reports whether a typed message could be a choice: short,
// not a bare number (a menu's), and not one of Taskiem's own commands.
func choiceWords(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" || len(t) > 80 {
		return false
	}
	if _, err := strconv.Atoi(t); err == nil {
		return false
	}
	first, _, _ := strings.Cut(strings.ToLower(t), " ")
	return !wdcheck.ReservedCommand(first)
}
