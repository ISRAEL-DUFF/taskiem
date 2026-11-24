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
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/saved"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Prefixes of what a menu's rows and a question's choices send back.
const (
	actPrefix   = "act:"
	formPrefix  = "form:"
	topicPrefix = "topic:"
	ansPrefix   = "ans:"
)

// refSep parts an order's number from its customer's name in a choice.
const refSep = " · "

// chatMenuMsg is a menu as a message (kind "menu"): a topic's ("" for
// the topics), what Taskiem understood, the row it picked with what it
// took from the message, and the picked row's command as a tip.
type chatMenuMsg struct {
	// from is where a list of the topics, or of a topic's rows, starts
	// (WhatsApp's second page of topics; Slack's next five).
	from int
	// all: every row at once, as Telegram's "All N" asks.
	all    bool
	Topic  string            `json:"topic,omitempty"`
	Line   string            `json:"line,omitempty"`
	Pick   string            `json:"pick,omitempty"`
	Values map[string]string `json:"values,omitempty"`
	Tip    string            `json:"tip,omitempty"`
}

// chatMenuState is a numbered menu waiting for a number (the channels
// without cards).
type chatMenuState struct {
	From  int      `json:"from,omitempty"`
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
	// Elsewhere: a list on WhatsApp, buttons on Telegram and Slack (M3);
	// a number from it is read too.
	type row struct{ id, item, title, line string }
	var rows []row
	head, section := m.Line, ""
	if m.Topic == "" {
		if head == "" {
			head = c.t("menu.what_do")
		}
		section = c.t("menu.topics")
		for _, t := range s.topicsFor(ctx, c)[min(m.from, len(s.topicsFor(ctx, c))):] {
			var first []string
			for _, a := range s.actionsOf(ctx, c, t.ID) {
				if len(first) < 3 {
					first = append(first, word(c.lang.Tag, titleWordID(a), a.Title))
				}
			}
			rows = append(rows, row{topicPrefix + t.ID, t.ID, word(c.lang.Tag, topicWordID(t), t.Name), strings.Join(first, ", ")})
		}
	} else {
		t, _ := chatTopicByID(m.Topic)
		section = word(c.lang.Tag, topicWordID(t), t.Name)
		if head == "" {
			head = c.t("menu.what_do")
		}
		head = "*" + section + "*: " + head
		acts := s.orderedActions(ctx, c, m.Topic, m.Pick)
		for _, a := range acts[min(m.from, len(acts)):] {
			rows = append(rows, row{actPrefix + a.ID, a.ID, word(c.lang.Tag, titleWordID(a), a.Title), word(c.lang.Tag, lineWordID(a), a.Line)})
		}
	}
	if len(rows) == 0 {
		return c.say(ctx, s, notUnderstood(c))
	}
	switch ch := c.ch.Name(); {
	// WhatsApp lists hold ten rows: more topics than that, nine and More.
	case ch == "whatsapp" && len(rows) > 10:
		rows = append(rows[:9:9], row{topicPrefix + topicsMore, topicsMore, c.t("menu.more"), c.t("menu.more_line")})
	// Slack: five rows at a time, then More… for the next five (§9).
	case ch == "slack" && len(rows) > menuPage:
		next := topicsMore + "@" + strconv.Itoa(m.from+menuPage) + pickSuffix(m)
		rows = append(rows[:menuPage:menuPage], row{topicPrefix + m.Topic + ":" + next, next, c.t("menu.more"), ""})
	// Telegram: a topic's five rows, then All N for every one (§9).
	case ch == "telegram" && m.Topic != "" && !m.all && len(rows) > menuPage:
		next := topicsMore + "@all" + pickSuffix(m)
		rows = append(rows[:menuPage:menuPage], row{topicPrefix + m.Topic + ":" + next, next, c.t("menu.all", "n", strconv.Itoa(len(rows))), ""})
	}
	st := chatMenuState{Topic: m.Topic, From: m.from}
	for _, r := range rows {
		st.Items = append(st.Items, r.item)
	}
	text := head
	if m.Tip != "" {
		text += "\n\n" + c.t("menu.tip", "command", m.Tip)
	}
	if err := s.saveSession(ctx, c, stateCollecting, chatData{Menu: &st}, waPendingTTL); err != nil {
		return err
	}
	if c.ch.Name() == "whatsapp" {
		l := &whatsapp.List{Button: c.t("menu.choose"), Sections: []whatsapp.ListSection{{Title: section}}}
		for _, r := range rows {
			l.Sections[0].Rows = append(l.Sections[0].Rows, whatsapp.ListRow{ID: r.id, Title: r.title, Description: r.line})
		}
		if c.note != "" {
			text, c.note = c.note+"\n\n"+text, ""
		}
		_, err := c.ch.Send(ctx, c.addr, whatsapp.Message{Text: c.ch.Prefix(c.tenant.Name) + text, List: l})
		return err
	}
	var bs []whatsapp.Button
	for _, r := range rows {
		bs = append(bs, whatsapp.Button{ID: r.id, Title: r.title})
	}
	if c.note != "" {
		text, c.note = c.note+"\n\n"+text, ""
	}
	msg := whatsapp.Message{Text: c.ch.Prefix(c.tenant.Name) + text, Buttons: bs}
	// Telegram lays the topics out two to a row (§9).
	if m.Topic == "" {
		msg.Columns = 2
	}
	_, err := c.ch.Send(ctx, c.addr, msg)
	return err
}

