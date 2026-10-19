package secrets

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// OAuth connection requests (decision 0034; migration 00174). Linking a
// connector by OAuth takes two HTTP requests ten minutes apart at most:
// the start, in the person's own session, and the provider's callback,
// which arrives unauthenticated with the state the start handed out. The
// row in between holds who asked for what, the state hashed, and the PKCE
// code verifier sealed under the tenant's key the way a connection's
// credentials are, so the exchange can be completed only from this
// database with this tenant's key, and only once.

// OAuthRequestTTL is how long a started request may be completed.
const OAuthRequestTTL = 10 * time.Minute

// oauthEnv is the vault environment the verifier is sealed in: reserved,
// like _identity, so no workflow or API can read it as a secret.
const oauthEnv = "_oauth"

// OAuthRequest is a started request, as the callback takes it.
type OAuthRequest struct {
	ID          uuid.UUID
	Tenant      uuid.UUID
	Person      string // the actor who started it; the connection is created as them
	Connector   string // id@major
	Environment string
	Name        string
	verifier    []byte
	verifierKey []byte
	kekVersion  int
}

// ErrOAuthRequest is returned by TakeOAuthRequest for a state that names
// no request, or one already used or expired. The three are not told
// apart: the state came from a browser.
var ErrOAuthRequest = errors.New("the connection request is unknown, already used or expired; start again")

func oauthBinding(id uuid.UUID) string { return "oauth_request:" + id.String() }

// randomToken is 32 random bytes, base64url without padding: 43
// characters of the PKCE verifier alphabet (RFC 7636 section 4.1).
func randomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// hashState is what a request is kept under: the state together with the
// browser binding, so a state carried back by any other browser (one the
// consent link was sent to) finds nothing.
func hashState(state, browser string) []byte {
	h := sha256.Sum256([]byte(state + "\x00" + browser))
	return h[:]
}

// BeginOAuthRequestTx records a request in a transaction scoped to tenant
// and returns the state for the provider, the browser binding (the caller
// sets it as a cookie only the starting browser holds) and the PKCE
// verifier (the caller derives the challenge; the verifier is not kept in
// the clear). The start is audited as connection.oauth_start.
func (v *Vault) BeginOAuthRequestTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, connector, env, name, by string) (state, browser, verifier string, err error) {
	if state, err = randomToken(); err != nil {
		return "", "", "", err
	}
	if browser, err = randomToken(); err != nil {
		return "", "", "", err
	}
	if verifier, err = randomToken(); err != nil {
		return "", "", "", err
	}
	id := uuid.Must(uuid.NewV7())
	ct, wdek, ver, err := v.encrypt(ctx, tx, tenant, id, oauthEnv, oauthBinding(id), []byte(verifier))
	if err != nil {
		return "", "", "", err
	}
	// Requests nobody completed are kept an hour past their expiry, for
	// an operator's look, then dropped with the next start.
	if _, err := tx.Exec(ctx, `DELETE FROM oauth_connection_requests WHERE tenant_id = $1 AND expires_at < now() - interval '1 hour'`, tenant); err != nil {
		return "", "", "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO oauth_connection_requests (id, tenant_id, state_hash, person, connector, environment, name, verifier, verifier_key, kek_version, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now() + $11)`,
		id, tenant, hashState(state, browser), by, connector, env, name, ct, wdek, ver, OAuthRequestTTL); err != nil {
		return "", "", "", err
	}
	return state, browser, verifier, audit(ctx, tx, tenant, by, "connection.oauth_start", connector+"/"+name, map[string]any{"request_id": id, "environment": env})
}

// TakeOAuthRequest finds the request a state names, for the browser that
// started it, and marks it used, in one statement, without a tenant
// scope: the callback has none yet. It returns ErrOAuthRequest when the
// state and binding name nothing usable.
func (v *Vault) TakeOAuthRequest(ctx context.Context, state, browser string) (*OAuthRequest, error) {
	if len(state) != 43 || len(browser) != 43 {
		return nil, ErrOAuthRequest
	}
	var q OAuthRequest
	err := v.Pool.QueryRow(ctx, `SELECT id, tenant_id, person, connector, environment, name, verifier, verifier_key, kek_version FROM taskiem_oauth_request_take($1)`, hashState(state, browser)).
		Scan(&q.ID, &q.Tenant, &q.Person, &q.Connector, &q.Environment, &q.Name, &q.verifier, &q.verifierKey, &q.kekVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrOAuthRequest
	}
	if err != nil {
		return nil, err
	}
	return &q, nil
}

// OAuthVerifierTx opens a taken request's PKCE verifier, in a transaction
// scoped to its tenant.
func (v *Vault) OAuthVerifierTx(ctx context.Context, tx pgx.Tx, q *OAuthRequest) (string, error) {
	raw, err := v.decrypt(ctx, tx, q.Tenant, q.ID, oauthEnv, oauthBinding(q.ID), 2, q.verifier, q.verifierKey, q.kekVersion)
	if err != nil {
		return "", fmt.Errorf("open the request's verifier: %w", err)
	}
	return string(raw), nil
}
