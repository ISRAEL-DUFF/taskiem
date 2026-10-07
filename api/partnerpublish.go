package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
)

// Four-eyes publishing in sub-tenants (docs/embedding.md#end-users-and-four-eyes):
// when a sub-tenant has four-eyes publishing on, an end user's publish is
// a request, which a person decides: a member of the sub-tenant with
// workflow.publish (the usual routes), or the partner, through its API
// key, deciding as the key's owner.

// listSubTenantPublishRequests lists a sub-tenant's pending publish requests.
func (s *Server) listSubTenantPublishRequests(w http.ResponseWriter, r *http.Request) {
	sub, err := subParam(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var out []publishRequest
	err = s.partnerTx(r, sub, "partner.subtenant.publish_requests.read", nil, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT q.workflow_id, w.name, q.version, q.requested_by, q.requested_at, q.status
			FROM publish_requests q JOIN workflows w ON w.id = q.workflow_id WHERE q.status = 'pending' ORDER BY q.requested_at`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[publishRequest])
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": nonNil(out)})
}

// decideSubTenantPublish approves or rejects a sub-tenant's publish
// request as the partner key's owner, who must be neither the requester
// nor the version's author.
func (s *Server) decideSubTenantPublish(approve bool) http.HandlerFunc {
	action := map[bool]string{true: "partner.subtenant.publish.approve", false: "partner.subtenant.publish.reject"}[approve]
	return func(w http.ResponseWriter, r *http.Request) {
		sub, err := subParam(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		wf, v, err := versionParams(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		var req struct {
			Comment string `json:"comment"`
		}
		_ = decodeOptional(r, &req)
		p := principalFrom(r.Context())
		var d publishDecision
		err = s.partnerTx(r, sub, action, map[string]any{"workflow": wf, "version": v}, func(tx pgx.Tx) error {
			var err error
			d, err = s.decidePublishTx(r, tx, sub, wf, v, p.Human(), approve, req.Comment, func(a string, detail map[string]any) error {
				detail["ip"] = clientIP(r)
				detail["via"] = "partner"
				raw, _ := json.Marshal(detail)
				_, err := tx.Exec(r.Context(), `SELECT taskiem_audit_append($1, $2, $3, $4, $5, $6)`, sub, p.ActorType(), p.Actor(), a, fmt.Sprintf("%s/%d", wf, v), raw)
				return err
			})
			return err
		})
		if err == nil && approve {
			// The version is the sub-tenant's: no Git proposal from here.
			d.published = false
		}
		s.answerPublishDecision(w, r, sub, wf, v, approve, d, err)
	}
}
