package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// A person's profile in a business (docs/spec/chat-five-more-flows.md,
// phase 3 plan, step 2): their job title, when they started, a note, their
// pay for the record (Taskiem pays no one here) and their usual hours,
// which clocking in is measured against. Read and changed by those who
// manage people; the pay is theirs only.

// rotaDays are the days of the week as a rota names them, Monday first.
var rotaDays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

var clockRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// rotaShift is one day's usual hours: an end before its start runs past
// midnight.
type rotaShift struct {
	Day   string `json:"day"`
	Start string `json:"start"`
	End   string `json:"end"`
}

// staffProfile is the profile as the API shows and takes it.
type staffProfile struct {
	Title     string      `json:"title"`
	StartedOn *string     `json:"started_on,omitempty"`
	Note      string      `json:"note"`
	PayKobo   *int64      `json:"pay_kobo,omitempty"`
	PayPer    *string     `json:"pay_per,omitempty"`
	Rota      []rotaShift `json:"rota"`
}

// check tidies a profile and says what is wrong with it.
func (p *staffProfile) check() error {
	p.Title = strings.TrimSpace(p.Title)
	p.Note = strings.TrimSpace(p.Note)
	if len([]rune(p.Title)) > 60 {
		return fmt.Errorf("%w: a job title is at most 60 letters", errBadRequest)
	}
	if len([]rune(p.Note)) > 500 {
		return fmt.Errorf("%w: a note is at most 500 letters", errBadRequest)
	}
	if p.StartedOn != nil {
		if *p.StartedOn == "" {
			p.StartedOn = nil
		} else if d, err := time.Parse("2006-01-02", *p.StartedOn); err != nil || d.After(time.Now().AddDate(1, 0, 0)) {
			return fmt.Errorf("%w: the day they started is a date, like 2026-03-01", errBadRequest)
		}
	}
	if (p.PayKobo == nil) != (p.PayPer == nil || *p.PayPer == "") {
		return fmt.Errorf("%w: give the pay and what it is per (hour, day, week or month), or neither", errBadRequest)
	}
	if p.PayPer != nil && *p.PayPer == "" {
		p.PayPer = nil
	}
	if p.PayKobo != nil {
		if *p.PayKobo < 0 || *p.PayKobo > 100_000_000_000 {
			return fmt.Errorf("%w: the pay is from ₦0 to ₦1,000,000,000", errBadRequest)
		}
		if !slices.Contains([]string{"hour", "day", "week", "month"}, *p.PayPer) {
			return fmt.Errorf("%w: pay is per hour, day, week or month", errBadRequest)
		}
	}
	if p.Rota == nil {
		p.Rota = []rotaShift{}
	}
	seen := map[string]bool{}
	for i := range p.Rota {
		r := &p.Rota[i]
		r.Day = strings.ToLower(strings.TrimSpace(r.Day))
		if !slices.Contains(rotaDays, r.Day) {
			return fmt.Errorf("%w: a day of the rota is mon, tue, wed, thu, fri, sat or sun", errBadRequest)
		}
		if seen[r.Day] {
			return fmt.Errorf("%w: one set of hours a day", errBadRequest)
		}
		seen[r.Day] = true
		if !clockRe.MatchString(r.Start) || !clockRe.MatchString(r.End) || r.Start == r.End {
			return fmt.Errorf("%w: hours are two different times, like 08:00 and 16:00", errBadRequest)
		}
	}
	slices.SortFunc(p.Rota, func(a, b rotaShift) int {
		return slices.Index(rotaDays, a.Day) - slices.Index(rotaDays, b.Day)
	})
	return nil
}

// staffProfileTx is a person's profile, empty when none was filled in.
func staffProfileTx(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID) (staffProfile, error) {
	var out staffProfile
	var started *time.Time
	var rota []byte
	err := tx.QueryRow(ctx, `SELECT title, started_on, note, pay_kobo, pay_per, rota FROM staff_profiles WHERE tenant_id = $1 AND user_id = $2`, tenant, user).
		Scan(&out.Title, &started, &out.Note, &out.PayKobo, &out.PayPer, &rota)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		out.Rota = []rotaShift{}
		return out, nil
	case err != nil:
		return out, err
	}
	if started != nil {
		d := started.Format("2006-01-02")
		out.StartedOn = &d
	}
	if err := json.Unmarshal(rota, &out.Rota); err != nil {
		return out, err
	}
	if out.Rota == nil {
		out.Rota = []rotaShift{}
	}
	return out, nil
}

// getStaff is GET /v1/staff/{user}: a person of the business and their
// profile.
func (s *Server) getStaff(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	user, err := uuid.Parse(chi.URLParam(r, "user"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	reveal := p.Can(PermPIIReveal)
	show := func(n string) string {
		if reveal {
			return n
		}
		return maskPhone(n)
	}
	var out struct {
		staffMember
		Profile staffProfile `json:"profile"`
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		people, err := staffPeopleTx(r.Context(), tx, p, user, show)
		if err != nil {
			return err
		}
		if len(people) == 0 {
			return pgx.ErrNoRows
		}
		out.staffMember = people[0]
		out.Profile, err = staffProfileTx(r.Context(), tx, p.TenantID, user)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// putStaffProfile is PUT /v1/staff/{user}/profile: the whole profile.
func (s *Server) putStaffProfile(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	user, err := uuid.Parse(chi.URLParam(r, "user"))
	if err != nil {
		s.fail(w, r, pgx.ErrNoRows)
		return
	}
	var in staffProfile
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := in.check(); err != nil {
		s.fail(w, r, err)
		return
	}
	rota, _ := json.Marshal(in.Rota)
	if err := s.tx(r, func(tx pgx.Tx) error {
		var member bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM memberships WHERE tenant_id = $1 AND user_id = $2)`, p.TenantID, user).Scan(&member); err != nil {
			return err
		}
		if !member {
			return pgx.ErrNoRows
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO staff_profiles (tenant_id, user_id, title, started_on, note, pay_kobo, pay_per, rota, updated_at, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now(), $9)
			ON CONFLICT (tenant_id, user_id) DO UPDATE SET title = EXCLUDED.title, started_on = EXCLUDED.started_on, note = EXCLUDED.note,
			pay_kobo = EXCLUDED.pay_kobo, pay_per = EXCLUDED.pay_per, rota = EXCLUDED.rota, updated_at = now(), updated_by = EXCLUDED.updated_by`,
			p.TenantID, user, in.Title, in.StartedOn, in.Note, in.PayKobo, in.PayPer, rota, p.Actor()); err != nil {
			return err
		}
		// The pay is not written to the audit log; that it changed is.
		return auditTx(r, tx, "staff.profile", user.String(), map[string]any{"title": in.Title, "rota_days": len(in.Rota), "pay_set": in.PayKobo != nil})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": in})
}
