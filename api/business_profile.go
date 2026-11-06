package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/media"
)

// A business's profile and gallery (migration 00399): what it says about
// itself to the businesses that find it, with or without a storefront. The
// words, the logo and the photos are changed by whoever may manage links;
// anyone in the business reads them. Photos are stored as the storefront's
// are (resized, stripped, checked by the moderator) and listed to others
// only while the business is findable.

func (s *Server) profileRoutes(r chi.Router) {
	manage := s.need(PermLinkManage)
	r.Get("/business-profile", s.getBusinessProfile)
	r.Get("/business-profile/image", s.profileImage)
	r.With(manage, s.tenantWide).Put("/business-profile", s.putBusinessProfile)
	r.With(manage, s.tenantWide).Post("/business-profile/logo", s.uploadProfileLogo)
	r.With(manage, s.tenantWide).Delete("/business-profile/logo", s.removeProfileLogo)
	r.With(manage, s.tenantWide).Post("/business-profile/gallery", s.addGalleryPhoto)
	r.With(manage, s.tenantWide).Put("/business-profile/gallery/order", s.orderGallery)
	r.With(manage, s.tenantWide).Patch("/business-profile/gallery/{id}", s.captionGalleryPhoto)
	r.With(manage, s.tenantWide).Delete("/business-profile/gallery/{id}", s.removeGalleryPhoto)
}

type profileOut struct {
	links.Profile
	Gallery    []links.GalleryPhoto `json:"gallery"`
	MaxGallery int                  `json:"max_gallery"`
}

func (s *Server) profileView(r *http.Request) (profileOut, error) {
	p := principalFrom(r.Context())
	out := profileOut{Gallery: []links.GalleryPhoto{}, MaxGallery: links.MaxGallery}
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		if out.Profile, err = links.ProfileTx(r.Context(), tx, p.TenantID); err != nil {
			return err
		}
		out.Gallery, err = links.GalleryTx(r.Context(), tx, p.TenantID)
		return err
	})
	return out, err
}

func (s *Server) getBusinessProfile(w http.ResponseWriter, r *http.Request) {
	out, err := s.profileView(r)
	if err != nil {
		s.fail(w, r, linkErr(err))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) putBusinessProfile(w http.ResponseWriter, r *http.Request) {
	var in links.ProfileIn
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := links.SaveProfileTx(r.Context(), tx, p.TenantID, in, p.ActorType()+":"+p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "link.profile", "profile", map[string]any{"name": in.Name != "", "tagline": in.Tagline != "", "area": in.Area != "", "about": in.About != ""})
	}); err != nil {
		s.fail(w, r, linkErr(err))
		return
	}
	s.getBusinessProfile(w, r)
}

// profileImage serves one of the business's own profile photos to the web
// app, whose CSP allows images from its own origin only.
func (s *Server) profileImage(w http.ResponseWriter, r *http.Request) {
	if s.Media == nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	data, ctype, err := s.Media.FetchPublic(r.Context(), principalFrom(r.Context()).TenantID, r.URL.Query().Get("src"))
	if errors.Is(err, media.ErrNotOurs) {
		s.fail(w, r, fmt.Errorf("%w: %s", errBadRequest, plainError(err)))
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(data) //nolint:gosec // a JPEG or PNG by its bytes (FetchPublic), sent as that type with nosniff
}

func (s *Server) uploadProfileLogo(w http.ResponseWriter, r *http.Request) {
	m, err := s.storeStorefrontImage(r)
	if err != nil {
		s.failImage(w, r, linkErr(err))
		return
	}
	p := principalFrom(r.Context())
	if err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := links.SetLogoTx(r.Context(), tx, p.TenantID, m.ID, p.ActorType()+":"+p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "link.profile_logo", m.ID.String(), nil)
	}); err != nil {
		s.fail(w, r, linkErr(err))
		return
	}
	s.getBusinessProfile(w, r)
}

func (s *Server) removeProfileLogo(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := links.SetLogoTx(r.Context(), tx, p.TenantID, uuid.Nil, p.ActorType()+":"+p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "link.profile_logo", "removed", nil)
	}); err != nil {
		s.fail(w, r, linkErr(err))
		return
	}
	s.getBusinessProfile(w, r)
}

func (s *Server) addGalleryPhoto(w http.ResponseWriter, r *http.Request) {
	caption := strings.TrimSpace(r.URL.Query().Get("caption"))
	// A thirteenth photo is refused before anything is stored.
	p := principalFrom(r.Context())
	var n int
	if err := s.tx(r, func(tx pgx.Tx) error {
		g, err := links.GalleryTx(r.Context(), tx, p.TenantID)
		n = len(g)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if n >= links.MaxGallery {
		s.fail(w, r, linkErr(links.ErrGalleryFull))
		return
	}
	m, err := s.storeStorefrontImage(r)
	if err != nil {
		s.failImage(w, r, err)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := links.AddPhotoTx(r.Context(), tx, p.TenantID, m.ID, caption); err != nil {
			return err
		}
		return auditTx(r, tx, "link.gallery_add", m.ID.String(), nil)
	}); err != nil {
		s.fail(w, r, linkErr(err))
		return
	}
	out, err := s.profileView(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) orderGallery(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []uuid.UUID `json:"ids"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if err := s.tx(r, func(tx pgx.Tx) error {
		if err := links.ReorderTx(r.Context(), tx, p.TenantID, in.IDs); err != nil {
			return err
		}
		return auditTx(r, tx, "link.gallery_order", "gallery", nil)
	}); err != nil {
		s.fail(w, r, linkErr(err))
		return
	}
	s.getBusinessProfile(w, r)
}

func (s *Server) captionGalleryPhoto(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var in struct {
		Caption string `json:"caption"`
	}
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if err := s.tx(r, func(tx pgx.Tx) error {
		if err := links.CaptionTx(r.Context(), tx, p.TenantID, id, in.Caption); err != nil {
			return err
		}
		return auditTx(r, tx, "link.gallery_caption", id.String(), nil)
	}); err != nil {
		s.fail(w, r, linkErr(err))
		return
	}
	s.getBusinessProfile(w, r)
}

func (s *Server) removeGalleryPhoto(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	p := principalFrom(r.Context())
	if err := s.tx(r, func(tx pgx.Tx) error {
		if err := links.RemovePhotoTx(r.Context(), tx, p.TenantID, id); err != nil {
			return err
		}
		return auditTx(r, tx, "link.gallery_remove", id.String(), nil)
	}); err != nil {
		s.fail(w, r, linkErr(err))
		return
	}
	s.getBusinessProfile(w, r)
}
