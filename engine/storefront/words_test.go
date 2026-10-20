package storefront

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestParsePrice(t *testing.T) {
	for in, want := range map[string]int64{
		"2000": 200000, "₦2,000": 200000, "₦ 2,000": 200000, "N2000": 200000, "#1500": 150000, "2k": 200000, "2.5k": 250000,
		"300.5": 30050, "1,250,000": 125000000, "2000 naira": 200000, "NGN 750": 75000, "1m": 100000000,
	} {
		if got, ok := ParsePrice(in); !ok || got != want {
			t.Errorf("ParsePrice(%q) = %d %v, want %d", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "free", "-200", "0", "2kk", "two thousand", "2000000000", "k"} {
		if got, ok := ParsePrice(bad); ok {
			t.Errorf("ParsePrice(%q) = %d, want refused", bad, got)
		}
	}
}

func TestParseMinutes(t *testing.T) {
	for in, want := range map[string]int{"30": 30, "45min": 45, "45mins": 45, "1h": 60, "1.5hrs": 90, "90m": 90} {
		if got, ok := ParseMinutes(in); !ok || got != want {
			t.Errorf("ParseMinutes(%q) = %d %v, want %d", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "3", "600", "soon", "1d"} {
		if _, ok := ParseMinutes(bad); ok {
			t.Errorf("ParseMinutes(%q) accepted", bad)
		}
	}
}

func TestParseHours(t *testing.T) {
	for in, want := range map[string]string{
		"mon-sat 8-6":                       "Mon–Sat 08:00–18:00; Sun closed",
		"Mon - Sat 8 - 6, closed Sunday":    "Mon–Sat 08:00–18:00; Sun closed",
		"daily 9am-9pm":                     "Mon–Sun 09:00–21:00",
		"every day 9am to 9pm":              "Mon–Sun 09:00–21:00",
		"9-5":                               "Mon–Sun 09:00–17:00",
		"mon-fri 9-6 sat 10-2":              "Mon–Fri 09:00–18:00; Sat 10:00–14:00; Sun closed",
		"weekdays 8:30-17:30, weekends 2-6": "Mon–Fri 08:30–17:30; Sat–Sun 14:00–18:00",
		"mon-fri 9-1 2-6":                   "Mon–Fri 09:00–13:00, 14:00–18:00; Sat–Sun closed",
		"monday to saturday 7am-10pm":       "Mon–Sat 07:00–22:00; Sun closed",
		"fri-sun 6pm-2am":                   "Mon–Thu closed; Fri–Sun 18:00–02:00",
		"open 24 hours":                     "Mon–Sun 00:00–23:59",
		"daily 8-8 closed sun":              "Mon–Sat 08:00–20:00; Sun closed",
		"Tues and Thurs 10-4":               "Mon closed; Tue 10:00–16:00; Wed closed; Thu 10:00–16:00; Fri–Sun closed",
	} {
		h, err := ParseHours(in)
		if err != nil {
			t.Errorf("ParseHours(%q): %v", in, err)
			continue
		}
		if got := FormatHours(h); got != want {
			t.Errorf("ParseHours(%q) = %s, want %s", in, got, want)
		}
		c := Card{Name: "Ada's Kitchen", Hours: h}
		if err := c.Check(); err != nil {
			t.Errorf("ParseHours(%q) gives hours a card refuses: %v", in, err)
		}
	}
	for _, bad := range []string{"", "closed sunday", "whenever", "mon-sat", "mon 8", "mon 25-26", "mon 9-09:00"} {
		if h, err := ParseHours(bad); !errors.Is(err, ErrHours) {
			t.Errorf("ParseHours(%q) = %v, %v; want refused", bad, FormatHours(h), err)
		}
	}
}

func TestSlugCandidates(t *testing.T) {
	got := SlugCandidates("Ada's Kitchen", "Yaba, Lagos")
	if got[0] != "adas-kitchen" || got[1] != "adas-kitchen-yaba-lagos" || got[2] != "adas-kitchen-2" {
		t.Fatalf("%v", got[:3])
	}
	for _, s := range SlugCandidates("Mama Put & Sons Restaurant and Bar Limited of Lagos", "Ikeja") {
		if len(s) > MaxSlug || strings.HasSuffix(s, "-") || strings.Contains(s, "--") {
			t.Errorf("bad candidate %q", s)
		}
	}
	if got := SlugCandidates("Jo", ""); got[0] != "jo-shop" {
		t.Errorf("short: %v", got[0])
	}
	if got := SlugCandidates("Ọja Ẹja", ""); got[0] != "ja-ja" {
		t.Logf("non-ASCII letters are dropped: %v", got[0])
	}
}

func TestQR(t *testing.T) {
	png, err := QRPNG("https://ada-kitchen.shops.test")
	if err != nil || !bytes.HasPrefix(png, []byte("\x89PNG")) {
		t.Fatalf("%v", err)
	}
	svg, err := QRSVG("https://ada-kitchen.shops.test")
	if err != nil || !strings.HasPrefix(svg, "<svg") {
		t.Fatalf("%v", err)
	}
	slug := "ada-kitchen"
	if u := PreviewURL(&slug, "shops.test"); u != "https://ada-kitchen.shops.test" {
		t.Error(u)
	}
	if u := PreviewURL(nil, "shops.test"); u != "" {
		t.Error(u)
	}
}
