package moneylog

import (
	"errors"
	"strings"
	"testing"
)

func TestParseKobo(t *testing.T) {
	for in, want := range map[string]int64{"1,234.50": 123450, "-1234.5": -123450, "(1,234.50)": -123450, "₦1,234": 123400, "NGN 1,234.50": 123450,
		"1,234.50 CR": 123450, "1,234.50 DR": -123450, "0.05": 5, "-₦20": -2000} {
		if got, ok := parseKobo(in); !ok || got != want {
			t.Errorf("%q: %d %v, want %d", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "abc", "1.234", "12,3a"} {
		if _, ok := parseKobo(bad); ok {
			t.Errorf("%q read", bad)
		}
	}
}

func TestParseStatement(t *testing.T) {
	// The common format.
	lines, err := ParseStatement(strings.NewReader("Date,Description,Amount,Reference,Balance\n"+
		"2026-10-20,TRF FROM ADA OBI,25000,FT123,125000\n23/10/2026,POS CHARGE,-150.75,,124849.25\n\n"), StatementFormat{})
	if err != nil || len(lines) != 2 || lines[0].AmountKobo != 2500000 || lines[1].AmountKobo != -15075 || lines[1].Date.Day() != 23 || lines[0].Reference != "FT123" {
		t.Fatalf("%v %+v", err, lines)
	}
	// A bank's own columns, matched once: money in and out apart.
	cols := StatementFormat{Date: "Trans Date", Description: "Narration", In: "Credit", Out: "Debit", Reference: "Ref"}
	lines, err = ParseStatement(strings.NewReader("\ufeffTrans Date,Narration,Debit,Credit,Ref\n20-Oct-2026,Salary,,\"100,000.00\",R1\n21-Oct-2026,Diesel,\"12,000.00\",,R2\n"), cols)
	if err != nil || len(lines) != 2 || lines[0].AmountKobo != 10000000 || lines[1].AmountKobo != -1200000 {
		t.Fatalf("own columns: %v %+v", err, lines)
	}
	// Missing columns and bad lines refuse the whole file.
	var se *StatementError
	if _, err := ParseStatement(strings.NewReader("when,what,how much\n2026-10-20,x,1\n"), StatementFormat{}); !errors.As(err, &se) || len(se.Missing) != 3 {
		t.Errorf("missing: %v", err)
	}
	_, err = ParseStatement(strings.NewReader("date,description,amount\n2026-10-20,ok,1\nyesterday,bad,1\n2026-10-21,zero,0\n"), StatementFormat{})
	if !errors.As(err, &se) || len(se.Problems) != 2 || se.Problems[0].Row != 3 || !errors.Is(err, ErrStatement) {
		t.Errorf("bad lines: %v %+v", err, se)
	}
	// The common format named in full (as an account with no saved
	// columns has) still takes a file without the optional reference.
	if lines, err := ParseStatement(strings.NewReader("date,description,amount\n2026-10-20,x,1\n"), CommonColumns); err != nil || len(lines) != 1 {
		t.Errorf("common format in full: %v", err)
	}
	if _, err := ParseStatement(strings.NewReader(""), StatementFormat{}); !errors.Is(err, ErrStatement) {
		t.Errorf("empty: %v", err)
	}
}
