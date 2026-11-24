package telegram_test

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/telegram"
)

// A reply carries the quoted message's words, and a forwarded message
// says it was forwarded (paying people H7: forward or reply "pay").
func TestParseForwardAndQuotedWords(t *testing.T) {
	u, err := telegram.Parse([]byte(`{"update_id":7,"message":{"message_id":12,"from":{"id":42,"first_name":"Ada"},"chat":{"id":42,"type":"private"},
		"text":"pay","reply_to_message":{"message_id":9,"chat":{"id":42,"type":"private"},"caption":"0123456789 GTBank"}}}`))
	if err != nil || u.ReplyTo != 9 || u.ReplyText != "0123456789 GTBank" || u.Forwarded {
		t.Fatalf("a reply: %+v, %v", u, err)
	}
	for _, body := range []string{
		`{"update_id":8,"message":{"message_id":13,"from":{"id":42,"first_name":"Ada"},"chat":{"id":42,"type":"private"},"text":"0123456789 GTBank","forward_origin":{"type":"hidden_user","sender_user_name":"Chidi","date":1}}}`,
		`{"update_id":9,"message":{"message_id":14,"from":{"id":42,"first_name":"Ada"},"chat":{"id":42,"type":"private"},"text":"0123456789 GTBank","forward_date":1700000000}}`,
	} {
		if u, err := telegram.Parse([]byte(body)); err != nil || !u.Forwarded || u.Text != "0123456789 GTBank" {
			t.Fatalf("forwarded: %+v, %v", u, err)
		}
	}
	if u, _ := telegram.Parse([]byte(`{"update_id":10,"message":{"message_id":15,"from":{"id":42,"first_name":"Ada"},"chat":{"id":42,"type":"private"},"text":"hi"}}`)); u.Forwarded {
		t.Fatalf("not forwarded: %+v", u)
	}
}
