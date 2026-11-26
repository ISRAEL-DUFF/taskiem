package media

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/richardlehane/mscfb"
)

// Documents a chat accepts besides photos, voice notes and PDFs
// (docs/spec/chat-previews-and-editing.md §1.2): Word, Excel, PowerPoint,
// OpenDocument, CSV and plain text. The type is read from the file's own
// bytes, never from its name or the browser's word for it.
const (
	TypeDOCX = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	TypeXLSX = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	TypePPTX = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	TypeDOC  = "application/msword"
	TypeXLS  = "application/vnd.ms-excel"
	TypePPT  = "application/vnd.ms-powerpoint"
	TypeODT  = "application/vnd.oasis.opendocument.text"
	TypeODS  = "application/vnd.oasis.opendocument.spreadsheet"
	TypeODP  = "application/vnd.oasis.opendocument.presentation"
	TypeCSV  = "text/csv"
	TypeText = "text/plain"
)

// A ZIP-based document is refused when unpacked it would be larger than
// this, or holds more entries (a "zip bomb").
const (
	MaxDocumentUnpacked = 100 << 20
	MaxDocumentEntries  = 2000
)

// ErrProtected refuses a password-protected document: it cannot be
// scanned.
var ErrProtected = fmt.Errorf("%w: a password-protected document cannot be checked; send it without its password", ErrFile)

// ErrMacros refuses a document that carries macros.
var ErrMacros = fmt.Errorf("%w: a document with macros is not accepted", ErrFile)

// documentTypes are the accepted documents, with their extensions (the
// first is the one a name is given) and their family for the bubble.
var documentTypes = map[string]struct {
	exts []string
	doc  string
}{
	TypeDOCX: {[]string{".docx"}, "word"},
	TypeDOC:  {[]string{".doc"}, "word"},
	TypeXLSX: {[]string{".xlsx"}, "excel"},
	TypeXLS:  {[]string{".xls"}, "excel"},
	TypePPTX: {[]string{".pptx"}, "powerpoint"},
	TypePPT:  {[]string{".ppt"}, "powerpoint"},
	TypeODT:  {[]string{".odt"}, "opendocument"},
	TypeODS:  {[]string{".ods"}, "opendocument"},
	TypeODP:  {[]string{".odp"}, "opendocument"},
	TypeCSV:  {[]string{".csv"}, "csv"},
	TypeText: {[]string{".txt", ".text", ".log", ".md"}, "text"},
}

// DocumentTypes are the accepted documents' types, for the storage
// bucket's list.
func DocumentTypes() []string {
	out := make([]string, 0, len(documentTypes))
	for t := range documentTypes {
		out = append(out, t)
	}
	return out
}

// DocKind is a document's family as the bubble shows it (word, excel,
// powerpoint, opendocument, csv, text); "" for a photo, PDF or voice
// note.
func DocKind(ctype string) string {
	return documentTypes[baseType(ctype)].doc
}

func baseType(ctype string) string {
	t, _, _ := strings.Cut(ctype, ";")
	return strings.TrimSpace(t)
}

// checkDocument reads a document's type from its bytes and refuses one
// with macros, a password, too much inside, or of a kind not accepted.
// It answers "" with no error for bytes that are no document at all.
func checkDocument(data []byte) (string, error) {
	switch {
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		return checkZipDocument(data)
	case bytes.HasPrefix(data, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}):
		return checkCompoundDocument(data)
	}
	if strings.HasPrefix(http.DetectContentType(data), "text/plain") {
		if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			return "", fmt.Errorf("%w: a text or CSV file must be UTF-8 text", ErrFile)
		}
		// A script is a program, whatever its name.
		if bytes.HasPrefix(bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF")), []byte("#!")) {
			return "", errNotAccepted("script")
		}
		return TypeText, nil
	}
	return "", nil
}

