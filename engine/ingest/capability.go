package ingest

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/capability"
	"github.com/israel-duff/taskiem/engine/connector"
)

// A capability_event trigger (decision 0044) starts on a rail's normalised
// payment events, whichever rail the business connected: payout.failed
// from Paystack's transfer_event, Moniepoint's event or iswallet's
// outflow_event alike. It is registered as the rails' own connector
// triggers, one row per connection and rail trigger, whose events are the
// normalised names; ingest starts it when a delivery is one of them
// (deliverNormalised), never on the raw event: the rows are marked so by
// their options.

// capabilitySub reports whether a subscriber is a capability_event
// trigger's row.
func capabilitySub(s subscriber) bool {
	b, _ := s.options["capability_event"].(bool)
	return b
}

func capabilityEvents(c map[string]any) []string {
	var out []string
	if evs, ok := c["events"].([]any); ok {
		for _, v := range evs {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func checkCapabilityEvent(c map[string]any, reg connector.Lookup) error {
	evs := capabilityEvents(c)
	if len(evs) == 0 {
		return fmt.Errorf("a capability_event trigger names the events it starts on")
	}
	for _, ev := range evs {
		if _, ok := capability.GetEvent(ev); !ok {
			return fmt.Errorf("%q is not a payment event (one of %s)", ev, strings.Join(capability.EventNames(), ", "))
		}
		if len(railsSending(reg, "", ev)) == 0 {
			return fmt.Errorf("no rail Taskiem connects to sends %s yet", ev)
		}
	}
	return nil
}

// railsSending are the rails (newest major first) of connector id (any,
// when empty) that send the normalised event.
func railsSending(reg connector.Lookup, id, ev string) []*connector.Connector {
	var out []*connector.Connector
	for _, k := range reg.List() {
		if k.Manifest.KindOf() != "rail" || (id != "" && k.Manifest.ID != id) {
			continue
		}
		if _, ok := k.Manifest.Events[ev]; ok {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return major(out[i]) > major(out[j]) })
	return out
}

func major(c *connector.Connector) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(c.Ref(), c.Manifest.ID+"@"))
	return n
}

// registerCapabilityEvent registers the trigger in env: for every rail
// that sends any of the events, one connector trigger row per rail trigger
// (the newest major's), listing the normalised events it carries. Rows do
// not depend on what is connected, so a rail connected after the deploy
// starts the workflow too: deliveries only come from connected rails, and
// readiness says what to connect. A row takes deliveries from the
// connection the trigger names, or from any.
func registerCapabilityEvent(ctx context.Context, tx pgx.Tx, tenant, wf uuid.UUID, version int, env string, c map[string]any, reg connector.Lookup) error {
	var conn *string
	if named := str(c, "connection"); named != "" {
		conn = &named
	}
	type key struct{ ref, trigger string }
	rows := map[key][]string{}
	for _, ev := range capabilityEvents(c) {
		seen := map[string]bool{}
		for _, k := range railsSending(reg, "", ev) {
			if seen[k.Manifest.ID] {
				continue // an older major
			}
			seen[k.Manifest.ID] = true
			at := key{k.Ref(), k.Manifest.Events[ev].Trigger}
			rows[at] = append(rows[at], ev)
		}
	}
	keys := make([]key, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].ref < keys[j].ref || keys[i].ref == keys[j].ref && keys[i].trigger < keys[j].trigger
	})
	for _, k := range keys {
		if _, err := tx.Exec(ctx, `INSERT INTO triggers (id, tenant_id, workflow_id, version, environment, type, connector, trigger_name, events, connection, options)
			VALUES ($1, $2, $3, $4, $5, 'connector_event', $6, $7, $8, $9, '{"capability_event": true}')`, uuid.Must(uuid.NewV7()), tenant, wf, version, env, k.ref, k.trigger, rows[k], conn); err != nil {
			return err
		}
	}
	return nil
}
