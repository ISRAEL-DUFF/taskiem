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
