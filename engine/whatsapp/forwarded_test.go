package whatsapp_test

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// A forwarded message says so (context.forwarded, or
// frequently_forwarded): its words are someone else's, such as account
// details to pay (paying people H7).
func TestParseForwarded(t *testing.T) {
	for _, ctx := range []map[string]any{{"forwarded": true}, {"frequently_forwarded": true}} {
		msg := whatsapptest.Text("0123456789 GTBank Chidi")
		msg["context"] = ctx
		in, err := whatsapp.Parse(whatsapptest.Delivery("PNID", "+2348010000001", "wamid.f1", msg))
		if err != nil || len(in) != 1 || !in[0].Forwarded || in[0].Quoted != "" {
			t.Fatalf("got %+v, %v", in, err)
		}
	}
	in, err := whatsapp.Parse(whatsapptest.Delivery("PNID", "+2348010000001", "wamid.f2", whatsapptest.Text("hi")))
	if err != nil || len(in) != 1 || in[0].Forwarded {
		t.Fatalf("not forwarded: %+v, %v", in, err)
	}
}
