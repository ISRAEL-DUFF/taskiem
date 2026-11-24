package api

// Chat tools (docs/spec/chat-tools.md): the new chat, built in phases
// behind one switch per business. Phase 1 is who a message goes to.
//
// A message's recipients decide who gets it: linked businesses it names,
// and "@Taskiem". Things it names (a product, an order) never change who
// gets it.
//
//   - WhatsApp, Telegram, Slack and the web's Taskiem conversation: a
//     message naming linked businesses goes to them, read by no command and
//     no model; one with "@Taskiem" is Taskiem's, as is one naming none.
//     The "..." marker still works, as before.
//   - A conversation with a business on the web: a message goes to that
//     business; other linked businesses it names get it too, each as its
//     own message in our conversation with it; one with "@Taskiem" goes to
//     the person's own Taskiem conversation, and to no business.
//
// While Taskiem is asking something (a pending question), a message is
// its answer, mentions or not.

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// chatToolsOn is whether a business has the chat tools switch on.
func chatToolsOn(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (bool, error) {
	var on bool
	err := tx.QueryRow(ctx, `SELECT chat_tools FROM tenants WHERE id = $1`, tenant).Scan(&on)
	return on, err
}

// getChatTools answers GET /v1/settings/chat-tools.
func (s *Server) getChatTools(w http.ResponseWriter, r *http.Request) {
	var on bool
	if err := s.tx(r, func(tx pgx.Tx) (err error) {
		on, err = chatToolsOn(r.Context(), tx, principalFrom(r.Context()).TenantID)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chat_tools": on})
}

// putChatTools answers PUT /v1/settings/chat-tools with {"chat_tools": true}.
func (s *Server) putChatTools(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On bool `json:"chat_tools"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `UPDATE tenants SET chat_tools = $2 WHERE id = $1`, p.TenantID, req.On); err != nil {
			return err
		}
		return auditTx(r, tx, "settings.chat_tools", p.TenantID.String(), map[string]any{"chat_tools": req.On})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chat_tools": req.On})
}

// taskiemTagRe is "@Taskiem" as a word, at the start or after a space or a
// mark, in any case.
var taskiemTagRe = regexp.MustCompile(`(?i)(^|[\s,(])@taskiem\b[,:]?`)

// cutTaskiem is a message without its "@Taskiem", and whether it had one;
// its lines are kept. A message that was only "@Taskiem" is "hi".
func cutTaskiem(text string) (string, bool) {
	if !taskiemTagRe.MatchString(text) {
		return text, false
	}
	lines := strings.Split(taskiemTagRe.ReplaceAllString(text, "$1"), "\n")
	for i := range lines {
		lines[i] = strings.Join(strings.Fields(lines[i]), " ")
	}
	out := strings.TrimSpace(strings.Join(lines, "\n"))
	if out == "" {
		out = "hi"
	}
	return out, true
}

// chatToolsRead reads the switch for a message on a chat channel and, when
// on, takes its "@Taskiem" off (c.toTaskiem says it had one). Buttons are
// never tagged.
func (s *Server) chatToolsRead(ctx context.Context, c *chat) error {
	if c.tenant.ID == uuid.Nil {
		return nil
	}
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) (err error) {
		c.tools, err = chatToolsOn(ctx, tx, c.tenant.ID)
		return err
	}); err != nil {
		return err
	}
	if c.tools && c.in.Reply == "" {
		c.in.Text, c.toTaskiem = cutTaskiem(c.in.Text)
	}
	return nil
}

// mentionedLinks are the linked businesses a message names, once each,
// in order: the recipients. Words after "@" that are no linked business
// (a product, an amount, a typo) are left as they are. which is a mention
// that could be two businesses (never picked in silence).
func (s *Server) mentionedLinks(ctx context.Context, c *chat, text string) (to []links.Link, parts []mentionPart, which *mentionPart, err error) {
	var list []links.Link
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		list, _, err = links.ListTx(ctx, tx)
		return err
	}); err != nil {
		return nil, nil, nil, err
	}
	mo := parseMentions(text, list)
	seen := map[uuid.UUID]bool{}
	for i, p := range mo.Parts {
		if len(p.Links) > 1 {
			return nil, nil, &mo.Parts[i], nil
		}
		if l := p.Links[0]; !seen[l.ID] {
			seen[l.ID] = true
			to = append(to, l)
		}
	}
	return to, mo.Parts, nil, nil
}

// toolsDirect sends a message naming linked businesses to them, with chat
// tools on (done false when it is Taskiem's: it names none, it called
// Taskiem, or Taskiem is asking something).
func (s *Server) toolsDirect(ctx context.Context, c *chat) (bool, error) {
	if !c.tools || c.toTaskiem || c.in.Reply != "" || c.state != stateIdle || !strings.Contains(c.in.Text, "@") {
		return false, nil
	}
	text := strings.TrimSpace(c.in.Text)
	to, parts, which, err := s.mentionedLinks(ctx, c, text)
	if err != nil {
		return true, err
	}
	if which != nil {
		return true, c.say(ctx, s, c.t("wa.mention.which", "name", whatsapp.SafeText(which.Typed), "list", mentionChoices(c, which.Links)))
	}
	if len(to) == 0 {
		return false, nil
	}
	if !c.p.Can(PermChatTrade) {
		return true, c.say(ctx, s, c.t("chat.link.denied", "org", c.tenant.Name))
	}
	body := directBody(text, parts)
	if body == "" {
		return true, c.say(ctx, s, c.t("chat.direct.empty", "name", whatsapp.SafeText(to[0].Name)))
	}
	return true, s.directSend(ctx, c, to, func(l links.Link) error {
		_, err := s.sendLink(ctx, c.p, l.ID, body, links.SendOpts{})
		return err
	})
}

// toolsFileTo is whether a photo or a file's caption names linked
// businesses, with chat tools on: then it goes to them (waDirectFile).
func (s *Server) toolsFileTo(ctx context.Context, c *chat, caption string) (bool, error) {
	if !c.tools || c.toTaskiem || c.state != stateIdle || !strings.Contains(caption, "@") {
		return false, nil
	}
	to, _, which, err := s.mentionedLinks(ctx, c, caption)
	return len(to) > 0 || which != nil, err
}
