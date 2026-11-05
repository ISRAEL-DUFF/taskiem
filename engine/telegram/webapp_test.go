package telegram_test

import (
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/telegram"
)

// A Mini App's initData is taken only signed for this bot, unchanged and
// fresh.
func TestVerifyInitData(t *testing.T) {
	now := time.Now()
	data := telegram.SignInitData("123:bot", 7001, now.Add(-time.Minute))
	if id, err := telegram.VerifyInitData("123:bot", data, 10*time.Minute, now); err != nil || id != 7001 {
		t.Fatalf("good: %d %v", id, err)
	}
	if _, err := telegram.VerifyInitData("999:other", data, 10*time.Minute, now); err == nil {
		t.Error("another bot's")
	}
	if _, err := telegram.VerifyInitData("123:bot", strings.Replace(data, "7001", "7002", 1), 10*time.Minute, now); err == nil {
		t.Error("changed")
	}
	old := telegram.SignInitData("123:bot", 7001, now.Add(-time.Hour))
	if _, err := telegram.VerifyInitData("123:bot", old, 10*time.Minute, now); err == nil {
		t.Error("old")
	}
	if _, err := telegram.VerifyInitData("123:bot", "", 10*time.Minute, now); err == nil {
		t.Error("none")
	}
}
