package api

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// A tenant's own WhatsApp number (spec 11.4). An admin holding
// secret.manage connects the tenant's WhatsApp Business number by hand:
// its phone number id, WhatsApp Business Account id, a system user's
// access token and, for a number on the tenant's own Meta app, that app's
// secret and a verify token. The token is checked against the Graph API,
// the credentials go in the tenant's vault, and from then on the
// tenant's people hear from, and write to, that number for this tenant.
// Embedded signup is a hook only (waEmbeddedSignup) until a Business
// Solution Provider is chosen (needs-people W3).

var graphID = regexp.MustCompile(`^[0-9]{5,32}$`)

type waNumberView struct {
	Connected     bool   `json:"connected"`
	PhoneNumberID string `json:"phone_number_id,omitempty"`
	WABAID        string `json:"waba_id,omitempty"`
	DisplayNumber string `json:"display_number,omitempty"`
	OwnApp        bool   `json:"own_app"`
	Source        string `json:"source,omitempty"`
	Status        string `json:"status,omitempty"`
	PublicMenu    bool   `json:"public_menu"`
	// Where to point the number's Meta app (paths on the edge's URL).
	WebhookPath   string             `json:"webhook_path,omitempty"`
	FlowsPath     string             `json:"flows_endpoint_path,omitempty"`
	Public        []waPublicWorkflow `json:"public_workflows"`
	FlowsEnabled  bool               `json:"flows_enabled"`
	SharedNumber  string             `json:"shared_number,omitempty"`
	CreatedAt     *time.Time         `json:"created_at,omitempty"`
	FlowsKeyError string             `json:"flows_key_error,omitempty"`
}

type waPublicWorkflow struct {
	WorkflowID uuid.UUID `json:"workflow_id"`
	Label      string    `json:"label"`
	Name       string    `json:"name,omitempty"`
}

func (s *Server) waNumberView(r *http.Request) (waNumberView, error) {
	p := principalFrom(r.Context())
	v := waNumberView{Public: []waPublicWorkflow{}}
	if s.WhatsApp != nil {
		v.SharedNumber, v.FlowsEnabled = s.WhatsApp.Config.DisplayNumber, s.WhatsApp.Config.FlowKey != nil
	}
	err := s.tx(r, func(tx pgx.Tx) error {
		var at time.Time
		err := tx.QueryRow(r.Context(), `SELECT phone_number_id, waba_id, display_number, own_app, source, status, public_menu, created_at
			FROM whatsapp_numbers WHERE tenant_id = $1`, p.TenantID).Scan(&v.PhoneNumberID, &v.WABAID, &v.DisplayNumber, &v.OwnApp, &v.Source, &v.Status, &v.PublicMenu, &at)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil {
			v.Connected, v.CreatedAt = true, &at
			v.WebhookPath, v.FlowsPath = "/channels/whatsapp", "/channels/whatsapp/flows"
			if v.OwnApp {
				v.WebhookPath, v.FlowsPath = "/channels/whatsapp/n/"+v.PhoneNumberID, "/channels/whatsapp/flows/"+v.PhoneNumberID
			}
		}
		rows, err := tx.Query(r.Context(), `SELECT p.workflow_id, p.label, COALESCE(w.name, '') FROM whatsapp_public_workflows p
			LEFT JOIN workflows w ON w.id = p.workflow_id WHERE p.tenant_id = $1 ORDER BY p.created_at, p.label`, p.TenantID)
		if err != nil {
			return err
		}
		v.Public, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (waPublicWorkflow, error) {
			var x waPublicWorkflow
			return x, r.Scan(&x.WorkflowID, &x.Label, &x.Name)
		})
		return err
	})
	if v.Public == nil {
		v.Public = []waPublicWorkflow{}
	}
	return v, err
}

