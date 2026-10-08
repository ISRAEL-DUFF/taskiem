//go:build !wasip1

package connectorsdk

import (
	"encoding/json"
	"fmt"
	"os"
)

// TestHTTP answers Do outside WebAssembly, so a connector's handlers can
// be unit-tested with go test. Nil refuses every request.
var TestHTTP func(HTTPRequest) (*HTTPResponse, error)

func hostHTTP(raw []byte) []byte {
	var w wireRequest
	_ = json.Unmarshal(raw, &w)
	req := w.HTTPRequest
	req.Body, _ = decodeB64(w.Body)
	var resp *HTTPResponse
	err := fmt.Errorf("no network outside the engine; set connectorsdk.TestHTTP")
	if TestHTTP != nil {
		resp, err = TestHTTP(req)
	}
	var out wireResponse
	if err != nil {
		out.Error = &wireError{Kind: KindOf(err), Message: err.Error()}
	} else {
		out = wireResponse{Status: resp.Status, Headers: resp.Headers, Body: encodeB64(resp.Body)}
	}
	b, _ := json.Marshal(out)
	return b
}

func hostLog(s string) { fmt.Fprintln(os.Stderr, s) }
