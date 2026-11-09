package extract

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Zip bomb limits for DOCX files. A real document.xml is rarely over a few
// megabytes even with hundreds of pages; the compression ratio check
// catches an entry that claims a modest size but is mostly repeats.
const (
	maxZipEntryBytes = 20 << 20
	maxZipEntries    = 10000
	maxZipRatio      = 200
)

// The WordprocessingML namespaces: transitional (what Word writes) and
// strict.
var wordNamespaces = map[string]bool{
	"http://schemas.openxmlformats.org/wordprocessingml/2006/main": true,
	"http://purl.oclc.org/ooxml/wordprocessingml/main":             true,
}

// DOCXText returns a Word document's text: each paragraph (headings
// included) as a line, a tab as a tab, a line break as a new line, and each
// table row as a line with its cells joined by " | ". Only the main body
// (word/document.xml) is read: headers, footers, footnotes and comments
// are left out, as is deleted text from tracked changes and text in runs
// marked hidden (<w:vanish/>), which Word does not show or print. Text
// hidden through a character style rather than the run's own properties
// is still read.
//
// It gives up after ExtractTimeout; see DOCXTextContext.
func DOCXText(data []byte) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ExtractTimeout)
	defer cancel()
	return DOCXTextContext(ctx, data)
}

// DOCXTextContext is DOCXText, stopping when ctx is done: it then returns
// no text and an error that wraps both ErrUnreadable and ctx.Err().
func DOCXTextContext(ctx context.Context, data []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", timedOut("Word document", err)
	}
	if len(data) > MaxFileBytes {
		return "", fmt.Errorf("%w: %d bytes, over the %d MB limit", ErrTooLarge, len(data), MaxFileBytes>>20)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("%w: not a Word (.docx) document: %w", ErrUnreadable, err)
	}
	if len(zr.File) > maxZipEntries {
		return "", fmt.Errorf("%w: the archive has %d entries", ErrTooLarge, len(zr.File))
	}
	var doc *zip.File
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			doc = f
		}
	}
	if doc == nil {
		return "", fmt.Errorf("%w: not a Word (.docx) document: no word/document.xml", ErrUnreadable)
	}
	xmlData, err := readZipEntry(doc)
	if err != nil {
		return "", err
	}
	text, err := documentXMLText(ctx, xmlData)
	if ctx.Err() != nil {
		return "", timedOut("Word document", ctx.Err())
	}
	if err != nil {
		return "", fmt.Errorf("%w: the Word document is damaged: %w", ErrUnreadable, err)
	}
	return text, nil
}

