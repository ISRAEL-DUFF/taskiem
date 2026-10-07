package whatsapp

import (
	"testing"
	"time"
)

func TestOutboxBackoff(t *testing.T) {
	want := []time.Duration{time.Minute, 4 * time.Minute, 16 * time.Minute, 64 * time.Minute, 256 * time.Minute, 6 * time.Hour, 6 * time.Hour, 6 * time.Hour}
	for i, w := range want {
		if got := OutboxBackoff(i + 1); got != w {
			t.Errorf("attempt %d: %s, want %s", i+1, got, w)
		}
	}
	if OutboxBackoff(0) != time.Minute || OutboxBackoff(100) != 6*time.Hour {
		t.Error("out-of-range attempts")
	}
}
