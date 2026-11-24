package runtime

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// The logger a connector action is handed takes the step's secret values
// out of everything logged through it: message, attributes, groups,
// errors, and attributes added with With. Secrets the step comes to hold
// later (a rotated token) are scrubbed too.
func TestStepLoggerScrubsSecrets(t *testing.T) {
	const key, later = "fixture-key-9f8e7d6c5b4a39281706f5e4d3c2b1a0", "tok_rotated_0123456789abcdef"
	var buf bytes.Buffer
	w := &Worker{Logger: slog.New(slog.NewTextHandler(&buf, nil))}
	p := &plan{scrub: []string{key}}
	l := w.stepLogger(p)
	l.Info("connector log", "line", "Authorization: Bearer "+key)
	l.Info("sent with "+key, "err", errors.New("401 for "+key), slog.Group("req", "token", key, "n", 3))
	l.With("creds", key).WithGroup("g").Warn("again", "k", key)
	p.scrub = append(p.scrub, later)
	l.Info("rotated", "token", later)
	out := buf.String()
	if strings.Contains(out, key) || strings.Contains(out, later) {
		t.Errorf("the log carries a secret:\n%s", out)
	}
	if strings.Count(out, "[secret]") < 6 || !strings.Contains(out, "req.n=3") {
		t.Errorf("unexpected log:\n%s", out)
	}
	if (&Worker{}).stepLogger(p) != nil {
		t.Error("no logger stays no logger")
	}
}
