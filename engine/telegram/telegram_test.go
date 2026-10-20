package telegram_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/telegram"
	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
)

const token = "123456789:AAHfakeTokenForTestsOnly_abcdefghijk"

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestConfigFromEnv(t *testing.T) {
	if c, err := telegram.ConfigFromEnv(env(nil)); c != nil || err != nil {
		t.Fatalf("unset: %v %v", c, err)
	}
	good := map[string]string{"TASKIEM_TELEGRAM_BOT_TOKEN": token, "TASKIEM_TELEGRAM_WEBHOOK_SECRET": "s3cret_-X",
		"TASKIEM_TELEGRAM_BOT_USERNAME": "@taskiem_bot"}
	c, err := telegram.ConfigFromEnv(env(good))
	if err != nil || c.BotToken != token || c.WebhookSecret != "s3cret_-X" || c.BotUsername != "taskiem_bot" || c.APIURL != telegram.DefaultAPIURL {
		t.Fatalf("%+v %v", c, err)
	}
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for a, b := range good {
			m[a] = b
		}
		if v == "" {
			delete(m, k)
		} else {
			m[k] = v
		}
		return m
	}
	c, err = telegram.ConfigFromEnv(env(with("TASKIEM_TELEGRAM_API_URL", "http://127.0.0.1:9999/")))
	if err != nil || c.APIURL != "http://127.0.0.1:9999" {
		t.Fatalf("override: %+v %v", c, err)
	}
	for name, tc := range map[string]struct {
		m    map[string]string
		want string
	}{
		"no secret":     {with("TASKIEM_TELEGRAM_WEBHOOK_SECRET", ""), "TASKIEM_TELEGRAM_WEBHOOK_SECRET"},
		"no username":   {with("TASKIEM_TELEGRAM_BOT_USERNAME", ""), "TASKIEM_TELEGRAM_BOT_USERNAME"},
		"bad secret":    {with("TASKIEM_TELEGRAM_WEBHOOK_SECRET", "has space"), "TASKIEM_TELEGRAM_WEBHOOK_SECRET"},
		"long secret":   {with("TASKIEM_TELEGRAM_WEBHOOK_SECRET", strings.Repeat("a", 257)), "TASKIEM_TELEGRAM_WEBHOOK_SECRET"},
		"short name":    {with("TASKIEM_TELEGRAM_BOT_USERNAME", "abcd"), "TASKIEM_TELEGRAM_BOT_USERNAME"},
		"bad name":      {with("TASKIEM_TELEGRAM_BOT_USERNAME", "taskiem-bot"), "TASKIEM_TELEGRAM_BOT_USERNAME"},
		"bad token":     {with("TASKIEM_TELEGRAM_BOT_TOKEN", "not/a/token"), "TASKIEM_TELEGRAM_BOT_TOKEN"},
		"bad url":       {with("TASKIEM_TELEGRAM_API_URL", "ftp://x"), "TASKIEM_TELEGRAM_API_URL"},
		"url with user": {with("TASKIEM_TELEGRAM_API_URL", "https://u:p@x"), "TASKIEM_TELEGRAM_API_URL"},
	} {
		c, err := telegram.ConfigFromEnv(env(tc.m))
		if err == nil || c != nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v %v", name, c, err)
			continue
		}
		if strings.Contains(err.Error(), "not/a/token") || strings.Contains(err.Error(), token) {
			t.Errorf("%s: the error shows the token: %v", name, err)
		}
	}
}

func TestAddresses(t *testing.T) {
	if a := telegram.Address(5551234); a != "tg:5551234" {
		t.Fatal(a)
	}
	if id, ok := telegram.ParseAddress("tg:9223372036854775807"); !ok || id != 9223372036854775807 {
		t.Fatal(id, ok)
	}
	for _, bad := range []string{"", "tg:", "tg:0", "tg:-1", "tg:012", "tg:1a", "tg: 1", "+2348012345678", "TG:1", "tg:9223372036854775808", "tg:123456789012345678901"} {
		if _, ok := telegram.ParseAddress(bad); ok || telegram.IsAddress(bad) {
			t.Errorf("%q parses", bad)
		}
	}
	if !telegram.IsAddress(telegram.Address(1)) {
		t.Error("tg:1")
	}
}

func TestVerifySecret(t *testing.T) {
	if !telegram.VerifySecret("abc", "abc") {
		t.Fatal("own secret refused")
	}
	for _, c := range [][2]string{{"abd", "abc"}, {"", "abc"}, {"abcd", "abc"}, {"", ""}, {"x", ""}} {
		if telegram.VerifySecret(c[0], c[1]) {
			t.Errorf("%q accepted for %q", c[0], c[1])
		}
	}
}

