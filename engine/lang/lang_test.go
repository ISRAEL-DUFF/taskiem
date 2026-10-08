package lang_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/israel-duff/taskiem/engine/lang"
)

// Every message English has must be in every catalogue file, with the same
// placeholders; a missing one would fall back to English at run time and
// be reported, but a shipped catalogue is complete.
func TestEveryEnglishMessageIsInEveryCatalogue(t *testing.T) {
	c, err := lang.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range lang.All() {
		if c.File(info.Tag) == nil {
			t.Errorf("no catalogue for %s", info.Tag)
		}
	}
	for _, p := range c.Check() {
		t.Errorf("%s", p)
	}
	if len(c.IDs()) < 100 {
		t.Errorf("only %d messages", len(c.IDs()))
	}
}

func TestMissingMessageFallsBackToEnglishAndIsReported(t *testing.T) {
	en := &lang.File{Language: lang.EN, Reviewed: true, Messages: map[string]lang.Entry{
		"a.hello": {Text: "Hello {name}.", Reviewed: true}, "a.bye": {Text: "Goodbye.", Reviewed: true}}}
	yo := &lang.File{Language: lang.YO, Messages: map[string]lang.Entry{"a.hello": {Text: "Ẹ n lẹ́ {name}."}, "a.extra": {Text: "x"}}}
	c, err := lang.FromFiles(en, yo)
	if err != nil {
		t.Fatal(err)
	}
	var reported []string
	c.SetOnMissing(func(tag lang.Tag, id string) { reported = append(reported, string(tag)+"/"+id) })
	if got := c.Text(lang.YO, "a.hello", "name", "Ade"); got != "Ẹ n lẹ́ Ade." {
		t.Errorf("translated: %q", got)
	}
	for range 3 {
		if got := c.Text(lang.YO, "a.bye"); got != "Goodbye." {
			t.Errorf("fallback: %q", got)
		}
	}
	if !slices.Equal(reported, []string{"yo/a.bye"}) || !slices.Equal(c.Missing(), []string{"yo/a.bye"}) {
		t.Errorf("reported once: %v %v", reported, c.Missing())
	}
	// A language with no file at all falls back too.
	if got := c.Text(lang.HA, "a.bye"); got != "Goodbye." {
		t.Errorf("no file: %q", got)
	}
	// An id English lacks shows as itself.
	if got := c.Text(lang.YO, "a.nope"); got != "a.nope" {
		t.Errorf("unknown id: %q", got)
	}
	probs := map[string]bool{}
	for _, p := range c.Check() {
		probs[string(p.Language)+"/"+p.ID+"/"+p.Kind] = true
	}
	for _, want := range []string{"yo/a.bye/missing", "yo/a.extra/extra", "pcm/*/missing"} {
		if !probs[want] {
			t.Errorf("Check missed %s: %v", want, probs)
		}
	}
	yo.Messages["a.hello"] = lang.Entry{Text: "Ẹ n lẹ́ {orukọ}."}
	found := false
	for _, p := range c.Check() {
		found = found || p.ID == "a.hello" && p.Kind == "placeholders"
	}
	if !found {
		t.Error("a changed placeholder was not caught")
	}
}

// Drafts are drafts: a file or an entry is reviewed only by a named native
// speaker (docs/languages.md), and nothing outside English claims review
// by default.
func TestDraftsAreMarkedUnreviewed(t *testing.T) {
	c := lang.Default()
	words, err := lang.LoadWords()
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range lang.All() {
		if info.Tag == lang.EN {
			continue
		}
		f := c.File(info.Tag)
		anyReviewed := f.Reviewed
		for id, e := range f.Messages {
			if e.Reviewed {
				anyReviewed = true
			}
			if f.Reviewed && !e.Reviewed {
				t.Errorf("%s is marked reviewed but %s is not", info.Tag, id)
			}
		}
		if anyReviewed && len(f.Reviewers) == 0 {
			t.Errorf("%s: entries marked reviewed with no reviewer named", info.Tag)
		}
		if !f.Reviewed && !strings.Contains(f.Status, "DRAFT") {
			t.Errorf("%s: an unreviewed catalogue must say DRAFT in its status", info.Tag)
		}
		if w := words[info.Tag]; w == nil || !w.Reviewed && !strings.Contains(w.Status, "DRAFT") {
			t.Errorf("%s: word lists missing or not marked as a draft", info.Tag)
		}
		if c.Reviewed(info.Tag, "lang.switched") != (f.Reviewed || f.Messages["lang.switched"].Reviewed) {
			t.Errorf("%s: Reviewed disagrees with the file", info.Tag)
		}
	}
}

