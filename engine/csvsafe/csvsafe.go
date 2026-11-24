// Package csvsafe is the one guard every CSV export uses against formula
// injection (CSV injection): a text cell a spreadsheet would run as a
// formula is written with a leading apostrophe, which spreadsheets show
// as text and hide. It is for cells holding words that are not Taskiem's
// own (a name, a class, a reference, a description); amounts and dates
// Taskiem formats itself are left alone, so a number stays a number.
package csvsafe

import "strings"

// Start are the first characters a spreadsheet reads as a formula: the
// operators, and a tab or carriage return, which Excel strips before
// reading what follows.
const Start = "=+-@\t\r"

// Text guards one text cell.
func Text(v string) string {
	if v != "" && strings.ContainsRune(Start, rune(v[0])) {
		return "'" + v
	}
	return v
}

// Unguard undoes Text on a cell read back in, so an export imports back
// unchanged.
func Unguard(v string) string {
	if len(v) > 1 && v[0] == '\'' && strings.ContainsRune(Start, rune(v[1])) {
		return v[1:]
	}
	return v
}
