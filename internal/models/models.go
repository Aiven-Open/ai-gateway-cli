// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package models holds the gateway's model table as the launcher uses it.
package models

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Model is one granted alias. Prices and context are nullable: absent is not zero.
type Model struct {
	Alias    string
	InUSD1M  *float64
	OutUSD1M *float64
	Context  *int
}

// Router is the AI Gateway's model-picking alias.
const Router = "aiven-model"

// Sorted returns a copy ordered by alias in byte order.
func Sorted(ms []Model) []Model {
	out := append([]Model(nil), ms...)
	sort.Slice(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	return out
}

// PriceLabel renders `alias ($X in / $Y out per 1M tok)`, or `alias (price n/a)` when either price is missing or zero.
func (m Model) PriceLabel() string {
	if p := m.priceText(); p != "" {
		return fmt.Sprintf("%s (%s)", m.Alias, p)
	}
	return m.Alias + " (price n/a)"
}

// Description is the price without the alias: `$X in / $Y out per 1M tok`, or `price n/a`.
func (m Model) Description() string {
	if p := m.priceText(); p != "" {
		return p
	}
	return "price n/a"
}

// Find returns the model with alias.
func Find(ms []Model, alias string) (Model, bool) {
	for _, m := range ms {
		if m.Alias == alias {
			return m, true
		}
	}
	return Model{}, false
}

// FirstGranted returns the first of aliases the table grants, or "".
func FirstGranted(ms []Model, aliases ...string) string {
	for _, a := range aliases {
		if _, ok := Find(ms, a); ok {
			return a
		}
	}
	return ""
}

// DefaultAlias returns the first alias in sorted order with prefix, else SonnetOrFirst.
func DefaultAlias(ms []Model, prefix string) string {
	for _, m := range Sorted(ms) {
		if strings.HasPrefix(m.Alias, prefix) {
			return m.Alias
		}
	}
	return SonnetOrFirst(ms)
}

// SonnetOrFirst returns production `claude-sonnet-5-5` or the POC gateway's `claude-sonnet` when granted, else FirstAlias.
func SonnetOrFirst(ms []Model) string {
	if a := FirstGranted(ms, "claude-sonnet-5-5", "claude-sonnet"); a != "" {
		return a
	}
	return FirstAlias(ms)
}

// FirstNotIn returns the first non-router alias in sorted order not in used, else FirstAlias.
func FirstNotIn(ms []Model, used ...string) string {
	for _, m := range Sorted(ms) {
		if m.Alias != Router && !slices.Contains(used, m.Alias) {
			return m.Alias
		}
	}
	return FirstAlias(ms)
}

// FirstAlias returns the first non-router alias in sorted order; the router only when it is all the table has; "" when empty.
func FirstAlias(ms []Model) string {
	sorted := Sorted(ms)
	for _, m := range sorted {
		if m.Alias != Router {
			return m.Alias
		}
	}
	if len(sorted) > 0 {
		return sorted[0].Alias
	}
	return ""
}

// Priced reports whether both prices are present and nonzero.
func (m Model) Priced() bool {
	return m.InUSD1M != nil && m.OutUSD1M != nil && *m.InUSD1M != 0 && *m.OutUSD1M != 0
}

func (m Model) priceText() string {
	if !m.Priced() {
		return ""
	}
	return fmt.Sprintf("$%.2f in / $%.2f out per 1M tok", *m.InUSD1M, *m.OutUSD1M)
}

// Table renders PriceLabel for every model, sorted, one per line.
func Table(ms []Model) string {
	var b strings.Builder
	for _, m := range Sorted(ms) {
		b.WriteString(m.PriceLabel())
		b.WriteByte('\n')
	}
	return b.String()
}

// CheapestPinnable is the lowest output-priced alias that can be pinned to a role: never the router, a priced alias before an unpriced one, ties to the first in sorted order; "" when nothing qualifies.
func CheapestPinnable(ms []Model) string {
	best, unpriced := "", ""
	var bestOut float64
	for _, m := range Sorted(ms) {
		switch {
		case m.Alias == Router:
		case !m.Priced():
			if unpriced == "" {
				unpriced = m.Alias
			}
		case best == "" || *m.OutUSD1M < bestOut:
			best, bestOut = m.Alias, *m.OutUSD1M
		}
	}
	if best != "" {
		return best
	}
	return unpriced
}
