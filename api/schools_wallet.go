package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/schools"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// The school's wallet (docs/spec/iswallet-account-numbers.md I3): instead
// of pasting a wallet id, an owner creates the school's wallet on the
// issuing connection (iswallet's create_wallet), and the students' numbers
// pay into it. The director's BVN and the business email are sent to the
// rail once and kept nowhere in Taskiem; the audit says a wallet was made,
// never with what.

// walletOwnerTypes are the kinds of owner a school's wallet may have:
// holding other people's money (fees) in its own segregated account, or
// only its own, pooled (iswallet, 27 October 2026).
var walletOwnerTypes = []string{"client_end_user_business", "client_end_business"}

type schoolWalletReq struct {
	// Connection is the issuing connection, as connector/name ("iswallet/main").
	Connection    string `json:"connection"`
	OwnerType     string `json:"owner_type"`
	BusinessEmail string `json:"business_email"`
	BVN           string `json:"bvn"`
	BusinessName  string `json:"business_name"`
}

// createSchoolWallet answers POST /v1/settings/schools/wallet (owners):
// the school's wallet is created on the connection, at most once for it
// (the key is the tenant and connection, and iswallet keeps one wallet per
// owner_ref), and becomes the wallet students' numbers pay into.
func (s *Server) createSchoolWallet(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if !slices.Contains(p.Roles, "owner") {
		writeErr(w, http.StatusForbidden, "only an owner creates the school's wallet")
		return
	}
	var req schoolWalletReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	req.BVN, req.BusinessEmail, req.BusinessName = strings.TrimSpace(req.BVN), strings.TrimSpace(req.BusinessEmail), strings.TrimSpace(req.BusinessName)
	if req.OwnerType == "" {
		req.OwnerType = "client_end_user_business"
	}
	switch {
	case !slices.Contains(walletOwnerTypes, req.OwnerType):
		s.fail(w, r, fmt.Errorf("%w: owner_type is client_end_user_business (fees, in the school's own account) or client_end_business (only its own money, pooled)", errBadRequest))
		return
	case !strings.Contains(req.BusinessEmail, "@"):
		s.fail(w, r, fmt.Errorf("%w: give the school's business email", errBadRequest))
		return
	case len(req.BVN) != 11 || strings.Trim(req.BVN, "0123456789") != "":
		s.fail(w, r, fmt.Errorf("%w: a BVN is 11 digits: the director's", errBadRequest))
		return
	}
	ref, name, ok := strings.Cut(req.Connection, "/")
	if !ok || ref == "" || name == "" {
		s.fail(w, r, fmt.Errorf("%w: name the connection as service/name, such as iswallet/main", errBadRequest))
		return
	}
	reg, err := s.Registry.For(r.Context(), p.TenantID.String())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	c, ok := connectorByID(reg, ref)
	if !ok || c.Actions["create_wallet"] == nil {
		s.fail(w, r, fmt.Errorf("%w: %s cannot create wallets", errBadRequest, ref))
		return
	}
	var school string
	if err := s.tx(r, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(), `SELECT COALESCE(NULLIF(name, ''), 'the school') FROM tenants WHERE id = $1`, p.TenantID).Scan(&school); err != nil {
			return err
		}
		var on bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM school_settings WHERE tenant_id = $1 AND enabled)`, p.TenantID).Scan(&on); err != nil {
			return err
		}
		if !on {
			return fmt.Errorf("%w: turn the Schools area on first", errConflict)
		}
		return nil
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.BusinessName == "" {
		req.BusinessName = school
	}
	in := map[string]any{"owner_type": req.OwnerType, "owner_ref": "tenant:" + p.TenantID.String(), "currency": "NGN",
		"business_email": req.BusinessEmail, "bvn": req.BVN, "business_name": req.BusinessName}
	out, err := s.createWallet(r.Context(), p.TenantID, c, name, in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	wallet, _ := out["wallet_id"].(string)
	var issuing schools.Issuing
	err = s.tx(r, func(tx pgx.Tx) error {
		was, err := schools.GetIssuing(r.Context(), tx, p.TenantID)
		if err != nil {
			return err
		}
		issuing = schools.Issuing{Channel: c.Manifest.ID + "/" + name, Account: wallet, SiblingsShareCredit: was.SiblingsShareCredit}
		if _, err := schools.SetIssuing(r.Context(), tx, p.TenantID, issuing, p.Actor()); err != nil {
			return err
		}
		return auditTx(r, tx, "settings.schools.wallet_created", p.TenantID.String(), map[string]any{"connection": issuing.Channel, "wallet_id": wallet,
			"owner_type": req.OwnerType, "request_id": out["request_id"], "before": was})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"wallet_id": wallet, "issuing": issuing})
}

// createWallet runs a connector's create_wallet on a prod connection, its
// key derived from the tenant and connection, so asking again (a lost
// answer, a second press) makes no second wallet.
func (s *Server) createWallet(ctx context.Context, tenant uuid.UUID, c *connector.Connector, name string, in map[string]any) (map[string]any, error) {
	spec := c.Manifest.Actions["create_wallet"]
	keySpec := connector.DefaultKeySpec
	if spec.Idempotency != nil {
		keySpec = *spec.Idempotency
	}
	key, err := keySpec.Key(effects.KeyInput{TenantID: tenant.String(), Seed: "school-wallet:" + c.Manifest.ID + "/" + name, StepID: "create_wallet"})
	if err != nil {
		return nil, err
	}
	use := secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: "schools.create_wallet"})
	creds, err := s.Vault.Credentials(use, tenant, "prod", c.Manifest.ID, name)
	if err != nil {
		return nil, fmt.Errorf("%w: no active %s connection named %s in prod", errBadRequest, c.Manifest.Name, name)
	}
	g := s.Egress
	if g == nil {
		g = &egress.Guard{Logger: s.Logger}
	}
	hc := g.Client(egress.Policy{Tenant: tenant.String(), Hosts: c.Manifest.HostsFor(creds), Purpose: "connector:" + c.Manifest.ID}, 20*time.Second)
	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	resp, err := c.Actions["create_wallet"].Execute(cctx, connector.Request{Input: in, Credentials: creds, HTTP: hc, Logger: s.Logger, Attempt: 1, IdempotencyKey: key})
	if err != nil {
		if effects.Classify(err) == effects.KindFatal {
			return nil, fmt.Errorf("%w: %s did not create the wallet: %s", errBadRequest, c.Manifest.Name, plainConnectorError(err))
		}
		return nil, fmt.Errorf("%s did not answer whether it created the wallet; asking again is safe: %s", c.Manifest.Name, plainConnectorError(err))
	}
	out, _ := resp.Output.(map[string]any)
	if w, _ := out["wallet_id"].(string); w == "" {
		return nil, fmt.Errorf("%s answered without a wallet", c.Manifest.Name)
	}
	return out, nil
}
