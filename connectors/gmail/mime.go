package gmail

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"strings"
	"unicode/utf8"
)

// message is what send_message and create_draft build into RFC 5322.
type message struct {
	From, ReplyTo         string
	To, Cc, Bcc           []string
	Subject, Text, HTML   string
	InReplyTo, References string
	Attachments           []attachment
}

type attachment struct {
	Filename, ContentType string
	Data                  []byte
}

// errHeader is a header value that would break out of its header.
var errHeader = errors.New("header values may not contain line breaks or control characters")

// safeHeader rejects CR, LF and other control characters (except tab), so
// no input can add a header or end the header block.
func safeHeader(field, v string) error {
	for _, r := range v {
		if r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s: %w", field, errHeader)
		}
	}
	if !utf8.ValidString(v) {
		return fmt.Errorf("%s: not valid UTF-8", field)
	}
	return nil
}

// addresses parses address inputs (each may itself be a comma-separated
// list) and formats them again: names are encoded (RFC 2047) and quoted
// by net/mail, so nothing in an input survives as raw header syntax.
func addresses(field string, in []string) ([]string, error) {
	var out []string
	for _, s := range in {
		if strings.TrimSpace(s) == "" {
			continue
		}
		if err := safeHeader(field, s); err != nil {
			return nil, err
		}
		list, err := mail.ParseAddressList(s)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not an email address: %w", field, s, err)
		}
		for _, a := range list {
			out = append(out, a.String())
		}
	}
	return out, nil
}

// encodeWord encodes a free-text header (Subject) when it is not plain
// printable ASCII, and folds long values between encoded words.
func encodeWord(v string) string {
	ascii := true
	for i := 0; i < len(v); i++ {
		if v[i] >= 0x7f || v[i] < 0x20 {
			ascii = false
			break
		}
	}
	if ascii && len(v) <= 900 {
		return v
	}
	return strings.ReplaceAll(mime.QEncoding.Encode("utf-8", v), "?= =?", "?=\r\n =?")
}

// msgIDs checks In-Reply-To and References: message ids in angle brackets,
// printable ASCII only.
func msgIDs(field, v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if err := safeHeader(field, v); err != nil {
		return "", err
	}
	ids := strings.Fields(v)
	for _, id := range ids {
		if len(id) < 3 || id[0] != '<' || id[len(id)-1] != '>' || strings.ContainsAny(id[1:len(id)-1], "<>") {
			return "", fmt.Errorf("%s: %q is not a <message-id>", field, id)
		}
		for i := 0; i < len(id); i++ {
			if id[i] < 0x21 || id[i] > 0x7e {
				return "", fmt.Errorf("%s: %q is not a <message-id>", field, id)
			}
		}
	}
	return strings.Join(ids, "\r\n "), nil
}

// header writes "Name: value" lines; addresses are folded one per line.
type header struct{ b *bytes.Buffer }

func (h header) set(name, value string) {
	if value != "" {
		fmt.Fprintf(h.b, "%s: %s\r\n", name, value)
	}
}

func (h header) addrs(name string, list []string) {
	if len(list) > 0 {
		h.set(name, strings.Join(list, ",\r\n "))
	}
}

