package expansion

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Connection is one connection in an environment.
type Connection struct {
	Name      string
	Connector string // connector id: paystack
}

// ErrNoConnection: the environment has no connection for a capability
// step yet. A deploy lets it through (connections are often made after,
// or only in some environments); a run is refused.
var ErrNoConnection = errors.New("no connection")

// ResolverFor resolves capability steps against an environment's
// connections and the connectors a tenant can use. A step that names a
// connection runs on it; one that names none runs on the only connection
// whose rail can do the capability. Refusals say what to do in plain words.
func ResolverFor(conns []Connection, reg connector.Lookup) Resolver {
	return func(name, uses string) (Rail, error) {
		var rails []Rail
		var other *Connection // a connection by that name whose service cannot
		for i, c := range conns {
			if name != "" && c.Name != name {
				continue
			}
			if r, ok := railOf(c, uses, reg); ok {
				rails = append(rails, r)
			} else if name != "" && other == nil {
				other = &conns[i]
			}
		}
		switch {
		case len(rails) == 1:
			return rails[0], nil
		case len(rails) == 0 && other != nil:
			return Rail{}, cannot(*other, uses, reg)
		case name != "" && len(rails) == 0:
			return Rail{}, fmt.Errorf("%w: there is no connection called %q in this environment; connect one, or choose another", ErrNoConnection, name)
		case len(rails) == 0:
			return Rail{}, fmt.Errorf("%w: no connection here can %s. %s", ErrNoConnection, lowerFirst(titleOf(uses)), suggest(uses, reg))
		}
		names := make([]string, len(rails))
		for i, r := range rails {
			names[i] = r.Connection
		}
		sort.Strings(names)
		return Rail{}, fmt.Errorf("several connections can %s (%s); choose one for this step", lowerFirst(titleOf(uses)), strings.Join(names, ", "))
	}
}

// railOf is the connection as a rail that can do uses: the newest major
// of its connector that declares it (any payment, for a wait).
func railOf(c Connection, uses string, reg connector.Lookup) (Rail, bool) {
	var best *connector.Connector
	major := 0
	for _, k := range reg.List() {
		if k.Manifest.ID != c.Connector || k.Manifest.KindOf() != "rail" {
			continue
		}
		if !declares(k.Manifest, uses) {
			continue
		}
		m := 0
		_, _ = fmt.Sscanf(strings.TrimPrefix(k.Ref(), k.Manifest.ID+"@"), "%d", &m)
		if best == nil || m > major {
			best, major = k, m
		}
	}
	if best == nil {
		return Rail{}, false
	}
	return Rail{Ref: best.Ref(), Connection: c.Name, Name: best.Manifest.Name, Manifest: best.Manifest}, true
}

func declares(m *connector.Manifest, uses string) bool {
	if wd.IsWait(uses) {
		return CanWait(m, uses)
	}
	_, ok := m.Capabilities[uses]
	return ok
}

// CanWait reports whether a rail can run the wait: for await_payment, it
// makes payment pages or one-time accounts and says when one is paid; for the payout waits, it
// settles a payment later (settles_by).
func CanWait(m *connector.Manifest, uses string) bool {
	if uses == "await_payment" {
		_, links := m.Capabilities["collect_link"]
		_, accounts := m.Capabilities["collect_transfer"]
		_, paid := m.Events["payment.received"]
		return (links || accounts) && paid
	}
	for _, c := range m.Capabilities {
		if c.SettlesBy != nil {
			return true
		}
	}
	return false
}

// cannot says that a connection's service cannot do the capability, and
// which can: "Your Flutterwave connection cannot open a bank account
// number for a customer to pay into. Connect a rail that can, such as
// Moniepoint."
func cannot(c Connection, uses string, reg connector.Lookup) error {
	name := c.Connector
	for _, k := range reg.List() {
		if k.Manifest.ID == c.Connector {
			name = k.Manifest.Name
		}
	}
	return fmt.Errorf("your %s connection cannot %s. %s", name, lowerFirst(titleOf(uses)), suggest(uses, reg))
}

func suggest(uses string, reg connector.Lookup) string {
	var names []string
	for _, k := range reg.List() {
		if k.Manifest.KindOf() == "rail" && declares(k.Manifest, uses) && !slices.Contains(names, k.Manifest.Name) {
			names = append(names, k.Manifest.Name)
		}
	}
	sort.Strings(names)
	switch len(names) {
	case 0:
		return "No rail Taskiem connects to can do it yet."
	case 1:
		return "Connect " + names[0] + ", which can."
	}
	if len(names) > 3 {
		names = names[:3]
	}
	return "Connect a rail that can, such as " + strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1] + "."
}
