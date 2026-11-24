package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// A list answer's rows in a chat: ten at most, each pressable action a
// button named by its row; on WhatsApp a list message whose rows name the
// run and the row; on Slack the buttons are one actions block.
func TestToolListRowsDrawn(t *testing.T) {
	a := madeAnswer{Type: "list", Title: "Open tickets"}
	for i := range 12 {
		a.Rows = append(a.Rows, answerRow{Title: fmt.Sprintf("A rather long ticket title %d", i), Sub: fmt.Sprintf("T-%d", i),
			Actions: []answerBtn{{Label: "Assign to me", Token: uuid.NewString()}, {Label: "Close", Tool: "gone"}}})
	}
	if !rowsPressable(a) || rowsPressable(madeAnswer{Type: "list", Rows: []answerRow{{Title: "x"}}}) || rowsPressable(madeAnswer{Type: "card"}) {
		t.Fatal("pressable")
	}
	bs := rowButtons(a)
	if len(bs) != 10 || !strings.HasPrefix(bs[0].ID, "tb:") || bs[0].Title != "A rather long ticke… · Assign to me" {
		t.Fatalf("buttons: %+v", bs)
	}
	run := uuid.New()
	l := answerList(lang.EN, run, a)
	rows := l.Rows()
	if len(rows) != 10 || rows[3].ID != "tr:"+run.String()+":3" || rows[3].Description != "T-3" || l.Button != "Choose" {
		t.Fatalf("list: %+v", l)
	}
	if head := answerListHead(lang.EN, a); !strings.Contains(head, "*Open tickets*") || !strings.Contains(head, "and 2 more") {
		t.Errorf("head: %q", head)
	}
	_, in, err := slackMessage("sl:T1:U1", whatsapp.Message{Text: "Open tickets", Buttons: bs})
	if err != nil {
		t.Fatal(err)
	}
	blocks := in["blocks"].([]any)
	els := blocks[1].(map[string]any)["elements"].([]any)
	if len(els) != 10 || els[0].(map[string]any)["value"] != bs[0].ID || !strings.HasPrefix(els[0].(map[string]any)["action_id"].(string), slackReply+".") {
		t.Errorf("slack: %+v", blocks)
	}
}