// topicsMore is the topics list's last row on WhatsApp, for the rest;
// with "@" and where to go on from ("more@5"), or "@all", the last row
// of a page on Slack and Telegram.
const topicsMore = "more"

// menuPage is how many rows a menu shows at once on Slack, and a topic's
// menu on Telegram, before More… or All N (§9).
const menuPage = 5

// menuMore shows the rest of a menu from its More… or All N: spec is
// "more" (WhatsApp's second list of topics, after from), "more@<from>" or
// "more@all".
func (s *Server) menuMore(ctx context.Context, c *chat, topic, spec string, from int) error {
	m := chatMenuMsg{Topic: topic}
	// The row picked first on the page before keeps its place, so the
	// next page neither skips a row nor shows it again.
	if rest, pick, ok := strings.Cut(spec, pickSep); ok && topic != "" {
		if a, found := chatActionByID(pick); found && a.Topic == topic {
			m.Pick = pick
		}
		spec = rest
	}
	switch rest, _ := strings.CutPrefix(spec, topicsMore+"@"); {
	case spec == topicsMore:
		m.from = from + 9
	case rest == "all":
		m.all = true
	default:
		n, err := strconv.Atoi(rest)
		if err != nil || n < 0 || n > 100 {
			return c.say(ctx, s, notUnderstood(c))
		}
		m.from = n
	}
	if topic == "" {
		m.Line = c.t("menu.more_line")
	} else if len(s.actionsOf(ctx, c, topic)) == 0 {
		return s.sayMenu(ctx, c, chatMenuMsg{})
	}
	return s.sayMenu(ctx, c, m)
}

// pickSep parts a More…'s place from the row picked first ("more@5~riders.add").
const pickSep = "~"

// pickSuffix is a topic's More… or All N's mark of its picked row.
func pickSuffix(m chatMenuMsg) string {
	if m.Topic == "" || m.Pick == "" {
		return ""
	}
	return pickSep + m.Pick
}

