package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	// Delivery lists the businesses that deliver for others (riders, docs/
	// spec/chat-riders.md), those covering our area first (section=delivery).
	Delivery     []links.ExploreCard `json:"delivery"`
	DeliveryMore bool                `json:"delivery_more"`
	// The sections of migration 00427 (docs/spec/mobile-app-explore-and-
	// offline.md §2.1): Featured (at most six, no paging), Trusted on
	// Taskiem, Popular (each card with its rounded count of links), and the
	// categories with their counts; section=category&category=<key> lists
	// one category's businesses in InCategory.
	Featured []links.ExploreCard `json:"featured"`
	// Across is Across Nigeria, in Near you's place when no business in our
	// area can be shown, or our area is not set (section=across pages it).
	Across       []links.ExploreCard `json:"across"`
	AcrossMore   bool                `json:"across_more"`
	Trusted      []links.ExploreCard `json:"trusted"`
	TrustedMore  bool                `json:"trusted_more"`
	Popular      []links.ExploreCard `json:"popular"`
	PopularMore  bool                `json:"popular_more"`
	Categories   []links.Category    `json:"categories"`
	InCategory   []links.ExploreCard `json:"in_category"`
	CategoryMore bool                `json:"category_more"`
	Blind        *blindOffer         `json:"blind,omitempty"`
	// Area is our own, as "near you" reads it ("Near you · Yaba").
	Area string `json:"area,omitempty"`
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

// moreSection is exploreSection for the sections of migration 00427.
func (s *Server) moreSection(r *http.Request, tx pgx.Tx, section, category string, offset int) ([]links.ExploreCard, bool, error) {
	cards, err := links.ExploreMoreTx(r.Context(), tx, section, category, explorePage+1, offset)
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
	out := exploreOut{Results: []links.ExploreCard{}, Near: []links.ExploreCard{}, Fresh: []links.ExploreCard{}, Delivery: []links.ExploreCard{},
		Featured: []links.ExploreCard{}, Across: []links.ExploreCard{}, Trusted: []links.ExploreCard{}, Popular: []links.ExploreCard{}, Categories: []links.Category{}, InCategory: []links.ExploreCard{}}
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
		case section == links.SectionDelivery:
			out.Delivery, out.DeliveryMore, err = s.exploreSection(r, tx, links.SectionDelivery, offset)
		case section == links.SectionAcross:
			out.Across, out.AcrossMore, err = s.moreSection(r, tx, links.SectionAcross, "", offset)
		case section == links.SectionTrusted:
			out.Trusted, out.TrustedMore, err = s.moreSection(r, tx, links.SectionTrusted, "", offset)
		case section == links.SectionPopular:
			out.Popular, out.PopularMore, err = s.moreSection(r, tx, links.SectionPopular, "", offset)
		case section == links.SectionFeatured:
			out.Featured, err = links.ExploreMoreTx(r.Context(), tx, links.SectionFeatured, "", 6, 0)
		case section == links.SectionCategory:
			category := r.URL.Query().Get("category")
			if !links.IsCategory(category) {
				return fmt.Errorf("%w: no such category", links.ErrInvalid)
			}
			out.InCategory, out.CategoryMore, err = s.moreSection(r, tx, links.SectionCategory, category, offset)
		default:
			if out.Area, err = links.OwnAreaTx(r.Context(), tx, principalFrom(r.Context()).TenantID); err != nil {
				return err
			}
			if out.Near, out.NearMore, err = s.exploreSection(r, tx, links.SectionNear, 0); err != nil {
				return err
			}
			if out.Delivery, out.DeliveryMore, err = s.exploreSection(r, tx, links.SectionDelivery, 0); err != nil {
				return err
			}
			if out.Fresh, out.More, err = s.exploreSection(r, tx, links.SectionNew, 0); err != nil {
				return err
			}
			if len(out.Near) == 0 {
				if out.Across, out.AcrossMore, err = s.moreSection(r, tx, links.SectionAcross, "", 0); err != nil {
					return err
				}
			}
			if out.Featured, err = links.ExploreMoreTx(r.Context(), tx, links.SectionFeatured, "", 6, 0); err != nil {
				return err
			}
			if out.Trusted, out.TrustedMore, err = s.moreSection(r, tx, links.SectionTrusted, "", 0); err != nil {
				return err
			}
			if out.Popular, out.PopularMore, err = s.moreSection(r, tx, links.SectionPopular, "", 0); err != nil {
				return err
			}
			out.Categories, err = links.ExploreCategoriesTx(r.Context(), tx)
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
	w.Header().Set("Cache-Control", pictureCache(r))
	_, _ = w.Write(data) //nolint:gosec // a JPEG or PNG by its bytes (FetchPublic), sent as that type with nosniff
}

// keptLong is how long a picture or file whose address names its version
// may be kept (docs/spec/mobile-app-explore-and-offline.md K-S4): the 30
// days a phone keeps what it read. A changed picture has another version,
// so another address; an attachment never changes.
const keptLong = "private, max-age=2592000, immutable"

// pictureCache is keptLong for a picture asked for by its version (the
// card's v), an hour for one asked for without.
func pictureCache(r *http.Request) string {
	if r.URL.Query().Get("v") != "" {
		return keptLong
	}
	return "private, max-age=3600"
}

// exploreScoresEvery is how often Explore's scores (whether each findable
// business is Trusted, its count of links) are worked out again
// (migration 00430): the sections read them instead of working them out
// for every business on every opening.
const exploreScoresEvery = 10 * time.Minute

// RescoreExplore works Explore's scores out again now, answering how many
// businesses were scored.
func (s *Server) RescoreExplore(ctx context.Context) (int, error) {
	var n int
	err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_link_explore_rescore()`).Scan(&n)
	return n, err
}

// runExploreScores keeps Explore's scores up to date: at once, then every
// exploreScoresEvery.
func (s *Server) runExploreScores(ctx context.Context) {
	tick := time.NewTicker(exploreScoresEvery)
	defer tick.Stop()
	for {
		if _, err := s.RescoreExplore(ctx); err != nil && ctx.Err() == nil {
			s.Logger.Error("explore: scores", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
