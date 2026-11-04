// Package areas holds which parts of Taskiem a business uses
// (docs/spec/business-areas.md): storefront, orders, stock, money, links
// and schools. An area that is off is hidden and refused; nothing in it is
// deleted, and turning it back on restores it.
//
// Five areas are rows of business_areas (migration 00305). The sixth,
// Schools, keeps its switch in school_settings (migration 00211) and is
// read from there. A business with no row for an area follows DefaultOn.
package areas

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/wd"
)

// Area is one switchable part of the product.
type Area string

const (
	Storefront Area = "storefront"
	Orders     Area = "orders"
	Stock      Area = "stock"
	Money      Area = "money"
	Links      Area = "links"
	Schools    Area = "schools"
)

// All lists the areas in the order the settings page shows them.
var All = []Area{Storefront, Orders, Stock, Money, Links, Schools}

// Info describes an area for the settings page, in plain words.
type Info struct {
	Area      Area   `json:"area"`
	Name      string `json:"name"`
	Covers    string `json:"covers"`
	DependsOn []Area `json:"depends_on"`
}

// dependsOn: turning an area on turns these on; one of them cannot be
// turned off while the area is on.
var dependsOn = map[Area][]Area{
	Storefront: {Orders},
	Schools:    {Money},
}

var info = map[Area]Info{
	Storefront: {Name: "Online shop", Covers: "Your shop page and its look, other pages, bookings, riders and deliveries."},
	Orders:     {Name: "Orders", Covers: "Orders from your shop, refunds and quotes, and orders to and from businesses you are linked with."},
	Stock:      {Name: "Stock", Covers: "What you have in stock, your suppliers and price lists."},
	Money:      {Name: "Money and invoices", Covers: "Invoices, money received, bills you owe and your money reports."},
	Links:      {Name: "Linked businesses", Covers: "Finding and linking with other businesses on Taskiem, and chatting with them."},
	Schools:    {Name: "Schools", Covers: "Students, families, classes, terms and school fees."},
}

// Catalogue lists every area with its plain words and what it needs.
func Catalogue() []Info {
	out := make([]Info, 0, len(All))
	for _, a := range All {
		i := info[a]
		i.Area = a
		i.DependsOn = slices.Clone(dependsOn[a])
		if i.DependsOn == nil {
			i.DependsOn = []Area{}
		}
		out = append(out, i)
	}
	return out
}

// Valid reports whether s names an area.
func Valid(s string) (Area, bool) {
	a := Area(s)
	return a, slices.Contains(All, a)
}

// DefaultOn is what a business with no row has. Everything but Schools is
// on until the signup question (plan phase 4) makes new businesses start
// with their areas off; that change writes rows for the businesses made
// before it, so none of them loses an area.
func DefaultOn(a Area) bool { return a != Schools }

// State says which areas are on.
type State map[Area]bool

// On reports whether the area is on.
func (s State) On(a Area) bool { return s[a] }

// BlockedError is a change refused because it would leave an area on
// without something it needs.
type BlockedError struct{ Area, Needed Area }

func (e *BlockedError) Error() string {
	return fmt.Sprintf("%s needs %s: turn %s off first, or keep %s on", info[e.Area].Name, info[e.Needed].Name, info[e.Area].Name, info[e.Needed].Name)
}

// ErrOff is an area that is off for the business.
var ErrOff = errors.New("areas: this part of Taskiem is not turned on for the business")

// Apply returns the state after the changes asked for. Turning an area on
// turns on what it needs; turning one off is refused (BlockedError) while
// an area that needs it stays on. Changes are applied together, so turning
// two areas off at once is fine whatever the order.
func Apply(cur State, want map[Area]bool) (State, error) {
	next := State{}
	for _, a := range All {
		next[a] = cur[a]
	}
	for a, on := range want {
		if on {
			next[a] = true
			for _, d := range dependsOn[a] {
				next[d] = true
			}
		}
	}
	for a, on := range want {
		if !on {
			next[a] = false
		}
	}
	for _, a := range All {
		if !next[a] {
			continue
		}
		for _, d := range dependsOn[a] {
			if !next[d] {
				return nil, &BlockedError{Area: a, Needed: d}
			}
		}
	}
	return next, nil
}

