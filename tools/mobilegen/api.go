package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

// The phone app's API client (docs/spec/mobile-app.md §4): the operations
// it uses, listed in mobile/api.txt by operationId, generated from the API
// description so the app and the server cannot disagree unseen.

const (
	// APISpec is the API description, from the repository root.
	APISpec = "api/openapi/openapi.yaml"
	// APIList names the operations the app uses, one operationId a line.
	APIList = "mobile/api.txt"
	// APIOut is the generated client.
	APIOut = "mobile/lib/api/api.g.dart"
)

type js = map[string]any

// GenerateAPI answers the Dart client for the operations APIList names.
func GenerateAPI(root string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(root, APISpec))
	if err != nil {
		return "", err
	}
	var doc js
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("%s: %w", APISpec, err)
	}
	listRaw, err := os.ReadFile(filepath.Join(root, APIList))
	if err != nil {
		return "", err
	}
	var want []string
	for _, l := range strings.Split(string(listRaw), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			want = append(want, l)
		}
	}
	g := &apiGen{doc: doc, classes: map[string]*dartClass{}}
	ops := g.operations()
	var methods []string
	for _, id := range want {
		op, ok := ops[id]
		if !ok {
			return "", fmt.Errorf("%s: no operation %q in %s", APIList, id, APISpec)
		}
		m, err := g.method(op)
		if err != nil {
			return "", fmt.Errorf("%s: %w", id, err)
		}
		methods = append(methods, m)
	}
	return g.file(methods), nil
}

type operation struct {
	id, method, path string
	op               js
	params           []any
}

type apiGen struct {
	doc     js
	classes map[string]*dartClass
	order   []string
}

func (g *apiGen) operations() map[string]operation {
	out := map[string]operation{}
	paths, _ := g.doc["paths"].(js)
	for p, v := range paths {
		item, _ := v.(js)
		shared, _ := item["parameters"].([]any)
		for _, m := range []string{"get", "post", "put", "patch", "delete"} {
			op, ok := item[m].(js)
			if !ok {
				continue
			}
			if id, ok := op["operationId"].(string); ok {
				params, _ := op["parameters"].([]any)
				out[id] = operation{id: id, method: strings.ToUpper(m), path: p, op: op, params: append(append([]any{}, shared...), params...)}
			}
		}
	}
	return out
}

// resolve follows a $ref to its schema (components only).
func (g *apiGen) resolve(s js) (js, string) {
	ref, ok := s["$ref"].(string)
	if !ok {
		return s, ""
	}
	name := strings.TrimPrefix(ref, "#/components/schemas/")
	comps, _ := g.doc["components"].(js)
	schemas, _ := comps["schemas"].(js)
	target, _ := schemas[name].(js)
	return target, name
}

func (g *apiGen) response(r any) js {
	m, _ := r.(js)
	if ref, ok := m["$ref"].(string); ok {
		comps, _ := g.doc["components"].(js)
		resps, _ := comps["responses"].(js)
		m, _ = resps[strings.TrimPrefix(ref, "#/components/responses/")].(js)
	}
	content, _ := m["content"].(js)
	app, _ := content["application/json"].(js)
	s, _ := app["schema"].(js)
	return s
}

var pathParam = regexp.MustCompile(`\{([a-zA-Z_]+)\}`)

func (g *apiGen) method(o operation) (string, error) {
	name := lowerFirst(o.id)
	var args []string
	call := fmt.Sprintf("'%s'", o.path)
	if pathParam.MatchString(o.path) {
		call = "'" + pathParam.ReplaceAllStringFunc(o.path, func(m string) string {
			return "${Uri.encodeComponent(" + camelJSON(m[1:len(m)-1]) + ")}"
		}) + "'"
	}
	var query []string
	for _, p := range o.params {
		pm, _ := p.(js)
		if ref, ok := pm["$ref"].(string); ok {
			comps, _ := g.doc["components"].(js)
			ps, _ := comps["parameters"].(js)
			pm, _ = ps[strings.TrimPrefix(ref, "#/components/parameters/")].(js)
		}
		n, _ := pm["name"].(string)
		in, _ := pm["in"].(string)
		switch in {
		case "path":
			args = append(args, "required String "+camelJSON(n))
		case "query":
			args = append(args, "String? "+camelJSON(n))
			query = append(query, fmt.Sprintf("'%s': ?%s", n, camelJSON(n)))
		}
	}
	body := ""
	if rb, ok := o.op["requestBody"].(js); ok {
		s := g.response(rb)
		if s != nil {
			t, err := g.typeOf(s, upperFirst(o.id)+"Request", true)
			if err != nil {
				return "", err
			}
			args = append([]string{"required " + t.dart + " body"}, args...)
			body = ", body: " + t.toJSON("body")
		}
	}
	codes := []string{}
	resps, _ := o.op["responses"].(js)
	for c := range resps {
		if strings.HasPrefix(c, "2") {
			codes = append(codes, c)
		}
	}
	sort.Strings(codes)
	ret := "void"
	conv := ""
	for _, c := range codes {
		if s := g.response(resps[c]); s != nil {
			t, err := g.typeOf(s, upperFirst(o.id)+"Response", true)
			if err != nil {
				return "", err
			}
			ret, conv = t.dart, t.fromJSON("j")
			break
		}
	}
	q := ""
	if len(query) > 0 {
		q = ", query: {" + strings.Join(query, ", ") + "}"
	}
	sig := ""
	if len(args) > 0 {
		sig = "{" + strings.Join(args, ", ") + "}"
	}
	summary, _ := o.op["summary"].(string)
	var b strings.Builder
	fmt.Fprintf(&b, "  /// %s (`%s %s`)\n", summary, o.method, o.path)
	if ret == "void" {
		fmt.Fprintf(&b, "  Future<void> %s(%s) async {\n    await _t.send('%s', %s%s%s);\n  }\n", name, sig, o.method, call, body, q)
	} else {
		fmt.Fprintf(&b, "  Future<%s> %s(%s) async {\n    final j = await _t.send('%s', %s%s%s);\n    return %s;\n  }\n", ret, name, sig, o.method, call, body, q, conv)
	}
	return b.String(), nil
}

