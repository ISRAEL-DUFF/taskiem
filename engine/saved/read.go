package saved

import (
	"regexp"
	"strings"

	phonenum "github.com/israel-duff/taskiem/engine/phone"
)

// Reading a message's words for what can be saved (saved-objects §9): "Save
// as…" on a message, and "save mum: GTBank 0123456789" to Taskiem. It finds
// an account number and its bank, a phone, an email, an address (its
// street, area and landmark), and a name written as one ("Account name:
// Ada Obi"), and says which kind they make: a bank account when there is an
// account number, a place when there is an address, a person when there is
// a phone or an email, else a text of the words. Nothing here goes to the
// AI, and nothing is kept: the person sees it filled in and saves it.

// Reading is what the words hold, for the sheet that saves one.
type Reading struct {
	Kind string `json:"kind"`
	// Title is a name the words give ("Account name: Ada Obi"), or "".
	Title string `json:"title"`
	// Details are the kind's fields as saved.Clean takes them, and for a
	// bank account bank_name and account_number (account_number is the
	// person's own words read back to them; it is sealed once saved).
	Details map[string]string `json:"details"`
}

var (
	readEmail = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	// A phone: +234 or 0, then 7, 8 or 9 and 9 more digits, spaced or not.
	readPhone = regexp.MustCompile(`(?:\+\s?234|\b0)[\s-]?[789](?:[\s-]?\d){9}\b`)
	// A run of digits, maybe spaced or dashed: an account number is 10.
	readDigits = regexp.MustCompile(`\d(?:[\s-]?\d){5,15}`)
	// "Account name: Ada Obi", "Name - Ada Obi", "acct name Ada Obi"; a
	// bare "name" only with its colon or dash ("name your price" is not one).
	readNamed = regexp.MustCompile(`(?im)^\s*(?:acc(?:oun)?t\.?\s*name\s*[:\-–]?|name\s*[:\-–])\s*([A-Za-z][A-Za-z .'\-]{1,80})$`)
	// Words that say an account is meant: a number with none of them and
	// no bank is an order or a reference, read as text.
	readAccountWords = regexp.MustCompile(`(?i)\b(acc(?:oun)?t|acct|a/c|acc|nuban|bank|transfer|pay(?:ment)?|send)\b`)
	// "…, Account name: Ada Obi" within a line, up to a comma or its end.
	readNamedIn = regexp.MustCompile(`(?i)acc(?:oun)?t\.?\s*name\s*[:\-–]\s*([A-Za-z][A-Za-z .'\-]{1,80}?)\s*(?:[,;\n]|$)`)
	// "Address: …", a line given as one.
	readAddress = regexp.MustCompile(`(?im)^\s*(?:address|addr|location)\s*[:\-–]\s*(.+)$`)
	// A street: a word that names one.
	readStreet = regexp.MustCompile(`(?i)\b(street|st|road|rd|avenue|ave|close|crescent|cres|way|lane|ln|drive|dr|estate|layout|junction|expressway|boulevard|blvd|plaza|quarters|qtrs)\b\.?`)
	// A landmark: "opposite the filling station", "beside GTBank".
	readLandmark = regexp.MustCompile(`(?i)^(opposite|opp\.?|beside|near|behind|close to|next to|after|before|by|off|in front of|adjacent to)\b`)
	readNotWord  = regexp.MustCompile(`[^a-zA-Z0-9]+`)
	// A line that is only a name: two to four words of letters.
	readOnlyName = regexp.MustCompile(`^[A-Za-z][A-Za-z'.\-]*(?:\s+[A-Za-z][A-Za-z'.\-]*){1,3}$`)
	// Words no one's name is made of: a greeting, thanks, a request.
	readNotName = regexp.MustCompile(`(?i)\b(thanks?|thank|please|pls|plz|good|morning|afternoon|evening|night|regards|kind|hello|hi|hey|dear|sir|ma|madam|pay|send|transfer|bank|account|acct|number|cheers|welcome|bless|sorry|okay|ok|your|my|our|the|you|me|us|name|price|call)\b`)
	// A run of digits split where it was spaced or dashed.
	readGroups = regexp.MustCompile(`\d+`)
)

