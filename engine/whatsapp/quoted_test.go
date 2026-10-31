package whatsapp_test

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// A message quoting another carries the quoted message's id
// (context.id): a reply in a linked business's conversation (business
// links L3).
func TestParseQuoted(t *testing.T) {
	msg := whatsapptest.Text("Keep 20 for us")
	msg["context"] = map[string]any{"from": "15550001111", "id": "wamid.out1"}
	in, err := whatsapp.Parse(whatsapptest.Delivery("PNID", "+2348010000001", "wamid.in1", msg))
	if err != nil || len(in) != 1 || in[0].Quoted != "wamid.out1" || in[0].Text != "Keep 20 for us" {
		t.Fatalf("got %+v, %v", in, err)
	}
	in, err = whatsapp.Parse(whatsapptest.Delivery("PNID", "+2348010000001", "wamid.in2", whatsapptest.Text("hi")))
	if err != nil || len(in) != 1 || in[0].Quoted != "" {
		t.Fatalf("no quote: %+v, %v", in, err)
	}
}
