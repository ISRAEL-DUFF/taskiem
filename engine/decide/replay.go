package decide

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/israel-duff/taskiem/engine/expr"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Verify replays decide() over a recorded history and checks that every
// block of decide-written events is exactly what decide produces from the
// events before it (build plan: determinism suite). Payloads are compared
// in canonical JSON, byte for byte.
func Verify(def *wd.Definition, h []history.Event) error {
	for k := 1; k < len(h); k++ {
		if h[k].Origin != history.OriginDecide || h[k-1].Origin == history.OriginDecide {
			continue
		}
		end := k
		for end < len(h) && h[end].Origin == history.OriginDecide {
			end++
		}
		want, err := Decide(def, h[:k])
		if err != nil {
			return fmt.Errorf("replay before seq %d: %w", h[k].Seq, err)
		}
		got := h[k:end]
		if len(want) != len(got) {
			return fmt.Errorf("replay before seq %d: decide produced %d events, history has %d", h[k].Seq, len(want), len(got))
		}
		for i := range want {
			if err := same(want[i], got[i]); err != nil {
				return fmt.Errorf("replay at seq %d: %w", got[i].Seq, err)
			}
		}
	}
	return nil
}

func same(want NewEvent, got history.Event) error {
	if want.Type != got.Type || want.StepID != got.StepID || want.Attempt != got.Attempt {
		return fmt.Errorf("want %s(%s)#%d, recorded %s(%s)#%d", want.Type, want.StepID, want.Attempt, got.Type, got.StepID, got.Attempt)
	}
	var wantRaw []byte
	if want.Payload != nil {
		b, err := json.Marshal(want.Payload)
		if err != nil {
			return err
		}
		wantRaw = b
	}
	a, err := canonical(wantRaw)
	if err != nil {
		return err
	}
	b, err := canonical(got.Payload)
	if err != nil {
		return err
	}
	if !bytes.Equal(a, b) {
		return fmt.Errorf("%s(%s) payload differs:\n  replay:   %s\n  recorded: %s", got.Type, got.StepID, a, b)
	}
	return nil
}

// canonical re-encodes JSON with sorted keys and normalised numbers, so
// Postgres jsonb storage does not affect the comparison.
func canonical(raw []byte) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return []byte("null"), nil
	}
	v, err := expr.DecodeJSON(raw)
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
