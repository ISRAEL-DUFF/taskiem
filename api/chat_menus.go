package api

// Menus in the chat (docs/spec/chat-menus.md): instead of words to learn,
// a topic's actions as rows to press, or the topics as tiles. In the
// Taskiem chat on the web and in the app a menu is a card (a message of
// kind "menu", its JSON as the text), drawn from the person's catalogue
// (GET /v1/chat/actions); elsewhere it is a numbered list, answered by a
// number, until WhatsApp's lists and Telegram's buttons come (M3).
//
// A row pressed comes back as "act:<id>": an action that shows something
// is read as its command typed; one that needs values asks for them one
// at a time (§7), makes its command from them and reads it as typed, with
// a Yes first when it changes something without asking by itself. Forms
// and pages open in the app and on the web; elsewhere they are links.
// Nothing is written by a menu or a question itself.

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Prefixes of what a menu's rows and a question's choices send back.
const (
	actPrefix   = "act:"
	topicPrefix = "topic:"
	ansPrefix   = "ans:"
)

// menuRows is how many of a topic's actions show in the chat (§6.1).
const menuRows = 5

// chatMenuMsg is a menu as a message (kind "menu"): a topic's ("" for
// the topics), what Taskiem understood, the row it picked with what it
// took from the message, and the picked row's command as a tip.
type chatMenuMsg struct {
	Topic  string            `json:"topic,omitempty"`
	Line   string            `json:"line,omitempty"`
	Pick   string            `json:"pick,omitempty"`
	Values map[string]string `json:"values,omitempty"`
	Tip    string            `json:"tip,omitempty"`
}

// chatMenuState is a numbered menu waiting for a number (the channels
// without cards).
type chatMenuState struct {
	Topic string   `json:"topic,omitempty"`
	Items []string `json:"items"` // action ids, or topic ids for the topics
}

// chatSteps is an action asking for its values (§7).
type chatSteps struct {
	Action string            `json:"action"`
	Values map[string]string `json:"values,omitempty"`
	// Offered are the choices of the question asked, for a number.
	Offered []string `json:"offered,omitempty"`
	Tries   int      `json:"tries,omitempty"`
}

// cards reports whether the conversation draws menus as cards: the
// Taskiem chat on the web and in the app.
func (c *chat) cards() bool { return c.ch != nil && c.ch.Name() == "web" }

// sayMenu shows a menu: a card in the Taskiem chat; elsewhere a numbered
// list, its numbers kept for the answer.
func (s *Server) sayMenu(ctx context.Context, c *chat, m chatMenuMsg) error {
	if c.cards() {
		if c.note != "" {
			note := c.note
			c.note = ""
			if err := s.waSay(ctx, c.ch, c.addr, c.tenant.Name, note); err != nil {
				return err
			}
		}
		b, _ := json.Marshal(m)
		id := s.keepHistory(ctx, c.tenant.ID, c.p.UserID, "web", false, "menu", string(b), nil)
		if live := liveOf(ctx); live != nil && live.user == c.p.UserID {
			if id == uuid.Nil {
				id = uuid.Must(uuid.NewV7())
			}
			live.add(TaskiemMessage{ID: id, Channel: "web", Kind: "menu", Text: string(b), CreatedAt: time.Now().UTC()})
		}
		return nil
	}
	var lines []string
	st := chatMenuState{Topic: m.Topic}
	if m.Topic == "" {
		lines = append(lines, "What would you like to do? Reply with a number:")
		for i, t := range s.topicsFor(ctx, c) {
			st.Items = append(st.Items, t.ID)
			lines = append(lines, strconv.Itoa(i+1)+". "+t.Name)
		}
	} else {
		t, _ := chatTopicByID(m.Topic)
		head := m.Line
		if head == "" {
			head = "What would you like to do?"
		}
		lines = append(lines, "*"+t.Name+"*: "+head+" Reply with a number:")
		for i, a := range s.orderedActions(ctx, c, m.Topic, m.Pick) {
			st.Items = append(st.Items, a.ID)
			lines = append(lines, strconv.Itoa(i+1)+". "+a.Title)
		}
	}
	if len(st.Items) == 0 {
		return c.say(ctx, s, notUnderstood(c))
	}
	if m.Tip != "" {
		lines = append(lines, "", "Next time you can type: *"+m.Tip+"*")
	}
	if err := s.saveSession(ctx, c, stateCollecting, chatData{Menu: &st}, waPendingTTL); err != nil {
		return err
	}
	return c.say(ctx, s, strings.Join(lines, "\n"))
}