func TestParse(t *testing.T) {
	u, err := telegram.Parse(telegramtest.Message(10, 777, "ada_o", "status"))
	if err != nil || u.ID != 10 || u.Kind != "message" || u.FromID != 777 || u.Username != "ada_o" || u.Language != "en" ||
		u.ChatID != 777 || u.ChatType != "private" || u.MessageID != 10 || u.Text != "status" || u.Voice != nil || !u.Private() {
		t.Fatalf("message: %+v %v", u, err)
	}
	u, err = telegram.Parse(telegramtest.Callback(11, 777, 42, "q1", "c:abc"))
	if err != nil || u.Kind != "callback" || u.CallbackID != "q1" || u.CallbackData != "c:abc" || u.FromID != 777 || u.ChatID != 777 || u.MessageID != 42 || !u.Private() {
		t.Fatalf("callback: %+v %v", u, err)
	}
	u, err = telegram.Parse(telegramtest.VoiceMessage(12, 777, "F1", 7))
	if err != nil || u.Kind != "message" || u.Voice == nil || u.Voice.FileID != "F1" || !u.Voice.Voice || u.Voice.Duration != 7 || u.Voice.MimeType != "audio/ogg" {
		t.Fatalf("voice: %+v %v", u, err)
	}
	u, err = telegram.Parse([]byte(`{"update_id":13,"message":{"message_id":5,"from":{"id":777},"chat":{"id":777,"type":"private"},
		"caption":"hear this","audio":{"file_id":"A1","duration":3,"mime_type":"audio/mpeg","file_size":2048}}}`))
	if err != nil || u.Text != "hear this" || u.Voice == nil || u.Voice.Voice || u.Voice.MimeType != "audio/mpeg" || u.Voice.Size != 2048 {
		t.Fatalf("audio: %+v %v", u, err)
	}
	u, err = telegram.Parse(telegramtest.PhotoMessage(19, 777, "P1", "Jollof rice 2000"))
	if err != nil || u.Kind != "message" || u.Text != "Jollof rice 2000" || u.Voice != nil || u.Photo == nil || u.Photo.FileID != "P1" || u.Photo.MimeType != "image/jpeg" {
		t.Fatalf("photo: %+v %+v %v", u, u.Photo, err)
	}
	u, err = telegram.Parse(telegramtest.PhotoMessage(20, 777, "P2", ""))
	if err != nil || u.Text != "" || u.Photo == nil || u.Photo.FileID != "P2" {
		t.Fatalf("photo without a caption: %+v %v", u, err)
	}
	u, err = telegram.Parse([]byte(`{"update_id":21,"message":{"message_id":7,"from":{"id":777},"chat":{"id":777,"type":"private"},
		"caption":"logo","document":{"file_id":"D1","mime_type":"image/png","file_size":4096}}}`))
	if err != nil || u.Text != "logo" || u.Photo == nil || u.Photo.FileID != "D1" || u.Photo.MimeType != "image/png" || u.Photo.Size != 4096 {
		t.Fatalf("image as a file: %+v %v", u, err)
	}
	u, err = telegram.Parse([]byte(`{"update_id":22,"message":{"message_id":8,"from":{"id":777},"chat":{"id":777,"type":"private"},
		"document":{"file_id":"D2","mime_type":"application/pdf"}}}`))
	if err != nil || u.Photo != nil {
		t.Fatalf("a PDF is not a photo: %+v %v", u, err)
	}
	u, err = telegram.Parse([]byte(`{"update_id":14,"message":{"message_id":6,"from":{"id":777,"username":"ada"},"chat":{"id":-100123,"type":"supergroup"},"text":"hi"}}`))
	if err != nil || u.Kind != "message" || u.ChatType != "supergroup" || u.ChatID != -100123 || u.Private() {
		t.Fatalf("group: %+v %v", u, err)
	}
	for name, body := range map[string]string{
		"edit":    `{"update_id":15,"edited_message":{"message_id":6,"from":{"id":777},"chat":{"id":777,"type":"private"},"text":"x"}}`,
		"channel": `{"update_id":16,"channel_post":{"message_id":6,"chat":{"id":-1001,"type":"channel"},"text":"x"}}`,
		"bot":     `{"update_id":17,"message":{"message_id":6,"from":{"id":9,"is_bot":true},"chat":{"id":9,"type":"private"},"text":"x"}}`,
		"no from": `{"update_id":18,"message":{"message_id":6,"chat":{"id":9,"type":"private"},"text":"x"}}`,
	} {
		if u, err := telegram.Parse([]byte(body)); err != nil || u.Kind != "other" || u.ID == 0 {
			t.Errorf("%s: %+v %v", name, u, err)
		}
	}
	for _, bad := range []string{``, `{`, `[]`, `{"message":{}}`, `{"update_id":"x"}`} {
		if _, err := telegram.Parse([]byte(bad)); err == nil {
			t.Errorf("%q parses", bad)
		}
	}
}
