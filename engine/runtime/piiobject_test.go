package runtime_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/decide"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/pii"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/wd"
)

const piiObjectFlow = `{"schema":"wd/v1","id":"wf_rec","version":1,"name":"rec","trigger":{"type":"manual"},
  "inputs":{"schema":{"$ref":"#/types/In"}},
  "types":{"In":{"type":"object","properties":{
    "holder":{"type":"object","x-pii":"name"},
    "record":{"type":"object","x-pii":"other"},
    "rows":{"type":"array","x-pii":"other"}}}},
  "steps":[
    {"id":"copy","type":"transform","config":{"output":{"who":"=trigger.body.holder.full","city":"=trigger.body.record.address.city","n":"=size(trigger.body.rows)","last":"=trigger.body.rows[599].ref"}}},
    {"id":"each","type":"foreach","needs":["copy"],"config":{"items":"=trigger.body.record.accounts","steps":[
      {"id":"check","type":"transform","config":{"output":{"acct":"=item.number"}}}]}}]}`

// A declared object is sealed value by value, and a long declared list
// whole. Decide reads both opened, live and on replay, exactly as it reads
// a declared scalar: a nested value copied into a later step (through a
// foreach item too) is the plain value to decide and sealed again where it
// is stored; and an erased person's nested value reads as erased.
func TestDeclaredObjectsOpenForDecideAndErase(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, piiObjectFlow)
	const name, city, account = "Ngozi Adeyemi", "Abeokuta South", "ACCT-7781-KUDA"
	rows := make([]any, 600) // 1,200 values: sealed whole
	for i := range rows {
		rows[i] = map[string]any{"ref": fmt.Sprintf("ROW-REF-%04d", i), "amount": i}
	}
	ref := e.Start(t, wf, map[string]any{"body": map[string]any{
		"holder": map[string]any{"full": name, "age": 41},
		"record": map[string]any{"address": map[string]any{"city": city}, "accounts": []any{map[string]any{"number": account}}},
		"rows":   rows}})
	e.Drain(t)
	if st := e.Status(t, ref); st != "completed" {
		t.Fatalf("status %s: %s", st, types(events(t, e, ref)))
	}
	// Stored: nothing of them readable, the copies included.
	for _, ev := range events(t, e, ref) {
		for _, plain := range []string{name, city, account, "ROW-REF-0599", "ROW-REF-0001"} {
			if strings.Contains(string(ev.Payload), plain) {
				t.Errorf("%q readable in stored %s(%s)", plain, ev.Type, ev.StepID)
			}
		}
	}
	// Opened: decide's view. The copies are the plain values, the foreach
	// item's step was given the plain account, and the history replays.
	opened, err := e.Store.OpenedHistory(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	var copied, checked bool
	for _, ev := range opened {
		p := string(ev.Payload)
		if strings.Contains(p, `"$pii"`) {
			t.Errorf("opened %s(%s) still holds an envelope: %.300s", ev.Type, ev.StepID, p)
		}
		if ev.Type == history.StepCompleted && ev.StepID == "copy" {
			copied = strings.Contains(p, name) && strings.Contains(p, city) && strings.Contains(p, "ROW-REF-0599") && strings.Contains(p, "600")
		}
		if ev.Type == history.StepCompleted && ev.StepID == "each[0].check" {
			checked = strings.Contains(p, account)
		}
	}
	if !copied || !checked {
		t.Errorf("copy read the nested values: %v; the foreach step got the plain account: %v\n%s", copied, checked, types(opened))
	}
	def, _ := wd.Load([]byte(piiObjectFlow))
	if err := decide.Verify(def, opened); err != nil {
		t.Errorf("replay over the opened history: %v", err)
	}
	// Erase the holder: their nested value, and its copy, read as erased;
	// the rest still opens.
	subject, err := e.Vault.SubjectFor(ctx, e.Tenant, "name", name)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Vault.Erase(ctx, e.Tenant, subject, "dpo"); err != nil {
		t.Fatal(err)
	}
	after, err := e.Store.OpenedHistory(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for _, ev := range after {
		all.Write(ev.Payload)
	}
	if s := all.String(); strings.Contains(s, name) || !strings.Contains(s, pii.Erased) || !strings.Contains(s, city) || !strings.Contains(s, "ROW-REF-0599") {
		t.Errorf("after erasing the holder: name readable %v, erased marker %v, city %v, rows %v",
			strings.Contains(s, name), strings.Contains(s, pii.Erased), strings.Contains(s, city), strings.Contains(s, "ROW-REF-0599"))
	}
}
