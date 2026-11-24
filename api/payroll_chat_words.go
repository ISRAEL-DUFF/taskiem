package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/connectors/payrolla"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// The chat's words for staff and for this month's changes (the leftovers
// of P4b-2 and P4b-3 in docs/spec/chat-pay-people.md), in the Taskiem chat
// on the web and in the app, on WhatsApp, Telegram and Slack:
//
//   - staff (payroll.prepare, P4b-2's calls): "add musa to payroll" gives
//     the Add someone form (a bank account is typed only in a form, where
//     the bank checks it), from the staff member when there is one;
//     "raise zainab's pay to 500k from january", "move zainab to sales"
//     and "end musa's employment" (or "remove musa from payroll") ask yes
//     first, saying what changes; "change zainab's salary account" gives
//     the form; "change my salary account" gives a linked employee My pay;
//   - this month's payroll (payroll.prepare, P4b-3's calls): "bonus 100k
//     to zainab for q4 target", "deduct 20k from musa, advance repaid",
//     "leave out ada this month, unpaid leave" and "put ada back" ask yes,
//     with that person's take-home now (read from Payrolla), then say it
//     after the change; a missing reason is asked for;
//   - account changes: "account changes" lists what waits (any payroll
//     permission); "approve zainab's account change" (payroll.approve)
//     asks Payrolla for a code and takes it typed in the WhatsApp or
//     Telegram thread, as a payroll's approval does (payroll_code_waits,
//     action bank_change: the change's id and the employee's name, never
//     the code); the only approver may approve their own (decision 0074,
//     settled 5), with no passkey of Taskiem's; "reject zainab's account
//     change, <why>" needs no code.
//
// Names are matched against Payrolla's (the team, or the payroll's lines);
// two people of one name are asked about, never guessed. Nothing of pay or
// an account number is kept: the session holds Payrolla's ids, the name,
// the amount said and the reason until yes.

// actionBankChange is a code waiting to approve a held account change.
const actionBankChange = "bank_change"

const (
	pyName   = `([\p{L}][\p{L}'’.\- ]*?)`
	pyAmount = `(₦?\s?[0-9][0-9,]*(?:\.[0-9]{1,2})?(?:\s?(?:k|m|naira)\b)?)`
	pyReason = `(?:(?:\s*[,:;-]\s*|\s+(?:for|because|as|reason:?)\s+)(.*?))?`
)

var (
	payrollAddWords = regexp.MustCompile(`(?i)^\s*(?:please\s+)?add\s+` + pyName + `\s+(?:to|on|onto|into)\s+(?:the\s+)?payroll\s*[.!]?\s*$`)
	payrollEndWords = regexp.MustCompile(`(?i)^\s*(?:end\s+` + pyName + `(?:'s|’s)?\s+employment|remove\s+` + pyName + `\s+from\s+(?:the\s+)?payroll|take\s+` + pyName +
		`\s+off\s+(?:the\s+)?payroll)\s*[.!]?\s*$`)
	payrollPayWords = regexp.MustCompile(`(?i)^\s*(?:raise|increase|change|reduce|cut|set|make)\s+` + pyName + `(?:'s|’s)\s+(?:monthly\s+)?(?:pay|salary)\s+to\s+` + pyAmount +
		`(?:\s*(?:a|per)\s+month)?(?:\s+(?:from|starting(?:\s+from)?|effective)\s+(.+?))?\s*[.!]?\s*$`)
	payrollMoveWords = regexp.MustCompile(`(?i)^\s*move\s+` + pyName + `\s+to\s+(?:the\s+)?(.+?)(?:\s+department)?\s*[.!]?\s*$`)
	payrollBankWords = regexp.MustCompile(`(?i)^\s*change\s+` + pyName + `(?:'s|’s)\s+(?:salary|bank|pay)\s+account\s*[.!]?\s*$`)
	payrollMineWords = regexp.MustCompile(`(?i)^\s*(?:change|update)\s+my\s+(?:salary|bank|pay)\s+account\s*[.!]?\s*$`)

	payrollBonusWords = regexp.MustCompile(`(?i)^\s*(?:add\s+(?:a\s+)?)?bonus\s+(?:of\s+)?` + pyAmount + `\s+(?:to|for)\s+` + pyName + pyReason + `\s*[.!]?\s*$`)
	payrollGiveWords  = regexp.MustCompile(`(?i)^\s*give\s+` + pyName + `\s+(?:a\s+)?` + pyAmount + `\s+bonus` + pyReason + `\s*[.!]?\s*$`)
	payrollDeductWord = regexp.MustCompile(`(?i)^\s*deduct\s+` + pyAmount + `\s+from\s+` + pyName + `(?:'s|’s)?(?:\s+pay)?` + pyReason + `\s*[.!]?\s*$`)
	payrollLeaveWords = regexp.MustCompile(`(?i)^\s*leave\s+(?:out\s+` + pyName + `|` + pyName + `\s+out)(?:\s+of)?(?:\s+(?:this\s+month(?:'s|’s)?(?:\s+payroll)?|(?:the\s+)?payroll))?` +
		pyReason + `\s*[.!]?\s*$`)
	payrollBackWords = regexp.MustCompile(`(?i)^\s*put\s+` + pyName + `\s+back(?:\s+(?:in|on|into)\s+(?:this\s+month(?:'s|’s)?\s+|the\s+)?payroll)?\s*[.!]?\s*$`)

	payrollChangesWords = regexp.MustCompile(`(?i)^\s*(?:show\s+(?:me\s+)?)?(?:the\s+)?(?:salary\s+|bank\s+)?account\s+changes(?:\s+waiting)?\s*[.?!]?\s*$`)
	payrollApproveCh    = regexp.MustCompile(`(?i)^\s*approve\s+(?:` + pyName + `(?:'s|’s)\s+|the\s+)?(?:new\s+)?(?:salary\s+|bank\s+)?account(?:\s+change)?\s*[.!]?\s*$`)
	payrollRejectCh     = regexp.MustCompile(`(?i)^\s*reject\s+(?:` + pyName + `(?:'s|’s)\s+|the\s+)?(?:new\s+)?(?:salary\s+|bank\s+)?account(?:\s+change)?\b\s*[,:;.-]?\s*(.*?)\s*$`)
)

