package api_test

import (
	"os"
	"testing"

	"github.com/israel-duff/taskiem/engine/links"
)

// TestMain writes a linked business's copy of a storefront at once: in
// production it waits a little, so a run of changes is written together
// (links.StorefrontSettle), which a test draining the work would not see.
func TestMain(m *testing.M) {
	links.StorefrontSettle = 0
	os.Exit(m.Run())
}
