package inmail

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// TextFromHTML is an HTML body as plain text: its words in reading order,
// a line per block, list items with a dash, and no scripts, styles or
// markup.
func TextFromHTML(s string) string {
	z := html.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	skip := 0
	newline := func() {
		if t := b.String(); t != "" && !strings.HasSuffix(t, "\n") {
			b.WriteByte('\n')
		}
	}
	for {
		switch z.Next() {
		case html.ErrorToken:
			return tidy(b.String())
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "script", "style", "head", "title":
				skip++
			case "br":
				b.WriteByte('\n')
			case "li":
				newline()
				b.WriteString("- ")
			case "p", "div", "tr", "h1", "h2", "h3", "h4", "h5", "h6", "table", "ul", "ol", "blockquote", "hr":
				newline()
			case "td", "th":
				b.WriteByte(' ')
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "script", "style", "head", "title":
				if skip > 0 {
					skip--
				}
			case "p", "div", "tr", "h1", "h2", "h3", "h4", "h5", "h6", "table", "li":
				newline()
			}
		case html.TextToken:
			if skip == 0 {
				b.WriteString(strings.Join(strings.Fields(string(z.Text())), " "))
				if t := z.Raw(); len(t) > 0 && (t[len(t)-1] == ' ' || t[len(t)-1] == '\n') {
					b.WriteByte(' ')
				}
			}
		}
	}
}

// tidy trims each line and keeps at most one blank line in a row.
func tidy(s string) string {
	var out []string
	blank := false
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			if !blank && len(out) > 0 {
				out = append(out, "")
			}
			blank = true
			continue
		}
		blank = false
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// Where an earlier message starts in a reply: "On Mon, 3 Jun 2026 at
// 10:02, Ada <ada@x.ng> wrote:" (which mail apps may wrap onto a second
// line), Outlook's separator and header block, and quoted lines.
var (
	wroteLine     = regexp.MustCompile(`(?i)^(on|le|am|el)\s.{4,200}\s(wrote|a écrit|schrieb|escribió)\s*:\s*$`)
	wroteStart    = regexp.MustCompile(`(?i)^on\s.{4,200}$`)
	wroteEnd      = regexp.MustCompile(`(?i)^.{0,200}wrote\s*:\s*$`)
	outlookMarker = regexp.MustCompile(`(?i)^(-{2,}\s*original message\s*-{2,}|_{10,})\s*$`)
	headerBlock   = regexp.MustCompile(`(?i)^from:\s.+`)
)

// Latest is the newest part of a reply: the text above the quoted earlier
// messages. A message that quotes nothing is returned whole.
func Latest(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	cut := len(lines)
	for i := 0; i < len(lines); i++ {
		l := strings.TrimSpace(lines[i])
		if wroteLine.MatchString(l) || outlookMarker.MatchString(l) {
			cut = i
			break
		}
		if i+1 < len(lines) && wroteStart.MatchString(l) && wroteEnd.MatchString(strings.TrimSpace(lines[i+1])) {
			cut = i
			break
		}
		// Outlook without a separator: a From: line right after a blank one,
		// followed by Sent: or Date:.
		if headerBlock.MatchString(l) && i > 0 && strings.TrimSpace(lines[i-1]) == "" && i+1 < len(lines) {
			next := strings.ToLower(strings.TrimSpace(lines[i+1]))
			if strings.HasPrefix(next, "sent:") || strings.HasPrefix(next, "date:") {
				cut = i
				break
			}
		}
	}
	kept := lines[:cut]
	// Quoted lines at the end (">" replies with no "wrote" line).
	end := len(kept)
	for end > 0 {
		l := strings.TrimSpace(kept[end-1])
		if l == "" || strings.HasPrefix(l, ">") {
			end--
			continue
		}
		break
	}
	out := strings.TrimSpace(strings.Join(kept[:end], "\n"))
	if out == "" {
		return strings.TrimSpace(text)
	}
	return out
}