// isMoreItem reports whether a menu's item is its More… or All N.
func isMoreItem(it string) bool { return it == topicsMore || strings.HasPrefix(it, topicsMore+"@") }

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
	if rest, ok := strings.CutPrefix(c.in.Reply, formPrefix); ok {
		if c.state != stateIdle {
			if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
				return true, err
			}
		}
		return true, s.formDone(ctx, c, rest)
	}
	if id, ok := strings.CutPrefix(c.in.Reply, topicPrefix); ok {
		if c.state != stateIdle && c.data.Menu != nil {
			if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
				return true, err
			}
		}
		// More… and All N: "more", "more@5", or a topic's "riders:more@all".
		topic, spec, paged := strings.Cut(id, ":")
		if !paged {
			topic, spec = "", id
		}
		if isMoreItem(spec) {
			if _, ok := chatTopicByID(topic); topic != "" && !ok {
				return true, c.say(ctx, s, notUnderstood(c))
			}
			return true, s.menuMore(ctx, c, topic, spec, 0)
		}
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
		// A number from it picks; anything else is read as a new message.
		if n, err := strconv.Atoi(strings.Trim(cmd, " #.")); err == nil && n >= 1 && n <= len(st.Items) {
			if isMoreItem(st.Items[n-1]) {
				return true, s.menuMore(ctx, c, st.Topic, st.Items[n-1], st.From)
			}
			if st.Topic == "" {
				return true, s.topicMenu(ctx, c, st.Items[n-1])
			}
			return true, s.menuAct(ctx, c, st.Items[n-1])
		}
	}
	if c.state != stateIdle || c.understood {
		return false, nil
	}
	switch cmd {
	case "help", "menu", "topics":
		return true, s.sayMenu(ctx, c, chatMenuMsg{Line: c.t("menu.pick_topic")})
	case "help all":
		return true, c.say(ctx, s, s.waHelp(c))
	}
	// In the Taskiem chat, a topic's word that is a command ("riders",
	// "stock", "orders"): its answer as always, and the topic's menu under it.
	if t, ok := chatTopicNamed(cmd); ok && c.cards() {
		for _, a := range s.actionsOf(ctx, c, t.ID) {
			if a.Opens != opensShow || normalizeCommand(a.Shortcut) != cmd {
				continue
			}
			if err := s.waUnderstood(ctx, c, a.Shortcut); err != nil {
				return true, err
			}
			if c.state != stateIdle {
				return true, nil // the answer asks something: no menu over it
			}
			return true, s.sayMenu(ctx, c, chatMenuMsg{Topic: t.ID, Line: c.t("menu.anything_else")})
		}
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
		return c.say(ctx, s, c.t("menu.not_yours"))
	}
	s.actionUsed(ctx, c, a.ID)
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
			return c.say(ctx, s, word(c.lang.Tag, titleWordID(a), a.Title)+": "+s.toolLink(a.Tool, &toolRequest{Add: a.Add}))
		}
		return s.stepsStart(ctx, c, a, nil)
	case opensPage:
		if s.PublicURL != "" {
			return c.say(ctx, s, word(c.lang.Tag, titleWordID(a), a.Title)+": "+s.PublicURL+a.Page)
		}
	case opensQuestions:
		return s.stepsStart(ctx, c, a, nil)
	}
	return c.say(ctx, s, c.t("menu.open_web", "title", word(c.lang.Tag, titleWordID(a), a.Title)))
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
	ask := word(c.lang.Tag, askWordID(a, *next), next.Ask)
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
	// The choices: buttons; on WhatsApp, which shows three, a list beyond
	// that. Anything else may be typed, a number among them too.
	if c.ch.Name() == "whatsapp" && len(st.Offered) > 3 {
		l := &whatsapp.List{Button: c.t("menu.choose"), Sections: []whatsapp.ListSection{{}}}
		for _, o := range st.Offered {
			l.Sections[0].Rows = append(l.Sections[0].Rows, whatsapp.ListRow{ID: ansPrefix + o, Title: o})
		}
		if c.note != "" {
			ask, c.note = c.note+"\n\n"+ask, ""
		}
		_, err := c.ch.Send(ctx, c.addr, whatsapp.Message{Text: c.ch.Prefix(c.tenant.Name) + ask, List: l})
		return err
	}
	var bs []whatsapp.Button
	for _, o := range st.Offered {
		bs = append(bs, whatsapp.Button{ID: ansPrefix + o, Title: clipTitle(o)})
	}
	return c.say(ctx, s, ask, bs...)
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