// accountIn is an account number in a run of digits: the run itself when
// it is 10, else 10 made of whole groups as they were spaced ("0123 456
// 789" in "0123 456 789 0803…", "0123456789" before another number).
func accountIn(run string) string {
	groups := readGroups.FindAllString(run, -1)
	for i := range groups {
		d := ""
		for _, g := range groups[i:] {
			d += g
			if len(d) >= 10 {
				break
			}
		}
		if len(d) == 10 {
			return d
		}
	}
	return ""
}

// readNote is what is left of the words once what was read is taken out:
// kept as the note ("call after 6"), its spaces folded, up to MaxNote.
func readNote(text string, taken ...string) string {
	for _, t := range taken {
		if t != "" {
			text = strings.Replace(text, t, " ", 1)
		}
	}
	text = strings.Trim(strings.Join(strings.Fields(text), " "), " ,.;:-–")
	return clip(text, MaxNote)
}

// bankWords are the words that name each bank, as people write them,
// longest first where one holds another.
var bankWords = []struct{ words, bank string }{
	{"guaranty trust", "Guaranty Trust Bank"}, {"gtbank", "Guaranty Trust Bank"}, {"gt bank", "Guaranty Trust Bank"}, {"gtb", "Guaranty Trust Bank"}, {"gtco", "Guaranty Trust Bank"},
	{"united bank for africa", "United Bank for Africa"}, {"uba", "United Bank for Africa"},
	{"first city monument", "First City Monument Bank"}, {"fcmb", "First City Monument Bank"},
	{"first bank", "First Bank of Nigeria"}, {"firstbank", "First Bank of Nigeria"},
	{"stanbic", "Stanbic IBTC Bank"}, {"standard chartered", "Standard Chartered Bank"},
	{"moniepoint", "Moniepoint Microfinance Bank"}, {"kuda", "Kuda Microfinance Bank"},
	{"opay", "OPay Digital Services Limited"}, {"palmpay", "PalmPay Limited"}, {"palm pay", "PalmPay Limited"},
	{"union bank", "Union Bank of Nigeria"}, {"ecobank", "Ecobank Nigeria"}, {"citibank", "Citibank Nigeria"},
	{"premiumtrust", "PremiumTrust Bank"}, {"premium trust", "PremiumTrust Bank"}, {"taj bank", "Taj Bank"}, {"jaiz", "Jaiz Bank"},
	{"wema", "Wema Bank"}, {"alat", "Wema Bank"}, {"zenith", "Zenith Bank"}, {"access", "Access Bank"},
	{"fidelity", "Fidelity Bank"}, {"sterling", "Sterling Bank"}, {"polaris", "Polaris Bank"}, {"keystone", "Keystone Bank"},
	{"unity bank", "Unity Bank"}, {"providus", "Providus Bank"}, {"globus", "Globus Bank"}, {"titan trust", "Titan Trust Bank"},
	{"lotus bank", "Lotus Bank"}, {"parallex", "Parallex Bank"}, {"suntrust", "SunTrust Bank"}, {"optimus", "Optimus Bank"},
	{"signature bank", "Signature Bank"},
}

// readBank is the bank the words name, by its full name, or "".
func readBank(text string) string {
	lower := " " + strings.ToLower(readNotWord.ReplaceAllString(text, " ")) + " "
	for _, b := range bankWords {
		if strings.Contains(lower, " "+b.words+" ") {
			return b.bank
		}
	}
	return ""
}

