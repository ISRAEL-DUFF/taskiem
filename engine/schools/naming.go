package schools

import "github.com/israel-duff/taskiem/engine/invoicing"

// DefaultNameLimit is the longest account name assumed when a rail
// documents none (invoicing.DefaultNameLimit).
const DefaultNameLimit = invoicing.DefaultNameLimit

// AccountName is the name a parent should see when they transfer into a
// student's account: the school's name and the child's, as every
// customer's permanent account is named (invoicing.AccountName;
// docs/spec/schools.md 6.3).
func AccountName(school, child string, limit int) (name string, shortened, flagged bool) {
	return invoicing.AccountName(school, child, limit)
}
