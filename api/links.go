package api

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/access"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/notices"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Business links, L0 (decision 0061; docs/spec/business-links.md §5):
// finding another business on Taskiem, asking it to link, and answering.
// Every read and write of a link goes through engine/links and the
// definers of migration 00275; the other side's people are told in their
// own business's scope, from its own view of the link.

// PermLinkManage requests, accepts, declines, blocks and ends links.
const PermLinkManage = access.LinkManage

func (s *Server) linkRoutes(r chi.Router) {
	manage := s.need(PermLinkManage)
	r.Get("/links", s.listLinks)
	r.Get("/links/search", s.searchLinks)
	r.With(manage, s.tenantWide).Post("/links", s.requestLink)
	r.With(manage, s.tenantWide).Post("/links/{id}/{answer}", s.answerLink)
	r.With(manage, s.tenantWide).Delete("/links/sent/{id}", s.cancelBlindLink)
	r.Get("/link-settings", s.getLinkSettings)
	r.With(manage, s.tenantWide).Put("/link-settings", s.putLinkSettings)
}

func linkErr(err error) error {
	switch {
	case errors.Is(err, links.ErrInvalid):
		return fmt.Errorf("%w: %s", errBadRequest, strings.TrimPrefix(err.Error(), "links: invalid: "))
	case errors.Is(err, links.ErrState):
		return fmt.Errorf("%w: the link cannot be answered that way now", errConflict)
	case errors.Is(err, links.ErrNotFound):
		return pgx.ErrNoRows
	case errors.Is(err, links.ErrTooMany):
		return fmt.Errorf("%w: that is enough link requests for today; try again tomorrow", errConflict)
	}
	return err
}

type linksOut struct {
	Links []links.Link         `json:"links"`
	Sent  []links.BlindRequest `json:"sent"`
}

