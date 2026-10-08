package api

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

// Contract drift (spec 6.4): where connectors' live outputs departed from
// their declared schemas, for the people who own the workflows using them.

type driftRow struct {
	Connector      string     `json:"connector"`
	Version        string     `json:"version"`
	Action         string     `json:"action"`
	Path           string     `json:"path"`
	Kind           string     `json:"kind"`
	Expected       string     `json:"expected"`
	Observed       string     `json:"observed"`
	FirstSeen      time.Time  `json:"first_seen"`
	LastSeen       time.Time  `json:"last_seen"`
	Occurrences    int64      `json:"occurrences"`
	LastRunID      *string    `json:"last_run_id,omitempty"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	AcknowledgedBy *string    `json:"acknowledged_by,omitempty"`
}

func (s *Server) listDrift(w http.ResponseWriter, r *http.Request) {
	var out []driftRow
	err := s.tx(r, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT connector, version, action, path, kind, expected, observed, first_seen, last_seen,
			occurrences, last_run_id::text, acknowledged_at, acknowledged_by FROM connector_drift
			ORDER BY acknowledged_at IS NOT NULL, last_seen DESC LIMIT 500`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var d driftRow
			if err := rows.Scan(&d.Connector, &d.Version, &d.Action, &d.Path, &d.Kind, &d.Expected, &d.Observed, &d.FirstSeen, &d.LastSeen,
				&d.Occurrences, &d.LastRunID, &d.AcknowledgedAt, &d.AcknowledgedBy); err != nil {
				return err
			}
			out = append(out, d)
		}
		return rows.Err()
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"drift": nonNil(out)})
}

// acknowledgeDrift records that someone has looked at a finding: the
// workflows handle it, or a fix is under way. It stays listed.
func (s *Server) acknowledgeDrift(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Connector string `json:"connector"`
		Version   string `json:"version"`
		Action    string `json:"action"`
		Path      string `json:"path"`
		Kind      string `json:"kind"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	err := s.tx(r, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE connector_drift SET acknowledged_at = now(), acknowledged_by = $6
			WHERE connector = $1 AND version = $2 AND action = $3 AND path = $4 AND kind = $5 AND acknowledged_at IS NULL`,
			req.Connector, req.Version, req.Action, req.Path, req.Kind, p.Actor())
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return auditTx(r, tx, "connector.drift_acknowledge", req.Connector+" "+req.Action+" "+req.Path, map[string]any{"version": req.Version, "kind": req.Kind})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
