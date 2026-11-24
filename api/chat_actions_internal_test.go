package api

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/israel-duff/taskiem/engine/lang"
)

// The catalogue holds together (docs/spec/chat-menus.md §3): ids unique,
// each under a topic; titles and lines within WhatsApp's list limits; what
// it does and opens known; one that asks has its questions and makes a
// whole command from their answers; a page a path; a topic short enough
// for its menu (§6.1).
func TestChatCatalogue(t *testing.T) {
	ids := map[string]bool{}
	per := map[string]int{}
	for _, a := range chatActions {
		if ids[a.ID] {
			t.Errorf("%s twice", a.ID)
		}
		ids[a.ID] = true
		if _, ok := chatTopicByID(a.Topic); !ok || !strings.HasPrefix(a.ID, a.Topic+".") {
			t.Errorf("%s: topic %q", a.ID, a.Topic)
		}
		per[a.Topic]++
		if n := utf8.RuneCountInString(a.Title); n == 0 || n > 30 {
			t.Errorf("%s: title of %d characters", a.ID, n)
		}
		if n := utf8.RuneCountInString(a.Line); n == 0 || n > 72 {
			t.Errorf("%s: line of %d characters", a.ID, n)
		}
		switch a.Does {
		case doesShow, doesAsks, doesChanges:
		default:
			t.Errorf("%s: does %q", a.ID, a.Does)
		}
		if a.may == nil || a.Icon == "" {
			t.Errorf("%s: no one may, or no icon", a.ID)
		}
		switch a.Opens {
		case opensShow:
			if a.Shortcut == "" {
				t.Errorf("%s: shows nothing", a.ID)
			}
		case opensPage:
			if !strings.HasPrefix(a.Page, "/") {
				t.Errorf("%s: page %q", a.ID, a.Page)
			}
		case opensForm:
			if a.Tool == "" {
				t.Errorf("%s: a form with no tool", a.ID)
			}
		case opensQuestions:
			if a.Make == nil {
				t.Errorf("%s: asks, and makes nothing", a.ID)
			}
		default:
			t.Errorf("%s: opens %q", a.ID, a.Opens)
		}
		if a.Make != nil {
			v := map[string]string{}
			for _, fl := range a.Fields {
				v[fl.Key] = "x" + fl.Key
				if fl.Kind == "choice" && len(fl.Choices) == 0 {
					t.Errorf("%s.%s: a choice of nothing", a.ID, fl.Key)
				}
			}
			cmd := a.Make(v)
			if cmd == "" || strings.ContainsAny(cmd, "{}[]") {
				t.Errorf("%s: makes %q", a.ID, cmd)
			}
			for _, fl := range a.Fields {
				if !strings.Contains(cmd, "x"+fl.Key) {
					t.Errorf("%s: %s is asked and not used: %q", a.ID, fl.Key, cmd)
				}
			}
		}
	}
	for _, tp := range chatTopics {
		if per[tp.ID] == 0 || per[tp.ID] > 7 {
			t.Errorf("topic %s has %d actions", tp.ID, per[tp.ID])
		}
	}
	// Optional values left out leave no gap.
	if got := fill("add rider {name} {phone}[ {areas}]")(map[string]string{"name": "Musa", "phone": "0803"}); got != "add rider Musa 0803" {
		t.Errorf("fill: %q", got)
	}
}

// catalogueWords are the catalogue's words by message id, in English.
func catalogueWords() map[string]string {
	out := map[string]string{}
	for _, t := range chatTopics {
		out[topicWordID(t)] = t.Name
	}
	for _, a := range chatActions {
		out[titleWordID(a)] = a.Title
		out[lineWordID(a)] = a.Line
		for _, f := range a.Fields {
			out[askWordID(a, f)] = f.Ask
		}
	}
	return out
}

// The catalogue's words are in en.json as written here, for translators:
// a word changed in one place and not the other fails. With
// TASKIEM_MENU_WORDS set to a path, the words are written there as JSON
// (to add new ones to the language files).
func TestCatalogueWordsInTheLanguageFiles(t *testing.T) {
	words := catalogueWords()
	if p := os.Getenv("TASKIEM_MENU_WORDS"); p != "" {
		b, _ := json.MarshalIndent(words, "", "  ")
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for id, english := range words {
		if got := tr(lang.EN, id); got != english {
			t.Errorf("%s: en.json has %q, the catalogue %q", id, got, english)
		}
	}
}

// A form's order chosen from its chips ("TK-A1B2C3 · Ada") is taken as its
// number, as the questions take it (review fix).
func TestTakenValuesReadAnOrdersChip(t *testing.T) {
	a, _ := chatActionByID("riders.give")
	got := takenValues(a, map[string]string{"ref": "TK-A1B2C3 · Ada", "rider": "Musa"})
	if got["ref"] != "TK-A1B2C3" || got["rider"] != "Musa" {
		t.Fatalf("%v", got)
	}
}

// The topics' words are in each language's word lists (chat-menus.md
// §8.3, g-menu-words): every topic has English words, and each language
// names only the catalogue's topics, with words for every one.
func TestTopicWordsInTheWordLists(t *testing.T) {
	for _, tag := range lang.Tags() {
		words := lang.TopicWords(tag)
		for id := range words {
			if _, ok := chatTopicByID(id); !ok {
				t.Errorf("%s: words for %q, which is no topic", tag, id)
			}
		}
		for _, tp := range chatTopics {
			if len(words[tp.ID]) == 0 {
				t.Errorf("%s: no words for %s", tag, tp.ID)
			}
		}
	}
}

// Topic words (review of H4): a short word is a whole word ("obi" is not
// "obinrin", "ego" not "egosi", "iwu" not "iwuri"), with an ending in
// English ("owes", "tasks"); a longer one starts a word ("riders"); and a
// weak match in the person's language does not beat a stronger one in
// English.
func TestTopicFromWords(t *testing.T) {
	yo, ig, en := []lang.Tag{lang.YO, lang.EN}, []lang.Tag{lang.IG, lang.EN}, []lang.Tag{lang.EN}
	for _, c := range []struct {
		text string
		tags []lang.Tag
		want string // "" for none
	}{
		{"owo", yo, "money"},
		{"owó mi dà", yo, "money"},
		{"obinrin mi", yo, ""},
		{"ọmọ obìnrin náà wá", yo, ""},
		{"ego", ig, "money"},
		{"egosi ya", ig, ""},
		{"iwuri", ig, ""},
		{"oru nke a", ig, ""},
		{"ndị ọrụ m", ig, "staff"},
		{"who owes me", en, "owe"},
		{"my rider issue", en, "riders"},
		{"my tasks", en, "staff"},
		{"payment came", en, ""},
		{"cashier is late", en, ""},
		// Yoruba's àkókò (bookings) once, English's staff words three times: staff.
		{"àkókò for my staff team worker", yo, "staff"},
		// One each: a tie between the languages goes to the person's.
		{"àkókò for staff", yo, "bookings"},
	} {
		got, ok := topicFromWords(c.text, c.tags, chatTopics)
		if (c.want == "") == ok || (ok && got.ID != c.want) {
			t.Errorf("%q (%v): %q %v, want %q", c.text, c.tags, got.ID, ok, c.want)
		}
	}
}
