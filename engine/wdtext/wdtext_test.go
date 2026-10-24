package wdtext_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/builtin"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdtext"
	"github.com/israel-duff/taskiem/templates"
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

// formDoc is a form trigger with access and a form step to someone.
func formDoc(access, to, extra string) string {
	verify := ""
	if access == "link" {
		verify = `"verify":"phone",`
	}
	return `{"schema":"wd/v1","id":"wf_f","version":1,"name":"f",
	 "trigger":{"type":"form","config":{"title":"Apply for a loan","access":"` + access + `",` + verify + `"after":{"mode":"message","message":"Thanks"}}},
	 "inputs":{"schema":{"type":"object","properties":{"phone":{"type":"string","x-pii":"phone"},"email":{"type":"string","x-pii":"email"}}}},
	 "steps":[{"id":"ask","type":"form","config":{"title":"Confirm your bank","to":` + to + `,` + extra + `,
	   "schema":{"type":"object","properties":{"bank":{"type":"string"}}}}}]}`
}

func TestDescribeForms(t *testing.T) {
	for _, c := range []struct{ access, to, extra, want string }{
		{"public", `{"phone":"=trigger.body.phone"}`, `"timeout":"72h"`,
			"1. When someone fills in the form “Apply for a loan”\n2. Ask someone, by their phone number, to fill in “Confirm your bank” (waits up to 72 hours)"},
		{"members", `{"email":"=trigger.body.email"}`, `"on_timeout":"continue"`,
			"1. When someone fills in the form “Apply for a loan” (members only)\n2. Ask someone, by their email address, to fill in “Confirm your bank”"},
		{"link", `{"role":"credit_officer"}`, `"on_timeout":"fail"`,
			"1. When someone fills in the form “Apply for a loan” (only with a link sent to them)\n2. Ask members with the credit officer role to fill in “Confirm your bank”"},
	} {
		def, err := wd.Load([]byte(formDoc(c.access, c.to, c.extra)))
		if err != nil {
			t.Fatal(err)
		}
		if got := wdtext.Text(wdtext.Describe(def, nil)); got != c.want {
			t.Errorf("got\n%s\nwant\n%s", got, c.want)
		}
	}
}

// A template step reads as the template it uses (decision 0047).
func TestDescribeTemplateStep(t *testing.T) {
	doc := `{"schema":"wd/v1","id":"wf_x","version":1,"name":"x","trigger":{"type":"manual"},
	 "steps":[{"id":"pay","type":"template","template":"run-payroll","params":{"business_name":"Ada Foods"}},
	          {"id":"inv","type":"template","template":"send-invoice","needs":["pay"],"input":{"name":"Ada","amount_naira":5000,"for":"Rice"}}]}`
	def, err := wd.Load([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	lines := wdtext.Describe(def, connector.NewRegistry())
	var got []string
	for _, l := range lines {
		got = append(got, l.Text)
	}
	text := strings.Join(got, "\n")
	if !strings.Contains(text, "Use the template: Run payroll from any payments account, which moves money, waits for a person to approve or answer and waits for something to happen") ||
		!strings.Contains(text, "Use the template: Send an invoice and hear when it is paid, which changes things in your services and waits for something to happen") {
		t.Errorf("read back:\n%s", text)
	}
	// A step that uses one of the business's own workflows (decision 0048)
	// reads as that workflow, found through the resolver.
	const id = "2f1a4d8e-3c0b-4e6f-9a21-5b7c8d9e0f12"
	own := templates.Template{ID: id, Title: "Double it", Summary: "S", Description: "D", Category: "workflow", Callable: true,
		Outputs: []templates.Output{{Name: "kobo", Type: "integer", Title: "Total", Description: "Twice", Value: "=steps.total.output.kobo"}},
		Definition: json.RawMessage(`{"schema":"wd/v1","id":"wf_d","version":1,"name":"Double it","trigger":{"type":"manual"},
		  "inputs":{"schema":{"type":"object","properties":{"amount_kobo":{"type":"integer"}}}},
		  "steps":[{"id":"total","type":"transform","config":{"output":{"kobo":"=trigger.body.amount_kobo * 2"}}}]}`)}
	def, err = wd.Load([]byte(`{"schema":"wd/v1","id":"wf_y","version":1,"name":"y","trigger":{"type":"manual"},
	 "steps":[{"id":"sub","type":"template","workflow":"` + id + `","input":{"amount_kobo":21}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	lines = wdtext.DescribeWith(def, connector.NewRegistry(), lang.EN, func(want string) (*templates.Template, error) {
		if want == id {
			return &own, nil
		}
		return nil, errors.New("unknown")
	})
	if len(lines) == 0 || lines[len(lines)-1].Text != "Use your workflow: Double it, which only reads" {
		t.Errorf("read back a workflow step: %+v", lines)
	}
	// Hausa too.
	lines = wdtext.DescribeWith(def, connector.NewRegistry(), lang.HA, func(string) (*templates.Template, error) { return &own, nil })
	if len(lines) == 0 || lines[len(lines)-1].Text != "Yi amfani da aikinka: Double it, wanda karantawa kawai yake" {
		t.Errorf("read back in Hausa: %+v", lines)
	}
}
