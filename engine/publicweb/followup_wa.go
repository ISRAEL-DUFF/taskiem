package publicweb

import (
	"net/http"

	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/lang"
)

// WhatsApp follow-ups on the public host (docs/spec/forms-that-sell.md
// §6.2, §6.4, L4). Follow-ups go by text, email or WhatsApp; the consent
// line names WhatsApp only where a follow-up may go there: the business's
// own number's account holds the built-in template approved with our
// words, and the owner lets follow-ups go there (DBStorefronts). The
// category Meta gave it there decides the words: utility, the line names
// WhatsApp; marketing, the line stays text or email and a box asks, and
// only a buyer who ticks it is reached there (the keep says so); nothing
// else is WhatsApp. The template's
// Finish my order button opens <form host>/back/<token>, a fixed address
// Meta approves, which leads to the link on the storefront's own
// address: drawn there, it uses nothing up (GET claims nothing).

// followupWords are an order form's consent line and, for a marketing
// template, the box that asks for WhatsApp; named says the form names
// WhatsApp (either), which the page's keeps then say (keep.js), so a
// follow-up goes there only on a promise the form made.
func (s *Server) followupWords(p StorefrontPage, l string) (consent, optIn string, named bool) {
	c, tag := lang.Default(), lang.Tag(l)
	switch p.FollowupWhatsApp {
	case "utility":
		return c.Text(tag, "form.followup.consent_whatsapp"), "", true
	case "marketing":
		return c.Text(tag, "form.followup.consent"), c.Text(tag, "form.followup.whatsapp_optin"), true
	}
	return c.Text(tag, "form.followup.consent"), "", false
}

// serveBackOnFormHost leads a follow-up's link opened on the form host
// (the WhatsApp template's button) to the same link on its storefront.
func (s *Server) serveBackOnFormHost(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		s.page(w, r, http.StatusMethodNotAllowed, "notfound", pageData{Title: "Page not found"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	if s.Keeps == nil || s.PreviewDomain == "" {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return
	}
	slug, ok, err := s.Keeps.Where(r.Context(), forms.TokenHash(token))
	if err != nil {
		s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
		return
	}
	if !ok {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "This link has expired"})
		return
	}
	http.Redirect(w, r, "https://"+slug+"."+s.PreviewDomain+"/back/"+token, http.StatusFound) //nolint:gosec // the storefront's slug from the database and a token matched by backPath
}
