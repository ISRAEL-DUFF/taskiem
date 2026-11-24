package wd

import (
	"encoding/json"
	"testing"
)

// The steps that read each saved thing (build plan H6): a step's own
// expressions, not those nested in it.
func TestSavedReaders(t *testing.T) {
	d, err := Load([]byte(`{"schema":"wd/v1","id":"wf_r","version":1,"name":"r","trigger":{"type":"manual"},
	  "inputs":{"schema":{"type":"object","properties":{}}},
	  "steps":[
	    {"id":"a","type":"transform","config":{"output":{"x":"=saved.price_list.file","y":"=saved[\"price-list\"].file_name"}}},
	    {"id":"each","type":"foreach","needs":["a"],"config":{"items":"=[1,2]","steps":[
	      {"id":"b","type":"transform","config":{"output":{"z":"=saved.intro.body"}}}]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(d.SavedReaders())
	if string(got) != `{"intro":["b"],"price-list":["a"],"price_list":["a"]}` {
		t.Errorf("readers: %s", got)
	}
}
