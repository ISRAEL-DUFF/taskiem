package main

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// decl is one custom property as the CSS declares it.
type decl struct{ name, value string }

var comments = regexp.MustCompile(`(?s)/\*.*?\*/`)

// rootBlocks answers the custom properties of a file's light `:root` blocks
// and its dark `:root[data-theme="dark"]` ones. The other blocks are left:
// `data-theme="system"` repeats the dark ones for a browser, and `data-brand`
// is the web's experiments with other brands.
func rootBlocks(css string) (light, dark []decl) {
	css = comments.ReplaceAllString(css, "")
	for i := 0; i < len(css); {
		open := strings.IndexByte(css[i:], '{')
		if open < 0 {
			break
		}
		open += i
		sel := strings.TrimSpace(css[lastBoundary(css, open):open])
		end := matching(css, open)
		body := css[open+1 : end]
		switch sel {
		case ":root":
			light = append(light, decls(body)...)
		case `:root[data-theme="dark"]`:
			dark = append(dark, decls(body)...)
		}
		if strings.HasPrefix(sel, "@") {
			// An at-rule's own blocks are read separately only when they are
			// the root's; a media query's dark root is the system's copy.
			i = end + 1
			continue
		}
		i = end + 1
	}
	return light, dark
}

// lastBoundary is where the selector before an opening brace starts.
func lastBoundary(s string, open int) int {
	for j := open - 1; j >= 0; j-- {
		if s[j] == '}' || s[j] == '{' || s[j] == ';' {
			return j + 1
		}
	}
	return 0
}

