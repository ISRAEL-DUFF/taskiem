// Package status is the public status page (Phase 4, P4-4; decision 0023,
// docs/reliability.md): components and their state, driven by incidents and
// maintenance windows operators declare, and optionally by the synthetic
// canary. Everything here is public: no tenant data, and the operators'
// names stay in the database (the audit trail), never on the page.
package status

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Component states, least to most severe.
const (
	Operational   = "operational"
	Maintenance   = "maintenance"
	Degraded      = "degraded"
	PartialOutage = "partial_outage"
	MajorOutage   = "major_outage"
)

var severity = map[string]int{Operational: 0, Maintenance: 1, Degraded: 2, PartialOutage: 3, MajorOutage: 4}

// Worse is the more severe of two component states.
func Worse(a, b string) string {
	if severity[b] > severity[a] {
		return b
	}
	return a
}

// Components are the platform's own parts, in display order. Incidents may
// also name integrations ("integration:paystack"), shown while affected.
var Components = []struct{ ID, Name, Description string }{
	{"api", "API and web app", "Signing in, building and publishing workflows, the REST API"},
	{"webhooks", "Webhook ingest", "Receiving webhooks and connector events (/hooks)"},
	{"runs", "Workflow runs", "Starting runs and executing their steps"},
	{"scheduler", "Schedules and timers", "Scheduled triggers, waits and timeouts"},
}

var integrationRe = regexp.MustCompile(`^integration:[a-z][a-z0-9_]{0,39}$`)

// ValidComponent reports whether c can be named by an incident.
func ValidComponent(c string) bool {
	for _, k := range Components {
		if k.ID == c {
			return true
		}
	}
	return integrationRe.MatchString(c)
}

// Kinds, statuses and impacts operators declare.
var (
	incidentStatuses    = []string{"investigating", "identified", "monitoring", "resolved"}
	maintenanceStatuses = []string{"scheduled", "in_progress", "completed"}
	impacts             = []string{"none", Degraded, PartialOutage, MajorOutage, Maintenance}
)

// ErrInvalid is a declaration the page cannot take.
var ErrInvalid = errors.New("invalid")

// ErrNotFound is an incident that does not exist.
var ErrNotFound = errors.New("no such incident")

// Declaration opens an incident or schedules maintenance.
type Declaration struct {
	Kind       string     `json:"kind"` // incident | maintenance
	Title      string     `json:"title"`
	Components []string   `json:"components"`
	Status     string     `json:"status,omitempty"` // default investigating / scheduled
	Impact     string     `json:"impact,omitempty"` // default degraded / maintenance
	Message    string     `json:"message"`
	StartsAt   *time.Time `json:"starts_at,omitempty"` // maintenance only
	EndsAt     *time.Time `json:"ends_at,omitempty"`
}

// Change is a new update on an incident.
type Change struct {
	Status  string `json:"status"`
	Impact  string `json:"impact,omitempty"` // default: the incident's current impact
	Message string `json:"message"`
}

