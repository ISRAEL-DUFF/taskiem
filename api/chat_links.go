package api

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Business links in chat (docs/spec/business-links.md §5.1): "links"
// lists them; "link Dangote", "who sells cement" and "find cement in
// Ikeja" (when no data table is named) answer a numbered list of
// businesses that chose to be found; the number picks one and three
// buttons say how we trade. "link 0803…" and "link <storefront address>"
// send blind, with the same answer whether or not a business is behind
// it. A request reaches the other business's owners and admins with
// Accept, Decline and Block, which work only for a person who may answer
// for that business's links.

// chatLink is a link being asked for: the businesses a search listed,
// then the one picked, or the blind address.
type chatLink struct {
	Found []chatLinkFound `json:"found,omitempty"`
	Pick  *chatLinkFound  `json:"pick,omitempty"`
	// Code is the link code of an offer's Link button.
	Code  string `json:"code,omitempty"`
	Phone string `json:"phone,omitempty"`
	Slug  string `json:"slug,omitempty"`
	Shown string `json:"shown,omitempty"`
}

type chatLinkFound struct {
	Business uuid.UUID `json:"business"`
	Name     string    `json:"name"`
	State    string    `json:"state"`
}

var (
	linkWordRe   = regexp.MustCompile(`(?i)^\s*link\s+(?:with\s+)?(.+?)\s*$`)
	whoSellsRe   = regexp.MustCompile(`(?i)^\s*who\s+(?:sells|sell|does|has|supplies)\s+(.+?)\s*\??\s*$`)
	findBizRe    = regexp.MustCompile(`(?i)^\s*find\s+(?:a\s+)?business(?:es)?\s+(?:for\s+|that\s+sells?\s+)?(.+?)\s*$`)
	linkSlugRe   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	linkRoleBtns = []string{links.RoleSupplier, links.RoleCustomer, links.RoleBoth}
)

// waLinkCommand acts on the link words; false when text is none of them.
func (s *Server) waLinkCommand(ctx context.Context, c *chat, text, cmd string) (bool, error) {
	switch {
	case cmd == "links" || cmd == "my links" || cmd == "linked businesses":
		return true, s.waLinks(ctx, c)
	case cmd == "find us" || cmd == "hide us":
		return true, s.waFindable(ctx, c, cmd == "find us")
	case whoSellsRe.MatchString(text):
		return true, s.waLinkFind(ctx, c, whoSellsRe.FindStringSubmatch(text)[1])
	case findBizRe.MatchString(text):
		return true, s.waLinkFind(ctx, c, findBizRe.FindStringSubmatch(text)[1])
	case linkWordRe.MatchString(text):
		return true, s.waLinkTo(ctx, c, linkWordRe.FindStringSubmatch(text)[1])
	case cmd == "link":
		return true, c.say(ctx, s, c.t("link.how"))
	}
	return false, nil
}

func (s *Server) chatLinks(ctx context.Context, c *chat) ([]links.Link, []links.BlindRequest, error) {
	var list []links.Link
	var sent []links.BlindRequest
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		list, sent, err = links.ListTx(ctx, tx)
		return err
	})
	return list, sent, err
}

// waLinks answers "links": requests waiting for our answer (each with its
// buttons, for those who may answer), then the linked businesses, then
// what we asked.
func (s *Server) waLinks(ctx context.Context, c *chat) error {
	list, sent, err := s.chatLinks(ctx, c)
	if err != nil {
		return err
	}
	if len(list) == 0 && len(sent) == 0 {
		return c.say(ctx, s, c.t("link.none"))
	}
	lines := []string{c.t("link.list_title")}
	var asked []links.Link
	for _, l := range list {
		name := whatsapp.SafeText(l.Name)
		switch l.State {
		case links.Active:
			lines = append(lines, c.t("link.list_active", "name", name, "role", linkRole(c.lang.Tag, l.Role)))
		case links.AskedUs:
			lines = append(lines, c.t("link.list_asked", "name", name))
			asked = append(asked, l)
		case links.Requested:
			lines = append(lines, c.t("link.list_requested", "name", name))
		case links.NotAccepted:
			lines = append(lines, c.t("link.list_not_accepted", "name", name))
		case links.Blocked:
			lines = append(lines, c.t("link.list_blocked", "name", name))
		case links.Ended:
			lines = append(lines, c.t("link.list_ended", "name", name))
		}
	}
	for _, b := range sent {
		lines = append(lines, c.t("link.list_sent", "shown", b.Shown))
	}
	if err := c.say(ctx, s, strings.Join(lines, "\n")); err != nil {
		return err
	}
	if !c.p.Can(PermLinkManage) {
		return nil
	}
	for _, l := range asked {
		if err := c.say(ctx, s, c.t("link.asked_body", "name", whatsapp.SafeText(l.Name), "org", c.tenant.Name, "role", linkRole(c.lang.Tag, l.Role)),
			linkAnswerButtons(c.lang.Tag, l)...); err != nil {
			return err
		}
	}
	return nil
}

