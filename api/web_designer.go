package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/embedsites"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Send to my web designer (no-technical-steps N9): an owner whose website
// someone else looks after sends them, by email, what to put on it: the
// proof that the website is the business's, and the code that shows the
// page there. The owner never handles either.

// EmbedSnippet is the code that shows a storefront's page on a website.
func EmbedSnippet(pageURL, business string) string {
	title := strings.NewReplacer(`"`, "&quot;", "<", "&lt;", ">", "&gt;", "&", "&amp;").Replace(business)
	return `<iframe src="` + strings.TrimRight(pageURL, "/") + `/embed" title="` + title + `" style="width:100%;min-height:720px;border:0" loading="lazy"></iframe>`
}

// designerEmail is the email's subject and body.
func designerEmail(business, owner string, site embedsites.Site, pageURL string) (string, string) {
	var proof string
	if site.Method == embedsites.DNS {
		in := site.Instructions()
		proof = "Add this DNS record for " + site.Host() + ":\n\n  Type:  TXT\n  Name:  " + in["record_name"] + "\n  Value: " + in["record_value"] + "\n"
	} else {
		in := site.Instructions()
		proof = "Put a plain text file at\n\n  " + in["file_url"] + "\n\ncontaining this line:\n\n  " + in["file_contents"] + "\n"
	}
	subject := business + ": please add our Taskiem page to " + site.Host()
	body := "Hello,\n\n" + business + " would like their Taskiem page (catalogue, orders and bookings) to show on " + site.Origin + ". There are two things to do.\n\n" +
		"1. Show that the website is " + business + "'s. " + proof + "\nKeep it there: Taskiem checks it every few hours.\n\n" +
		"2. Put this code where the page should appear:\n\n  " + EmbedSnippet(pageURL, business) + "\n\n" +
		"Once the first is done, " + business + " presses Check in Taskiem and the page shows on the website.\n\n" +
		"If you have a question, reply to " + owner + ".\n\nTaskiem, on behalf of " + business + "\n"
	return subject, body
}

// sendToDesigner answers POST /v1/embed-sites/{id}/send-to-designer with
// {"email": "..."}.
func (s *Server) sendToDesigner(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	to, err := sender.NormaliseEmail(req.Email)
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: that is not an email address", errBadRequest))
		return
	}
	if !s.mailOn() {
		s.fail(w, r, fmt.Errorf("%w: this Taskiem cannot send email; copy the code and the record to your web designer instead", errConflict))
		return
	}
	p := principalFrom(r.Context())
	var site embedsites.Site
	var sf storefront.Storefront
	var business, owner string
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if site, err = embedsites.Get(r.Context(), tx, p.TenantID, id); err != nil {
			return siteErr(err)
		}
		if sf, err = storefront.Get(r.Context(), tx, p.TenantID); err != nil {
			return storefrontErr(err)
		}
		if err := tx.QueryRow(r.Context(), `SELECT name FROM tenants WHERE id = $1`, p.TenantID).Scan(&business); err != nil {
			return err
		}
		if p.UserID != uuid.Nil {
			if err := tx.QueryRow(r.Context(), `SELECT email FROM users WHERE id = $1`, p.UserID).Scan(&owner); err != nil {
				return err
			}
		}
		return auditTx(r, tx, "embed_site.sent_to_designer", site.Origin, map[string]any{"to_domain": to[strings.LastIndexByte(to, '@')+1:]})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	pageURL := sf.URL(s.previewDomain())
	if pageURL == "" {
		s.fail(w, r, fmt.Errorf("%w: publish your page first", errConflict))
		return
	}
	if owner == "" {
		owner = "the business"
	}
	subject, body := designerEmail(business, owner, site, pageURL)
	if err := s.sendMail(r.Context(), to, subject, body); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": true, "to": to})
}
