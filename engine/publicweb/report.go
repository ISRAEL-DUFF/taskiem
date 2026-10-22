package publicweb

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/formguard"
)

// Reports from a page's footer (storefronts spec 7.3, 7.4;
// docs/moderation.md): a reason, an optional message and contact, taken
// without JavaScript. They reach the operator console; enough of them from
// different people suspend the page until someone looks.

// Report is one report as the public host takes it.
type Report struct {
	Kind    string // storefront or form
	Site    string // the storefront's id, or the form's public id
	Reason  string
	Message string
	// Contact is the reporter's phone or email, sealed for the api role
	// (forms.Keys), or nil.
	Contact []byte
	// IPHash is a keyed digest of the address, to count distinct people.
	IPHash []byte
}

// Reports records a report; false when it is not taken (unknown page, too
// many from one address).
type Reports func(ctx context.Context, r Report) (bool, error)

// DBReports records reports with taskiem_public_report.
func DBReports(pool *pgxpool.Pool) Reports {
	return func(ctx context.Context, r Report) (bool, error) {
		var ok bool
		err := pool.QueryRow(ctx, `SELECT taskiem_public_report($1, $2, $3, $4, $5, $6)`, r.Kind, r.Site, r.Reason, r.Message, r.Contact, r.IPHash).Scan(&ok)
		return ok, err
	}
}

// reportReasons are the reasons a visitor picks from.
var reportReasons = []struct{ Value, Label string }{
	{"scam", "It looks like a scam"},
	{"impersonation", "It pretends to be someone else"},
	{"prohibited", "It sells something not allowed"},
	{"offensive", "It is offensive"},
	{"other", "Something else"},
}

type reportData struct {
	pageData
	Business string
	Home     string
	Reasons  []struct{ Value, Label string }
	Done     bool
	Error    string
}

var (
	reportLimitOnce sync.Once
	reportLimit     *formguard.Limiter
)

func reportLimiter() *formguard.Limiter {
	reportLimitOnce.Do(func() { reportLimit = formguard.NewLimiter(rate.Every(time.Minute), 5, 10000, time.Hour) })
	return reportLimit
}

// reportIPHash is a stable keyed digest of the visitor's address: the same
// person reporting twice counts once.
func (s *Server) reportIPHash(r *http.Request) []byte {
	key := s.FormKey
	if s.FormKeys != nil {
		key = s.FormKeys.Guard
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte("report client address\x00" + s.ClientIP(r)))
	return m.Sum(nil)
}

// storefrontSuspended answers for a suspended page (every path), and
// reports whether it did.
func (s *Server) storefrontSuspended(w http.ResponseWriter, r *http.Request, site Site) bool {
	p, ok, err := s.storefrontPage(r.Context(), site.ID)
	if err != nil || !ok || p.Status != "suspended" {
		return false // a lookup failure is answered by the page itself
	}
	s.page(w, r, http.StatusGone, "unavailable", pageData{Title: "This page is unavailable"})
	return true
}

// serveReport shows and takes the "Report this page" form.
func (s *Server) serveReport(w http.ResponseWriter, r *http.Request, site Site) {
	p, ok, err := s.storefrontPage(r.Context(), site.ID)
	if err != nil || !ok {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return
	}
	d := reportData{pageData: pageData{Title: "Report this page", Lang: "en", Look: s.lookFor(r.Context(), site.Tenant, p.Model.Card.Name)}, Business: p.Model.Card.Name, Home: "/",
		Reasons: reportReasons}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		s.renderPrivate(w, r, http.StatusOK, "report.html", d)
		return
	case http.MethodPost:
	default:
		w.Header().Set("Allow", "GET, HEAD, POST")
		s.page(w, r, http.StatusMethodNotAllowed, "notfound", pageData{Title: "Page not found"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil {
		d.Error = "That was too long."
		s.renderPrivate(w, r, http.StatusBadRequest, "report.html", d)
		return
	}
	ok, err = s.takeReport(r, "storefront", site.ID.String())
	switch {
	case errors.Is(err, errReportInvalid):
		d.Error = "Choose a reason."
		s.renderPrivate(w, r, http.StatusBadRequest, "report.html", d)
	case err != nil:
		s.Logger.Error("public host: a report", "storefront", site.ID, "err", err)
		s.page(w, r, http.StatusServiceUnavailable, "unavailable", pageData{Title: "Try again shortly"})
	default:
		// Taken or not (too many from one address), the answer is the same.
		_ = ok
		d.Done = true
		s.renderPrivate(w, r, http.StatusOK, "report.html", d)
	}
}

var errReportInvalid = errors.New("publicweb: an invalid report")

// takeReport reads a report form (reason, message, contact) and records
// it. The honeypot field, filled in, takes nothing.
func (s *Server) takeReport(r *http.Request, kind, site string) (bool, error) {
	if !reportLimiter().Allow(s.ClientIP(r)) || r.PostForm.Get("website") != "" {
		return false, nil
	}
	reason := r.PostForm.Get("reason")
	valid := false
	for _, x := range reportReasons {
		valid = valid || x.Value == reason
	}
	if !valid || s.Reports == nil {
		return false, errReportInvalid
	}
	msg, _ := text(r.PostForm.Get("message"), 500, true)
	rep := Report{Kind: kind, Site: site, Reason: reason, Message: msg, IPHash: s.reportIPHash(r)}
	if c := strings.TrimSpace(r.PostForm.Get("contact")); c != "" && len(c) <= 200 && s.FormKeys != nil {
		sealed, err := s.FormKeys.Seal("report contact", c)
		if err != nil {
			return false, err
		}
		rep.Contact = sealed
	}
	return s.Reports(r.Context(), rep)
}
