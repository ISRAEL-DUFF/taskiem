package saved

import (
	"errors"
	"strings"
	"testing"
)

func TestCleanKinds(t *testing.T) {
	c, err := Clean(KindPerson, map[string]any{"phone": "0803 123 4567", "email": "mum@example.ng", "birthday": "1960-03-21", "note": " for  her ", "junk": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Details["phone"] != "+2348031234567" || c.Details["note"] != "for her" || c.Details["junk"] != nil || c.Hint != "+2348031234567" {
		t.Fatalf("person: %+v", c)
	}
	if c, err := Clean(KindPerson, nil); err != nil || c.Hint != "A person" {
		t.Fatalf("an empty person: %+v %v", c, err)
	}

	c, err = Clean(KindPlace, map[string]any{"line": "12 Herbert Macaulay Way", "area": "Yaba", "landmark": "opposite the filling station", "phone": "08031234567"})
	if err != nil || c.Hint != "Yaba" || c.Details["landmark"] != "opposite the filling station" {
		t.Fatalf("place: %+v %v", c, err)
	}

	c, err = Clean(KindText, map[string]any{"body": "\nPay to:\r\n  GTBank   0123456789 \n\n"})
	if err != nil || c.Details["body"] != "Pay to:\nGTBank 0123456789" || c.Hint != "2 lines of text" {
		t.Fatalf("text: %+v %v", c, err)
	}

	c, err = Clean(KindProviderAccount, map[string]any{"type": "electricity", "provider": "Ikeja Electric", "number": "45012345678", "name_on_it": "ADA OBI"})
	if err != nil || c.Hint != "Ikeja Electric 45012345678" || ProviderWord("electricity") != "Electricity" {
		t.Fatalf("provider: %+v %v", c, err)
	}

	for _, bad := range []struct {
		kind string
		in   map[string]any
		says string
	}{
		{KindPerson, map[string]any{"phone": "12"}, "phone number"},
		{KindPerson, map[string]any{"email": "not an email"}, "email"},
		{KindPerson, map[string]any{"birthday": "21/03/1960"}, "birthday"},
		{KindPerson, map[string]any{"birthday": "2999-01-01"}, "birthday"},
		{KindPlace, map[string]any{"area": "Yaba"}, "street and number"},
		{KindText, map[string]any{"body": "  \n "}, "Write the text"},
		{KindText, map[string]any{"body": strings.Repeat("a", MaxText+1)}, "4000"},
		{KindProviderAccount, map[string]any{"type": "gas", "provider": "X", "number": "1"}, "what the provider is"},
		{KindProviderAccount, map[string]any{"type": "tv", "provider": "DSTV"}, "number at the provider"},
		{KindPlace, map[string]any{"line": 12}, "is words"},
		{"car", nil, "not a kind"},
	} {
		_, err := Clean(bad.kind, bad.in)
		if !errors.Is(err, ErrInvalid) || !strings.Contains(Plain(err), bad.says) {
			t.Errorf("%s %v: %v", bad.kind, bad.in, err)
		}
	}
}
