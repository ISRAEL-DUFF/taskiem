package api

// Finding people (docs/spec/people-chatting.md §5, §8.4, §14; migration
// 00475; engine/people): a person's privacy settings towards other
// people, their contacts, looking a person up by number, their link (and
// its page /p/{code}), contact matching by keyed hashes, and last seen.
// Every route is for a person: their own session, or a business session
// acting for its person; API keys, field agents and embed users are
// refused (personOf). The chats between people are stream A's; they ask
// engine/people's PeoplePolicy.

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"html/template"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/chatlinks"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/people"
	"github.com/israel-duff/taskiem/engine/peoplechats"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// peopleRoutes are the person's routes (personalRoutes and
// peoplePersonRoutes list them).
func (s *Server) peopleRoutes(r chi.Router) {
	r.Get("/me/privacy/people", s.myPeoplePrivacy)
	r.Put("/me/privacy/people", s.putMyPeoplePrivacy)
	r.Get("/me/contacts", s.myContacts)
	r.Post("/me/contacts", s.addContact)
	r.Put("/me/contacts/{person}", s.renameContact)
	r.Delete("/me/contacts/{person}", s.removeContact)
	r.Get("/me/contacts/key", s.contactsKey)
	r.Post("/me/contacts/match", s.matchContacts)
	r.Get("/me/link", s.myPersonLink)
	r.Post("/me/link", s.newPersonLink)
	r.Get("/me/link/qr", s.myPersonLinkQR)
	r.Post("/me/presence", s.myPresence)
	r.Post("/people/lookup", s.lookupPerson)
	r.Get("/people/links/{code}", s.personByLink)
	r.Get("/people/{id}", s.getPerson)
	r.Get("/people/{id}/photo", s.personPhoto)
}

// peoplePublicRoutes are for anyone: a person's card at their link.
func (s *Server) peoplePublicRoutes(r chi.Router) {
	r.Get("/person-links/{code}", s.getPersonLinkCard)
	r.Get("/person-links/{code}/photo", s.personLinkPhoto)
}

// people is the server's engine/people; without one configured, one with
// no key (settings, contacts by chat and lookups work; links, matching
// and adding contacts do not).
func (s *Server) people() *people.People {
	if s.People != nil {
		return s.People
	}
	s.peopleOnce.Do(func() {
		s.peopleDefault = peoplechats.JoinPeople(&people.People{Pool: s.Store.Pool})
	})
	return s.peopleDefault
}

// peopleFail answers engine/people's refusals.
func (s *Server) peopleFail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, people.ErrBatch):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Send at most " + strconv.Itoa(people.MatchBatch) + " hashes, each 64 hex digits.", "max_batch": people.MatchBatch})
	case errors.Is(err, people.ErrInvalid):
		writeErr(w, http.StatusBadRequest, "That is not a setting or a name Taskiem can keep.")
	case errors.Is(err, people.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, people.ErrLimit):
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "You have looked up many people today. Try again tomorrow.", "limit": "day"})
	case errors.Is(err, people.ErrOldKey):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "The contacts key has changed. Fetch it again and hash your contacts with it.", "key_changed": true})
	case errors.Is(err, people.ErrOff):
		writeErr(w, http.StatusServiceUnavailable, "Finding people is not set up on this Taskiem.")
	default:
		s.personFail(w, r, err)
	}
}

// --- what a person sees of another ---

// personCardOut is a person as the viewer may see them.
type personCardOut struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	Deleted bool      `json:"deleted"`
	// Suspended: the account is suspended, not deleted: their name, and
	// nothing else of them; it can't get messages right now.
	Suspended bool `json:"suspended,omitempty"`
	// ContactName is the name the viewer gave them; InContacts, the viewer
	// added them; TheyAddedYou, they added the viewer.
	ContactName  *string `json:"contact_name"`
	InContacts   bool    `json:"in_contacts"`
	TheyAddedYou bool    `json:"they_added_you"`
	// Photo, LastSeenAt and Online: only where their settings let the
	// viewer see them.
	Photo      *photoRef  `json:"photo"`
	LastSeenAt *time.Time `json:"last_seen_at"`
	Online     *bool      `json:"online"`
	ReadTicks  bool       `json:"read_ticks"`
}