// readZipEntry reads one entry, refusing one that expands past the limits.
// The sizes in the zip's headers are the uploader's word, so the bytes
// actually read are counted too.
func readZipEntry(f *zip.File) ([]byte, error) {
	if f.UncompressedSize64 > maxZipEntryBytes {
		return nil, fmt.Errorf("%w: %s expands to %d bytes", ErrTooLarge, f.Name, f.UncompressedSize64)
	}
	if f.UncompressedSize64 > 1<<20 && f.UncompressedSize64 > maxZipRatio*max(f.CompressedSize64, 1) {
		return nil, fmt.Errorf("%w: %s is compressed suspiciously well (a zip bomb)", ErrTooLarge, f.Name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrUnreadable, f.Name, err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(io.LimitReader(rc, maxZipEntryBytes+1))
	if err != nil && !errors.Is(err, zip.ErrChecksum) {
		return nil, fmt.Errorf("%w: %s: %w", ErrUnreadable, f.Name, err)
	}
	if len(b) > maxZipEntryBytes {
		return nil, fmt.Errorf("%w: %s expands past %d MB", ErrTooLarge, f.Name, maxZipEntryBytes>>20)
	}
	return b, nil
}

// docxTable is a table being read: the cells of the current row, and the
// text of the current cell.
type docxTable struct {
	cells []string
	cell  []string // paragraphs of the cell being read
}

// documentXMLText walks word/document.xml as a token stream. Paragraphs
// inside a table cell are joined with spaces into the cell's text; a table
// inside a cell becomes part of that cell's text. A run whose own
// properties carry w:vanish is hidden, and so is everything inside it
// (a text box anchored in a hidden run).
func documentXMLText(ctx context.Context, data []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var (
		w      = lineWriter{tabs: true}
		para   strings.Builder
		inText bool
		tables []*docxTable
		sawDoc bool
		elems  []xml.Name // the open elements
		runs   []bool     // for each open w:r, whether it is hidden
		hidden int        // open w:r elements that are hidden
		tokens int
	)
	isWord := func(n xml.Name, local string) bool { return wordNamespaces[n.Space] && n.Local == local }
	// emit sends a finished line to the innermost open cell, or to the
	// output when no table is open.
	emit := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if len(tables) > 0 {
			t := tables[len(tables)-1]
			t.cell = append(t.cell, s)
			return
		}
		for _, l := range strings.Split(s, "\n") {
			w.text(l)
			w.line()
		}
	}
	for !w.full() {
		if tokens++; tokens&1023 == 0 && ctx.Err() != nil {
			return "", ctx.Err()
		}
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := len(elems)
			elems = append(elems, t.Name)
			if !wordNamespaces[t.Name.Space] {
				continue
			}
			switch t.Name.Local {
			case "document":
				sawDoc = true
			case "r":
				runs = append(runs, false)
			case "vanish":
				// Only the run's own properties: w:vanish in a paragraph
				// mark's properties hides the mark, not the text.
				if n >= 2 && isWord(elems[n-1], "rPr") && isWord(elems[n-2], "r") && len(runs) > 0 && !runs[len(runs)-1] && onOff(t) {
					runs[len(runs)-1] = true
					hidden++
				}
			case "t":
				inText = hidden == 0
			case "tab":
				if hidden == 0 {
					para.WriteByte('\t')
				}
			case "br", "cr":
				if hidden == 0 {
					para.WriteByte('\n')
				}
			case "p":
				para.Reset()
			case "tbl":
				tables = append(tables, &docxTable{})
			case "tr":
				if len(tables) > 0 {
					tables[len(tables)-1].cells = nil
				}
			case "tc":
				if len(tables) > 0 {
					tables[len(tables)-1].cell = nil
				}
			}
		case xml.EndElement:
			if len(elems) > 0 {
				elems = elems[:len(elems)-1]
			}
			if !wordNamespaces[t.Name.Space] {
				continue
			}
			switch t.Name.Local {
			case "r":
				if n := len(runs); n > 0 {
					if runs[n-1] {
						hidden--
					}
					runs = runs[:n-1]
				}
			case "t":
				inText = false
			case "p":
				emit(para.String())
				para.Reset()
			case "tc":
				if len(tables) > 0 {
					tb := tables[len(tables)-1]
					cw := lineWriter{tabs: true}
					cw.text(strings.Join(tb.cell, " "))
					tb.cells = append(tb.cells, cw.String())
					tb.cell = nil
				}
			case "tr":
				if len(tables) > 0 {
					tb := tables[len(tables)-1]
					row := strings.Join(tb.cells, " | ")
					tb.cells = nil
					// A row of empty cells says nothing.
					if strings.Trim(row, " |") != "" {
						tables = tables[:len(tables)-1]
						emit(row)
						tables = append(tables, tb)
					}
				}
			case "tbl":
				if len(tables) > 0 {
					tables = tables[:len(tables)-1]
				}
			}
		case xml.CharData:
			if inText {
				para.Write(t)
			}
		}
	}
	if !sawDoc {
		return "", errors.New("no w:document element")
	}
	return w.String(), nil
}

// onOff reads a WordprocessingML on/off property such as w:vanish: on
// unless its w:val says otherwise.
func onOff(t xml.StartElement) bool {
	for _, a := range t.Attr {
		if a.Name.Local == "val" {
			switch strings.ToLower(a.Value) {
			case "false", "0", "off":
				return false
			}
		}
	}
	return true
}
