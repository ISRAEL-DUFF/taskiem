package peoplechats_test

import (
	"bytes"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/peoplechats"
)

func TestSealIsBoundToItsOwnerAndRow(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	a, b := uuid.New(), uuid.New()
	sealed, err := peoplechats.Seal(key, a, "m:1", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("hello")) {
		t.Fatal("the words are in the sealed value")
	}
	if got, err := peoplechats.Open(key, a, "m:1", sealed); err != nil || string(got) != "hello" {
		t.Fatalf("open: %q %v", got, err)
	}
	if _, err := peoplechats.Open(key, b, "m:1", sealed); err == nil {
		t.Error("opened for another person")
	}
	if _, err := peoplechats.Open(key, a, "m:2", sealed); err == nil {
		t.Error("opened for another row")
	}
	other := bytes.Repeat([]byte{8}, 32)
	if _, err := peoplechats.Open(other, a, "m:1", sealed); err == nil {
		t.Error("opened under another key")
	}
}

func TestRequestAllowance(t *testing.T) {
	for _, c := range []struct {
		name              string
		newWeek           bool
		accepted, refused int
		waiting, perDay   int
	}{
		{"new account", true, 0, 0, 2, 3},
		{"older account", false, 0, 0, 5, 10},
		{"accepted often", false, 10, 1, 10, 20},
		{"capped", false, 200, 0, 20, 50},
		{"deleted or blocked often", false, 2, 5, 2, 4},
		{"a few refused among many accepted", false, 20, 4, 15, 30},
		{"never below one", true, 0, 9, 1, 1},
	} {
		w, d := peoplechats.RequestAllowance(c.newWeek, c.accepted, c.refused)
		if w != c.waiting || d != c.perDay {
			t.Errorf("%s: %d waiting, %d a day; want %d, %d", c.name, w, d, c.waiting, c.perDay)
		}
	}
}
