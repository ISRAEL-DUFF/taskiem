package api

import (
	"slices"
	"testing"

	"github.com/israel-duff/taskiem/engine/moneydigest"
)

// moneydigest decides who may get the money summaries from its own copy of
// which built-in roles read invoices: it must match the role table.
func TestMoneyDigestRolesMatchRolePermissions(t *testing.T) {
	if len(moneydigest.Roles) != len(rolePermissions) {
		t.Fatalf("moneydigest.Roles has %d roles, the role table %d", len(moneydigest.Roles), len(rolePermissions))
	}
	for role, perms := range rolePermissions {
		read, ok := moneydigest.Roles[role]
		if !ok {
			t.Errorf("moneydigest.Roles lacks %q", role)
			continue
		}
		if want := slices.Contains(perms, PermInvoiceRead); read != want {
			t.Errorf("%s: moneydigest.Roles says invoice.read %v, the role table %v", role, read, want)
		}
	}
}
