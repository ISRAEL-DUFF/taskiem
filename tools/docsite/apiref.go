package main

import (
	"bytes"
	"fmt"
	"html/template"
	"sort"
	"strings"

	"github.com/israel-duff/taskiem/api/openapi"
)

// The API reference: /api (introduction, authentication, errors and the
// groups), /api/<group> (its operations, anchored by operationId) and
// /api/schemas (the shared schemas).

func tagSlug(name string) string { return slugify(name) }

func apiNav(cfg Config, doc *openapi.Document) []navItem {
	items := []navItem{{Title: "Overview", URL: cfg.Base + "api"}}
	for _, t := range doc.Tags {
		items = append(items, navItem{Title: t.Name, URL: cfg.Base + "api/" + tagSlug(t.Name)})
	}
	return append(items, navItem{Title: "Schemas", URL: cfg.Base + "api/schemas"})
}

// mdHTML renders Markdown; a single paragraph loses its <p>.
func mdHTML(s string) template.HTML {
	if s == "" {
		return ""
	}
	out, err := parseMarkdown([]byte(s)).render()
	if err != nil {
		return template.HTML(template.HTMLEscapeString(s)) //nolint:gosec // escaped
	}
	out = bytes.TrimSpace(out)
	if bytes.Count(out, []byte("<p>")) == 1 && bytes.HasPrefix(out, []byte("<p>")) && bytes.HasSuffix(out, []byte("</p>")) {
		out = out[3 : len(out)-4]
	}
	return template.HTML(out) //nolint:gosec // goldmark escapes text and drops raw HTML
}

var schemeNames = map[string]string{
	"sessionCookie": "session cookie",
	"csrfHeader":    "X-Taskiem-Request header",
	"sessionToken":  "session token",
	"apiKey":        "API key",
	"endUserToken":  "end-user token",
}

func securityText(reqs []map[string][]string) string {
	if len(reqs) == 0 {
		return "None"
	}
	var alts []string
	for _, req := range reqs {
		var names []string
		for n := range req {
			names = append(names, n)
		}
		sort.Slice(names, func(i, j int) bool { return names[i] > names[j] }) // the cookie before its header
		for i, n := range names {
			if v, ok := schemeNames[n]; ok {
				names[i] = v
			}
		}
		alts = append(alts, strings.Join(names, " with "))
	}
	return strings.Join(alts, ", or ")
}

