package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

// A plan's count limit holds under creates made at the same moment: with
// room for one more, exactly one of them is made.
func TestLimitCountsHoldUnderParallelCreates(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	tenant := tenantID(t, owner)
	wd := `{"schema":"wd/v1","id":"wf_x","version":1,"name":"x","trigger":{"type":"manual"},"steps":[{"id":"a","type":"transform","config":{"output":{"x":1}}}]}`
	owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "first", "definition": json.RawMessage(wd)})
	owner.must(204, "PUT", "/v1/secrets/prod/first", map[string]any{"value": "x"})
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "fakepay@1", "name": "first", "credentials": map[string]string{}})
	var have struct{ workflows, secrets, connections int }
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM workflows WHERE tenant_id = $1),
		(SELECT count(*) FROM secrets WHERE tenant_id = $1 AND name IS NOT NULL), (SELECT count(*) FROM connections WHERE tenant_id = $1 AND status = 'active')`,
		tenant).Scan(&have.workflows, &have.secrets, &have.connections); err != nil {
		t.Fatal(err)
	}
	// Room for exactly one more of each.
	if err := w.env.Store.SetLimits(context.Background(), tenant, map[string]any{"max_workflows": have.workflows + 1, "max_secrets": have.secrets + 1,
		"max_connections": have.connections + 1}, "operator"); err != nil {
		t.Fatal(err)
	}
	const n = 10
	race := func(what string, ok int, create func(i int) int) {
		t.Helper()
		var wg sync.WaitGroup
		codes := make([]int, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				codes[i] = create(i)
			}()
		}
		wg.Wait()
		made := 0
		for _, c := range codes {
			if c == ok {
				made++
			}
		}
		if made != 1 {
			t.Errorf("%s: %d of %d creates at once succeeded with room for one: %v", what, made, n, codes)
		}
	}
	race("workflows", 201, func(i int) int {
		st, _ := owner.do("POST", "/v1/workflows", map[string]any{"name": fmt.Sprintf("w%d", i), "definition": json.RawMessage(wd)})
		return st
	})
	race("secrets", 204, func(i int) int {
		st, _ := owner.do("PUT", fmt.Sprintf("/v1/secrets/prod/s%d", i), map[string]any{"value": "x"})
		return st
	})
	race("connections", 201, func(i int) int {
		st, _ := owner.do("POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "fakepay@1", "name": fmt.Sprintf("c%d", i),
			"credentials": map[string]string{}})
		return st
	})
}
