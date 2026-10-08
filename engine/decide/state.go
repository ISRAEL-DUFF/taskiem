package decide

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/wd"
)

// State is a run's history folded up to one event, so a long run (a foreach
// over thousands of items) is decided again by folding only what is new,
// not by re-reading and re-parsing everything before it. Deciding from a
// State gives exactly what Decide gives from the same history.
type State struct {
	d *decider
}

// ErrGap means events handed to Extend do not follow the folded ones.
var ErrGap = errors.New("decide: events do not follow the folded history")

// Fold folds a history that starts with RunStarted.
func Fold(def *wd.Definition, h []history.Event) (*State, error) {
	if len(h) == 0 || h[0].Type != history.RunStarted {
		return nil, fmt.Errorf("decide: history must start with RunStarted")
	}
	d := &decider{def: def, facts: map[string]*facts{}, complSeq: map[string]int64{}}
	if err := d.load(h); err != nil {
		return nil, err
	}
	return &State{d: d}, nil
}

// Seq is the sequence number of the last folded event.
func (s *State) Seq() int64 { return s.d.seq }

// Extend folds events that directly follow the folded ones. On error the
// State must be discarded: it may hold part of the events.
func (s *State) Extend(h []history.Event) error {
	if len(h) > 0 && h[0].Seq != s.d.seq+1 {
		return fmt.Errorf("%w: next is %d, got %d", ErrGap, s.d.seq+1, h[0].Seq)
	}
	for i := 1; i < len(h); i++ {
		if h[i].Seq != h[i-1].Seq+1 {
			return fmt.Errorf("%w: %d after %d", ErrGap, h[i].Seq, h[i-1].Seq)
		}
	}
	return s.d.load(h)
}

// Decide returns the events that follow from the folded history. The State
// is unchanged: the events are folded in once they are recorded.
func (s *State) Decide() ([]NewEvent, error) {
	if s.d.terminal {
		return nil, nil
	}
	d := s.d.clone()
	for pass := 0; pass < MaxPasses; pass++ {
		before := len(d.out)
		d.run()
		if len(d.out) == before || d.terminal {
			return d.out, nil
		}
	}
	return nil, fmt.Errorf("decide: no fixpoint after %d passes", MaxPasses)
}

// clone copies everything a decision changes. Values decoded from history
// (trigger, outputs, payloads) are never modified in place, so they are
// shared.
func (d *decider) clone() *decider {
	c := *d
	c.facts = make(map[string]*facts, len(d.facts))
	for k, f := range d.facts {
		c.facts[k] = f.clone()
	}
	c.order = slices.Clone(d.order)
	c.complSeq = maps.Clone(d.complSeq)
	c.out = nil
	c.memo = nil
	return &c
}

func (f *facts) clone() *facts {
	c := *f
	c.scheduled = maps.Clone(f.scheduled)
	c.outcome = maps.Clone(f.outcome)
	c.failures = maps.Clone(f.failures)
	c.retries = maps.Clone(f.retries)
	c.intents = maps.Clone(f.intents)
	c.timerFired = slices.Clone(f.timerFired)
	c.approvalReqs = slices.Clone(f.approvalReqs)
	c.approvalReqAt = slices.Clone(f.approvalReqAt)
	return &c
}

// Clone returns an independent copy, to extend without changing s.
func (s *State) Clone() *State { return &State{d: s.d.clone()} }
