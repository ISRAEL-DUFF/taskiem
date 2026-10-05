package termii

import (
	"context"
	"testing"

	"github.com/israel-duff/taskiem/connectors/internal/fixture"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

func call(t *testing.T, action string, input map[string]any, exchanges ...string) (connector.Response, error) {
	t.Helper()
	var exs []fixture.Exchange
	for _, n := range exchanges {
		exs = append(exs, fixture.Load(t, n))
	}
	srv := fixture.Serve(t, exs...)
	return New(Options{BaseURL: srv.URL}).Actions[action].Execute(context.Background(), connector.Request{
		Input: input, Credentials: map[string]string{"api_key": "TLtest", "sender_id": "Taskiem"}, HTTP: srv.Client(),
	})
}

func TestRegisterAndClass(t *testing.T) {
	c := New(Options{})
	if err := connector.NewRegistry().Register(c); err != nil {
		t.Fatal(err)
	}
	if c.Manifest.Actions["send_sms"].Class != effects.UnsafeWrite {
		t.Error("send_sms must be unsafe_write: Termii has no idempotency key")
	}
}

func TestSendSMSUsesConnectionSender(t *testing.T) {
	r, err := call(t, "send_sms", map[string]any{"to": "2348012345678", "sms": "Payroll PR-10 paid", "channel": "dnd"}, "send_sms")
	if err != nil || r.Output.(map[string]any)["message_id"] != "9122821270554876574" {
		t.Errorf("%v %v", r.Output, err)
	}
}

func TestSendSMSRejectedIsFatal(t *testing.T) {
	_, err := call(t, "send_sms", map[string]any{"to": "2348012345678", "sms": "x", "from": "Unknown"}, "send_sms_bad_sender")
	if effects.Classify(err) != effects.KindFatal {
		t.Errorf("want fatal, got %v", err)
	}
}

func TestBalance(t *testing.T) {
	r, err := call(t, "check_balance", nil, "balance")
	if err != nil || r.Output.(map[string]any)["balance"] != 1200.5 {
		t.Errorf("%v %v", r.Output, err)
	}
}
