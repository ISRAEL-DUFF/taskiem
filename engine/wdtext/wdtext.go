// Package wdtext describes a workflow definition as plain numbered steps,
// for people who never see its JSON: the WhatsApp build command (spec
// 11.1: "shown back as plain steps to confirm") and the template gallery's
// preview. It reads names and descriptions where a definition has them
// and otherwise says what each step does from its type and its
// connector's manifest. It never prints expressions or values.
//
// The words around names come from engine/lang's wd.* messages, so the
// WhatsApp read-back follows the person's language (DescribeIn). Names,
// connector and action titles, and roles are the tenant's or the
// connector's own words and stay as written.
package wdtext

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/wd"
)

// Line is one step of the description. Depth is 0 for top-level steps and
// grows inside foreach bodies, branches and parallel branches; Number is
// the label shown ("3", "3a", "3a.i").
type Line struct {
	Number string `json:"number"`
	Depth  int    `json:"depth"`
	Text   string `json:"text"`
}

// Describe returns the trigger as line 1 and each step after it, nested
// steps indented under their parent, in English. reg may be nil
// (connector refs are then named as written).
func Describe(def *wd.Definition, reg connector.Lookup) []Line {
	return DescribeIn(def, reg, lang.EN)
}

// DescribeIn is Describe in a language.
func DescribeIn(def *wd.Definition, reg connector.Lookup, t lang.Tag) []Line {
	d := describer{reg: reg, lang: t}
	out := []Line{{Number: "1", Text: d.trigger(def.Trigger)}}
	d.steps(&out, def.Steps, 0, "", 2)
	return out
}

// Text renders lines as a numbered list, nested steps indented.
func Text(lines []Line) string {
	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strings.Repeat("   ", l.Depth))
		switch l.Number {
		case "":
		case "•":
			b.WriteString("• ")
		default:
			b.WriteString(l.Number)
			b.WriteString(". ")
		}
		b.WriteString(l.Text)
	}
	return b.String()
}

type describer struct {
	reg  connector.Lookup
	lang lang.Tag
}

// t is message id in the describer's language.
func (d describer) t(id string, kv ...string) string { return lang.Default().Text(d.lang, id, kv...) }

func (d describer) steps(out *[]Line, steps []*wd.Step, depth int, prefix string, first int) {
	for i, st := range steps {
		num := "•"
		if prefix != "•" {
			num = prefix + label(depth, first+i)
		}
		*out = append(*out, Line{Number: num, Depth: depth, Text: d.step(st)})
		switch st.Type {
		case "foreach":
			if st.Foreach != nil {
				d.steps(out, st.Foreach.Steps, depth+1, num, 0)
			}
		case "parallel":
			if st.Parallel != nil {
				for j, br := range st.Parallel.Branches {
					bn := num + label(depth+1, j)
					*out = append(*out, Line{Number: bn, Depth: depth + 1, Text: d.t("wd.alongside", "name", plain(br.Name))})
					d.steps(out, br.Steps, depth+2, bn, 0)
				}
			}
		case "branch":
			if st.Branch != nil {
				n := 0
				for _, p := range st.Branch.Paths {
					bn := num + label(depth+1, n)
					n++
					*out = append(*out, Line{Number: bn, Depth: depth + 1, Text: d.t("wd.if", "name", plain(p.Name))})
					d.steps(out, p.Steps, depth+2, bn, 0)
				}
				if st.Branch.Default != nil && len(st.Branch.Default.Steps) > 0 {
					bn := num + label(depth+1, n)
					*out = append(*out, Line{Number: bn, Depth: depth + 1, Text: d.t("wd.otherwise")})
					d.steps(out, st.Branch.Default.Steps, depth+2, bn, 0)
				}
			}
		}
		if st.OnError != nil && len(st.OnError.Steps) > 0 {
			*out = append(*out, Line{Depth: depth + 1, Text: d.t("wd.on_error")})
			d.steps(out, st.OnError.Steps, depth+2, "•", 0)
		}
	}
}

// label numbers a step at a depth: 1, 2, 3 at the top; a, b, c below; i,
// ii, iii below that.
func label(depth, i int) string {
	switch {
	case depth == 0:
		return strconv.Itoa(i)
	case depth%3 == 0:
		return "." + strconv.Itoa(i+1)
	case depth%3 == 1:
		if i < 26 {
			return "abcdefghijklmnopqrstuvwxyz"[i : i+1]
		}
		return strconv.Itoa(i + 1)
	default:
		return "." + roman(i+1)
	}
}

func roman(n int) string {
	vals := []struct {
		v int
		s string
	}{{10, "x"}, {9, "ix"}, {5, "v"}, {4, "iv"}, {1, "i"}}
	var b strings.Builder
	for _, x := range vals {
		for n >= x.v {
			b.WriteString(x.s)
			n -= x.v
		}
	}
	return b.String()
}

