package publicweb

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Forms left half-filled on the public host (docs/spec/forms-that-sell.md
// §6): an order form that follows up draws the consent line under the
// contact and a keep token, its submit token's twin; a small script sends
// a keep once a valid contact is typed and when the page is left, with
// the contact and the product choice only. A follow-up's link, /back/<token>,
// draws a page that uses nothing up (a link previewer opening it restores
// nothing): Finish my order claims the restore once and opens the form
// with the choice kept; Stop these messages stops them.

const (
	// keepPath ends a keep's address: /a/<ref>/keep, /<front>/a/<ref>/keep.
	keepPath = "/keep"
	// keepSuffix makes a page's keep token's form id from its submit
	// token's, so neither stands for the other.
	keepSuffix = "#keep"
	// maxKeepBody bounds a keep's body.
	maxKeepBody = 4096
)

// FormKeepsPerIP caps the keeps one client address sends, across forms:
// a person sends a few per page.
var FormKeepsPerIP = formguard.Bucket{Rate: rate.Every(time.Hour / 120), Burst: 30}

// FormKeepContactsPerIP caps the different contacts one client address
// names in its keeps, across forms: a person names one or two (a typo
// corrected), and a script naming strangers' numbers to have them texted
// is refused after ten. Generous for an address many phones share
// (carrier-grade NAT), as the other buckets are.
var FormKeepContactsPerIP = formguard.Bucket{Rate: rate.Every(time.Hour / 10), Burst: 10}

// FormKeepHoldPerIP: one address naming more than five different contacts
// on one form within about an hour holds that form's follow-ups for its
// owner to release (§6.5), as a run of keeps does.
var FormKeepHoldPerIP = formguard.Bucket{Rate: rate.Every(time.Hour / 5), Burst: 5}

// backPath is a follow-up's link: /back/<token>, and its Stop.
var backPath = regexp.MustCompile(`^/back/([A-Za-z0-9_-]{32})(?:/(stop))?$`)

// keepFor gives an order form that follows up its keep's address, and
// whether the form names WhatsApp; its token is drawn with the submit
// token (showForm).
func (s *Server) keepFor(f *formData, whatsApp bool) {
	f.Keep = &keepView{URL: f.Action + keepPath, WhatsApp: whatsApp}
}

// serveKeep takes a keep: always 204 unless limited, so a script learns
// nothing of what was kept.
func (s *Server) serveKeep(w http.ResponseWriter, r *http.Request, site Site, formPath string) {
	var frontSlug, ref string
	switch {
	case strings.HasPrefix(formPath, "/a/") && actionRef.MatchString(formPath[len("/a/"):]):
		ref = formPath[len("/a/"):]
	case frontActionPath.MatchString(formPath):
		m := frontActionPath.FindStringSubmatch(formPath)
		frontSlug, ref = m[1], m[2]
	default:
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if s.Keeps == nil || s.FormKeys == nil || s.guard == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxKeepBody)
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	v := r.PostForm
	ip := formguard.HashIP(s.ipHashKey(), s.ClientIP(r), s.now())
	if !s.FormKeeps.Allow(hex.EncodeToString(ip[:])) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	if formguard.HoneypotTripped(v) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ctx := r.Context()
	p, ok, err := s.storefrontPage(ctx, site)
	if err != nil || !ok || p.Status != "published" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	actions, base := p.Model.Home().Actions, ""
	var front *storefront.PublicFront
	if frontSlug != "" {
		f, found := frontOf(p, frontSlug)
		if !found {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		actions, base, front = f.Actions, "/"+f.Slug, &f
	}
	act, enabled := findAction(actions, ref)
	if !enabled || act.Kind != "order" || act.ID == nil || !p.Followups[*act.ID] {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	form := formID(site.ID, strings.TrimPrefix(base+"/"+act.Ref(), "/"))
	claims, err := s.guard.Verify(v.Get("keep"), form+keepSuffix, s.now())
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !s.FormPerForm.Allow("keep:" + act.ID.String()) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	// The status hash the page's order has, or would have: the keep is
	// refused once it is sent, and two contacts on the page make one.
	page := storefront.StatusHash(s.statusKey(claims.IdempotencyKeyFor(form)))
	k := forms.Keep{Contact: v.Get("contact"), Choice: keepChoice(v, act, p.Items), At: s.now().UTC(), Page: page}
	_, contact, err := k.ContactOf()
	if err != nil {
		w.WriteHeader(http.StatusNoContent) // a contact that does not read: nothing kept
		return
	}
	// A contact this address has not named on this form lately counts
	// against its contacts' bucket, and against the form's hold.
	addr := hex.EncodeToString(ip[:])
	m := hmac.New(sha256.New, s.ipHashKey())
	m.Write([]byte(contact))
	seen := addr + "|" + act.ID.String() + "|" + hex.EncodeToString(m.Sum(nil))
	if _, named := s.keepSeen.Get(seen); !named {
		k.Hold = !s.FormKeepHold.Allow(addr + "|" + act.ID.String())
		if !s.FormKeepContacts.Allow(addr) {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		s.keepSeen.Put(seen, true)
	}
	if front != nil {
		id := front.ID
		k.Front = &id
	}
	if o := v.Get("site"); o != "" {
		if origins, err := s.embedOrigins(ctx, site.Tenant); err == nil && slices.Contains(origins, o) {
			k.Site = o
		}
	}
	// WhatsApp only where the form the buyer saw named it, and names it
	// still (L4's review): the page says it drew the WhatsApp words, and
	// this host would draw them now.
	if _, _, named := s.followupWords(p, p.Model.Card.Language); named && v.Get("named") == "wa" {
		k.Choice.WhatsAppNamed = true
		if s.WAFollowups == "marketing" && v.Get("wa") == "1" {
			k.Choice.WhatsApp = true // the buyer asked for WhatsApp (L4)
		}
	}
	payload, err := SealKeep(s.FormKeys, *act.ID, k)
	if err != nil {
		w.WriteHeader(http.StatusNoContent) // a contact that does not read: nothing kept
		return
	}
	if _, err := s.Keeps.Keep(ctx, *act.ID, payload, page); err != nil && !errors.Is(err, context.Canceled) {
		s.Logger.Error("public host: a keep", "action", act.ID, "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// keepChoice reads the product choice from a keep: only a package the
// form sells, an area it delivers to, and lines of what it lists; nothing
// else typed.
func keepChoice(v url.Values, act storefront.PublicAction, items []storefront.Item) forms.Choice {
	var c forms.Choice
	form := storefront.EffectiveForm(act.Form, act.Kind)
	if pf, ok := storefront.FormPackages(form); ok {
		want, _ := strconv.Atoi(v.Get(packageField))
		for _, pk := range pf.Packages {
			if pk.Qty == want {
				c.Package = want
			}
		}
		// A size of the product the form sells, as the page lists it.
		if it, found := findItem(items, pf.Item); found {
			if z, ok := findItem(storefront.SizeItems(it), v.Get(sizeField)); ok {
				c.Size = z.ID
			}
		}
	}
	for _, z := range act.Zones {
		if z.Name == v.Get("zone") {
			c.Zone = z.Name
		}
	}
	if storefront.HasWidget(form, "basket") {
		on := storefront.BasketItems(form)
		for key, vals := range v {
			id, ok := strings.CutPrefix(key, "q.")
			if !ok || id == "" || len(id) > 120 || len(c.Lines) >= 50 {
				continue
			}
			// Only a row the page lists and the form's basket offers (a line
			// of it is <id>~k).
			id, _, _ = strings.Cut(id, "~")
			if it, found := findItem(items, id); !found || !storefront.OnForm(it, on) {
				continue
			}
			n, err := strconv.Atoi(vals[0])
			if err != nil || n <= 0 || n > 100 {
				continue
			}
			if i := slices.IndexFunc(c.Lines, func(l forms.ChoiceLine) bool { return l.ID == id }); i >= 0 {
				c.Lines[i].Qty = min(100, c.Lines[i].Qty+n)
				continue
			}
			c.Lines = append(c.Lines, forms.ChoiceLine{ID: id, Qty: n})
		}
		slices.SortFunc(c.Lines, func(a, b forms.ChoiceLine) int { return strings.Compare(a.ID, b.ID) })
	}
	return c
}

// backData is a follow-up's link's page.
type backData struct {
	pageData
	Business string
	Message  string
	// Restore offers Finish my order (Post); CanStop offers Stop these
	// messages (Stop).
	Restore, CanStop bool
	Post, Stop       string
	Home             string
}

// serveBack answers a follow-up's link: GET draws the page and claims
// nothing; POST finishes the order (the restore, once) or stops.
func (s *Server) serveBack(w http.ResponseWriter, r *http.Request, site Site, token, what string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, HEAD, POST")
		s.page(w, r, http.StatusMethodNotAllowed, "notfound", pageData{Title: "Page not found"})
		return
	}
	ctx := r.Context()
	p, ok, err := s.storefrontPage(ctx, site)
	if err != nil {
		s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
		return
	}
	if !ok || s.Keeps == nil {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return
	}
	business := p.Model.Card.Name
	d := backData{pageData: pageData{Title: "Your order", Lang: p.Model.Card.Language, Look: s.lookFor(ctx, site.Tenant, business)}, Business: business,
		Post: "/back/" + token, Stop: "/back/" + token + "/stop", Home: "/", CanStop: true}
	if d.Lang == "" {
		d.Lang = "en"
	}
	hash := forms.TokenHash(token)
	w.Header().Set("X-Robots-Tag", "noindex")
	if r.Method == http.MethodPost {
		ip := formguard.HashIP(s.ipHashKey(), s.ClientIP(r), s.now())
		if !s.Submissions.Allow(hex.EncodeToString(ip[:])) {
			w.Header().Set("Retry-After", "60")
			d.Message = "Too many tries from this connection. Please wait a few minutes and try again."
			s.renderPrivate(w, r, http.StatusTooManyRequests, "back.html", d)
			return
		}
	}
	// The link's form gone from the page: said so, and the link is kept,
	// should the form come back within its days.
	gone := "The order form this link opens is no longer on " + business + "'s page. You can still order from " + business + "'s page."
	used := "This link has been used already or is more than 7 days old. You can still order from " + business + "'s page."
	switch {
	case r.Method == http.MethodPost && what == "stop":
		stopped, err := s.Keeps.Stop(ctx, site.Tenant, hash)
		if err != nil {
			s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
			return
		}
		d.Title, d.CanStop = "Messages stopped", false
		d.Message = "You will get no more messages like this from " + business + "."
		if !stopped {
			d.Title, d.Message = "Nothing to stop", "This link is more than 7 days old, so there is nothing to stop. "+business+" will not send you another message about that order."
		}
		s.renderPrivate(w, r, http.StatusOK, "back.html", d)
	case r.Method == http.MethodPost && what == "":
		form, ok, err := s.Keeps.RestoreOK(ctx, site.Tenant, hash)
		if err != nil {
			s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
			return
		}
		if _, found := restoreURL(p, Restored{Action: &form}); ok && !found {
			d.Message = gone
			s.renderPrivate(w, r, http.StatusGone, "back.html", d)
			return
		}
		got, ok, err := s.Keeps.Restore(ctx, site.Tenant, hash)
		if err != nil {
			s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
			return
		}
		if loc, found := restoreURL(p, got); ok && found {
			http.Redirect(w, r, loc, http.StatusSeeOther) //nolint:gosec // a path on this page, the form found in its published model; the query is the choice the server kept
			return
		}
		d.Message = used
		s.renderPrivate(w, r, http.StatusGone, "back.html", d)
	case what != "":
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
	default:
		form, ok, err := s.Keeps.RestoreOK(ctx, site.Tenant, hash)
		if err != nil {
			s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
			return
		}
		_, found := restoreURL(p, Restored{Action: &form})
		d.Restore = ok && found
		switch {
		case d.Restore:
			d.Message = "You started an order with " + business + ". Finish it where you left off: what you chose is kept, and you type your details again."
		case ok:
			d.Message = gone
		default:
			d.Message = used
		}
		s.renderPrivate(w, r, http.StatusOK, "back.html", d)
	}
}

// restoreURL is the order form a restore opens, with the choice kept: the
// package, its size and the area, or the basket's first item.
func restoreURL(p StorefrontPage, got Restored) (string, bool) {
	if got.Action == nil {
		return "", false
	}
	for _, f := range p.Model.Fronts {
		for _, a := range f.Actions {
			if a.ID == nil || *a.ID != *got.Action {
				continue
			}
			base := ""
			if f.Slug != "" {
				base = "/" + f.Slug
			}
			q := url.Values{}
			if got.Choice.Package > 0 {
				q.Set(packageField, strconv.Itoa(got.Choice.Package))
			}
			if got.Choice.Size != "" {
				q.Set(sizeField, got.Choice.Size)
			}
			if got.Choice.Zone != "" {
				q.Set("zone", got.Choice.Zone)
			}
			if len(got.Choice.Lines) > 0 {
				q.Set(oneItemQuery, got.Choice.Lines[0].ID)
			}
			loc := base + "/a/" + a.Ref()
			if len(q) > 0 {
				loc += "?" + q.Encode()
			}
			return loc, true
		}
	}
	return "", false
}
