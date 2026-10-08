package money

import (
	"encoding/json"
	"testing"
)

func TestFormatAndParse(t *testing.T) {
	for minor, want := range map[int64]string{450000: "4500.00", 5: "0.05", 0: "0.00", 100: "1.00", -250: "-2.50", 99995404316_07: "99995404316.07"} {
		if got := Format(minor, 2); got != want {
			t.Errorf("Format(%d) = %s, want %s", minor, got, want)
		}
		if back, err := Parse(want, 2); err != nil || back != minor {
			t.Errorf("Parse(%s) = %d %v", want, back, err)
		}
	}
	if got := Format(123, 0); got != "123" {
		t.Errorf("scale 0: %s", got)
	}
	for _, v := range []any{2000.5, json.Number("2000.50"), "2000.5"} {
		if got, err := Parse(v, 2); err != nil || got != 200050 {
			t.Errorf("Parse(%v) = %d %v", v, got, err)
		}
	}
	for _, bad := range []any{"1.005", "abc", "", nil, true} {
		if _, err := Parse(bad, 2); err == nil {
			t.Errorf("Parse(%v) accepted", bad)
		}
	}
	if got, err := Parse("1.500", 2); err != nil || got != 150 {
		t.Errorf("trailing zero: %d %v", got, err)
	}
}

func TestMinor(t *testing.T) {
	for _, v := range []any{450000, int64(450000), 450000.0, json.Number("450000"), "450000"} {
		if got, err := Minor(v); err != nil || got != 450000 {
			t.Errorf("Minor(%v) = %d %v", v, got, err)
		}
	}
	for _, bad := range []any{4500.5, "45.00", nil, json.Number("1.5")} {
		if _, err := Minor(bad); err == nil {
			t.Errorf("Minor(%v) accepted", bad)
		}
	}
}

func TestReader(t *testing.T) {
	var r Reader
	if r.Ceil(26.875, 2) != 2688 || r.Ceil("10.75", 2) != 1075 || r.Minor(nil, 2) != 0 || r.Err != nil {
		t.Errorf("reader: %v", r.Err)
	}
	if r.Minor("1.005", 2); r.Err == nil {
		t.Error("inexact amount read without error")
	}
}

func TestFloorAndDecimal(t *testing.T) {
	var r Reader
	if got := r.Floor(20672860.25790078, 2); got != 2067286025 || r.Err != nil {
		t.Errorf("floor %d %v", got, r.Err)
	}
	if Decimal(0.00025406) != "0.00025406" || Decimal("21.37") != "21.37" || Decimal(nil) != "" || Decimal(2.765e-05) != "0.00002765" {
		t.Error("decimal")
	}
}

func TestScale(t *testing.T) {
	for cur, want := range map[string]int{"KES": 2, "ugx": 0, " XAF ": 0, "GHS": 2, "EUR": 2, "RWF": 0, "ZMW": 2} {
		if got, ok := Scale(cur); !ok || got != want {
			t.Errorf("%q: %d %v, want %d", cur, got, ok, want)
		}
	}
	if _, ok := Scale("XYZ"); ok {
		t.Error("unknown currency scaled")
	}
}
