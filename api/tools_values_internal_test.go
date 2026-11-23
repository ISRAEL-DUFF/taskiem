package api

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/wd"
)

// A scanned code in a tool's echo reads as its text, never as a map.
func TestToolValuesScanReadsAsText(t *testing.T) {
	got := toolValues(&wd.Definition{}, map[string]any{"code": map[string]any{"text": "6151234567890", "format": "ean13"}})
	if len(got) != 1 || got[0].Value != "6151234567890" {
		t.Fatalf("echo: %+v", got)
	}
}
