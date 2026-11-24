package extract

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Page is one page read from a website.
type Page struct {
	URL   string
	Title string
	// Text has the page's headings, paragraphs, list items and table rows
	// (cells joined by " | ") as lines. Scripts, styles, navigation, page
	// headers and footers, forms and hidden elements are left out.
	Text string
}

// dropped are elements whose content is never text a customer would ask
// about: code, styling, graphics, embedded documents and interactive
// controls (a form's labels describe the form, not the business).
var dropped = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Noscript: true, atom.Svg: true,
	atom.Template: true, atom.Form: true, atom.Nav: true, atom.Iframe: true,
	atom.Object: true, atom.Embed: true, atom.Canvas: true, atom.Math: true,
	atom.Head: true, atom.Button: true, atom.Select: true, atom.Textarea: true,
	atom.Input: true, atom.Dialog: true, atom.Audio: true, atom.Video: true,
	atom.Picture: true, atom.Img: true, atom.Map: true,
}

// chromeRoles are ARIA roles for page chrome rather than content.
var chromeRoles = map[string]bool{"navigation": true, "banner": true, "contentinfo": true, "search": true, "dialog": true, "alertdialog": true, "menu": true, "menubar": true}

// blocks are elements that start and end a line.
var blocks = map[atom.Atom]bool{
	atom.P: true, atom.Div: true, atom.H1: true, atom.H2: true, atom.H3: true,
	atom.H4: true, atom.H5: true, atom.H6: true, atom.Li: true, atom.Ul: true,
	atom.Ol: true, atom.Dl: true, atom.Dt: true, atom.Dd: true, atom.Section: true,
	atom.Article: true, atom.Main: true, atom.Aside: true, atom.Blockquote: true,
	atom.Pre: true, atom.Figure: true, atom.Figcaption: true, atom.Address: true,
	atom.Table: true, atom.Caption: true, atom.Thead: true, atom.Tbody: true,
	atom.Tfoot: true, atom.Hr: true, atom.Header: true, atom.Footer: true,
	atom.Details: true, atom.Summary: true, atom.Fieldset: true, atom.Legend: true,
	atom.Body: true,
}

// HTMLText turns an HTML document into a Page and returns the same-origin
// links it has: absolute, without fragments, deduplicated, in document
// order. The Title is the <title>, or failing that the first <h1>. The text
// comes from the page's <main> (or role="main") when it has one, else its
// <article> elements, else the whole body.
func HTMLText(doc []byte, pageURL *url.URL) (Page, []*url.URL, error) {
	if pageURL == nil {
		return Page{}, nil, fmt.Errorf("%w: no page URL", ErrUnreadable)
	}
	root, err := html.Parse(bytes.NewReader(doc))
	if err != nil {
		return Page{}, nil, fmt.Errorf("%w: %w", ErrUnreadable, err)
	}
	p := Page{URL: pageURL.String()}

	if t := find(root, func(n *html.Node) bool { return n.DataAtom == atom.Title }); t != nil {
		p.Title = inlineText(t)
	}
	if p.Title == "" {
		if h := find(root, func(n *html.Node) bool { return n.DataAtom == atom.H1 && !hiddenNode(n) }); h != nil {
			p.Title = inlineText(h)
		}
	}

	var roots []*html.Node
	walk(root, func(n *html.Node) bool {
		if hiddenNode(n) {
			return false
		}
		if n.DataAtom == atom.Main || attr(n, "role") == "main" {
			roots = append(roots, n)
			return false
		}
		return true
	})
	if len(roots) == 0 {
		walk(root, func(n *html.Node) bool {
			if hiddenNode(n) {
				return false
			}
			if n.DataAtom == atom.Article {
				roots = append(roots, n)
				return false
			}
			return true
		})
	}
	if len(roots) == 0 {
		roots = []*html.Node{root}
	}
	var w lineWriter
	for _, r := range roots {
		blockText(&w, r)
		w.line()
	}
	p.Text = w.String()
	return p, links(root, pageURL), nil
}

// walk visits n and its descendants in document order; visit returns
// false to skip a node's children.
func walk(n *html.Node, visit func(*html.Node) bool) {
	if n.Type == html.ElementNode && !visit(n) {
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, visit)
	}
}

// find returns the first element under n for which match is true.
func find(n *html.Node, match func(*html.Node) bool) *html.Node {
	var found *html.Node
	walk(n, func(c *html.Node) bool {
		if found != nil {
			return false
		}
		if match(c) {
			found = c
			return false
		}
		return true
	})
	return found
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return true
		}
	}
	return false
}

// hiddenNode reports whether an element is not shown to a reader: the
// hidden attribute, aria-hidden, or an inline style that hides it.
// Stylesheets are not applied, so an element hidden by a class still shows.
func hiddenNode(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	if hasAttr(n, "hidden") || strings.EqualFold(attr(n, "aria-hidden"), "true") {
		return true
	}
	if n.DataAtom == atom.Input && strings.EqualFold(attr(n, "type"), "hidden") {
		return true
	}
	style := strings.ToLower(strings.Join(strings.Fields(attr(n, "style")), ""))
	return strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden")
}

