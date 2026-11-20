package saved

import (
	"errors"
	"reflect"
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
