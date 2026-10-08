package decide

import (
	"errors"
	"testing"

	"github.com/israel-duff/taskiem/engine/history"
)

func TestExtendRefusesGaps(t *testing.T) {
	s := newSim(t, `{"schema":"wd/v1","id":"wf_g","version":1,"name":"g","trigger":{"type":"manual"},
		"steps":[{"id":"a","type":"wait","config":{"duration":"1m"}}],"settings":{}}`, map[string]any{})
	st, err := Fold(s.def, s.h[:1])
	if err != nil {
		t.Fatal(err)
	}
	skipped := s.h[1]
	skipped.Seq = 3
	if err := st.Extend([]history.Event{skipped}); !errors.Is(err, ErrGap) {
		t.Errorf("skipping an event: %v", err)
	}
	st, _ = Fold(s.def, s.h[:1])
	gappy := []history.Event{s.h[1], s.h[1]}
	gappy[1].Seq = 5
	if err := st.Extend(gappy); !errors.Is(err, ErrGap) {
		t.Errorf("a hole inside the events: %v", err)
	}
}

func TestDecideLeavesStateUnchanged(t *testing.T) {
	s := newSim(t, `{"schema":"wd/v1","id":"wf_g","version":1,"name":"g","trigger":{"type":"manual"},
		"steps":[{"id":"a","type":"wait","config":{"duration":"1m"}},{"id":"b","type":"wait","needs":["a"],"config":{"duration":"1m"}}],"settings":{}}`, map[string]any{})
	st, err := Fold(s.def, s.h[:1])
	if err != nil {
		t.Fatal(err)
	}
	first, _ := st.Decide()
	second, _ := st.Clone().Decide()
	third, _ := st.Decide()
	if len(first) == 0 || len(first) != len(second) || len(first) != len(third) || st.Seq() != 1 {
		t.Errorf("deciding changed the state: %d %d %d, seq %d", len(first), len(second), len(third), st.Seq())
	}
}
