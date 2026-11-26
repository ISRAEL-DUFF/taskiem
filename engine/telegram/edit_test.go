package telegram_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/israel-duff/taskiem/engine/telegram"
	"github.com/israel-duff/taskiem/engine/telegram/telegramtest"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// A message sent is changed in place (editMessageText), as an edited
// business message is (docs/spec/chat-previews-and-editing.md §3.3); one
// that cannot be edited any more is refused, so the follow-up goes instead.
func TestEditMessage(t *testing.T) {
	ctx := context.Background()
	bot := telegramtest.New(t, token)
	p := telegram.New(nil, telegram.Config{BotToken: token, APIURL: bot.URL, BotUsername: "taskiem_test_bot"}, quiet, nil)
	id, err := p.Client.SendMessage(ctx, 777, "[Dangote] Musa: Rice is ₦45,000", nil)
	if err != nil {
		t.Fatal(err)
	}
	addr := telegram.Address(777)
	if err := p.Edit(ctx, addr, strconv.FormatInt(id, 10), whatsapp.Message{Text: "[Dangote] Musa: Rice is *₦44,000*"}); err != nil {
		t.Fatal(err)
	}
	edits := bot.Calls("editMessageText")
	if len(edits) != 1 || edits[0].MessageID != id || edits[0].Text != "[Dangote] Musa: Rice is ₦44,000" || len(edits[0].Entities) != 1 {
		t.Fatalf("the edit: %+v", edits)
	}
	bot.FailEdits(true)
	if err := p.Edit(ctx, addr, strconv.FormatInt(id, 10), whatsapp.Message{Text: "again"}); err == nil {
		t.Fatal("an edit Telegram refuses is not refused")
	}
	if err := p.Edit(ctx, addr, "not-a-number", whatsapp.Message{Text: "again"}); err == nil {
		t.Fatal("a bad message id is not refused")
	}
}
