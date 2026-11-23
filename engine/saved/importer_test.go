package saved_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/saved"
)

func TestReadImport(t *testing.T) {
	file := "\uFEFFTitle,Name,Bank,Account Number,Phone,Street,Area,Kind\n" +
		"Dangote Cement,,GTBank,0123456789,,,,\n" +
		"Bola Stores,bola,,,0803 123 4567,,,\n" +
		"Apapa warehouse,warehouse,,,,4 Creek Road,Apapa,\n" +
		",,,,,,,\n" +
		"Ikeja branch,,,,,12 Allen Avenue,Ikeja,branch\n"
	rows, err := saved.ReadImport(file)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, r := range rows {
		got = append(got, r.Name+"/"+r.Kind)
	}
	if strings.Join(got, " ") != "dangote-cement/bank_account bola/person warehouse/place ikeja-branch/place" {
		t.Fatalf("rows: %v", got)
	}
	if rows[0].Account.AccountNumber != "0123456789" || rows[0].Hint != "GTBank ••6789" || rows[0].Line != 2 || rows[3].Line != 6 {
		t.Fatalf("an account: %+v", rows[0])
	}
	if rows[2].Details["line"] != "4 Creek Road" || rows[2].Details["area"] != "Apapa" {
		t.Fatalf("a place: %+v", rows[2].Details)
	}

	// Any line that cannot be taken: nothing, each line and why.
	_, err = saved.ReadImport("title,account_number,bank,name\nAda Bank,123,GTBank,\nBola,,,\nChi Stores,0123456780,Access,chi\nDayo,0123456789,,\n")
	var ie *saved.ImportError
	if !errors.As(err, &ie) || len(ie.Problems) != 3 || ie.Total != 4 {
		t.Fatalf("problems: %d %+v", ie.Total, ie.Problems)
	}
	if ie.Problems[0].Line != 2 || ie.Problems[1].Line != 3 || !strings.Contains(ie.Problems[1].Message, "kind column") ||
		ie.Problems[2].Line != 5 || ie.Problems[2].Message != "Choose the bank." {
		t.Fatalf("problems: %+v", ie.Problems)
	}
	_, err = saved.ReadImport("title,phone\nAda,08031234567\nAda,08031234568\n")
	if !errors.As(err, &ie) || len(ie.Problems) != 1 || !strings.Contains(ie.Problems[0].Message, "@ada is on line 2 too") {
		t.Fatalf("a name twice: %v", err)
	}
	for _, bad := range []string{"", "colour,size\nred,10\n", "title\n"} {
		if _, err := saved.ReadImport(bad); !errors.As(err, &ie) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if _, err := saved.ReadImport("title,phone\n" + strings.Repeat("x,\n", 1) + strings.Repeat("Ada,0803\n", saved.MaxImportRows+1)); !errors.As(err, &ie) {
		t.Errorf("too many: %v", err)
	}
	if n := saved.NameFor("Dangote Cement (Apapa) Nigeria Limited Ltd"); n != "dangote-cement-apapa-nigeria-lim" {
		t.Errorf("NameFor: %q", n)
	}
}

// Files as spreadsheets save them: lines named as the file numbers them,
// Excel's byte-order mark, semicolons and tabs, column names as people
// write them, a lone name column of real names, branches, numbers and a
// type column.
func TestReadImportFiles(t *testing.T) {
	one := func(t *testing.T, file string) []saved.ImportRow {
		t.Helper()
		rows, err := saved.ReadImport(file)
		if err != nil {
			var ie *saved.ImportError
			errors.As(err, &ie)
			t.Fatalf("%v %+v", err, ie)
		}
		return rows
	}
	// Blank lines and a cell over two lines: each problem names the line
	// the file shows it on.
	_, err := saved.ReadImport("title,phone,note\n\nAda,0803 123 4567,\"rice,\nbeans\"\nBola,08,\n\nChi,0803 123 4568,\nDayo,,\n")
	var ie *saved.ImportError
	if !errors.As(err, &ie) || len(ie.Problems) != 2 || ie.Problems[0].Line != 5 || ie.Problems[1].Line != 8 {
		t.Fatalf("lines: %+v", ie)
	}
	rows := one(t, "title,phone,note\n\nAda,0803 123 4567,\"rice,\nbeans\"\nChi,0803 123 4568,\n")
	if rows[0].Line != 3 || rows[1].Line != 5 {
		t.Fatalf("lines: %+v", rows)
	}

	// Excel's mark before a quoted first column name.
	rows = one(t, "\uFEFF\"Title\",\"Phone\"\n\"Ada\",\"0803 123 4567\"\n")
	if rows[0].Name != "ada" || rows[0].Kind != saved.KindPerson {
		t.Fatalf("BOM: %+v", rows)
	}

	// Semicolons, and tabs.
	rows = one(t, "Title;Bank;Account No.;Phone #\n\"Dangote; Apapa\";Guaranty Trust Bank;0123456789;\nBola;;;0803 123 4567\n")
	if len(rows) != 2 || rows[0].Title != "Dangote; Apapa" || rows[0].Account.AccountNumber != "0123456789" || rows[0].Hint != "GTBank ••6789" || rows[1].Kind != saved.KindPerson {
		t.Fatalf("semicolons: %+v", rows)
	}
	rows = one(t, "Title\tE-mail\tPhone\nAda\tada@shop.ng\t\nBola\t\t0803 123 4567\n")
	if len(rows) != 2 || rows[0].Details["email"] != "ada@shop.ng" || rows[1].Details["phone"] == nil {
		t.Fatalf("tabs: %+v", rows)
	}

	// A lone name column holding names as people write them.
	rows = one(t, "Name,Phone\nMrs. Ade,0803 123 4567\nMama Ngozi & Sons,0803 123 4568\nOlumide Adebayo Ogunlesi Enterprises Nig,0803 123 4569\n")
	if rows[0].Title != "Mrs. Ade" || rows[0].Name != "mrs-ade" || rows[1].Name != "mama-ngozi-sons" || rows[2].Name != "olumide-adebayo-ogunlesi-enterpr" {
		t.Fatalf("lone name: %+v", rows)
	}
	// Beside a title, a name column is the @name, by its rule.
	if _, err := saved.ReadImport("Title,Name,Phone\nMrs. Ade,Mrs. Ade,0803 123 4567\n"); !errors.As(err, &ie) {
		t.Fatalf("a name beside a title: %v", err)
	}

	// A file of branches: places, and one with a phone and no kind a
	// person; the kind column makes a place of one with a phone.
	rows = one(t, "Branch,Address,State,Phone,Kind\nIkeja,12 Allen Avenue,Lagos,,\nWuse,3 Aminu Kano Crescent,Abuja,,\nTolu,5 Bode Street,Ibadan,0803 123 4567,\nLekki,1 Admiralty Way,Lagos,0803 123 4568,branch\n")
	got := []string{}
	for _, r := range rows {
		got = append(got, r.Name+"/"+r.Kind)
	}
	if strings.Join(got, " ") != "ikeja/place wuse/place tolu/person lekki/place" ||
		rows[0].Details["line"] != "12 Allen Avenue" || rows[0].Details["area"] != "Lagos" || rows[0].Hint != "Lagos" ||
		rows[2].Details["address"] != "5 Bode Street" || rows[3].Details["phone"] == nil {
		t.Fatalf("branches: %v %+v", got, rows)
	}

	// A number column: a phone on a person's or a place's line, a provider
	// account only with a provider.
	rows = one(t, "Title,Number,Street,Provider,Type\nAda,0803 123 4567,,,\nShop,0803 123 4568,4 Creek Road,,\nMeter,45012345678,,Ikeja Electric,electricity\n")
	if rows[0].Kind != saved.KindPerson || rows[0].Details["phone"] == nil || rows[1].Kind != saved.KindPlace || rows[1].Details["phone"] == nil ||
		rows[2].Kind != saved.KindProviderAccount || rows[2].Details["number"] != "45012345678" {
		t.Fatalf("numbers: %+v", rows)
	}
	if _, err := saved.ReadImport("Title,Number\nInvoice,45012345678\n"); !errors.As(err, &ie) || !strings.Contains(ie.Problems[0].Message, "kind column") {
		t.Fatalf("a number alone: %v", err)
	}

	// A type column of kind words.
	rows = one(t, "Title,Type,Phone,Street\nBola Stores,Supplier,0803 123 4567,\nApapa,Branch,,4 Creek Road\n")
	if rows[0].Kind != saved.KindPerson || rows[1].Kind != saved.KindPlace {
		t.Fatalf("type: %+v", rows)
	}
}

// NameFor takes accents off as the clients do.
func TestNameForAccents(t *testing.T) {
	for in, want := range map[string]string{
		"Ọlá Stores":         "ola-stores",
		"Ìfẹ́":               "ife",
		"Ṣadé & Ńkẹ́ Foods":  "sade-nke-foods",
		"Ụ̀chẹ́ Ịfẹ́ànyí":    "uche-ifeanyi",
		"Café Crème":         "cafe-creme",
		"  --Bola--  ":       "bola",
		"Ọ":                  "",
		"Hausa ƙasa ɗan ɓa":  "hausa-asa-an-a",
		"Mrs. Ade (Ikeja) 2": "mrs-ade-ikeja-2",
	} {
		if got := saved.NameFor(in); got != want {
			t.Errorf("NameFor(%q) = %q, want %q", in, got, want)
		}
	}
}
