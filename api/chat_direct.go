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
// businesses apply, as for "ask Dangote …" (sendLink). A photo or a file
// captioned so goes the same way (waDirectFile), each business keeping its
// own copy, as from the Chat page.

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/media"
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
	// "... sold 5 bags" names no one: read as it always was. Only a lone
	// "..." or one with an "@" is the marker's.
	if marked && text != "" && !strings.Contains(text, "@") {
		marked = false
	}
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
		state, ttl := c.state, chatStateTTL(c.state)
		if state == stateIdle {
			state, ttl = stateCollecting, directWait
		}
		if err := s.saveSession(ctx, c, state, d, ttl); err != nil {
			return true, err
		}
		return true, c.say(ctx, s, c.t("chat.direct.wait"))
	}
	to, body, err := s.directTo(ctx, c, text, false)
	if err != nil || to == nil {
		return true, err
	}
	return true, s.directSend(ctx, c, to, func(l links.Link) error {
		_, err := s.sendLink(ctx, c.p, l.ID, body, links.SendOpts{})
		return err
	})
}

// directTo is who a marked message is for (every business it mentions,
// once each) and what they receive; nil, with the reason already said,
// when it can go to no one: a mention not linked or that could be two
// businesses (then it goes to none of them), no mention, or nothing to say
// (a file may go with no words).
func (s *Server) directTo(ctx context.Context, c *chat, text string, file bool) ([]links.Link, string, error) {
	var list []links.Link
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		list, _, err = links.ListTx(ctx, tx)
		return err
	}); err != nil {
		return nil, "", err
	}
	mo := parseMentions(text, list)
	if len(mo.Unknown) > 0 {
		return nil, "", c.say(ctx, s, c.t("wa.mention.unlinked", "name", whatsapp.SafeText(mo.Unknown[0])))
	}
	if len(mo.Parts) == 0 {
		return nil, "", c.say(ctx, s, c.t("chat.direct.who"))
	}
	var to []links.Link
	seen := map[uuid.UUID]bool{}
	for _, p := range mo.Parts {
		if len(p.Links) > 1 {
			return nil, "", c.say(ctx, s, c.t("wa.mention.which", "name", whatsapp.SafeText(p.Typed), "list", mentionChoices(c, p.Links)))
		}
		if l := p.Links[0]; !seen[l.ID] {
			seen[l.ID] = true
			to = append(to, l)
		}
	}
	body := directBody(text, mo.Parts)
	if body == "" && !file {
		return nil, "", c.say(ctx, s, c.t("chat.direct.empty", "name", whatsapp.SafeText(to[0].Name)))
	}
	return to, body, nil
}

// directRefusals are the errors a send is refused with that the person is
// told plainly; anything else is a failure, logged.
var directRefusals = []error{links.ErrInvalid, links.ErrState, links.ErrLimit, links.ErrStorage,
	media.ErrThreat, media.ErrNoScanner, media.ErrFile, media.ErrImage}

// directReason is a refusal in words: its message without the sentinel's.
func directReason(err error) string {
	if errors.Is(err, media.ErrNoScanner) {
		return "files cannot be checked for malware here, so none can be sent"
	}
	reason := err.Error()
	for _, e := range directRefusals {
		reason = strings.TrimPrefix(reason, e.Error()+": ")
	}
	return strings.TrimPrefix(reason, "media: ")
}

// directSend sends to each business in turn, the same words (and file) to
// each as its own message, and says what happened (directReceipt). One
// failing does not stop the rest, and the receipt always names what
// went, so sending again to the rest never sends anything twice.
func (s *Server) directSend(ctx context.Context, c *chat, to []links.Link, send func(links.Link) error) error {
	rs := make([]directResult, len(to))
	for i, l := range to {
		rs[i] = directResult{name: l.Name, err: send(l)}
		if err := rs[i].err; err != nil && !errIsAny(err, directRefusals...) {
			s.Logger.Error("direct message: not sent", "tenant", c.tenant.ID, "link", l.ID, "err", err)
		}
	}
	return c.say(ctx, s, directReceipt(c.t, rs))
}

// directResult is how a send to one business went.
type directResult struct {
	name string
	err  error
}

