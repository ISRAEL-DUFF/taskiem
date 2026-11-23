package api

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/saved"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Saved things from the Taskiem chat (saved objects S1, after its review):
// "share @our-account with Ada's Kitchen" shares a saved thing with a
// linked business or a customer, as the chats do (saved_share.go): the
// person is asked first, in the words the server asks before sharing what
// is sensitive, and the share is recorded once it is sent. "save this as
// @dangote", replying to a linked business's message told on the phone,
// saves what it says, as "save dangote: …" does (saved_words.go).

var (
	savedShareWords  = regexp.MustCompile(`(?is)^\s*share\s+@([a-z0-9][a-z0-9-]{0,31})\s+with\s+(.{2,100}?)\s*$`)
	savedQuotedWords = regexp.MustCompile(`(?i)^\s*save\s+(?:this\s+|it\s+)?as\s+@?([a-z0-9][a-z0-9_-]{0,31})\s*$`)
)

// chatShare is a share waiting for the person's yes.
type chatShare struct {
	ID    uuid.UUID `json:"id"`
	Owner string    `json:"owner"`
	Name  string    `json:"name"`
	// To: a linked business (link) or a customer (customer), by id, and
	// as the person named them.
	To     string `json:"to"`
	ToID   string `json:"to_id"`
	ToName string `json:"to_name"`
}

// waShareSaved starts sharing a saved thing; false when the words are not
// "share @name with someone".
func (s *Server) waShareSaved(ctx context.Context, c *chat, text string) (bool, error) {
	m := savedShareWords.FindStringSubmatch(text)
	if m == nil || c.p == nil || c.p.UserID == uuid.Nil || c.tenant.ID == uuid.Nil {
		return false, nil
	}
	name, whom := strings.ToLower(m[1]), strings.TrimSpace(strings.TrimRight(m[2], ".!?"))
	got, err := s.savedFind(ctx, c, name)
	if err != nil {
		return true, err
	}
	if len(got) == 0 {
		return true, c.say(ctx, s, c.t("wa.saved.share_none", "name", name))
	}
	one, ok, err := s.savedPickOne(ctx, c, chatSavedPick{Kind: "share", Text: text, Name: name}, got)
	if !ok || err != nil {
		return true, err
	}
	x, o := one.row, foundOwner(c, one)
	sh := &chatShare{ID: x.ID, Owner: o.label(), Name: x.Name}
	to, names, err := s.shareRecipient(ctx, c, whom)
	if err != nil {
		return true, err
	}
	switch {
	case len(names) > 1:
		return true, c.say(ctx, s, c.t("wa.saved.share_which", "names", strings.Join(names, ", ")))
	case to == nil:
		return true, c.say(ctx, s, c.t("wa.saved.share_nobody", "to", whom))
	}
	sh.To, sh.ToID, sh.ToName = to.To, to.ToID, to.ToName
	// Checked as the send will be (whose it is, who may share it), so the
	// question asked is the one that holds.
	card, _, err := s.savedCardOf(ctx, c.p, c.p.UserID, c.tenant.ID, savedShare{ID: sh.ID, Owner: sh.Owner, Confirm: true}, sh.ToName)
	if refusedWords(err) != "" {
		return true, c.say(ctx, s, c.t("wa.saved.share_not", "reason", refusedWords(err)))
	}
	if err != nil {
		return true, err
	}
	if err := s.saveSession(ctx, c, stateConfirming, chatData{Share: sh}, waPendingTTL); err != nil {
		return true, err
	}
	ask := c.t("wa.saved.share_plain", "name", sh.Name, "to", sh.ToName)
	if card.Sensitive {
		ask = c.t("wa.saved.share_ask", "question", shareQuestion(x, sh.ToName))
	}
	return true, c.say(ctx, s, ask, shareButtons(c)...)
}

// shareButtons: Share and Cancel.
func shareButtons(c *chat) []whatsapp.Button {
	return []whatsapp.Button{{ID: "yes", Title: c.t("button.share")}, {ID: "cancel", Title: c.t("button.cancel")}}
}

// shareRecipient is the linked business or customer whom names: one, or
// the names of all it fits when more than one (none when nobody).
func (s *Server) shareRecipient(ctx context.Context, c *chat, whom string) (*chatShare, []string, error) {
	var found []chatShare
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		if c.p.Can(PermChatTrade) {
			list, _, err := links.ListTx(ctx, tx)
			if err != nil {
				return err
			}
			if hit, used := links.Mentioned(list, whom); used > 0 {
				for _, l := range hit {
					found = append(found, chatShare{To: "link", ToID: l.ID.String(), ToName: l.Name})
				}
			}
		}
		if c.p.Can(PermOrders) {
			threads, _, err := s.customerThreadsTx(ctx, tx, c.tenant.ID, "", nil, 500)
			if err != nil {
				return err
			}
			want := strings.ToLower(whom)
			var exact, near []chatShare
			for _, v := range threads {
				n := strings.ToLower(strings.TrimSpace(v.Name))
				switch {
				case n == "":
				case n == want:
					exact = append(exact, chatShare{To: "customer", ToID: v.ID.String(), ToName: v.Name})
				case strings.Contains(n, want):
					near = append(near, chatShare{To: "customer", ToID: v.ID.String(), ToName: v.Name})
				}
			}
			if len(exact) > 0 {
				near = exact
			}
			found = append(found, near...)
		}
		return nil
	})
	if err != nil || len(found) == 0 {
		return nil, nil, err
	}
	if len(found) == 1 {
		return &found[0], nil, nil
	}
	names := make([]string, 0, len(found))
	for _, f := range found {
		names = append(names, f.ToName)
	}
	return nil, names, nil
}