// orderedActions are a topic's actions, the picked one first.
func (s *Server) orderedActions(ctx context.Context, c *chat, topic, pick string) []chatAction {
	acts := s.actionsOf(ctx, c, topic)
	for i, a := range acts {
		if a.ID == pick && i > 0 {
			acts = append([]chatAction{a}, append(acts[:i:i], acts[i+1:]...)...)
			break
		}
	}
	return acts
}

// topicMenu shows a topic's menu, or the topics when the person can use
// none of it.
func (s *Server) topicMenu(ctx context.Context, c *chat, topic string) error {
	if len(s.actionsOf(ctx, c, topic)) == 0 {
		return s.sayMenu(ctx, c, chatMenuMsg{})
	}
	return s.sayMenu(ctx, c, chatMenuMsg{Topic: topic})
}

// waMenuWords reads what belongs to menus: a row pressed, a topic chosen,
// a number for a numbered menu, an answer to a question, help in the
// Taskiem chat, and "help <topic>". False when it is none of these.
func (s *Server) waMenuWords(ctx context.Context, c *chat, text, cmd string) (bool, error) {
	if id, ok := strings.CutPrefix(c.in.Reply, actPrefix); ok {
		if c.state != stateIdle {
			if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
				return true, err
			}
		}
		return true, s.menuAct(ctx, c, id)
	}
	if id, ok := strings.CutPrefix(c.in.Reply, topicPrefix); ok {
		return true, s.topicMenu(ctx, c, id)
	}
	if c.data.Steps != nil && c.state != stateIdle {
		answer := text
		if v, ok := strings.CutPrefix(c.in.Reply, ansPrefix); ok {
			answer = v
		}
		return true, s.stepsAnswer(ctx, c, answer)
	}
	if c.data.Menu != nil && c.state != stateIdle {
		st := *c.data.Menu
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return true, err
		}
		n, err := strconv.Atoi(strings.Trim(cmd, " #."))
		if err != nil || n < 1 || n > len(st.Items) {
			return false, nil // not a number from it: read as a new message
		}
		if st.Topic == "" {
			return true, s.topicMenu(ctx, c, st.Items[n-1])
		}
		return true, s.menuAct(ctx, c, st.Items[n-1])
	}
	if c.state != stateIdle || c.understood {
		return false, nil
	}
	switch {
	case cmd == "help" && c.cards(), cmd == "menu", cmd == "topics":
		return true, s.sayMenu(ctx, c, chatMenuMsg{Line: "Pick a topic, or just tell me what you want in your own words."})
	case cmd == "help all":
		return true, c.say(ctx, s, s.waHelp(c))
	}
	if rest, ok := strings.CutPrefix(cmd, "help "); ok {
		if t, ok := chatTopicNamed(rest); ok {
			return true, s.topicMenu(ctx, c, t.ID)
		}
	}
	return false, nil
}

// menuTopicWord shows a topic's menu for its word alone ("sales",
// "staff") when nothing else read it (the words that are commands, such
// as "riders" or "stock", answer as they always have).
func (s *Server) menuTopicWord(ctx context.Context, c *chat, cmd string) (bool, error) {
	t, ok := chatTopicNamed(cmd)
	if !ok || c.state != stateIdle {
		return false, nil
	}
	return true, s.topicMenu(ctx, c, t.ID)
}