// directReceipt is what the sender is told: the businesses the message
// went to, then each it did not, with why (a refusal in its own words, a
// failure as something to send again).
func directReceipt(t func(id string, kv ...string) string, rs []directResult) string {
	var sent, lines []string
	for _, r := range rs {
		name := whatsapp.SafeText(r.name)
		switch {
		case r.err == nil:
			sent = append(sent, "*"+name+"*")
		case errIsAny(r.err, directRefusals...):
			lines = append(lines, t("chat.direct.not_sent", "name", name, "reason", whatsapp.SafeText(directReason(r.err))))
		default:
			lines = append(lines, t("chat.direct.failed", "name", name))
		}
	}
	if len(sent) > 0 {
		lines = append([]string{t("chat.direct.sent", "names", strings.Join(sent, ", "))}, lines...)
	}
	return strings.Join(lines, "\n")
}

// waDirectFile sends a photo or a file captioned "... @business" (or sent
// with "@business" while a lone "..." waits) to the businesses its
// caption mentions, with the caption's words: fetched from the chat app,
// scanned for malware once, then for each business our copy and theirs,
// as the Chat page's upload makes them; a copy stored for a message that
// is not sent is removed.
func (s *Server) waDirectFile(ctx context.Context, c *chat, caption string) error {
	if !c.p.Can(PermChatTrade) {
		return c.say(ctx, s, c.t("chat.link.denied", "org", c.tenant.Name))
	}
	to, body, err := s.directTo(ctx, c, caption, true)
	if err != nil || to == nil {
		return err
	}
	if s.Media == nil {
		return c.say(ctx, s, c.t("chat.link.not_sent", "reason", "files cannot be stored here"))
	}
	ref, max, name := c.in.Image, int64(media.MaxImageBytes), ""
	if c.in.Document != nil {
		ref, max, name = c.in.Document, int64(media.MaxFileBytes), c.in.Document.Name
	}
	data, _, _, err := s.fetchMedia(ctx, c, ref, max)
	if errors.Is(err, whatsapp.ErrMediaTooLarge) {
		return c.say(ctx, s, c.t("chat.link.not_sent", "reason", fmt.Sprintf("the file is larger than %d MB", max>>20)))
	}
	if err != nil {
		return err
	}
	if err := s.Media.ScanChatFile(ctx, data); err != nil {
		if errIsAny(err, directRefusals...) {
			return c.say(ctx, s, c.t("chat.link.not_sent", "reason", whatsapp.SafeText(directReason(err))))
		}
		return err
	}
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	att := func(f media.File) *links.Attachment {
		return &links.Attachment{Kind: media.ChatKind(f.Type), Type: f.Type, Size: f.Size, Name: name, Path: f.Path}
	}
	return s.directSend(ctx, c, to, func(l links.Link) error {
		mine, err := s.Media.PutChatFile(ctx, c.tenant.ID, data)
		if err != nil {
			return err
		}
		var theirs *links.Attachment
		var other uuid.UUID
		theirCopy := func(ctx context.Context, business uuid.UUID) (*links.Attachment, error) {
			f, err := s.Media.PutChatFile(ctx, business, data)
			if err != nil {
				return nil, err
			}
			theirs, other = att(f), business
			return theirs, nil
		}
		if _, err := s.sendLink(ctx, c.p, l.ID, body, links.SendOpts{Ours: att(mine), TheirCopy: theirCopy}); err != nil {
			s.dropChatFile(ctx, c.tenant.ID, mine.Path)
			if theirs != nil {
				s.dropChatFile(ctx, other, theirs.Path)
			}
			return err
		}
		return nil
	})
}

// endDirectWait forgets a lone "..."'s wait, keeping whatever else the
// conversation was in the middle of.
func (s *Server) endDirectWait(ctx context.Context, c *chat) error {
	d := c.data
	d.Direct = nil
	if c.state == stateCollecting && reflect.DeepEqual(d, chatData{}) {
		return s.saveSession(ctx, c, stateIdle, chatData{}, 0)
	}
	return s.saveSession(ctx, c, c.state, d, chatStateTTL(c.state))
}

// chatStateTTL is how long a state is kept when it is saved again unchanged:
// a step-up link's ten minutes, else a pending command's.
func chatStateTTL(state string) time.Duration {
	if state == stateStepUp {
		return waHandoffTTL
	}
	return waPendingTTL
}