// checkZipDocument checks an Office Open XML or OpenDocument file, by the
// type its own parts declare.
func checkZipDocument(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("%w: a damaged ZIP file", ErrFile)
	}
	if len(zr.File) > MaxDocumentEntries {
		return "", fmt.Errorf("%w: the document holds more than %d parts", ErrFile, MaxDocumentEntries)
	}
	var total uint64
	var types, mimetype *zip.File
	for _, f := range zr.File {
		total += f.UncompressedSize64
		if total > MaxDocumentUnpacked {
			return "", fmt.Errorf("%w: the document unpacks to more than %d MB", ErrFile, MaxDocumentUnpacked>>20)
		}
		if f.Flags&0x1 != 0 {
			return "", ErrProtected
		}
		name := strings.ToLower(f.Name)
		base := path.Base(name)
		if base == "vbaproject.bin" || base == "vbaprojectsignature.bin" || base == "vbadata.xml" ||
			strings.HasPrefix(name, "basic/") || strings.HasPrefix(name, "scripts/") {
			return "", ErrMacros
		}
		switch f.Name {
		case "[Content_Types].xml":
			types = f
		case "mimetype":
			mimetype = f
		}
	}
	switch {
	case types != nil:
		raw, err := readZipPart(types)
		if err != nil {
			return "", err
		}
		return ooxmlType(raw)
	case mimetype != nil:
		raw, err := readZipPart(mimetype)
		if err != nil {
			return "", err
		}
		switch t := strings.TrimSpace(string(raw)); t {
		case TypeODT, TypeODS, TypeODP:
			return t, nil
		}
	}
	return "", fmt.Errorf("%w: a ZIP file that is not a Word, Excel, PowerPoint or OpenDocument file", ErrFile)
}

// readZipPart reads one small part, never more than 1 MB of it, whatever
// its header claims.
func readZipPart(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: a damaged document", ErrFile)
	}
	defer func() { _ = rc.Close() }()
	raw, err := io.ReadAll(io.LimitReader(rc, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: a damaged document", ErrFile)
	}
	return raw, nil
}

// ooxmlType reads an Office Open XML file's type from its main part in
// [Content_Types].xml; a macro-enabled one, or a template, is refused.
func ooxmlType(raw []byte) (string, error) {
	var ct struct {
		Defaults []struct {
			ContentType string `xml:"ContentType,attr"`
		} `xml:"Default"`
		Overrides []struct {
			ContentType string `xml:"ContentType,attr"`
		} `xml:"Override"`
	}
	if err := xml.Unmarshal(raw, &ct); err != nil {
		return "", fmt.Errorf("%w: a damaged document", ErrFile)
	}
	all := make([]string, 0, len(ct.Defaults)+len(ct.Overrides))
	for _, d := range ct.Defaults {
		all = append(all, strings.ToLower(d.ContentType))
	}
	for _, o := range ct.Overrides {
		all = append(all, strings.ToLower(o.ContentType))
	}
	found := ""
	for _, t := range all {
		switch {
		case strings.Contains(t, "macroenabled") || strings.Contains(t, "vbaproject") || strings.Contains(t, "ms-office.activex"):
			return "", ErrMacros
		case t == "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml":
			found = TypeDOCX
		case t == "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml":
			found = TypeXLSX
		case t == "application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml":
			found = TypePPTX
		}
	}
	if found == "" {
		return "", fmt.Errorf("%w: an Office file of a kind that is not accepted", ErrFile)
	}
	return found, nil
}

// checkCompoundDocument checks an old Office file (Word 97–2003, Excel,
// PowerPoint), a compound file: its type by the streams it holds, refused
// with macros or a password. A password-protected new Office file is a
// compound file too.
func checkCompoundDocument(data []byte) (ctype string, err error) {
	defer func() {
		if v := recover(); v != nil {
			ctype, err = "", fmt.Errorf("%w: a damaged document", ErrFile)
		}
	}()
	r, err := mscfb.New(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("%w: a damaged document", ErrFile)
	}
	streams := map[string]*mscfb.File{}
	n := 0
	for f, err := r.Next(); err == nil; f, err = r.Next() {
		if n++; n > MaxDocumentEntries {
			return "", fmt.Errorf("%w: the document holds more than %d parts", ErrFile, MaxDocumentEntries)
		}
		switch f.Name {
		case "Macros", "_VBA_PROJECT_CUR", "VBA", "_VBA_PROJECT", "PROJECT", "PROJECTwm":
			return "", ErrMacros
		}
		if len(f.Path) == 0 {
			streams[f.Name] = f
		}
	}
	if streams["EncryptionInfo"] != nil || streams["EncryptedPackage"] != nil || streams["EncryptedSummary"] != nil {
		return "", ErrProtected
	}
	read := func(f *mscfb.File, n int64) []byte {
		b := make([]byte, min(n, f.Size))
		k, _ := io.ReadFull(f, b)
		return b[:k]
	}
	switch {
	case streams["WordDocument"] != nil:
		// The file information block: its magic, then fEncrypted.
		fib := read(streams["WordDocument"], 12)
		if len(fib) < 12 || binary.LittleEndian.Uint16(fib) != 0xA5EC {
			return "", fmt.Errorf("%w: a damaged Word document", ErrFile)
		}
		if binary.LittleEndian.Uint16(fib[10:])&0x0100 != 0 {
			return "", ErrProtected
		}
		return TypeDOC, nil
	case streams["Workbook"] != nil || streams["Book"] != nil:
		wb := streams["Workbook"]
		if wb == nil {
			wb = streams["Book"]
		}
		// The workbook's first records, up to its first EOF: a FILEPASS
		// record says it is encrypted.
		b := read(wb, 64<<10)
		for i := 0; i+4 <= len(b); {
			typ, size := binary.LittleEndian.Uint16(b[i:]), int(binary.LittleEndian.Uint16(b[i+2:]))
			if typ == 0x002F {
				return "", ErrProtected
			}
			if typ == 0x000A {
				break
			}
			i += 4 + size
		}
		return TypeXLS, nil
	case streams["PowerPoint Document"] != nil:
		// A VBAInfoContainer record (version 0xF, type 0x03FF) holds a
		// presentation's macros.
		b := read(streams["PowerPoint Document"], 32<<20)
		if bytes.Contains(b, []byte{0x0F, 0x00, 0xFF, 0x03}) {
			return "", ErrMacros
		}
		return TypePPT, nil
	}
	return "", fmt.Errorf("%w: a compound file that is not a Word, Excel or PowerPoint document", ErrFile)
}

