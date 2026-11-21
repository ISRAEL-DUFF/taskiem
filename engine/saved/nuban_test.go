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
	// 999992 + 812345678 (15 digits): check worked the same way.
	sum := 0
	for i, r := range "999992812345678" {
		sum += int(r-'0') * []int{3, 7, 3}[i%3]
	}
	n := "812345678" + string(rune('0'+(10-sum%10)%10))
	if fits, known := NUBANFits("999992", n); !fits || !known {
		t.Errorf("999992 %s: %v %v", n, fits, known)
	}
	if fits, known := NUBANFits("99992", n); !fits || !known {
		t.Errorf("a 5-digit code: %v %v", fits, known)
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