// waShareReply answers Share or Cancel.
func (s *Server) waShareReply(ctx context.Context, c *chat, cmd string) error {
	sh := c.data.Share
	switch cmd {
	case "yes", "y", "share", "confirm":
	case "no", "n":
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return err
		}
		return c.say(ctx, s, c.t("wa.saved.share_left"))
	default:
		return c.say(ctx, s, c.t("wa.saved.share_reminder", "name", sh.Name), shareButtons(c)...)
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	// Made again now, as it is at the send (it may have changed).
	card, rec, err := s.savedCardOf(ctx, c.p, c.p.UserID, c.tenant.ID, savedShare{ID: sh.ID, Owner: sh.Owner, Confirm: true}, sh.ToName)
	if err == nil {
		err = s.sendSavedTo(ctx, c, sh, card)
	}
	if words := refusedWords(err); words != "" {
		return c.say(ctx, s, c.t("wa.saved.share_not", "reason", words))
	}
	if errors.Is(err, links.ErrInvalid) || errors.Is(err, links.ErrState) {
		return c.say(ctx, s, c.t("wa.saved.share_not", "reason", err.Error()))
	}
	if err != nil {
		return err
	}
	s.recordShareBy(ctx, savedWho{p: c.p, where: c.ch.Name()}, rec)
	return c.say(ctx, s, c.t("wa.saved.shared", "name", sh.Name, "to", sh.ToName))
}

// sendSavedTo sends card where sh says: a linked business's conversation,
// as a tag in the words; a customer's, as a card in the app or its lines on
// WhatsApp, as a reply from the page does.
func (s *Server) sendSavedTo(ctx context.Context, c *chat, sh *chatShare, card saved.Card) error {
	id, err := uuid.Parse(sh.ToID)
	if err != nil {
		return customerSendErr{400, "That one is gone."}
	}
	if sh.To == "link" {
		_, err := s.sendLink(ctx, c.p, id, "@"+card.Name, links.SendOpts{Mentions: []links.Mention{{Kind: "saved", Name: card.Name, Saved: &card}}})
		return err
	}
	inApp := false
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		_, _, err := s.customerLineIn(ctx, tx, c.tenant.ID, sh.ToID, lineApp)
		if se := (customerSendErr{}); errors.As(err, &se) {
			return nil
		}
		inApp = err == nil
		return err
	}); err != nil {
		return err
	}
	// Signed with the member's first name, as a reply from the page is.
	var sender string
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		var name string
		err := tx.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, c.p.UserID).Scan(&name)
		sender = firstWord(name)
		return err
	}); err != nil {
		return err
	}
	in := customerSend{SenderID: c.p.Actor(), SenderName: sender, Default: true}
	if inApp {
		in.Text, in.Card, in.Line, in.Default = card.Title, &customerCard{Type: "saved", Title: card.Title, Saved: &card}, lineApp, false
	} else {
		in.Text = card.Plain()
	}
	_, _, err = s.sendCustomer(ctx, c.tenant.ID, sh.ToID, in)
	return err
}

// waSaveQuoted saves what a linked business's message says, told on the
// phone and replied to with "save this as @name"; false when the words are
// not that.
func (s *Server) waSaveQuoted(ctx context.Context, c *chat, text string) (bool, error) {
	m := savedQuotedWords.FindStringSubmatch(text)
	if m == nil || c.p == nil || c.p.UserID == uuid.Nil {
		return false, nil
	}
	if c.in.Quoted == "" || c.tenant.ID == uuid.Nil {
		return true, c.say(ctx, s, c.t("wa.saved.save_quoted_none"))
	}
	var body string
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) error {
		link, pair, ok, err := links.SentTx(ctx, tx, c.ch.Name(), s.chatAddrHash(c.tenant.ID, c.ch.Name(), c.addr), c.in.Quoted)
		if err != nil || !ok {
			return err
		}
		msg, err := links.MessageTx(ctx, tx, c.tenant.ID, link, pair)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		body = strings.TrimSpace(msg.Body)
		return err
	})
	if err != nil {
		return true, err
	}
	if body == "" {
		return true, c.say(ctx, s, c.t("wa.saved.save_quoted_none"))
	}
	return s.waSaveWords(ctx, c, "save "+m[1]+": "+body)
}
