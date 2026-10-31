package telegram_test

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/telegram"
)

// A message quoting another carries the quoted message's id (a reply in
// a linked business's conversation, business links L3).
func TestParseReplyTo(t *testing.T) {
	u, err := telegram.Parse([]byte(`{"update_id":7,"message":{"message_id":12,"from":{"id":42,"first_name":"Ada"},"chat":{"id":42,"type":"private"},
		"text":"Keep 20 for us","reply_to_message":{"message_id":9,"chat":{"id":42,"type":"private"},"text":"[Dangote] Rice is back"}}}`))
	if err != nil || u.Kind != "message" || u.ReplyTo != 9 || u.Text != "Keep 20 for us" {
		t.Fatalf("got %+v, %v", u, err)
	}
	u, err = telegram.Parse([]byte(`{"update_id":8,"message":{"message_id":13,"from":{"id":42,"first_name":"Ada"},"chat":{"id":42,"type":"private"},"text":"hi"}}`))
	if err != nil || u.ReplyTo != 0 {
		t.Fatalf("no reply: %+v, %v", u, err)
	}
}
