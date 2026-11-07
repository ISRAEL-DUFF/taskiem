package api

import "testing"

// "@Taskiem" (chat_tools.go): a word in any case, at the start or after a
// space or a mark, taken off with its comma or colon; the lines are kept.
func TestCutTaskiem(t *testing.T) {
	for _, c := range []struct {
		in, out string
		ok      bool
	}{
		{"@Taskiem status", "status", true},
		{"@taskiem, what do I owe @Dangote?", "what do I owe @Dangote?", true},
		{"what does @Dangote charge, @TASKIEM?", "what does @Dangote charge, ?", true},
		{"hi\n@Taskiem: summary\nof today", "hi\nsummary\nof today", true},
		{"(@Taskiem) hi", "() hi", true},
		{"@Taskiem", "hi", true},
		{"  @Taskiem  ", "hi", true},
		{"@TaskiemBot hi", "@TaskiemBot hi", false},
		{"mail me@taskiem.com", "mail me@taskiem.com", false},
		{"@Dangote hi", "@Dangote hi", false},
	} {
		out, ok := cutTaskiem(c.in)
		if ok != c.ok || out != c.out {
			t.Errorf("%q: %q %v, want %q %v", c.in, out, ok, c.out, c.ok)
		}
	}
}

// The words of changes told as diary lines (chat_changes.go).
func TestChangeWords(t *testing.T) {
	for got, want := range map[string]string{
		orderStepWords("accept", "AD-1", "Rivers Foods"):     "Accepted order AD-1 from Rivers Foods",
		orderStepWords("dispatched", "AD-1", "Rivers Foods"): "Order AD-1 from Rivers Foods is on its way",
		orderStepWords("some_step", "AD-1", "Rivers Foods"):  "Order AD-1 from Rivers Foods: some step",
		qty(1, "bag"):    "1 bag",
		qty(20, "bag"):   "20 bags",
		qty(3, "box"):    "3 boxes",
		qty(4, ""):       "4",
		qty(2, "pieces"): "2 pieces",
		buyerStepWords("send", "PO-1", "Bayo Rice", 23400000):   "Sent order PO-1 to Bayo Rice: ₦234,000",
		buyerStepWords("confirm", "PO-1", "Bayo Rice", 0):       "Confirmed Bayo Rice's quote for order PO-1",
		buyerStepWords("all_came", "PO-1", "Bayo Rice", 0):      "All of order PO-1 came from Bayo Rice",
		buyerStepWords("pay", "INV-7", "Bayo Rice", 500000):     "Paying INV-7 from Taskiem: ₦5,000 to Bayo Rice",
		buyerStepWords("declare", "INV-7", "Bayo Rice", 500000): "Told Bayo Rice you paid ₦5,000 of INV-7 outside Taskiem",
		buyerStepWords("query", "INV-7", "Bayo Rice", 0):        "Questioned INV-7 from Bayo Rice",
		diaryWords([]string{"A"}):                               "✓ A",
		diaryWords([]string{"A", "B"}):                          "✓ 2 changes:\n• A\n• B",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}
