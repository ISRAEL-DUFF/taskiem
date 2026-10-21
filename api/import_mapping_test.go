package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

// importWith posts a CSV with a header-to-column mapping.
func importWith(t *testing.T, c *client, path, body string, mapping map[string]string) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "debtors.csv")
	_, _ = io.WriteString(fw, body)
	if mapping != nil {
		raw, _ := json.Marshal(mapping)
		_ = mw.WriteField("mapping", string(raw))
	}
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, c.base+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// A CSV's headers match the table's columns however they are written,
// and the person matches the rest (no-technical-steps N5).
func TestImportMatchesHeaders(t *testing.T) {
	w := newTablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	tbl := owner.must(201, "POST", "/v1/tables", debtorsTable)
	path := "/v1/tables/" + tbl["id"].(string) + "/import"
	file := "Name,PHONE,Balance Kobo,Paid,Notes\nAda,2348012345678,500,no,call after 5\n"
	st, out := importWith(t, owner, path, file, nil)
	if st != 400 || !strings.Contains(toJSON(out), `\"Notes\"`) || !strings.Contains(toJSON(out), "leave it out") {
		t.Fatalf("an unmatched header: %d %v", st, out)
	}
	st, out = importWith(t, owner, path, file, map[string]string{"Notes": ""})
	if st != 200 || out["imported"] != float64(1) {
		t.Fatalf("with the mapping: %d %v", st, out)
	}
	if st, out := importWith(t, owner, path, "Name,Notes\nBola,x\n", map[string]string{"Notes": "colour"}); st != 400 || !strings.Contains(toJSON(out), "no column") {
		t.Errorf("a mapping to nowhere: %d %v", st, out)
	}
}
