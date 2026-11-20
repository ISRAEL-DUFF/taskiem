package api

import (
	"context"
	"strings"

	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// A numbered pick between two saved things of one name (saved objects S2,
// after its review): a command that acts on @name (invoice, deliver,
// remind, share) when one's own and the business's both have the name asks
// which, by number, and the command is read again with the answer. A
// question about @name answers both instead (sayFound); it acts on
// nothing.

// chatSavedPick is a command waiting for which @name: its kind and its
// words, read again once the person says 1 or 2.
type chatSavedPick struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
	Name string `json:"name"`
	// An invoice's amount and what it is for, read from other languages'
	// words before it waited.
	Kobo int64  `json:"kobo,omitempty"`
	What string `json:"what,omitempty"`
}

// savedPickOne is the one thing a command acts on among what @name named:
// the one there is, the one the person picked, or none while they are
// asked which (ok false: the question is said).
func (s *Server) savedPickOne(ctx context.Context, c *chat, pick chatSavedPick, got []savedFound) (savedFound, bool, error) {
	if len(got) == 1 {
		return got[0], true, nil
	}
	if c.savedChoice != "" {
		for _, f := range got {
			if f.mine == (c.savedChoice == "mine") {
				return f, true, nil
			}
		}
	}
	var mine, theirs string
	for _, f := range got {
		if f.mine {
			mine = f.row.Title
		} else {
			theirs = f.row.Title
		}
	}
	if err := s.saveSession(ctx, c, stateCollecting, chatData{SavedPick: &pick}, waPendingTTL); err != nil {
		return savedFound{}, false, err
	}
	return savedFound{}, false, c.say(ctx, s, c.t("wa.saved.pick", "name", pick.Name, "mine", whatsapp.SafeText(mine),
		"org", c.tenant.Name, "theirs", whatsapp.SafeText(theirs)))
}

// waSavedPickReply reads 1 or 2 (or "mine", "the business's") and does the
// waiting command with it; false when the reply is neither, which lets the
// command go and is read as a new message.
func (s *Server) waSavedPickReply(ctx context.Context, c *chat, cmd string) (bool, error) {
	pick := *c.data.SavedPick
	switch strings.Trim(cmd, " .)") {
	case "1", "mine", "my own", "yours", "own":
		c.savedChoice = "mine"
	case "2", "business", "the business", "the business's", "business's":
		c.savedChoice = "business"
	default:
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
	switch pick.Kind {
	case "invoice":
		return true, s.waInvoiceStart(ctx, c, "@"+pick.Name, pick.Kobo, pick.What, true)
	case "deliver":
		return s.waDeliverToSaved(ctx, c, pick.Text)
	case "remind":
		return s.waSavedReminders(ctx, c, pick.Text)
	case "share":
		return s.waShareSaved(ctx, c, pick.Text)
	}
	return false, nil
}