// stepsCheck names what is wrong with an answer (a message's id), "" when
// it will do.
func stepsCheck(kind, v string) string {
	switch kind {
	case "phone":
		if n := len(regexp.MustCompile(`\D`).ReplaceAllString(v, "")); n < 10 || n > 15 {
			return "menu.check.phone"
		}
	case "money":
		if !moneyAnswer.MatchString(strings.ReplaceAll(v, " ", "")) {
			return "menu.check.money"
		}
	case "number":
		if !numberAnswer.MatchString(v) {
			return "menu.check.number"
		}
	case "signed":
		if !signedAnswer.MatchString(strings.ReplaceAll(v, " ", "")) {
			return "menu.check.signed"
		}
	case "order", "booking":
		if !refAnswer.MatchString(v) {
			return "menu.check.ref"
		}
	case "saved", "saved_place", "payee":
		if _, err := saved.Name(v); err != nil {
			return "menu.check.saved"
		}
	case "account":
		if _, err := saved.AccountNumber(v); err != nil {
			return "menu.check.account"
		}
	}
	if utf8.RuneCountInString(v) > 150 {
		return "menu.check.long"
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
	// An order or a booking offered as "TK-A1B2C3 · Ada": its number.
	if ref, _, ok := strings.Cut(v, refSep); ok && (fl.Kind == "order" || fl.Kind == "booking") {
		v = strings.TrimSpace(ref)
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
			return s.stepsWrong(ctx, c, a, st, c.t("menu.choose_one", "choices", strings.Join(fl.Choices, ", ")))
		}
		v = match
	}
	if why := stepsCheck(fl.Kind, v); why != "" {
		return s.stepsWrong(ctx, c, a, st, c.t(why))
	}
	if v == "" {
		return s.stepsWrong(ctx, c, a, st, c.t("menu.need_answer"))
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
		if err := c.say(ctx, s, c.t("menu.leave")); err != nil {
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
	return c.say(ctx, s, c.t("menu.shall", "title", word(c.lang.Tag, titleWordID(a), a.Title), "command", whatsapp.SafeText(shown)), understoodButtons(c)...)
}

// actionUsed counts an action as used by the person today (most used
// first, §6.1), and lets their days past thirty go. A failure is only
// logged: the menus' order is not worth an answer.
func (s *Server) actionUsed(ctx context.Context, c *chat, id string) {
	if c.p == nil || c.p.UserID == uuid.Nil || c.tenant.ID == uuid.Nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO chat_action_uses (tenant_id, user_id, action, day) VALUES ($1, $2, $3, current_date)
			ON CONFLICT (tenant_id, user_id, action, day) DO UPDATE SET uses = chat_action_uses.uses + 1`, c.tenant.ID, c.p.UserID, id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM chat_action_uses WHERE tenant_id = $1 AND user_id = $2 AND day < current_date - 30`, c.tenant.ID, c.p.UserID)
		return err
	})
	if err != nil {
		s.Logger.Warn("chat menus: counting a use", "err", err)
	}
}