// Reply buttons hold 20 characters; USSD screens and SMS carry ASCII, and
// Taskiem's own texts there must fit a 160-character screen.
func TestChannelLimits(t *testing.T) {
	c := lang.Default()
	for _, info := range lang.All() {
		for _, id := range c.IDs() {
			text := c.Text(info.Tag, id)
			switch {
			case strings.HasPrefix(id, "button."):
				if n := utf8.RuneCountInString(text); n > 20 {
					t.Errorf("%s %s: %d characters (max 20): %q", info.Tag, id, n, text)
				}
			case strings.HasPrefix(id, "ussd.") || strings.HasPrefix(id, "sms."):
				a := lang.ASCII(strings.ReplaceAll(text, "{reference}", "ABCDEF12"))
				if len(a) > 160 {
					t.Errorf("%s %s: %d characters on a screen: %q", info.Tag, id, len(a), a)
				}
				for _, r := range a {
					if r < 0x20 || r > 0x7e {
						t.Errorf("%s %s: %q is not plain ASCII", info.Tag, id, a)
						break
					}
				}
			}
		}
	}
}

// Every message id the API sends exists in English.
func TestEveryIDTheAPIUsesExists(t *testing.T) {
	c := lang.Default()
	files, _ := filepath.Glob("../../api/*.go")
	if len(files) < 10 {
		t.Fatalf("api sources not found: %v", files)
	}
	idRe := regexp.MustCompile(`"([a-z]+\.[a-z_.]*[a-z_])"`)
	use := regexp.MustCompile(`\bc\.t\(|\btr\(|ussdMsg \+ "`)
	seen := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if !use.MatchString(line) {
				continue
			}
			for _, m := range idRe.FindAllStringSubmatch(line, -1) {
				id := m[1]
				if strings.HasSuffix(id, "_") || strings.Count(id, ".") == 0 { // a prefix completed at run time
					continue
				}
				seen++
				if !c.Has(id) {
					t.Errorf("%s:%d: message %q is not in en.json", filepath.Base(f), i+1, id)
				}
			}
		}
	}
	// Ids built at run time.
	for _, id := range []string{"wa.handoff.approve", "wa.handoff.reject", "wa.form.pin_approve", "wa.form.pin_reject",
		"run.status.completed", "run.status.failed", "run.status.needs_reconciliation", "run.status.running", "run.status.waiting",
		"run.status.queued", "run.status.cancelled", "approval.status.open", "approval.status.approved", "approval.status.rejected",
		"approval.status.expired", "approval.status.cancelled"} {
		if !c.Has(id) {
			t.Errorf("%s is not in en.json", id)
		}
	}
	if seen < 100 {
		t.Errorf("only %d uses found; the scan is broken", seen)
	}
}

func TestFoldAndASCII(t *testing.T) {
	cases := map[string]string{"Ẹ jọ̀wọ́!": "e jowo", "  KÍ   ni?? ": "ki ni", "Ƙi ɗaya": "ki daya", "Gịnị dara": "gini dara", "na’am": "na'am"}
	for in, want := range cases {
		if got := lang.Fold(in); got != want {
			t.Errorf("Fold(%q) = %q, want %q", in, got, want)
		}
	}
	if got := lang.ASCII("Ìbéèrè Ƙi ₦500 • Ọ dị mma"); got != "Ibeere Ki N500 - O di mma" {
		t.Errorf("ASCII: %q", got)
	}
}

func TestParseListAndFind(t *testing.T) {
	got, err := lang.ParseList(" yo, PCM ,yo")
	if err != nil || !slices.Equal(got, []lang.Tag{lang.EN, lang.YO, lang.PCM}) {
		t.Fatalf("%v %v", got, err)
	}
	if got, _ := lang.ParseList(""); !slices.Equal(got, []lang.Tag{lang.EN}) {
		t.Fatalf("empty: %v", got)
	}
	if _, err := lang.ParseList("yo,fr"); err == nil {
		t.Fatal("fr accepted")
	}
	for name, want := range map[string]lang.Tag{"Yorùbá": lang.YO, "yoruba": lang.YO, "pidgin": lang.PCM, "Naija": lang.PCM, "hausa": lang.HA, "IG": lang.IG, "english": lang.EN} {
		if i, ok := lang.Find(name); !ok || i.Tag != want {
			t.Errorf("Find(%q) = %v %v", name, i.Tag, ok)
		}
	}
	if _, ok := lang.Find("klingon"); ok {
		t.Error("klingon found")
	}
}

