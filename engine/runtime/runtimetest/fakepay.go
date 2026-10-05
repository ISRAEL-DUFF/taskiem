// Package runtimetest provides a fake payment provider and an engine
// harness for integration, determinism, and crash-recovery tests.
package runtimetest

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

// FakepayManifest is a connector/v1 manifest covering every action class.
const FakepayManifest = `
manifest: connector/v1
id: fakepay
version: 1.0.0
name: Fakepay
category: payments
auth: { type: none }
actions:
  transfer:
    title: Transfer (idempotent by reference)
    class: idempotent_write
    idempotency: { field: reference, encoding: base32_lower, length: 32, prefix: "tsk_", limits: { min_length: 16, max_length: 50, charset: "a-z0-9_-" } }
    compensate: refund
    input: { type: object, required: [amount, logical_id], properties: { amount: { type: integer }, logical_id: { type: string } } }
  bank_transfer:
    title: Bank transfer (reconcilable, provider ignores duplicate keys)
    class: reconcilable_write
    idempotency: { field: reference, encoding: base32_lower, length: 32, prefix: "tsk_" }
    reconcile: verify
    input: { type: object, required: [amount, logical_id], properties: { amount: { type: integer }, logical_id: { type: string } } }
  verify:
    title: Verify by reference
    class: read
    input: { type: object, properties: { reference: { type: string } } }
  notify:
    title: Notify (no key, no status API)
    class: unsafe_write
    input: { type: object, required: [logical_id], properties: { logical_id: { type: string } } }
  refund:
    title: Refund
    class: idempotent_write
    idempotency: { field: reference, encoding: base32_lower, length: 32, prefix: "tsk_" }
    input: { type: object, properties: { logical_id: { type: string } } }
`

// Fault is what the provider does to one call.
type Fault int

const (
	NoFault      Fault = iota
	FailBefore         // 5xx before doing anything (retryable, nothing executed)
	FailAfter          // executes, then the connection drops (unknown outcome)
	RefuseBefore       // connection refused (not sent)
)

// Provider is an in-memory payment provider shared by all workers, playing
// the role of the outside world.
type Provider struct {
	mu         sync.Mutex
	byRef      map[string]map[string]any // reference -> transfer record
	executions map[string]int            // logical id -> times an effect really happened
	refs       map[string]map[string]bool
	// Faults decides the fault for each call; nil means no faults.
	Faults func(action string) Fault
}

func NewProvider() *Provider {
	return &Provider{byRef: map[string]map[string]any{}, executions: map[string]int{}, refs: map[string]map[string]bool{}}
}

// RandomFaults returns a fault function with the given probabilities.
func RandomFaults(seed uint64, before, after float64) func(string) Fault {
	var mu sync.Mutex
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)) //nolint:gosec // deterministic test fault injection
	return func(action string) Fault {
		if action == "verify" {
			return NoFault
		}
		mu.Lock()
		defer mu.Unlock()
		x := r.Float64()
		switch {
		case x < before:
			if action == "notify" {
				return RefuseBefore
			}
			return FailBefore
		case x < before+after:
			return FailAfter
		}
		return NoFault
	}
}

func (p *Provider) fault(action string) Fault {
	if p.Faults == nil {
		return NoFault
	}
	return p.Faults(action)
}

// Executions returns how many times the effect for a logical id happened.
func (p *Provider) Executions(logicalID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.executions[logicalID]
}

// Snapshot returns a copy of all execution counts.
func (p *Provider) Snapshot() map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]int, len(p.executions))
	for k, v := range p.executions {
		out[k] = v
	}
	return out
}

func (p *Provider) transfer(dedup bool) connector.ActionFunc {
	return func(_ context.Context, req connector.Request) (connector.Response, error) {
		ref, _ := req.Input["reference"].(string)
		logical, _ := req.Input["logical_id"].(string)
		f := p.fault("transfer")
		if f == FailBefore {
			return connector.Response{}, fmt.Errorf("503 service unavailable: %w", effects.ErrRetryable)
		}
		p.mu.Lock()
		rec, seen := p.byRef[ref]
		if !seen || !dedup {
			rec = map[string]any{"reference": ref, "status": "success", "amount": req.Input["amount"]}
			p.byRef[ref] = rec
			p.executions[logical]++
			if p.refs[logical] == nil {
				p.refs[logical] = map[string]bool{}
			}
			p.refs[logical][ref] = true
		}
		p.mu.Unlock()
		if f == FailAfter {
			return connector.Response{}, fmt.Errorf("connection reset after send: %w", effects.ErrUnknownOutcome)
		}
		return connector.Response{Output: rec}, nil
	}
}

// Connector returns the fakepay connector bound to this provider.
func (p *Provider) Connector() *connector.Connector {
	return &connector.Connector{
		Manifest: connector.MustParse([]byte(FakepayManifest)),
		Actions: map[string]connector.Action{
			"transfer":      p.transfer(true),
			"bank_transfer": p.transfer(false),
			"refund":        p.transfer(true),
			"verify": connector.ActionFunc(func(_ context.Context, req connector.Request) (connector.Response, error) {
				ref, _ := req.Input["reference"].(string)
				p.mu.Lock()
				rec, ok := p.byRef[ref]
				p.mu.Unlock()
				if !ok {
					return connector.Response{}, connector.ErrNotFound
				}
				return connector.Response{Output: rec}, nil
			}),
			"notify": connector.ActionFunc(func(_ context.Context, req connector.Request) (connector.Response, error) {
				logical, _ := req.Input["logical_id"].(string)
				switch p.fault("notify") {
				case RefuseBefore, FailBefore:
					return connector.Response{}, fmt.Errorf("connection refused: %w", effects.ErrNotSent)
				case FailAfter:
					p.mu.Lock()
					p.executions[logical]++
					p.mu.Unlock()
					return connector.Response{}, fmt.Errorf("timeout after send: %w", effects.ErrUnknownOutcome)
				}
				p.mu.Lock()
				p.executions[logical]++
				p.mu.Unlock()
				return connector.Response{Output: map[string]any{"sent": true}}, nil
			}),
		},
	}
}
