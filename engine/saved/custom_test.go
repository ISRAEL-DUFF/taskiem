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
	if h := CustomHint("Car", fields, vals); h != "LND 123 XY · 3 Mar 2027" {
		t.Fatalf("hint: %q", h)
	}
	c := MakeCustomCard("Car", "car", "My car", fields, vals)
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
