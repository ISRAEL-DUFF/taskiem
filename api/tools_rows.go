package api

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// A list answer's rows with their buttons in a chat (made tools §7, M2).
// When a tool's run started from a chat ends with a list whose rows have
// actions, the first ten rows' buttons are minted and sent with the list:
// on WhatsApp as a list message, whose row, chosen ("tr:<run>:<row>"),
// answers with that row's buttons as reply buttons; on Telegram and Slack
// as one message with the rows as lines and a button for each row's
// action. Choosing a row is the person the answer went to's alone, in its
// business, while its buttons last; a button pressed is then any press
// (pressButton: the business's members, the tool's roles, once, its seven
// days). More rows than ten: the rest are in the app, by the link.

// chatRows is how many of a list answer's rows a chat shows, each with its
// buttons (WhatsApp's list message holds ten).
const chatRows = 10

// rowReplyPrefix is a list message row's id: "tr:<run>:<row>".
const rowReplyPrefix = "tr:"

// rowsPressable reports whether any row a chat shows has a button.
func rowsPressable(a madeAnswer) bool {
	if a.Type != "list" {
		return false
	}
	for i, r := range a.Rows {
		if i == chatRows {
			break
		}
		for _, b := range r.Actions {
			if b.Token != "" {
				return true
			}
		}
	}
	return false
}

// answerListHead is a list answer's words over a WhatsApp list message:
// its title, and how many rows are only in the app.
func answerListHead(l lang.Tag, a madeAnswer) string {
	var lines []string
	if a.Title != "" {
		lines = append(lines, "*"+a.Title+"*")
	}
	if n := len(a.Rows) - chatRows; n > 0 {
		lines = append(lines, tr(l, "wa.tool.list_more", "count", strconv.Itoa(n)))
	}
	return strings.Join(lines, "\n")
}

// answerList is a list answer's first rows as WhatsApp's list message: a
// row's title and, as its description, its sub and state.
func answerList(l lang.Tag, run uuid.UUID, a madeAnswer) *whatsapp.List {
	list := &whatsapp.List{Button: tr(l, "menu.choose"), Sections: []whatsapp.ListSection{{}}}
	for i, r := range a.Rows {
		if i == chatRows {
			break
		}
		title := strings.TrimSpace(r.Title)
		if title == "" {
			title = strconv.Itoa(i + 1)
		}
		desc := r.Sub
		if r.State != nil && r.State.Text != "" {
			desc = strings.TrimPrefix(desc+" · "+r.State.Text, " · ")
		}
		list.Sections[0].Rows = append(list.Sections[0].Rows, whatsapp.ListRow{
			ID: fmt.Sprintf("%s%s:%d", rowReplyPrefix, run, i), Title: whatsapp.SafeText(title), Description: whatsapp.SafeText(desc)})
	}
	return list
}

// rowButtons are the first rows' buttons as one message's buttons, for a
// chat that shows each button on its own (Telegram, Slack): each named by
// its row and its label, cut short.
func rowButtons(a madeAnswer) []whatsapp.Button {
	var out []whatsapp.Button
	for i, r := range a.Rows {
		if i == chatRows {
			break
		}
		for _, b := range r.Actions {
			if b.Token == "" {
				continue
			}
			label := strings.TrimSpace(clip(strings.TrimSpace(b.Label), 20))
			if t := strings.TrimSpace(r.Title); t != "" {
				label = clip(t, 20) + " · " + label
			}
			out = append(out, whatsapp.Button{ID: buttonReplyPrefix + b.Token, Title: label})
		}
	}
	return out
}

// waToolRow is a list message's row chosen: that row's buttons, as reply
// buttons, for the person the answer went to, in this business, while
// they last.
func (s *Server) waToolRow(ctx context.Context, c *chat, ref string) error {
	runID, idx, _ := strings.Cut(ref, ":")
	run, err := uuid.Parse(runID)
	i, ierr := strconv.Atoi(idx)
	if err != nil || ierr != nil || i < 0 || i >= chatRows {
		return c.say(ctx, s, c.t("wa.tool.button_gone"))
	}
	if !c.p.Can(PermRunStart) {
		return c.say(ctx, s, c.t("wa.run.denied", "org", c.tenant.Name))
	}
	me := c.p.Actor()
	a, err := s.answerOf(ctx, c.tenant.ID, run, func(startedBy, _ string) bool { return startedBy != "" && startedBy == me }, chatRows)
	if errors.Is(err, pgx.ErrNoRows) {
		return c.say(ctx, s, c.t("wa.tool.row_not_yours"))
	}
	if err != nil {
		return err
	}
	if a.Type != "list" || i >= len(a.Rows) {
		return c.say(ctx, s, c.t("wa.tool.button_gone"))
	}
	r := a.Rows[i]
	bs := chatButtons(r.Actions)
	if len(bs) == 0 && len(r.Actions) > 0 {
		// Its buttons' seven days are over, or their tools are gone.
		return c.say(ctx, s, c.t("wa.tool.button_gone"))
	}
	text := "*" + whatsapp.SafeText(r.Title) + "*"
	if r.Sub != "" {
		text += " · " + whatsapp.SafeText(r.Sub)
	}
	if r.State != nil && r.State.Text != "" {
		text += "\n" + whatsapp.SafeText(r.State.Text)
	}
	return c.say(ctx, s, text, bs...)
}
