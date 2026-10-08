package wdtext_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/builtin"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdtext"
)

func TestSchedule(t *testing.T) {
	for cron, want := range map[string]string{
		"0 10 * * 5":    "Every Friday at 10:00 (Africa/Lagos time)",
		"30 8 * * 1-5":  "Every weekday (Monday to Friday) at 08:30 (Africa/Lagos time)",
		"0 20 * * *":    "Every day at 20:00 (Africa/Lagos time)",
		"0 9 25 * *":    "On the 25th of every month at 09:00 (Africa/Lagos time)",
		"0 9 1 * *":     "On the 1st of every month at 09:00 (Africa/Lagos time)",
		"*/15 * * * *":  "Every 15 minutes",
		"5 */3 * * *":   "Every 3 hours",
		"0 * * * *":     "Every hour, on the hour",
		"0 9 * * 1,3,5": "Every Monday, Wednesday and Friday at 09:00 (Africa/Lagos time)",
		"0 9 1 1 *":     "On the schedule 0 9 1 1 * (Africa/Lagos time)",
	} {
		if got := wdtext.Schedule(cron, ""); got != want {
			t.Errorf("%s: %q, want %q", cron, got, want)
		}
	}
}

func TestDescribe(t *testing.T) {
	reg := connector.NewRegistry()
	if err := builtin.Register(reg, builtin.Options{}); err != nil {
		t.Fatal(err)
	}
	doc := `{"schema":"wd/v1","id":"wf_x","version":1,"name":"x","trigger":{"type":"webhook","config":{"path":"/p","auth":"hmac"}},
	 "steps":[
	  {"id":"approve","type":"approval","config":{"role":"finance_manager","count":1,"timeout":"24h"}},
	  {"id":"pay","type":"foreach","needs":["approve"],"when":"=steps.approve.output.decision == 'approved'","config":{"items":"=trigger.body.rows","max_concurrency":5,"steps":[
	    {"id":"send","type":"connector","connector":"paystack@1","action":"transfer","input":{"amount":"=item.amount","recipient":"=item.r"},"effect":{"idempotency_seed":"=item.id"}},
	    {"id":"check","type":"branch","needs":["send"],"config":{"paths":[{"name":"failed","when":"=steps.send.output.status == 'failed'","steps":[
	      {"id":"tell","type":"connector","connector":"termii@1","action":"send_sms","input":{"to":"=env.p","sms":"x"}}]}]}}]}},
	  {"id":"pause","type":"wait","needs":["pay"],"config":{"duration":"2d"}},
	  {"id":"hook","type":"http","needs":["pause"],"config":{"method":"POST","url":"https://api.example.test/x","class":"unsafe_write"},
	   "on_error":{"steps":[{"id":"oops","type":"transform","name":"Note *the* failure","config":{"output":{}}}]}}]}`
	def, err := wd.Load([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	got := wdtext.Text(wdtext.Describe(def, reg))
	want := strings.Join([]string{
		"1. When your system calls this workflow's web address (checked with hmac)",
		"2. Ask a finance manager to approve (waits up to 24 hours)",
		"3. For each item in the list (at most 5 at a time), only when its condition holds:",
		"   3a. Send transfer with Paystack",
		"   3b. Decide what to do:",
		"      3b.i. If failed:",
		"         3b.i.1. Send SMS with Termii",
		"4. Wait 2 days",
		"5. Call api.example.test (POST)",
		"   If that step fails:",
		"      • Note the failure",
	}, "\n")
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}
