// Package ops is the operator identity behind the operator console
// (decision 0027, docs/operator-console.md): Taskiem's own staff, a
// platform-level identity apart from every tenant. Accounts are managed
// from the CLI only (taskiem operators); the API signs operators in with a
// passkey or single sign-on and keeps their sessions apart from tenants'.
// Every operator action is appended to the platform audit chain.
package ops

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
)

// PlatformChain is the audit chain id of platform-level actions: the
// ordinary hash chain (anchored, exported and verified like a tenant's)
// under an id no tenant can have (migration 00142).
var PlatformChain = uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")

// ErrNotFound is an operator that does not exist (or is not active).
var ErrNotFound = errors.New("no such operator")

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// Operator is an operator account as the CLI lists it.
type Operator struct {
	ID         uuid.UUID  `json:"id"`
	Email      string     `json:"email"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	Passkeys   int64      `json:"passkeys"`
	SSO        bool       `json:"sso"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSignIn *time.Time `json:"last_sign_in"`
}

// HashToken is how session and enrolment tokens are kept.
func HashToken(tok string) []byte {
	h := sha256.Sum256([]byte(tok))
	return h[:]
}

// NewToken is a 256-bit random token, URL-safe.
func NewToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func notFound(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "P0002" {
		return fmt.Errorf("%w: %s", ErrNotFound, pe.Message)
	}
	return err
}

// Add creates an operator, or reactivates one; audited in the platform
// chain as by.
func Add(ctx context.Context, pool *pgxpool.Pool, email, name, by string) (uuid.UUID, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !emailRe.MatchString(email) || len(email) > 254 {
		return uuid.Nil, fmt.Errorf("%q is not an email address", email)
	}
	if len(name) > 200 {
		return uuid.Nil, errors.New("a name has at most 200 characters")
	}
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT taskiem_ops_operator_add($1, $2, $3)`, email, strings.TrimSpace(name), by).Scan(&id); err != nil {
		return uuid.Nil, err
	}
	return id, Audit(ctx, pool, by, "operator.add", email, map[string]any{"operator": id})
}

// Disable stops an operator: their sessions and unused enrolment links end
// at once.
func Disable(ctx context.Context, pool *pgxpool.Pool, email, by string) error {
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT taskiem_ops_operator_disable($1, $2)`, email, by).Scan(&id); err != nil {
		return notFound(err)
	}
	return Audit(ctx, pool, by, "operator.disable", strings.ToLower(email), map[string]any{"operator": id})
}

// Reset removes an operator's passkeys (a lost device) and ends their
// sessions; it returns how many passkeys were removed.
func Reset(ctx context.Context, pool *pgxpool.Pool, email, by string) (int64, error) {
	var n int64
	if err := pool.QueryRow(ctx, `SELECT taskiem_ops_operator_reset($1)`, email).Scan(&n); err != nil {
		return 0, notFound(err)
	}
	return n, Audit(ctx, pool, by, "operator.passkeys.reset", strings.ToLower(email), map[string]any{"removed": n})
}

// List returns every operator.
func List(ctx context.Context, pool *pgxpool.Pool) ([]Operator, error) {
	rows, err := pool.Query(ctx, `SELECT id, email, name, status, passkeys, sso, created_by, created_at, last_sign_in FROM taskiem_ops_operators()`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Operator])
}

// EnrolTTL is how long an enrolment link lasts by default.
const EnrolTTL = 24 * time.Hour

// Enrol issues a one-time link for an active operator to add a passkey,
// and returns it (publicURL/ops/enrol#token). The token is kept hashed.
func Enrol(ctx context.Context, pool *pgxpool.Pool, email, publicURL string, ttl time.Duration, by string) (string, error) {
	if ttl <= 0 {
		ttl = EnrolTTL
	}
	tok := NewToken()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT taskiem_ops_enrol_issue($1, $2, make_interval(secs => $3), $4)`, email, HashToken(tok), ttl.Seconds(), by).Scan(&id); err != nil {
		return "", notFound(err)
	}
	if err := Audit(ctx, pool, by, "operator.enrol.issue", strings.ToLower(email), map[string]any{"operator": id, "expires_in": ttl.String()}); err != nil {
		return "", err
	}
	return EnrolURL(publicURL, tok), nil
}

// EnrolURL is where an enrolment token is used. The token goes in the
// fragment, which browsers never send to a server or in a Referer.
func EnrolURL(publicURL, tok string) string {
	base := strings.TrimRight(publicURL, "/")
	if u, err := url.Parse(base); err != nil || u.Host == "" {
		base = "https://<TASKIEM_PUBLIC_URL>"
	}
	return base + "/ops/enrol#" + tok
}

// Audit appends an operator action to the platform chain. actor is
// "operator:<email>" for the console, "cli:<user>" for the CLI.
func Audit(ctx context.Context, pool *pgxpool.Pool, actor, action, target string, detail map[string]any) error {
	return db.InTenantTx(ctx, pool, []uuid.UUID{PlatformChain}, func(tx pgx.Tx) error {
		return AuditTx(ctx, tx, actor, action, target, detail)
	})
}

// AuditTx is Audit inside a transaction scoped to PlatformChain.
func AuditTx(ctx context.Context, tx pgx.Tx, actor, action, target string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'platform_admin', $2, $3, $4, $5)`, PlatformChain, actor, action, target, raw)
	return err
}