// matching is the closing brace of the block opened at open.
func matching(s string, open int) int {
	depth := 0
	for j := open; j < len(s); j++ {
		switch s[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return len(s) - 1
}

// decls reads the custom properties of a block's body (nested blocks left).
func decls(body string) []decl {
	var out []decl
	depth, start := 0, 0
	for j := 0; j <= len(body); j++ {
		if j < len(body) {
			switch body[j] {
			case '(':
				depth++
				continue
			case ')':
				depth--
				continue
			}
			if body[j] != ';' || depth != 0 {
				continue
			}
		}
		part := strings.TrimSpace(body[start:j])
		start = j + 1
		if !strings.HasPrefix(part, "--") {
			continue
		}
		k := strings.IndexByte(part, ':')
		if k < 0 {
			continue
		}
		out = append(out, decl{strings.TrimSpace(part[:k]), strings.Join(strings.Fields(part[k+1:]), " ")})
	}
	return out
}

// --- values ---

type rgba struct{ r, g, b, a float64 }

func (c rgba) argb() uint32 {
	ch := func(v float64) uint32 { return uint32(math.Round(math.Max(0, math.Min(255, v)))) }
	return ch(c.a*255)<<24 | ch(c.r)<<16 | ch(c.g)<<8 | ch(c.b)
}

type shadow struct {
	x, y, blur, spread float64
	c                  rgba
}

type kind int

const (
	kColor kind = iota
	kShadows
	kDuration
	kNumber
	kLength
	kCurve
	kSkip
)

// value is a token's value once its var()s are read.
type value struct {
	kind    kind
	color   rgba
	shadows []shadow
	ms      float64
	num     float64
	curve   [4]float64
}

type tokens struct {
	names []string
	light map[string]value
	dark  map[string]value
}

var varRef = regexp.MustCompile(`var\((--[a-z0-9-]+)\)`)

// resolve reads every token for light and for dark. Dark starts as light
// and takes the dark blocks' tokens over it, as the browser does.
func resolve(light, dark []decl) (tokens, error) {
	lraw := map[string]string{}
	var names []string
	for _, d := range light {
		if _, ok := lraw[d.name]; !ok {
			names = append(names, d.name)
		}
		lraw[d.name] = d.value
	}
	draw := map[string]string{}
	for k, v := range lraw {
		draw[k] = v
	}
	for _, d := range dark {
		if _, ok := lraw[d.name]; !ok {
			return tokens{}, fmt.Errorf("%s is dark only: give it a light value in :root", d.name)
		}
		draw[d.name] = d.value
	}
	t := tokens{names: names, light: map[string]value{}, dark: map[string]value{}}
	for _, theme := range []struct {
		raw map[string]string
		out map[string]value
	}{{lraw, t.light}, {draw, t.dark}} {
		for _, n := range names {
			v, err := parse(expand(theme.raw[n], theme.raw, 0))
			if err != nil {
				return tokens{}, fmt.Errorf("%s: %w", n, err)
			}
			theme.out[n] = v
		}
	}
	// Motion, sizes and numbers are the same in both themes: the app keeps
	// one of each. Say so if the web ever makes one differ.
	for _, n := range names {
		l, d := t.light[n], t.dark[n]
		if l.kind != d.kind {
			return tokens{}, fmt.Errorf("%s is a different kind of value in dark", n)
		}
		if (l.kind == kDuration || l.kind == kNumber || l.kind == kLength || l.kind == kCurve) && fmt.Sprint(l) != fmt.Sprint(d) {
			return tokens{}, fmt.Errorf("%s differs in dark: the app keeps motion and sizes for both themes", n)
		}
	}
	return t, nil
}

func expand(v string, raw map[string]string, depth int) string {
	if depth > 10 {
		return v
	}
	return varRef.ReplaceAllStringFunc(v, func(m string) string {
		name := varRef.FindStringSubmatch(m)[1]
		r, ok := raw[name]
		if !ok {
			return m
		}
		return expand(r, raw, depth+1)
	})
}

var (
	reMs     = regexp.MustCompile(`^(-?[0-9.]+)ms$`)
	rePx     = regexp.MustCompile(`^(-?[0-9.]+)px$`)
	reNum    = regexp.MustCompile(`^-?[0-9.]+$`)
	reCubic  = regexp.MustCompile(`^cubic-bezier\(([^)]*)\)$`)
	reMix    = regexp.MustCompile(`^color-mix\(in srgb, (.+?) ([0-9.]+)%, (.+)\)$`)
	reRGBA   = regexp.MustCompile(`^rgba?\(([^)]*)\)$`)
	reHex    = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)
	skipping = []string{"gradient(", "Inter", "url("}
)

func parse(v string) (value, error) {
	v = strings.TrimSpace(v)
	for _, s := range skipping {
		if strings.Contains(v, s) {
			return value{kind: kSkip}, nil
		}
	}
	switch {
	case v == "none":
		return value{kind: kShadows}, nil
	case reMs.MatchString(v):
		f, _ := strconv.ParseFloat(reMs.FindStringSubmatch(v)[1], 64)
		return value{kind: kDuration, ms: f}, nil
	case rePx.MatchString(v):
		f, _ := strconv.ParseFloat(rePx.FindStringSubmatch(v)[1], 64)
		return value{kind: kLength, num: f}, nil
	case reNum.MatchString(v):
		f, _ := strconv.ParseFloat(v, 64)
		return value{kind: kNumber, num: f}, nil
	case reCubic.MatchString(v):
		parts := strings.Split(reCubic.FindStringSubmatch(v)[1], ",")
		if len(parts) != 4 {
			return value{}, fmt.Errorf("a curve of %d numbers", len(parts))
		}
		var c [4]float64
		for i, p := range parts {
			f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
			if err != nil {
				return value{}, err
			}
			c[i] = f
		}
		return value{kind: kCurve, curve: c}, nil
	}
	if c, err := color(v); err == nil {
		return value{kind: kColor, color: c}, nil
	}
	if s, err := shadows(v); err == nil {
		return value{kind: kShadows, shadows: s}, nil
	}
	return value{}, fmt.Errorf("a value the app cannot take: %q", v)
}

func color(v string) (rgba, error) {
	v = strings.TrimSpace(v)
	switch {
	case v == "transparent":
		return rgba{}, nil
	case reHex.MatchString(v):
		h := v[1:]
		if len(h) == 3 {
			h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		}
		if len(h) == 6 {
			h += "ff"
		}
		n, _ := strconv.ParseUint(h, 16, 32)
		return rgba{float64(n >> 24 & 255), float64(n >> 16 & 255), float64(n >> 8 & 255), float64(n&255) / 255}, nil
	case reRGBA.MatchString(v):
		parts := strings.Split(reRGBA.FindStringSubmatch(v)[1], ",")
		if len(parts) < 3 {
			return rgba{}, fmt.Errorf("rgba of %d parts", len(parts))
		}
		f := make([]float64, 4)
		f[3] = 1
		for i, p := range parts {
			x, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
			if err != nil {
				return rgba{}, err
			}
			f[i] = x
		}
		return rgba{f[0], f[1], f[2], f[3]}, nil
	case reMix.MatchString(v):
		m := reMix.FindStringSubmatch(v)
		a, err := color(m[1])
		if err != nil {
			return rgba{}, err
		}
		p, _ := strconv.ParseFloat(m[2], 64)
		b, err := color(m[3])
		if err != nil {
			return rgba{}, err
		}
		return mix(a, b, p/100), nil
	}
	return rgba{}, fmt.Errorf("not a colour: %q", v)
}

// mix is CSS color-mix in srgb: premultiplied by alpha, as the browser does.
func mix(a, b rgba, p float64) rgba {
	alpha := a.a*p + b.a*(1-p)
	if alpha == 0 {
		return rgba{}
	}
	ch := func(x, y float64) float64 { return (x*a.a*p + y*b.a*(1-p)) / alpha }
	return rgba{ch(a.r, b.r), ch(a.g, b.g), ch(a.b, b.b), alpha}
}

// shadows reads a box-shadow list: "x y blur spread? colour", comma between.
func shadows(v string) ([]shadow, error) {
	var out []shadow
	for _, part := range splitTop(v) {
		f := strings.Fields(part)
		var nums []float64
		var col string
		for i, w := range f {
			if n, ok := length(w); ok {
				nums = append(nums, n)
				continue
			}
			col = strings.Join(f[i:], " ")
			break
		}
		if len(nums) < 2 || len(nums) > 4 || col == "" {
			return nil, fmt.Errorf("not a shadow: %q", part)
		}
		c, err := color(col)
		if err != nil {
			return nil, err
		}
		s := shadow{x: nums[0], y: nums[1], c: c}
		if len(nums) > 2 {
			s.blur = nums[2]
		}
		if len(nums) > 3 {
			s.spread = nums[3]
		}
		out = append(out, s)
	}
	return out, nil
}

func length(w string) (float64, bool) {
	if w == "0" {
		return 0, true
	}
	if m := rePx.FindStringSubmatch(w); m != nil {
		f, err := strconv.ParseFloat(m[1], 64)
		return f, err == nil
	}
	return 0, false
}

// splitTop splits on commas outside parentheses.
func splitTop(v string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range v {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(v[start:i]))
				start = i + 1
			}
		}
	}
	return append(out, strings.TrimSpace(v[start:]))
}

