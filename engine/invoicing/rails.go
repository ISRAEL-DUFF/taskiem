package invoicing

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// Rail is the business's connection an invoice is paid through.
type Rail struct {
	Connector  string // the connector's id: paystack
	Connection string // the connection's name: main
	Name       string // the service's name: Paystack
	// Transfer is what the rail does with one-time accounts; nil when it
	// cannot open them.
	Transfer *connector.TransferFacts
	// Link: the rail makes payment pages (collect_link).
	Link bool
	// Caps are the capabilities the rail maps, by name (virtual_account,
	// list_credits); Events the normalised events its webhooks carry.
	Caps   map[string]connector.CapabilityMap
	Events map[string]bool
	conn   *connector.Connector
}

// Channel names the rail's connection as the money log does:
// "iswallet/main".
func (r Rail) Channel() string { return r.Connector + "/" + r.Connection }

// Maps reports whether the rail maps capability.
func (r Rail) Maps(capability string) bool {
	_, ok := r.Caps[capability]
	return ok
}

// Takes reports whether the rail's mapping of capability uses an input
// field (account_name, account, customer.email): not ignored or
// unsupported.
func (r Rail) Takes(capability, field string) bool {
	cm, ok := r.Caps[capability]
	return ok && !slices.Contains(cm.Ignores, field) && !slices.Contains(cm.Unsupported, field)
}

// Needs reports whether the rail cannot do capability without an input
// field.
func (r Rail) Needs(capability, field string) bool {
	cm, ok := r.Caps[capability]
	return ok && slices.Contains(cm.Requires, field)
}

// Unsafe reports whether the rail's capability is an unsafe write: an
// answer that never came may have opened something, and must not be sent
// again without a person checking.
func (r Rail) Unsafe(capability string) bool {
	cm, ok := r.Caps[capability]
	return ok && cm.Unsafe
}

func railOf(id, connection string, c *connector.Connector) Rail {
	rl := Rail{Connector: id, Connection: connection, Name: c.Manifest.Name, conn: c, Caps: c.Manifest.Capabilities, Events: map[string]bool{}}
	if cm, ok := c.Manifest.Capabilities["collect_transfer"]; ok {
		rl.Transfer = cm.Transfer
	}
	_, rl.Link = c.Manifest.Capabilities["collect_link"]
	for ev := range c.Manifest.Events {
		rl.Events[ev] = true
	}
	return rl
}

// Rails reaches the business's payment rails outside a workflow run.
type Rails interface {
	// Resolve finds the prod connection named connection ("main", or
	// "paystack/main" when several rails have one of that name).
	Resolve(ctx context.Context, tenant uuid.UUID, connection string) (Rail, error)
	// Call does capability with the rail, each write keyed from key.
	Call(ctx context.Context, tenant uuid.UUID, r Rail, capability string, input map[string]any, key effects.KeyInput) (map[string]any, error)
	// Key is the key capability's last write is sent with, from key: the
	// rail's payment id for the rails mapped so far, known before the
	// call so a payment can be found even when the answer is lost.
	Key(r Rail, capability string, key effects.KeyInput) (string, error)
}

// CapabilityRails reaches the business's rails for capabilities other than
// an invoice's payment, outside a workflow run: issuing customers' account
// numbers (virtual_account, engine/schools) and reading their credits
// back (list_credits, engine/lookback). RegistryRails is one.
type CapabilityRails interface {
	// Capable lists the active prod connections whose rail maps capability.
	Capable(ctx context.Context, tenant uuid.UUID, capability string) ([]Rail, error)
	// ResolveChannel finds the active prod connection a channel names
	// ("iswallet/main") whose rail maps capability; ErrNoRail when none.
	ResolveChannel(ctx context.Context, tenant uuid.UUID, channel, capability string) (Rail, error)
	// Call does capability with the rail, each write keyed from key.
	Call(ctx context.Context, tenant uuid.UUID, r Rail, capability string, input map[string]any, key effects.KeyInput) (map[string]any, error)
}

var _ CapabilityRails = (*RegistryRails)(nil)

// ErrNoRail is a connection that is missing or cannot take payments for
// invoices; the message says which and what to do.
var ErrNoRail = errors.New("invoicing: no payments connection")

// RegistryRails is Rails over the connector registry, the vault and the
// egress guard: the tenant's own credentials, its own connections.
type RegistryRails struct {
	Pool     *pgxpool.Pool
	Registry *connector.Registry
	Vault    *secrets.Vault
	Guard    *egress.Guard
	// Timeout bounds one call (default 30 seconds).
	Timeout time.Duration
}

