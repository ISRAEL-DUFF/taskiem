// Package wdtext describes a workflow definition as plain numbered steps,
// for people who never see its JSON: the WhatsApp build command (spec
// 11.1: "shown back as plain steps to confirm") and the template gallery's
// preview. It reads names and descriptions where a definition has them
// and otherwise says what each step does from its type and its
// connector's manifest. It never prints expressions or values.
package wdtext

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
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
// steps indented under their parent. reg may be nil (connector refs are
// then named as written).
func Describe(def *wd.Definition, reg connector.Lookup) []Line {
	d := describer{reg: reg}
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

type describer struct{ reg connector.Lookup }

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
					*out = append(*out, Line{Number: bn, Depth: depth + 1, Text: "Alongside: " + plain(br.Name)})
					d.steps(out, br.Steps, depth+2, bn, 0)
				}
			}
		case "branch":
			if st.Branch != nil {
				n := 0
				for _, p := range st.Branch.Paths {
					bn := num + label(depth+1, n)
					n++
					*out = append(*out, Line{Number: bn, Depth: depth + 1, Text: "If " + plain(p.Name) + ":"})
					d.steps(out, p.Steps, depth+2, bn, 0)
				}
				if st.Branch.Default != nil && len(st.Branch.Default.Steps) > 0 {
					bn := num + label(depth+1, n)
					*out = append(*out, Line{Number: bn, Depth: depth + 1, Text: "Otherwise:"})
					d.steps(out, st.Branch.Default.Steps, depth+2, bn, 0)
				}
			}
		}
		if st.OnError != nil && len(st.OnError.Steps) > 0 {
			*out = append(*out, Line{Depth: depth + 1, Text: "If that step fails:"})
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
		return Schedule(str("cron"), str("timezone"))
	case "webhook":
		auth := str("auth")
		switch auth {
		case "none":
			return "When your system calls this workflow's web address (no signature check)"
		case "":
			return "When your system calls this workflow's web address"
		}
		return "When your system calls this workflow's web address (checked with " + auth + ")"
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
			return "When " + name + " reports " + strings.Join(evs, " or ")
		}
		return "When " + name + " sends an event (" + strings.ReplaceAll(str("trigger"), "_", " ") + ")"
	case "polling":
		return "Every " + Duration(str("interval")) + ", check " + d.connectorName(str("connector")) + " (" + d.actionTitle(str("connector"), str("action")) + ") for new items"
	case "manual":
		return "When someone starts it"
	case "whatsapp":
		return "When a message arrives on WhatsApp"
	case "ussd":
		return "When someone dials the USSD code"
	case "email":
		return "When an email arrives"
	case "database_change":
		return "When a database row changes"
	case "subflow":
		return "When another workflow calls it"
	}
	return "When it is triggered (" + t.Type + ")"
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
		what = strings.TrimSuffix(what, ":") + ", only when its condition holds"
		if colon {
			what += ":"
		}
	}
	return what
}

func (d describer) what(st *wd.Step) string {
	switch st.Type {
	case "connector":
		return d.actionTitle(st.Connector, st.Action) + " with " + d.connectorName(st.Connector)
	case "approval":
		a := st.Approval
		if a == nil {
			return "Wait for approval"
		}
		var s string
		switch {
		case a.Policy != "":
			s = "Get approval under the " + plain(a.Policy) + " policy"
		case a.Role != "":
			n := max(a.Count, 1)
			if n == 1 {
				s = "Ask a " + plain(a.Role) + " to approve"
			} else {
				s = fmt.Sprintf("Ask %d people with the %s role to approve", n, plain(a.Role))
			}
		default:
			s = "Wait for approval"
		}
		if a.Timeout != "" {
			s += " (waits up to " + Duration(a.Timeout) + ")"
		}
		return s
	case "foreach":
		s := "For each item in the list"
		if f := st.Foreach; f != nil && f.MaxConcurrency > 0 {
			s += fmt.Sprintf(" (at most %d at a time)", f.MaxConcurrency)
		}
		return s + ":"
	case "parallel":
		return "Do these at the same time:"
	case "branch":
		return "Decide what to do:"
	case "wait":
		if w := st.Wait; w != nil && w.Duration != "" {
			return "Wait " + Duration(w.Duration)
		}
		return "Wait until the set time"
	case "signal":
		if s := st.Signal; s != nil {
			return "Wait for " + plain(s.Event)
		}
		return "Wait for a signal"
	case "transform":
		return "Prepare the data for the next steps"
	case "http":
		if h := st.HTTP; h != nil {
			where := "an external service"
			if u, err := url.Parse(h.URL); err == nil && u.Host != "" && !strings.HasPrefix(h.URL, "=") {
				where = u.Host
			}
			return "Call " + where + " (" + h.Method + ")"
		}
		return "Call an external service"
	case "code":
		if c := st.Code; c != nil {
			return "Run custom " + c.Language + " code"
		}
		return "Run custom code"
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
func Duration(s string) string {
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
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}

var weekdays = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

// Schedule reads a five-field cron expression aloud for the common
// shapes and falls back to the expression itself.
func Schedule(cron, tz string) string {
	if tz == "" {
		tz = "Africa/Lagos"
	}
	f := strings.Fields(cron)
	if len(f) != 5 {
		return "On the schedule " + cron + " (" + tz + ")"
	}
	min, hour, dom, mon, dow := f[0], f[1], f[2], f[3], f[4]
	at := ""
	if h, err := strconv.Atoi(hour); err == nil {
		if m, err := strconv.Atoi(min); err == nil {
			at = fmt.Sprintf(" at %02d:%02d", h, m)
		}
	}
	suffix := " (" + tz + " time)"
	switch {
	case strings.HasPrefix(min, "*/") && hour == "*" && dom == "*" && mon == "*" && dow == "*":
		return "Every " + strings.TrimPrefix(min, "*/") + " minutes"
	case hour == "*" && dom == "*" && mon == "*" && dow == "*":
		if min == "0" {
			return "Every hour, on the hour"
		}
		return "Every hour at " + min + " minutes past"
	case strings.HasPrefix(hour, "*/") && dom == "*" && mon == "*" && dow == "*":
		return "Every " + strings.TrimPrefix(hour, "*/") + " hours"
	case at == "":
	case dom == "*" && mon == "*" && dow == "*":
		return "Every day" + at + suffix
	case dom == "*" && mon == "*" && (dow == "1-5" || dow == "MON-FRI"):
		return "Every weekday (Monday to Friday)" + at + suffix
	case dom == "*" && mon == "*":
		if days := dayNames(dow); days != "" {
			return "Every " + days + at + suffix
		}
	case mon == "*" && dow == "*":
		if dom == "L" {
			return "On the last day of every month" + at + suffix
		}
		if n, err := strconv.Atoi(dom); err == nil {
			return "On the " + ordinal(n) + " of every month" + at + suffix
		}
	}
	return "On the schedule " + cron + suffix
}

func dayNames(dow string) string {
	var names []string
	for _, p := range strings.Split(dow, ",") {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 7 {
			return ""
		}
		names = append(names, weekdays[n%7])
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

func ordinal(n int) string {
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