func (s *Server) listLinks(w http.ResponseWriter, r *http.Request) {
	var out linksOut
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out.Links, out.Sent, err = links.ListTx(r.Context(), tx)
		return err
	}); err != nil {
		s.fail(w, r, linkErr(err))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// linkSearchOut answers a search: the businesses found, or, for a phone
// number or a storefront address, the one row that sends a request to it
// on a tap, which looks the same whether or not a business is behind it.
type linkSearchOut struct {
	Results []links.Card `json:"results"`
	Blind   *blindOffer  `json:"blind,omitempty"`
}

type blindOffer struct {
	Kind  string `json:"kind"`
	Shown string `json:"shown"`
}

func (s *Server) searchLinks(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	out := linkSearchOut{Results: []links.Card{}}
	if links.LooksLikePhone(q) {
		if phone, err := whatsapp.Normalise(q, s.defaultCountry()); err == nil {
			out.Blind = &blindOffer{Kind: "phone", Shown: links.Mask("phone", phone)}
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out.Results, err = links.SearchTx(r.Context(), tx, q, 20)
		return err
	}); err != nil {
		s.fail(w, r, linkErr(err))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// linkRequest asks a business to link: one a search listed (business), or
// blind, whoever uses a phone number or a storefront address.
type linkRequest struct {
	Business *uuid.UUID `json:"business"`
	Phone    string     `json:"phone"`
	Address  string     `json:"address"`
	// Role is how we trade with them: supplier (they supply us),
	// customer (they buy from us) or both.
	Role string `json:"role"`
}

type linkRequestOut struct {
	// Link is the request, for a business a search listed.
	Link *links.Link `json:"link,omitempty"`
	// Sent, for a request sent blind: the same answer whether or not a
	// business is behind the address.
	Sent    bool   `json:"sent"`
	Message string `json:"message"`
}

func (s *Server) requestLink(w http.ResponseWriter, r *http.Request) {
	var req linkRequest
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	by := links.Actor{Type: p.ActorType(), ID: p.Actor()}
	var blind *links.Blind
	switch {
	case req.Business != nil && (req.Phone != "" || req.Address != ""), req.Business == nil && req.Phone != "" && req.Address != "":
		s.fail(w, r, fmt.Errorf("%w: name one of business, phone or address", errBadRequest))
		return
	case req.Phone != "":
		phone, err := whatsapp.Normalise(req.Phone, s.defaultCountry())
		if err != nil {
			s.fail(w, r, fmt.Errorf("%w: %q is not a phone number", errBadRequest, req.Phone))
			return
		}
		blind = &links.Blind{Phone: phone}
	case req.Address != "":
		blind = &links.Blind{Slug: strings.Trim(strings.TrimSpace(req.Address), "/")}
	case req.Business == nil:
		s.fail(w, r, fmt.Errorf("%w: name a business, a phone number or a storefront address", errBadRequest))
		return
	}
	var told uuid.UUID
	var out linkRequestOut
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if blind != nil {
			told, err = links.RequestBlindTx(r.Context(), tx, p.TenantID, *blind, req.Role, by, s.linkKey(p.TenantID))
			return err
		}
		if told, err = links.RequestTx(r.Context(), tx, *req.Business, req.Role, by); err != nil {
			return err
		}
		list, _, err := links.ListTx(r.Context(), tx)
		if err != nil {
			return err
		}
		if i := slices.IndexFunc(list, func(l links.Link) bool { return l.Business == *req.Business }); i >= 0 {
			out.Link = &list[i]
		}
		return nil
	}); err != nil {
		s.fail(w, r, linkErr(err))
		return
	}
	s.tellLinkAsked(r.Context(), told, p.TenantID)
	if blind != nil {
		shown := blind.Slug
		if blind.Phone != "" {
			shown = links.Mask("phone", blind.Phone)
		}
		out.Sent, out.Message = true, tr(lang.EN, "link.blind_sent", "shown", shown)
		writeJSON(w, http.StatusAccepted, out)
		return
	}
	if out.Link == nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	switch out.Link.State {
	case links.Active:
		out.Message = tr(lang.EN, "link.already", "name", out.Link.Name)
	case links.AskedUs:
		out.Message = tr(lang.EN, "link.they_asked", "name", out.Link.Name)
	default:
		out.Sent, out.Message = true, tr(lang.EN, "link.sent", "name", out.Link.Name)
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) answerLink(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	answer := chi.URLParam(r, "answer")
	p := principalFrom(r.Context())
	var l links.Link
	var other uuid.UUID
	shown := true
	if err := s.tx(r, func(tx pgx.Tx) error {
		if other, err = links.AnswerTx(r.Context(), tx, id, answer, links.Actor{Type: p.ActorType(), ID: p.Actor()}); err != nil {
			return err
		}
		if l, err = links.GetTx(r.Context(), tx, id); errors.Is(err, links.ErrNotFound) {
			// Declined, cancelled or unblocked: no longer shown.
			shown, err = false, nil
		}
		return err
	}); err != nil {
		s.fail(w, r, linkErr(err))
		return
	}
	s.tellLinkAnswered(r.Context(), other, id, answer)
	if !shown {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) cancelBlindLink(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		if err := links.CancelBlindTx(r.Context(), tx, id); err != nil {
			return err
		}
		return auditTx(r, tx, "link.cancel", id.String(), map[string]any{"how": "blind"})
	}); err != nil {
		s.fail(w, r, linkErr(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getLinkSettings(w http.ResponseWriter, r *http.Request) {
	var st links.Settings
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		st, err = links.SettingsTx(r.Context(), tx, principalFrom(r.Context()).TenantID)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

type linkSettingsReq struct {
	// Findable: null goes back to the default (on with a published
	// storefront).
	Findable *bool `json:"findable"`
}

func (s *Server) putLinkSettings(w http.ResponseWriter, r *http.Request) {
	var req linkSettingsReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var st links.Settings
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if st, err = links.SetFindableTx(r.Context(), tx, p.TenantID, req.Findable, p.ActorType()+":"+p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "link.settings", "links", map[string]any{"findable": req.Findable})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// linkKey keys the hashes of the addresses a business sent blind
// requests to: the reach book's key when there is one, else one of the
// business's own (the hash only finds the same request twice).
func (s *Server) linkKey(tenant uuid.UUID) []byte {
	if s.Reach != nil && len(s.Reach.HashKey) == 32 {
		return s.Reach.HashKey
	}
	k := sha256.Sum256([]byte("taskiem/link-blind/v1\x00" + tenant.String()))
	return k[:]
}

// --- telling each side's people ---

// linkPrefix starts the buttons of a link request in chat: lk:<answer>:<link>.
const linkPrefix = "lk:"

// linkPeople are the members of the business in tx's scope who may answer
// for its links: the built-in roles holding link.manage.
func linkPeopleTx(ctx context.Context, tx pgx.Tx) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT user_id FROM memberships WHERE role = ANY ($1)`, access.Holding(PermLinkManage))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

type linkTold struct {
	channel, addr string
}

// tellLink tells the people of a business who answer for its links about
// one of them, in the app's bell and in chat, in that business's own
// scope and from its own view of the link: what it knows of the other side
// is what taskiem_links_of shows it. pick chooses the link; buttons are
// the chat buttons for it.
func (s *Server) tellLink(ctx context.Context, tenant uuid.UUID, pick func(links.Link) bool, text func(l lang.Tag, link links.Link, org string) (title, body string),
	buttons func(l lang.Tag, link links.Link) []whatsapp.Button) {
	var link links.Link
	var org string
	var to []linkTold
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		list, _, err := links.ListTx(ctx, tx)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(list, pick)
		if i < 0 {
			return errNoLinkToTell
		}
		link = list[i]
		if err := tx.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&org); err != nil {
			return err
		}
		users, err := linkPeopleTx(ctx, tx)
		if err != nil {
			return err
		}
		title, body := text(lang.EN, link, org)
		if err := notices.AddTx(ctx, tx, tenant, users, notices.Notice{Kind: notices.Setup, Title: title, Body: body, Link: "/links", Group: "link:" + link.ID.String()}); err != nil {
			return err
		}
		for _, channel := range []string{"whatsapp", "telegram", "slack"} {
			addrs, err := chatAddrsTx(ctx, tx, channel, users)
			if err != nil {
				return err
			}
			for _, u := range users {
				if a, ok := addrs[u]; ok && !slices.ContainsFunc(to, func(t linkTold) bool { return t.addr == a }) {
					to = append(to, linkTold{channel, a})
				}
			}
		}
		return nil
	})
	if errors.Is(err, errNoLinkToTell) {
		return
	}
	if err != nil {
		s.Logger.Warn("links: telling the business", "tenant", tenant, "err", err)
		return
	}
	for _, t := range to {
		ch, err := s.chatChannelFor(ctx, t.channel, tenant)
		if err != nil {
			continue
		}
		l := s.chooseLang(ctx, tenant, t.addr, "").Tag
		title, body := text(l, link, org)
		var bs []whatsapp.Button
		if buttons != nil {
			bs = buttons(l, link)
		}
		if err := s.waSay(ctx, ch, t.addr, "", strings.TrimSpace(title+"\n"+body), bs...); err != nil {
			s.Logger.Warn("links: a chat message", "tenant", tenant, "err", err)
		}
	}
}

var errNoLinkToTell = errors.New("links: nothing to tell")

func linkRole(l lang.Tag, role string) string { return tr(l, "link.role."+role) }

// tellLinkAsked tells a business that by asked it to link, with Accept,
// Decline and Block; off the request's path, so a request that reached a
// business takes as long to answer as one that did not.
func (s *Server) tellLinkAsked(ctx context.Context, tenant, by uuid.UUID) {
	if tenant == uuid.Nil {
		return
	}
	s.background(ctx, "link asked", func(ctx context.Context) {
		s.tellLink(ctx, tenant, func(l links.Link) bool { return l.Business == by && l.State == links.AskedUs }, func(l lang.Tag, link links.Link, org string) (string, string) {
			return tr(l, "link.asked_title", "name", link.Name), tr(l, "link.asked_body", "name", link.Name, "org", org, "role", linkRole(l, link.Role))
		}, linkAnswerButtons)
	})
}

func linkAnswerButtons(l lang.Tag, link links.Link) []whatsapp.Button {
	return []whatsapp.Button{
		{ID: linkPrefix + links.Accept + ":" + link.ID.String(), Title: tr(l, "link.button_accept")},
		{ID: linkPrefix + links.Decline + ":" + link.ID.String(), Title: tr(l, "link.button_decline")},
		{ID: linkPrefix + links.Block + ":" + link.ID.String(), Title: tr(l, "link.button_block")},
	}
}

// tellLinkAnswered tells the other side what it may know of an answer: an
// acceptance, or the end of a link. A decline, a block or a cancel is
// never told.
func (s *Server) tellLinkAnswered(ctx context.Context, other, id uuid.UUID, answer string) {
	if answer != links.Accept && answer != links.End {
		return
	}
	s.background(ctx, "link answered", func(ctx context.Context) {
		s.tellLink(ctx, other, func(l links.Link) bool { return l.ID == id }, func(l lang.Tag, link links.Link, org string) (string, string) {
			return tr(l, "link."+answer+"ed_title", "name", link.Name), tr(l, "link."+answer+"ed_body", "name", link.Name, "org", org)
		}, nil)
	})
}