// menuAct does a row: as its command typed, its questions, its form or
// its page.
func (s *Server) menuAct(ctx context.Context, c *chat, id string) error {
	a, ok := chatActionByID(id)
	if !ok || !s.mayAct(c, s.chatAreas(ctx, c), a) {
		return c.say(ctx, s, "That isn't something you can do here. Send *help* to see what you can.")
	}
	switch a.Opens {
	case opensShow:
		if a.Shortcut == "" {
			break
		}
		return s.waUnderstood(ctx, c, a.Shortcut)
	case opensForm:
		if len(a.Fields) > 0 && !c.cards() {
			return s.stepsStart(ctx, c, a, nil)
		}
		if a.Tool != "" && s.PublicURL != "" {
			return c.say(ctx, s, a.Title+": "+s.toolLink(a.Tool, &toolRequest{Add: a.Add}))
		}
		return s.stepsStart(ctx, c, a, nil)
	case opensPage:
		if s.PublicURL != "" {
			return c.say(ctx, s, a.Title+": "+s.PublicURL+a.Page)
		}
	case opensQuestions:
		return s.stepsStart(ctx, c, a, nil)
	}
	return c.say(ctx, s, a.Title+": open it in Taskiem on the web.")
}

// --- step by step (§7) ---

// stepsStart begins asking for an action's values, those given taken.
func (s *Server) stepsStart(ctx context.Context, c *chat, a chatAction, given map[string]string) error {
	st := &chatSteps{Action: a.ID, Values: map[string]string{}}
	for k, v := range given {
		if strings.TrimSpace(v) != "" {
			st.Values[k] = strings.TrimSpace(v)
		}
	}
	return s.stepsNext(ctx, c, a, st, "")
}

// stepsNext asks the next question, or, with every value, does it.
func (s *Server) stepsNext(ctx context.Context, c *chat, a chatAction, st *chatSteps, before string) error {
	var next *chatField
	for i := range a.Fields {
		if _, ok := st.Values[a.Fields[i].Key]; !ok {
			next = &a.Fields[i]
			break
		}
	}
	if next == nil {
		return s.stepsDone(ctx, c, a, st)
	}
	ask := next.Ask
	for k, v := range st.Values {
		ask = strings.ReplaceAll(ask, "{"+k+"}", v)
	}
	if before != "" {
		ask = before + "\n" + ask
	}
	st.Offered = s.choicesFor(ctx, c, *next)
	if err := s.saveSession(ctx, c, stateCollecting, chatData{Steps: st}, waPendingTTL); err != nil {
		return err
	}
	if len(st.Offered) == 0 {
		return c.say(ctx, s, ask)
	}
	// The choices: buttons where there are few enough (three on
	// WhatsApp); else numbered. Anything else may be typed.
	limit := 5
	if c.ch.Name() == "whatsapp" {
		limit = 3
	}
	if len(st.Offered) <= limit {
		var bs []whatsapp.Button
		for _, o := range st.Offered {
			bs = append(bs, whatsapp.Button{ID: ansPrefix + o, Title: clipTitle(o)})
		}
		return c.say(ctx, s, ask, bs...)
	}
	lines := []string{ask}
	for i, o := range st.Offered {
		lines = append(lines, strconv.Itoa(i+1)+". "+o)
	}
	lines = append(lines, "Reply with a number, or type it.")
	return c.say(ctx, s, strings.Join(lines, "\n"))
}

func clipTitle(s string) string {
	if utf8.RuneCountInString(s) <= 20 {
		return s
	}
	return string([]rune(s)[:19]) + "…"
}

// Checks on an answer, by the kind of value.
var (
	moneyAnswer  = regexp.MustCompile(`(?i)^(?:₦|n|ngn)?\s*\d[\d,]*(?:\.\d{1,2})?k?$`)
	numberAnswer = regexp.MustCompile(`^\d{1,6}$`)
	signedAnswer = regexp.MustCompile(`^[+-]?\d{1,6}$`)
	refAnswer    = regexp.MustCompile(`(?i)^[a-z]{2}-[a-z0-9]{6}$`)
)