// getWhatsAppNumber shows the tenant's own number, if any (never its
// credentials).
func (s *Server) getWhatsAppNumber(w http.ResponseWriter, r *http.Request) {
	v, err := s.waNumberView(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// putWhatsAppNumber connects (or replaces) the tenant's own number.
func (s *Server) putWhatsAppNumber(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if s.WhatsApp == nil {
		s.fail(w, r, errWhatsAppOff)
		return
	}
	var req struct {
		PhoneNumberID string `json:"phone_number_id"`
		WABAID        string `json:"waba_id"`
		DisplayNumber string `json:"display_number"`
		AccessToken   string `json:"access_token"`
		AppSecret     string `json:"app_secret"`
		VerifyToken   string `json:"verify_token"`
		OwnApp        *bool  `json:"own_app"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	ownApp := req.OwnApp == nil || *req.OwnApp
	req.AccessToken, req.AppSecret, req.VerifyToken = strings.TrimSpace(req.AccessToken), strings.TrimSpace(req.AppSecret), strings.TrimSpace(req.VerifyToken)
	switch {
	case !graphID.MatchString(req.PhoneNumberID):
		s.fail(w, r, fmt.Errorf("%w: phone_number_id is the number's id in WhatsApp Manager (digits)", errBadRequest))
		return
	case !graphID.MatchString(req.WABAID):
		s.fail(w, r, fmt.Errorf("%w: waba_id is the WhatsApp Business Account id (digits)", errBadRequest))
		return
	case req.AccessToken == "" || len(req.AccessToken) > 1024:
		s.fail(w, r, fmt.Errorf("%w: access_token is a system user's token with whatsapp_business_messaging", errBadRequest))
		return
	case ownApp && (len(req.AppSecret) < 16 || len(req.VerifyToken) < 16 || len(req.AppSecret) > 256 || len(req.VerifyToken) > 256):
		s.fail(w, r, fmt.Errorf("%w: a number on your own Meta app needs its app_secret and a verify_token of at least 16 characters", errBadRequest))
		return
	case req.PhoneNumberID == s.WhatsApp.Config.PhoneNumberID:
		s.fail(w, r, fmt.Errorf("%w: that is the shared number", errConflict))
		return
	}
	// One number, one organisation.
	var owner uuid.UUID
	err := s.Store.Pool.QueryRow(r.Context(), `SELECT tenant_id FROM taskiem_wa_number_route($1)`, req.PhoneNumberID).Scan(&owner)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.fail(w, r, err)
		return
	}
	if err == nil && owner != p.TenantID {
		s.fail(w, r, fmt.Errorf("%w: that number is connected to another organisation", errConflict))
		return
	}
	// The token must reach that number.
	c := &whatsapp.Client{BaseURL: s.WhatsApp.Client.BaseURL, PhoneNumberID: req.PhoneNumberID, Token: req.AccessToken,
		Egress: s.WhatsApp.Client.Egress, HTTP: s.WhatsApp.Client.HTTP}
	info, err := c.PhoneNumberInfo(r.Context())
	if err != nil {
		s.Logger.Warn("whatsapp: checking an own number", "err", err)
		s.fail(w, r, fmt.Errorf("%w: the Graph API refused the token for that phone number id; check both", errBadRequest))
		return
	}
	display := req.DisplayNumber
	if display == "" {
		display = info.DisplayPhoneNumber
	}
	display, err = whatsapp.Normalise(display, s.WhatsApp.Config.DefaultCountry)
	if err != nil {
		s.fail(w, r, fmt.Errorf("%w: display_number: %w", errBadRequest, err))
		return
	}
	// The Flows key goes to the number so its forms can reach the endpoint.
	keyErr := ""
	if k := s.WhatsApp.Config.FlowKey; k != nil {
		if err := c.SetFlowsPublicKey(r.Context(), k.PublicPEM()); err != nil {
			s.Logger.Warn("whatsapp: uploading the Flows key", "err", err)
			keyErr = "the Flows public key could not be uploaded to the number; WhatsApp forms will not work from it until it is"
		}
	}
	creds := map[string]string{whatsapp.OwnAccessToken: req.AccessToken}
	if ownApp {
		creds[whatsapp.OwnAppSecret], creds[whatsapp.OwnVerifyToken] = req.AppSecret, req.VerifyToken
	}
	for name, v := range creds {
		if _, err := s.Vault.Put(r.Context(), p.TenantID, whatsapp.OwnEnv, name, []byte(v), p.Actor()); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if !ownApp {
		for _, name := range []string{whatsapp.OwnAppSecret, whatsapp.OwnVerifyToken} {
			if err := s.Vault.Delete(r.Context(), p.TenantID, whatsapp.OwnEnv, name, p.Actor()); err != nil && !errors.Is(err, secrets.ErrNotFound) {
				s.fail(w, r, err)
				return
			}
		}
	}
	err = s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `DELETE FROM whatsapp_numbers WHERE tenant_id = $1 AND phone_number_id <> $2`, p.TenantID, req.PhoneNumberID); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO whatsapp_numbers (phone_number_id, tenant_id, waba_id, display_number, own_app, created_by)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (phone_number_id) DO UPDATE SET waba_id = EXCLUDED.waba_id, display_number = EXCLUDED.display_number, own_app = EXCLUDED.own_app,
			  status = 'active', updated_at = now()`, req.PhoneNumberID, p.TenantID, req.WABAID, display, ownApp, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "whatsapp.number.connect", req.PhoneNumberID, map[string]any{"waba_id": req.WABAID, "display_number": whatsapp.MaskNumber(display),
			"own_app": ownApp, "source": "manual"})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.WhatsApp.Forget()
	v, err := s.waNumberView(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v.FlowsKeyError = keyErr
	writeJSON(w, http.StatusOK, v)
}

// deleteWhatsAppNumber disconnects the tenant's own number: its people
// hear from the shared number again.
func (s *Server) deleteWhatsAppNumber(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var pnid string
	err := s.tx(r, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(), `DELETE FROM whatsapp_numbers WHERE tenant_id = $1 RETURNING phone_number_id`, p.TenantID).Scan(&pnid); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `DELETE FROM whatsapp_public_sessions WHERE tenant_id = $1`, p.TenantID); err != nil {
			return err
		}
		return auditTx(r, tx, "whatsapp.number.disconnect", pnid, nil)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, name := range []string{whatsapp.OwnAccessToken, whatsapp.OwnAppSecret, whatsapp.OwnVerifyToken} {
		if err := s.Vault.Delete(r.Context(), p.TenantID, whatsapp.OwnEnv, name, p.Actor()); err != nil && !errors.Is(err, secrets.ErrNotFound) {
			s.fail(w, r, err)
			return
		}
	}
	if s.WhatsApp != nil {
		s.WhatsApp.Forget()
	}
	w.WriteHeader(http.StatusNoContent)
}

// putWhatsAppPublicMenu turns the public menu on the tenant's own number
// on or off and sets the workflows it offers (workflow.publish: these run
// for anyone who writes to the number, without signing in).
func (s *Server) putWhatsAppPublicMenu(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var req struct {
		Enabled   bool               `json:"enabled"`
		Workflows []waPublicWorkflow `json:"workflows"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(req.Workflows) > 9 {
		s.fail(w, r, fmt.Errorf("%w: at most 9 workflows on a menu", errBadRequest))
		return
	}
	seen := map[uuid.UUID]bool{}
	for i, x := range req.Workflows {
		l := strings.TrimSpace(x.Label)
		if l == "" || len([]rune(l)) > 24 || seen[x.WorkflowID] {
			s.fail(w, r, fmt.Errorf("%w: each workflow once, with a label of 1 to 24 characters", errBadRequest))
			return
		}
		seen[x.WorkflowID] = true
		req.Workflows[i].Label = l
	}
	err := s.tx(r, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE whatsapp_numbers SET public_menu = $2, updated_at = now() WHERE tenant_id = $1`, p.TenantID, req.Enabled)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: connect the organisation's own WhatsApp number first; public menus are not offered on the shared number", errConflict)
		}
		if _, err := tx.Exec(r.Context(), `DELETE FROM whatsapp_public_workflows WHERE tenant_id = $1`, p.TenantID); err != nil {
			return err
		}
		for _, x := range req.Workflows {
			var ok bool
			if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM workflows WHERE id = $1)`, x.WorkflowID).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("%w: no workflow %s", errBadRequest, x.WorkflowID)
			}
			if _, err := tx.Exec(r.Context(), `INSERT INTO whatsapp_public_workflows (tenant_id, workflow_id, label, created_by) VALUES ($1, $2, $3, $4)`,
				p.TenantID, x.WorkflowID, x.Label, p.Actor()); err != nil {
				return err
			}
		}
		ids := make([]string, len(req.Workflows))
		for i, x := range req.Workflows {
			ids[i] = x.WorkflowID.String()
		}
		return auditTx(r, tx, "whatsapp.public_menu.update", "whatsapp", map[string]any{"enabled": req.Enabled, "workflows": ids})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.waNumberView(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