// chatPayroll is a change to the payroll said in the chat, waiting for
// yes (or, Need set, for its reason): Payrolla's ids, the name, what was
// said, and the form's own id for the Idempotency-Key.
type chatPayroll struct {
	// Do is pay, department, end, bonus, deduction, leave_out or put_back.
	Do         string `json:"do"`
	Employee   string `json:"employee"`
	Name       string `json:"name"`
	Run        string `json:"run,omitempty"`
	Period     string `json:"period,omitempty"`
	Kobo       int64  `json:"kobo,omitempty"`
	From       string `json:"from,omitempty"`
	Department string `json:"department,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Exclusion  string `json:"exclusion,omitempty"`
	// Net is their take-home before the change, as Payrolla said it.
	Net int64 `json:"net,omitempty"`
	// Basic, Housing, Transport and Pensionable are kept as they are on a
	// change of pay (said so before yes).
	Basic       int64  `json:"basic,omitempty"`
	Housing     int64  `json:"housing,omitempty"`
	Transport   int64  `json:"transport,omitempty"`
	Pensionable *bool  `json:"pensionable,omitempty"`
	Need        string `json:"need,omitempty"` // reason
	Key         string `json:"key"`
}

// payrollAmount reads an amount as said: "100k", "₦100,000", "1.5m",
// "250000"; ok false for anything else or more than ₦100,000,000.
func payrollAmount(s string) (int64, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSpace(strings.TrimPrefix(s, "₦"))
	s = strings.TrimSpace(strings.TrimSuffix(s, "naira"))
	mult := 100.0
	switch {
	case strings.HasSuffix(s, "k"):
		mult, s = 100_000, strings.TrimSpace(strings.TrimSuffix(s, "k"))
	case strings.HasSuffix(s, "m"):
		mult, s = 100_000_000, strings.TrimSpace(strings.TrimSuffix(s, "m"))
	}
	s = strings.ReplaceAll(s, ",", "")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	k := int64(v*mult + 0.5)
	if k < 100 || k > payMax {
		return 0, false
	}
	return k, true
}

// payFromDate reads when new pay starts: "january", "1 january", "next
// month", "today"; "" is today. ok is false for anything else.
func payFromDate(now time.Time, s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(strings.Trim(s, ".!")))
	s = strings.TrimSpace(strings.TrimPrefix(s, "the "))
	switch s {
	case "", "today", "now":
		return "", true
	}
	day := 1
	if f := strings.Fields(s); len(f) >= 2 {
		if d, err := strconv.Atoi(strings.TrimRight(f[0], "stndrh")); err == nil && d >= 1 && d <= 31 {
			day, s = d, strings.Join(f[1:], " ")
		}
	}
	if strings.HasPrefix(s, "next month") {
		s = "next"
	}
	f := strings.Fields(s)
	if len(f) == 0 || len(f) > 2 {
		return "", false
	}
	year := ""
	if len(f) == 2 {
		year = f[1]
	}
	period, ok := payrollMonth(now, f[0], year)
	if !ok {
		return "", false
	}
	t, err := time.Parse("2006-01-02", fmt.Sprintf("%s-%02d", period, day))
	if err != nil || t.Format("02") != fmt.Sprintf("%02d", day) {
		return "", false
	}
	return t.Format("2006-01-02"), true
}

// someone is a person a name may mean.
type someone struct{ ID, First, Last string }

func (p someone) name() string { return strings.TrimSpace(p.First + " " + p.Last) }

// matchName finds who a name said means: the full name first, then the
// first name, the last name, or the start of the full name.
func matchName(said string, people []someone) []someone {
	said = strings.ToLower(strings.Join(strings.Fields(strings.TrimSuffix(strings.TrimSuffix(said, "'s"), "’s")), " "))
	if said == "" {
		return nil
	}
	for _, test := range []func(p someone) bool{
		func(p someone) bool { return strings.ToLower(p.name()) == said },
		func(p someone) bool {
			return strings.ToLower(p.First) == said || strings.ToLower(p.Last) == said
		},
		func(p someone) bool {
			return strings.HasPrefix(strings.ToLower(p.name()), said+" ") || len(said) >= 3 && strings.HasPrefix(strings.ToLower(p.name()), said)
		},
	} {
		var got []someone
		for _, p := range people {
			if test(p) && !slices.ContainsFunc(got, func(x someone) bool { return x.ID == p.ID }) {
				got = append(got, p)
			}
		}
		if len(got) > 0 {
			return got
		}
	}
	return nil
}

func namesOf(ps []someone) string {
	n := make([]string, len(ps))
	for i, p := range ps {
		n[i] = p.name()
	}
	return whatsapp.SafeText(strings.Join(n, ", "))
}

// groupOf is the first of the groups a match filled.
func groupOf(m []string, idx ...int) string {
	for _, i := range idx {
		if i < len(m) && strings.TrimSpace(m[i]) != "" {
			return strings.TrimSpace(m[i])
		}
	}
	return ""
}

func (s *Server) payrollURL(path string) string { return strings.TrimRight(s.PublicURL, "/") + path }

// waPayrollWords answers the staff, change and account-change words, and
// a change waiting for yes or its reason; false when the message is none
// of them, so it passes on.
func (s *Server) waPayrollWords(ctx context.Context, c *chat, text, cmd string) (bool, error) {
	if c.data.Payroll != nil && c.state != stateIdle {
		// Anything but an answer lets the change go and is read anew.
		if done, err := s.payrollWaitingReply(ctx, c, text, cmd); done || err != nil {
			return done, err
		}
	}
	if c.state != stateIdle || c.in.Reply != "" {
		return false, nil
	}
	if payrollMineWords.MatchString(text) {
		return true, s.payrollMyAccount(ctx, c)
	}
	// Words that can only be about payroll are answered to anyone, a
	// refusal included; the others only to those who see the payroll.
	if m := payrollAddWords.FindStringSubmatch(text); m != nil {
		return true, s.payrollAddChat(ctx, c, m[1])
	}
	if m := payrollEndWords.FindStringSubmatch(text); m != nil {
		return true, s.payrollStaffAsk(ctx, c, chatPayroll{Do: "end"}, groupOf(m, 1, 2, 3))
	}
	if !c.p.Can(PermPayrollSee) {
		return false, nil
	}
	switch {
	case payrollChangesWords.MatchString(text):
		return true, s.payrollChangesChat(ctx, c)
	}
	if m := payrollApproveCh.FindStringSubmatch(text); m != nil {
		return true, s.payrollChangeAskCode(ctx, c, m[1], "")
	}
	if m := payrollRejectCh.FindStringSubmatch(text); m != nil {
		return true, s.payrollChangeReject(ctx, c, m[1], m[2])
	}
	if m := payrollPayWords.FindStringSubmatch(text); m != nil {
		kobo, ok := payrollAmount(m[2])
		if !ok {
			return true, c.say(ctx, s, c.t("payroll.words.amount"))
		}
		from, ok := payFromDate(time.Now().In(lagos()), m[3])
		if !ok {
			return true, c.say(ctx, s, c.t("payroll.staff.from_bad"))
		}
		return true, s.payrollStaffAsk(ctx, c, chatPayroll{Do: "pay", Kobo: kobo, From: from}, m[1])
	}
	if m := payrollBankWords.FindStringSubmatch(text); m != nil {
		return true, s.payrollStaffAsk(ctx, c, chatPayroll{Do: "bank"}, m[1])
	}
	if m := payrollBonusWords.FindStringSubmatch(text); m != nil {
		return true, s.payrollRunAsk(ctx, c, "bonus", m[2], m[1], m[3])
	}
	if m := payrollGiveWords.FindStringSubmatch(text); m != nil {
		return true, s.payrollRunAsk(ctx, c, "bonus", m[1], m[2], m[3])
	}
	if m := payrollDeductWord.FindStringSubmatch(text); m != nil {
		return true, s.payrollRunAsk(ctx, c, "deduction", m[2], m[1], m[3])
	}
	if m := payrollLeaveWords.FindStringSubmatch(text); m != nil {
		return true, s.payrollRunAsk(ctx, c, "leave_out", groupOf(m, 1, 2), "", m[3])
	}
	if m := payrollBackWords.FindStringSubmatch(text); m != nil {
		return true, s.payrollRunAsk(ctx, c, "put_back", m[1], "", "")
	}
	// "move zainab to sales" only when Zainab is in Payrolla: the words
	// are too common to claim otherwise.
	if m := payrollMoveWords.FindStringSubmatch(text); m != nil && c.p.Can(PermPayrollPrepare) {
		return s.payrollMoveChat(ctx, c, m[1], m[2])
	}
	return false, nil
}

// payrollWaitingReply answers while a change waits: yes does it, no or
// stop drops it; a reason it waits for is taken; anything else lets it go
// and is read as a new message.
func (s *Server) payrollWaitingReply(ctx context.Context, c *chat, text, cmd string) (bool, error) {
	cp := *c.data.Payroll
	switch {
	case cmd == "no" || cmd == "cancel" || cmd == "stop":
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return true, err
		}
		return true, c.say(ctx, s, c.t("payroll.words.nothing"))
	case cp.Need == "reason" && c.in.Reply == "":
		reason := strings.TrimSpace(text)
		if reason == "" || utf8.RuneCountInString(reason) > 200 {
			return true, c.say(ctx, s, c.t("payroll.words.reason_short"))
		}
		cp.Need, cp.Reason = "", reason
		return true, s.payrollConfirm(ctx, c, cp)
	case cmd == "yes" && cp.Need == "":
		if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
			return true, err
		}
		return true, s.payrollChangeDo(ctx, c, cp)
	}
	if err := s.saveSession(ctx, c, stateIdle, chatData{}, 0); err != nil {
		return true, err
	}
	return false, nil
}

// payrollConfirm keeps a change and asks yes, saying what it does.
func (s *Server) payrollConfirm(ctx context.Context, c *chat, cp chatPayroll) error {
	if cp.Key == "" {
		cp.Key = uuid.NewString()
	}
	name := whatsapp.SafeText(cp.Name)
	var words string
	switch cp.Do {
	case "pay":
		date := payDateWords(cp.From)
		if cp.From == "" {
			date = c.t("payroll.staff.today")
		}
		if cp.Net > 0 {
			words = c.t("payroll.staff.ask_pay", "name", name, "before", invoicing.Naira(cp.Net), "amount", invoicing.Naira(cp.Kobo), "date", date)
		} else {
			words = c.t("payroll.staff.ask_pay_new", "name", name, "amount", invoicing.Naira(cp.Kobo), "date", date)
		}
		if cp.Basic+cp.Housing+cp.Transport > 0 {
			words += " " + c.t("payroll.staff.parts_kept")
		}
	case "department":
		words = c.t("payroll.staff.ask_move", "name", name, "department", whatsapp.SafeText(cp.Department))
	case "end":
		words = c.t("payroll.staff.ask_end", "name", name)
	case "bonus":
		words = c.t("payroll.words.ask_bonus", "amount", invoicing.Naira(cp.Kobo), "name", name, "period", cp.Period, "reason", whatsapp.SafeText(cp.Reason),
			"net", invoicing.Naira(cp.Net))
	case "deduction":
		words = c.t("payroll.words.ask_deduction", "amount", invoicing.Naira(cp.Kobo), "name", name, "period", cp.Period, "reason", whatsapp.SafeText(cp.Reason),
			"net", invoicing.Naira(cp.Net))
	case "leave_out":
		words = c.t("payroll.words.ask_leave_out", "name", name, "period", cp.Period, "reason", whatsapp.SafeText(cp.Reason), "net", invoicing.Naira(cp.Net))
	case "put_back":
		words = c.t("payroll.words.ask_put_back", "name", name, "period", cp.Period)
	}
	if err := s.saveSession(ctx, c, stateConfirming, chatData{Payroll: &cp}, waPendingTTL); err != nil {
		return err
	}
	return c.say(ctx, s, words, whatsapp.Button{ID: "yes", Title: c.t("button.yes")}, whatsapp.Button{ID: "no", Title: c.t("button.no")})
}

// payrollAskReason waits for why (a bonus, a deduction, leaving out).
func (s *Server) payrollAskReason(ctx context.Context, c *chat, cp chatPayroll) error {
	cp.Need, cp.Key = "reason", uuid.NewString()
	if err := s.saveSession(ctx, c, stateCollecting, chatData{Payroll: &cp}, waPendingTTL); err != nil {
		return err
	}
	if cp.Do == "leave_out" {
		return c.say(ctx, s, c.t("payroll.words.leave_reason", "name", whatsapp.SafeText(cp.Name)))
	}
	return c.say(ctx, s, c.t("payroll.words.reason", "name", whatsapp.SafeText(cp.Name)))
}

// --- staff ---

// payrollTeamPeople are the company's employees, as someone, for a name.
func (s *Server) payrollTeamPeople(ctx context.Context, cl *payrolla.Client) ([]payrolla.Employee, []someone, error) {
	emps, err := cl.AllEmployees(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	return emps, peopleOf(emps), nil
}

// peopleOf are employees as someone, for a name.
func peopleOf(emps []payrolla.Employee) []someone {
	people := make([]someone, 0, len(emps))
	for _, e := range emps {
		people = append(people, someone{ID: e.ID, First: e.FirstName, Last: e.LastName})
	}
	return people
}

// payrollConnFor is the chat person's Payrolla client and connection, or
// the words that say why there is none (said already).
func (s *Server) payrollConnFor(ctx context.Context, c *chat) (*payrolla.Client, *payrollaConn, bool, error) {
	cl, conn, err := s.payrollaClient(ctx, c.p, "payroll.chat")
	if err != nil {
		var pr payRefused
		if e := s.payrollaSays(ctx, c.p, conn, err); errors.As(e, &pr) {
			return nil, conn, false, c.say(ctx, s, pr.msg)
		}
		return nil, conn, false, err
	}
	return cl, conn, true, nil
}

// sayTeamRefused says a staff call's refusal in the chat.
func (s *Server) sayTeamRefused(ctx context.Context, c *chat, conn *payrollaConn, err error) error {
	_, err = s.sayRefused(ctx, c, s.teamSays(ctx, c.p, conn, err, "employees"))
	return err
}

// payrollAddChat answers "add musa to payroll": the Add someone form,
// from the staff member when there is one (a bank account is typed only
// in a form, where the bank checks it).
func (s *Server) payrollAddChat(ctx context.Context, c *chat, said string) error {
	if !c.p.Can(PermPayrollPrepare) {
		return c.say(ctx, s, c.t("payroll.not_allowed"))
	}
	cl, conn, ok, err := s.payrollConnFor(ctx, c)
	if !ok {
		return err
	}
	emps, people, err := s.payrollTeamPeople(ctx, cl)
	if err != nil {
		return s.sayTeamRefused(ctx, c, conn, err)
	}
	for _, p := range matchName(said, people) {
		for _, e := range emps {
			if e.ID == p.ID && e.Status != "TERMINATED" {
				return c.say(ctx, s, c.t("payroll.staff.already", "name", whatsapp.SafeText(e.Name()), "url", s.payrollURL("/payroll/team")))
			}
		}
	}
	links, err := s.payrollaLinks(ctx, c.tenant.ID)
	if err != nil {
		return err
	}
	linked := map[uuid.UUID]bool{}
	for _, l := range links {
		linked[l.UserID] = true
	}
	var staff []someone
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		ms, err := staffPeopleTx(ctx, tx, c.p, uuid.Nil, func(n string) string { return n })
		for _, m := range ms {
			if !linked[m.UserID] {
				first, last, _ := strings.Cut(strings.TrimSpace(m.Name), " ")
				staff = append(staff, someone{ID: m.UserID.String(), First: first, Last: last})
			}
		}
		return err
	}); err != nil {
		return err
	}
	got := matchName(said, staff)
	switch len(got) {
	case 0:
		return c.say(ctx, s, c.t("payroll.staff.add_new", "name", whatsapp.SafeText(said), "url", s.payrollURL("/payroll/team?add=new")))
	case 1:
		return c.say(ctx, s, c.t("payroll.staff.add_form", "name", whatsapp.SafeText(got[0].name()), "url", s.payrollURL("/payroll/team?add="+got[0].ID)))
	}
	return c.say(ctx, s, c.t("payroll.words.which", "names", namesOf(got)))
}

// payrollStaffAsk finds who a staff change is for and asks yes: a change
// of pay, ending their employment; a new account gives the form.
func (s *Server) payrollStaffAsk(ctx context.Context, c *chat, cp chatPayroll, said string) error {
	if !c.p.Can(PermPayrollPrepare) {
		return c.say(ctx, s, c.t("payroll.not_allowed"))
	}
	cl, conn, ok, err := s.payrollConnFor(ctx, c)
	if !ok {
		return err
	}
	emps, people, err := s.payrollTeamPeople(ctx, cl)
	if err != nil {
		return s.sayTeamRefused(ctx, c, conn, err)
	}
	got := matchName(said, people)
	if len(got) > 1 {
		// Those still employed first.
		var still []someone
		for _, p := range got {
			for _, e := range emps {
				if e.ID == p.ID && e.Status != "TERMINATED" {
					still = append(still, p)
				}
			}
		}
		if len(still) > 0 {
			got = still
		}
	}
	switch len(got) {
	case 0:
		return c.say(ctx, s, c.t("payroll.words.not_found", "name", whatsapp.SafeText(said)))
	case 1:
	default:
		return c.say(ctx, s, c.t("payroll.words.which", "names", namesOf(got)))
	}
	var e payrolla.Employee
	for _, x := range emps {
		if x.ID == got[0].ID {
			e = x
		}
	}
	cp.Employee, cp.Name = e.ID, e.Name()
	if e.Status == "TERMINATED" {
		return c.say(ctx, s, c.t("payroll.staff.left", "name", whatsapp.SafeText(e.Name())))
	}
	switch cp.Do {
	case "bank":
		return c.say(ctx, s, c.t("payroll.staff.bank_form", "name", whatsapp.SafeText(e.Name()), "url", s.payrollURL("/payroll/team")))
	case "pay":
		if e.Pay != nil {
			cp.Net = e.Pay.GrossKobo
			cp.Basic, cp.Housing, cp.Transport = e.Pay.BasicKobo, e.Pay.HousingKobo, e.Pay.TransportKobo
			pens := e.Pay.Pensionable
			cp.Pensionable = &pens
			if cp.Basic+cp.Housing+cp.Transport > cp.Kobo {
				// The parts no longer fit: the form, not a guess.
				return c.say(ctx, s, c.t("payroll.staff.pay_form", "name", whatsapp.SafeText(e.Name()), "amount", invoicing.Naira(cp.Kobo), "url", s.payrollURL("/payroll/team")))
			}
		}
	}
	return s.payrollConfirm(ctx, c, cp)
}

// payrollMoveChat answers "move zainab to sales" when Zainab is someone
// in Payrolla; false otherwise, so the words pass on.
func (s *Server) payrollMoveChat(ctx context.Context, c *chat, said, dept string) (bool, error) {
	cl, _, err := s.payrollaClient(ctx, c.p, "payroll.chat")
	if err != nil {
		return false, nil //nolint:nilerr // no Payrolla: not payroll's words
	}
	// Names kept a few minutes: "move the generator to the shop" need
	// not list Payrolla's employees each time.
	emps, people, err := s.payrollNamesOf(ctx, c.tenant.ID, cl)
	if err != nil {
		return false, nil //nolint:nilerr // not payroll's words then
	}
	got := matchName(said, people)
	if len(got) == 0 {
		return false, nil
	}
	if len(got) > 1 {
		return true, c.say(ctx, s, c.t("payroll.words.which", "names", namesOf(got)))
	}
	dept = strings.TrimSpace(dept)
	if dept == "" || utf8.RuneCountInString(dept) > 80 {
		return false, nil
	}
	if r, size := utf8.DecodeRuneInString(dept); size > 0 {
		dept = strings.ToUpper(string(r)) + dept[size:]
	}
	for _, e := range emps {
		if e.ID == got[0].ID {
			if e.Status == "TERMINATED" {
				return true, c.say(ctx, s, c.t("payroll.staff.left", "name", whatsapp.SafeText(e.Name())))
			}
			return true, s.payrollConfirm(ctx, c, chatPayroll{Do: "department", Employee: e.ID, Name: e.Name(), Department: dept})
		}
	}
	return false, nil
}

// payrollMyAccount answers "change my salary account": My pay, where a
// linked employee types their new account and Payrolla's code.
func (s *Server) payrollMyAccount(ctx context.Context, c *chat) error {
	emp, err := s.myEmployee(ctx, c.p)
	if err != nil {
		return err
	}
	if emp == "" {
		return c.say(ctx, s, c.t("payroll.payslip.not_linked"))
	}
	return c.say(ctx, s, c.t("payroll.staff.my_account", "url", s.payrollURL("/payroll/me")))
}

// --- this month's changes ---

// openRun is the newest payroll that may still change (prepared, or
// worked out and not yet approved).
func openRun(ctx context.Context, cl *payrolla.Client) (*payrolla.Run, error) {
	runs, err := cl.Runs(ctx, "", 10)
	if err != nil {
		return nil, err
	}
	for _, r := range runs {
		if openStatus(r.Status) {
			return &r, nil
		}
	}
	return nil, nil
}

// payrollRunAsk finds the payroll and the person a change is for, and
// asks yes with their take-home now (or asks why, first).
func (s *Server) payrollRunAsk(ctx context.Context, c *chat, do, said, amount, reason string) error {
	if !c.p.Can(PermPayrollPrepare) {
		return c.say(ctx, s, c.t("payroll.not_allowed"))
	}
	cp := chatPayroll{Do: do, Reason: strings.TrimSpace(reason)}
	if do == "bonus" || do == "deduction" {
		k, ok := payrollAmount(amount)
		if !ok {
			return c.say(ctx, s, c.t("payroll.words.amount"))
		}
		cp.Kobo = k
	}
	if utf8.RuneCountInString(cp.Reason) > 200 {
		return c.say(ctx, s, c.t("payroll.words.reason_short"))
	}
	cl, ok, err := s.payrollClientFor(ctx, c)
	if !ok {
		return err
	}
	r, err := openRun(ctx, cl)
	if err != nil {
		_, err = s.sayRefused(ctx, c, s.runSays(ctx, c.lang.Tag, c.p, nil, cl, "", err))
		return err
	}
	if r == nil {
		return c.say(ctx, s, c.t("payroll.words.no_open", "month", strings.ToLower(monthNames[time.Now().In(lagos()).Month()-1])))
	}
	cp.Run, cp.Period = r.ID, periodWords(r.PeriodStart, r.PeriodEnd)
	lines, err := cl.Lines(ctx, r.ID)
	if err != nil {
		_, err = s.sayRefused(ctx, c, s.runSays(ctx, c.lang.Tag, c.p, nil, cl, r.ID, err))
		return err
	}
	if do == "put_back" {
		ex, err := cl.Exclusions(ctx, r.ID)
		if err != nil {
			_, err = s.sayRefused(ctx, c, s.runSays(ctx, c.lang.Tag, c.p, nil, cl, r.ID, err))
			return err
		}
		var out []someone
		byEmp := map[string]string{}
		for _, x := range ex {
			n, err := cl.RunEmployeeName(ctx, x.EmployeeID)
			if err != nil {
				// Never a name matched against nothing: Payrolla's refusal.
				_, err = s.sayRefused(ctx, c, s.runSays(ctx, c.lang.Tag, c.p, nil, cl, r.ID, err))
				return err
			}
			first, last, _ := strings.Cut(n, " ")
			out = append(out, someone{ID: x.EmployeeID, First: first, Last: last})
			byEmp[x.EmployeeID] = x.ID
		}
		got := matchName(said, out)
		switch len(got) {
		case 0:
			return c.say(ctx, s, c.t("payroll.words.not_left_out", "name", whatsapp.SafeText(said), "period", cp.Period))
		case 1:
			cp.Employee, cp.Name, cp.Exclusion = got[0].ID, got[0].name(), byEmp[got[0].ID]
			return s.payrollConfirm(ctx, c, cp)
		}
		return c.say(ctx, s, c.t("payroll.words.which", "names", namesOf(got)))
	}
	people := make([]someone, 0, len(lines))
	for _, l := range lines {
		people = append(people, someone{ID: l.EmployeeID, First: l.FirstName, Last: l.LastName})
	}
	got := matchName(said, people)
	switch len(got) {
	case 0:
		return c.say(ctx, s, c.t("payroll.words.not_found_run", "name", whatsapp.SafeText(said), "period", cp.Period))
	case 1:
	default:
		return c.say(ctx, s, c.t("payroll.words.which", "names", namesOf(got)))
	}
	cp.Employee, cp.Name = got[0].ID, got[0].name()
	for _, l := range lines {
		if l.EmployeeID == cp.Employee {
			cp.Net = l.NetKobo
		}
	}
	if cp.Reason == "" {
		return s.payrollAskReason(ctx, c, cp)
	}
	return s.payrollConfirm(ctx, c, cp)
}

// --- yes ---

// payrollChangeDo makes a change said and confirmed, through P4b-2's and
// P4b-3's calls, and says what it did.
func (s *Server) payrollChangeDo(ctx context.Context, c *chat, cp chatPayroll) error {
	if !c.p.Can(PermPayrollPrepare) {
		return c.say(ctx, s, c.t("payroll.not_allowed"))
	}
	name := whatsapp.SafeText(cp.Name)
	switch cp.Do {
	case "pay", "department", "end":
		return s.payrollStaffDo(ctx, c, cp, name)
	}
	route := map[string]string{"bonus": "run-adjust:", "deduction": "run-adjust:", "leave_out": "run-exclude:",
		"put_back": "run-exclusions-remove:"}[cp.Do] + cp.Run
	if cp.Do == "put_back" {
		route += ":" + cp.Exclusion
	}
	cl, conn, wr, err := s.runWrite(ctx, c.p, route, "chat:"+cp.Key)
	if err != nil {
		if errors.Is(err, errPayrollaNotConnected) {
			_, err = s.sayRefused(ctx, c, s.payrollaSays(ctx, c.p, conn, err))
		}
		return err
	}
	var ch payrolla.Changed
	var action string
	detail := map[string]any{"employee_id": cp.Employee, "via": c.ch.Name()}
	switch cp.Do {
	case "bonus", "deduction":
		ch, err = cl.AddAdjustment(ctx, cp.Run, cp.Employee, strings.ToUpper(cp.Do), cp.Kobo, cp.Reason, wr)
		action, detail["type"] = "payroll.run_adjust", cp.Do
	case "leave_out":
		ch, err = cl.Exclude(ctx, cp.Run, cp.Employee, cp.Reason, wr)
		action = "payroll.run_exclude"
	case "put_back":
		ch, err = cl.PutBack(ctx, cp.Run, cp.Exclusion, wr)
		action = "payroll.run_put_back"
	}
	if err != nil {
		_, err = s.sayRefused(ctx, c, s.runSays(ctx, c.lang.Tag, c.p, conn, cl, cp.Run, err))
		return err
	}
	s.payrollChatAudit(ctx, c, action, cp.Run, detail)
	if ch.ComputeError != nil {
		return c.say(ctx, s, c.t("payroll.refused.compute_error", "reason", strings.TrimRight(strings.TrimSpace(ch.ComputeError.Message), ".")))
	}
	lines := ch.Lines
	if len(lines) == 0 {
		lines, _ = cl.Lines(ctx, cp.Run)
	}
	var net int64 = -1
	for _, l := range lines {
		if l.EmployeeID == cp.Employee {
			net = l.NetKobo
		}
	}
	switch {
	case cp.Do == "leave_out":
		return c.say(ctx, s, c.t("payroll.words.done_leave_out", "name", name, "period", cp.Period, "people", peopleWords(c.lang.Tag, ch.Run.EmployeeCount),
			"amount", invoicing.Naira(ch.Run.TotalNetKobo)))
	case net < 0:
		return c.say(ctx, s, c.t("payroll.words.done"))
	case cp.Do == "put_back":
		return c.say(ctx, s, c.t("payroll.words.done_put_back", "name", name, "period", cp.Period, "net", invoicing.Naira(net)))
	}
	return c.say(ctx, s, c.t("payroll.words.done_adjust", "name", name, "net", invoicing.Naira(net), "before", invoicing.Naira(cp.Net)))
}

// payrollStaffDo changes someone's pay or department, or ends their
// employment, as Team does.
func (s *Server) payrollStaffDo(ctx context.Context, c *chat, cp chatPayroll, name string) error {
	cl, conn, err := s.payrollaClient(ctx, c.p, "payroll.chat")
	if err != nil {
		return s.sayTeamRefused(ctx, c, conn, err)
	}
	route := "employee-change:" + cp.Employee
	if cp.Do == "end" {
		route = "employee-end:" + cp.Employee
	}
	key, err := payrollaKey(conn, route, "chat:"+cp.Key)
	if err != nil {
		return err
	}
	wr := payrolla.Write{Key: key, User: c.p.UserID, Name: s.actingName(ctx, c.p)}
	var words, action string
	var detail map[string]any
	switch cp.Do {
	case "end":
		_, err = cl.EndEmployment(ctx, cp.Employee, wr)
		action = "payroll.employee_end"
		words = c.t("payroll.staff.done_end", "name", name)
	case "department":
		d := cp.Department
		_, err = cl.ChangeEmployee(ctx, cp.Employee, payrolla.EmployeeChange{Department: &d}, wr)
		action, detail = "payroll.employee_change", map[string]any{"fields": []string{"department"}}
		words = c.t("payroll.staff.done_move", "name", name, "department", whatsapp.SafeText(d))
	case "pay":
		pay := &payrolla.PayInput{GrossKobo: cp.Kobo, BasicKobo: cp.Basic, HousingKobo: cp.Housing, TransportKobo: cp.Transport, Pensionable: cp.Pensionable, EffectiveFrom: cp.From}
		_, err = cl.ChangeEmployee(ctx, cp.Employee, payrolla.EmployeeChange{Pay: pay}, wr)
		from := cp.From
		if from == "" {
			from = time.Now().In(lagos()).Format("2006-01-02")
		}
		action, detail = "payroll.employee_change", map[string]any{"fields": []string{"pay"}}
		words = c.t("payroll.staff.done_pay", "name", name, "date", payDateWords(from))
	}
	if err != nil {
		return s.sayTeamRefused(ctx, c, conn, err)
	}
	s.forgetPayrollNames(c.tenant.ID)
	s.teamChatAudit(ctx, c, action, "payrolla_employee:"+cp.Employee, detail)
	return c.say(ctx, s, words)
}

// teamChatAudit records a chat's change to the team or an account change
// (no pay, no account number, never a code).
func (s *Server) teamChatAudit(ctx context.Context, c *chat, action, target string, detail map[string]any) {
	r, err := chatRequest(ctx, c)
	if err != nil {
		return
	}
	if detail == nil {
		detail = map[string]any{}
	}
	detail["via"] = c.ch.Name()
	if err := s.tx(r, func(tx pgx.Tx) error { return auditTx(r, tx, action, target, detail) }); err != nil {
		s.Logger.Warn("payroll: auditing", "action", action, "err", err)
	}
}

// --- account changes ---

// pendingChanges are the account changes waiting, with each employee's
// name, as someone for a name.
func (s *Server) pendingChanges(ctx context.Context, cl *payrolla.Client) ([]payrolla.BankChange, map[string]string, []someone, error) {
	list, err := cl.BankChanges(ctx)
	if err != nil || len(list) == 0 {
		return nil, nil, nil, err
	}
	names, people, err := changeNames(ctx, cl, list)
	if err != nil {
		return nil, nil, nil, err
	}
	return list, names, people, nil
}

// changeNames are the names of everyone (by employee id), and those whose
// account changes wait as someone: from Payrolla's employee list, and one
// by one for anyone the list did not reach.
func changeNames(ctx context.Context, cl *payrolla.Client, list []payrolla.BankChange) (map[string]string, []someone, error) {
	emps, err := cl.AllEmployees(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	names := map[string]string{}
	for _, e := range emps {
		names[e.ID] = e.Name()
	}
	for _, c := range list {
		if _, ok := names[c.EmployeeID]; ok {
			continue
		}
		e, err := cl.Employee(ctx, c.EmployeeID)
		if payrolla.IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		names[e.ID] = e.Name()
		emps = append(emps, e)
	}
	var people []someone
	for _, e := range emps {
		if slices.ContainsFunc(list, func(c payrolla.BankChange) bool { return c.EmployeeID == e.ID }) && !slices.ContainsFunc(people, func(p someone) bool { return p.ID == e.ID }) {
			people = append(people, someone{ID: e.ID, First: e.FirstName, Last: e.LastName})
		}
	}
	return names, people, nil
}

// changeSays says an account change's refusal in words, in l: the code's
// as a payroll's are, and the change's own.
func (s *Server) changeSays(ctx context.Context, l lang.Tag, p *Principal, conn *payrollaConn, err error) error {
	switch payrolla.Code(err) {
	case "SAME_PERSON":
		return runRefused{status: http.StatusForbidden, code: "same_person", msg: tr(l, "payroll.change.same_person")}
	case "INVALID_STATE":
		return runRefused{status: http.StatusConflict, code: "decided", msg: tr(l, "payroll.change.decided")}
	case "NOT_FOUND":
		return runRefused{status: http.StatusNotFound, code: "not_found", msg: tr(l, "payroll.change.gone")}
	case "CODE_INVALID", "CODE_EXPIRED", "CHALLENGE_NOT_FOUND", "CODE_LOCKED", "CODE_USED", "TOO_MANY_CODES", "APPROVER_NOT_LINKED", "NOT_AN_APPROVER",
		"PAYMENT_CONFIRMATION_NOT_SET_UP":
		return s.runSays(ctx, l, p, conn, nil, "", err)
	}
	if errors.Is(err, errBadRequest) {
		return err
	}
	return s.teamSays(ctx, p, conn, err, "approve")
}

// changeLine is an account change in words: "ZAINAB A. BELLO, Access Bank ••2222".
func changeLine(c payrolla.BankChange) string {
	return whatsapp.SafeText(c.AccountName) + ", " + bankName(c.BankCode) + " ••" + c.Last4
}

// payrollChangesChat answers "account changes": what waits, who asked
// and how; Approve buttons in a thread that takes codes.
func (s *Server) payrollChangesChat(ctx context.Context, c *chat) error {
	cl, conn, ok, err := s.payrollConnFor(ctx, c)
	if !ok {
		return err
	}
	list, names, _, err := s.pendingChanges(ctx, cl)
	if err != nil {
		_, err = s.sayRefused(ctx, c, s.changeSays(ctx, c.lang.Tag, c.p, conn, err))
		return err
	}
	if len(list) == 0 {
		return c.say(ctx, s, c.t("payroll.change.none"))
	}
	ids := make([]string, len(list))
	for i, x := range list {
		ids[i] = x.ID
	}
	asked, err := s.askedBy(ctx, c.tenant.ID, ids)
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(c.t("payroll.change.title"))
	var buttons []whatsapp.Button
	approve := c.p.Can(PermPayrollApprove) && codeThread(c)
	var titles []string
	if approve {
		shown := make([]string, 0, 3)
		for _, x := range list[:min(3, len(list))] {
			shown = append(shown, names[x.EmployeeID])
		}
		titles = approveTitles(shown, func(name string) string { return c.t("payroll.button.approve_who", "name", name) })
	}
	for _, x := range list {
		var who string
		switch {
		case x.RequestedVia == "EMPLOYEE":
			who = c.t("payroll.change.asked_employee")
		case asked[x.ID].Name != "":
			who = c.t("payroll.change.asked_taskiem", "who", whatsapp.SafeText(asked[x.ID].Name))
		case x.RequestedVia == "CONNECT":
			who = c.t("payroll.change.asked_taskiem_someone")
		default:
			who = c.t("payroll.change.asked_payrolla")
		}
		b.WriteString("\n" + c.t("payroll.change.line", "name", whatsapp.SafeText(names[x.EmployeeID]), "account", changeLine(x), "asked", who))
		if approve && len(buttons) < len(titles) {
			buttons = append(buttons, whatsapp.Button{ID: payrollPrefix + "b:" + x.ID, Title: titles[len(buttons)]})
		}
	}
	if c.p.Can(PermPayrollApprove) {
		first, _, _ := strings.Cut(names[list[0].EmployeeID], " ")
		b.WriteString("\n\n" + c.t("payroll.change.how", "name", whatsapp.SafeText(strings.ToLower(first))))
	}
	b.WriteString("\n" + c.t("payroll.change.open", "url", s.payrollURL("/payroll/account-changes")))
	return c.say(ctx, s, b.String(), buttons...)
}

// changeFor finds the waiting change a name means (the only one, when no
// name is said); words, when there is no one change, are said already.
func (s *Server) changeFor(ctx context.Context, c *chat, cl *payrolla.Client, conn *payrollaConn, said string) (*payrolla.BankChange, string, error) {
	list, names, people, err := s.pendingChanges(ctx, cl)
	if err != nil {
		_, err = s.sayRefused(ctx, c, s.changeSays(ctx, c.lang.Tag, c.p, conn, err))
		return nil, "", err
	}
	if len(list) == 0 {
		return nil, "", c.say(ctx, s, c.t("payroll.change.none"))
	}
	if strings.TrimSpace(said) == "" {
		if len(list) == 1 {
			return &list[0], names[list[0].EmployeeID], nil
		}
		return nil, "", c.say(ctx, s, c.t("payroll.change.which", "names", namesOf(people)))
	}
	got := matchName(said, people)
	switch len(got) {
	case 0:
		return nil, "", c.say(ctx, s, c.t("payroll.change.none_for", "name", whatsapp.SafeText(said)))
	case 1:
	default:
		return nil, "", c.say(ctx, s, c.t("payroll.change.which", "names", namesOf(got)))
	}
	for i := range list {
		if list[i].EmployeeID == got[0].ID {
			return &list[i], names[got[0].ID], nil
		}
	}
	return nil, "", c.say(ctx, s, c.t("payroll.change.none"))
}

// payrollChangeAskCode asks Payrolla for a code to approve a held
// account change and waits for it in this thread. id, when given, is the
// change (a button); otherwise said names whose. The web, the app and
// Slack get Account changes, whose sheet takes the code.
func (s *Server) payrollChangeAskCode(ctx context.Context, c *chat, said, id string) error {
	if !c.p.Can(PermPayrollApprove) {
		return c.say(ctx, s, c.t("payroll.not_allowed"))
	}
	if !codeThread(c) {
		return c.say(ctx, s, c.t("payroll.change.open_page", "url", s.payrollURL("/payroll/account-changes")))
	}
	cl, conn, ok, err := s.payrollConnFor(ctx, c)
	if !ok {
		return err
	}
	var ch *payrolla.BankChange
	var name string
	if id != "" {
		list, names, _, err := s.pendingChanges(ctx, cl)
		if err != nil {
			_, err = s.sayRefused(ctx, c, s.changeSays(ctx, c.lang.Tag, c.p, conn, err))
			return err
		}
		for i := range list {
			if list[i].ID == id {
				ch, name = &list[i], names[list[i].EmployeeID]
			}
		}
		if ch == nil {
			return c.say(ctx, s, c.t("payroll.change.decided"))
		}
	} else if ch, name, err = s.changeFor(ctx, c, cl, conn, said); ch == nil {
		return err
	}
	key, err := payrollaKey(conn, "bank-change-code:"+ch.ID, "chat:"+uuid.NewString())
	if err != nil {
		return err
	}
	got, err := cl.StartBankChangeApproval(ctx, ch.ID, payrolla.Write{Key: key, User: c.p.UserID, Name: s.actingName(ctx, c.p)})
	if err != nil {
		_, err = s.sayRefused(ctx, c, s.changeSays(ctx, c.lang.Tag, c.p, conn, err))
		return err
	}
	until := time.Now().Add(codeWaitTTL)
	if t, err := time.Parse(time.RFC3339, got.ExpiresAt); err == nil && t.Before(until) {
		until = t
	}
	if err := s.setPayrollWait(ctx, c, payrollWait{Challenge: got.ID, Action: actionBankChange, Run: ch.ID, Period: cutRunes(name, 40), Expires: until}); err != nil {
		return err
	}
	s.teamChatAudit(ctx, c, "payroll.bank_change_code", bankChangeTarget(ch.ID), map[string]any{"channel": got.Channel})
	words := codeWhere(c.lang.Tag, got) + " " + c.t("payroll.change.type", "name", whatsapp.SafeText(name), "account", changeLine(*ch))
	if got.Channel == "SMS" {
		words += "\n\n" + c.t("payroll.code.safer")
	}
	return c.say(ctx, s, words)
}

// payrollChangeCodeTyped sends a code typed in the thread to approve a
// held account change, once. The only approver approving their own is
// said so; Taskiem asks no passkey of its own (decision 0074).
func (s *Server) payrollChangeCodeTyped(ctx context.Context, c *chat, w *payrollWait, code string) error {
	cl, conn, err := s.payrollaClient(ctx, c.p, "payroll.chat")
	if err == nil {
		var key string
		// Each attempt at a code gets its own key.
		if key, err = payrollaKey(conn, "bank-change-approve:"+w.Run+":"+w.Challenge, "chat:"+uuid.NewString()); err != nil {
			return err
		}
		_, err = cl.ApproveBankChange(ctx, w.Run, w.Challenge, code, payrolla.Write{Key: key, User: c.p.UserID, Name: s.actingName(ctx, c.p)})
	}
	if err != nil {
		if errors.Is(err, errPayrollaNotConnected) {
			_, err = s.sayRefused(ctx, c, s.payrollaSays(ctx, c.p, conn, err))
			return err
		}
		return s.payrollCodeRefused(ctx, c, w, s.changeSays(ctx, c.lang.Tag, c.p, conn, err))
	}
	if err := s.dropPayrollWait(ctx, c); err != nil {
		return err
	}
	asked, _ := s.askedBy(ctx, c.tenant.ID, []string{w.Run})
	own := asked[w.Run].UserID == c.p.UserID
	s.teamChatAudit(ctx, c, "payroll.bank_change_approve", bankChangeTarget(w.Run), map[string]any{"confirmed_with_code": true, "own_request": own})
	if own {
		return c.say(ctx, s, c.t("payroll.change.approved_own", "name", whatsapp.SafeText(w.Period)))
	}
	return c.say(ctx, s, c.t("payroll.change.approved", "name", whatsapp.SafeText(w.Period)))
}

// payrollCodeRefused says a refused code: a wrong one asks again (the code keeps
// waiting); an expired, locked or used one drops it and offers a new one;
// anything else drops it.
func (s *Server) payrollCodeRefused(ctx context.Context, c *chat, w *payrollWait, err error) error {
	var rr runRefused
	if errors.As(err, &rr) {
		switch rr.code {
		case "code_invalid":
			return c.say(ctx, s, rr.msg+" "+c.t("payroll.code.again"))
		case "code_expired", "code_locked", "code_used":
			if derr := s.dropPayrollWait(ctx, c); derr != nil {
				return derr
			}
			b := map[string]string{payrolla.ActionLock: "l", payrolla.ActionPayNow: "p", payrolla.ActionRetryFailed: "f", actionBankChange: "b"}[w.Action]
			if b == "" {
				b = "a"
			}
			return c.say(ctx, s, rr.msg, whatsapp.Button{ID: payrollPrefix + b + ":" + w.Run, Title: c.t("payroll.button.new_code")})
		}
	}
	if derr := s.dropPayrollWait(ctx, c); derr != nil {
		return derr
	}
	_, err = s.sayRefused(ctx, c, err)
	return err
}

// payrollChangeReject turns a held account change down, with a note and
// no code (it moves no money).
func (s *Server) payrollChangeReject(ctx context.Context, c *chat, said, note string) error {
	if !c.p.Can(PermPayrollApprove) {
		return c.say(ctx, s, c.t("payroll.not_allowed"))
	}
	note = strings.TrimSpace(note)
	if note == "" || utf8.RuneCountInString(note) > 300 {
		whose := "the"
		if said = strings.ToLower(strings.TrimSpace(said)); said != "" {
			whose = said + "'s"
		}
		return c.say(ctx, s, c.t("payroll.change.reject_how", "whose", whatsapp.SafeText(whose)))
	}
	cl, conn, ok, err := s.payrollConnFor(ctx, c)
	if !ok {
		return err
	}
	ch, name, err := s.changeFor(ctx, c, cl, conn, said)
	if ch == nil {
		return err
	}
	key, err := payrollaKey(conn, "bank-change-reject:"+ch.ID, "chat:"+uuid.NewString())
	if err != nil {
		return err
	}
	if _, err := cl.RejectBankChange(ctx, ch.ID, note, payrolla.Write{Key: key, User: c.p.UserID, Name: s.actingName(ctx, c.p)}); err != nil {
		_, err = s.sayRefused(ctx, c, s.changeSays(ctx, c.lang.Tag, c.p, conn, err))
		return err
	}
	s.teamChatAudit(ctx, c, "payroll.bank_change_reject", bankChangeTarget(ch.ID), nil)
	return c.say(ctx, s, c.t("payroll.change.rejected", "name", whatsapp.SafeText(name)))
}

// buttonTitleMax is the most a WhatsApp reply button's title holds.
const buttonTitleMax = 20

// approveTitles are the Approve buttons' titles for names, each different
// from the others (WhatsApp refuses two buttons with one title): the first
// name; for names that would read alike, the first name and the last
// name's initial, then the whole name; and, when even those read alike
// once cut to the button's 20 characters, a number after.
func approveTitles(names []string, title func(name string) string) []string {
	forms := func(n string) []string {
		f := strings.Fields(n)
		if len(f) == 0 {
			return []string{n, n, n}
		}
		short := f[0]
		if len(f) > 1 {
			r, _ := utf8.DecodeRuneInString(f[len(f)-1])
			short += " " + string(r) + "."
		}
		return []string{f[0], short, strings.Join(f, " ")}
	}
	level := make([]int, len(names))
	out := make([]string, len(names))
	for range 3 {
		for i, n := range names {
			out[i] = cutRunes(title(forms(n)[level[i]]), buttonTitleMax)
		}
		clash := false
		for i := range out {
			for j := range out {
				if i != j && strings.EqualFold(out[i], out[j]) && level[i] < 2 {
					level[i]++
					clash = true
					break
				}
			}
		}
		if !clash {
			break
		}
	}
	for i := range out {
		for j := 0; j < i; j++ {
			if strings.EqualFold(out[i], out[j]) {
				n := fmt.Sprintf(" %d", i+1)
				out[i] = strings.TrimSpace(cutRunes(out[i], buttonTitleMax-len(n))) + n
				break
			}
		}
	}
	return out
}
