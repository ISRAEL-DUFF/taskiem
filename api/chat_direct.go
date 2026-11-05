package api

// A message straight to linked businesses from the Taskiem conversation
// (docs/spec/direct-messages.md): "..." as its first line, or before it on the
// same line, then the message with "@business" for who it is for:
//
//	...
//	@Dangote how much is a bag of cement?
//
// goes to Dangote's conversation as "how much is a bag of cement?", read by
// no command and no model. Without the marker every message is read as it
// always was. A lone "..." waits a few minutes for the message. The person
// needs chat.trade; the plan's messages per day and the audit in both
// businesses apply, as for "ask Dangote …" (sendLink).

import (
	"context"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// directWait is how long a lone "..." waits for the message.
const directWait = 5 * time.Minute

// cutDirect is the message after the "..." marker, and whether there is
// one: three or more full stops, or the one character "…" a phone's
// keyboard makes of them, at the start, followed by a space, a new line
// or nothing ("...and" is no marker).
func cutDirect(text string) (string, bool) {
	t := strings.TrimLeft(text, " \t\r\n")
	var after string
	switch {
	case strings.HasPrefix(t, "..."):
		after = strings.TrimLeft(t, ".")
	case strings.HasPrefix(t, "…"):
		after = strings.TrimPrefix(t, "…")
	default:
		return "", false
	}
	if r, _ := utf8.DecodeRuneInString(after); after != "" && !unicode.IsSpace(r) {
		return "", false
	}
	return strings.TrimSpace(after), true
}

// directBody is what the businesses receive: the mentions at the start
// left out ("@Dangote how much…" is "how much…"), a mention further in
// kept as typed without its "@" ("Hi @Dangote, …" is "Hi Dangote, …").
func directBody(text string, parts []mentionPart) string {
	r := []rune(text)
	skip := func(i int) int {
		for i < len(r) && (unicode.IsSpace(r[i]) || r[i] == ',' || r[i] == ':') {
			i++
		}
		return i
	}
	at, k := skip(0), 0
	for k < len(parts) && parts[k].At == at {
		at = skip(parts[k].End)
		k++
	}
	var b strings.Builder
	for _, p := range parts[k:] {
		b.WriteString(string(r[at:p.At]))
		b.WriteString(p.Typed)
		at = p.End
	}
	b.WriteString(string(r[at:]))
	return strings.TrimSpace(b.String())
}

// waDirect sends a message with the marker to the businesses it mentions
// (done false when the message has no marker and none was waited for).
// Buttons are never one.
func (s *Server) waDirect(ctx context.Context, c *chat) (bool, error) {
	if c.in.Reply != "" {
		return false, nil
	}
	text, marked := cutDirect(c.in.Text)
	if !marked {
		if c.data.Direct == nil {
			return false, nil
		}
		// After a lone "...": this message, if it says who it is for.
		waited := time.Since(*c.data.Direct) <= directWait
		if err := s.endDirectWait(ctx, c); err != nil {
			return true, err
		}
		if !waited || !strings.Contains(c.in.Text, "@") {
			return false, nil
		}
		text = strings.TrimSpace(c.in.Text)
	}
	if !c.p.Can(PermChatTrade) {
		return true, c.say(ctx, s, c.t("chat.link.denied", "org", c.tenant.Name))
	}
	if text == "" {
		now := time.Now().UTC()
		d := c.data
		d.Direct = &now
		// Whatever Taskiem was asking is still asked; on its own, the
		// wait ends by itself.
		state, ttl := c.state, waPendingTTL
		if state == stateIdle {
			state, ttl = stateCollecting, directWait
		}
		if err := s.saveSession(ctx, c, state, d, ttl); err != nil {
			return true, err
		}
		return true, c.say(ctx, s, c.t("chat.direct.wait"))
	}
	var list []links.Link
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		list, _, err = links.ListTx(ctx, tx)
		return err
	}); err != nil {
		return true, err
	}
	mo := parseMentions(text, list)
	// Nothing goes to anyone unless every mention names one business.
	if len(mo.Unknown) > 0 {
		return true, c.say(ctx, s, c.t("wa.mention.unlinked", "name", whatsapp.SafeText(mo.Unknown[0])))
	}
	if len(mo.Parts) == 0 {
		return true, c.say(ctx, s, c.t("chat.direct.who"))
	}
	var to []links.Link
	seen := map[uuid.UUID]bool{}
	for _, p := range mo.Parts {
		if len(p.Links) > 1 {
			return true, c.say(ctx, s, c.t("wa.mention.which", "name", whatsapp.SafeText(p.Typed), "list", mentionChoices(c, p.Links)))
		}
		if l := p.Links[0]; !seen[l.ID] {
			seen[l.ID] = true
			to = append(to, l)
		}
	}
	body := directBody(text, mo.Parts)
	if body == "" {
		return true, c.say(ctx, s, c.t("chat.direct.empty", "name", whatsapp.SafeText(to[0].Name)))
	}
	// The same words to each, as its own message in its own conversation.
	var sent, lines []string
	for _, l := range to {
		if _, err := s.sendLink(ctx, c.p, l.ID, body, links.SendOpts{}); err != nil {
			if !errIsAny(err, links.ErrInvalid, links.ErrState, links.ErrLimit) {
				return true, err
			}
			reason := err.Error()
			for _, e := range []error{links.ErrInvalid, links.ErrState, links.ErrLimit} {
				reason = strings.TrimPrefix(reason, e.Error()+": ")
			}
			lines = append(lines, c.t("chat.direct.not_sent", "name", whatsapp.SafeText(l.Name), "reason", whatsapp.SafeText(reason)))
			continue
		}
		sent = append(sent, "*"+whatsapp.SafeText(l.Name)+"*")
	}
	if len(sent) > 0 {
		lines = append([]string{c.t("chat.direct.sent", "names", strings.Join(sent, ", "))}, lines...)
	}
	return true, c.say(ctx, s, strings.Join(lines, "\n"))
}

// endDirectWait forgets a lone "..."'s wait, keeping whatever else the
// conversation was in the middle of.
func (s *Server) endDirectWait(ctx context.Context, c *chat) error {
	d := c.data
	d.Direct = nil
	if c.state == stateCollecting && reflect.DeepEqual(d, chatData{}) {
		return s.saveSession(ctx, c, stateIdle, chatData{}, 0)
	}
	return s.saveSession(ctx, c, c.state, d, waPendingTTL)
}
