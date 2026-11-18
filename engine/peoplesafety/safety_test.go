package peoplesafety

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

const day = 24 * time.Hour

// TestAllowance: the request allowance grows with age (a week, 30 days, 90
// days) and accepted requests, and shrinks with refusals, reports, a
// shared device in the first week, and a moderator's limit.
func TestAllowance(t *testing.T) {
	for _, c := range []struct {
		name        string
		s           Signals
		wait, today int
		slowed      bool
	}{
		{"first week", Signals{Age: day}, 2, 3, true},
		{"a week", Signals{Age: 8 * day}, 5, 10, false},
		{"a month", Signals{Age: 31 * day}, 8, 15, false},
		{"three months", Signals{Age: 91 * day}, 10, 20, false},
		{"accepted", Signals{Age: 91 * day, Accepted: 10}, 15, 30, false},
		{"accepted, capped", Signals{Age: 91 * day, Accepted: 100}, 25, 60, false},
		{"often refused", Signals{Age: 91 * day, Accepted: 1, Refused: 4}, 3, 7, false},
		{"refused, but mostly accepted", Signals{Age: 91 * day, Accepted: 10, Refused: 4}, 15, 30, false},
		{"reported by two", Signals{Age: 91 * day, ReportedBy: 2}, 3, 6, true},
		{"reported by five", Signals{Age: 91 * day, Accepted: 40, ReportedBy: 5}, 1, 1, true},
		{"first week, shared device", Signals{Age: day, DeviceAccounts: 3}, 1, 1, true},
		{"first week, two accounts on the device", Signals{Age: day, DeviceAccounts: 2}, 2, 3, true},
		{"older, shared device", Signals{Age: 31 * day, DeviceAccounts: 5}, 8, 15, false},
		{"limited", Signals{Age: 91 * day, Accepted: 20, Limited: true}, 1, 2, true},
	} {
		w, d := Allowance(c.s)
		if w != c.wait || d != c.today || Slowed(c.s) != c.slowed {
			t.Errorf("%s: %d waiting, %d a day, slowed %v; want %d, %d, %v", c.name, w, d, Slowed(c.s), c.wait, c.today, c.slowed)
		}
	}
}

type fakeKMS struct{}

func (fakeKMS) SealPlatform(_ context.Context, purpose string, v []byte) (string, string, error) {
	return base64.StdEncoding.EncodeToString(append([]byte(purpose+"|"), v...)), "k1", nil
}

func (fakeKMS) OpenPlatform(_ context.Context, purpose, _, sealed string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return nil, err
	}
	p := []byte(purpose + "|")
	if len(raw) < len(p) || string(raw[:len(p)]) != string(p) {
		return nil, errors.New("wrong purpose")
	}
	return raw[len(p):], nil
}

// TestBoxIsBoundToItsOwnerAndRow: a box opens what it sealed, for the same
// row only; its kept key opens only for its own purpose and owner.
func TestBoxIsBoundToItsOwnerAndRow(t *testing.T) {
	ctx := context.Background()
	id, other := uuid.New(), uuid.New()
	b, err := NewBox(ctx, fakeKMS{}, ReportPurpose(id), id)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := b.Seal(MessagesRow, []byte(`[{"body":"pay me"}]`))
	if err != nil {
		t.Fatal(err)
	}
	again, err := OpenBox(ctx, fakeKMS{}, ReportPurpose(id), id, b.Sealed, b.KMSKey)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := again.Open(MessagesRow, sealed); err != nil || string(got) != `[{"body":"pay me"}]` {
		t.Fatalf("opened: %q %v", got, err)
	}
	if _, err := again.Open(FileRow(id), sealed); err == nil {
		t.Fatal("opened for another row")
	}
	if _, err := OpenBox(ctx, fakeKMS{}, ReportPurpose(other), id, b.Sealed, b.KMSKey); err == nil {
		t.Fatal("the key opened for another report")
	}
	wrong, err := OpenBox(ctx, fakeKMS{}, ReportPurpose(id), other, b.Sealed, b.KMSKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrong.Open(MessagesRow, sealed); err == nil {
		t.Fatal("opened as another owner")
	}
	if _, err := NewBox(ctx, nil, HeldPurpose(id), id); !errors.Is(err, ErrNoKMS) {
		t.Fatalf("no key service: %v", err)
	}
}

func TestHalfLabel(t *testing.T) {
	if (Half{2026, 1}).Label() != "January to June 2026" || (Half{2026, 2}).Label() != "July to December 2026" {
		t.Fatal("labels")
	}
}
