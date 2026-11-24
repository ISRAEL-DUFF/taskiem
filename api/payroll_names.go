package api

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/connectors/payrolla"
)

// payrollNamesTTL is how long a business's Payrolla employee names are
// kept for the chat's "move zainab to sales", which any "move X to Y"
// asks: a few minutes, so a message about the generator does not list
// Payrolla's employees each time. A change to the team made through
// Taskiem forgets them at once.
const payrollNamesTTL = 5 * time.Minute

// payrollNameCache keeps each business's employees' ids, names and
// status (never pay or an account), in memory only.
type payrollNameCache struct {
	mu sync.Mutex
	m  map[uuid.UUID]payrollNamesEntry
}

type payrollNamesEntry struct {
	at     time.Time
	emps   []payrolla.Employee
	forgot time.Time // a list read before this is not kept
}

// payrollNamesOf are the business's employees (ids, names and status
// only) and them as someone, kept for payrollNamesTTL.
func (s *Server) payrollNamesOf(ctx context.Context, tenant uuid.UUID, cl *payrolla.Client) ([]payrolla.Employee, []someone, error) {
	c := &s.payrollNames
	start := time.Now()
	c.mu.Lock()
	e, ok := c.m[tenant]
	c.mu.Unlock()
	if ok && e.emps != nil && start.Sub(e.at) < payrollNamesTTL {
		return e.emps, peopleOf(e.emps), nil
	}
	all, err := cl.AllEmployees(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	emps := make([]payrolla.Employee, 0, len(all))
	for _, x := range all {
		emps = append(emps, payrolla.Employee{ID: x.ID, FirstName: x.FirstName, LastName: x.LastName, Status: x.Status})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[uuid.UUID]payrollNamesEntry{}
	}
	if now := c.m[tenant]; start.After(now.forgot) {
		c.m[tenant] = payrollNamesEntry{at: start, emps: emps, forgot: now.forgot}
	}
	return emps, peopleOf(emps), nil
}

// forgetPayrollNames forgets a business's kept employee names, after a
// change to its team made through Taskiem.
func (s *Server) forgetPayrollNames(tenant uuid.UUID) {
	c := &s.payrollNames
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[uuid.UUID]payrollNamesEntry{}
	}
	c.m[tenant] = payrollNamesEntry{forgot: time.Now()}
}
