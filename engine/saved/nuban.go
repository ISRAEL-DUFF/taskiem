package saved

// The check digit of a Nigerian account number (NUBAN, CBN's standard):
// its last digit is set by the bank's code and the other nine, so only
// some banks' codes fit a given number. Banks a number fits are offered
// first when the bank is not known; a bank it does not fit is not ruled
// out (some providers' numbers are not NUBANs), only put later.

var nubanWeights = []int{3, 7, 3}

// NUBANFits reports whether number (10 digits) can be an account at the
// bank with code: a 3-digit code (deposit money banks), or a 5- or 6-digit
// one (other institutions: a 5-digit code is the 6-digit one without its
// leading 9). known is false when the code is not one the check knows.
func NUBANFits(code, number string) (fits, known bool) {
	if !tenDigits.MatchString(number) || !digitsOnly(code) {
		return false, false
	}
	switch len(code) {
	case 3, 6:
	case 5:
		code = "9" + code
	default:
		return false, false
	}
	s := code + number[:9]
	sum := 0
	for i, r := range s {
		sum += int(r-'0') * nubanWeights[i%3]
	}
	check := (10 - sum%10) % 10
	return check == int(number[9]-'0'), true
}

func digitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