// waFindable answers "find us" and "hide us": whether other businesses
// find this one by search.
func (s *Server) waFindable(ctx context.Context, c *chat, on bool) error {
	if !c.p.Can(PermLinkManage) {
		return c.say(ctx, s, c.t("link.denied", "org", c.tenant.Name))
	}
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		if _, err := links.SetFindableTx(ctx, tx, c.tenant.ID, &on, "user:"+c.p.UserID.String()); err != nil {
			return err
		}
		return waAudit(ctx, tx, c.ch.Name(), c.p, "link.settings", "links", map[string]any{"findable": on})
	}); err != nil {
		return err
	}
	if on {
		return c.say(ctx, s, c.t("link.findable_on", "org", c.tenant.Name))
	}
	return c.say(ctx, s, c.t("link.findable_off", "org", c.tenant.Name))
}

// waLinkTo answers "link <who>": a phone number or a storefront address
// is asked blind; a name is searched.
func (s *Server) waLinkTo(ctx context.Context, c *chat, who string) error {
	if links.LooksLikePhone(who) {
		phone, err := whatsapp.Normalise(who, s.defaultCountry())
		if err != nil {
			return c.say(ctx, s, c.t("link.how"))
		}
		return s.waLinkAskRole(ctx, c, &chatLink{Phone: phone, Shown: links.Mask("phone", phone)}, c.t("link.which_role_blind", "shown", links.Mask("phone", phone)))
	}
	cards, err := s.chatLinkSearch(ctx, c, who)
	if err != nil {
		return err
	}
	// One word that names no business found may be a storefront's
	// address: asked blind, so whether it is one is never told.
	if slug := strings.ToLower(strings.Trim(who, "/ ")); len(cards) == 0 && linkSlugRe.MatchString(slug) && len(slug) >= 3 && len(slug) <= 30 {
		return s.waLinkAskRole(ctx, c, &chatLink{Slug: slug, Shown: slug}, c.t("link.which_role_blind", "shown", slug))
	}
	return s.waLinkFound(ctx, c, who, cards)
}

func (s *Server) chatLinkSearch(ctx context.Context, c *chat, q string) ([]links.Card, error) {
	var cards []links.Card
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		cards, err = links.SearchTx(ctx, tx, q, 9)
		return err
	})
	return cards, err
}

// waLinkFind answers "who sells cement" and "find business …".
func (s *Server) waLinkFind(ctx context.Context, c *chat, q string) error {
	cards, err := s.chatLinkSearch(ctx, c, q)
	if err != nil {
		return err
	}
	return s.waLinkFound(ctx, c, q, cards)
}

// waLinkFindSome answers "find <words>" that named no data table with the
// businesses found, if any; false when none was.
func (s *Server) waLinkFindSome(ctx context.Context, c *chat, q string) (bool, error) {
	cards, err := s.chatLinkSearch(ctx, c, q)
	if err != nil || len(cards) == 0 {
		return false, err
	}
	return true, s.waLinkFound(ctx, c, q, cards)
}