// ChatFileName is a file's name as kept and shown: its own name without
// path parts, control characters or right-to-left marks, at most 120
// characters, and with the extension of its real type, so "invoice.pdf.exe"
// that is a PDF shows as "invoice.pdf" and a renamed file its real kind.
// A text file named .csv is kept as CSV (TextType).
func ChatFileName(name, ctype string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsControl(r), r == 0x200E, r == 0x200F, r == 0x061C, r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069, r == 0xFEFF:
			return -1
		}
		return r
	}, name)
	name = strings.TrimRight(strings.TrimSpace(name), ". ")
	exts := extsOf(baseType(ctype))
	stem := name
	if len(exts) > 0 {
		low := strings.ToLower(name)
		kept := ""
		for _, e := range exts {
			if strings.HasSuffix(low, e) {
				kept = name[len(name)-len(e):]
				break
			}
		}
		if kept == "" {
			// Not its real extension: the last one goes, unless what is
			// left ends with the real one (invoice.pdf.exe).
			if i := strings.LastIndexByte(name, '.'); i > 0 {
				stem = name[:i]
			}
			low = strings.ToLower(stem)
			for _, e := range exts {
				if strings.HasSuffix(low, e) {
					kept = stem[len(stem)-len(e):]
					stem = stem[:len(stem)-len(e)]
					break
				}
			}
			if kept == "" {
				kept = exts[0]
			}
		} else {
			stem = name[:len(name)-len(kept)]
		}
		stem = strings.Trim(strings.TrimSpace(stem), ". ")
		if stem == "" {
			stem = "file"
		}
		if r := []rune(stem); len(r)+len([]rune(kept)) > 120 {
			stem = string(r[:120-len([]rune(kept))])
		}
		return stem + kept
	}
	if r := []rune(stem); len(r) > 120 {
		stem = string(r[:120])
	}
	return stem
}

// TextType is a text file's type by its name: CSV when it is named .csv,
// else plain text. The bytes are the same; only the icon differs.
func TextType(name, ctype string) string {
	if baseType(ctype) == TypeText && strings.HasSuffix(strings.ToLower(strings.TrimSpace(name)), ".csv") {
		return TypeCSV
	}
	return ctype
}

func extsOf(ctype string) []string {
	if d, ok := documentTypes[ctype]; ok {
		return d.exts
	}
	switch ctype {
	case "application/pdf":
		return []string{".pdf"}
	case "image/jpeg":
		return []string{".jpg", ".jpeg"}
	case "image/png":
		return []string{".png"}
	case "image/webp":
		return []string{".webp"}
	case "audio/ogg":
		return []string{".ogg", ".opus", ".oga"}
	case "audio/webm":
		return []string{".webm"}
	case "audio/mpeg":
		return []string{".mp3"}
	case "audio/mp4":
		return []string{".m4a", ".mp4"}
	}
	return nil
}

// errNotAccepted is the refusal of a file of a kind not on the list.
func errNotAccepted(sniffed string) error {
	return fmt.Errorf("%w: a %s file; Taskiem can send photos, voice notes, PDFs and Word, Excel, PowerPoint and text documents", ErrFile, sniffed)
}
