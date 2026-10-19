package publicnames

import (
	"errors"
	"strings"
	"testing"
)

// r makes a one-rune string, so tests can hold invisible characters
// without putting them in the source.
func r(c rune) string { return string(c) }

func TestSkeleton(t *testing.T) {
	cases := []struct{ in, want string }{
		{"paystack", "paystack"},
		{"PAYSTACK", "paystack"},
		{"0pay", "opay"},
		{"g7bank", "gtbank"},
		{"5terling", "sterlng"},
		{"4ccessb4nk", "acesbank"},
		{"3cobank", "ecobank"},
		{"8ank", "bank"},
		{"1ogin", "logln"},
		{"login", "logln"},
		{"LOGIN", "logln"},
		{"|ogin", "logln"},
		{"rnoniepoint", "monlepolnt"},
		{"moniepoint", "monlepolnt"},
		{"vvhatsapp", "whatsap"},
		{"whatsapp", "whatsap"},
		{"paystaack", "paystack"},
		{"gtbannk", "gtbank"},
		{"flutterwave", "fluterwave"},
		{"gtbank-help", "gtbank-help"},
		{"pay-y", "pay-y"},
		// Cyrillic а, е, о, р, с, ѕ, у, х, і.
		{"\u043epay", "opay"},
		{"\u0440ay\u0455tack", "paystack"},
		{"\u0430\u0441\u0441\u0435ss", "aces"},
		{"\u0445\u0443z", "xyz"},
		{"\u0456nterswitch", "lnterswltch"},
		{"\u0405\u0422\u0415RLING", "sterlng"},
		// Greek ο, ν, α, ρ, κ, and capital Η, which looks like H.
		{"\u03bfpay", "opay"},
		{"\u03bderve", "verve"},
		{"\u03b1\u03c1\u03c1le", "aple"},
		{"\u03bauda", "kuda"},
		{"\u0397ELP", "help"},
		// Accents and fullwidth letters decompose.
		{"p\u00e1yst\u00e1ck", "paystack"},
		{"\uff27\uff34bank", "gtbank"},
		{"", ""},
		{"ada's kitchen", "ada's kltchen"},
	}
	for _, c := range cases {
		if got := Skeleton(c.in); got != c.want {
			t.Errorf("Skeleton(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSkeletonLookAlikesMatch(t *testing.T) {
	pairs := [][2]string{
		{"opay", "0pay"},
		{"opay", "\u043ep\u0430y"},
		{"interswitch", "1nterswltch"},
		{"moniepoint", "rnoniepoint"},
		{"whatsapp", "vvhatsap"},
		{"google", "g00gle"},
		{"paystack", "p4y5t4ck"},
		{"gtbank", "GTBANK"},
	}
	for _, p := range pairs {
		if Skeleton(p[0]) != Skeleton(p[1]) {
			t.Errorf("Skeleton(%q) = %q, Skeleton(%q) = %q, want equal", p[0], Skeleton(p[0]), p[1], Skeleton(p[1]))
		}
	}
}

func TestNormalizeSlug(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"ada-kitchen", "ada-kitchen", true},
		{"  Ada-Kitchen  ", "ada-kitchen", true},
		{"abc", "abc", true},
		{"a1b2c3", "a1b2c3", true},
		{strings.Repeat("a", 40), strings.Repeat("a", 40), true},
		{"ab", "", false},
		{"", "", false},
		{strings.Repeat("a", 41), "", false},
		{"-ada", "", false},
		{"ada-", "", false},
		{"ada--kitchen", "", false},
		{"ada_kitchen", "", false},
		{"ada kitchen", "", false},
		{"ada.kitchen", "", false},
		{"caf\u00e9", "", false},
		{"\u043epay", "", false},
	}
	for _, c := range cases {
		got, err := NormalizeSlug(c.in)
		if c.ok {
			if err != nil || got != c.want {
				t.Errorf("NormalizeSlug(%q) = %q, %v; want %q", c.in, got, err, c.want)
			}
			continue
		}
		if !errors.Is(err, ErrSlugFormat) {
			t.Errorf("NormalizeSlug(%q) error = %v, want ErrSlugFormat", c.in, err)
		}
	}
}

func TestCheckSlug(t *testing.T) {
	cases := []struct {
		slug string
		want error
	}{
		// Allowed, including the false-positive guards.
		{"ada-kitchen", nil},
		{"mama-put-lekki", nil},
		{"global-foods", nil},        // glo is matched only as a token
		{"cabana", nil},              // cbn is not a token here
		{"union-street-bakery", nil}, // union is not protected; unionbank is
		{"access-hair-salon", nil},   // access is not protected; accessbank is
		{"firstclass-tailors", nil},  // first is not protected; firstbank is
		{"jambalaya-kitchen", nil},   // jamb is matched only as a token
		{"remittance-services", nil}, // remita is matched only as a token
		{"visage-salon", nil},        // visa is matched only as a token
		{"zenithal-prints", nil},     // zenith is matched only as a token
		{"applied-arts", nil},
		{"lekki-mall-shoes", nil},
		{"shopwise", nil}, // reserved words match only the whole slug
		{"supportive-care", nil},

		// Malformed.
		{"ab", ErrSlugFormat},
		{"ada--kitchen", ErrSlugFormat},
		{"-ada", ErrSlugFormat},

		// Reserved, exactly or by skeleton.
		{"www", ErrReserved},
		{"admin", ErrReserved},
		{"ADMIN", ErrReserved},
		{"4dmin", ErrReserved},
		{"l0gin", ErrReserved},
		{"sign-in", ErrReserved},
		{"support", ErrReserved},
		{"payments", ErrReserved},
		{"shop", ErrReserved},
		{"no-reply", ErrReserved},
		{"noreply", ErrReserved},
		{"mall", ErrReserved}, // shares mail's skeleton; accepted as a cost

		// Protected, exactly, as a token, a run of tokens, or an affix.
		{"gtbank", ErrProtected},
		{"gtbank-support", ErrProtected},
		{"paystack-ng", ErrProtected},
		{"my-opay", ErrProtected},
		{"0pay", ErrProtected},
		{"\u043epay-ng", ErrSlugFormat}, // non-ASCII never reaches the list
		{"gtbankng", ErrProtected},
		{"lagos-gtbank", ErrProtected},
		{"mygtbank", ErrProtected},
		{"g7bank-help", ErrProtected},
		{"pay-stack", ErrProtected},
		{"access-bank", ErrProtected},
		{"accessbank-ng", ErrProtected},
		{"union-bank-ng", ErrProtected},
		{"unionbank", ErrProtected},
		{"first-bank", ErrProtected},
		{"my-first-bank-ng", ErrProtected},
		{"rnoniepoint", ErrProtected},
		{"vvhatsapp-support", ErrProtected},
		{"1nterswitch", ErrProtected},
		{"flutterwavee", ErrProtected},
		{"uba", ErrProtected},
		{"uba-loans", ErrProtected},
		{"cbn-grants", ErrProtected},
		{"glo-data", ErrProtected},
		{"mtn-promo", ErrProtected},
		{"9mobile-data", ErrProtected},
		{"zenith-motors", ErrProtected},
		{"sterling-silver", ErrProtected},
		{"apple-bakery", ErrProtected},
		{"firs-tax", ErrProtected},
		{"jamb-result", ErrProtected},
		{"efcc", ErrProtected},
		{"efcc-recovery", ErrProtected},
		{"taskiem-help", ErrProtected},
		{"xenibase-login", ErrProtected},
	}
	for _, c := range cases {
		err := CheckSlug(c.slug)
		if c.want == nil {
			if err != nil {
				t.Errorf("CheckSlug(%q) = %v, want nil", c.slug, err)
			}
			continue
		}
		if !errors.Is(err, c.want) {
			t.Errorf("CheckSlug(%q) = %v, want %v", c.slug, err, c.want)
		}
	}
}

func TestCheckSlugNamesTerm(t *testing.T) {
	err := CheckSlug("my-0pay")
	if err == nil || !strings.Contains(err.Error(), `"opay"`) {
		t.Errorf("CheckSlug(my-0pay) = %v, want it to name opay", err)
	}
	err = CheckSlug("sign-in")
	if err == nil || !strings.Contains(err.Error(), `"signin"`) {
		t.Errorf("CheckSlug(sign-in) = %v, want it to name signin", err)
	}
}

func TestCheckDisplayName(t *testing.T) {
	cases := []struct {
		name string
		want error
	}{
		{"Ada's Kitchen", nil},
		{"  Mama Put  ", nil},
		{"A", nil},
		{strings.Repeat("a", 80), nil},
		{strings.Repeat("\u00e9", 80), nil}, // counted in characters, not bytes
		{"Global Foods", nil},
		{"Cabana Lounge", nil},
		{"Union Street Bakery", nil},
		{"Access Hair Salon", nil},
		{"Firstclass Tailors", nil},
		{"St. Mary's Bakery", nil},
		{"Caf\u00e9 Ol\u00fa", nil},
		{"Ada " + r(0x1F469) + r(0x200D) + r(0x1F373), nil}, // emoji with a zero-width joiner
		{"Cafe @ Lekki", nil},

		{"", ErrDisplayName},
		{"   ", ErrDisplayName},
		{strings.Repeat("a", 81), ErrDisplayName},
		{"Ada\xffKitchen", ErrDisplayName},
		{"Ada\nKitchen", ErrDisplayName},
		{"Ada\x00Kitchen", ErrDisplayName},
		{"Ada" + r(0x202E) + "nehctik", ErrDisplayName},
		{"Ada" + r(0x2066) + "Kitchen", ErrDisplayName},
		{"Ada" + r(0x200B) + "Kitchen", ErrDisplayName},
		{"Ada" + r(0xFEFF) + "Kitchen", ErrDisplayName},
		{"Visit https://evil.example", ErrDisplayName},
		{"www.adakitchen", ErrDisplayName},
		{"adakitchen.com.ng", ErrDisplayName},
		{"Ada Kitchen ada.ng", ErrDisplayName},
		{"Order: ada@example.com", ErrDisplayName},

		{"GTBank Customer Care", ErrProtected},
		{"Paystack Payments", ErrProtected},
		{"GT Bank Support", ErrProtected},
		{"First Bank of Nigeria", ErrProtected},
		{"Access Bank Plc", ErrProtected},
		{"0PAY Agent", ErrProtected},
		{"\u041epay Agent", ErrProtected}, // Cyrillic О
		{"MTN Promo", ErrProtected},
		{"CBN Grants", ErrProtected},
		{"Taskiem Support", ErrProtected},
		{"Moniepoint POS", ErrProtected},
	}
	for _, c := range cases {
		err := CheckDisplayName(c.name)
		if c.want == nil {
			if err != nil {
				t.Errorf("CheckDisplayName(%q) = %v, want nil", c.name, err)
			}
			continue
		}
		if !errors.Is(err, c.want) {
			t.Errorf("CheckDisplayName(%q) = %v, want %v", c.name, err, c.want)
		}
	}
}

func TestCheckDomain(t *testing.T) {
	cases := []struct {
		in, want string
		err      error
	}{
		{"adakitchen.com", "adakitchen.com", nil},
		{"AdaKitchen.com.ng.", "adakitchen.com.ng", nil},
		{" shop.adakitchen.ng ", "shop.adakitchen.ng", nil},
		{"ada-kitchen.co.uk", "ada-kitchen.co.uk", nil},
		{"globalfoods.com", "globalfoods.com", nil},
		{"access-hair.com.ng", "access-hair.com.ng", nil},
		{"unilag.edu.ng", "unilag.edu.ng", nil},

		{"", "", ErrDomain},
		{"localhost", "", ErrDomain},
		{"app.localhost", "", ErrDomain},
		{"adakitchen", "", ErrDomain},
		{"127.0.0.1", "", ErrDomain},
		{"10.0.0.1.", "", ErrDomain},
		{"::1", "", ErrDomain},
		{"1.2.3.999", "", ErrDomain},
		{"xn--pystack-2fg.com", "", ErrDomain},
		{"shop.xn--80ak6aa92e.com", "", ErrDomain},
		{"-ada.com", "", ErrDomain},
		{"ada-.com", "", ErrDomain},
		{"ada..com", "", ErrDomain},
		{"ada_kitchen.com", "", ErrDomain},
		{"ada kitchen.com", "", ErrDomain},
		{"https://ada.com", "", ErrDomain},
		{strings.Repeat("a", 64) + ".com", "", ErrDomain},
		{"com.ng", "", ErrDomain},
		{"cbn.gov.ng", "", ErrDomain},
		{"portal.nimc.gov.ng", "", ErrDomain},
		{"army.mil.ng", "", ErrDomain},

		{"gtbank.com", "", ErrProtected},
		{"gtbank-support.com.ng", "", ErrProtected},
		{"paystack-ng.com", "", ErrProtected},
		{"0pay.ng", "", ErrProtected},
		{"myopay.com", "", ErrProtected},
		{"accessbank.co.uk", "", ErrProtected},
		{"gtbank.adakitchen.com", "", ErrProtected}, // subdomains count too
		{"uba.ng", "", ErrProtected},
		{"taskiem-app.com", "", ErrProtected},
	}
	for _, c := range cases {
		got, err := CheckDomain(c.in)
		if c.err == nil {
			if err != nil || got != c.want {
				t.Errorf("CheckDomain(%q) = %q, %v; want %q", c.in, got, err, c.want)
			}
			continue
		}
		if !errors.Is(err, c.err) || got != "" {
			t.Errorf("CheckDomain(%q) = %q, %v; want %v", c.in, got, err, c.err)
		}
	}
}

func TestPublicSuffix(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"ada.com", 1},
		{"ada.ng", 1},
		{"ada.com.ng", 2},
		{"ada.sch.ng", 2},
		{"ada.co.za", 2},
		{"ada.co.ng", 1}, // not in the list, so ng alone
		{"com.ng", 2},
	}
	for _, c := range cases {
		if got := publicSuffix(strings.Split(c.in, ".")); got != c.want {
			t.Errorf("publicSuffix(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// Every protected name refuses itself as a slug, and no two names share a
// skeleton (which would silently drop one).
func TestProtectedList(t *testing.T) {
	if len(protectedSkel) != len(protected) {
		t.Fatalf("%d protected names share skeletons", len(protected)-len(protectedSkel))
	}
	for _, p := range protected {
		if err := CheckSlug(p.name); !errors.Is(err, ErrProtected) && !errors.Is(err, ErrReserved) {
			t.Errorf("CheckSlug(%q) = %v, want refused", p.name, err)
		}
	}
	if len(reservedSkel) != len(Reserved) {
		t.Errorf("%d reserved words share skeletons", len(Reserved)-len(reservedSkel))
	}
}