func TestMatchIntent(t *testing.T) {
	all := []lang.Tag{lang.EN}
	cases := []struct {
		text string
		tags []lang.Tag
		want lang.Intent
		arg  string
	}{
		// English is unchanged from the commands in docs/whatsapp.md.
		{"Help", all, lang.IntentHelp, ""},
		{"What failed today?", all, lang.IntentStatus, ""},
		{"approvals", all, lang.IntentApprovals, ""},
		{"stop", all, lang.IntentCancel, ""},
		{"Yes", all, lang.IntentYes, ""},
		{"run Payroll Run", all, lang.IntentRun, "Payroll Run"},
		{"start", all, lang.IntentHelp, ""},
		{"start salary reminders", all, lang.IntentRun, "salary reminders"},
		{"switch acme", all, lang.IntentSwitch, "acme"},
		{"organisations", all, lang.IntentSwitch, ""},
		{"language yoruba", all, lang.IntentLanguage, "yoruba"},
		// The person's language first, English still understood.
		{"abeg run payroll", []lang.Tag{lang.PCM, lang.EN}, lang.IntentRun, "payroll"},
		{"wetin fail today", []lang.Tag{lang.PCM, lang.EN}, lang.IntentStatus, ""},
		{"status", []lang.Tag{lang.PCM, lang.EN}, lang.IntentStatus, ""},
		{"Fagilé", []lang.Tag{lang.YO, lang.EN}, lang.IntentCancel, ""},
		{"bẹ́ẹ̀ni", []lang.Tag{lang.YO, lang.EN}, lang.IntentYes, ""},
		{"beeni", []lang.Tag{lang.YO, lang.EN}, lang.IntentYes, ""}, // typed without accents
		{"fara biyan albashi", []lang.Tag{lang.HA, lang.EN}, lang.IntentRun, "biyan albashi"},
		{"a'a", []lang.Tag{lang.HA, lang.EN}, lang.IntentNo, ""},
		{"kagbuo", []lang.Tag{lang.IG, lang.EN}, lang.IntentCancel, ""},
		{"asụsụ hausa", []lang.Tag{lang.IG, lang.EN}, lang.IntentLanguage, "hausa"},
		// Another language's words mean nothing unless it is that person's.
		{"fagilé", all, lang.IntentNone, ""},
		{"please pay salaries every friday", all, lang.IntentNone, ""},
	}
	for _, c := range cases {
		m, ok := lang.MatchIntent(c.text, c.tags)
		if m.Intent != c.want || ok != (c.want != lang.IntentNone) || m.Arg != c.arg {
			t.Errorf("%q in %v: %+v %v, want %s %q", c.text, c.tags, m, ok, c.want, c.arg)
		}
	}
}

// A word that means one thing in English must not mean another in a
// draft list (people mix English in).
func TestWordListsDoNotContradictEnglish(t *testing.T) {
	words, err := lang.LoadWords()
	if err != nil {
		t.Fatal(err)
	}
	for tag, w := range words {
		if tag == lang.EN {
			continue
		}
		for intent, ps := range w.Phrases {
			for _, p := range ps {
				if m, ok := lang.MatchIntent(p, []lang.Tag{lang.EN}); ok && m.Intent != intent {
					t.Errorf("%s: %q is %s, but %s in English", tag, p, intent, m.Intent)
				}
			}
		}
	}
}

func TestDetect(t *testing.T) {
	on := []lang.Tag{lang.EN, lang.PCM, lang.YO, lang.HA, lang.IG}
	cases := map[string]lang.Tag{
		"Abeg wetin dey happen for my account?":           lang.PCM,
		"Ẹ jọ̀wọ́, kí ni mo lè ṣe lónìí?":                 lang.YO,
		"Sannu, don Allah ina son taimako":                lang.HA,
		"Biko, kedu ihe m nwere ike ime taa?":             lang.IG,
		"Please send salary reminders to staff on Friday": lang.EN,
		"yes":  lang.EN,
		"ok":   lang.EN,
		"help": lang.EN,
	}
	for text, want := range cases {
		if got, _ := lang.Detect(text, on); got != want {
			t.Errorf("Detect(%q) = %s, want %s", text, got, want)
		}
	}
	// Only languages that are on are detected.
	if got, ok := lang.Detect("Abeg wetin dey happen for my account?", []lang.Tag{lang.EN, lang.YO}); ok || got != lang.EN {
		t.Errorf("detected a language that is off: %s", got)
	}
}