// actionUses are the person's uses of each action in the last thirty
// days; nil when they cannot be read.
func (s *Server) actionUses(ctx context.Context, c *chat) map[string]int {
	if c.p == nil || c.p.UserID == uuid.Nil || c.tenant.ID == uuid.Nil {
		return nil
	}
	if c.uses != nil {
		return c.uses
	}
	out := map[string]int{}
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT action, sum(uses) FROM chat_action_uses WHERE tenant_id = $1 AND user_id = $2 AND day >= current_date - 30 GROUP BY action`,
			c.tenant.ID, c.p.UserID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a string
			var n int
			if err := rows.Scan(&a, &n); err != nil {
				return err
			}
			out[a] = n
		}
		return rows.Err()
	})
	if err != nil {
		s.Logger.Warn("chat menus: reading uses", "err", err)
		return nil
	}
	c.uses = out
	return out
}

// formDone does an action from its form in the app or on the web (M4):
// "form:<id>:<values as JSON>". The values are taken as the questions take
// them; with every one it needs, the command is made and read as typed,
// the form's Save being the person's yes; else what is missing is said.
func (s *Server) formDone(ctx context.Context, c *chat, rest string) error {
	id, raw, _ := strings.Cut(rest, ":")
	a, ok := chatActionByID(id)
	if !ok || !s.mayAct(c, s.chatAreas(ctx, c), a) {
		return c.say(ctx, s, c.t("menu.not_yours"))
	}
	var given map[string]string
	if err := json.Unmarshal([]byte(raw), &given); err != nil {
		return c.say(ctx, s, c.t("menu.need_answer"))
	}
	values := takenValues(a, given)
	var missing []string
	for _, f := range a.Fields {
		if _, ok := values[f.Key]; ok {
			continue
		}
		if f.Optional && strings.TrimSpace(given[f.Key]) == "" {
			values[f.Key] = ""
			continue
		}
		label, _ := askLabel(c.lang.Tag, a, f)
		missing = append(missing, label)
	}
	if len(missing) > 0 {
		return c.say(ctx, s, c.t("menu.form_missing", "fields", strings.Join(missing, " ")))
	}
	s.actionUsed(ctx, c, a.ID)
	cmd := a.Shortcut
	if a.Make != nil {
		cmd = a.Make(values)
	}
	return s.waUnderstood(ctx, c, cmd)
}

// menuDoneRe is how a menu ended, as kept: chose:<action id>, closed, or
// passed.
var menuDoneRe = regexp.MustCompile(`^(closed|passed|chose:[a-z]+\.[a-z_]+)$`)

// menuWithDone is a menu message's JSON with how it ended in it ("done":
// chose, closed or passed; "chose": the action's id), so it folds to a
// line saying so. Any other message is as it is.
func menuWithDone(kind, text string, done *string) string {
	if kind != "menu" || done == nil || !menuDoneRe.MatchString(*done) {
		return text
	}
	var m map[string]any
	if json.Unmarshal([]byte(text), &m) != nil || m == nil {
		return text
	}
	how, id, _ := strings.Cut(*done, ":")
	m["done"] = how
	if id != "" {
		m["chose"] = id
	}
	b, err := json.Marshal(m)
	if err != nil {
		return text
	}
	return string(b)
}

// settleMenus ends the person's open menus in the Taskiem chat once they
// say something (chat-menus.md: a menu folds when you type): a row pressed
// (act:, form:) is what the newest menu chose; every other open menu, and
// the newest when they said something else, passed.
func (s *Server) settleMenus(ctx context.Context, p *Principal, msg whatsapp.Inbound) {
	if p.UserID == uuid.Nil {
		return
	}
	chose := ""
	if id, ok := strings.CutPrefix(msg.Reply, "act:"); ok {
		chose = id
	} else if rest, ok := strings.CutPrefix(msg.Reply, "form:"); ok {
		chose, _, _ = strings.Cut(rest, ":")
	}
	if _, ok := chatActionByID(chose); !ok || !menuDoneRe.MatchString("chose:"+chose) {
		chose = ""
	}
	ctx = context.WithoutCancel(ctx)
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) error {
		if chose != "" {
			if _, err := tx.Exec(ctx, `UPDATE chat_history SET menu_done = $3 WHERE id = (
				SELECT id FROM chat_history WHERE tenant_id = $1 AND user_id = $2 AND kind = 'menu' AND menu_done IS NULL AND expires_at > now()
				ORDER BY created_at DESC, id DESC LIMIT 1)`, p.TenantID, p.UserID, "chose:"+chose); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `UPDATE chat_history SET menu_done = 'passed'
			WHERE tenant_id = $1 AND user_id = $2 AND kind = 'menu' AND menu_done IS NULL`, p.TenantID, p.UserID)
		return err
	})
	if err != nil {
		s.Logger.Warn("chat menus: not settled", "tenant", p.TenantID, "err", err)
	}
}

// closeMenu is POST /v1/taskiem-chat/menus/{id}/close: the menu's ✕, it
// folds to "closed, nothing done"; or, with {"chose": id}, a row that
// opened a tool or a page in the app (nothing sent to Taskiem). A menu that
// already ended stays as it ended.
func (s *Server) closeMenu(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil || p.UserID == uuid.Nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	// A row that opened a tool or a page here (no message sent): the menu
	// chose it.
	var in struct {
		Chose string `json:"chose"`
	}
	if r.ContentLength != 0 {
		if err := decodeBody(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	done := "closed"
	if in.Chose != "" {
		if _, ok := chatActionByID(in.Chose); !ok || !menuDoneRe.MatchString("chose:"+in.Chose) {
			s.fail(w, r, fmt.Errorf("%w: no such action", errBadRequest))
			return
		}
		done = "chose:" + in.Chose
	}
	var found bool
	if err := s.tx(r, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(), `SELECT true FROM chat_history WHERE id = $1 AND tenant_id = $2 AND user_id = $3 AND kind = 'menu' AND expires_at > now()`,
			id, p.TenantID, p.UserID).Scan(&found); err != nil {
			return err
		}
		_, err := tx.Exec(r.Context(), `UPDATE chat_history SET menu_done = $3 WHERE id = $1 AND tenant_id = $2 AND menu_done IS NULL`, id, p.TenantID, done)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