// waLinkFound lists businesses found, numbered, and keeps them for the
// number that picks one.
func (s *Server) waLinkFound(ctx context.Context, c *chat, q string, cards []links.Card) error {
	if len(cards) == 0 {
		return c.say(ctx, s, c.t("link.found_none", "query", fmt.Sprintf("%q", whatsapp.SafeText(q))))
	}
	lines := []string{c.t("link.found_title")}
	cl := &chatLink{}
	for i, card := range cards {
		line := strconv.Itoa(i+1) + ". *" + whatsapp.SafeText(card.Name) + "*"
		if card.Area != "" {
			line += " · " + whatsapp.SafeText(card.Area)
		}
		var items []string
		for _, it := range card.Items {
			x := whatsapp.SafeText(it.Name)
			if it.PriceKobo != nil {
				x += " " + storefront.FormatNaira(*it.PriceKobo)
			}
			items = append(items, x)
		}
		if len(items) > 0 {
			line += " · " + strings.Join(items, ", ")
		}
		switch card.State {
		case links.Active:
			line += " " + c.t("link.found_linked")
		case links.Requested, links.NotAccepted:
			line += " " + c.t("link.found_requested")
		case links.AskedUs:
			line += " " + c.t("link.found_asked_us")
		}
		lines = append(lines, line)
		cl.Found = append(cl.Found, chatLinkFound{Business: card.Business, Name: card.Name, State: card.State})
	}
	if !c.p.Can(PermLinkManage) {
		lines = append(lines, c.t("link.found_owner_only"))
		return c.say(ctx, s, strings.Join(lines, "\n"))
	}
	lines = append(lines, c.t("link.found_pick"))
	if err := s.saveSession(ctx, c, stateCollecting, chatData{Link: cl}, waPendingTTL); err != nil {
		return err
	}
	return c.say(ctx, s, strings.Join(lines, "\n"))
}

func (s *Server) waLinkAskRole(ctx context.Context, c *chat, cl *chatLink, question string) error {
	if !c.p.Can(PermLinkManage) {
		return c.say(ctx, s, c.t("link.denied", "org", c.tenant.Name))
	}
	if err := s.saveSession(ctx, c, stateConfirming, chatData{Link: cl}, waPendingTTL); err != nil {
		return err
	}
	return c.say(ctx, s, question, linkRoleButtons(c)...)
}

func linkRoleButtons(c *chat) []whatsapp.Button {
	out := make([]whatsapp.Button, 0, len(linkRoleBtns))
	for _, r := range linkRoleBtns {
		out = append(out, whatsapp.Button{ID: linkPrefix + "role:" + r, Title: c.t("link.button_role_" + r)})
	}
	return out
}

// waLinkReply takes the number that picks a business, then the role.
func (s *Server) waLinkReply(ctx context.Context, c *chat, text, cmd string) error {
	cl := c.data.Link
	if c.state == stateCollecting {
		n, err := strconv.Atoi(strings.TrimSpace(cmd))
		if err != nil || n < 1 || n > len(cl.Found) {
			return c.say(ctx, s, c.t("link.pick_number"))
		}
		pick := cl.Found[n-1]
		switch pick.State {
		case links.Active:
			return s.waLinkDone(ctx, c, c.t("link.already", "name", whatsapp.SafeText(pick.Name)))
		case links.Requested:
			return s.waLinkDone(ctx, c, c.t("link.waiting", "name", whatsapp.SafeText(pick.Name)))
		case links.AskedUs:
			if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
				return err
			}
			return s.waLinks(ctx, c)
		}
		return s.waLinkAskRole(ctx, c, &chatLink{Pick: &pick}, c.t("link.which_role", "name", whatsapp.SafeText(pick.Name)))
	}
	role, ok := strings.CutPrefix(text, linkPrefix+"role:")
	if !ok || !links.ValidRole(role) {
		name := cl.Shown
		if cl.Pick != nil {
			name = whatsapp.SafeText(cl.Pick.Name)
		}
		return c.say(ctx, s, c.t("link.which_role", "name", name), linkRoleButtons(c)...)
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	by := links.Actor{Type: "user", ID: c.p.UserID.String()}
	var told uuid.UUID
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		switch {
		case cl.Pick != nil:
			told, err = links.RequestTx(ctx, tx, cl.Pick.Business, role, by)
		case cl.Code != "":
			told, err = links.RequestByCodeTx(ctx, tx, cl.Code, role, by)
		default:
			told, err = links.RequestBlindTx(ctx, tx, c.tenant.ID, links.Blind{Phone: cl.Phone, Slug: cl.Slug, Shown: cl.Shown}, role, by, s.linkKey(c.tenant.ID))
		}
		return err
	})
	switch {
	case errors.Is(err, links.ErrTooMany):
		return c.say(ctx, s, c.t("link.too_many"))
	case errors.Is(err, links.ErrNotFound):
		return c.say(ctx, s, c.t("link.gone"))
	case err != nil:
		return err
	}
	s.tellLinkAsked(ctx, told, c.tenant.ID)
	switch {
	case cl.Pick != nil:
		return c.say(ctx, s, c.t("link.sent", "name", whatsapp.SafeText(cl.Pick.Name)))
	case cl.Code != "":
		return c.say(ctx, s, c.t("link.sent", "name", cl.Shown))
	}
	return c.say(ctx, s, c.t("link.blind_sent", "shown", cl.Shown))
}

