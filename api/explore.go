package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Explore (migration 00401): finding a business by browsing. The page lists
// businesses that chose to be found, with their logo and photos; asking one
// to link is the same request as from a search (POST /v1/links). Nothing is
// served for a business that is not findable (unless we are linked with it),
// for a photo that does not exist or was refused, or for ourselves: the same
// 404 for each.

func (s *Server) exploreRoutes(r chi.Router) {
	r.Get("/explore", s.explore)
	r.Get("/explore/{business}/logo", s.exploreLogo)
	r.Get("/explore/{business}/photos/{photo}", s.explorePhoto)
}

// explorePage is how many businesses a section of the page lists at once.
const explorePage = 12

type exploreOut struct {
	// Results are what a search found; Near and Fresh are the page's two
	// sections when nothing is typed. More says Fresh has another page.
	Results []links.ExploreCard `json:"results"`
	Near    []links.ExploreCard `json:"near"`
	Fresh   []links.ExploreCard `json:"fresh"`
	More    bool                `json:"more"`
	Blind   *blindOffer         `json:"blind,omitempty"`
}

func (s *Server) explore(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	out := exploreOut{Results: []links.ExploreCard{}, Near: []links.ExploreCard{}, Fresh: []links.ExploreCard{}}
	// A phone number is never searched: one row that sends a request.
	if links.LooksLikePhone(q) {
		if phone, err := whatsapp.Normalise(q, s.defaultCountry()); err == nil {
			out.Blind = &blindOffer{Kind: "phone", Shown: links.Mask("phone", phone)}
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 || offset > 1000 {
		offset = 0
	}
	section := r.URL.Query().Get("section")
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		switch {
		case q != "":
			out.Results, err = links.ExploreTx(r.Context(), tx, links.SectionSearch, q, 20, 0)
		case section == links.SectionNew:
			// One more than a page tells whether there is another.
			out.Fresh, err = links.ExploreTx(r.Context(), tx, links.SectionNew, "", explorePage+1, offset)
			if len(out.Fresh) > explorePage {
				out.Fresh, out.More = out.Fresh[:explorePage], true
			}
		default:
			if out.Near, err = links.ExploreTx(r.Context(), tx, links.SectionNear, "", explorePage, 0); err != nil {
				return err
			}
			out.Fresh, err = links.ExploreTx(r.Context(), tx, links.SectionNew, "", explorePage+1, 0)
			if len(out.Fresh) > explorePage {
				out.Fresh, out.More = out.Fresh[:explorePage], true
			}
		}
		return err
	}); err != nil {
		s.fail(w, r, linkErr(err))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) exploreLogo(w http.ResponseWriter, r *http.Request) {
	business, err := uuid.Parse(chi.URLParam(r, "business"))
	if err != nil || s.Media == nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var urls map[string]string
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		urls, err = links.ExploreLogoURLsTx(r.Context(), tx, business)
		return err
	}); err != nil {
		s.fail(w, r, linkErr(err))
		return
	}
	s.sendBusinessImage(w, r, business, urls)
}

func (s *Server) explorePhoto(w http.ResponseWriter, r *http.Request) {
	business, err := uuid.Parse(chi.URLParam(r, "business"))
	n, nerr := strconv.Atoi(chi.URLParam(r, "photo"))
	if err != nil || nerr != nil || n < 0 || n > 7 || s.Media == nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var urls map[string]string
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		urls, _, err = links.PhotoURLsTx(r.Context(), tx, business, n)
		return err
	}); err != nil {
		s.fail(w, r, linkErr(err))
		return
	}
	s.sendBusinessImage(w, r, business, urls)
}

// sendBusinessImage fetches one of a business's stored images with its own
// storage connection (media.FetchPublic checks the address is under its
// project) and sends it, in the size asked for or the nearest.
func (s *Server) sendBusinessImage(w http.ResponseWriter, r *http.Request, business uuid.UUID, urls map[string]string) {
	src := urls[r.URL.Query().Get("size")]
	for _, k := range []string{"320", "800", "2000"} {
		if src == "" {
			src = urls[k]
		}
	}
	data, ctype, err := s.Media.FetchPublic(r.Context(), business, src)
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(data) //nolint:gosec // a JPEG or PNG by its bytes (FetchPublic), sent as that type with nosniff
}
