package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/ai"
)

// Price is a model's first-party price in US dollars per million tokens.
// Cache writes cost 1.25× input; cache reads are CacheRead, or 0.1× input
// when not listed.
type Price struct {
	In, Out, CacheRead float64
}

// defaultPrices are Anthropic's published first-party prices for the
// models the suite is run with (the claude-api reference's model table,
// October 2026). Another provider's rate card goes in with -price.
var defaultPrices = map[string]Price{
	"claude-opus-5-5":   {In: 4, Out: 20, CacheRead: 0.20},
	"claude-sonnet-5-5": {In: 2, Out: 10, CacheRead: 0.20},
	"claude-opus-5":     {In: 5, Out: 25},
	"claude-fable-5-1":  {In: 10, Out: 50, CacheRead: 0.25},
}

// priceFor finds a model's price, a dated snapshot matching its alias.
func priceFor(prices map[string]Price, model string) (Price, bool) {
	if p, ok := prices[model]; ok {
		return p, true
	}
	best, bestLen := Price{}, 0
	for k, p := range prices {
		if strings.HasPrefix(model, k+"-") && len(k) > bestLen {
			best, bestLen = p, len(k)
		}
	}
	return best, bestLen > 0
}

// cost is what usage cost on model, or nil when the price is unknown (a
// fake or self-hosted model): unknown is not zero.
func cost(prices map[string]Price, model string, u ai.Usage) *float64 {
	p, ok := priceFor(prices, model)
	if !ok {
		return nil
	}
	read := p.CacheRead
	if read == 0 {
		read = 0.1 * p.In
	}
	c := (float64(u.InputTokens)*p.In + float64(u.OutputTokens)*p.Out + float64(u.CacheCreationTokens)*1.25*p.In + float64(u.CacheReadTokens)*read) / 1e6
	return &c
}

// parsePrices reads -price model=in,out[,cache_read] flags over the
// defaults.
func parsePrices(flags []string) (map[string]Price, error) {
	out := map[string]Price{}
	for k, v := range defaultPrices {
		out[k] = v
	}
	for _, f := range flags {
		model, vals, ok := strings.Cut(f, "=")
		parts := strings.Split(vals, ",")
		if !ok || model == "" || len(parts) < 2 || len(parts) > 3 {
			return nil, fmt.Errorf("-price %q: want model=in,out[,cache_read] in dollars per million tokens", f)
		}
		var nums [3]float64
		for i, p := range parts {
			n, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("-price %q: %q is not a price", f, p)
			}
			nums[i] = n
		}
		out[model] = Price{In: nums[0], Out: nums[1], CacheRead: nums[2]}
	}
	return out, nil
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, " ") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }
