// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package models holds the gateway's model table as the launcher uses it.
package models

import (
	"slices"
	"sort"
	"strings"
)

// Model is one granted alias. Prices and context are nullable: absent is not zero.
type Model struct {
	Alias string
	// Owner is the catalogue's owned_by and Name its display name; either is empty when the entry omits it.
	Owner, Name string
	InUSD1M     *float64
	OutUSD1M    *float64
	Context     *int
	// MaxOutput is the most tokens one response may hold.
	MaxOutput *int
}

// Router is the AI Gateway's model-picking alias.
const Router = "aiven-model"

// Sorted returns a copy ordered by alias in byte order.
func Sorted(ms []Model) []Model {
	out := append([]Model(nil), ms...)
	sort.Slice(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	return out
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

// SonnetOrFirst returns `claude-sonnet-5-5` or `claude-sonnet` when granted, else FirstAlias.
func SonnetOrFirst(ms []Model) string {
	if a := FirstGranted(ms, "claude-sonnet-5-5", "claude-sonnet"); a != "" {
		return a
	}
	return FirstAlias(ms)
}

// CodexOrFirst returns production `gpt-6.1-sol` or `gpt-6-sol` when granted, else DefaultAlias for `gpt-`.
func CodexOrFirst(ms []Model) string {
	if a := FirstGranted(ms, "gpt-6.1-sol", "gpt-6-sol"); a != "" {
		return a
	}
	return DefaultAlias(ms, "gpt-")
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