// dartType is how one schema is written in Dart, read from JSON and back.
type dartType struct {
	dart     string // without the ? of an optional field
	fromJSON func(v string) string
	toJSON   func(v string) string
}

func (g *apiGen) typeOf(s js, name string, top bool) (dartType, error) {
	if all, ok := s["allOf"].([]any); ok && len(all) == 1 {
		if one, ok := all[0].(js); ok {
			return g.typeOf(one, name, top)
		}
	}
	if _, ok := s["$ref"].(string); ok {
		target, ref := g.resolve(s)
		if target == nil {
			return dartType{}, fmt.Errorf("no schema %s", ref)
		}
		return g.typeOf(target, ref, true)
	}
	if _, ok := s["oneOf"]; ok {
		return plain("Object?"), nil
	}
	if _, ok := s["anyOf"]; ok {
		return plain("Object?"), nil
	}
	t, _ := s["type"].(string)
	if t == "" {
		if _, ok := s["properties"]; ok {
			t = "object"
		}
	}
	switch t {
	case "string":
		if f, _ := s["format"].(string); f == "date-time" {
			return dartType{"DateTime", func(v string) string { return "DateTime.parse(" + v + " as String)" }, func(v string) string { return v + ".toUtc().toIso8601String()" }}, nil
		}
		return cast("String"), nil
	case "integer":
		return dartType{"int", func(v string) string { return "(" + v + " as num).toInt()" }, id}, nil
	case "number":
		return dartType{"double", func(v string) string { return "(" + v + " as num).toDouble()" }, id}, nil
	case "boolean":
		return cast("bool"), nil
	case "array":
		items, _ := s["items"].(js)
		it, err := g.typeOf(items, name+"Item", false)
		if err != nil {
			return dartType{}, err
		}
		return dartType{
			"List<" + it.dart + ">",
			func(v string) string { return "(" + v + " as List).map((e) => " + it.fromJSON("e") + ").toList()" },
			func(v string) string { return v + ".map((e) => " + it.toJSON("e") + ").toList()" },
		}, nil
	case "object":
		props, _ := s["properties"].(js)
		if len(props) == 0 {
			return dartType{"Map<String, dynamic>", func(v string) string { return v + " as Map<String, dynamic>" }, id}, nil
		}
		if err := g.class(name, s); err != nil {
			return dartType{}, err
		}
		return dartType{name, func(v string) string { return name + ".fromJson(" + v + " as Map<String, dynamic>)" }, func(v string) string { return v + ".toJson()" }}, nil
	}
	return plain("Object?"), nil
}

func id(v string) string { return v }

func cast(t string) dartType {
	return dartType{t, func(v string) string { return v + " as " + t }, id}
}

func plain(t string) dartType {
	return dartType{t, func(v string) string { return v }, id}
}

type dartField struct {
	json, name, doc string
	t               dartType
	required        bool
}

type dartClass struct {
	name, doc string
	fields    []dartField
}

func (g *apiGen) class(name string, s js) error {
	if _, done := g.classes[name]; done {
		return nil
	}
	c := &dartClass{name: name}
	c.doc, _ = s["description"].(string)
	g.classes[name] = c
	g.order = append(g.order, name)
	req := map[string]bool{}
	if r, ok := s["required"].([]any); ok {
		for _, x := range r {
			if n, ok := x.(string); ok {
				req[n] = true
			}
		}
	}
	props, _ := s["properties"].(js)
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ps, _ := props[k].(js)
		t, err := g.typeOf(ps, name+upperFirst(camelJSON(k)), false)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", name, k, err)
		}
		doc, _ := ps["description"].(string)
		nullable, _ := ps["nullable"].(bool)
		c.fields = append(c.fields, dartField{json: k, name: dartName(camelJSON(k)), doc: doc, t: t, required: req[k] && !nullable && t.dart != "Object?"})
	}
	return nil
}

