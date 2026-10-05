package spike

import (
	"reflect"
	"testing"
)

func TestDecideLinear(t *testing.T) {
	steps := []string{"a", "b"}
	h := []Event{{1, "RunStarted", ""}}
	want := [][]Command{
		{{Kind: "schedule", StepID: "a"}},
	}
	if got := Decide(steps, h); !reflect.DeepEqual(got, want[0]) {
		t.Fatalf("start: %v", got)
	}
	h = append(h, Event{2, "StepScheduled", "a"})
	if got := Decide(steps, h); got != nil {
		t.Fatalf("while a runs: %v", got)
	}
	h = append(h, Event{3, "StepCompleted", "a"})
	if got := Decide(steps, h); !reflect.DeepEqual(got, []Command{{Kind: "schedule", StepID: "b"}}) {
		t.Fatalf("after a: %v", got)
	}
	h = append(h, Event{4, "StepScheduled", "b"}, Event{5, "StepCompleted", "b"})
	if got := Decide(steps, h); !reflect.DeepEqual(got, []Command{{Kind: "complete"}}) {
		t.Fatalf("after b: %v", got)
	}
	h = append(h, Event{6, "RunCompleted", ""})
	if got := Decide(steps, h); got != nil {
		t.Fatalf("after end: %v", got)
	}
}

// Determinism: replaying every prefix of a history twice gives identical commands.
func TestDecideIsDeterministic(t *testing.T) {
	steps := []string{"a", "b", "c"}
	h := []Event{{1, "RunStarted", ""}, {2, "StepScheduled", "a"}, {3, "StepCompleted", "a"}, {4, "StepScheduled", "b"}}
	for i := range h {
		if a, b := Decide(steps, h[:i+1]), Decide(steps, h[:i+1]); !reflect.DeepEqual(a, b) {
			t.Fatalf("prefix %d: %v vs %v", i, a, b)
		}
	}
}
