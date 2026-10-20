package telegram_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/egress"
	"github.com/israel-duff/taskiem/engine/telegram"
	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
)

// quiet keeps the egress log out of test output.
var quiet = &egress.Guard{Logger: slog.New(slog.DiscardHandler)}

// client reaches the fake through the egress guard New builds for a
// loopback Bot API URL.
func client(t *testing.T) (*telegram.Client, *telegramtest.Bot) {
	bot := telegramtest.New(t, token)
	p := telegram.New(nil, telegram.Config{BotToken: token, APIURL: bot.URL, BotUsername: "taskiem_test_bot"}, quiet, nil)
	return p.Client, bot
}

func noToken(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), token) {
		t.Errorf("the error shows the token: %v", err)
	}
}

func TestClientSend(t *testing.T) {
	ctx := context.Background()
	c, bot := client(t)
	rows := [][]telegram.InlineButton{{{Text: "Approve", Data: "c:a"}}, {{Text: "Reject", Data: "c:b"}}}
	id, err := c.SendMessage(ctx, 777, "Pay <b>Ada</b>?", rows)
	if err != nil || id == 0 {
		t.Fatal(id, err)
	}
	s := bot.Last(t, 777)
	if s.Text != "Pay <b>Ada</b>?" || s.MessageID != id || len(s.Keyboard) != 2 || s.Keyboard[1][0] != (telegram.InlineButton{Text: "Reject", Data: "c:b"}) {
		t.Fatalf("%+v", s)
	}
	long := strings.Repeat("😀", 3000) // 6000 UTF-16 units
	if _, err := c.SendMessage(ctx, 777, long, nil); err != nil {
		t.Fatal(err)
	}
	if s := bot.Last(t, 777); !strings.HasSuffix(s.Text, "…") || len([]rune(s.Text)) != 2048 || s.Keyboard != nil {
		t.Errorf("clipped to %d runes", len([]rune(s.Text)))
	}
	if _, err := c.SendMessage(ctx, 777, "x", [][]telegram.InlineButton{{{Text: "a", Data: strings.Repeat("d", 65)}}}); err == nil {
		t.Error("65 bytes of callback data accepted")
	}
	if _, err := c.SendMessage(ctx, 777, " ", nil); err == nil {
		t.Error("empty text accepted")
	}

	pid, err := c.SendPhoto(ctx, 777, "https://ada.shops.test/qr.png", "*Ada's Kitchen* https://ada.shops.test", nil)
	if err != nil || pid == 0 {
		t.Fatal(pid, err)
	}
	if p := bot.Photos(777); len(p) != 1 || p[0].Photo != "https://ada.shops.test/qr.png" || p[0].Text != "Ada's Kitchen https://ada.shops.test" || len(p[0].Entities) != 1 {
		t.Fatalf("%+v", p)
	}
	if _, err := c.SendPhoto(ctx, 777, "http://ada.shops.test/qr.png", "", nil); err == nil {
		t.Error("a photo URL that is not https")
	}

	if err := c.RemoveKeyboard(ctx, 777, id); err != nil {
		t.Fatal(err)
	}
	if e := bot.Calls("editMessageReplyMarkup"); len(e) != 1 || e[0].MessageID != id || e[0].Keyboard == nil || len(e[0].Keyboard) != 0 {
		t.Errorf("%+v", e)
	}
	if err := c.RemoveKeyboard(ctx, 777, 9999); err == nil {
		t.Error("editing an unknown message")
	} else {
		noToken(t, err)
	}
	if err := c.AnswerCallback(ctx, "q1", "Approved"); err != nil {
		t.Fatal(err)
	}
	if a := bot.Calls("answerCallbackQuery"); len(a) != 1 || a[0].CallbackID != "q1" || a[0].Text != "Approved" {
		t.Errorf("%+v", a)
	}
	if err := c.SetWebhook(ctx, "https://taskiem.example/telegram/webhook", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if w := bot.Webhook(); w.URL != "https://taskiem.example/telegram/webhook" || w.Secret != "s3cret" || strings.Join(w.AllowedUpdates, ",") != "message,callback_query" {
		t.Errorf("%+v", w)
	}
	if u, err := c.GetMe(ctx); err != nil || u != "taskiem_test_bot" {
		t.Error(u, err)
	}
}

func TestClientErrors(t *testing.T) {
	ctx := context.Background()
	c, bot := client(t)
	bot.FailNext(403, 0)
	_, err := c.SendMessage(ctx, 777, "hi", nil)
	if !errors.Is(err, telegram.ErrBlocked) {
		t.Fatalf("403: %v", err)
	}
	noToken(t, err)
	bot.FailNext(429, 31)
	_, err = c.SendMessage(ctx, 777, "hi", nil)
	var ra *telegram.RetryAfterError
	if !errors.As(err, &ra) || ra.After != 31*time.Second {
		t.Fatalf("429: %v", err)
	}
	var delay interface{ RetryAfterDelay() time.Duration }
	if !errors.As(err, &delay) || delay.RetryAfterDelay() != 31*time.Second {
		t.Error("no RetryAfterDelay")
	}
	noToken(t, err)
	if _, err := c.SendMessage(ctx, 777, "hi", nil); err != nil {
		t.Fatalf("only the next send fails: %v", err)
	}
	bot.Block(888, true)
	if _, err := c.SendMessage(ctx, 888, "hi", nil); !errors.Is(err, telegram.ErrBlocked) {
		t.Errorf("blocked chat: %v", err)
	}

	// A wrong token, and a server that is gone: the token stays out.
	wrong := *c
	wrong.Token = "999:BBBwrongTokenForTestsOnly_abcdefghij"
	_, err = wrong.GetMe(ctx)
	var ae *telegram.APIError
	if !errors.As(err, &ae) || ae.Status != 401 || strings.Contains(err.Error(), wrong.Token) {
		t.Errorf("wrong token: %v", err)
	}
	bot.Close()
	for _, err := range []error{
		func() error { _, err := c.SendMessage(ctx, 777, "hi", nil); return err }(),
		func() error { _, err := c.Download(ctx, "voice/file_1.oga", 1<<20); return err }(),
	} {
		if err == nil {
			t.Fatal("a closed server answered")
		}
		noToken(t, err)
	}
}

func TestClientFiles(t *testing.T) {
	ctx := context.Background()
	c, bot := client(t)
	audio := []byte(strings.Repeat("OggS", 256))
	bot.SetFile("F1", "voice/file_1.oga", audio, 0)
	path, size, err := c.FileInfo(ctx, "F1")
	if err != nil || path != "voice/file_1.oga" || size != int64(len(audio)) {
		t.Fatal(path, size, err)
	}
	data, err := c.Download(ctx, path, 4096)
	if err != nil || string(data) != string(audio) || bot.Downloads() != 1 {
		t.Fatal(len(data), err)
	}
	if _, err := c.Download(ctx, path, 100); !errors.Is(err, telegram.ErrFileTooLarge) {
		t.Errorf("cap: %v", err)
	}
	if _, _, err := c.FileInfo(ctx, "nope"); err == nil {
		t.Error("unknown file")
	} else {
		noToken(t, err)
	}
	if _, err := c.Download(ctx, "voice/missing.oga", 4096); err == nil {
		t.Error("missing file downloads")
	} else {
		noToken(t, err)
	}
	for _, bad := range []string{"", "/etc/passwd", "../bot/x", "a?b"} {
		if _, err := c.Download(ctx, bad, 4096); err == nil {
			t.Errorf("%q downloads", bad)
		}
	}
}