func (s *Server) waLinkDone(ctx context.Context, c *chat, text string) error {
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return err
	}
	return c.say(ctx, s, text)
}

// waLinkOffer takes an offer's Link button: the business behind the code,
// then how we trade with it, then the request.
func (s *Server) waLinkOffer(ctx context.Context, c *chat, code string) error {
	if !c.p.Can(PermLinkManage) {
		return c.say(ctx, s, c.t("link.denied", "org", c.tenant.Name))
	}
	var card links.CodeCard
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		card, err = links.ByCodeTx(ctx, tx, code)
		return err
	})
	switch {
	case errors.Is(err, links.ErrNotFound):
		return c.say(ctx, s, c.t("link.gone"))
	case err != nil:
		return err
	}
	name := whatsapp.SafeText(card.Name)
	switch card.State {
	case links.Active:
		return c.say(ctx, s, c.t("link.already", "name", name))
	case links.Requested:
		return c.say(ctx, s, c.t("link.waiting", "name", name))
	case links.AskedUs:
		return s.waLinks(ctx, c)
	}
	return s.waLinkAskRole(ctx, c, &chatLink{Code: code, Shown: name}, c.t("link.which_role", "name", name))
}

// waLinkPress takes Accept, Decline or Block on a request: for whichever
// of the person's businesses the request was made to, if they may answer
// for its links there.
func (s *Server) waLinkPress(ctx context.Context, c *chat, reply string) error {
	answer, idText, _ := strings.Cut(strings.TrimPrefix(reply, linkPrefix), ":")
	id, err := uuid.Parse(idText)
	if err != nil || !slices.Contains([]string{links.Accept, links.Decline, links.Block}, answer) {
		return c.say(ctx, s, c.t("link.gone"))
	}
	tenants := append([]tenantRef{c.tenant}, slices.DeleteFunc(slices.Clone(c.tenants), func(t tenantRef) bool { return t.ID == c.tenant.ID })...)
	for _, t := range tenants {
		var found links.Link
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{t.ID}, func(tx pgx.Tx) error {
			var err error
			found, err = links.GetTx(ctx, tx, id)
			return err
		}); errors.Is(err, links.ErrNotFound) {
			continue
		} else if err != nil {
			return err
		}
		if found.State != links.AskedUs {
			return c.say(ctx, s, c.t("link.gone"))
		}
		p, err := s.principalOf(ctx, t.ID, c.p.UserID)
		if err != nil || !p.Can(PermLinkManage) {
			return c.say(ctx, s, c.t("link.denied", "org", t.Name))
		}
		var other uuid.UUID
		err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{t.ID}, func(tx pgx.Tx) error {
			var err error
			other, err = links.AnswerTx(ctx, tx, id, answer, links.Actor{Type: "user", ID: p.UserID.String()})
			return err
		})
		if errors.Is(err, links.ErrState) || errors.Is(err, links.ErrNotFound) {
			return c.say(ctx, s, c.t("link.gone"))
		}
		if err != nil {
			return err
		}
		s.tellLinkAnswered(ctx, other, id, answer)
		return c.say(ctx, s, c.t("link.done_"+answer, "name", whatsapp.SafeText(found.Name)))
	}
	return c.say(ctx, s, c.t("link.gone"))
}
