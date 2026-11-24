package api

import (
	"testing"
	"time"
)

func TestPayrollAmount(t *testing.T) {
	for in, want := range map[string]int64{"100k": 10000000, "₦100,000": 10000000, "1.5m": 150000000, "250000": 25000000, "20 k": 2000000, "500 naira": 50000} {
		if got, ok := payrollAmount(in); !ok || got != want {
			t.Errorf("%q: %d %v, want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"0k", "abc", "", "-5k", "200m"} {
		if _, ok := payrollAmount(in); ok {
			t.Errorf("%q read", in)
		}
	}
}

func TestPayFromDate(t *testing.T) {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, lagos())
	for in, want := range map[string]string{"": "", "today": "", "january": "2027-01-01", "1 january": "2027-01-01", "15th march 2027": "2027-03-15",
		"next month": "2026-12-01", "december": "2026-12-01"} {
		if got, ok := payFromDate(now, in); !ok || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"someday", "31 february", "1 2 3"} {
		if _, ok := payFromDate(now, in); ok {
			t.Errorf("%q read", in)
		}
	}
}

func TestMatchName(t *testing.T) {
	people := []someone{{ID: "1", First: "Musa", Last: "Ali"}, {ID: "2", First: "Musa", Last: "Bello"}, {ID: "3", First: "Zainab", Last: "Bello"}}
	for said, want := range map[string]int{"musa": 2, "Musa Ali": 1, "zainab's": 1, "bello": 2, "zai": 1, "ada": 0, "": 0, "musa bello": 1} {
		if got := matchName(said, people); len(got) != want {
			t.Errorf("%q: %v", said, got)
		}
	}
}

// The phrases each set of words reads, and what they take from them.
func TestPayrollChatPhrases(t *testing.T) {
	type c struct {
		text string
		want []string
	}
	for _, x := range []struct {
		name  string
		words interface{ FindStringSubmatch(string) []string }
		cases []c
	}{
		{"add", payrollAddWords, []c{{"add Musa to payroll", []string{"Musa"}}, {"please add musa ali to the payroll.", []string{"musa ali"}}}},
		{"end", payrollEndWords, []c{{"end musa's employment", []string{"musa", "", ""}}, {"remove musa from payroll", []string{"", "musa", ""}},
			{"take ada obi off payroll", []string{"", "", "ada obi"}}}},
		{"pay", payrollPayWords, []c{{"raise zainab's pay to 500k from january", []string{"zainab", "500k", "january"}},
			{"change Zainab's salary to ₦450,000 a month", []string{"Zainab", "₦450,000", ""}}}},
		{"move", payrollMoveWords, []c{{"move zainab to sales", []string{"zainab", "sales"}}, {"move musa to the kitchen department", []string{"musa", "kitchen"}}}},
		{"bonus", payrollBonusWords, []c{{"bonus 100k to zainab for q4 target", []string{"100k", "zainab", "q4 target"}},
			{"add a bonus of 50,000 to musa ali", []string{"50,000", "musa ali", ""}}}},
		{"give", payrollGiveWords, []c{{"give zainab a 100k bonus for q4 target", []string{"zainab", "100k", "q4 target"}}}},
		{"deduct", payrollDeductWord, []c{{"deduct 20k from musa, advance repaid", []string{"20k", "musa", "advance repaid"}},
			{"deduct 20k from musa's pay for advance repaid", []string{"20k", "musa", "advance repaid"}}}},
		{"leave", payrollLeaveWords, []c{{"leave out ada this month, unpaid leave", []string{"ada", "", "unpaid leave"}},
			{"leave ada out this month because unpaid leave", []string{"", "ada", "unpaid leave"}}, {"leave out ada", []string{"ada", "", ""}}}},
		{"back", payrollBackWords, []c{{"put ada back", []string{"ada"}}, {"put ada back in this month's payroll", []string{"ada"}}}},
		{"changes", payrollChangesWords, []c{{"account changes", []string{}}, {"show me the salary account changes", []string{}}}},
		{"approve", payrollApproveCh, []c{{"approve zainab's account change", []string{"zainab"}}, {"approve the account change", []string{""}}}},
		{"reject", payrollRejectCh, []c{{"reject zainab's account change, call her first", []string{"zainab", "call her first"}}}},
		{"mine", payrollMineWords, []c{{"change my salary account", []string{}}}},
	} {
		for _, k := range x.cases {
			m := x.words.FindStringSubmatch(k.text)
			if m == nil {
				t.Errorf("%s: %q not read", x.name, k.text)
				continue
			}
			for i, w := range k.want {
				if m[i+1] != w {
					t.Errorf("%s: %q group %d = %q, want %q", x.name, k.text, i+1, m[i+1], w)
				}
			}
		}
	}
	for _, not := range []string{"approve payroll", "reject payroll, wrong", "payroll", "add to customers", "leave it", "bonus"} {
		for _, re := range []interface{ MatchString(string) bool }{payrollAddWords, payrollApproveCh, payrollRejectCh, payrollBonusWords, payrollLeaveWords} {
			if re.MatchString(not) {
				t.Errorf("%q read as a change", not)
			}
		}
	}
}
