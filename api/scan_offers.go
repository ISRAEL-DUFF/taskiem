package api

import (
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/wd"
)

// A code scanned from the phone's home screen, with no workflow open
// (docs/spec/device-fields.md §7, D2): the app reads the code, then asks
// which of the business's workflows take it. A workflow takes a scan when
// it is started by hand and has a scan among its top-level inputs whose
// formats include the code's; the person is offered those they may start
// (the workflow's roles), tools first. Nothing starts here: the app opens
// the tool's form, or the workflow's start, filled in with the code.

// scanOffer is one workflow a scanned code can start.
type scanOffer struct {
	// Kind is tool (a member's tool, opened by its command) or workflow.
	Kind        string    `json:"kind"`
	Workflow    uuid.UUID `json:"workflow_id"`
	Version     int       `json:"version"`
	Environment string    `json:"environment"`
	Name        string    `json:"name"`
	About       string    `json:"about,omitempty"`
	Command     string    `json:"command,omitempty"`
	Icon        string    `json:"icon,omitempty"`
	// Input is the scan input the code fills; Why its why.
	Input string `json:"input"`
	Why   string `json:"why,omitempty"`
	// Asks are the other inputs it requires, by name: none, and the start
	// needs nothing more than the code.
	Asks []string `json:"asks"`
}

// maxScanOffers is how many workflows a code is offered: a phone's sheet.
const maxScanOffers = 20

// scanOffers is GET /v1/scan/offers?format=ean_13: the workflows the
// caller may start that take a code of that format (format left out: a
// code typed). The code itself is not sent: what is offered depends only
// on its format.
func (s *Server) scanOffers(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	env, err := environment(p, r.URL.Query().Get("environment"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	format := strings.TrimSpace(r.URL.Query().Get("format"))
	if format != "" && !slices.Contains(wd.ScanFormats, format) {
		writeErr(w, http.StatusBadRequest, "format is one of "+strings.Join(wd.ScanFormats, ", ")+", or left out for a code typed")
		return
	}
	out := []scanOffer{}
	err = s.tx(r, func(tx pgx.Tx) error {
		// Deployed, started by hand, with a scan among its inputs.
		rows, err := tx.Query(r.Context(), `SELECT d.workflow_id, d.version, v.definition,
			  COALESCE(t.command, ''), COALESCE(t.name, ''), COALESCE(t.about, ''), COALESCE(t.icon, ''), COALESCE(t.audience, '')
			FROM deployments d
			JOIN workflow_versions v ON v.workflow_id = d.workflow_id AND v.version = d.version
			LEFT JOIN workflow_tools t ON t.workflow_id = d.workflow_id AND t.environment = d.environment
			WHERE d.environment = $1 AND v.definition #>> '{trigger,type}' = 'manual'
			  AND jsonb_path_exists(v.definition, '$.inputs.schema.properties.* ? (@.type == "scan")')
			ORDER BY t.command IS NULL, COALESCE(t.name, v.definition ->> 'name')`, env)
		if err != nil {
			return err
		}
		type found struct {
			wf                                   uuid.UUID
			version                              int
			def                                  []byte
			command, name, about, icon, audience string
		}
		all, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (found, error) {
			var f found
			err := row.Scan(&f.wf, &f.version, &f.def, &f.command, &f.name, &f.about, &f.icon, &f.audience)
			return f, err
		})
		if err != nil {
			return err
		}
		for _, f := range all {
			if len(out) == maxScanOffers {
				break
			}
			d, err := s.definitionFor(f.wf, f.version, f.def)
			if err != nil {
				continue
			}
			name, prop, ok := wd.ScanInput(d)
			if !ok || !wd.ScanTakes(prop, format) || !mayStart(p, d.Roles) {
				continue
			}
			o := scanOffer{Kind: "workflow", Workflow: f.wf, Version: f.version, Environment: env, Name: d.Name, Input: name, Asks: []string{}}
			o.Why, _ = prop["why"].(string)
			if f.command != "" {
				if f.audience != wd.ToolForMembers {
					continue
				}
				o.Kind, o.Command, o.Name, o.About, o.Icon = "tool", f.command, f.name, f.about, f.icon
			}
			raw, _ := d.InputsSchema()
			schema, _ := raw.(map[string]any)
			if req, ok := schema["required"].([]any); ok {
				for _, x := range req {
					if k, _ := x.(string); k != "" && k != name {
						o.Asks = append(o.Asks, k)
					}
				}
			}
			out = append(out, o)
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"offers": out, "format": format})
}
