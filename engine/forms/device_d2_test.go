package forms

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/media"
)

type cleanScanner struct{}

func (cleanScanner) Scan(context.Context, []byte) (string, error) { return "", nil }

type memFiles struct{ n int }

func (m *memFiles) PutFile(_ context.Context, _ uuid.UUID, data []byte) (media.File, error) {
	m.n++
	return media.File{Path: "f" + string(rune('0'+m.n)), Type: http.DetectContentType(data), Size: int64(len(data))}, nil
}

func (*memFiles) SignURL(context.Context, uuid.UUID, string, time.Duration) (string, time.Time, error) {
	return "", time.Time{}, nil
}

func (*memFiles) Delete(context.Context, uuid.UUID, string, []string) error { return nil }

// D2: a scan's code checked (format, length, control characters), a
// signature's signer when the field asks, and what the audit keeps.
func TestScanAndSignatureAnswers(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"code": map[string]any{"type": "scan", "why": "w"},
		"sign": map[string]any{"type": "signature", "why": "w", "ask_name": true},
	}}
	at := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	app := Submission{Channel: "app", Device: &Device{App: "app"}}

	// A code read by the camera, of a format the field takes (it names
	// none: every one the scanner reads), and a signature with its signer.
	ref := FileRef{ID: "0193a000-0000-7000-8000-0000000000aa", Name: "signature.png", Type: "image/png", Size: 900, SHA256: "ab"}
	answers := map[string]any{"code": map[string]any{"text": "6151100093217", "format": "ean_13"}, "sign": signedFile{FileRef: ref, SignedBy: "  Ngozi   Eze "}}
	if why := deviceAnswers(schema, answers, app, at); why != "" {
		t.Fatal(why)
	}
	if c := answers["code"].(map[string]any); c["source"] != "camera" || c["format"] != "ean_13" || c["text"] != "6151100093217" {
		t.Fatalf("code: %v", c)
	}
	sg := answers["sign"].(map[string]any)
	if sg["source"] != "drawn" || sg["state"] != "answered" || sg["signed_by"] != "Ngozi Eze" || sg["id"] != ref.ID {
		t.Fatalf("sign: %v", sg)
	}
	audit := deviceAudit(schema, answers)
	if audit["code"] != "scan answered camera" || audit["sign"] != "signature answered drawn" {
		t.Fatalf("audit: %v", audit)
	}
	for _, v := range audit {
		if strings.Contains(v, "6151100093217") || strings.Contains(v, "Ngozi") {
			t.Fatalf("the audit keeps a value: %v", audit)
		}
	}

	// Refused in plain words.
	for _, c := range []struct {
		answers map[string]any
		want    string
	}{
		{map[string]any{"code": map[string]any{"text": "61511\x0000932", "format": "ean_13"}}, "no control characters"},
		{map[string]any{"code": map[string]any{"text": strings.Repeat("9", 2001), "format": "qr"}}, "at most 2000 characters"},
		{map[string]any{"code": map[string]any{"text": "x", "format": "pdf417"}}, "does not take pdf417 codes"},
		{map[string]any{"code": map[string]any{"text": "   "}}, "a scan is the code's text"},
		{map[string]any{"sign": ref}, "say who signed"},
		{map[string]any{"sign": signedFile{FileRef: ref, SignedBy: "  "}}, "say who signed"},
	} {
		if why := deviceAnswers(schema, c.answers, app, at); !strings.Contains(why, c.want) {
			t.Errorf("%v: %q, want %q", c.answers, why, c.want)
		}
	}
	// Without ask_name, no name is kept even when one is sent.
	plain := map[string]any{"type": "object", "properties": map[string]any{"sign": map[string]any{"type": "signature", "why": "w"}}}
	answers = map[string]any{"sign": signedFile{FileRef: ref, SignedBy: "Ngozi"}}
	if why := deviceAnswers(plain, answers, app, at); why != "" || answers["sign"].(map[string]any)["signed_by"] != nil {
		t.Fatalf("no ask_name: %q %v", why, answers["sign"])
	}
}

// A signature is a PNG by its content and at most 1 MB; the signer's name
// sent beside it in the answers travels with the file.
func TestSignatureUploads(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")
	jpeg := []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00")
	schema := map[string]any{"type": "object", "properties": map[string]any{"sign": map[string]any{"type": "signature", "why": "w", "ask_name": true}}}
	s := &Service{Scanner: cleanScanner{}, Files: &memFiles{}}
	in := func(data []byte, typ string) Incoming {
		return Incoming{
			Submission: Submission{Channel: "app", Answers: map[string]any{"sign": map[string]any{"signed_by": "Ngozi"}}},
			Uploads:    []Upload{{Meta: FileMeta{Field: "sign", Name: "signature.png", Type: typ}, Data: data}},
		}
	}
	tg := target{schema: schema}
	answers, files, why, err := s.files(context.Background(), in(png, "image/png"), tg)
	if err != nil || why != "" || len(files) != 1 {
		t.Fatalf("a PNG: %q %v", why, err)
	}
	if sf, ok := answers["sign"].(signedFile); !ok || sf.SignedBy != "Ngozi" {
		t.Fatalf("the signer: %#v", answers["sign"])
	}
	for _, c := range []struct {
		data []byte
		want string
	}{
		{jpeg, "a signature is a PNG drawing"},
		{append(append([]byte{}, png...), make([]byte, MaxSignatureBytes)...), "larger than 1 MB"},
	} {
		if _, _, why, _ := s.files(context.Background(), in(c.data, "image/png"), tg); !strings.Contains(why, c.want) {
			t.Errorf("%q, want %q", why, c.want)
		}
	}
}
