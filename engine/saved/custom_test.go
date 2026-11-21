package saved

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCustomKinds(t *testing.T) {
	name, fields, err := CleanKind("  Car ", []Field{
		{Label: "Plate number", Type: "text", Required: true},
		{Label: "Insurance due", Type: "date"},
		{Label: "Chassis number", Type: "text", NeverShare: true},
		{Label: "Fuel", Type: "choice", Options: []string{"Petrol", " Diesel ", "Petrol"}},
		{Label: "Value", Type: "money"},
		{Label: "Driver’s phone", Type: "phone"},
	})
	if err != nil || name != "Car" {
		t.Fatal(name, err)
	}
	if keys := []string{fields[0].Key, fields[1].Key, fields[2].Key, fields[5].Key}; !reflect.DeepEqual(keys, []string{"plate_number", "insurance_due", "chassis_number", "drivers_phone"}) {
		t.Fatalf("keys: %v", keys)
	}
	if !fields[2].Sensitive || !reflect.DeepEqual(fields[3].Options, []string{"Petrol", "Diesel"}) {
		t.Fatalf("fields: %+v", fields)
	}
	for _, bad := range [][]Field{
		nil,
		{{Label: "A", Type: "text"}, {Label: "a", Type: "text"}},
		{{Label: "A", Type: "colour"}},
		{{Label: "Fuel", Type: "choice", Options: []string{"Petrol"}}},
	} {
		if _, _, err := CleanKind("Car", bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v passed", bad)
		}
	}
	if _, _, err := CleanKind("Bank account", []Field{{Label: "A", Type: "text"}}); !errors.Is(err, ErrInvalid) {
		t.Error("a known kind's name passed")
	}

	vals, err := CleanValues(fields, map[string]any{"plate_number": "LND 123 XY", "insurance_due": "2027-03-03", "chassis_number": "JTDBR32E720123456", "fuel": "Diesel", "value": "₦4,500,000"}, nil)
	if err != nil || vals["value"] != "4500000" {
		t.Fatal(vals, err)
	}
	if _, err := CleanValues(fields, map[string]any{"insurance_due": "3 March"}, vals); !errors.Is(err, ErrInvalid) {
		t.Error("a bad date passed")
	}
	if _, err := CleanValues(fields, map[string]any{"plate_number": nil}, vals); !errors.Is(err, ErrInvalid) {
		t.Error("a required field removed")
	}
	plain, secret, masked := SplitValues(fields, vals)
	if plain["chassis_number"] != "" || secret["chassis_number"] != "JTDBR32E720123456" || masked["chassis_number"] != "••3456" {
		t.Fatalf("split: %v %v %v", plain, secret, masked)
	}
	if h := CustomHint("Car", fields, vals, nil); h != "LND 123 XY · 3 Mar 2027" {
		t.Fatalf("hint: %q", h)
	}
	c := MakeCustomCard("Car", "car", "My car", fields, vals, nil)
	if c.Kind != KindCustom || c.KindName != "Car" || len(c.Lines) != 4 || c.Sensitive || c.Lines[3].Value != "₦4,500,000" {
		t.Fatalf("card: %+v", c)
	}
	if err := c.Check(); err != nil {
		t.Fatal(err)
	}
	for _, st := range Starters {
		if _, _, err := CleanKind(st.Name, st.Fields); err != nil {
			t.Errorf("starter %s: %v", st.Name, err)
		}
	}
}

