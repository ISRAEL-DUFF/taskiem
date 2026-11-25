package api

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/connectors/payrolla"
	"github.com/israel-duff/taskiem/engine/invoicing"
)

// Settings and schedules in the chat (P4b-5 of
// docs/spec/chat-pay-people.md): "payroll settings" says how the payroll
// is set up, with a link to see it; "pension schedule for november" (or
// PAYE, tax, NHF, NHIS) gives the schedule's total and a link. A
// schedule's lines (each person's amount, PIN and PFA) are never put in a
// message: they are behind sign-in, as payslips are.

var (
	payrollSettingsWords = regexp.MustCompile(`(?i)^\s*(?:show\s+(?:me\s+)?(?:the\s+|my\s+)?)?payroll\s+(?:settings|setup)\s*[.?!]?\s*$`)
	payrollScheduleWords = regexp.MustCompile(`(?i)^\s*(?:show\s+(?:me\s+)?)?(?:the\s+)?(paye|tax|pension|nhf|nhis|housing\s+fund|health\s+insurance)\s+schedules?(?:\s+for\s+([a-z]+)(?:\s+(\d{4}))?)?\s*[.?!]?\s*$`)
)

// waPayrollReports answers "payroll settings" and "<kind> schedule for
// <month>"; false when the message is neither, or the person may not see
// the payroll.
func (s *Server) waPayrollReports(ctx context.Context, c *chat, text string) (bool, error) {
	if c.p == nil || c.p.UserID == uuid.Nil || c.tenant.ID == uuid.Nil || c.ch == nil || c.state != stateIdle || c.in.Reply != "" || !c.p.Can(PermPayrollSee) {
		return false, nil
	}
	if payrollSettingsWords.MatchString(text) {
		return true, s.payrollSettingsChat(ctx, c)
	}
	if m := payrollScheduleWords.FindStringSubmatch(text); m != nil {
		return true, s.payrollScheduleChat(ctx, c, m[1], m[2], m[3])
	}
	return false, nil
}

// sayReportRefused says why the settings or schedules can't be read.
func (s *Server) sayReportRefused(ctx context.Context, c *chat, err error) error {
	var pr payRefused
	if errors.As(err, &pr) {
		if pr.code == "scope_required" {
			if isOwnerOf(c.p) {
				return c.say(ctx, s, c.t("payroll.schedule.scope_owner"))
			}
			return c.say(ctx, s, c.t("payroll.schedule.scope_other"))
		}
		return c.say(ctx, s, pr.msg)
	}
	if errors.Is(err, errPayrollaNotConnected) {
		return c.say(ctx, s, c.t("payroll.none"))
	}
	return err
}

func (s *Server) payrollSettingsChat(ctx context.Context, c *chat) error {
	cl, err := s.reportsClient(ctx, c.p)
	if err != nil {
		return s.sayReportRefused(ctx, c, err)
	}
	st, err := cl.Settings(ctx)
	if err != nil {
		return s.sayReportRefused(ctx, c, s.reportsSays(ctx, c.p, err))
	}
	set := setupOf(st, "")
	var lines []string
	for _, r := range set.Rows {
		lines = append(lines, "• "+r.Label+": "+r.Value)
	}
	if set.NotSetUp != "" {
		lines = append(lines, set.NotSetUp)
	}
	for _, r := range set.Deductions {
		lines = append(lines, "• "+r.Label+": "+r.Value)
	}
	return c.say(ctx, s, c.t("payroll.settings.chat", "lines", strings.Join(lines, "\n"), "url", strings.TrimRight(s.PublicURL, "/")+"/payroll/settings"))
}

func scheduleKindOf(word string) string {
	w := strings.ToLower(strings.Join(strings.Fields(word), " "))
	switch w {
	case "paye", "tax":
		return "paye"
	case "housing fund", "nhf":
		return "nhf"
	case "health insurance", "nhis":
		return "nhis"
	}
	return "pension"
}

var scheduleTitles = map[string]string{"paye": "PAYE", "pension": "pension", "nhf": "NHF", "nhis": "NHIS"}

// payrollScheduleChat finds the newest schedule of a kind (for a month, if
// named) and gives its total and a link; with one schedule a PFA, a
// month's pension is said as one total.
func (s *Server) payrollScheduleChat(ctx context.Context, c *chat, word, month, year string) error {
	kind := scheduleKindOf(word)
	period := ""
	if month != "" {
		var ok bool
		if period, ok = payrollMonth(time.Now().In(lagos()), month, year); !ok {
			return c.say(ctx, s, c.t("payroll.prepare.which"))
		}
	}
	cl, err := s.reportsClient(ctx, c.p)
	if err != nil {
		return s.sayReportRefused(ctx, c, err)
	}
	list, err := cl.Remittances(ctx, "")
	if err != nil {
		return s.sayReportRefused(ctx, c, s.reportsSays(ctx, c.p, err))
	}
	// The month asked for; a month named without a year that has none yet
	// may be last year's.
	var found []payrolla.Remittance
	for _, rm := range list {
		if scheduleKind(rm.Authority) != kind {
			continue
		}
		switch {
		case period == "":
			if len(found) == 0 || found[0].Period == rm.Period {
				found = append(found, rm)
			}
		case rm.Period == period, year == "" && len(found) == 0 && len(rm.Period) == 7 && rm.Period[5:] == period[5:]:
			if len(found) == 0 || found[0].Period == rm.Period {
				found = append(found, rm)
			}
		}
	}
	title := scheduleTitles[kind]
	if len(found) == 0 {
		if period == "" {
			return c.say(ctx, s, c.t("payroll.schedule.none_any", "kind", title))
		}
		return c.say(ctx, s, c.t("payroll.schedule.none", "kind", title, "period", monthWords(period)))
	}
	var total, people int64
	toPay := false
	for _, rm := range found {
		total += rm.TotalKobo
		people += rm.EmployeeCount
		toPay = toPay || rm.Status != "PAID"
	}
	v := viewOf(found[0], "")
	state := c.t("payroll.schedule.paid")
	if toPay {
		state = c.t("payroll.schedule.to_pay")
		if v.When != "" {
			state = c.t("payroll.schedule.due", "when", strings.TrimPrefix(v.When, "Due "))
		}
	}
	url := strings.TrimRight(s.PublicURL, "/") + "/payroll/schedules"
	if len(found) == 1 {
		url += "/" + found[0].ID
	}
	return c.say(ctx, s, c.t("payroll.schedule.card", "kind", title, "period", v.PeriodWords, "amount", invoicing.Naira(total),
		"people", peopleWords(c.lang.Tag, people), "state", state, "url", url))
}