// Read reads the words for what can be saved.
func Read(text string) Reading {
	text = strings.TrimSpace(text)
	r := Reading{Details: map[string]string{}}
	rest := text
	if m := readEmail.FindString(rest); m != "" {
		r.Details["email"] = m
		rest = strings.Replace(rest, m, " ", 1)
	}
	if m := readPhone.FindString(rest); m != "" {
		if p, err := phonenum.Normalise(m); err == nil {
			r.Details["phone"], r.Details["phone_written"] = p, m
			rest = strings.Replace(rest, m, " ", 1)
		}
	}
	var number string
	for _, m := range readDigits.FindAllString(rest, -1) {
		if d := accountIn(m); d != "" {
			number, r.Details["account_number"] = m, d
			rest = strings.Replace(rest, m, " ", 1)
			break
		}
	}
	if m := readNamed.FindStringSubmatch(text); m != nil {
		r.Title = strings.TrimSpace(m[1])
	} else if m := readNamedIn.FindStringSubmatch(text); m != nil {
		r.Title = strings.TrimSpace(m[1])
	}
	bank := readBank(rest)
	// A number with other words, no bank and no word of an account is not
	// one ("Order ref 1234567890"): the words are read without it. A number
	// alone is asked about (which bank?).
	if number != "" && bank == "" && !readAccountWords.MatchString(text) && strings.TrimSpace(readNotWord.ReplaceAllString(rest, " ")) != "" {
		delete(r.Details, "account_number")
	}
	if _, ok := r.Details["account_number"]; ok {
		r.Kind = KindBankAccount
		if bank != "" {
			r.Details["bank_name"] = bank
		}
		if r.Title == "" {
			// A line that is only a name, beside the number: "ADA OBI".
			for _, l := range strings.Split(text, "\n") {
				l = strings.TrimSpace(l)
				if readOnlyName.MatchString(l) && readBank(l) == "" && !readNotName.MatchString(l) {
					r.Title = l
					break
				}
			}
		}
		delete(r.Details, "email")
		delete(r.Details, "phone")
		delete(r.Details, "phone_written")
		return r
	}
	if line, area, landmark, found := readPlace(text); line != "" {
		r.Kind = KindPlace
		r.Details["line"] = line
		if area != "" {
			r.Details["area"] = area
		}
		if landmark != "" {
			r.Details["landmark"] = landmark
		}
		delete(r.Details, "email")
		if note := readNote(text, found, r.Details["phone_written"]); note != "" {
			r.Details["note"] = note
		}
		delete(r.Details, "phone_written")
		return r
	}
	if r.Details["phone"] != "" || r.Details["email"] != "" {
		r.Kind = KindPerson
		named := ""
		if m := readNamed.FindString(text); m != "" {
			named = m
		}
		if note := readNote(text, r.Details["email"], r.Details["phone_written"], named); note != "" {
			r.Details["note"] = note
		}
		delete(r.Details, "phone_written")
		return r
	}
	delete(r.Details, "phone_written")
	r.Kind = KindText
	r.Details = map[string]string{"body": text}
	return r
}

// readPlace reads an address: given as one ("Address: …"), or a line
// with a number and a word that names a street ("12 Herbert Macaulay
// Way": "I'll close the shop by 6" names none, "on my way" no number);
// then its parts by commas: the street and number, the area after it, a
// landmark ("opposite the filling station"). whole is the words read as
// the address, as written.
func readPlace(text string) (line, area, landmark, whole string) {
	found := ""
	if m := readAddress.FindStringSubmatch(text); m != nil {
		found, whole = m[1], m[0]
	} else {
		for _, l := range strings.Split(text, "\n") {
			if streetLine(l) {
				found, whole = l, l
				break
			}
		}
	}
	if found == "" {
		return "", "", "", ""
	}
	// What else the line holds (a phone, an email) is not the address.
	found = readPhone.ReplaceAllString(readEmail.ReplaceAllString(found, ""), "")
	for _, part := range strings.Split(found, ",") {
		part = strings.Trim(strings.TrimSpace(part), ".")
		switch {
		case part == "":
		case readLandmark.MatchString(part):
			if landmark == "" {
				landmark = part
			}
		case line == "":
			line = part
		case area == "":
			area = part
		}
	}
	return line, area, landmark, whole
}

// readHouse is a house number: "12", "12b", "No. 12", "Plot 4".
var readHouse = regexp.MustCompile(`(?i)(^|[\s,])(?:no\.?\s*|plot\s+|block\s+)?\d+[a-z]?\b`)

// streetLine says whether a line reads as an address: a house number
// before a word that names a street, in the same part of it (up to a
// comma).
func streetLine(l string) bool {
	for _, part := range strings.Split(l, ",") {
		loc := readStreet.FindStringIndex(part)
		if loc == nil {
			continue
		}
		if readHouse.MatchString(part[:loc[0]]) {
			return true
		}
	}
	return false
}
