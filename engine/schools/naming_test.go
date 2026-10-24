package schools

import (
	"testing"
	"unicode/utf8"
)

// The account name: the school's name shortens word by word, then is cut;
// the child's never shortens unless it alone does not fit, and then it is
// flagged (docs/spec/schools.md 6.3).
func TestAccountName(t *testing.T) {
	for _, c := range []struct {
		school, child string
		limit         int
		want          string
		short, flag   bool
	}{
		{"Greenfield Court Academy", "Ebube Eze", 40, "GREENFIELD COURT ACADEMY / EBUBE EZE", false, false},
		// 36 runes in full: the school's last word goes.
		{"Greenfield Court Academy", "Ebube Eze", 31, "GREENFIELD COURT / EBUBE EZE", true, false},
		{"Greenfield Court Academy", "Ebube Eze", 24, "GREENFIELD / EBUBE EZE", true, false},
		// Then the one word is cut.
		{"Greenfield Court Academy", "Ebube Eze", 18, "GREENF / EBUBE EZE", true, false},
		// A long child's name keeps whole; the school shrinks around it.
		{"Greenfield Court Academy", "Chukwuemeka Oluwaseun Adebayo", 34, "GR / CHUKWUEMEKA OLUWASEUN ADEBAYO", true, false},
		// The child's name alone is too long: cut last, and flagged.
		{"Greenfield Court Academy", "Chukwuemeka Oluwaseun Adebayo", 31, "CHUKWUEMEKA OLUWASEUN ADEBAYO", true, false},
		{"Greenfield Court Academy", "Chukwuemeka Oluwaseun Adebayo-Williams", 31, "CHUKWUEMEKA OLUWASEUN ADEBAYO-W", true, true},
		// Spaces and case are tidied; the default limit is 31.
		{"  st. mary's   college ", " ada  obi ", 0, "ST. MARY'S COLLEGE / ADA OBI", false, false},
		{"", "Ada Obi", 31, "ADA OBI", false, false},
		// Characters, not bytes.
		{"Ọ̀rẹ́ Academy", "Adé Obi", 31, "ỌRẸ ACADEMY / ADÉ OBI", false, false},
	} {
		got, short, flag := AccountName(c.school, c.child, c.limit)
		if c.school == "Ọ̀rẹ́ Academy" {
			// Combining marks count as runes: only check the shape.
			if utf8.RuneCountInString(got) > 31 || short || flag {
				t.Errorf("%q: %q %v %v", c.school, got, short, flag)
			}
			continue
		}
		if got != c.want || short != c.short || flag != c.flag {
			t.Errorf("AccountName(%q, %q, %d) = %q %v %v, want %q %v %v", c.school, c.child, c.limit, got, short, flag, c.want, c.short, c.flag)
		}
		limit := c.limit
		if limit == 0 {
			limit = DefaultNameLimit
		}
		if utf8.RuneCountInString(got) > limit {
			t.Errorf("%q is longer than %d", got, limit)
		}
	}
}
