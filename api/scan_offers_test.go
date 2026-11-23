package api_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// A code scanned from the phone's home screen, with no workflow open,
// offers the workflows that take it (device fields §7, D2): those started
// by hand with a scan input of the code's format, that the person may
// start, tools first; the start reads the code as the run keeps it, its
// source from whether the phone gave a format.
func TestScanOffers(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Acme", "owner@scan.test")
	publish := func(name string, def string) string {
		wf := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": name, "definition": json.RawMessage(def)})["id"].(string)
		owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
		return wf
	}
	// A tool for stock keepers: the code and how many.
	publish("Count an item", `{"schema":"wd/v1","id":"wf_count","version":1,"name":"Count an item","trigger":{"type":"manual"},"roles":["operator"],
		"inputs":{"schema":{"type":"object","required":["item"],"properties":{
		  "item":{"type":"scan","why":"the code on the item's pack","formats":["ean_13","upc_a"]},
		  "count":{"type":"integer"}}}},
		"outputs":[{"name":"read","type":"string","title":"Read","description":"How the code was read","value":"=steps.n.output.read"}],
		"tool":{"name":"Count an item","command":"shelf-count","about":"Count an item by its code","answer":{"type":"text","text":"=outputs.read"}},
		"steps":[{"id":"n","type":"transform","config":{"output":{"read":"=trigger.body.item.source + ' ' + trigger.body.item.text"}}}]}`)
	// A plain workflow, for anyone, that also asks a note.
	plain := publish("Check in a parcel", `{"schema":"wd/v1","id":"wf_parcel","version":1,"name":"Check in a parcel","trigger":{"type":"manual"},
		"inputs":{"schema":{"type":"object","required":["label","note"],"properties":{
		  "label":{"type":"scan","why":"the parcel's label"},
		  "note":{"type":"string"}}}},
		"steps":[{"id":"n","type":"transform","config":{"output":{}}}]}`)
	// One with no scan: never offered.
	publish("Leave", `{"schema":"wd/v1","id":"wf_leave","version":1,"name":"Leave","trigger":{"type":"manual"},
		"inputs":{"schema":{"type":"object","properties":{"who":{"type":"string"}}}},
		"steps":[{"id":"n","type":"transform","config":{"output":{}}}]}`)

	keeper := w.member(t, owner, "keeper@scan.test", "operator")
	cashier := w.member(t, owner, "cashier@scan.test", "builder")

	offers := func(c *client, q string) []map[string]any {
		var out []map[string]any
		for _, o := range c.must(200, "GET", "/v1/scan/offers"+q, nil)["offers"].([]any) {
			out = append(out, o.(map[string]any))
		}
		return out
	}
	got := offers(keeper, "?format=ean_13")
	if len(got) != 2 || got[0]["kind"] != "tool" || got[0]["command"] != "shelf-count" || got[0]["input"] != "item" || toJSON(got[0]["asks"]) != `[]` ||
		got[1]["kind"] != "workflow" || got[1]["workflow_id"] != plain || got[1]["input"] != "label" || toJSON(got[1]["asks"]) != `["note"]` {
		t.Fatalf("an EAN-13: %v", got)
	}
	// A QR code: the tool takes only barcodes.
	if got := offers(keeper, "?format=qr"); len(got) != 1 || got[0]["workflow_id"] != plain {
		t.Errorf("a QR code: %v", got)
	}
	// Typed: both take a code typed.
	if got := offers(keeper, ""); len(got) != 2 {
		t.Errorf("typed: %v", got)
	}
	// The workflow's roles hold: the cashier is not offered the count.
	if got := offers(cashier, "?format=ean_13"); len(got) != 1 || got[0]["workflow_id"] != plain {
		t.Errorf("a cashier: %v", got)
	}
	if e := keeper.must(400, "GET", "/v1/scan/offers?format=pdf417", nil); !strings.Contains(e["error"].(string), "format is one of") {
		t.Errorf("a format the scanner does not read: %v", e)
	}

	// Started with the code as the phone read it: camera, whatever the
	// body says; a code of a format it does not take is refused.
	keeper.must(422, "POST", "/v1/tools/shelf-count/start", map[string]any{"input": map[string]any{"item": map[string]any{"text": "6151100093217", "format": "qr"}}})
	run := keeper.must(201, "POST", "/v1/tools/shelf-count/start", map[string]any{"input": map[string]any{"item": map[string]any{"text": "6151100093217", "format": "ean_13", "source": "typed"}, "count": 4}})["run_id"].(string)
	w.env.Drain(t)
	if a := keeper.must(200, "GET", "/v1/runs/"+run+"/answer", nil); a["text"] != "camera 6151100093217" {
		t.Errorf("scanned: %v", a)
	}
	// Typed: typed.
	run = keeper.must(201, "POST", "/v1/tools/shelf-count/start", map[string]any{"input": map[string]any{"item": "6151100093218"}})["run_id"].(string)
	w.env.Drain(t)
	if a := keeper.must(200, "GET", "/v1/runs/"+run+"/answer", nil); a["text"] != "typed 6151100093218" {
		t.Errorf("typed: %v", a)
	}
}
