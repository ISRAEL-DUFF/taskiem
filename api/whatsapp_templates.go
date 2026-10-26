package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// An own number's WhatsApp templates (docs/whatsapp.md#templates): the
// Channels page shows each library template's status in the number's
// WhatsApp Business Account, submits one to Meta, and checks the
// statuses now. The platform number's are the operator's (taskiem
// whatsapp templates). Everything needs secret.manage, as the number
// does.

type waTemplatesView struct {
	// Tracked: the organisation has an own number whose account the
	// registry follows; without one the list shows the library only.
	Tracked   bool                      `json:"tracked"`
	Templates []whatsapp.TemplateStatus `json:"templates"`
	// Note says what cannot go out of the window until approved ("" when
	// every template is approved).
	Note string `json:"note,omitempty"`
}

// ownNumber is the tenant's own number's Platform, or errConflict.
func (s *Server) ownNumber(ctx context.Context, p *Principal) (*whatsapp.Platform, error) {
	if s.WhatsApp == nil {
		return nil, errWhatsAppOff
	}
	wa, err := s.WhatsApp.ForTenant(ctx, p.TenantID)
	if err != nil {
		return nil, err
	}
	if !wa.Own() {
		return nil, fmt.Errorf("%w: connect your own WhatsApp number first", errConflict)
	}
	return wa, nil
}

func (s *Server) waTemplatesView(ctx context.Context, p *Principal) (waTemplatesView, error) {
	v := waTemplatesView{Templates: []whatsapp.TemplateStatus{}}
	wa, err := s.ownNumber(ctx, p)
	if errors.Is(err, errConflict) || errors.Is(err, errWhatsAppOff) {
		for _, t := range whatsapp.AllTemplates {
			v.Templates = append(v.Templates, whatsapp.TemplateStatus{Name: t.Name, Category: whatsapp.Category(t), Language: "en", Status: whatsapp.TemplateNotSubmitted, Purpose: t.Purpose})
		}
		return v, nil
	}
	if err != nil {
		return v, err
	}
	v.Tracked = true
	if v.Templates, err = wa.TemplateStatuses(ctx); err != nil {
		return v, err
	}
	v.Note = whatsapp.NotApprovedNote(v.Templates)
	return v, nil
}

// getWhatsAppTemplates lists the templates with their status in the own
// number's account.
func (s *Server) getWhatsAppTemplates(w http.ResponseWriter, r *http.Request) {
	v, err := s.waTemplatesView(r.Context(), principalFrom(r.Context()))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// submitWhatsAppTemplate submits one library template to Meta from the
// own number's account (audited).
func (s *Server) submitWhatsAppTemplate(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	name := chi.URLParam(r, "name")
	if _, ok := whatsapp.TemplateNamed(name); !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	wa, err := s.ownNumber(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	st, err := wa.SubmitTemplate(r.Context(), name)
	if err != nil {
		var ge *whatsapp.GraphError
		if errors.Is(err, whatsapp.ErrNoHost) || errors.Is(err, whatsapp.ErrNoWABA) {
			s.fail(w, r, fmt.Errorf("%w: %w", errConflict, err))
			return
		}
		if errors.As(err, &ge) {
			s.Logger.Warn("whatsapp: submitting a template", "template", name, "err", err)
			s.fail(w, r, fmt.Errorf("%w: Meta refused the submission: %s", errBadRequest, ge.Message))
			return
		}
		s.fail(w, r, err)
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, "whatsapp.template.submit", name, map[string]any{"waba_id": wa.WABA, "status": st.Status, "meta_id": st.MetaID})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// checkWhatsAppTemplates asks Meta for the own number's templates now.
func (s *Server) checkWhatsAppTemplates(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	wa, err := s.ownNumber(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := wa.CheckTemplates(r.Context()); err != nil {
		var ge *whatsapp.GraphError
		if errors.As(err, &ge) {
			s.Logger.Warn("whatsapp: checking templates", "err", err)
			s.fail(w, r, fmt.Errorf("%w: Meta did not list the account's templates: %s. The token needs whatsapp_business_management", errBadRequest, ge.Message))
			return
		}
		s.fail(w, r, err)
		return
	}
	v, err := s.waTemplatesView(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
