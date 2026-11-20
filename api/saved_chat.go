package api

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/saved"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// @name in a message to Taskiem (saved objects S1, part 2; §6.3): on
// WhatsApp, Telegram and Slack there is no picker, so a message that is
// only a saved thing's name ("@mum", "@bank-details?", "show @warehouse")
// is matched here, in the person's own saved things and then their
// business's, and answered with its details: an account number masked, a
// text in full to copy. Two things of one name are both answered, each
// said whose. The web chat and the app send their words the same way. A
// name that is no saved thing passes on, so "@Dangote" still names a
// linked business. Nothing here goes to the AI.

var savedAsk = regexp.MustCompile(`(?i)^(?:show\s+(?:me\s+)?)?@([a-z0-9][a-z0-9_-]{0,31})\s*\??$`)

// savedNameOf is the saved thing's name a message asks for, or "".
func savedNameOf(text string) string {
	m := savedAsk.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return ""
	}
	n, err := saved.Name(m[1])
	if err != nil {
		return ""
	}
	return n
}

// waSavedName answers a message that is only @name of a saved thing;
// false when the message is not one, or names none.
func (s *Server) waSavedName(ctx context.Context, c *chat, text string) (bool, error) {
	name := savedNameOf(text)
	if name == "" || c.p == nil || c.p.UserID == uuid.Nil {
		return false, nil
	}
	type found struct {
		row  savedRow
		mine bool
	}
	var got []found
	look := func(o savedOwner) error {
		return s.savedTx(ctx, o, func(tx pgx.Tx) error {
			where, owner := ownerWhere(o)
			x, err := scanSaved(tx.QueryRow(ctx, `SELECT `+savedCols+` FROM saved_objects WHERE `+where+` AND name = $2`, owner, name))
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err == nil {
				got = append(got, found{x, o.mine()})
			}
			return err
		})
	}
	if err := look(savedOwner{person: c.p.UserID, by: c.p.UserID}); err != nil {
		return true, err
	}
	if c.tenant.ID != uuid.Nil {
		if err := look(savedOwner{tenant: c.tenant.ID, by: c.p.UserID}); err != nil {
			return true, err
		}
	}
	if len(got) == 0 {
		return false, nil
	}
	parts := make([]string, 0, len(got))
	for _, f := range got {
		block := s.savedLines(c, f.row)
		if len(got) > 1 {
			whose := c.t("wa.saved.yours")
			if !f.mine {
				whose = c.t("wa.saved.business", "org", c.tenant.Name)
			}
			block = whose + "\n" + block
		}
		parts = append(parts, block)
	}
	if len(got) > 1 {
		return true, c.say(ctx, s, c.t("wa.saved.two", "name", name)+"\n\n"+strings.Join(parts, "\n\n"))
	}
	return true, c.say(ctx, s, parts[0])
}

// savedLines is a saved thing in lines for a chat: its name and title,
// then what it holds (an account number only masked).
func (s *Server) savedLines(c *chat, x savedRow) string {
	str := func(k string) string { v, _ := x.Details[k].(string); return v }
	head := c.t("wa.saved.head", "name", x.Name, "title", whatsapp.SafeText(x.Title))
	var lines []string
	add := func(v string) {
		if v != "" {
			lines = append(lines, whatsapp.SafeText(v))
		}
	}
	switch x.Kind {
	case saved.KindBankAccount:
		add(c.t("wa.saved.bank", "bank", str("bank_name"), "number", "••"+str("last4")))
		if x.CheckState == saved.Checked && x.CheckedName != nil {
			add(c.t("wa.saved.bank_name", "name", *x.CheckedName))
		} else {
			add(c.t("wa.saved.not_checked"))
		}
	case saved.KindText:
		// The words in full, to copy or forward; as they were saved.
		return head + "\n\n" + str("body")
	case saved.KindProviderAccount:
		add(str("provider") + ": " + str("number"))
		add(str("name_on_it"))
	case saved.KindFile:
		// What it is; the file is opened from Saved, or sent from a chat.
		f, _ := saved.FileOf(x.Details)
		add(f.Name + " (" + f.Hint() + ")")
	case saved.KindCustom:
		// A made-up kind's fields, the sensitive ones masked.
		v := x.view(savedOwner{}, false).Values
		for _, f := range x.Fields {
			if val := v[f.Key]; val != "" {
				if !f.Sensitive {
					val = saved.ShownValue(f, val)
				}
				add(f.Label + ": " + val)
			}
		}
	default:
		for _, k := range []string{"line", "area", "landmark", "phone", "email", "address", "birthday"} {
			add(str(k))
		}
	}
	add(str("note"))
	if len(lines) == 0 {
		return head
	}
	return head + "\n" + strings.Join(lines, "\n")
}
