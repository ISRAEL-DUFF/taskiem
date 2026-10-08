package main

import (
	"bytes"
	"strconv"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// md parses GitHub-flavoured Markdown as the repository's docs are
// written: tables, strikethrough, task lists and bare links. Raw HTML is
// not rendered (goldmark's default).
var md = goldmark.New(goldmark.WithExtensions(extension.GFM))

// mdDoc is a parsed Markdown file.
type mdDoc struct {
	src      []byte
	root     ast.Node
	title    string    // the first level-1 heading
	headings []heading // every heading, with its GitHub-style id
	links    []mdLink
}

type heading struct {
	level int
	text  string
	id    string
	node  *ast.Heading
}

// mdLink is a link or image destination and where it is.
type mdLink struct {
	dest string
	line int
	node ast.Node // *ast.Link or *ast.Image
}

func parseMarkdown(src []byte) *mdDoc {
	d := &mdDoc{src: src, root: md.Parser().Parse(text.NewReader(src), parser.WithContext(parser.NewContext()))}
	used := map[string]int{}
	_ = ast.Walk(d.root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Heading:
			t := plainText(n, src)
			id := slugify(t)
			if k := used[id]; k > 0 {
				used[id] = k + 1
				id += "-" + strconv.Itoa(k)
			} else {
				used[id] = 1
			}
			n.SetAttributeString("id", []byte(id))
			d.headings = append(d.headings, heading{level: n.Level, text: t, id: id, node: n})
			if n.Level == 1 && d.title == "" {
				d.title = t
			}
		case *ast.Link:
			d.links = append(d.links, mdLink{dest: string(n.Destination), line: lineOf(n, src), node: n})
		case *ast.Image:
			d.links = append(d.links, mdLink{dest: string(n.Destination), line: lineOf(n, src), node: n})
		}
		return ast.WalkContinue, nil
	})
	return d
}

// hasAnchor reports whether a heading has the id.
func (d *mdDoc) hasAnchor(id string) bool {
	for _, h := range d.headings {
		if h.id == id {
			return true
		}
	}
	return false
}

// render writes the document as HTML.
func (d *mdDoc) render() ([]byte, error) {
	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, d.src, d.root); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// unlink replaces a link with its text.
func unlink(n ast.Node) {
	parent := n.Parent()
	if parent == nil {
		return
	}
	for c := n.FirstChild(); c != nil; {
		next := c.NextSibling()
		parent.InsertBefore(parent, n, c)
		c = next
	}
	parent.RemoveChild(parent, n)
}

// slugify makes a heading's id as GitHub does: lower case, punctuation
// dropped (but not hyphens or underscores), each space a hyphen.
func slugify(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// plainText is a node's text without markup.
func plainText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch c := c.(type) {
		case *ast.Text:
			b.Write(c.Value(src))
			if c.SoftLineBreak() || c.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(c.Value)
		case *ast.AutoLink:
			b.Write(c.Label(src))
		case *ast.RawHTML:
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

// blockText is the text of a block for the search index: paragraphs,
// list items, table cells and code, separated by spaces.
func blockText(n ast.Node, src []byte) string {
	if n.Type() == ast.TypeBlock {
		switch n.(type) {
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			var b strings.Builder
			lines := n.Lines()
			for i := range lines.Len() {
				seg := lines.At(i)
				b.Write(seg.Value(src))
			}
			return strings.Join(strings.Fields(b.String()), " ")
		}
	}
	return strings.Join(strings.Fields(plainText(n, src)), " ")
}

// lineOf finds the source line of an inline node from its block.
func lineOf(n ast.Node, src []byte) int {
	for p := n; p != nil; p = p.Parent() {
		if p.Type() == ast.TypeBlock && p.Lines().Len() > 0 {
			start := p.Lines().At(0).Start
			// Find the line holding the link's text, if it has any.
			if t, ok := n.FirstChild().(*ast.Text); ok && t != nil {
				start = t.Segment.Start
			}
			return bytes.Count(src[:start], []byte("\n")) + 1
		}
	}
	return 0
}