// Load reads the business's areas: the rows it has, Schools from its own
// switch, and the default for the rest. tx is the business's scope.
func Load(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (State, error) {
	st := State{}
	for _, a := range All {
		st[a] = DefaultOn(a)
	}
	rows, err := tx.Query(ctx, `SELECT area, enabled FROM business_areas WHERE tenant_id = $1`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var area string
		var on bool
		if err := rows.Scan(&area, &on); err != nil {
			return nil, err
		}
		if a, ok := Valid(area); ok {
			st[a] = on
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	var schools bool
	switch err := tx.QueryRow(ctx, `SELECT enabled FROM school_settings WHERE tenant_id = $1`, tenant).Scan(&schools); {
	case errors.Is(err, pgx.ErrNoRows):
		schools = false
	case err != nil:
		return nil, err
	}
	st[Schools] = schools
	return st, nil
}

// Enabled reports whether one stored area is on for the business. It is
// the check made on a request, so it reads one row. The Schools area is
// refused by the schools package itself.
func Enabled(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, a Area) (bool, error) {
	if a == Schools {
		var on bool
		err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT enabled FROM school_settings WHERE tenant_id = $1), false)`, tenant).Scan(&on)
		return on, err
	}
	var on bool
	err := tx.QueryRow(ctx, `SELECT enabled FROM business_areas WHERE tenant_id = $1 AND area = $2`, tenant, string(a)).Scan(&on)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultOn(a), nil
	}
	return on, err
}

// Save writes the stored areas that changed (not Schools, whose switch is
// school_settings). It reports which areas changed.
func Save(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, before, after State, by string) ([]Area, error) {
	var changed []Area
	for _, a := range All {
		if a == Schools || before[a] == after[a] {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO business_areas (tenant_id, area, enabled, updated_by) VALUES ($1, $2, $3, $4)
			ON CONFLICT (tenant_id, area) DO UPDATE SET enabled = $3, updated_by = $4, updated_at = now()`, tenant, string(a), after[a], by); err != nil {
			return nil, err
		}
		changed = append(changed, a)
	}
	return changed, nil
}

// connectorAreas say which area a connector's steps belong to. A step of
// one of them is refused when the area is off (the worker checks), and a
// workflow that has one is listed when the area is about to be turned off.
var connectorAreas = map[string]Area{
	"storefront": Storefront,
	"riders":     Storefront,
	"stock":      Stock,
	"invoices":   Money,
	"bills":      Money,
	"link":       Links,
	"schools":    Schools,
}

// triggerAreas say which area a workflow trigger's events come from.
var triggerAreas = map[string]Area{
	"storefront": Storefront,
	"invoice":    Money,
	"link":       Links,
}

// ConnectorArea names the area a connector reference ("stock@1") belongs
// to, if any.
func ConnectorArea(ref string) (Area, bool) {
	id, _, _ := strings.Cut(ref, "@")
	a, ok := connectorAreas[id]
	return a, ok
}

// TriggerArea names the area a trigger type belongs to, if any.
func TriggerArea(typ string) (Area, bool) {
	a, ok := triggerAreas[typ]
	return a, ok
}

// OffSentence is what a step, a message or a page says when its area is off.
func OffSentence(a Area) string {
	return info[a].Name + " is not turned on for this business. An owner turns it on in Settings."
}

// UsedBy is a published workflow step that belongs to an area.
type UsedBy struct {
	Workflow    string `json:"workflow"`
	Step        string `json:"step"`
	Environment string `json:"environment"`
}

// Affected lists the deployed workflow steps and triggers that use the
// area, so turning it off can warn first. Only what is deployed counts: a
// draft or an older version does nothing until it is.
func Affected(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, a Area) ([]UsedBy, error) {
	rows, err := tx.Query(ctx, `SELECT w.name, d.environment, v.definition
		  FROM deployments d
		  JOIN workflows w ON w.id = d.workflow_id
		  JOIN workflow_versions v ON v.workflow_id = d.workflow_id AND v.version = d.version
		 WHERE d.tenant_id = $1
		 ORDER BY w.name, d.environment`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UsedBy{}
	for rows.Next() {
		var name, env string
		var doc []byte
		if err := rows.Scan(&name, &env, &doc); err != nil {
			return nil, err
		}
		def, err := wd.Load(doc)
		if err != nil {
			continue // a definition that no longer loads does nothing to warn about
		}
		if t, ok := TriggerArea(def.Trigger.Type); ok && t == a {
			out = append(out, UsedBy{Workflow: name, Step: "when it starts", Environment: env})
		}
		def.Walk(func(s *wd.Step) {
			if s.Connector == "" {
				return
			}
			if c, ok := ConnectorArea(s.Connector); ok && c == a {
				label := s.Name
				if label == "" {
					label = s.ID
				}
				out = append(out, UsedBy{Workflow: name, Step: label, Environment: env})
			}
		})
	}
	return out, rows.Err()
}