// --- Dart ---

func camel(name string) string {
	parts := strings.Split(strings.TrimPrefix(name, "--"), "-")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

func num(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

// flutterBlur is the blur radius Flutter needs to draw a CSS blur the same.
// CSS blurs with a Gaussian whose deviation is half the radius; Flutter's
// BoxShadow takes the deviation as 0.57735 × radius + 0.5. Rounded to a
// hundredth, so the file stays readable.
func flutterBlur(css float64) float64 {
	if css <= 1 {
		return css
	}
	return math.Round((css/2-0.5)/0.57735*100) / 100
}

func dartColor(c rgba) string { return fmt.Sprintf("Color(0x%08X)", c.argb()) }

func dartShadows(ss []shadow) string {
	if len(ss) == 0 {
		return "<BoxShadow>[]"
	}
	var b strings.Builder
	b.WriteString("<BoxShadow>[")
	for i, s := range ss {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "BoxShadow(offset: Offset(%s, %s), blurRadius: %s, spreadRadius: %s, color: %s)", num(s.x), num(s.y), num(flutterBlur(s.blur)), num(s.spread), dartColor(s.c))
	}
	b.WriteString("]")
	return b.String()
}

func dart(t tokens) string {
	var b strings.Builder
	b.WriteString(`// GENERATED by tools/mobilegen from web/src/styles.css, kit.css and
// motion.css: the web chat's own tokens (docs/spec/mobile-app.md §4.1).
// Do not edit; change the CSS and run ` + "`go run ./tools/mobilegen`" + `.

// ignore_for_file: constant_identifier_names

import 'package:flutter/animation.dart';
import 'package:flutter/painting.dart';

/// The colours and shadows of one theme, light or dark.
class TkTokens {
`)
	themed := func(k kind) bool { return k == kColor || k == kShadows }
	var fields []string
	for _, n := range t.names {
		if themed(t.light[n].kind) {
			fields = append(fields, n)
		}
	}
	for _, n := range fields {
		typ := "Color"
		if t.light[n].kind == kShadows {
			typ = "List<BoxShadow>"
		}
		fmt.Fprintf(&b, "  /// `%s`\n  final %s %s;\n", n, typ, camel(n))
	}
	b.WriteString("\n  const TkTokens({\n")
	for _, n := range fields {
		fmt.Fprintf(&b, "    required this.%s,\n", camel(n))
	}
	b.WriteString("  });\n}\n")
	for _, theme := range []struct {
		name string
		vals map[string]value
	}{{"tkLight", t.light}, {"tkDark", t.dark}} {
		fmt.Fprintf(&b, "\nconst %s = TkTokens(\n", theme.name)
		for _, n := range fields {
			v := theme.vals[n]
			if v.kind == kColor {
				fmt.Fprintf(&b, "  %s: %s,\n", camel(n), dartColor(v.color))
			} else {
				fmt.Fprintf(&b, "  %s: %s,\n", camel(n), dartShadows(v.shadows))
			}
		}
		b.WriteString(");\n")
	}
	b.WriteString("\n/// Motion, sizes and numbers: the same in both themes.\nabstract final class Tk {\n")
	var rest []string
	for _, n := range t.names {
		if k := t.light[n].kind; !themed(k) && k != kSkip {
			rest = append(rest, n)
		}
	}
	sort.SliceStable(rest, func(i, j int) bool { return t.light[rest[i]].kind < t.light[rest[j]].kind })
	for _, n := range rest {
		v := t.light[n]
		switch v.kind {
		case kDuration:
			fmt.Fprintf(&b, "  /// `%s`\n  static const %s = Duration(milliseconds: %d);\n", n, camel(n), int(math.Round(v.ms)))
		case kNumber, kLength:
			fmt.Fprintf(&b, "  /// `%s`\n  static const double %s = %s;\n", n, camel(n), num(v.num))
		case kCurve:
			fmt.Fprintf(&b, "  /// `%s`\n  static const %s = Cubic(%s, %s, %s, %s);\n", n, camel(n), num(v.curve[0]), num(v.curve[1]), num(v.curve[2]), num(v.curve[3]))
		}
	}
	b.WriteString("}\n")
	var skipped []string
	for _, n := range t.names {
		if t.light[n].kind == kSkip {
			skipped = append(skipped, n)
		}
	}
	if len(skipped) > 0 {
		b.WriteString("\n// Not taken (the app draws these itself): " + strings.Join(skipped, ", ") + ".\n")
	}
	return b.String()
}