func (d describer) connectorName(ref string) string {
	if d.reg != nil {
		if c, ok := d.reg.Get(ref); ok && c.Manifest.Name != "" {
			return c.Manifest.Name
		}
	}
	id, _, _ := strings.Cut(ref, "@")
	return id
}

func (d describer) actionTitle(ref, action string) string {
	if d.reg != nil {
		if c, ok := d.reg.Get(ref); ok {
			if a, ok := c.Manifest.Actions[action]; ok && a.Title != "" {
				return a.Title
			}
		}
	}
	return strings.ReplaceAll(action, "_", " ")
}

func (d describer) trigger(t wd.Trigger) string {
	cfg := t.Config
	str := func(k string) string { s, _ := cfg[k].(string); return s }
	switch t.Type {
	case "schedule":
		return ScheduleIn(str("cron"), str("timezone"), d.lang)
	case "webhook":
		auth := str("auth")
		switch auth {
		case "none":
			return d.t("wd.trigger.webhook_unsigned")
		case "":
			return d.t("wd.trigger.webhook")
		}
		return d.t("wd.trigger.webhook_checked", "auth", auth)
	case "connector_event":
		name := d.connectorName(str("connector"))
		var evs []string
		if list, ok := cfg["events"].([]any); ok {
			for _, e := range list {
				if s, ok := e.(string); ok {
					evs = append(evs, s)
				}
			}
		}
		if len(evs) > 0 {
			return d.t("wd.trigger.event_reports", "connector", name, "events", strings.Join(evs, d.t("wd.or")))
		}
		return d.t("wd.trigger.event", "connector", name, "trigger", strings.ReplaceAll(str("trigger"), "_", " "))
	case "polling":
		return d.t("wd.trigger.polling", "interval", DurationIn(str("interval"), d.lang), "connector", d.connectorName(str("connector")),
			"action", d.actionTitle(str("connector"), str("action")))
	case "manual", "whatsapp", "ussd", "email", "database_change", "subflow":
		return d.t("wd.trigger." + t.Type)
	}
	return d.t("wd.trigger.other", "type", t.Type)
}

func (d describer) step(st *wd.Step) string {
	what := d.what(st)
	if st.Name != "" {
		text := plain(st.Name)
		if st.Type == "connector" {
			text += " (" + d.connectorName(st.Connector) + ")"
		}
		what = text
	}
	if st.When != "" && st.Type != "branch" {
		colon := strings.HasSuffix(what, ":")
		what = d.t("wd.only_when", "step", strings.TrimSuffix(what, ":"))
		if colon {
			what += ":"
		}
	}
	return what
}

func (d describer) what(st *wd.Step) string {
	switch st.Type {
	case "connector":
		return d.t("wd.connector", "action", d.actionTitle(st.Connector, st.Action), "connector", d.connectorName(st.Connector))
	case "approval":
		a := st.Approval
		if a == nil {
			return d.t("wd.approval")
		}
		var s string
		switch {
		case a.Policy != "":
			s = d.t("wd.approval.policy", "policy", plain(a.Policy))
		case a.Role != "":
			n := max(a.Count, 1)
			if n == 1 {
				s = d.t("wd.approval.role_one", "role", plain(a.Role))
			} else {
				s = d.t("wd.approval.role_many", "count", strconv.Itoa(n), "role", plain(a.Role))
			}
		default:
			s = d.t("wd.approval")
		}
		if a.Timeout != "" {
			s = d.t("wd.approval.timeout", "approval", s, "duration", DurationIn(a.Timeout, d.lang))
		}
		return s
	case "foreach":
		if f := st.Foreach; f != nil && f.MaxConcurrency > 0 {
			return d.t("wd.foreach_limited", "count", strconv.Itoa(f.MaxConcurrency))
		}
		return d.t("wd.foreach")
	case "parallel":
		return d.t("wd.parallel")
	case "branch":
		return d.t("wd.branch")
	case "wait":
		if w := st.Wait; w != nil && w.Duration != "" {
			return d.t("wd.wait_for", "duration", DurationIn(w.Duration, d.lang))
		}
		return d.t("wd.wait_until")
	case "signal":
		if s := st.Signal; s != nil {
			return d.t("wd.signal_named", "event", plain(s.Event))
		}
		return d.t("wd.signal")
	case "transform":
		return d.t("wd.transform")
	case "http":
		if h := st.HTTP; h != nil {
			if u, err := url.Parse(h.URL); err == nil && u.Host != "" && !strings.HasPrefix(h.URL, "=") {
				return d.t("wd.http_host", "host", u.Host, "method", h.Method)
			}
			return d.t("wd.http_service", "method", h.Method)
		}
		return d.t("wd.http")
	case "code":
		if c := st.Code; c != nil {
			return d.t("wd.code_language", "language", c.Language)
		}
		return d.t("wd.code")
	case "container":
		if c := st.Container; c != nil {
			repo, _, _ := strings.Cut(c.Image, "@")
			return d.t("wd.container_named", "image", plain(repo[strings.LastIndex(repo, "/")+1:]))
		}
		return d.t("wd.container")
	}
	return strings.ReplaceAll(st.Type, "_", " ")
}

