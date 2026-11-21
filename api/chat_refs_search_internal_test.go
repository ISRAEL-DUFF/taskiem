package api

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// fakeRefs is a kind's list of n, newest first, a minute apart from start.
func fakeRefs(kind string, n int, start time.Time, party func(i int) string) refPage {
	return func(_ context.Context, _ pgx.Tx, offset int) ([]chatRef, error) {
		var out []chatRef
		for i := offset; i < n && len(out) < refSearchPage; i++ {
			status := "paid"
			if i%3 == 0 {
				status = "overdue"
			}
			out = append(out, chatRef{Kind: kind, ID: fmt.Sprintf("%s-%d", kind, i), Name: fmt.Sprintf("%s-%04d", kind[:3], i),
				Party: party(i), When: start.Add(-time.Duration(i) * time.Minute), Status: status})
		}
		return out, nil
	}
}

// The picker's search (menus-everywhere.md §7): every record, not only the
// newest; words matched at the start of a word, a number anywhere in a
// reference, a status by its words; newest first across kinds; the cursor
// goes on where each kind stopped, never skipping or repeating one.
func TestSearchRefs(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	ada := func(i int) string {
		if i == 950 {
			return "Ada's Kitchen"
		}
		return "Bola Foods"
	}
	pages := map[string]refPage{
		"invoice": fakeRefs("invoice", 1200, now, ada),
		"bill":    fakeRefs("bill", 30, now.Add(-30*time.Second), func(int) string { return "Dangote" }),
	}
	search := func(q string, kinds []string, from map[string]int, limit int) ([]chatRef, string) {
		t.Helper()
		if from == nil {
			from = map[string]int{}
		}
		got, cursor, err := searchRefs(ctx, nil, refSearch{words: splitWords(q), kinds: kinds, from: from, limit: limit, paged: len(from) > 0}, pages)
		if err != nil {
			t.Fatal(err)
		}
		return got, cursor
	}

	// Older than the newest 200: found by its party.
	got, cursor := search("ada", []string{"invoice", "bill"}, nil, 20)
	if len(got) != 1 || got[0].Name != "inv-0950" || cursor != "" {
		t.Fatalf("ada: %v %q", got, cursor)
	}
	// A number anywhere in a reference; a status by its words.
	if got, _ = search("0950", []string{"invoice"}, nil, 20); len(got) != 1 {
		t.Fatalf("0950: %v", got)
	}
	if got, _ = search("unpaid dangote", []string{"bill"}, nil, 50); len(got) != 10 {
		t.Fatalf("unpaid dangote: %d", len(got))
	}

	// Newest first across kinds, then page after page with the cursor: every
	// one once, in order.
	seen := map[string]bool{}
	var last time.Time
	from := map[string]int{}
	for n := 0; ; n++ {
		got, cursor = search("", []string{"invoice", "bill"}, from, 50)
		for _, r := range got {
			if seen[r.ID] {
				t.Fatalf("%s twice", r.ID)
			}
			if !last.IsZero() && r.When.After(last) {
				t.Fatalf("%s out of order", r.ID)
			}
			seen[r.ID], last = true, r.When
		}
		if cursor == "" {
			break
		}
		req := httptest.NewRequest("GET", "/v1/chat-refs?cursor="+cursor, nil)
		q, _, err := refSearchOf(req)
		if err != nil {
			t.Fatalf("cursor %q: %v", cursor, err)
		}
		from = q.from
		if n > 100 {
			t.Fatal("no end")
		}
	}
	if len(seen) != 1230 {
		t.Fatalf("every one once: %d", len(seen))
	}

	// A kind the person may not see here is not read.
	if got, _ = search("", []string{"order"}, nil, 20); len(got) != 0 {
		t.Fatalf("orders: %v", got)
	}
}

func splitWords(q string) []string { return strings.Fields(strings.ToLower(q)) }

// What is asked is checked: kinds, limit, cursor.
func TestRefSearchOf(t *testing.T) {
	for _, bad := range []string{"kind=dog", "limit=0", "limit=51", "cursor=invoice", "cursor=dog:3", "cursor=invoice:-1"} {
		if _, _, err := refSearchOf(httptest.NewRequest("GET", "/x?"+bad, nil)); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
	if _, ok, _ := refSearchOf(httptest.NewRequest("GET", "/x", nil)); ok {
		t.Error("nothing asked is not a search")
	}
	s, ok, err := refSearchOf(httptest.NewRequest("GET", "/x?q=Ada+Unpaid&kind=invoice,bill,invoice&limit=5", nil))
	if err != nil || !ok || len(s.kinds) != 2 || s.limit != 5 || s.words[0] != "ada" || s.words[1] != "unpaid" {
		t.Fatalf("%+v %v %v", s, ok, err)
	}
}
