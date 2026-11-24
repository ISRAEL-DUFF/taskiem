package api

// A command typed in full counts toward "most used" (docs/spec/chat-menus.md
// §6.1): a row pressed, an action the model picked and a command the person
// typed are each a use, counted per person and business by day
// (chat_action_uses). Which action a typed command is comes from the
// catalogue itself: each action's command, as its Make writes it from its
// values, read back as a pattern ("invoice {who} {amount} for {for}" is
// "invoice … … for …"), and the shortcut of one with no values. The words
// of the other languages count by their command (the word lists'
// intents). A message nothing read as a command (the model's, the menus',
// the short answer) is not a typed command and is not counted here.

import (
	"context"
	"regexp"
	"strings"
	"sync"

	"github.com/israel-duff/taskiem/engine/lang"
)

// typedPattern is one way an action's command is typed.
type typedPattern struct {
	re *regexp.Regexp
	// literal is how much of the pattern is its own words: the more, the
	// surer ("order … to …" over "order …").
	literal int
}

var (
	typedOnce     sync.Once
	typedPatterns map[string][]typedPattern
)

// typedSentinel marks a value inside a made command.
const typedSentinel = "\x00"

// typedPatternsOf are the patterns of the catalogue's commands, by action
// id, made once. An action whose command has no words of its own (only
// values, as "@Tunde count the float" is) has none: it is counted where
// its command is read (staff_tasks.go).
func typedPatternsOf() map[string][]typedPattern {
	typedOnce.Do(func() {
		typedPatterns = map[string][]typedPattern{}
		for _, a := range chatActions {
			if a.Make == nil {
				if a.Shortcut != "" && len(a.Fields) == 0 && !strings.HasPrefix(a.Shortcut, "/") {
					cmd := normalizeCommand(a.Shortcut)
					typedPatterns[a.ID] = []typedPattern{{regexp.MustCompile(`^` + regexp.QuoteMeta(cmd) + `$`), len(cmd)}}
				}
				continue
			}
			// With every value, and with the optional ones left out.
			for _, withOptional := range []bool{true, false} {
				v := map[string]string{}
				for _, fl := range a.Fields {
					if fl.Optional && !withOptional {
						continue
					}
					v[fl.Key] = typedSentinel + fl.Key + typedSentinel
				}
				if p, ok := typedPatternOf(a, a.Make(v)); ok {
					typedPatterns[a.ID] = append(typedPatterns[a.ID], p)
				}
			}
		}
	})
	return typedPatterns
}

// typedPatternOf reads a command made with sentinels back as a pattern.
func typedPatternOf(a chatAction, made string) (typedPattern, bool) {
	parts := strings.Split(strings.ToLower(made), typedSentinel)
	var b strings.Builder
	literal := 0
	for i, part := range parts {
		if i%2 == 0 {
			b.WriteString(regexp.QuoteMeta(part))
			literal += len(strings.Trim(part, " @"))
			continue
		}
		var fl chatField
		for _, x := range a.Fields {
			if x.Key == part {
				fl = x
			}
		}
		if fl.Kind == "choice" && len(fl.Choices) > 0 {
			quoted := make([]string, len(fl.Choices))
			shortest := 0
			for j, ch := range fl.Choices {
				quoted[j] = regexp.QuoteMeta(strings.ToLower(ch))
				if shortest == 0 || len(ch) < shortest {
					shortest = len(ch)
				}
			}
			b.WriteString(`(?:` + strings.Join(quoted, "|") + `)`)
			literal += shortest
			continue
		}
		b.WriteString(`\S.*?`)
	}
	// Nothing of its own to know it by: not a pattern.
	if literal < 3 {
		return typedPattern{}, false
	}
	return typedPattern{regexp.MustCompile(`^` + b.String() + `$`), literal}, true
}

// typedIntentActions are the commands of the word lists in each language,
// as the action each does; arg is whether it takes words after it.
var typedIntentActions = map[lang.Intent]struct {
	id  string
	arg bool
}{
	lang.IntentInvoice: {"money.invoice", true}, lang.IntentExpense: {"money.spent", true},
	lang.IntentCashIn: {"money.cash", true}, lang.IntentCashOut: {"money.cash", true},
	lang.IntentSold: {"sales.sell", true}, lang.IntentCount: {"stock.count", false},
	lang.IntentTakeOff: {"stock.adjust", true}, lang.IntentReceived: {"buying.got", true},
	lang.IntentOrders: {"orders.open", false}, lang.IntentMyPage: {"page.see", false},
	lang.IntentOpenShop: {"page.shut", false}, lang.IntentCloseShop: {"page.shut", false},
	lang.IntentFronts: {"page.fronts", false}, lang.IntentReady: {"orders.move", true}, lang.IntentDelivered: {"orders.move", true},
	lang.IntentStatus: {"auto.status", false}, lang.IntentApprovals: {"staff.approvals", false},
	lang.IntentTables: {"lists.all", false}, lang.IntentFind: {"lists.find", true}, lang.IntentAdd: {"lists.add", true},
	lang.IntentNewTable: {"lists.new", false}, lang.IntentRun: {"auto.run", true}, lang.IntentBuild: {"auto.build", true},
	lang.IntentSwitch: {"account.switch", false}, lang.IntentLanguage: {"account.lang", false}, lang.IntentPlan: {"account.plan", false},
}

// typedActionID is the action a message typed in full is the command of:
// the catalogue's patterns in English, the surest first; else the word
// lists' command in the person's language. "" when it is none.
func typedActionID(text string, m lang.Match, matched bool) string {
	cmd := normalizeCommand(text)
	if cmd == "" || strings.HasPrefix(cmd, "/") {
		return ""
	}
	best, score := "", 0
	for _, a := range chatActions {
		for _, p := range typedPatternsOf()[a.ID] {
			if p.literal > score && p.re.MatchString(cmd) {
				best, score = a.ID, p.literal
			}
		}
	}
	if best != "" {
		return best
	}
	if matched && m.Language != lang.EN {
		arg := strings.TrimSpace(m.Arg) != ""
		if m.Intent == lang.IntentStock {
			if arg {
				return "stock.one"
			}
			return "stock.low"
		}
		if x, ok := typedIntentActions[m.Intent]; ok && (arg || !x.arg) {
			return x.id
		}
	}
	return ""
}

// typedUse is the action a message the person typed is the command of,
// when it is one they may use and nothing else has counted it: not a
// press, not the model's command, not read again inside this message.
func (s *Server) typedUse(ctx context.Context, c *chat, text string, m lang.Match, matched bool) (string, bool) {
	if c.typedSeen || c.understood || c.in.Reply != "" || c.p == nil {
		return "", false
	}
	c.typedSeen = true
	id := typedActionID(text, m, matched)
	a, ok := chatActionByID(id)
	if !ok || !s.mayAct(c, s.chatAreas(ctx, c), a) {
		return "", false
	}
	return id, true
}