func TestCustomFieldsPlaceBankFile(t *testing.T) {
	_, fields, err := CleanKind("Supplier", []Field{
		{Label: "Warehouse", Type: "place"},
		{Label: "Account", Type: "bank_account"},
		{Label: "Price list", Type: "file", Sensitive: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !fields[1].Sensitive || fields[2].Sensitive {
		t.Fatalf("sensitive: an account always, a file never: %+v", fields)
	}
	vals, err := CleanValues(fields, map[string]any{"warehouse": " 12  Herbert Macaulay Way, Yaba ", "account": "Zenith Bank 0123 456 789", "price_list": "01a1f840-0000-7000-8000-000000000001"}, nil)
	if err != nil || vals["warehouse"] != "12 Herbert Macaulay Way, Yaba" || vals["account"] != "Zenith Bank · 0123456789" {
		t.Fatal(vals, err)
	}
	for _, bad := range []map[string]any{{"account": "0123456789"}, {"account": "Zenith 12345"}, {"price_list": "menu.pdf"}} {
		if _, err := CleanValues(fields, bad, nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("%v passed", bad)
		}
	}
	_, _, masked := SplitValues(fields, vals)
	if masked["account"] != "Zenith Bank ••6789" {
		t.Fatalf("masked: %v", masked)
	}
	files := map[string]FileMeta{"price_list": {Name: "prices.pdf", Type: "application/pdf", Size: 245760}}
	if h := CustomHint("Supplier", fields, vals, files); h != "12 Herbert Macaulay Way, Yaba · Zenith Bank ••6789" {
		t.Fatalf("hint: %q", h)
	}
	c := MakeCustomCard("Supplier", "dangote", "Dangote", fields, vals, files)
	if len(c.Lines) != 3 || c.Lines[1].Value != "Zenith Bank · 0123456789" || c.Lines[2].Value != "prices.pdf" || !c.Sensitive {
		t.Fatalf("card: %+v", c)
	}
	if h := files["price_list"].Hint(); h != "PDF · 240 KB" {
		t.Fatalf("file hint: %q", h)
	}
	if _, err := Clean(KindFile, map[string]any{"name": "menu.pdf"}); !errors.Is(err, ErrInvalid) {
		t.Error("a file kind with no file passed")
	}
}

// A field's key made from its label never takes a key given for a field
// kept from before, wherever that field is in the list.
func TestCleanKindKeysAvoidGiven(t *testing.T) {
	_, fs, err := CleanKind("Car", []Field{{Label: "Plate", Type: "text"}, {Key: "plate", Label: "Old plate", Type: "text"}})
	if err != nil || fs[0].Key == "plate" || fs[1].Key != "plate" {
		t.Fatalf("keys: %+v %v", fs, err)
	}
	if _, _, err := CleanKind("Car", []Field{{Key: "a", Label: "A", Type: "text"}, {Key: "a", Label: "B", Type: "text"}}); err == nil {
		t.Fatal("a key given twice was taken")
	}
}

func TestCustomValueParsing(t *testing.T) {
	num := Field{Key: "n", Label: "Seats", Type: "number"}
	for _, v := range []string{"Inf", "NaN", "-Inf", "infinity", "0x10", "1e400"} {
		if _, err := cleanValue(num, v); err == nil {
			t.Errorf("number %q taken", v)
		}
	}
	if v, err := cleanValue(num, "1,200.5"); err != nil || v != "1200.5" {
		t.Errorf("1,200.5: %q %v", v, err)
	}
	money := Field{Key: "m", Label: "Rent", Type: "money"}
	for in, want := range map[string]string{"₦ 25,000": "25000", "N25000": "25000", "n 25,000.50": "25000.50", "NGN 25,000": "25000", "ngn25000": "25000", "25,000 naira": "25000", "25000": "25000"} {
		if v, err := cleanValue(money, in); err != nil || v != want {
			t.Errorf("money %q: %q %v", in, v, err)
		}
	}
	for _, in := range []string{"Naira", "N", "twenty", "25.123", "$25"} {
		if _, err := cleanValue(money, in); err == nil {
			t.Errorf("money %q taken", in)
		}
	}
}

// One upload is in one field only; a field gone takes its value with it,
// and a value no longer good for its field's type is let go.
func TestCustomValuesKeptAndLetGo(t *testing.T) {
	id := "0190f3c2-7a3b-7c1d-8e2f-1a2b3c4d5e6f"
	files := []Field{{Key: "front", Label: "Front", Type: "file"}, {Key: "back", Label: "Back", Type: "file"}}
	if _, err := CleanValues(files, map[string]any{"front": id, "back": id}, nil); err == nil {
		t.Fatal("one file in two fields")
	}
	was := map[string]string{"plate": "LAG 123", "vin": "SECRET", "made": "2020-01-02", "colour": "red"}
	now := []Field{{Key: "plate", Label: "Plate", Type: "text"}, {Key: "made", Label: "Made", Type: "date"}, {Key: "colour", Label: "Colour", Type: "date"}}
	got := KeepFields(now, was)
	if len(got) != 2 || got["plate"] != "LAG 123" || got["made"] != "2020-01-02" {
		t.Fatalf("kept: %v", got)
	}
	plain, secret, _ := SplitValues(now, was)
	if _, ok := plain["vin"]; ok || len(secret) != 0 {
		t.Fatalf("a removed field's value kept: %v %v", plain, secret)
	}
}

func TestDesensitised(t *testing.T) {
	was := []Field{
		{Key: "vin", Label: "VIN", Type: "text", Sensitive: true},
		{Key: "chip", Label: "Chip", Type: "text", Sensitive: true, NeverShare: true},
		{Key: "acct", Label: "Account", Type: "bank_account", Sensitive: true},
		{Key: "plate", Label: "Plate", Type: "text"},
		{Key: "pin", Label: "PIN", Type: "text", Sensitive: true},
	}
	now := []Field{
		{Key: "vin", Label: "VIN", Type: "text"},                       // sensitive off
		{Key: "chip", Label: "Chip", Type: "text", Sensitive: true},    // shareable now
		{Key: "acct", Label: "Account", Type: "text", Sensitive: true}, // another type
		{Key: "plate", Label: "Plate", Type: "text", Sensitive: true},  // made sensitive: fine
		{Key: "pin", Label: "PIN code", Type: "text", Sensitive: true}, // relabelled: fine
	}
	got := Desensitised(was, now)
	if len(got) != 3 || got["vin"] != "VIN" || got["chip"] != "Chip" || got["acct"] != "Account" {
		t.Fatalf("desensitised: %v", got)
	}
}

func TestNote(t *testing.T) {
	if n, err := Note("  pays   for\n rice "); err != nil || n != "pays for rice" {
		t.Fatalf("note: %q %v", n, err)
	}
	if _, err := Note(strings.Repeat("x", MaxNote+1)); err == nil {
		t.Fatal("a long note taken")
	}
}