// plain turns an identifier into words ("finance_manager" → "finance
// manager") and strips characters that format messages.
func plain(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	return strings.Map(func(r rune) rune {
		switch r {
		case '*', '`', '~':
			return -1
		case '\n', '\r', '\t':
			return ' '
		}
		return r
	}, s)
}

// Duration reads a wd/v1 duration ("90s", "15m", "24h", "7d") aloud.
func Duration(s string) string { return DurationIn(s, lang.EN) }

// DurationIn is Duration in a language.
func DurationIn(s string, t lang.Tag) string {
	if len(s) < 2 {
		return s
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return s
	}
	unit := map[byte]string{'s': "second", 'm': "minute", 'h': "hour", 'd': "day"}[s[len(s)-1]]
	if unit == "" {
		return s
	}
	if n == 1 {
		return lang.Default().Text(t, "wd.unit."+unit+"_one")
	}
	return lang.Default().Text(t, "wd.unit."+unit+"s", "count", strconv.Itoa(n))
}

var weekdays = []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}

// Schedule reads a five-field cron expression aloud for the common
// shapes and falls back to the expression itself.
func Schedule(cron, tz string) string { return ScheduleIn(cron, tz, lang.EN) }

// ScheduleIn is Schedule in a language.
func ScheduleIn(cron, tz string, t lang.Tag) string {
	tr := func(id string, kv ...string) string { return lang.Default().Text(t, id, kv...) }
	if tz == "" {
		tz = "Africa/Lagos"
	}
	f := strings.Fields(cron)
	if len(f) != 5 {
		return tr("wd.schedule.raw", "cron", cron, "tz", tz)
	}
	min, hour, dom, mon, dow := f[0], f[1], f[2], f[3], f[4]
	at := ""
	if h, err := strconv.Atoi(hour); err == nil {
		if m, err := strconv.Atoi(min); err == nil {
			at = fmt.Sprintf("%02d:%02d", h, m)
		}
	}
	switch {
	case strings.HasPrefix(min, "*/") && hour == "*" && dom == "*" && mon == "*" && dow == "*":
		return tr("wd.schedule.minutes", "count", strings.TrimPrefix(min, "*/"))
	case hour == "*" && dom == "*" && mon == "*" && dow == "*":
		if min == "0" {
			return tr("wd.schedule.hourly")
		}
		return tr("wd.schedule.hourly_at", "minute", min)
	case strings.HasPrefix(hour, "*/") && dom == "*" && mon == "*" && dow == "*":
		return tr("wd.schedule.hours", "count", strings.TrimPrefix(hour, "*/"))
	case at == "":
	case dom == "*" && mon == "*" && dow == "*":
		return tr("wd.schedule.daily", "time", at, "tz", tz)
	case dom == "*" && mon == "*" && (dow == "1-5" || dow == "MON-FRI"):
		return tr("wd.schedule.weekdays", "time", at, "tz", tz)
	case dom == "*" && mon == "*":
		if days := dayNames(dow, t); days != "" {
			return tr("wd.schedule.days", "days", days, "time", at, "tz", tz)
		}
	case mon == "*" && dow == "*":
		if dom == "L" {
			return tr("wd.schedule.last_day", "time", at, "tz", tz)
		}
		if n, err := strconv.Atoi(dom); err == nil {
			return tr("wd.schedule.monthly", "day", ordinal(n, t), "time", at, "tz", tz)
		}
	}
	return tr("wd.schedule.raw_local", "cron", cron, "tz", tz)
}

func dayNames(dow string, t lang.Tag) string {
	var names []string
	for _, p := range strings.Split(dow, ",") {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 7 {
			return ""
		}
		names = append(names, lang.Default().Text(t, "wd.day."+weekdays[n%7]))
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return lang.Default().Text(t, "wd.and", "list", strings.Join(names[:len(names)-1], ", "), "last", names[len(names)-1])
}

// ordinal is a day of the month: "25th" in English; the number alone in
// other languages, whose messages place it.
func ordinal(n int, t lang.Tag) string {
	if t != lang.EN {
		return strconv.Itoa(n)
	}
	suf := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suf = "st"
		case 2:
			suf = "nd"
		case 3:
			suf = "rd"
		}
	}
	return strconv.Itoa(n) + suf
}
