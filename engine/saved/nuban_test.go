package saved

import "testing"

// The check digit: numbers worked by CBN's method for a 3-digit and a
// 6-digit code, one digit off, and codes the check does not know.
func TestNUBANFits(t *testing.T) {
	// 058 (GTBank) + 012345678: 0*3+5*7+8*3+0*3+1*7+2*3+3*3+4*7+5*3+6*3+7*7+8*3 = 215; check (10-5)%10 = 5.
	if fits, known := NUBANFits("058", "0123456785"); !fits || !known {
		t.Errorf("058 0123456785: %v %v", fits, known)
	}
	if fits, _ := NUBANFits("058", "0123456786"); fits {
		t.Errorf("a wrong check digit fits")
	}
	// A 5-digit code (other institutions): worked as "9" and the five.
	sum := 0
	for i, r := range "950211812345678" {
		sum += int(r-'0') * []int{3, 7, 3}[i%3]
	}
	n := "812345678" + string(rune('0'+(10-sum%10)%10))
	if fits, known := NUBANFits("50211", n); !fits || !known {
		t.Errorf("50211 %s: %v %v", n, fits, known)
	}
	// A provider's own six digits are not worked: the bank's CBN code is
	// found by its name.
	if _, known := NUBANFits("000014", "0123456785"); known {
		t.Errorf("a NIP code was worked")
	}
	if CBNCode("000013", "Guaranty Trust Bank") != "058" || CBNCode("044", "x") != "044" || CBNCode("999992", "OPay Digital Services") != "" {
		t.Errorf("CBN codes: %q %q %q", CBNCode("000013", "Guaranty Trust Bank"), CBNCode("044", "x"), CBNCode("999992", "OPay Digital Services"))
	}
	if _, known := NUBANFits("ABC", "0123456785"); known {
		t.Errorf("a code that is not digits")
	}
	if _, known := NUBANFits("0581", "0123456785"); known {
		t.Errorf("a 4-digit code")
	}
	if _, known := NUBANFits("058", "012345"); known {
		t.Errorf("a short number")
	}
}
