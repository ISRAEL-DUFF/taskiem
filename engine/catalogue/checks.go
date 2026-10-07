package catalogue

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/connpkg"
	"github.com/israel-duff/taskiem/engine/conntest"
	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/wasmconn"
)

// Licences a catalogue connector may be published under (decision 0020):
// the linked tier of decision 0007, MPL-2.0 (file-level copyleft: the
// connector's own files stay open, nothing else is affected), and
// LicenseRef-Proprietary for closed connectors the publisher licenses to
// Taskiem and installing tenants under the publisher agreement.
var Licences = map[string]bool{
	"MIT": true, "MIT-0": true, "Apache-2.0": true, "BSD-2-Clause": true, "BSD-3-Clause": true, "ISC": true, "PostgreSQL": true,
	"0BSD": true, "Unlicense": true, "CC0-1.0": true, "BlueOak-1.0.0": true, "MPL-2.0": true, "LicenseRef-Proprietary": true,
}

// Check is one automated check's outcome.
type Check struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

// Report is everything the automated stage found, kept with the
// submission for the reviewer.
type Report struct {
	Passed      bool                 `json:"passed"`
	Checks      []Check              `json:"checks"`
	Lint        []Finding            `json:"lint,omitempty"`
	Module      *wasmconn.ModuleInfo `json:"module,omitempty"`
	Conformance *conntest.Report     `json:"conformance,omitempty"`
	Hosts       map[string][]string  `json:"hosts,omitempty"` // host -> addresses it resolved to
	CheckedAt   time.Time            `json:"checked_at"`
}

// Failed names the checks that did not pass.
func (r *Report) Failed() []string {
	var out []string
	for _, c := range r.Checks {
		if !c.Pass {
			out = append(out, c.Name)
		}
	}
	return out
}

// Published is a version already in the catalogue, for the semver rules.
type Published struct {
	Version  string
	Manifest *connector.Manifest
}

// Checker runs the automated stage of the review pipeline.
type Checker struct {
	// Runtime compiles the submitted module. Nil starts a runtime with
	// Limits for each run and closes it after, so nothing a submission
	// compiled stays in memory.
	Runtime *wasmconn.Runtime
	Limits  wasmconn.Limits
	// Resolver looks up declared hosts; nil uses the system resolver.
	Resolver egress.Resolver
	// Blocked decides whether an address is off limits; default
	// egress.BlockedAddr. Tests never loosen it: a host on a private
	// address fails the check.
	Blocked func(netip.Addr) bool
	// Timeout bounds one conformance case; default 10s.
	Timeout time.Duration
	Now     func() time.Time
}

