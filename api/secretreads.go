package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/secrets"
)

// listSecretReads serves GET /v1/secrets/reads: every recorded decryption
// of a secret or connection credential for use (spec 14.1), newest first.
// Filters: name, connection (id), run, env, kind, since, until (RFC 3339 or
// a date), limit (default 100, at most 1000). A key limited to one
// environment sees only that environment's reads; platform reads (Git,
// alert channels, identity) are in reserved environments and visible only
// to tenant-wide callers.
func (s *Server) listSecretReads(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := secrets.ReadFilter{Name: q.Get("name"), Kind: q.Get("kind")}
	p := principalFrom(r.Context())
	if v := q.Get("env"); v != "" || p.Environment != "" {
		env, err := envParam(r, v)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		f.Environment = env
	}
	for _, x := range []struct {
		key string
		dst *uuid.UUID
	}{{"connection", &f.Connection}, {"run", &f.Run}} {
		if v := q.Get(x.key); v != "" {
			id, err := uuid.Parse(v)
			if err != nil {
				s.fail(w, r, fmt.Errorf("%w: %s is an id", errBadRequest, x.key))
				return
			}
			*x.dst = id
		}
	}
	for _, x := range []struct {
		key string
		dst *time.Time
	}{{"since", &f.Since}, {"until", &f.Until}} {
		if v := q.Get(x.key); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				if t, err = time.Parse("2006-01-02", v); err != nil {
					s.fail(w, r, fmt.Errorf("%w: %s is an RFC 3339 time or a date (2006-01-02)", errBadRequest, x.key))
					return
				}
			}
			*x.dst = t
		}
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			s.fail(w, r, fmt.Errorf("%w: limit is a positive number", errBadRequest))
			return
		}
		f.Limit = n
	}
	var out []secrets.Read
	err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = secrets.ListReads(r.Context(), tx, f)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reads": nonNil(out)})
}

// secretUseReport: how often each secret and connection was decrypted for
// use, per day; when each was last used; which were not used in the period;
// and the hourly digests in the audit chain checked against the reads.
func secretUseReport(ctx context.Context, tx pgx.Tx, _ *Server, tenant uuid.UUID, from, to time.Time) (*report, error) {
	rows, err := tx.Query(ctx, `SELECT to_char(at AT TIME ZONE 'UTC', 'YYYY-MM-DD'), kind, environment, name, COALESCE(connection_id::text, ''), COALESCE(connector, ''),
			count(*), max(at), array_agg(DISTINCT purpose ORDER BY purpose)
		FROM secret_reads WHERE at >= $1 AND at < $2
		GROUP BY 1, 2, 3, 4, 5, 6 ORDER BY 1, 2, 3, 4, 5`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rep := &report{Columns: []string{"day", "kind", "environment", "name", "connection_id", "connector", "reads", "last_read_at", "purposes"}}
	byKind := map[string]int64{}
	var total int64
	for rows.Next() {
		var day, kind, env, name, conn, connector string
		var n int64
		var last time.Time
		var purposes []string
		if err := rows.Scan(&day, &kind, &env, &name, &conn, &connector, &n, &last, &purposes); err != nil {
			return nil, err
		}
		rep.Rows = append(rep.Rows, map[string]any{"day": day, "kind": kind, "environment": env, "name": name, "connection_id": conn,
			"connector": connector, "reads": n, "last_read_at": last, "purposes": purposes})
		byKind[kind] += n
		total += n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	// Workflow secrets and connections with no read in the period, with
	// their last use ever (null: never used).
	type unused struct {
		Kind         string     `json:"kind"`
		Environment  string     `json:"environment"`
		Name         string     `json:"name"`
		ConnectionID string     `json:"connection_id,omitempty"`
		Connector    string     `json:"connector,omitempty"`
		LastUsedAt   *time.Time `json:"last_used_at"`
	}
	var idle []unused
	u, err := tx.Query(ctx, `SELECT 'secret', s.environment, s.name, '', '',
			(SELECT max(r.at) FROM secret_reads r WHERE r.tenant_id = s.tenant_id AND r.connection_id IS NULL AND r.environment = s.environment AND r.name = s.name)
		FROM secrets s WHERE s.name IS NOT NULL AND s.environment NOT LIKE '\_%'
		  AND NOT EXISTS (SELECT 1 FROM secret_reads r WHERE r.tenant_id = s.tenant_id AND r.connection_id IS NULL AND r.environment = s.environment
		                    AND r.name = s.name AND r.at >= $1 AND r.at < $2)
		UNION ALL
		SELECT 'connection', c.environment, c.name, c.id::text, c.connector,
			(SELECT max(r.at) FROM secret_reads r WHERE r.tenant_id = c.tenant_id AND r.connection_id = c.id)
		FROM connections c WHERE c.secret_ref IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM secret_reads r WHERE r.tenant_id = c.tenant_id AND r.connection_id = c.id AND r.at >= $1 AND r.at < $2)
		ORDER BY 1, 2, 3`, from, to)
	if err != nil {
		return nil, err
	}
	defer u.Close()
	for u.Next() {
		var x unused
		if err := u.Scan(&x.Kind, &x.Environment, &x.Name, &x.ConnectionID, &x.Connector, &x.LastUsedAt); err != nil {
			return nil, err
		}
		idle = append(idle, x)
	}
	if err := u.Err(); err != nil {
		return nil, err
	}
	u.Close()

	checks, err := secrets.VerifyDigests(ctx, tx, tenant, from, to, 0)
	if err != nil {
		return nil, err
	}
	status := map[string]int{}
	var problems []secrets.DigestCheck
	for _, c := range checks {
		status[c.Status]++
		if c.Status == "mismatch" {
			problems = append(problems, c)
		}
	}
	rep.Summary = map[string]any{
		"reads": total, "by_kind": byKind, "unused": nonNil(idle),
		"digests": status, "digests_intact": len(problems) == 0, "digest_mismatches": nonNil(problems),
	}
	return rep, nil
}