func (d *Declaration) normalise() error {
	d.Title, d.Message = strings.TrimSpace(d.Title), strings.TrimSpace(d.Message)
	switch d.Kind {
	case "", "incident":
		d.Kind = "incident"
		if d.Status == "" {
			d.Status = "investigating"
		}
		if d.Impact == "" {
			d.Impact = Degraded
		}
		if d.StartsAt != nil || d.EndsAt != nil {
			return fmt.Errorf("%w: only maintenance has a window (starts_at, ends_at)", ErrInvalid)
		}
	case "maintenance":
		if d.Status == "" {
			d.Status = "scheduled"
		}
		if d.Impact == "" {
			d.Impact = Maintenance
		}
		if d.StartsAt == nil || d.EndsAt == nil || !d.EndsAt.After(*d.StartsAt) {
			return fmt.Errorf("%w: maintenance needs starts_at before ends_at", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: kind is incident or maintenance", ErrInvalid)
	}
	if d.Title == "" || len(d.Title) > 200 {
		return fmt.Errorf("%w: a title of 1 to 200 characters is required", ErrInvalid)
	}
	if len(d.Components) == 0 || len(d.Components) > 20 {
		return fmt.Errorf("%w: name 1 to 20 components (%s, or integration:<name>)", ErrInvalid, componentIDs())
	}
	for i, c := range d.Components {
		d.Components[i] = strings.TrimSpace(c)
		if !ValidComponent(d.Components[i]) {
			return fmt.Errorf("%w: unknown component %q (%s, or integration:<name>)", ErrInvalid, c, componentIDs())
		}
	}
	return checkUpdate(d.Kind, d.Status, d.Impact, d.Message)
}

func componentIDs() string {
	var ids []string
	for _, c := range Components {
		ids = append(ids, c.ID)
	}
	return strings.Join(ids, ", ")
}

func checkUpdate(kind, status, impact, message string) error {
	allowed := incidentStatuses
	if kind == "maintenance" {
		allowed = maintenanceStatuses
	}
	if !slices.Contains(allowed, status) {
		return fmt.Errorf("%w: a %s's status is one of %s", ErrInvalid, kind, strings.Join(allowed, ", "))
	}
	if !slices.Contains(impacts, impact) {
		return fmt.Errorf("%w: impact is one of %s", ErrInvalid, strings.Join(impacts, ", "))
	}
	if (kind == "maintenance") != (impact == Maintenance || (status == "completed" && impact == "none")) {
		return fmt.Errorf("%w: impact %s does not apply to a %s", ErrInvalid, impact, kind)
	}
	if message == "" || len(message) > 5000 {
		return fmt.Errorf("%w: a message of 1 to 5000 characters is required", ErrInvalid)
	}
	return nil
}

// Open records a declaration as actor (cli:<user> or api:<token name>);
// it returns the incident's id.
func Open(ctx context.Context, pool *pgxpool.Pool, d Declaration, actor string) (uuid.UUID, error) {
	if err := d.normalise(); err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err := pool.QueryRow(ctx, `SELECT taskiem_status_open($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		d.Kind, d.Title, d.Components, d.StartsAt, d.EndsAt, d.Status, d.Impact, d.Message, actor).Scan(&id)
	return id, dbErr(err)
}

// Post appends an update to an incident as actor.
func Post(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, c Change, actor string) error {
	c.Message = strings.TrimSpace(c.Message)
	var kind, impact string
	err := pool.QueryRow(ctx, `SELECT i.kind, u.impact FROM status_incidents i
		JOIN LATERAL (SELECT impact FROM status_updates WHERE incident_id = i.id ORDER BY id DESC LIMIT 1) u ON true WHERE i.id = $1`, id).Scan(&kind, &impact)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	switch {
	case c.Impact != "":
	case c.Status == "resolved" || c.Status == "completed":
		c.Impact = "none"
	default:
		c.Impact = impact
	}
	if err := checkUpdate(kind, c.Status, c.Impact, c.Message); err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `SELECT taskiem_status_update($1, $2, $3, $4, $5)`, id, c.Status, c.Impact, c.Message, actor)
	return dbErr(err)
}

// dbErr turns the definer functions' refusals into ErrInvalid/ErrNotFound.
func dbErr(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "P0002":
			return ErrNotFound
		case "22023", "23514":
			return fmt.Errorf("%w: %s", ErrInvalid, pe.Message)
		}
	}
	return err
}

// Update is one public update on an incident.
type Update struct {
	Status  string    `json:"status"`
	Impact  string    `json:"impact"`
	Message string    `json:"message"`
	At      time.Time `json:"at"`
	// Actor is who posted it: only in the admin API, never on the page.
	Actor string `json:"actor,omitempty"`
	seq   int64
}

// Incident is an incident or maintenance window with its updates, newest
// first.
type Incident struct {
	ID         uuid.UUID  `json:"id"`
	Kind       string     `json:"kind"`
	Title      string     `json:"title"`
	Components []string   `json:"components"`
	StartsAt   *time.Time `json:"starts_at,omitempty"`
	EndsAt     *time.Time `json:"ends_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	Status     string     `json:"status"`
	Impact     string     `json:"impact"`
	Closed     bool       `json:"closed"`
	ClosedAt   *time.Time `json:"closed_at,omitempty"`
	Updates    []Update   `json:"updates"`
}

// active reports whether the incident affects its components now.
func (i Incident) active(now time.Time) bool {
	if i.Closed {
		return false
	}
	if i.Kind == "maintenance" && i.Status == "scheduled" {
		return i.StartsAt != nil && !now.Before(*i.StartsAt) && now.Before(*i.EndsAt)
	}
	return true
}

func (i Incident) componentState() string {
	switch i.Impact {
	case "none":
		return Operational
	case Maintenance:
		return Maintenance
	}
	return i.Impact
}

// Component is one part of the platform and its state now.
type Component struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
	// Automatic: the state comes from the synthetic canary, not an
	// operator's declaration.
	Automatic bool `json:"automatic,omitempty"`
}

// Canary summarises the synthetic probe: whether it ran lately, the last
// result, and the share of probes that passed in the last 24 hours.
type Canary struct {
	LastAt    *time.Time `json:"last_at,omitempty"`
	LastOK    bool       `json:"last_ok"`
	Probes24h int        `json:"probes_24h"`
	Passed24h float64    `json:"passed_24h"` // 0..1
}

// Page is the whole status page.
type Page struct {
	Status      string      `json:"status"` // the worst component state
	Summary     string      `json:"summary"`
	Components  []Component `json:"components"`
	Active      []Incident  `json:"active"`
	Upcoming    []Incident  `json:"upcoming"`
	Recent      []Incident  `json:"recent"` // closed in the last RecentDays
	Canary      *Canary     `json:"canary,omitempty"`
	GeneratedAt time.Time   `json:"generated_at"`
}

// Options tune Load.
type Options struct {
	// Canary shows the canary's results and lets repeated failures mark
	// components degraded automatically.
	Canary bool
	// FailuresToDegrade is how many consecutive failed probes, the last
	// within StaleAfter, mark a component degraded; default 3.
	FailuresToDegrade int
	StaleAfter        time.Duration // default 10m
	// RecentDays is how far back closed incidents are listed; default 14.
	RecentDays int
	// WithActors includes who posted each update (admin API only).
	WithActors bool
}

func (o *Options) defaults() {
	if o.FailuresToDegrade <= 0 {
		o.FailuresToDegrade = 3
	}
	if o.StaleAfter <= 0 {
		o.StaleAfter = 10 * time.Minute
	}
	if o.RecentDays <= 0 {
		o.RecentDays = 14
	}
}

// Incidents loads incidents created since a time or still open, newest
// first, with their updates.
func Incidents(ctx context.Context, pool *pgxpool.Pool, since time.Time, withActors bool) ([]Incident, error) {
	rows, err := pool.Query(ctx, `SELECT i.id, i.kind, i.title, i.components, i.scheduled_start, i.scheduled_end, i.created_at,
		u.id, u.status, u.impact, u.message, u.actor, u.at
		FROM status_incidents i JOIN status_updates u ON u.incident_id = i.id
		WHERE i.id IN (SELECT x.id FROM status_incidents x
			WHERE x.created_at >= $1 OR x.scheduled_end >= $1
			   OR NOT EXISTS (SELECT 1 FROM status_updates c WHERE c.incident_id = x.id AND c.status IN ('resolved', 'completed'))
			ORDER BY x.created_at DESC LIMIT 200)
		ORDER BY i.created_at DESC, i.id, u.id DESC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Incident
	for rows.Next() {
		var i Incident
		var u Update
		if err := rows.Scan(&i.ID, &i.Kind, &i.Title, &i.Components, &i.StartsAt, &i.EndsAt, &i.CreatedAt,
			&u.seq, &u.Status, &u.Impact, &u.Message, &u.Actor, &u.At); err != nil {
			return nil, err
		}
		if !withActors {
			u.Actor = ""
		}
		if n := len(out); n > 0 && out[n-1].ID == i.ID {
			out[n-1].Updates = append(out[n-1].Updates, u)
			continue
		}
		i.Status, i.Impact = u.Status, u.Impact // newest update first
		if u.Status == "resolved" || u.Status == "completed" {
			i.Closed, i.ClosedAt = true, &u.At
		}
		i.Updates = []Update{u}
		out = append(out, i)
	}
	return out, rows.Err()
}

type probe struct {
	at time.Time
	ok bool
	// stage is where a failed probe failed (accept, complete, output).
	stage string
}

func probes(ctx context.Context, pool *pgxpool.Pool, since time.Time) ([]probe, error) {
	rows, err := pool.Query(ctx, `SELECT at, ok, COALESCE(error, '') FROM status_probes WHERE at >= $1 ORDER BY at DESC, id DESC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []probe
	for rows.Next() {
		var p probe
		var e string
		if err := rows.Scan(&p.at, &p.ok, &e); err != nil {
			return nil, err
		}
		p.stage, _, _ = strings.Cut(e, ":")
		out = append(out, p)
	}
	return out, rows.Err()
}

// Load builds the page as of now.
func Load(ctx context.Context, pool *pgxpool.Pool, now time.Time, o Options) (*Page, error) {
	o.defaults()
	incidents, err := Incidents(ctx, pool, now.AddDate(0, 0, -o.RecentDays), o.WithActors)
	if err != nil {
		return nil, err
	}
	var ps []probe
	if o.Canary {
		if ps, err = probes(ctx, pool, now.Add(-24*time.Hour)); err != nil {
			return nil, err
		}
	}
	return Build(now, incidents, ps, o), nil
}

// Build computes the page from incidents (newest first) and probes (newest
// first); it is pure, for tests.
func Build(now time.Time, incidents []Incident, ps []probe, o Options) *Page {
	o.defaults()
	p := &Page{GeneratedAt: now.UTC(), Active: []Incident{}, Upcoming: []Incident{}, Recent: []Incident{}}
	state := map[string]string{}
	var integrations []string
	for _, i := range incidents {
		switch {
		case i.active(now):
			p.Active = append(p.Active, i)
			for _, c := range i.Components {
				if _, seen := state[c]; !seen && integrationRe.MatchString(c) {
					integrations = append(integrations, c)
				}
				state[c] = Worse(state[c], i.componentState())
			}
		case !i.Closed && i.Kind == "maintenance" && i.StartsAt != nil && now.Before(*i.StartsAt):
			p.Upcoming = append(p.Upcoming, i)
		case i.Closed && i.ClosedAt != nil && i.ClosedAt.After(now.AddDate(0, 0, -o.RecentDays)):
			p.Recent = append(p.Recent, i)
		}
	}
	slices.Reverse(p.Upcoming) // soonest first
	automatic := map[string]bool{}
	if o.Canary {
		p.Canary = summarise(ps)
		if len(ps) >= o.FailuresToDegrade && now.Sub(ps[0].at) <= o.StaleAfter {
			failing := true
			for _, x := range ps[:o.FailuresToDegrade] {
				failing = failing && !x.ok
			}
			if failing {
				// The newest failure says where: the webhook was not
				// accepted, or the run did not finish.
				c := "runs"
				if ps[0].stage == "accept" {
					c = "webhooks"
				}
				if state[c] == "" || state[c] == Operational {
					state[c], automatic[c] = Degraded, true
				}
			}
		}
	}
	p.Status = Operational
	for _, c := range Components {
		st := state[c.ID]
		if st == "" {
			st = Operational
		}
		p.Components = append(p.Components, Component{ID: c.ID, Name: c.Name, Description: c.Description, Status: st, Automatic: automatic[c.ID]})
		p.Status = Worse(p.Status, st)
	}
	for _, c := range integrations {
		name := strings.TrimPrefix(c, "integration:")
		p.Components = append(p.Components, Component{ID: c, Name: "Integration: " + name, Status: state[c]})
		p.Status = Worse(p.Status, state[c])
	}
	p.Summary = map[string]string{
		Operational:   "All systems operational",
		Maintenance:   "Maintenance in progress",
		Degraded:      "Some systems are degraded",
		PartialOutage: "Partial outage",
		MajorOutage:   "Major outage",
	}[p.Status]
	return p
}

func summarise(ps []probe) *Canary {
	c := &Canary{Probes24h: len(ps)}
	if len(ps) == 0 {
		return c
	}
	at := ps[0].at.UTC()
	c.LastAt, c.LastOK = &at, ps[0].ok
	ok := 0
	for _, x := range ps {
		if x.ok {
			ok++
		}
	}
	c.Passed24h = float64(ok) / float64(len(ps))
	return c
}

// RecordProbe stores one canary result for the page; error is a short code
// (accept, complete, output, with a detail after a colon).
func RecordProbe(ctx context.Context, pool *pgxpool.Pool, ok bool, accept, complete time.Duration, errCode string) error {
	ms := func(d time.Duration) *int32 {
		if d <= 0 {
			return nil
		}
		v := int32(min(d.Milliseconds(), 1<<31-1)) //nolint:gosec // bounded above
		return &v
	}
	var e *string
	if errCode != "" {
		e = &errCode
	}
	_, err := pool.Exec(ctx, `SELECT taskiem_status_record_probe($1, $2, $3, $4)`, ok, ms(accept), ms(complete), e)
	return err
}