var dartReserved = map[string]bool{"default": true, "class": true, "in": true, "is": true, "new": true, "this": true, "switch": true, "case": true, "enum": true, "final": true, "var": true, "const": true, "true": true, "false": true, "null": true, "return": true, "void": true, "with": true, "if": true, "else": true, "for": true, "do": true, "while": true, "try": true, "catch": true, "super": true, "extends": true, "assert": true, "break": true, "continue": true, "rethrow": true, "throw": true}

func dartName(n string) string {
	if dartReserved[n] {
		return n + "_"
	}
	return n
}

func (g *apiGen) file(methods []string) string {
	var b strings.Builder
	b.WriteString(`// GENERATED by tools/mobilegen from api/openapi/openapi.yaml, for the
// operations mobile/api.txt names (docs/spec/mobile-app.md §4). Do not
// edit; change the API description or the list and run
// ` + "`go run ./tools/mobilegen`" + `.

// ignore_for_file: non_constant_identifier_names, unnecessary_this

import 'transport.dart';

/// Taskiem's API, as the app uses it.
class TaskiemApi {
  TaskiemApi(this._t);

  final Transport _t;

`)
	b.WriteString(strings.Join(methods, "\n"))
	b.WriteString("}\n")
	for _, n := range g.order {
		c := g.classes[n]
		b.WriteString("\n")
		if c.doc != "" {
			b.WriteString(docComment(c.doc, ""))
		}
		fmt.Fprintf(&b, "class %s {\n", c.name)
		if len(c.fields) == 0 {
			fmt.Fprintf(&b, "  const %s();\n\n  factory %s.fromJson(Map<String, dynamic> j) => const %s();\n\n  Map<String, dynamic> toJson() => {};\n}\n", c.name, c.name, c.name)
			continue
		}
		fmt.Fprintf(&b, "  const %s({", c.name)
		for i, f := range c.fields {
			if i > 0 {
				b.WriteString(", ")
			}
			if f.required {
				b.WriteString("required ")
			}
			b.WriteString("this." + f.name)
		}
		b.WriteString("});\n\n")
		for _, f := range c.fields {
			if f.doc != "" {
				b.WriteString(docComment(f.doc, "  "))
			}
			fmt.Fprintf(&b, "  final %s %s;\n", fieldType(f), f.name)
		}
		fmt.Fprintf(&b, "\n  factory %s.fromJson(Map<String, dynamic> j) => %s(\n", c.name, c.name)
		for _, f := range c.fields {
			v := "j['" + f.json + "']"
			if f.required || f.t.dart == "Object?" {
				fmt.Fprintf(&b, "        %s: %s,\n", f.name, f.t.fromJSON(v))
			} else {
				fmt.Fprintf(&b, "        %s: %s == null ? null : %s,\n", f.name, v, f.t.fromJSON(v))
			}
		}
		b.WriteString("      );\n\n  Map<String, dynamic> toJson() => {\n")
		for _, f := range c.fields {
			if f.required {
				fmt.Fprintf(&b, "        '%s': %s,\n", f.json, f.t.toJSON(f.name))
			} else if f.t.toJSON(f.name) == f.name {
				fmt.Fprintf(&b, "        '%s': ?%s,\n", f.json, f.name)
			} else {
				// Dart's null-aware entry: left out when null.
				fmt.Fprintf(&b, "        '%s': ?%s,\n", f.json, f.t.toJSON(f.name+"?"))
			}
		}
		b.WriteString("      };\n}\n")
	}
	return b.String()
}

func fieldType(f dartField) string {
	if f.required || strings.HasSuffix(f.t.dart, "?") {
		return f.t.dart
	}
	return f.t.dart + "?"
}

func docComment(s, indent string) string {
	var b strings.Builder
	line := indent + "///"
	for _, w := range strings.Fields(s) {
		// A word with angle brackets ("https://t.me/<bot>") is code to Dart's
		// docs, not HTML.
		if strings.ContainsAny(w, "<>") && !strings.Contains(w, "`") {
			w = "`" + w + "`"
		}
		if len(line)+1+len(w) > 80 && line != indent+"///" {
			b.WriteString(line + "\n")
			line = indent + "///"
		}
		line += " " + w
	}
	b.WriteString(line + "\n")
	return b.String()
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// camelJSON turns a JSON name (tenant_id) into a Dart one (tenantId).
func camelJSON(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '_' || r == '-' || r == '.' })
	for i := 1; i < len(parts); i++ {
		parts[i] = upperFirst(parts[i])
	}
	return strings.Join(parts, "")
}
