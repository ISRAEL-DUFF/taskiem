package peoplechats_test

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/peoplechats"
)

// The search index's tokens: a word's beginnings, keyed per person, never
// the word; a query finds a word by its beginning, without accents or
// case; another person's key gives other tokens.
func TestSearchTokens(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	ada, bola := uuid.New(), uuid.New()
	ka, kb := peoplechats.SearchKey(key, ada), peoplechats.SearchKey(key, bola)
	if bytes.Equal(ka, kb) || bytes.Equal(ka, key) {
		t.Fatal("the search key is not the person's own")
	}
	toks := peoplechats.Tokens(ka, "Ọ̀ṣun market, Saturday!")
	set := map[string]bool{}
	for _, tk := range toks {
		if len(tk) != peoplechats.TokenSize {
			t.Fatalf("token size %d", len(tk))
		}
		for _, w := range []string{"osun", "market", "saturday", "Saturday"} {
			if bytes.Contains(tk, []byte(w)) {
				t.Fatalf("a word in a token: %q", w)
			}
		}
		set[string(tk)] = true
	}
	// osun: os, osu, osun; market: 2..6; saturday: 2..8
	if len(toks) != 3+5+7 {
		t.Fatalf("tokens: %d", len(toks))
	}
	for _, q := range []string{"osun", "OSUN", "Ọ̀ṣun", "mark", "satur", "saturday", "market saturday"} {
		words, qt := peoplechats.Query(ka, q)
		if len(words) == 0 {
			t.Fatalf("%q: no words", q)
		}
		for _, tk := range qt {
			if !set[string(tk)] {
				t.Fatalf("%q: a token not indexed", q)
			}
		}
		if !peoplechats.Matches(words, "Ọ̀ṣun market, Saturday!") {
			t.Fatalf("%q does not match", q)
		}
		// Another person's key: none of the tokens.
		_, other := peoplechats.Query(kb, q)
		for _, tk := range other {
			if set[string(tk)] {
				t.Fatalf("%q: another key's token matches", q)
			}
		}
	}
	// A long word is found by its first 8 letters, then checked.
	words, qt := peoplechats.Query(ka, "saturdays")
	if len(qt) != 1 || !set[string(qt[0])] || peoplechats.Matches(words, "Ọ̀ṣun market, Saturday!") {
		t.Fatal("a longer word than the text's matched")
	}
	// One letter is not searched for; a word in the middle is not a beginning.
	if w, _ := peoplechats.Query(ka, "a"); len(w) != 0 {
		t.Fatal("a one-letter word searched")
	}
	if w, _ := peoplechats.Query(ka, "ket"); peoplechats.Matches(w, "market") {
		t.Fatal("the middle of a word matched")
	}
	// At most MaxTokens.
	if n := len(peoplechats.Tokens(ka, strings.Repeat("abcdefghij ", 10)+longText())); n > peoplechats.MaxTokens {
		t.Fatalf("%d tokens", n)
	}
}

func longText() string {
	var b strings.Builder
	for i := range 2000 {
		b.WriteString(string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)%26)) + "x ")
	}
	return b.String()
}

func TestSnippet(t *testing.T) {
	text := strings.Repeat("words before ", 20) + "the Ọ̀ṣun festival is on Saturday " + strings.Repeat("words after ", 20)
	words, _ := peoplechats.Query(peoplechats.SearchKey(make([]byte, 32), uuid.New()), "osun")
	s := peoplechats.Snippet(text, words, 60)
	if !strings.Contains(s, "Ọ̀ṣun") || !strings.HasPrefix(s, "…") || !strings.HasSuffix(s, "…") || len([]rune(s)) > 62 {
		t.Fatalf("snippet: %q", s)
	}
	if s := peoplechats.Snippet("short osun", words, 60); s != "short osun" {
		t.Fatalf("short: %q", s)
	}
	if s := peoplechats.Snippet("nothing here", words, 60); s != "" {
		t.Fatalf("no match: %q", s)
	}
}

// TestNormaliseAtOnce: many goroutines normalising at the same moment get
// the same words as one alone (a shared transform.Chain once panicked).
func TestNormaliseAtOnce(t *testing.T) {
	words := []string{"Ọ̀rẹ́", "Café", "BÚKÀ", "naïve", "Ẹ̀kọ́", "hello", "ÀṢẸ"}
	want := make([]string, len(words))
	for i, w := range words {
		want[i] = peoplechats.Normalise(w)
	}
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				for i, w := range words {
					if got := peoplechats.Normalise(w); got != want[i] {
						errs <- got + " != " + want[i]
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}
