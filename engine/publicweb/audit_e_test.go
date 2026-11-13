package publicweb

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// Regression tests for the public host's findings of the second audit
// (E8, E11): each was written to fail on the code as it stood.

// countingBody counts what the server reads of a request's body.
type countingBody struct {
	r io.Reader
	n atomic.Int64
}

func (c *countingBody) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }

// bigUpload is a multipart body: the fields in order, then a file of
// size bytes.
func bigUpload(fields [][2]string, fileFirst bool, size int64) (*countingBody, string) {
	const boundary = "xxxxxxxxxxxxtaskiemtestboundary"
	var head strings.Builder
	for _, f := range fields {
		head.WriteString("--" + boundary + "\r\nContent-Disposition: form-data; name=\"" + f[0] + "\"\r\n\r\n" + f[1] + "\r\n")
	}
	fileHead := "--" + boundary + "\r\nContent-Disposition: form-data; name=\"a.id_card\"; filename=\"me.png\"\r\nContent-Type: image/png\r\n\r\n"
	file := io.MultiReader(strings.NewReader(fileHead), io.LimitReader(zeros{}, size), strings.NewReader("\r\n"))
	var body io.Reader
	if fileFirst {
		body = io.MultiReader(file, strings.NewReader(head.String()), strings.NewReader("--"+boundary+"--\r\n"))
	} else {
		body = io.MultiReader(strings.NewReader(head.String()), file, strings.NewReader("--"+boundary+"--\r\n"))
	}
	return &countingBody{r: body}, "multipart/form-data; boundary=" + boundary
}

// TestFileFormIsRefusedBeforeItsFilesAreRead (E8): junk posted to a
// public form that takes files is refused on its token, before the body
// is read to the end.
func TestFileFormIsRefusedBeforeItsFilesAreRead(t *testing.T) {
	e := newFormHost(t)
	e.add("widgets00001", `{"title":"Everything"}`, widgetSchema)
	page := must(t, e.get("/f/widgets00001"), 200)
	tm, sm := tokenRe.FindStringSubmatch(page), stateRe.FindStringSubmatch(page)
	if tm == nil || sm == nil {
		t.Fatal("no token or state in the page")
	}
	// The page's own form sends the token and the honeypot before
	// anything large.
	ti, hi, si, fi := strings.Index(page, `name="token"`), strings.Index(page, `name="website"`), strings.Index(page, `name="state"`), strings.Index(page, `type="file"`)
	if ti < 0 || hi < 0 || ti > si || hi > si || si > fi {
		t.Errorf("the form's order: token %d, honeypot %d, state %d, file %d", ti, hi, si, fi)
	}
	const size = 50 << 20
	send := func(fields [][2]string, fileFirst bool) (*httptest.ResponseRecorder, int64) {
		t.Helper()
		body, ctype := bigUpload(fields, fileFirst, size)
		r := httptest.NewRequest(http.MethodPost, "/f/widgets00001", body)
		r.ContentLength = -1
		r.Header.Set("Content-Type", ctype)
		r.Host = formHost
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, r)
		return w, body.n.Load()
	}
	for name, c := range map[string]struct {
		fields    [][2]string
		fileFirst bool
	}{
		"a bad token":            {[][2]string{{"token", "not-a-token"}, {"website", ""}, {"state", sm[1]}, {"do", "send"}}, false},
		"no token":               {[][2]string{{"website", ""}, {"state", sm[1]}, {"do", "send"}}, false},
		"the honeypot filled in": {[][2]string{{"token", tm[1]}, {"website", "https://spam.example"}, {"state", sm[1]}, {"do", "send"}}, false},
		"the file first":         {[][2]string{{"token", tm[1]}, {"website", ""}, {"state", sm[1]}, {"do", "send"}}, true},
	} {
		w, read := send(c.fields, c.fileFirst)
		if w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d", name, w.Code)
		}
		if read > 256<<10 {
			t.Errorf("%s: %d bytes of the body were read before it was refused", name, read)
		}
	}
	if len(e.f.writes) != 0 {
		t.Fatalf("junk was kept: %d", len(e.f.writes))
	}
}

// TestCodeRequestsNeedTheTokenAndAreFew (E10): a code is asked for with
// the page's token, an address may ask for few, and a form with many
// codes no one confirmed tells its owners while it still serves people.
func TestCodeRequestsNeedTheTokenAndAreFew(t *testing.T) {
	e := newFormHost(t)
	pub := e.add("verify000001", `{"title":"Register","verify":"phone"}`, `{"type":"object","required":["name"],"properties":{"name":{"type":"string"}}}`)
	body := must(t, e.get("/f/verify000001"), 200, "Send code")
	// Without the page's token nothing is sent.
	must(t, e.post("/f/verify000001/code", url.Values{"address": {"0803 123 4567"}, "website": {""}}), 422)
	must(t, e.post("/f/verify000001/code", url.Values{"address": {"0803 123 4567"}, "website": {""}, "token": {"made-up"}, "state": {""}}), 422)
	if len(e.f.codes) != 0 {
		t.Fatalf("a code without the page's token: %d", len(e.f.codes))
	}
	// Over its ceiling the form still sends this person a code, and the
	// owners are told.
	e.f.codeAnswer = CodePressed
	must(t, e.next("/f/verify000001/code", body, url.Values{"address": {"0803 123 4567"}}), 200, "We sent a code")
	if len(e.f.tripped) != 1 || e.f.tripped[0] != pub.Endpoint {
		t.Fatalf("the owners were not told: %v", e.f.tripped)
	}
	c := e.f.codes[len(e.f.codes)-1]
	if len(c.asker) != 32 || !bytes.Equal(c.number, e.keys.AddressHash("any form", "+2348031234567")) {
		t.Fatalf("who asked, and the number across forms: %x %x", c.asker, c.number)
	}
	e.f.codeAnswer = CodeBusy
	must(t, e.next("/f/verify000001/code", body, url.Values{"address": {"0803 123 4568"}}), 429, "This form is busy")
	e.f.codeAnswer = CodeNumber
	must(t, e.next("/f/verify000001/code", body, url.Values{"address": {"0803 123 4568"}}), 429, "Too many codes have been sent to this phone number today")
	e.f.codeAnswer = ""
	// One address asks for few: four at once, then it waits.
	must(t, e.next("/f/verify000001/code", body, url.Values{"address": {"0803 123 4569"}}), 200, "We sent a code")
	must(t, e.next("/f/verify000001/code", body, url.Values{"address": {"0803 123 4570"}}), 429, "Too many codes have been asked for from this connection")
}
