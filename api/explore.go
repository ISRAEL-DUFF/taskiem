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
	// sections when nothing is typed, each with whether another page waits
	// (section=near or section=new, with offset, gives it).
	Results  []links.ExploreCard `json:"results"`
	Near     []links.ExploreCard `json:"near"`
	NearMore bool                `json:"near_more"`
	Fresh    []links.ExploreCard `json:"fresh"`
	More     bool                `json:"more"`
	Blind    *blindOffer         `json:"blind,omitempty"`
}

// section lists one page of a browse section: one more than a page, to tell
// whether there is another.
func (s *Server) exploreSection(r *http.Request, tx pgx.Tx, section string, offset int) ([]links.ExploreCard, bool, error) {
	cards, err := links.ExploreTx(r.Context(), tx, section, "", explorePage+1, offset)
	if err != nil {
		return nil, false, err
	}
	if len(cards) > explorePage {
		return cards[:explorePage], true, nil
	}
	return cards, false, nil
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
			out.Fresh, out.More, err = s.exploreSection(r, tx, links.SectionNew, offset)
		case section == links.SectionNear:
			out.Near, out.NearMore, err = s.exploreSection(r, tx, links.SectionNear, offset)
		default:
			if out.Near, out.NearMore, err = s.exploreSection(r, tx, links.SectionNear, 0); err != nil {
				return err
			}
			out.Fresh, out.More, err = s.exploreSection(r, tx, links.SectionNew, 0)
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
