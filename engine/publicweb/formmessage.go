package publicweb

import (
	"html/template"
	"regexp"
	"strings"
)

// A form's thank-you message is minimal Markdown (forms 6.6): paragraphs,
// line breaks, **bold**, and [links](https://…) only to the
// organisation's verified websites. Any other link keeps its words and
// loses its address; anything else is shown as typed.

var (
	mdLink = regexp.MustCompile(`\[([^\]\n]{1,200})\]\(([^)\s]{1,500})\)`)
	mdBold = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	mdPara = regexp.MustCompile(`\n[ \t]*\n+`)
)

// formMessage renders a thank-you message; empty is the default.
func formMessage(md string, origins []string) template.HTML {
	md = strings.TrimSpace(strings.ReplaceAll(md, "\r\n", "\n"))
	if md == "" {
		md = "Thank you. We have your answers."
	}
	var b strings.Builder
	for _, para := range mdPara.Split(md, -1) {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		b.WriteString("<p>")
		last := 0
		for _, m := range mdLink.FindAllStringSubmatchIndex(para, -1) {
			b.WriteString(inline(para[last:m[0]]))
			words, href := para[m[2]:m[3]], para[m[4]:m[5]]
			if target, ok := safeRedirect(href, origins); ok {
				b.WriteString(`<a href="` + template.HTMLEscapeString(target) + `" rel="noopener noreferrer" target="_top">` + inline(words) + `</a>`)
			} else {
				b.WriteString(inline(words))
			}
			last = m[1]
		}
		b.WriteString(inline(para[last:]))
		b.WriteString("</p>")
	}
	//nolint:gosec // G203: every piece of the message is escaped above; only <p>, <br>, <strong> and links to verified websites are added
	return template.HTML(b.String())
}

// inline escapes text and makes its **bold** and line breaks.
func inline(s string) string {
	s = template.HTMLEscapeString(s)
	s = mdBold.ReplaceAllString(s, "<strong>$1</strong>")
	return strings.ReplaceAll(s, "\n", "<br>")
}
