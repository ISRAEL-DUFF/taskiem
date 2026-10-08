package wdtext_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/wdtext"
)

// Every shape the read-back knows, in every language: no message id is
// left showing, and nothing falls back to English.
func TestDescribeInLanguages(t *testing.T) {
	docs := []string{
		`{"schema":"wd/v1","id":"wf_a","version":1,"name":"a","trigger":{"type":"webhook","config":{"path":"/p","auth":"none"}},
		 "steps":[
		  {"id":"a1","type":"approval","config":{"policy":"high_value"}},
		  {"id":"a2","type":"approval","needs":["a1"],"config":{"role":"credit_officer","count":2,"timeout":"1d"}},
		  {"id":"a3","type":"approval","needs":["a2"],"config":{"role":"credit_officer","count":1,"timeout":"1h"}},
		  {"id":"f","type":"foreach","needs":["a3"],"config":{"items":"=trigger.body.rows","steps":[
		    {"id":"w","type":"wait","config":{"duration":"90s"}}]}},
		  {"id":"p","type":"parallel","needs":["f"],"config":{"branches":[{"name":"left","steps":[{"id":"s","type":"signal","config":{"event":"paid","correlation":"=run.id","timeout":"1h"}}]},
		    {"name":"right","steps":[{"id":"k","type":"container","config":{"image":"ghcr.io/acme/report@sha256:0000000000000000000000000000000000000000000000000000000000000000","command":["run"]}}]}]}},
		  {"id":"b","type":"branch","needs":["p"],"config":{"paths":[{"name":"big","when":"=true","steps":[{"id":"c","type":"code","config":{"language":"python","source":"x"}}]}],
		    "default":{"steps":[{"id":"h","type":"http","config":{"method":"GET","url":"=env.u","class":"read"}}]}}},
		  {"id":"t","type":"transform","needs":["b"],"when":"=true","config":{"output":{}}}]}`,
		`{"schema":"wd/v1","id":"wf_b","version":1,"name":"b","trigger":{"type":"manual"},"steps":[{"id":"t","type":"transform","config":{"output":{}}}]}`,
	}
	triggers := []string{"manual", "whatsapp", "ussd", "email", "database_change", "subflow"}
	crons := []string{"0 10 * * 5", "30 8 * * 1-5", "0 20 * * *", "0 9 25 * *", "0 9 L * *", "*/15 * * * *", "5 */3 * * *", "0 * * * *", "7 * * * *",
		"0 9 * * 1,3,5", "0 9 1 1 *", "bad"}
	for _, info := range lang.All() {
		var out []string
		for _, doc := range docs {
			def, err := wd.Load([]byte(doc))
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, wdtext.Text(wdtext.DescribeIn(def, nil, info.Tag)))
		}
		for _, c := range crons {
			out = append(out, wdtext.ScheduleIn(c, "", info.Tag))
		}
		for _, d := range []string{"1s", "2m", "1h", "3d"} {
			out = append(out, wdtext.DurationIn(d, info.Tag))
		}
		for _, tr := range triggers {
			def := &wd.Definition{Trigger: wd.Trigger{Type: tr}}
			out = append(out, wdtext.Text(wdtext.DescribeIn(def, nil, info.Tag)))
		}
		all := strings.Join(out, "\n")
		if strings.Contains(all, "wd.") {
			t.Errorf("%s: a message id shows:\n%s", info.Tag, all)
		}
		if info.Tag != lang.EN && info.Tag != lang.PCM && (strings.Contains(all, "Every day") || strings.Contains(all, "Wait for approval")) {
			t.Errorf("%s: English shows:\n%s", info.Tag, all)
		}
	}
	for _, m := range lang.Default().Missing() {
		if strings.Contains(m, "/wd.") {
			t.Errorf("fell back to English: %s", m)
		}
	}
	// Other languages place the day of the month themselves.
	if got := wdtext.ScheduleIn("0 9 25 * *", "", lang.YO); got != lang.Default().Text(lang.YO, "wd.schedule.monthly", "day", "25", "time", "09:00", "tz", "Africa/Lagos") {
		t.Errorf("yoruba monthly: %q", got)
	}
}