type photoRef struct {
	URL       string    `json:"url"`
	UpdatedAt time.Time `json:"updated_at"`
}

// deletedName is how a deleted account is shown (§14, §17 Q17).
const deletedName = "Deleted account"

func cardOut(c people.Card) personCardOut {
	out := personCardOut{ID: c.ID, Name: c.Name, Deleted: c.Deleted, ContactName: c.ContactName, InContacts: c.InContacts,
		TheyAddedYou: c.TheyAddedYou, LastSeenAt: c.LastSeenAt, Online: c.Online, ReadTicks: c.ReadTicks}
	switch {
	case c.Suspended:
		out.Deleted, out.Suspended = false, true
	case c.Deleted:
		out.Name = deletedName
	}
	if c.PhotoAt != nil {
		out.Photo = &photoRef{URL: "/v1/people/" + c.ID.String() + "/photo?v=" + strconv.FormatInt(c.PhotoAt.UnixMilli(), 10), UpdatedAt: *c.PhotoAt}
	}
	return out
}

// personParam reads a person's id from the path; 404 when it is not one.
func personParam(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return uuid.Nil, false
	}
	return id, true
}

// getPerson is GET /v1/people/{id}: a person as the viewer may see them
// (name, photo, last seen and online by their settings), and their number
// when they chose to show it to people they chat with or added.
func (s *Server) getPerson(w http.ResponseWriter, r *http.Request) {
	viewer, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	id, ok := personParam(w, r, "id")
	if !ok {
		return
	}
	if !s.limiter("people-view:"+viewer.String(), time.Second, 60).Allow() {
		writeErr(w, http.StatusTooManyRequests, "Too many at once. Wait a moment.")
		return
	}
	p := s.people()
	c, err := p.CardOf(r.Context(), viewer, id)
	if err != nil {
		s.peopleFail(w, r, err)
		return
	}
	number, err := p.NumberShownTo(r.Context(), id, viewer)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var n *string
	if number != "" {
		n = &number
	}
	writeJSON(w, http.StatusOK, struct {
		personCardOut
		Number *string `json:"number"`
	}{cardOut(c), n})
}

// personPhoto is GET /v1/people/{id}/photo: their photo when their photo
// setting lets the viewer see it; 404 otherwise.
func (s *Server) personPhoto(w http.ResponseWriter, r *http.Request) {
	viewer, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	id, ok := personParam(w, r, "id")
	if !ok {
		return
	}
	ctype, data, at, err := s.people().Photo(r.Context(), viewer, id)
	if err != nil {
		s.peopleFail(w, r, err)
		return
	}
	writePhoto(w, r, ctype, data, at, "private, max-age=300")
}

// writePhoto answers a photo as its stored type, never sniffed;
// http.ServeContent sets its length and Last-Modified, and answers a
// conditional request with 304.
func writePhoto(w http.ResponseWriter, r *http.Request, ctype string, data []byte, at time.Time, cache string) {
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", cache)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", at, bytes.NewReader(data))
}

// --- settings ---

