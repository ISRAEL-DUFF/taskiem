// Package cataloguetest builds catalogue packages from the example
// connector for tests.
package cataloguetest

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"net/netip"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/connpkg"
	"github.com/israel-duff/taskiem/engine/conntest"
	"github.com/israel-duff/taskiem/engine/wasmconn/wasmtest"
)

// Key is a fixed publisher key for tests.
func Key() (ed25519.PrivateKey, string) {
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, "taskiem catalogue test publisher")
	k := ed25519.NewKeyFromSeed(seed)
	pub := k.Public().(ed25519.PublicKey)
	return k, base64.StdEncoding.EncodeToString(pub)
}

// Package is the example ledger connector as publisher's p_<publisher>_ledger
// at version, signed with key. edit, when set, changes the manifest first
// (the suite and module stay the example's).
func Package(t testing.TB, publisher, version string, key ed25519.PrivateKey, edit func(string) string) *connpkg.Package {
	t.Helper()
	manifest, module := wasmtest.Example(t)
	m := strings.ReplaceAll(string(manifest), "x_example_ledger", "p_"+publisher+"_ledger")
	m = strings.Replace(m, "version: 1.0.0", "version: "+version, 1)
	if edit != nil {
		m = edit(m)
	}
	suite, err := conntest.LoadDir(filepath.Join(root(), "examples", "wasm-connector", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	p := &connpkg.Package{Format: connpkg.Format, ID: "p_" + publisher + "_ledger", Version: version, Manifest: m, Module: module, Licence: "Apache-2.0",
		Conformance: suite.Inline(), Attestation: connpkg.Attestation{OriginalWork: true, Contact: "dev@" + publisher + ".test"}, CreatedAt: "2026-10-07T09:00:00Z"}
	p.Sign(key)
	return p
}

// Resolver resolves every host to one address.
type Resolver struct{ Addr string }

// Public resolves every host to a public address.
var Public = Resolver{Addr: "93.184.215.14"}

func (r Resolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr(r.Addr)}, nil
}

func root() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}
