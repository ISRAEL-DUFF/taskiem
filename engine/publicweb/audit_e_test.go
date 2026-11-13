package publicweb

import (
	"io"
	"net/http"
	"net/http/httptest"
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