func (s *site) renderAPI(doc *openapi.Document) error {
	esc := template.HTMLEscapeString
	var b strings.Builder

	// Overview.
	fmt.Fprintf(&b, "<h1>%s</h1><p class=\"lead\">%s</p>", esc(doc.Info.Title), mdHTML(doc.Info.Summary))
	b.WriteString(string(mdHTML(doc.Info.Description)))
	b.WriteString(`<h2 id="security-schemes">Security schemes</h2><table><thead><tr><th>Name</th><th>Kind</th><th>Description</th></tr></thead><tbody>`)
	names := make([]string, 0, len(doc.Components.SecuritySchemes))
	for n := range doc.Components.SecuritySchemes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		sc := doc.Components.SecuritySchemes[n]
		kind := sc.Type
		switch sc.Type {
		case "http":
			kind = "HTTP " + sc.Scheme
			if sc.BearerFormat != "" {
				kind += " (" + sc.BearerFormat + ")"
			}
		case "apiKey":
			kind = sc.In + " <code>" + esc(sc.Name) + "</code>"
		}
		fmt.Fprintf(&b, "<tr><td><code>%s</code></td><td>%s</td><td>%s</td></tr>", esc(n), kind, mdHTML(sc.Description))
	}
	b.WriteString(`</tbody></table><h2 id="groups">Groups</h2><table><thead><tr><th>Group</th><th>What it covers</th></tr></thead><tbody>`)
	ops := doc.Operations()
	byTag := map[string][]openapi.Op{}
	for _, op := range ops {
		byTag[op.Tags[0]] = append(byTag[op.Tags[0]], op)
	}
	for _, t := range doc.Tags {
		fmt.Fprintf(&b, `<tr><td><a href="%s">%s</a></td><td>%s (%d operations)</td></tr>`, esc(s.cfg.Base+"api/"+tagSlug(t.Name)), esc(t.Name), mdHTML(t.Description), len(byTag[t.Name]))
	}
	fmt.Fprintf(&b, `</tbody></table><p>The document itself: <a href="%sapi/openapi.yaml">openapi.yaml</a> (OpenAPI 3.1).</p>`, esc(s.cfg.Base))
	s.entries = append(s.entries, entry{Title: "API reference", URL: s.cfg.Base + "api", Text: doc.Info.Summary + " " + strings.Join(strings.Fields(doc.Info.Description), " ")})
	if err := s.render("api.html", "API reference", "api", template.HTML(b.String()), ""); err != nil { //nolint:gosec // escaped above
		return err
	}

	// One page per group.
	for _, t := range doc.Tags {
		b.Reset()
		fmt.Fprintf(&b, "<h1>%s</h1><p class=\"lead\">%s</p>", esc(t.Name), mdHTML(t.Description))
		if g, ok := s.pages[t.Guide]; ok {
			fmt.Fprintf(&b, `<p>Guide: <a href="%s">%s</a></p>`, esc(s.url(t.Guide)), esc(g.Title))
		}
		b.WriteString(`<ul class="toc">`)
		for _, op := range byTag[t.Name] {
			fmt.Fprintf(&b, `<li><a href="#%s"><span class="method %s">%s</span> <code>%s</code></a> %s</li>`, esc(op.OperationID), strings.ToLower(op.Method), op.Method, esc(op.Path), esc(op.Summary))
		}
		b.WriteString(`</ul>`)
		for _, op := range byTag[t.Name] {
			s.renderOp(&b, doc, op)
			s.entries = append(s.entries, entry{
				Title: "API: " + t.Name, Section: op.Summary, URL: s.cfg.Base + "api/" + tagSlug(t.Name) + "#" + op.OperationID,
				Text: op.Method + " " + op.Path + " " + op.Description + " " + op.Permission,
			})
		}
		name := "api/" + tagSlug(t.Name)
		if err := s.render(name+".html", t.Name+" API", name, template.HTML(b.String()), ""); err != nil { //nolint:gosec // escaped above
			return err
		}
	}

	// Shared schemas.
	b.Reset()
	b.WriteString(`<h1>Schemas</h1><p class="lead">The shapes operations share. A field marked required is always present.</p>`)
	names = names[:0]
	for n := range doc.Components.Schemas {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		sch := doc.Components.Schemas[n]
		fmt.Fprintf(&b, `<h2 id="%s">%s</h2>`, esc(n), esc(n))
		if d, _ := sch["description"].(string); d != "" {
			fmt.Fprintf(&b, "<p>%s</p>", mdHTML(d))
		}
		s.schemaBlock(&b, sch, true)
	}
	return s.render("api/schemas.html", "API schemas", "api/schemas", template.HTML(b.String()), "") //nolint:gosec // escaped above
}

func (s *site) renderOp(b *strings.Builder, doc *openapi.Document, op openapi.Op) {
	esc := template.HTMLEscapeString
	fmt.Fprintf(b, `<section class="op"><h2 id="%s">%s</h2><p class="endpoint"><span class="method %s">%s</span> <code>%s</code></p>`,
		esc(op.OperationID), esc(op.Summary), strings.ToLower(op.Method), op.Method, esc(op.Path))
	if op.Description != "" {
		fmt.Fprintf(b, "<p>%s</p>", mdHTML(op.Description))
	}
	b.WriteString(`<dl class="facts">`)
	fmt.Fprintf(b, "<dt>Authentication</dt><dd>%s</dd>", esc(securityText(op.Security)))
	if op.Permission != "" {
		fmt.Fprintf(b, "<dt>Permission</dt><dd><code>%s</code></dd>", esc(op.Permission))
	}
	if len(op.Features) > 0 {
		var fs []string
		for _, f := range op.Features {
			fs = append(fs, "<code>"+esc(f)+"</code>")
		}
		fmt.Fprintf(b, "<dt>Plan feature</dt><dd>%s (when billing is on)</dd>", strings.Join(fs, ", "))
	}
	if op.AllEnvironments {
		b.WriteString("<dt>Environment-limited keys</dt><dd>Refused: this reaches every environment</dd>")
	}
	b.WriteString("</dl>")
	if len(op.Parameters) > 0 {
		b.WriteString(`<h3>Parameters</h3><table><thead><tr><th>Name</th><th>In</th><th>Type</th><th>Description</th></tr></thead><tbody>`)
		for _, p := range op.Parameters {
			req := ""
			if p.Required {
				req = ` <span class="req">required</span>`
			}
			fmt.Fprintf(b, "<tr><td><code>%s</code>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>", esc(p.Name), req, esc(p.In), s.typeHTML(p.Schema), mdHTML(p.Description))
		}
		b.WriteString("</tbody></table>")
	}
	if rb := op.RequestBody; rb != nil {
		b.WriteString("<h3>Request body</h3>")
		for _, ct := range sortedKeys(rb.Content) {
			fmt.Fprintf(b, "<p><code>%s</code></p>", esc(ct))
			s.schemaBlock(b, rb.Content[ct].Schema, false)
		}
	}
	b.WriteString(`<h3>Responses</h3><table><thead><tr><th>Status</th><th>Description</th><th>Body</th></tr></thead><tbody>`)
	for _, code := range sortedKeys(op.Responses) {
		r := doc.Response(op.Responses[code])
		var body []string
		for _, ct := range sortedKeys(r.Content) {
			body = append(body, "<code>"+esc(ct)+"</code> "+string(s.typeHTML(r.Content[ct].Schema)))
		}
		status := code
		if code == "default" {
			status = "4xx, 5xx"
		}
		fmt.Fprintf(b, "<tr><td>%s</td><td>%s</td><td>%s</td></tr>", esc(status), mdHTML(r.Description), strings.Join(body, "<br>"))
	}
	b.WriteString("</tbody></table></section>")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i] == "default" || keys[j] == "default" {
			return keys[j] == "default" && keys[i] != "default"
		}
		return keys[i] < keys[j]
	})
	return keys
}

