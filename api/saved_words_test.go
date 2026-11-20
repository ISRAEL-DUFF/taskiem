package api_test

import (
	"strings"
	"testing"
)

// Saving from a message and in words (saved objects S1, part 4): the
// words read for what can be saved; "save mum: GTBank 0123456789" to
// Taskiem shown as understood and saved on Save, in the business's saved
// things (Keep it to myself puts it in one's own); no bank asked for; a
// name taken refused; No saves nothing; nothing waits in the clear.
func TestSavedFromWords(t *testing.T) {
	w := newWAWorld(t)
	owner := w.tenant(t, "Bola Stores", "bola@words.test")

	read := owner.must(200, "POST", "/v1/me/saved/read", map[string]any{"text": "Account name: Ada Obi\nZenith Bank\n0123 456 789"})
	if read["kind"] != "bank_account" || read["title"] != "Ada Obi" || read["details"].(map[string]any)["account_number"] != "0123456789" {
		t.Fatalf("read: %v", read)
	}
	owner.must(400, "POST", "/v1/me/saved/read", map[string]any{"text": " "})

	// An account, the business's.
	got := taskiemSays(t, owner, "save mum: GTBank 0123456789")
	for _, want := range []string{"Save this as @mum?", "Bank: Guaranty Trust Bank", "Account number: 0123456789", "Bola Stores's saved things"} {
		if !strings.Contains(got, want) {
			t.Fatalf("asked: %q lacks %q", got, want)
		}
	}
	var waiting string
	if err := w.env.DB.Admin.QueryRow(t.Context(), `SELECT data::text FROM chat_sessions WHERE state = 'awaiting_confirmation' ORDER BY updated_at DESC LIMIT 1`).Scan(&waiting); err != nil || strings.Contains(waiting, "0123456789") {
		t.Fatalf("waiting in the clear: %q %v", waiting, err)
	}
	if got := taskiemSays(t, owner, "maybe"); !strings.Contains(got, "Tap Save to keep it as @mum") {
		t.Fatalf("reminded: %q", got)
	}
	if got := taskiemSays(t, owner, "yes"); !strings.Contains(got, "Saved as @mum") {
		t.Fatalf("saved: %q", got)
	}
	list := owner.must(200, "GET", "/v1/saved", nil)["saved"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["name"] != "mum" || list[0].(map[string]any)["title"] != "Mum" || list[0].(map[string]any)["hint"] != "GTBank ••6789" {
		t.Fatalf("the business's: %v", list)
	}

	// A place, kept to oneself.
	got = taskiemSays(t, owner, "save @warehouse: 12 Herbert Macaulay Way, Yaba, opposite the filling station")
	if !strings.Contains(got, "Address: 12 Herbert Macaulay Way") || !strings.Contains(got, "Landmark: opposite the filling station") {
		t.Fatalf("a place: %q", got)
	}
	if got := taskiemSays(t, owner, "mine"); !strings.Contains(got, "Saved as @warehouse") {
		t.Fatalf("kept to myself: %q", got)
	}
	mine := owner.must(200, "GET", "/v1/me/saved", nil)["saved"].([]any)
	if len(mine) != 1 || mine[0].(map[string]any)["details"].(map[string]any)["area"] != "Yaba" {
		t.Fatalf("one's own: %v", mine)
	}

	if got := taskiemSays(t, owner, "save dad: 0123456780"); !strings.Contains(got, "Which bank is 0123456780 with?") {
		t.Fatalf("no bank: %q", got)
	}
	if got := taskiemSays(t, owner, "save mum: UBA 2034567891"); !strings.Contains(got, "You already have something called @mum") {
		t.Fatalf("taken: %q", got)
	}
	taskiemSays(t, owner, "save terms: No refunds after 7 days")
	if got := taskiemSays(t, owner, "no"); !strings.Contains(got, "Not saved") {
		t.Fatalf("no: %q", got)
	}
	if n := len(owner.must(200, "GET", "/v1/saved", nil)["saved"].([]any)); n != 1 {
		t.Fatalf("saved after no: %d", n)
	}
}