// base64Body encodes a part body in 76-character lines.
func base64Body(data []byte) []byte {
	enc := base64.StdEncoding.EncodeToString(data)
	var b bytes.Buffer
	for len(enc) > 76 {
		b.WriteString(enc[:76])
		b.WriteString("\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc)
	b.WriteString("\r\n")
	return b.Bytes()
}

func textPart(ctype, body string) (textproto.MIMEHeader, []byte) {
	h := textproto.MIMEHeader{}
	h.Set("Content-Type", mime.FormatMediaType(ctype, map[string]string{"charset": "UTF-8"}))
	h.Set("Content-Transfer-Encoding", "base64")
	return h, base64Body([]byte(body))
}

// bodyParts renders the text and HTML as one part, or as
// multipart/alternative when there are both, with its Content-Type.
func bodyParts(m message) (ctype string, body []byte, err error) {
	switch {
	case m.HTML == "":
		h, b := textPart("text/plain", m.Text)
		return h.Get("Content-Type"), b, nil
	case m.Text == "":
		h, b := textPart("text/html", m.HTML)
		return h.Get("Content-Type"), b, nil
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, p := range [][2]string{{"text/plain", m.Text}, {"text/html", m.HTML}} {
		h, b := textPart(p[0], p[1])
		w, err := mw.CreatePart(h)
		if err != nil {
			return "", nil, err
		}
		if _, err := w.Write(b); err != nil {
			return "", nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return "", nil, err
	}
	return mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": mw.Boundary()}), buf.Bytes(), nil
}

// build renders the message as RFC 5322 with MIME bodies. Every header value
// is checked or re-encoded; bodies are base64, so their content cannot be
// read as headers or boundaries.
func build(m message) ([]byte, error) {
	to, err := addresses("to", m.To)
	if err != nil {
		return nil, err
	}
	cc, err := addresses("cc", m.Cc)
	if err != nil {
		return nil, err
	}
	bcc, err := addresses("bcc", m.Bcc)
	if err != nil {
		return nil, err
	}
	if len(to)+len(cc)+len(bcc) == 0 {
		return nil, errors.New("at least one of to, cc and bcc is required")
	}
	var from, replyTo []string
	if m.From != "" {
		if from, err = addresses("from", []string{m.From}); err != nil {
			return nil, err
		}
		if len(from) != 1 {
			return nil, errors.New("from: one address")
		}
	}
	if replyTo, err = addresses("reply_to", []string{m.ReplyTo}); err != nil {
		return nil, err
	}
	if err := safeHeader("subject", m.Subject); err != nil {
		return nil, err
	}
	inReplyTo, err := msgIDs("in_reply_to", m.InReplyTo)
	if err != nil {
		return nil, err
	}
	refs, err := msgIDs("references", m.References)
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	h := header{&out}
	h.addrs("From", from)
	h.addrs("To", to)
	h.addrs("Cc", cc)
	h.addrs("Bcc", bcc)
	h.addrs("Reply-To", replyTo)
	h.set("Subject", encodeWord(m.Subject))
	h.set("In-Reply-To", inReplyTo)
	h.set("References", refs)
	h.set("MIME-Version", "1.0")

	ctype, body, err := bodyParts(m)
	if err != nil {
		return nil, err
	}
	if len(m.Attachments) == 0 {
		h.set("Content-Type", ctype)
		if !strings.HasPrefix(ctype, "multipart/") {
			h.set("Content-Transfer-Encoding", "base64")
		}
		out.WriteString("\r\n")
		out.Write(body)
		return out.Bytes(), nil
	}

	var parts bytes.Buffer
	mw := multipart.NewWriter(&parts)
	bh := textproto.MIMEHeader{}
	bh.Set("Content-Type", ctype)
	if !strings.HasPrefix(ctype, "multipart/") {
		bh.Set("Content-Transfer-Encoding", "base64")
	}
	w, err := mw.CreatePart(bh)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(body); err != nil {
		return nil, err
	}
	for i, a := range m.Attachments {
		field := fmt.Sprintf("attachments[%d]", i)
		if err := safeHeader(field+".filename", a.Filename); err != nil {
			return nil, err
		}
		ct := a.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		if err := safeHeader(field+".content_type", ct); err != nil {
			return nil, err
		}
		mt, params, err := mime.ParseMediaType(ct)
		if err != nil || strings.HasPrefix(mt, "multipart/") {
			return nil, fmt.Errorf("%s.content_type: %q is not a single media type", field, ct)
		}
		delete(params, "boundary")
		ah := textproto.MIMEHeader{}
		ah.Set("Content-Type", mime.FormatMediaType(mt, params))
		ah.Set("Content-Transfer-Encoding", "base64")
		disp := mime.FormatMediaType("attachment", map[string]string{"filename": a.Filename})
		if disp == "" {
			return nil, fmt.Errorf("%s.filename: %q cannot be encoded", field, a.Filename)
		}
		ah.Set("Content-Disposition", disp)
		w, err := mw.CreatePart(ah)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(base64Body(a.Data)); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	h.set("Content-Type", mime.FormatMediaType("multipart/mixed", map[string]string{"boundary": mw.Boundary()}))
	out.WriteString("\r\n")
	out.Write(parts.Bytes())
	return out.Bytes(), nil
}
