package api

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/lang"
)

// The other languages' invoice words (draft lists in engine/lang), as
// waCommand reads them: the person's language, then English.
func TestInvoiceWordsPerLanguage(t *testing.T) {
	for _, c := range []struct {
		tag  lang.Tag
		in   string
		name string
		kobo int64
		what string
		ok   bool
	}{
		{lang.PCM, "send invoice give Ada 25,000 for 5 bags of rice", "Ada", 2500000, "5 bags of rice", true},
		{lang.PCM, "abeg send bill give Chi 10k", "Chi", 1000000, "", true},
		{lang.YO, "ìwé owó Ada 25,000 fún àpò ìrẹsì 5", "Ada", 2500000, "àpò ìrẹsì 5", true},
		{lang.YO, "IWE OWO Bola Ade 5000 fun iresi", "Bola Ade", 500000, "iresi", true}, // no accents, any case
		{lang.YO, "fi ìwé owó ránṣẹ́ sí Ada 2500 for rice", "Ada", 250000, "rice", true},
		{lang.HA, "takardar kuɗi Musa 25,000 don buhun shinkafa 5", "Musa", 2500000, "buhun shinkafa 5", true},
		{lang.HA, "don allah aika takardar kudi ga Musa 3k", "Musa", 300000, "", true},
		{lang.IG, "akwụkwọ ụgwọ Ada 25,000 maka akpa osikapa 5", "Ada", 2500000, "akpa osikapa 5", true},
		{lang.IG, "biko ziga akwukwo ugwo nye Chidi 7500", "Chidi", 750000, "", true},
		{lang.IG, "akwụkwọ ụgwọ Ada", "", 0, "", false},
	} {
		m, matched := lang.MatchIntent(c.in, []lang.Tag{c.tag, lang.EN})
		if !matched || m.Intent != lang.IntentInvoice || m.Language != c.tag {
			t.Errorf("%s %q: matched %+v %v", c.tag, c.in, m, matched)
			continue
		}
		name, kobo, what, ok := parseInvoiceArgs(strings.Fields(m.Arg), lang.ForWords(m.Language))
		if ok != c.ok || name != c.name || kobo != c.kobo || what != c.what {
			t.Errorf("%s %q: %q %d %q %v", c.tag, c.in, name, kobo, what, ok)
		}
	}
	// Another language's words are not read for someone in English, and
	// English's stay with the English parser in every language.
	if m, ok := lang.MatchIntent("ìwé owó Ada 25000", []lang.Tag{lang.EN}); ok {
		t.Errorf("Yoruba words matched in English: %+v", m)
	}
	for _, tag := range []lang.Tag{lang.PCM, lang.YO, lang.HA, lang.IG} {
		for _, en := range []string{"invoice Ada 25000", "bill Ada 25000", "spent 500 on fuel", "expense 500 fuel", "cash in 500", "cash out 500"} {
			if m, ok := lang.MatchIntent(en, []lang.Tag{tag, lang.EN}); ok {
				t.Errorf("%s: English %q matched a word list: %+v", tag, en, m)
			}
		}
	}
}

func TestParseInvoiceCommand(t *testing.T) {
	for _, c := range []struct {
		in   string
		name string
		kobo int64
		what string
		ok   bool
	}{
		{"invoice Ada 25,000 for 5 bags of rice", "Ada", 2500000, "5 bags of rice", true},
		{"Invoice Bola Ade ₦25,000.50 for rice", "Bola Ade", 2500050, "rice", true},
		{"bill Chi 10k", "Chi", 1000000, "", true},
		{"invoice Chi N2500.5", "Chi", 250050, "", true},
		{"invoice Chi ngn1,250", "Chi", 125000, "", true},
		{"invoice Ada", "", 0, "", false},
		{"invoice 25000", "", 0, "", false},
		{"invoice Ada lots for rice", "", 0, "", false},
		{"invoice Ada 0.50", "", 0, "", false},
		{"invoices", "", 0, "", false},
		{"pay Ada 25000", "", 0, "", false},
	} {
		name, kobo, what, ok := parseInvoiceCommand(c.in)
		if ok != c.ok || name != c.name || kobo != c.kobo || what != c.what {
			t.Errorf("%q: %q %d %q %v", c.in, name, kobo, what, ok)
		}
	}
}
