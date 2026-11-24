package wdtext

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/wd"
)

// A tool is read back after its steps (made tools §13, Describe it): the
// command that calls it, who may use it, what it asks and what it says
// back, in the person's language; a workflow that is no tool is not.
func TestDescribeReadsTheToolBack(t *testing.T) {
	def, err := wd.Load([]byte(changesV1))
	if err != nil {
		t.Fatal(err)
	}
	text := Text(Describe(def, nil))
	for _, want := range []string{"• Use it from the chat with /fuel: Log fuel", "• Your own people can use it", "• It asks for: amount", "• It answers with words"} {
		if !strings.Contains(text, want) {
			t.Errorf("lacks %q:\n%s", want, text)
		}
	}
	cust := strings.Replace(changesV1, `"says":["fuel"],`, `"for":"customers",`, 1)
	cust = strings.Replace(cust, `"description":"Spent",`, `"description":"Spent","public":true,`, 1)
	if def, err = wd.Load([]byte(cust)); err != nil {
		t.Fatal(err)
	}
	if text := Text(DescribeIn(def, nil, lang.PCM)); !strings.Contains(text, "Your customers fit use am") || !strings.Contains(text, "/fuel") {
		t.Errorf("for customers, in Pidgin:\n%s", text)
	}
	plain := strings.Replace(changesV1, `"tool":{"name":"Log fuel","command":"fuel","about":"Record fuel","says":["fuel"],
	  "answer":{"type":"text","text":"='Logged'"}},`, "", 1)
	if def, err = wd.Load([]byte(plain)); err != nil {
		t.Fatal(err)
	}
	if text := Text(Describe(def, nil)); strings.Contains(text, "chat") {
		t.Errorf("no tool, yet:\n%s", text)
	}
}