// typeHTML is a one-line description of a schema's type.
func (s *site) typeHTML(sch map[string]any) template.HTML {
	esc := template.HTMLEscapeString
	if sch == nil {
		return ""
	}
	if ref, ok := sch["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/components/schemas/")
		return template.HTML(fmt.Sprintf(`<a href="%sapi/schemas#%s">%s</a>`, esc(s.cfg.Base), esc(name), esc(name))) //nolint:gosec // escaped
	}
	var types []string
	switch t := sch["type"].(type) {
	case string:
		types = []string{t}
	case []any:
		for _, x := range t {
			types = append(types, fmt.Sprint(x))
		}
	}
	for i, t := range types {
		if t == "array" {
			if items, ok := sch["items"].(map[string]any); ok {
				types[i] = "array of " + string(s.typeHTML(items))
				continue
			}
		}
		types[i] = esc(t)
	}
	out := strings.Join(types, " or ")
	if out == "" {
		out = "any"
	}
	if f, ok := sch["format"].(string); ok {
		out += " (" + esc(f) + ")"
	}
	if e, ok := sch["enum"].([]any); ok {
		var vals []string
		for _, v := range e {
			vals = append(vals, "<code>"+esc(fmt.Sprint(v))+"</code>")
		}
		out += ": " + strings.Join(vals, ", ")
	}
	return template.HTML(out) //nolint:gosec // escaped above
}

// schemaBlock shows a schema: a link to a shared one, or its fields.
func (s *site) schemaBlock(b *strings.Builder, sch map[string]any, top bool) {
	props, _ := sch["properties"].(map[string]any)
	if _, ref := sch["$ref"]; ref || len(props) == 0 {
		if !top || sch["type"] != "object" {
			fmt.Fprintf(b, "<p>%s</p>", s.typeHTML(sch))
		}
		return
	}
	b.WriteString(`<table class="fields"><thead><tr><th>Field</th><th>Type</th><th>Description</th></tr></thead><tbody>`)
	s.fieldRows(b, sch, "", 0)
	b.WriteString("</tbody></table>")
}

func (s *site) fieldRows(b *strings.Builder, sch map[string]any, prefix string, depth int) {
	esc := template.HTMLEscapeString
	props, _ := sch["properties"].(map[string]any)
	required := map[string]bool{}
	if req, ok := sch["required"].([]any); ok {
		for _, r := range req {
			required[fmt.Sprint(r)] = true
		}
	}
	for _, name := range sortedKeys(props) {
		p, _ := props[name].(map[string]any)
		req := ""
		if required[name] {
			req = ` <span class="req">required</span>`
		}
		desc, _ := p["description"].(string)
		fmt.Fprintf(b, "<tr><td><code>%s%s</code>%s</td><td>%s</td><td>%s</td></tr>", esc(prefix), esc(name), req, s.typeHTML(p), mdHTML(desc))
		if depth >= 3 {
			continue
		}
		if _, ok := p["properties"]; ok {
			s.fieldRows(b, p, prefix+name+".", depth+1)
		} else if items, ok := p["items"].(map[string]any); ok {
			if _, ok := items["properties"]; ok {
				s.fieldRows(b, items, prefix+name+"[].", depth+1)
			}
		}
	}
}