// myPeoplePrivacy is GET /v1/me/privacy/people: who can find the person,
// message them, see their last seen and photo, read ticks, and whether
// their number is shown to people they chat with (§5.4).
func (s *Server) myPeoplePrivacy(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	st, err := s.people().SettingsOf(r.Context(), person)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// putMyPeoplePrivacy is PUT /v1/me/privacy/people: the settings given
// change; 400 for an audience that is not everyone, contacts or nobody.
func (s *Server) putMyPeoplePrivacy(w http.ResponseWriter, r *http.Request) {
	if !jsonOnly(w, r) {
		return
	}
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var in people.SettingsIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	st, err := s.people().SaveSettings(r.Context(), person, in)
	if err != nil {
		s.peopleFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// --- contacts ---

type contactOut struct {
	personCardOut
	Source  string    `json:"source"`
	AddedAt time.Time `json:"added_at"`
}

// myContacts is GET /v1/me/contacts: the people the person added, by the
// name shown, each as they may see them.
func (s *Server) myContacts(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	list, err := s.people().Contacts(r.Context(), person)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]contactOut, len(list))
	for i, c := range list {
		out[i] = contactOut{personCardOut: cardOut(c.Card), Source: c.Source, AddedAt: c.AddedAt}
	}
	writeJSON(w, http.StatusOK, map[string]any{"contacts": out})
}

// contactOf answers one contact of the person's list.
func (s *Server) contactOf(ctx context.Context, person, contact uuid.UUID) (contactOut, error) {
	list, err := s.people().Contacts(ctx, person)
	if err != nil {
		return contactOut{}, err
	}
	for _, c := range list {
		if c.ID == contact {
			return contactOut{personCardOut: cardOut(c.Card), Source: c.Source, AddedAt: c.AddedAt}, nil
		}
	}
	return contactOut{}, people.ErrNotFound
}

// addContact is POST /v1/me/contacts ({person_id, add_token, from, name?}):
// adds a person the viewer found, by number, link or contact matching
// (the add_token those answered, for a day). 403 {add_token: true} without
// a good token.
func (s *Server) addContact(w http.ResponseWriter, r *http.Request) {
	if !jsonOnly(w, r) {
		return
	}
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var req struct {
		PersonID uuid.UUID `json:"person_id"`
		AddToken string    `json:"add_token"`
		From     string    `json:"from"`
		Name     string    `json:"name"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.From != people.FromNumber && req.From != people.FromLink && req.From != people.FromContacts {
		writeErr(w, http.StatusBadRequest, "from is number, link or contacts")
		return
	}
	p := s.people()
	if !p.Keyed() {
		s.peopleFail(w, r, people.ErrOff)
		return
	}
	if !p.CheckAddToken(person, req.PersonID, req.AddToken) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Find this person again (by their number, their link or your contacts) to add them.", "add_token": true})
		return
	}
	if err := p.AddContact(r.Context(), person, req.PersonID, req.Name, req.From); err != nil {
		s.peopleFail(w, r, err)
		return
	}
	out, err := s.contactOf(r.Context(), person, req.PersonID)
	if err != nil {
		s.peopleFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// renameContact is PUT /v1/me/contacts/{person} ({name}): the name the
// person gave a contact ("" or null for none).
func (s *Server) renameContact(w http.ResponseWriter, r *http.Request) {
	if !jsonOnly(w, r) {
		return
	}
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	contact, ok := personParam(w, r, "person")
	if !ok {
		return
	}
	var req struct {
		Name *string `json:"name"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	name := ""
	if req.Name != nil {
		name = *req.Name
	}
	if err := s.people().RenameContact(r.Context(), person, contact, name); err != nil {
		s.peopleFail(w, r, err)
		return
	}
	out, err := s.contactOf(r.Context(), person, contact)
	if err != nil {
		s.peopleFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// removeContact is DELETE /v1/me/contacts/{person}.
func (s *Server) removeContact(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	contact, ok := personParam(w, r, "person")
	if !ok {
		return
	}
	if err := s.people().RemoveContact(r.Context(), person, contact); err != nil {
		s.peopleFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- finding by number (§5.1) ---

// inviteWords are what the app offers to send, from the person's own
// phone (SMS or WhatsApp), to someone not on Taskiem: Taskiem sends
// nothing.
func inviteWords(link, app string) string {
	w := "I'm on Taskiem, where we can chat and shop together."
	if link != "" {
		w += " Chat with me there: " + link
	}
	if app != "" {
		w += " Get the app: " + app
	}
	return w
}

type foundOut struct {
	Found    bool           `json:"found"`
	Self     bool           `json:"self,omitempty"`
	Person   *personCardOut `json:"person,omitempty"`
	AddToken string         `json:"add_token,omitempty"`
	Invite   *inviteOut     `json:"invite,omitempty"`
}

type inviteOut struct {
	Words string `json:"words"`
	Link  string `json:"link"`
}

// lookupPerson is POST /v1/people/lookup ({phone}): the person whose
// verified phone it is, when their "find me by my number" lets the viewer
// find them ({found: true, person, add_token}); else, the same for a
// number that is nobody's and one whose person hides from the viewer,
// {found: false, invite: {words, link}}: "Not on Taskiem yet" and the
// words to send from the phone. Fifty a day per person, and limits per
// address (429).
func (s *Server) lookupPerson(w http.ResponseWriter, r *http.Request) {
	if !jsonOnly(w, r) {
		return
	}
	viewer, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var req struct {
		Phone string `json:"phone"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	number, ok := phoneNumber(w, req.Phone)
	if !ok {
		return
	}
	ip := limitIP(r)
	if !s.limiter("people-lookup:"+viewer.String(), 3*time.Second, 10).Allow() || !s.limiter("people-lookup-ip:"+ip, 2*time.Second, 30).Allow() ||
		!s.limiter("people-lookup-ip-day:"+ip, 24*time.Hour/300, 300).Allow() {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "Too many lookups. Wait a while, then try again.", "limit": "rate"})
		return
	}
	p := s.people()
	f, err := p.FindByNumber(r.Context(), viewer, number)
	if errors.Is(err, people.ErrNotFound) {
		link := ""
		if p.Keyed() {
			if l, err := p.LinkOf(r.Context(), viewer); err == nil {
				link = l.URL
			} else {
				s.Logger.Warn("people: the person's link for an invite", "err", err)
			}
		}
		writeJSON(w, http.StatusOK, foundOut{Invite: &inviteOut{Words: inviteWords(link, s.AppGetURL), Link: link}})
		return
	}
	if err != nil {
		s.peopleFail(w, r, err)
		return
	}
	c := cardOut(f.Card)
	writeJSON(w, http.StatusOK, foundOut{Found: true, Self: f.Self, Person: &c, AddToken: f.AddToken})
}

// --- contact matching (§5.3) ---

// contactsWords are what the app says before asking for the phone's
// contacts, and the honest limit of the method (§5.3, §17 Q4).
const contactsWords = "Find friends from your contacts? Their numbers are checked without being sent to us in readable form: " +
	"your phone scrambles each number with a key from Taskiem that changes every week, sends only the scrambled numbers, " +
	"and Taskiem answers which of them are people whose settings let you find them this way. Nothing from your contacts is kept. " +
	"Taskiem holds the key, so this protects your contacts if what is sent is seen by someone else, not from Taskiem itself."

// contactsKey is GET /v1/me/contacts/key: the week's key the app hashes
// its contacts' numbers with (SHA-256 of the key's bytes, then
// "taskiem-contacts:" and the E.164 number; 64 hex digits each), when to
// fetch it again, how many hashes a request may carry, and the words.
func (s *Server) contactsKey(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	k, err := s.people().CurrentContactsKey()
	if err != nil {
		s.peopleFail(w, r, err)
		return
	}
	// Fewer a day in an account's first week (people.MatchHashesFirstWeek).
	perDay := people.MatchHashesPerDay
	if err := db.InPersonTx(r.Context(), s.Store.Pool, person, func(tx pgx.Tx) error {
		newWeek, err := personNewWeek(r.Context(), tx, person)
		if newWeek {
			perDay = people.MatchHashesFirstWeek
		}
		return err
	}); err != nil {
		s.personFail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, map[string]any{"key_id": k.ID, "key": base64.StdEncoding.EncodeToString(k.Key), "expires_at": k.ExpiresAt,
		"hash": "sha256(key || \"" + people.HashPrefix + "\" || E.164), hex", "max_batch": people.MatchBatch,
		"max_per_day": perDay, "words": contactsWords})
}

// matchContacts is POST /v1/me/contacts/match ({key_id, hashes}): which
// hashes are people who let themselves be found from contacts, each with
// what the asker may see and an add token. Nothing of the request is
// kept. 409 {key_changed} for a key no longer answered; 400 for more than
// max_batch or a hash that is not 64 hex digits; 429 past the day's
// limits or the address's.
func (s *Server) matchContacts(w http.ResponseWriter, r *http.Request) {
	if !jsonOnly(w, r) {
		return
	}
	viewer, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var req struct {
		KeyID  string   `json:"key_id"`
		Hashes []string `json:"hashes"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.limiter("people-match-ip:"+limitIP(r), 2*time.Minute, 30).Allow() {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "Too many at once. Try again later.", "limit": "rate"})
		return
	}
	ms, err := s.people().MatchContacts(r.Context(), viewer, req.KeyID, req.Hashes)
	if err != nil {
		s.peopleFail(w, r, err)
		return
	}
	type matchOut struct {
		Hash     string        `json:"hash"`
		Person   personCardOut `json:"person"`
		AddToken string        `json:"add_token"`
	}
	out := make([]matchOut, len(ms))
	for i, m := range ms {
		out[i] = matchOut{Hash: m.Hash, Person: cardOut(m.Card), AddToken: m.AddToken}
	}
	writeJSON(w, http.StatusOK, map[string]any{"matches": out})
}

// --- the person's link (§5.2) ---

type personLinkOut struct {
	people.Link
	Words string `json:"words"`
	QR    string `json:"qr"`
}

func (s *Server) personLinkView(l people.Link) personLinkOut {
	return personLinkOut{Link: l, Words: inviteWords(l.URL, ""), QR: "/v1/me/link/qr"}
}

// myPersonLink is GET /v1/me/link: the person's link (made the first time),
// its words to share and its QR code's address.
func (s *Server) myPersonLink(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	l, err := s.people().LinkOf(r.Context(), person)
	if err != nil {
		s.peopleFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.personLinkView(l))
}

// newPersonLink is POST /v1/me/link: "Make a new link"; the old one says
// "This link no longer works" for 30 days.
func (s *Server) newPersonLink(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	if !s.limiter("people-link-new:"+person.String(), time.Minute, 5).Allow() {
		writeErr(w, http.StatusTooManyRequests, "Too many new links. Wait a minute.")
		return
	}
	l, err := s.people().ReplaceLink(r.Context(), person)
	if err != nil {
		s.peopleFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.personLinkView(l))
}

// myPersonLinkQR is GET /v1/me/link/qr: the link as a QR code, SVG or
// (?format=png) PNG.
func (s *Server) myPersonLinkQR(w http.ResponseWriter, r *http.Request) {
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	l, err := s.people().LinkOf(r.Context(), person)
	if err != nil {
		s.peopleFail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Query().Get("format") == "png" {
		png, err := storefront.QRPNG(l.URL)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
		return
	}
	svg, err := storefront.QRSVG(l.URL)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	_, _ = io.WriteString(w, svg)
}

// personLinkGone is what a replaced link says.
const personLinkGone = "This link no longer works. Ask the person for their new one."

// personByLink is GET /v1/people/links/{code}: the person a link names, as
// the signed-in viewer may see them, with an add token; 410 {gone} for a
// link replaced in the last 30 days, 404 for any other.
func (s *Server) personByLink(w http.ResponseWriter, r *http.Request) {
	viewer, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	if !s.limiter("people-link:"+viewer.String(), time.Second, 30).Allow() {
		writeErr(w, http.StatusTooManyRequests, "Too many links opened. Wait a moment.")
		return
	}
	p := s.people()
	id, err := p.Resolve(r.Context(), chi.URLParam(r, "code"))
	switch {
	case errors.Is(err, people.ErrLinkReplaced):
		writeJSON(w, http.StatusGone, map[string]any{"error": personLinkGone, "gone": true})
		return
	case errors.Is(err, people.ErrLinkNotFound):
		writeErr(w, http.StatusNotFound, "This link is not a Taskiem person's link. Check it, or ask them for it.")
		return
	case err != nil:
		s.peopleFail(w, r, err)
		return
	}
	c, err := p.CardOf(r.Context(), viewer, id)
	if err != nil {
		s.peopleFail(w, r, err)
		return
	}
	out := foundOut{Found: true, Self: id == viewer, Person: ptr(cardOut(c))}
	if id != viewer {
		out.AddToken = p.AddToken(viewer, id)
	}
	writeJSON(w, http.StatusOK, out)
}

func ptr[T any](v T) *T { return &v }

// personLinkCard is a person's card at their link, for anyone.
type personLinkCard struct {
	Code  string    `json:"code"`
	Link  string    `json:"link"`
	Name  string    `json:"name"`
	Photo *photoRef `json:"photo"`
	App   string    `json:"app,omitempty"`
}

func (s *Server) personLinkCardFor(ctx context.Context, code string) (personLinkCard, error) {
	p := s.people()
	c, err := p.PublicCardOf(ctx, code)
	if err != nil {
		return personLinkCard{}, err
	}
	code = chatlinks.Clean(code)
	out := personLinkCard{Code: code, Link: p.LinkURL(code), Name: c.Name, App: s.AppGetURL}
	if c.HasPhoto {
		out.Photo = &photoRef{URL: "/v1/person-links/" + code + "/photo"}
		if _, _, at, err := p.Photo(ctx, uuid.Nil, c.Person); err == nil {
			out.Photo.URL += "?v=" + strconv.FormatInt(at.UnixMilli(), 10)
			out.Photo.UpdatedAt = at
		}
	}
	return out, nil
}

// getPersonLinkCard is GET /v1/person-links/{code}, for anyone: the name,
// and the photo when the person shows it to everyone. 410 {gone} for a
// link replaced in the last 30 days, 404 for any other. Sixty a minute
// per address.
func (s *Server) getPersonLinkCard(w http.ResponseWriter, r *http.Request) {
	if !s.limiter("person-link:"+limitIP(r), time.Minute, 60).Allow() {
		writeErr(w, http.StatusTooManyRequests, "Too many links opened. Wait a minute, then try again.")
		return
	}
	out, err := s.personLinkCardFor(r.Context(), chi.URLParam(r, "code"))
	switch {
	case errors.Is(err, people.ErrLinkReplaced):
		writeJSON(w, http.StatusGone, map[string]any{"error": personLinkGone, "gone": true})
	case errors.Is(err, people.ErrLinkNotFound), errors.Is(err, people.ErrOff):
		writeErr(w, http.StatusNotFound, "This link is not a Taskiem person's link. Check it, or ask them for it.")
	case err != nil:
		s.fail(w, r, err)
	default:
		w.Header().Set("Cache-Control", "no-cache")
		writeJSON(w, http.StatusOK, out)
	}
}

// personLinkPhoto is GET /v1/person-links/{code}/photo, for anyone: the
// photo when the person shows it to everyone; 404 otherwise.
func (s *Server) personLinkPhoto(w http.ResponseWriter, r *http.Request) {
	if !s.limiter("person-link:"+limitIP(r), time.Minute, 60).Allow() {
		writeErr(w, http.StatusTooManyRequests, "Too many links opened. Wait a minute, then try again.")
		return
	}
	p := s.people()
	c, err := p.PublicCardOf(r.Context(), chi.URLParam(r, "code"))
	if err != nil || !c.HasPhoto {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	ctype, data, at, err := p.Photo(r.Context(), uuid.Nil, c.Person)
	if err != nil {
		s.peopleFail(w, r, err)
		return
	}
	writePhoto(w, r, ctype, data, at, "public, max-age=300")
}

// --- presence ---

// myPresence is POST /v1/me/presence ({online}): the app's heartbeat while
// it is in front (online: true, about every 30 seconds) and its goodbye
// when it goes to the background (online: false). 204.
func (s *Server) myPresence(w http.ResponseWriter, r *http.Request) {
	if !jsonOnly(w, r) {
		return
	}
	person, err := personOf(r)
	if err != nil {
		s.personFail(w, r, err)
		return
	}
	var req struct {
		Online bool `json:"online"`
		// Connection: one of the person's live connections (GET
		// /v1/me/live's ready event): it is in front (online) or away.
		Connection *uuid.UUID `json:"connection"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.limiter("presence:"+person.String(), 5*time.Second, 6).Allow() {
		w.WriteHeader(http.StatusNoContent) // a heartbeat too soon changes nothing
		return
	}
	if req.Connection != nil {
		// Away is the person's only when none of their connections is in
		// front; a connection no longer open is said as the heartbeat is.
		found, err := s.liveFront(r.Context(), person, *req.Connection, req.Online)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if found {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	p := s.people()
	if req.Online {
		err = p.Touch(r.Context(), person)
	} else {
		err = p.Away(r.Context(), person)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- the page /p/{code}, when no web app is served here ---

const personLinkSource = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{if .Gone}}This link no longer works{{else if .Missing}}Link not found{{else}}Chat with {{.Card.Name}}{{end}} · Taskiem</title>
<style>
:root{color-scheme:light dark;--bg:#fafaf7;--fg:#1c1c1a;--muted:#5f5f5a;--card:#fff;--line:#e4e4de;--accent:#0f6b4f;--on:#fff}
@media (prefers-color-scheme:dark){:root{--bg:#121211;--fg:#f1f1ec;--muted:#a9a9a2;--card:#1d1d1b;--line:#33332f;--accent:#3fb489;--on:#08140f}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.5 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif}
` + cardPageCSS + `
img{width:96px;height:96px;border-radius:50%;object-fit:cover}h1{font-size:1.5rem;margin:.75rem 0 .25rem}p{margin:.25rem 0;color:var(--muted)}
.btn{display:block;margin-top:1rem;padding:.8rem 1rem;border-radius:999px;text-decoration:none;font-weight:600;border:1px solid var(--accent);color:var(--accent)}
.btn.main{background:var(--accent);color:var(--on)}
</style></head><body><main><div class="card">
{{if .Gone}}<h1>This link no longer works</h1><p>They made a new link. Ask them for it.</p>
{{else if .Missing}}<h1>Link not found</h1><p>Check the link, or ask them for it.</p>
{{else}}{{with .Photo}}<img src="{{.}}" alt="">{{end}}
<h1>{{.Card.Name}}</h1><p>is on Taskiem</p>
<a class="btn main" href="{{.Chat}}">Chat on Taskiem</a>
{{with .App}}<a class="btn" href="{{.}}">Get the Taskiem app</a>{{end}}{{end}}
</div></main></body></html>`

var personLinkPage = template.Must(template.New("p").Parse(personLinkSource))

// personLinkHTML serves /p/{code} where this server serves no web app (the
// web app's own page is used where it does, and the phone app opens the
// link itself): the person's name, their photo when shown to everyone,
// "Chat on Taskiem" (signing in, then the chat) and "Get the Taskiem app".
func (s *Server) personLinkHTML(w http.ResponseWriter, r *http.Request) {
	if !s.limiter("person-link:"+limitIP(r), time.Minute, 60).Allow() {
		writeErr(w, http.StatusTooManyRequests, "Too many links opened. Wait a minute, then try again.")
		return
	}
	code := chatlinks.Clean(chi.URLParam(r, "code"))
	data := struct {
		Card          personLinkCard
		Gone, Missing bool
		Photo         string
		Chat, App     string
	}{App: s.AppGetURL}
	status := http.StatusOK
	out, err := s.personLinkCardFor(r.Context(), code)
	switch {
	case errors.Is(err, people.ErrLinkReplaced):
		data.Gone, status = true, http.StatusGone
	case errors.Is(err, people.ErrLinkNotFound), errors.Is(err, people.ErrOff):
		data.Missing, status = true, http.StatusNotFound
	case err != nil:
		s.fail(w, r, err)
		return
	default:
		data.Card = out
		if out.Photo != nil {
			data.Photo = out.Photo.URL
		}
		data.Chat = strings.TrimSuffix(s.PublicURL, "/") + "/login?next=" + "/p/" + code
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(status)
	_ = personLinkPage.Execute(w, data)
}