// skipped reports whether an element and its content are left out of the
// text.
func skipped(n *html.Node) bool {
	if n.Type == html.CommentNode {
		return true
	}
	if n.Type != html.ElementNode {
		return false
	}
	if dropped[n.DataAtom] || hiddenNode(n) || chromeRoles[strings.ToLower(attr(n, "role"))] {
		return true
	}
	// A header or footer is page chrome unless it belongs to a piece of
	// content (an article's byline, a section's heading group).
	if n.DataAtom == atom.Header || n.DataAtom == atom.Footer {
		for a := n.Parent; a != nil; a = a.Parent {
			switch a.DataAtom {
			case atom.Article, atom.Main, atom.Section, atom.Aside:
				return false
			}
		}
		return true
	}
	return false
}

// blockText writes n's text, a line per block element and a line per table
// row.
func blockText(w *lineWriter, n *html.Node) {
	if w.full() || skipped(n) {
		return
	}
	switch n.Type {
	case html.TextNode:
		w.text(n.Data)
		return
	case html.ElementNode:
		switch n.DataAtom {
		case atom.Br:
			w.line()
			return
		case atom.Tr:
			w.line()
			var cells []string
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.DataAtom == atom.Td || c.DataAtom == atom.Th {
					if !skipped(c) {
						cells = append(cells, inlineText(c))
					}
				}
			}
			if row := strings.Join(cells, " | "); strings.Trim(row, " |") != "" {
				w.text(row)
				w.line()
			}
			return
		case atom.Pre:
			// Preformatted text keeps its lines.
			w.line()
			for _, l := range strings.Split(inlineTextRaw(n), "\n") {
				w.text(l)
				w.line()
			}
			return
		}
	}
	block := n.Type == html.ElementNode && blocks[n.DataAtom]
	if block {
		w.line()
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		blockText(w, c)
	}
	if block {
		w.line()
	}
}

// inlineText is n's text on one line, white space collapsed.
func inlineText(n *html.Node) string {
	var w lineWriter
	w.text(inlineTextRaw(n))
	return w.String()
}

// inlineTextRaw is n's text as written, skipped elements left out and a
// line break for <br>.
func inlineTextRaw(n *html.Node) string {
	var b strings.Builder
	var rec func(*html.Node)
	rec = func(n *html.Node) {
		if b.Len() > MaxText || skipped(n) {
			return
		}
		switch {
		case n.Type == html.TextNode:
			b.WriteString(n.Data)
		case n.DataAtom == atom.Br:
			b.WriteByte('\n')
		case n.Type == html.ElementNode && blocks[n.DataAtom]:
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
		if n.Type == html.ElementNode && (blocks[n.DataAtom] || n.DataAtom == atom.Td || n.DataAtom == atom.Th) {
			b.WriteByte(' ')
		}
	}
	rec(n)
	return b.String()
}

// links returns the page's links to its own origin. A <base href> changes
// what relative links are relative to, as in a browser.
func links(root *html.Node, pageURL *url.URL) []*url.URL {
	base := pageURL
	if b := find(root, func(n *html.Node) bool { return n.DataAtom == atom.Base && hasAttr(n, "href") }); b != nil {
		if u, err := pageURL.Parse(attr(b, "href")); err == nil {
			base = u
		}
	}
	origin := originOf(pageURL)
	seen := map[string]bool{}
	var out []*url.URL
	walk(root, func(n *html.Node) bool {
		if n.DataAtom != atom.A && n.DataAtom != atom.Area {
			return true
		}
		href := attr(n, "href")
		if href == "" {
			return true
		}
		u, err := base.Parse(href)
		if err != nil || originOf(u) != origin {
			return true
		}
		normalise(u)
		if k := u.String(); !seen[k] {
			seen[k] = true
			out = append(out, u)
		}
		return true
	})
	return out
}

// originOf is a URL's scheme, host and port, with the scheme's default port
// made explicit so that example.com and example.com:443 match. It is empty
// for anything but http and https.
func originOf(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" || u.Host == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[scheme]
	}
	return scheme + "://" + strings.ToLower(u.Hostname()) + ":" + port
}

// normalise makes equivalent URLs spell the same: no fragment or user
// info, a lower-case host without its scheme's default port, and "/" for
// an empty path.
func normalise(u *url.URL) {
	u.Fragment, u.RawFragment, u.User = "", "", nil
	u.Scheme = strings.ToLower(u.Scheme)
	host, port := strings.ToLower(u.Hostname()), u.Port()
	if strings.Contains(host, ":") {
		host = "[" + host + "]" // IPv6
	}
	defaultPort := u.Scheme == "http" && port == "80" || u.Scheme == "https" && port == "443"
	if port != "" && !defaultPort {
		host += ":" + port
	}
	u.Host = host
	if u.Path == "" {
		u.Path = "/"
	}
}
