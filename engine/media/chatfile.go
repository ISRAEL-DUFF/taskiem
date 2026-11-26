package media

import (
	"fmt"
	"net/http"
	"time"

	"github.com/israel-duff/taskiem/engine/voice"
)

// Files in a conversation between a person and a business in the app
// (docs/spec/taskiem-for-everyone.md §8.5): photos up to 10 MB, voice
// notes up to 5 minutes, files up to 25 MB. Each side keeps its own copy,
// so a file is checked and cleaned once here, then stored by each side.
const (
	MaxChatPhotoBytes = MaxImageBytes
	MaxChatVoice      = 5 * time.Minute
	// MaxChatVoiceBytes holds a voice note whose length cannot be read
	// from it (WebM, MP3, M4A: only Ogg's is read) to about five minutes
	// of speech.
	MaxChatVoiceBytes = 5 << 20
	MaxChatFileBytes  = MaxFileBytes
)

// Facts are what is known about a chat's file beyond its kind, type and
// size (docs/spec/chat-previews-and-editing.md §1.3), all optional so
// older messages read as before.
type Facts struct {
	// Pages is a PDF's page count, known once its picture is drawn.
	Pages int `json:"pages,omitempty"`
	// Protected: a password-protected PDF.
	Protected bool `json:"protected,omitempty"`
	// Preview is the state of a PDF's first-page picture: ready, none (a
	// protected PDF) or failed; empty while it is being drawn.
	Preview string `json:"preview,omitempty"`
	// W and H are a photo's, or the PDF picture's, size in pixels.
	W int `json:"w,omitempty"`
	H int `json:"h,omitempty"`
	// Doc is a document's family: word, excel, powerpoint, opendocument,
	// csv or text.
	Doc string `json:"doc,omitempty"`
}

// Map is the facts as a stored attachment's fields.
func (f Facts) Map(into map[string]any) map[string]any {
	if into == nil {
		into = map[string]any{}
	}
	set := func(k string, v any, ok bool) {
		if ok {
			into[k] = v
		} else {
			delete(into, k)
		}
	}
	set("pages", f.Pages, f.Pages > 0)
	set("protected", true, f.Protected)
	set("preview", f.Preview, f.Preview != "")
	set("w", f.W, f.W > 0)
	set("h", f.H, f.H > 0)
	set("doc", f.Doc, f.Doc != "")
	return into
}

// FactsOf reads the facts back from a stored attachment's fields.
func FactsOf(m map[string]any) Facts {
	num := func(k string) int {
		if n, ok := m[k].(float64); ok {
			return int(n)
		}
		return 0
	}
	f := Facts{Pages: num("pages"), W: num("w"), H: num("h")}
	f.Protected, _ = m["protected"].(bool)
	f.Preview, _ = m["preview"].(string)
	f.Doc, _ = m["doc"].(string)
	return f
}

// ChatUpload is a file sent in a chat, checked and cleaned: its type,
// the bytes to keep, its name as kept, and what is known of it.
type ChatUpload struct {
	Type string
	Data []byte
	Name string
	Facts
}

// CheckChatFile checks a photo, voice note, PDF or document sent in an
// app conversation and cleans it as PutChatFile and PutFile do, without
// storing it. It answers the file's type and the bytes to keep
// (CheckChatUpload has the rest).
func CheckChatFile(data []byte) (string, []byte, error) {
	u, err := CheckChatUpload(data, "")
	return u.Type, u.Data, err
}

// CheckChatUpload checks a file sent in a chat: a photo turned upright
// and stripped of its metadata (its largest size, with its width and
// height), a voice note stripped of its tags (Ogg up to five minutes, by
// its own length; the others by size), a PDF refused with scripts or
// embedded files, or a document (§1.2) by its own bytes, refused with
// macros, a password or a zip bomb. Anything else is refused, whatever its
// name. Its name is cleaned and given its real extension (ChatFileName).
// The caller scans the file for malware first (ScanChatFile).
func CheckChatUpload(data []byte, name string) (ChatUpload, error) {
	if len(data) == 0 {
		return ChatUpload{}, fmt.Errorf("%w: the file is empty", ErrFile)
	}
	u, err := checkChatUpload(data)
	if err != nil {
		return ChatUpload{}, err
	}
	u.Type = TextType(name, u.Type)
	u.Doc = DocKind(u.Type)
	if name != "" {
		u.Name = ChatFileName(name, u.Type)
	}
	return u, nil
}

func checkChatUpload(data []byte) (ChatUpload, error) {
	ctype, clean, err := cleanVoice(data)
	if err != nil {
		return ChatUpload{}, err
	}
	if ctype != "" {
		// Ogg by its own length; an Ogg whose length cannot be read, and
		// the others, by size.
		d, ok := time.Duration(0), false
		if ctype == "audio/ogg" {
			d, ok = voice.OggDuration(clean)
		}
		if (ok && d > MaxChatVoice) || (!ok && len(clean) > MaxChatVoiceBytes) {
			return ChatUpload{}, fmt.Errorf("%w: a voice note longer than 5 minutes", ErrFile)
		}
		return ChatUpload{Type: ctype, Data: clean}, nil
	}
	switch sniffed := http.DetectContentType(data); sniffed {
	case "application/pdf":
		if len(data) > MaxChatFileBytes {
			return ChatUpload{}, fmt.Errorf("%w: larger than %d MB", ErrFile, MaxChatFileBytes>>20)
		}
		if err := checkPDF(data); err != nil {
			return ChatUpload{}, err
		}
		return ChatUpload{Type: sniffed, Data: data}, nil
	case "image/jpeg", "image/png", "image/webp":
		if len(data) > MaxChatPhotoBytes {
			return ChatUpload{}, fmt.Errorf("%w: a photo larger than %d MB", ErrImage, MaxChatPhotoBytes>>20)
		}
		sizes, err := processImage(data)
		if err != nil {
			return ChatUpload{}, err
		}
		return ChatUpload{Type: sizes[0].ctype, Data: sizes[0].data, Facts: Facts{W: sizes[0].width, H: sizes[0].height}}, nil
	default:
		if len(data) > MaxChatFileBytes {
			return ChatUpload{}, fmt.Errorf("%w: larger than %d MB", ErrFile, MaxChatFileBytes>>20)
		}
		doc, err := checkDocument(data)
		if err != nil {
			return ChatUpload{}, err
		}
		if doc == "" {
			return ChatUpload{}, errNotAccepted(sniffed)
		}
		return ChatUpload{Type: doc, Data: data}, nil
	}
}
