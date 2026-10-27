package brevo

import (
	"context"
	"testing"

	"github.com/israel-duff/taskiem/engine/conntest"
)

// Every action passes the conformance kit on fixtures shaped after the provider's
// published examples.
func TestConformance(t *testing.T) {
	s, err := conntest.LoadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	rep := conntest.Run(context.Background(), New(Options{}), s, conntest.Options{})
	for _, r := range rep.Results {
		for _, p := range r.Problems {
			t.Errorf("%s: %s", r.Case, p)
		}
	}
	if len(rep.Uncovered)+len(rep.ReadWrites) > 0 {
		t.Errorf("uncovered %v, reads that write %v", rep.Uncovered, rep.ReadWrites)
	}
}
