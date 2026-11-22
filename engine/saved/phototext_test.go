package saved

import "testing"

func TestPhotoText(t *testing.T) {
	for in, want := range map[string]string{
		"GTBank O123456789":              "GTBank 0123456789",
		"2O3456789l UBA":                 "2034567891 UBA",
		"Acct: 0l23 456 789":             "Acct: 0123 456 789",
		"Chidi Okeke 0123456789 GTB 45k": "Chidi Okeke 0123456789 GTB 45k",
		"Bola Ade\nOpay 8O31234567 5k":   "Bola Ade\nOpay 8031234567 5k",
		// Words stay words; a name with a letter like a digit is not touched.
		"BOSS Lagos":     "BOSS Lagos",
		"Total: N45,000": "Total: N45,000",
		// Mostly letters: left alone.
		"SOIL 12": "SOIL 12",
	} {
		if got := PhotoText(in); got != want {
			t.Errorf("PhotoText(%q) = %q, want %q", in, got, want)
		}
	}
	// Read after: the account and its bank.
	if r := Read(PhotoText("Access Bank\nO69O123456\nADA OBI")); r.Details["account_number"] != "0690123456" || r.Details["bank_name"] != "Access Bank" {
		t.Errorf("read: %+v", r)
	}
}
