// Package inmail receives email for workflows' email triggers (decision
// 0038, docs/email.md): the mail role's SMTP server, which takes mail for
// known addresses only and leaves it sealed in the inbox; the parser,
// which turns a raw message into what a run sees; and the api side, which
// drains the inbox, keeps attachments and starts runs.
package inmail

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/transform"
)

// MaxMessageBytes is the largest message taken (SMTP SIZE).
const MaxMessageBytes = 25 << 20

// Limits on a message's structure, so a crafted message cannot make the
// parser work without end.
const (
	maxParts = 100
	maxDepth = 10
)

// Address is a mailbox: a display name and an address.
type Address struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address"`
}

// Attachment is a file in a message, decoded.
type Attachment struct {
	Name string
	Type string
	Data []byte
	// Inline is a part shown in the body (an image in the HTML), not
	// attached by the sender.
	Inline bool
}

// Message is a parsed email.
type Message struct {
	MessageID   string
	From        Address
	To, Cc      []Address
	ReplyTo     []Address
	Subject     string
	Date        time.Time
	Text, HTML  string
	Attachments []Attachment
	// Header is the top-level header, decoded as sent.
	Header textproto.MIMEHeader
}

// ErrMalformed is a message that cannot be read as email.
var ErrMalformed = errors.New("inmail: malformed message")

var words = &mime.WordDecoder{CharsetReader: charsetReader}

func charsetReader(charset string, r io.Reader) (io.Reader, error) {
	charset = strings.ToLower(strings.TrimSpace(charset))
	if charset == "" || charset == "utf-8" || charset == "us-ascii" || charset == "utf8" {
		return r, nil
	}
	enc, err := htmlindex.Get(charset)
	if err != nil {
		return nil, fmt.Errorf("inmail: unknown charset %q", charset)
	}
	return transform.NewReader(r, enc.NewDecoder()), nil
}

// Parse reads a raw RFC 5322 message.
func Parse(raw []byte) (*Message, error) {
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	h := textproto.MIMEHeader(m.Header)
	out := &Message{Header: h}
	out.MessageID = strings.Trim(strings.TrimSpace(h.Get("Message-Id")), "<>")
	out.Subject = decodeWords(h.Get("Subject"))
	if d, err := mail.ParseDate(h.Get("Date")); err == nil {
		out.Date = d
	}
	if from := addresses(h.Get("From")); len(from) > 0 {
		out.From = from[0]
	}
	out.To = addresses(h.Get("To"))
	out.Cc = addresses(h.Get("Cc"))
	out.ReplyTo = addresses(h.Get("Reply-To"))
	w := &walker{msg: out}
	if err := w.part(h, m.Body, 0); err != nil {
		return nil, err
	}
	if out.Text == "" && out.HTML != "" {
		out.Text = TextFromHTML(out.HTML)
	}
	out.Text = strings.TrimSpace(out.Text)
	return out, nil
}

func decodeWords(s string) string {
	if d, err := words.DecodeHeader(s); err == nil {
		s = d
	}
	return strings.TrimSpace(validUTF8(s))
}

// addresses parses an address list, keeping what can be read of a
// malformed one.
func addresses(v string) []Address {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	p := mail.AddressParser{WordDecoder: words}
	list, err := p.ParseList(v)
	if err != nil {
		if a, err := p.Parse(v); err == nil {
			list = []*mail.Address{a}
		} else {
			return nil
		}
	}
	out := make([]Address, 0, len(list))
	for _, a := range list {
		out = append(out, Address{Name: validUTF8(a.Name), Address: strings.ToLower(a.Address)})
	}
	return out
}

type walker struct {
	msg   *Message
	parts int
}

func (w *walker) part(h textproto.MIMEHeader, body io.Reader, depth int) error {
	w.parts++
	if w.parts > maxParts || depth > maxDepth {
		return fmt.Errorf("%w: too many parts", ErrMalformed)
	}
	ctype, params, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil {
		ctype, params = "text/plain", map[string]string{"charset": "us-ascii"}
	}
	if strings.HasPrefix(ctype, "multipart/") {
		b := params["boundary"]
		if b == "" {
			return fmt.Errorf("%w: a multipart part without a boundary", ErrMalformed)
		}
		mr := multipart.NewReader(body, b)
		for {
			p, err := mr.NextRawPart()
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				// A truncated multipart keeps the parts read so far.
				return nil //nolint:nilerr // what was read is kept
			}
			if err := w.part(p.Header, p, depth+1); err != nil {
				return err
			}
		}
	}
	// A part cut short (a truncated message) keeps what was read.
	data, _ := io.ReadAll(decodeTransfer(h.Get("Content-Transfer-Encoding"), body))
	disp, dparams, _ := mime.ParseMediaType(h.Get("Content-Disposition"))
	name := decodeWords(dparams["filename"])
	if name == "" {
		name = decodeWords(params["name"])
	}
	attached := disp == "attachment" || (name != "" && !strings.HasPrefix(ctype, "text/"))
	switch {
	case !attached && ctype == "text/plain" && w.msg.Text == "":
		w.msg.Text = decodeText(data, params["charset"])
		return nil
	case !attached && ctype == "text/html" && w.msg.HTML == "":
		w.msg.HTML = decodeText(data, params["charset"])
		return nil
	case !attached && strings.HasPrefix(ctype, "text/"):
		// A second text part (a signature, an alternative): already have one.
		return nil
	}
	if name == "" {
		name = "attachment"
		if exts, _ := mime.ExtensionsByType(ctype); len(exts) > 0 {
			name += exts[0]
		}
	}
	w.msg.Attachments = append(w.msg.Attachments, Attachment{Name: name, Type: ctype, Data: data, Inline: disp == "inline" || (disp == "" && h.Get("Content-Id") != "")})
	return nil
}

func decodeTransfer(enc string, r io.Reader) io.Reader {
	switch strings.ToLower(strings.TrimSpace(enc)) {
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, &stripSpace{r: r})
	case "quoted-printable":
		return quotedprintable.NewReader(r)
	}
	return r
}

// stripSpace drops the line breaks and spaces in base64 bodies, which
// the standard decoder only partly tolerates.
type stripSpace struct{ r io.Reader }

func (s *stripSpace) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	j := 0
	for _, c := range p[:n] {
		if c != '\r' && c != '\n' && c != ' ' && c != '\t' {
			p[j] = c
			j++
		}
	}
	if j == 0 && n > 0 && err == nil {
		return s.Read(p)
	}
	return j, err
}

func decodeText(data []byte, charset string) string {
	r, err := charsetReader(charset, bytes.NewReader(data))
	if err != nil {
		return validUTF8(string(data))
	}
	out, err := io.ReadAll(r)
	if err != nil {
		return validUTF8(string(data))
	}
	return strings.ReplaceAll(validUTF8(string(out)), "\r\n", "\n")
}

func validUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "�")
}

// Domain is an address's domain, lower-cased.
func Domain(address string) string {
	at := strings.LastIndexByte(address, '@')
	if at < 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(address[at+1:], "."))
}
