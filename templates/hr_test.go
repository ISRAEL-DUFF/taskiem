package templates_test

import (
	"encoding/json"
	"reflect"
	"regexp"
	"testing"

	"github.com/israel-duff/taskiem/templates"
)

// The HR help desk's reply forms (decision 0065): the HR form is open to
// hr and hr_lead and takes HR- numbers only, the confidential one to
// hr_lead alone and HC- numbers only; the customers' ticket reply form
// takes T- numbers, never an HR request's.
func TestHRReplyForms(t *testing.T) {
	type form struct {
		Trigger struct {
			Config struct {
				Access string   `json:"access"`
				Roles  []string `json:"roles"`
			} `json:"config"`
		} `json:"trigger"`
		Inputs struct {
			Schema struct {
				Properties map[string]struct {
					Pattern string `json:"pattern"`
				} `json:"properties"`
			} `json:"schema"`
		} `json:"inputs"`
	}
	for _, c := range []struct {
		id      string
		roles   []string
		takes   []string
		refuses []string
	}{
		{"ticket-reply", nil, []string{"T-1042", "t 1042", "1042"}, []string{"HR-1042", "HC-1042", "hr 1042"}},
		{"hr-ticket-reply", []string{"hr", "hr_lead"}, []string{"HR-1042", "hr 1042", "1042"}, []string{"HC-1042", "T-1042", "hc 1042"}},
		{"hr-confidential-reply", []string{"hr_lead"}, []string{"HC-1042", "hc 1042", "1042"}, []string{"HR-1042", "T-1042"}},
	} {
		tpl, ok := templates.Default().Get(c.id)
		if !ok {
			t.Fatalf("%s: no such template", c.id)
		}
		var f form
		if err := json.Unmarshal(tpl.Definition, &f); err != nil {
			t.Fatal(err)
		}
		if f.Trigger.Config.Access != "members" || !reflect.DeepEqual(f.Trigger.Config.Roles, c.roles) {
			t.Errorf("%s: access %q roles %v, want members %v", c.id, f.Trigger.Config.Access, f.Trigger.Config.Roles, c.roles)
		}
		re := regexp.MustCompile(f.Inputs.Schema.Properties["ticket"].Pattern)
		for _, s := range c.takes {
			if !re.MatchString(s) {
				t.Errorf("%s refuses %q", c.id, s)
			}
		}
		for _, s := range c.refuses {
			if re.MatchString(s) {
				t.Errorf("%s takes %q", c.id, s)
			}
		}
	}
	// Each HR form works on its own tables only.
	for id, want := range map[string][]string{"hr-ticket-reply": {"hr_tickets", "hr_ticket_messages"}, "hr-confidential-reply": {"hr_confidential_tickets", "hr_confidential_messages"}} {
		tpl, _ := templates.Default().Get(id)
		var got []string
		for _, tb := range tpl.TablesFor(map[string]any{}) {
			got = append(got, tb.Name)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: tables %v, want %v", id, got, want)
		}
	}
}
