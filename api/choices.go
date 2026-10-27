package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// listChoices answers GET /v1/connections/choices: what one of a
// connector's pickers offers on a connection (a Mailchimp audience, a
// Brevo list), so a template's parameter is chosen, not typed
// (no-technical-steps N8). It runs the picker's read action with the
// connection's credentials, refreshed and kept where its connector
// refreshes them.
func (s *Server) listChoices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id, picker, name := q.Get("connector"), q.Get("picker"), q.Get("connection")
	if name == "" {
		name = "main"
	}
	env, err := envParam(r, q.Get("environment"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	reg, err := s.Registry.For(r.Context(), p.TenantID.String())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	c, ok := connectorByID(reg, id)
	if !ok {
		s.fail(w, r, fmt.Errorf("%w: connector %q is not available", errBadRequest, id))
		return
	}
	pk, ok := c.Manifest.Pickers[picker]
	action := c.Actions[pk.Action]
	if !ok || action == nil {
		s.fail(w, r, fmt.Errorf("%w: %s has no picker %q", errBadRequest, c.Manifest.Name, picker))
		return
	}
	ctx := secrets.WithUse(r.Context(), secrets.Use{Kind: secrets.KindConnection, Purpose: secrets.PurposePickerList})
	creds, err := s.Vault.Credentials(ctx, p.TenantID, env, c.Manifest.ID, name)
	if errors.Is(err, secrets.ErrNotFound) {
		s.fail(w, r, fmt.Errorf("%w: there is no active %s connection named %q in %s", errConflict, c.Manifest.Name, name, env))
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	hc := s.egressGuard().Client(egress.Policy{Tenant: p.TenantID.String(), Hosts: c.Manifest.HostsFor(creds), Purpose: "picker:" + c.Manifest.ID}, 20*time.Second)
	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	resp, err := action.Execute(cctx, connector.Request{Input: map[string]any{}, Credentials: creds, HTTP: hc, Logger: s.Logger, Attempt: 1,
		Rotate: func(ctx context.Context, fn connector.RotateFunc) (map[string]string, error) {
			return s.Vault.RotateCredentials(ctx, p.TenantID, env, c.Manifest.ID, name, fn)
		}})
	if err != nil {
		s.Logger.Warn("picker: listing failed", "connector", c.Manifest.ID, "picker", picker, "err", err)
		s.fail(w, r, fmt.Errorf("%w: %s did not list them; check the connection on the Connections page", errConflict, c.Manifest.Name))
		return
	}
	out, _ := resp.Output.(map[string]any)
	choices, err := c.Manifest.Choices(picker, out)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"choices": choices})
}