// Resolve implements Rails.
func (r *RegistryRails) Resolve(ctx context.Context, tenant uuid.UUID, connection string) (Rail, error) {
	if connection == "" {
		connection = "main"
	}
	want, name, _ := strings.Cut(connection, "/")
	if name == "" {
		want, name = "", connection
	}
	reg, err := r.Registry.For(ctx, tenant.String())
	if err != nil {
		return Rail{}, err
	}
	var found []Rail
	err = db.InTenantTx(ctx, r.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT connector FROM connections WHERE environment = 'prod' AND name = $1 AND status = 'active' AND ($2 = '' OR connector = $2)`, name, want)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		for _, id := range ids {
			c := latest(reg, id)
			if c == nil {
				continue
			}
			rl := railOf(id, name, c)
			if rl.Transfer != nil || rl.Link {
				found = append(found, rl)
			}
		}
		return nil
	})
	if err != nil {
		return Rail{}, err
	}
	switch len(found) {
	case 0:
		return Rail{}, fmt.Errorf("%w: no connection named %q in prod can take payments (connect Paystack, Flutterwave, OPay or another payments service, or change the invoice's connection)", ErrNoRail, connection)
	case 1:
		return found[0], nil
	}
	return Rail{}, fmt.Errorf("%w: several payments services have a connection named %q; name one, such as %s/%s", ErrNoRail, name, found[0].Connector, name)
}

// latest is the newest version of connector id the tenant has.
func latest(reg connector.Lookup, id string) *connector.Connector {
	var best *connector.Connector
	for _, c := range reg.List() {
		if c.Manifest.ID == id && (best == nil || newer(c.Manifest.Version, best.Manifest.Version)) {
			best = c
		}
	}
	return best
}

// Call implements Rails.
func (r *RegistryRails) Call(ctx context.Context, tenant uuid.UUID, rl Rail, capability string, input map[string]any, key effects.KeyInput) (map[string]any, error) {
	if rl.conn == nil {
		return nil, fmt.Errorf("%w: the rail was not resolved", ErrNoRail)
	}
	m := rl.conn.Manifest
	purpose := "invoices.payment"
	if p, ok := callPurpose[capability]; ok {
		purpose = p
	}
	creds, err := r.Vault.Credentials(secrets.WithUse(ctx, secrets.Use{Kind: secrets.KindConnection, Purpose: purpose}), tenant, "prod", m.ID, rl.Connection)
	if errors.Is(err, secrets.ErrNotFound) {
		return nil, fmt.Errorf("%w: the %s connection %q is missing in prod", ErrNoRail, m.Name, rl.Connection)
	}
	if err != nil {
		return nil, err
	}
	g := r.Guard
	if g == nil {
		g = &egress.Guard{}
	}
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	hc := g.Client(egress.Policy{Tenant: tenant.String(), Hosts: m.HostsFor(creds), Purpose: "connector:" + m.ID}, timeout)
	base := connector.Request{Credentials: creds, HTTP: hc, Attempt: 1, KeyFirstSent: time.Now()}
	return connector.CallCapability(ctx, rl.conn, capability, input, base, func(step string) (string, error) {
		return stepKey(m, capability, step, key)
	})
}

// callPurpose is what the vault records a call's credentials were read for,
// for capabilities other than an invoice's payment.
var callPurpose = map[string]string{"virtual_account": "schools.account", "list_credits": "money.lookback"}

// Capable lists the tenant's active prod connections whose rail maps
// capability, by connector and name.
func (r *RegistryRails) Capable(ctx context.Context, tenant uuid.UUID, capability string) ([]Rail, error) {
	reg, err := r.Registry.For(ctx, tenant.String())
	if err != nil {
		return nil, err
	}
	var out []Rail
	err = db.InTenantTx(ctx, r.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		out = out[:0]
		rows, err := tx.Query(ctx, `SELECT connector, name FROM connections WHERE environment = 'prod' AND status = 'active' ORDER BY connector, name`)
		if err != nil {
			return err
		}
		type cn struct{ id, name string }
		list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (cn, error) {
			var c cn
			return c, row.Scan(&c.id, &c.name)
		})
		if err != nil {
			return err
		}
		for _, c := range list {
			if conn := latest(reg, c.id); conn != nil {
				if rl := railOf(c.id, c.name, conn); rl.Maps(capability) {
					out = append(out, rl)
				}
			}
		}
		return nil
	})
	return out, err
}

// ResolveChannel finds the tenant's active prod connection a channel
// names ("iswallet/main") whose rail maps capability.
func (r *RegistryRails) ResolveChannel(ctx context.Context, tenant uuid.UUID, channel, capability string) (Rail, error) {
	list, err := r.Capable(ctx, tenant, capability)
	if err != nil {
		return Rail{}, err
	}
	for _, rl := range list {
		if rl.Channel() == channel {
			return rl, nil
		}
	}
	return Rail{}, fmt.Errorf("%w: no active connection %q in prod can %s", ErrNoRail, channel, strings.ReplaceAll(capability, "_", " "))
}

// Key implements Rails.
func (r *RegistryRails) Key(rl Rail, capability string, key effects.KeyInput) (string, error) {
	if rl.conn == nil {
		return "", fmt.Errorf("%w: the rail was not resolved", ErrNoRail)
	}
	plan := rl.conn.Manifest.Capabilities[capability].Plan()
	if len(plan) == 0 {
		return "", fmt.Errorf("%w: %s cannot %s", ErrNoRail, rl.Name, capability)
	}
	last := plan[len(plan)-1]
	step := last.ID
	if step == "" {
		step = last.Action
	}
	return stepKey(rl.conn.Manifest, capability, step, key)
}

func stepKey(m *connector.Manifest, capability, step string, key effects.KeyInput) (string, error) {
	k := key
	k.StepID = key.StepID + ":" + step
	return m.CapabilityKey(capability, step, k)
}

// PaymentKey is the key material of the account or link seq of kind for
// an invoice: the rail's payment id is derived from it, so a retry gets
// the same account and its payment can be found before the rail answers.
func PaymentKey(tenant, invoice uuid.UUID, kind string, seq int) effects.KeyInput {
	return effects.KeyInput{TenantID: tenant.String(), Seed: "invoice:" + invoice.String(), StepID: fmt.Sprintf("%s:%d", kind, seq)}
}

// newer compares dotted version numbers: 1.10.0 is newer than 1.2.0.
func newer(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(as), len(bs)); i++ {
		var x, y int
		if i < len(as) {
			_, _ = fmt.Sscan(as[i], &x)
		}
		if i < len(bs) {
			_, _ = fmt.Sscan(bs[i], &y)
		}
		if x != y {
			return x > y
		}
	}
	return false
}