// Run checks a package from the publisher whose namespace is slug and
// whose registered key is pub, against the versions it already published.
func (c *Checker) Run(ctx context.Context, p *connpkg.Package, slug string, pub ed25519.PublicKey, published []Published) *Report {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	rep := &Report{CheckedAt: now().UTC()}
	rt := c.Runtime
	if rt == nil {
		var err error
		if rt, err = wasmconn.New(ctx, c.Limits); err != nil {
			rep.Checks = append(rep.Checks, Check{Name: "module", Detail: "the sandbox did not start: " + err.Error()})
			return rep
		}
		defer func() { _ = rt.Close(context.WithoutCancel(ctx)) }()
	}
	add := func(name string, err error, detail string) {
		ch := Check{Name: name, Pass: err == nil, Detail: detail}
		if err != nil {
			ch.Detail = err.Error()
		}
		rep.Checks = append(rep.Checks, ch)
	}

	// 1. The signature over the digest, with the publisher's key.
	add("signature", p.Verify(pub), "signed by key "+p.KeyID+"; digest "+p.Digest)

	// 2. The manifest, strictly, in the publisher's namespace.
	m, findings := Lint([]byte(p.Manifest), LintOptions{Publisher: slug})
	rep.Lint = findings
	var mErr error
	switch errs := Errors(findings); {
	case len(errs) > 0:
		msgs := make([]string, len(errs))
		for i, f := range errs {
			msgs[i] = f.Path + ": " + f.Message
		}
		mErr = fmt.Errorf("%s", strings.Join(msgs, "; "))
	case m.ID != p.ID || m.Version != p.Version:
		mErr = fmt.Errorf("the package says %s %s but the manifest %s %s", p.ID, p.Version, m.ID, m.Version)
	}
	add("manifest", mErr, fmt.Sprintf("%d warning(s) for the reviewer", len(findings)-len(Errors(findings))))

	// 3. The module: size, imports on the allow-list, the ABI export,
	// memory within the limit; then it must load with the manifest.
	info, err := rt.Inspect(ctx, p.Module)
	rep.Module = &info
	var conn *connector.Connector
	if err == nil && mErr == nil {
		conn, err = rt.LoadPublished(ctx, []byte(p.Manifest), p.Module)
	}
	add("module", err, fmt.Sprintf("%d bytes; imports %s; memory %d of %d pages", info.Size, strings.Join(info.Imports, ", "), info.MemoryMinPages, info.LimitPages))

	// 4. The licence and the attestation.
	var lErr error
	if !Licences[p.Licence] {
		lErr = fmt.Errorf("licence %q is not accepted (one of %s)", p.Licence, strings.Join(sortedKeys(Licences), ", "))
	}
	add("licence", lErr, p.Licence)
	var aErr error
	switch {
	case !p.Attestation.OriginalWork:
		aErr = fmt.Errorf("the publisher must attest the connector is original work it may publish")
	case !strings.Contains(p.Attestation.Contact, "@"):
		aErr = fmt.Errorf("the attestation needs a contact email address")
	}
	add("attestation", aErr, p.Attestation.Contact)

	// 5. Hosts resolve to public addresses only.
	if m != nil {
		rep.Hosts = map[string][]string{}
		add("hosts", c.hosts(ctx, m.Hosts(), rep.Hosts), strings.Join(m.Hosts(), ", "))
	} else {
		add("hosts", fmt.Errorf("no manifest to read hosts from"), "")
	}

	// 6. Semver against what the publisher already published.
	if m != nil {
		add("semver", Semver(m, published), "")
	} else {
		add("semver", fmt.Errorf("no manifest"), "")
	}

	// 7. Conformance: every case passes in the sandbox, every action is
	// covered, and every idempotent write sends its key.
	var cErr error
	switch {
	case p.Conformance == nil || len(p.Conformance.Cases) == 0:
		cErr = fmt.Errorf("the package has no conformance cases")
	case conn == nil:
		cErr = fmt.Errorf("the module does not load")
	default:
		cr := conntest.Run(ctx, conn, p.Conformance, conntest.Options{Timeout: c.Timeout})
		rep.Conformance = &cr
		var probs []string
		if n := cr.Failed(); n > 0 {
			probs = append(probs, fmt.Sprintf("%d case(s) fail", n))
		}
		if len(cr.Uncovered) > 0 {
			probs = append(probs, "no case for "+strings.Join(cr.Uncovered, ", "))
		}
		if len(cr.KeyNotSent) > 0 {
			probs = append(probs, "the idempotency key never reaches the provider in "+strings.Join(cr.KeyNotSent, ", ")+": the class claims a deduplication the module does not do")
		}
		if len(probs) > 0 {
			cErr = fmt.Errorf("%s", strings.Join(probs, "; "))
		}
	}
	add("conformance", cErr, "")

	rep.Passed = len(rep.Failed()) == 0
	return rep
}

func (c *Checker) hosts(ctx context.Context, hosts []string, seen map[string][]string) error {
	res := c.Resolver
	if res == nil {
		res = net.DefaultResolver
	}
	blocked := c.Blocked
	if blocked == nil {
		blocked = egress.BlockedAddr
	}
	var probs []string
	for _, h := range hosts {
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		addrs, err := res.LookupNetIP(rctx, "ip", h)
		cancel()
		if err != nil || len(addrs) == 0 {
			probs = append(probs, fmt.Sprintf("%s does not resolve", h))
			continue
		}
		for _, a := range addrs {
			seen[h] = append(seen[h], a.String())
			if blocked(a) {
				probs = append(probs, fmt.Sprintf("%s resolves to %s, which is not a public address", h, a))
			}
		}
	}
	if len(probs) > 0 {
		return fmt.Errorf("%s", strings.Join(probs, "; "))
	}
	return nil
}

// Semver applies connector/v1 rule 8 to a new version: it must be newer
// than every published version of its major, and must not remove actions,
// input or output fields, or change an action's class.
func Semver(m *connector.Manifest, published []Published) error {
	var probs []string
	major, _, _ := strings.Cut(m.Version, ".")
	for _, p := range published {
		pm, _, _ := strings.Cut(p.Version, ".")
		if pm != major {
			continue
		}
		if !wasmconn.Newer(m.Version, p.Version) {
			probs = append(probs, fmt.Sprintf("%s is not newer than the published %s", m.Version, p.Version))
			continue
		}
		d := Compare(p.Manifest, m)
		if len(d.RemovedActions) > 0 {
			probs = append(probs, fmt.Sprintf("removes action(s) %s that %s has: that needs a new major version", strings.Join(d.RemovedActions, ", "), p.Version))
		}
		for _, cc := range d.ClassChanges {
			probs = append(probs, fmt.Sprintf("changes %s from %s to %s: that needs a new major version", cc.Action, cc.From, cc.To))
		}
		for _, f := range d.RemovedFields {
			probs = append(probs, fmt.Sprintf("removes %s: that needs a new major version", f))
		}
	}
	if len(probs) > 0 {
		return fmt.Errorf("%s", strings.Join(probs, "; "))
	}
	return nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
