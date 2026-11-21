package saved

import "strings"

// The check digit of a Nigerian account number (NUBAN, CBN's standard):
// its last digit is set by the bank's code and the other nine, so only
// some banks' codes fit a given number. Banks a number fits are offered
// first when the bank is not known; a bank it does not fit is not ruled
// out (some providers' numbers are not NUBANs), only put later.

var nubanWeights = []int{3, 7, 3}

// NUBANFits reports whether number (10 digits) can be an account at the
// bank with CBN's code: 3 digits (deposit money banks) or 5 (other
// institutions, worked as "9" and the five). known is false for any other
// code: a provider's own (CBNCode finds a bank's CBN code by its name).
func NUBANFits(code, number string) (fits, known bool) {
	if !tenDigits.MatchString(number) || !digitsOnly(code) {
		return false, false
	}
	switch len(code) {
	case 3:
	case 5:
		// CBN's code for other institutions, worked as "9" and the five.
		code = "9" + code
	default:
		// A provider's own code (NIP's six digits): see CBNCode.
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

// cbnCodes are the CBN codes of the deposit money banks, by the words
// that name them: the check digit is worked with these, whatever code a
// payments provider lists the bank under (NIP's "000014" is Access, 044).
var cbnCodes = []struct{ words, code string }{
	{"access", "044"}, {"guaranty trust", "058"}, {"gtbank", "058"}, {"gtb", "058"}, {"zenith", "057"},
	{"first bank", "011"}, {"firstbank", "011"}, {"united bank for africa", "033"}, {"uba", "033"},
	{"fidelity", "070"}, {"first city monument", "214"}, {"fcmb", "214"}, {"union bank", "032"},
	{"sterling", "232"}, {"wema", "035"}, {"stanbic", "221"}, {"ecobank", "050"}, {"polaris", "076"},
	{"keystone", "082"}, {"unity bank", "215"}, {"citibank", "023"}, {"standard chartered", "068"},
	{"providus", "101"}, {"jaiz", "301"}, {"titan trust", "102"}, {"globus", "103"}, {"suntrust", "100"},
	{"taj bank", "302"}, {"lotus bank", "303"}, {"premiumtrust", "105"}, {"parallex", "104"}, {"optimus", "107"},
}

// CBNCode is the CBN code to work a bank's check digit with: the code
// itself when it is CBN's own (3 digits, or 5 for other institutions),
// else the deposit money bank its name names; "" when not known (a
// provider's own code: the check is not made).
func CBNCode(code, name string) string {
	if digitsOnly(code) && (len(code) == 3 || len(code) == 5) {
		return code
	}
	lower := " " + strings.ToLower(readNotWord.ReplaceAllString(name, " ")) + " "
	for _, b := range cbnCodes {
		if strings.Contains(lower, " "+b.words+" ") {
			return b.code
		}
	}
	return ""
}
