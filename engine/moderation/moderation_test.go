package moderation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/ai"
)

func TestWords(t *testing.T) {
	for _, c := range []struct {
		text, want, category string
	}{
		{"Jollof rice and chicken", Allowed, ""},
		{"Unisex braids and wigs", Allowed, ""},
		{"D0UBLE  your m0ney in 7 days!!", Blocked, CatInvestment},
		{"Please verify-your-account here", Blocked, CatPhishing},
		{"Quick loans for traders", Flagged, CatFinancial},
		{"Original and first copy sneakers", Flagged, CatCounterfeit},
		{"P1stol for sale", Blocked, CatWeapons},
		{"Codeine syrup available", Blocked, CatDrugs},
	} {
		f := Words(Text{Field: "x", Text: c.text})
		if f.Verdict != c.want || f.Category != c.category {
			t.Errorf("%q: %s %s, want %s %s", c.text, f.Verdict, f.Category, c.want, c.category)
		}
	}
}

func TestPrice(t *testing.T) {
	c := &Checker{}
	k := func(v int64) *int64 { return &v }
	for _, x := range []struct {
		kobo *int64
		want string
	}{{nil, Allowed}, {k(150000), Allowed}, {k(0), Flagged}, {k(-5), Flagged}, {k(DefaultCeilingK + 1), Flagged}} {
		if got := c.Price("item", x.kobo).Verdict; got != x.want {
			t.Errorf("%v: %s, want %s", x.kobo, got, x.want)
		}
	}
}

type mapCache map[string]Finding

func (m mapCache) Get(_ context.Context, h []byte) (Finding, bool, error) {
	f, ok := m[string(h)]
	return f, ok, nil
}

func (m mapCache) Put(_ context.Context, h []byte, f Finding) error {
	m[string(h)] = f
	return nil
}

func TestTexts(t *testing.T) {
	model := &ai.Fake{Respond: func(req ai.Request) (*ai.Response, error) {
		msg := req.Messages[0].Text
		var pieces []struct {
			ID   int    `json:"id"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(msg[strings.Index(msg, "["):]), &pieces); err != nil {
			return nil, err
		}
		var out []map[string]any
		for _, p := range pieces {
			v, cat, why := Allowed, "", ""
			if strings.Contains(p.Text, "Gucci") {
				v, cat, why = Flagged, CatCounterfeit, "branded goods at a low price"
			}
			out = append(out, map[string]any{"id": p.ID, "verdict": v, "category": cat, "reason": why})
		}
		raw, _ := json.Marshal(map[string]any{"results": out})
		return &ai.Response{JSON: raw}, nil
	}}
	c := &Checker{Model: model}
	cache := mapCache{}
	ctx := context.Background()
	texts := []Text{{"name", "Ada's Kitchen"}, {"item", "Gucci bags ₦5,000"}, {"item", "Double your money"}, {"item", "Ada's Kitchen"}}
	res, err := c.Texts(ctx, cache, texts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != Blocked || len(res.Findings) != 2 || res.ModelSkipped {
		t.Fatalf("result: %+v", res)
	}
	// The blocked text never reached the model; the rest was asked once.
	if n := len(model.Requests()); n != 1 || strings.Contains(model.Requests()[0].Messages[0].Text, "Double") {
		t.Fatalf("model asked %d times: %+v", n, model.Requests())
	}
	// Asked again: from the cache, not the model.
	if _, err := c.Texts(ctx, cache, texts[:2]); err != nil || len(model.Requests()) != 1 {
		t.Fatalf("cached: %v, %d requests", err, len(model.Requests()))
	}
	// A model that fails does not stop the check; the result says so.
	broken := &Checker{Model: &ai.Fake{Respond: func(ai.Request) (*ai.Response, error) { return nil, errors.New("down") }}}
	res, err = broken.Texts(ctx, nil, []Text{{"name", "Tunde Phones"}})
	if err != nil || !res.ModelSkipped || res.Verdict != Allowed {
		t.Fatalf("model down: %+v %v", res, err)
	}
	// No model: words only.
	res, _ = (&Checker{}).Texts(ctx, nil, []Text{{"name", "Mama Put"}})
	if !res.ModelSkipped {
		t.Fatalf("no model: %+v", res)
	}
}
