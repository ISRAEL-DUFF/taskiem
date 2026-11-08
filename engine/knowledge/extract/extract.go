// Package extract turns a business's website pages and uploaded files into
// plain text for its customer assistant's knowledge store (decision 0062
// D12, plan V5b in docs/spec/voice-assistant.md, gaps K6 and K7).
//
// It reads three kinds of input: HTML pages (HTMLText, and Crawl to read a
// whole site), PDF files (PDFText) and Word documents (DOCXText); FileText
// picks between the last two by content. Everything here treats its input
// as hostile: a business uploads whatever it likes and a website says
// whatever it likes, so every reader bounds the bytes it decompresses, the
// work it does and the text it returns, and none of them executes anything.
//
// The text is plain: one line per paragraph, heading, list item or table
// row, so that the store can split it into passages without knowing where
// it came from.
package extract

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Errors callers tell apart. They are wrapped with detail; check with
// errors.Is.
var (
	// ErrScanned is a PDF with no text layer, such as a scanned document.
	// Reading it needs OCR, which Taskiem does not have yet.
	ErrScanned = errors.New("the PDF has no text in it, only images (a scanned document)")
	// ErrUnreadable is a file this package cannot read: not a PDF or Word
	// document, encrypted, or broken.
	ErrUnreadable = errors.New("the file cannot be read")
	// ErrTooLarge is a file over MaxFileBytes, or an archive that would
	// expand past the limits (a zip bomb).
	ErrTooLarge = errors.New("the file is too large")
)

const (
	// MaxFileBytes is the largest file accepted.
	MaxFileBytes = 25 << 20
	// MaxText is the most text returned for one file or page, in bytes.
	// Longer text is cut at a paragraph.
	MaxText = 2 << 20
)

// FileText returns the text of a PDF or Word (DOCX) file. It goes by the
// content, not the file's name or declared type, which an uploader controls.
func FileText(data []byte) (string, error) {
	if len(data) > MaxFileBytes {
		return "", fmt.Errorf("%w: %d bytes, over the %d MB limit", ErrTooLarge, len(data), MaxFileBytes>>20)
	}
	if isPDF(data) {
		return PDFText(data)
	}
	if isDOCX(data) {
		return DOCXText(data)
	}
	return "", fmt.Errorf("%w: not a PDF or Word (.docx) document", ErrUnreadable)
}

// isPDF reports whether data starts like a PDF. Readers accept the header
// anywhere in the first kilobyte, as some producers put junk before it.
func isPDF(data []byte) bool {
	head := data[:min(len(data), 1024)]
	return bytes.Contains(head, []byte("%PDF-"))
}

// isDOCX reports whether data is a zip holding word/document.xml. The local
// file headers are checked by name rather than opening the archive, which
// DOCXText does with its limits.
func isDOCX(data []byte) bool {
	return bytes.HasPrefix(data, []byte("PK\x03\x04")) && bytes.Contains(data, []byte("word/document.xml"))
}

// truncate cuts text over MaxText at the last paragraph break before the
// limit, or failing that the last line break or space, so a passage is
// never cut mid-word.
func truncate(s string) string {
	if len(s) <= MaxText {
		return s
	}
	cut := s[:MaxText]
	for !utf8.ValidString(cut) && len(cut) > 0 {
		cut = cut[:len(cut)-1]
	}
	for _, sep := range []string{"\n\n", "\n", " "} {
		if i := strings.LastIndex(cut, sep); i > MaxText/2 {
			return strings.TrimSpace(cut[:i])
		}
	}
	return strings.TrimSpace(cut)
}

// lineWriter gathers text into lines: runs of white space become one
// space, blank lines are dropped and each line is trimmed.
type lineWriter struct {
	lines []string
	cur   strings.Builder
	space bool // a space is owed before the next character
	tabs  bool // keep tabs (a Word document's w:tab) rather than collapse them
	size  int  // bytes written so far, to stop early
}

// text appends s to the current line, collapsing white space.
func (w *lineWriter) text(s string) {
	for _, r := range s {
		if r == '\t' && w.tabs {
			w.cur.WriteByte('\t')
			w.space = false
			continue
		}
		if unicode.IsSpace(r) || r == 0xa0 || r == 0x200b || r == 0xfeff {
			if w.cur.Len() > 0 {
				w.space = true
			}
			continue
		}
		// Control characters, and private-use characters such as a symbol
		// font's bullets, carry nothing a search could use.
		if unicode.IsControl(r) || unicode.Is(unicode.Co, r) {
			continue
		}
		if w.space {
			w.cur.WriteByte(' ')
			w.space = false
		}
		w.cur.WriteRune(r)
	}
}

// line ends the current line, if it has anything in it.
func (w *lineWriter) line() {
	if w.cur.Len() > 0 {
		w.lines = append(w.lines, strings.TrimRight(w.cur.String(), "\t"))
		w.size += w.cur.Len() + 1
		w.cur.Reset()
	}
	w.space = false
}

// full reports whether enough text has been gathered that more would be
// cut by truncate anyway.
func (w *lineWriter) full() bool { return w.size+w.cur.Len() > MaxText+1024 }

func (w *lineWriter) String() string {
	w.line()
	return truncate(strings.Join(w.lines, "\n"))
}