// stepsCheck says what is wrong with an answer, "" when it will do.
func stepsCheck(kind, v string) string {
	switch kind {
	case "phone":
		if n := len(regexp.MustCompile(`\D`).ReplaceAllString(v, "")); n < 10 || n > 15 {
			return "That doesn't look like a phone number. Try one like 0803 123 4567."
		}
	case "money":
		if !moneyAnswer.MatchString(strings.ReplaceAll(v, " ", "")) {
			return "Send an amount, like 12,000 or 12k."
		}
	case "number":
		if !numberAnswer.MatchString(v) {
			return "Send a number, like 30."
		}
	case "signed":
		if !signedAnswer.MatchString(strings.ReplaceAll(v, " ", "")) {
			return "Send a number, like -2 or 3."
		}
	case "ref":
		if !refAnswer.MatchString(v) {
			return "That isn't an order's number. It looks like TK-A1B2C3."
		}
	}
	if utf8.RuneCountInString(v) > 150 {
		return "That's too long. Say it in fewer words."
	}
	return ""
}

// stepsAnswer takes the answer to the question asked.
func (s *Server) stepsAnswer(ctx context.Context, c *chat, text string) error {
	st := c.data.Steps
	if st.Values == nil {
		st.Values = map[string]string{}
	}
	a, ok := chatActionByID(st.Action)
	if !ok {
		return s.saveSession(ctx, c, stateIdle, chatData{}, 0)
	}
	var fl chatField
	for _, x := range a.Fields {
		if _, done := st.Values[x.Key]; !done {
			fl = x
			break
		}
	}
	v := strings.TrimSpace(text)
	if n, err := strconv.Atoi(strings.Trim(v, " #.")); err == nil && n >= 1 && n <= len(st.Offered) && fl.Kind != "number" && fl.Kind != "signed" {
		v = st.Offered[n-1]
	}
	if strings.EqualFold(v, "skip") && fl.Optional {
		st.Values[fl.Key] = ""
		return s.stepsNext(ctx, c, a, st, "")
	}
	if fl.Kind == "choice" && len(fl.Choices) > 0 {
		match := ""
		for _, ch := range fl.Choices {
			if strings.EqualFold(ch, v) {
				match = ch
			}
		}
		if match == "" {
			return s.stepsWrong(ctx, c, a, st, "Choose one of: "+strings.Join(fl.Choices, ", ")+".")
		}
		v = match
	}
	if why := stepsCheck(fl.Kind, v); why != "" {
		return s.stepsWrong(ctx, c, a, st, why)
	}
	if v == "" {
		return s.stepsWrong(ctx, c, a, st, "I need an answer to go on. Send *cancel* to stop.")
	}
	st.Values[fl.Key], st.Tries = v, 0
	return s.stepsNext(ctx, c, a, st, "")
}

// stepsWrong asks again with why, at most twice, then leaves it.
func (s *Server) stepsWrong(ctx context.Context, c *chat, a chatAction, st *chatSteps, why string) error {
	st.Tries++
	if st.Tries > 2 {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		if err := c.say(ctx, s, "Let's leave it for now."); err != nil {
			return err
		}
		return s.topicMenu(ctx, c, a.Topic)
	}
	return s.stepsNext(ctx, c, a, st, why)
}

// stepsDone makes the command and reads it as typed: at once when it
// only shows something or asks by itself; else shown first, for a Yes.
func (s *Server) stepsDone(ctx context.Context, c *chat, a chatAction, st *chatSteps) error {
	cmd := a.Shortcut
	if a.Make != nil {
		cmd = a.Make(st.Values)
	}
	if a.Does != doesChanges {
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return s.waUnderstood(ctx, c, cmd)
	}
	if err := s.saveSession(ctx, c, stateConfirming, chatData{Understand: &chatUnderstand{Command: cmd}, Nonce: uuid.NewString()}, waPendingTTL); err != nil {
		return err
	}
	// Shown with a phone's last four digits, so it can be checked.
	shown := cmd
	if a.Make != nil {
		v := map[string]string{}
		for k, x := range st.Values {
			v[k] = x
		}
		for _, fl := range a.Fields {
			if fl.Kind == "phone" && v[fl.Key] != "" {
				v[fl.Key] = maskPhone(regexp.MustCompile(`\D`).ReplaceAllString(v[fl.Key], ""))
			}
		}
		shown = a.Make(v)
	}
	return c.say(ctx, s, a.Title+": *"+whatsapp.SafeText(shown)+"*\nShall I do it?", understoodButtons(c)...)
}
