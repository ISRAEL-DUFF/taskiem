package plain_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/plain"
)

const doc = `{"steps":[{"id":"pay","name":"Pay the supplier","type":"connector"},
  {"id":"branch","type":"branch","config":{"paths":[{"when":"=true","steps":[{"id":"tell","name":"Tell the owner"}]}]}}]}`

func TestProblemsInPlainWords(t *testing.T) {
	st := plain.StepsOf([]byte(doc))
	for _, c := range []struct {
		path, msg, want, fix string
	}{
		{"/steps/pay", `connector "paystack@1" is not available`, "“Pay the supplier” uses Paystack, which is not available in your organisation.", ""},
		{"/steps/0/input", `no active paystack connection in prod: not found`, "“Pay the supplier”: Paystack is not connected.", "/connections?connect=paystack"},
		{"/steps/tell", `steps.pay may not have run before tell; add it to needs: =steps.pay.output`, "“Tell the owner” uses what pay gives before it has run. Make it come after pay.", "#step=tell"},
		{"/steps/pay/input", `missing property 'recipient'`, "“Pay the supplier” needs recipient.", "#step=pay"},
		{"/", `approval policy "high_value" has no active version`, "The approval rules \"high value\" are not switched on yet.", "/policies"},
		{"/steps/pay/when", `ERROR: <input>:1:5: undeclared reference to 'stpes'`, "“Pay the supplier” has a formula Taskiem cannot work out. Open the step to set it again.", "#step=pay"},
		{"/steps/pay", `status 401: Invalid key`, "“Pay the supplier”: the service refused the connection's key. Connect it again with the right key.", "/connections"},
	} {
		got, fix := plain.Problem(c.path, c.msg, st)
		if got != c.want || fix.Path != c.fix {
			t.Errorf("%s %q:\n got %q %q\nwant %q %q", c.path, c.msg, got, fix.Path, c.want, c.fix)
		}
	}
	// Anything else loses its paths and contract names.
	got, _ := plain.Problem("/steps/pay", "something odd at /steps/pay/input in wd/v1", st)
	if strings.Contains(got, "/steps") || strings.Contains(got, "wd/v1") || !strings.HasPrefix(got, "“Pay the supplier”") {
		t.Errorf("fallback: %q", got)
	}
	if got := plain.RunError("Send the text", "context deadline exceeded"); got != "“Send the text”: the service did not answer in time." {
		t.Errorf("run error: %q", got)
	}
}
