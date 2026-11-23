// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package models

import (
	"regexp"
	"strings"
)

const (
	claudeBase   = `(claude-(?:[a-z]+(?:[.-]\d{1,2}){0,2}|\d+(?:[.-]\d+)?-[a-z]+))`
	bedrockScope = `(?:(?:us|eu|apac|global|au|jp|in|ca)\.)?`
	vendorPrefix = `(anthropic|openai|zai|zhipu|z-ai|qwen|alibaba|deepseek|mistral|mistralai|` +
		`moonshotai|minimax|google|meta|nvidia|amazon|aws)`
)

var (
	vendorPrefixedRe = regexp.MustCompile(
		`^` + bedrockScope + vendorPrefix + `\.([A-Za-z0-9][A-Za-z0-9._:@-]*)$`,
	)
	anthropicDatedRe  = regexp.MustCompile(`^` + claudeBase + `-\d{8}$`)
	anthropicVertexRe = regexp.MustCompile(`^` + claudeBase + `@(?:\d{8}|default)$`)
	anthropicBodyRe   = regexp.MustCompile(
		`^` + claudeBase + `(?:-\d{8})?(?:@(?:\d{8}|default))?(?:-v\d+(?::\d+)?|-\d+:\d+|:\d+)?$`,
	)
	openaiDatedRe   = regexp.MustCompile(`^(gpt-[a-z0-9.]+(?:-[a-z0-9.]+)*)-\d{4}-\d{2}-\d{2}$`)
	vertexSuffixRe  = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)@(?:\d{8}|default)$`)
	stripVertexRe   = regexp.MustCompile(`@(?:\d{8}|default)$`)
	stripRevisionRe = regexp.MustCompile(`(?:-v\d+(?::\d+)?|-\d+:\d+|:\d+)$`)
	stripDateRe     = regexp.MustCompile(`(?:-\d{8}|-\d{4}-\d{2}-\d{2})$`)
	dottedVerRe     = regexp.MustCompile(`(\d)\.(\d)`)
	dashedVerRe     = regexp.MustCompile(`-(\d+)-(\d+)$`)
)

// ProviderAlias derives the granted gateway alias wrapped by a provider model id, such as
// eu.anthropic.claude-sonnet-5.5 -> claude-sonnet-5-5 or us.deepseek.deepseek-v3.2-v1:0 -> deepseek-v3.2.
// It returns "" for an unknown shape, an ungranted alias or an alias filed under another publisher.
func ProviderAlias(granted []Model, id string) string {
	if id == "" || strings.ContainsAny(id, " \t\r\n") {
		return ""
	}
	if m := vendorPrefixedRe.FindStringSubmatch(id); m != nil {
		ns, body := strings.ToLower(m[1]), m[2]
		expected := owners[ns]
		if ns == "anthropic" {
			am := anthropicBodyRe.FindStringSubmatch(body)
			if am == nil {
				return ""
			}
			return acceptAlias(granted, expected, claudeCandidates(am[1])...)
		}
		return acceptAlias(granted, expected, stripProviderSuffixes(body)...)
	}
	if m := anthropicDatedRe.FindStringSubmatch(id); m != nil {
		return acceptAlias(granted, "Anthropic", claudeCandidates(m[1])...)
	}
	if m := anthropicVertexRe.FindStringSubmatch(id); m != nil {
		return acceptAlias(granted, "Anthropic", claudeCandidates(m[1])...)
	}
	if m := openaiDatedRe.FindStringSubmatch(id); m != nil {
		return acceptAlias(granted, "OpenAI", m[1])
	}
	if m := vertexSuffixRe.FindStringSubmatch(id); m != nil {
		return acceptAlias(granted, "", m[1])
	}
	return ""
}

func claudeCandidates(base string) []string {
	out := []string{base}
	if dashed := dottedVerRe.ReplaceAllString(base, `$1-$2`); dashed != base {
		out = append(out, dashed)
	}
	if dotted := dashedVerRe.ReplaceAllString(base, `-$1.$2`); dotted != base {
		out = append(out, dotted)
	}
	return out
}

func stripProviderSuffixes(body string) []string {
	out := []string{body}
	cur := body
	if next := stripVertexRe.ReplaceAllString(cur, ""); next != cur {
		out = append(out, next)
		cur = next
	}
	if next := stripRevisionRe.ReplaceAllString(cur, ""); next != cur {
		out = append(out, next)
		cur = next
	}
	if next := stripDateRe.ReplaceAllString(cur, ""); next != cur {
		out = append(out, next)
	}
	return out
}

func acceptAlias(granted []Model, expectedPublisher string, candidates ...string) string {
	for _, cand := range candidates {
		if cand == "" || cand == Router {
			continue
		}
		g, ok := Find(granted, cand)
		if !ok {
			continue
		}
		if k, isKnown := known[cand]; isKnown {
			if o := g.owner(); o != "" && o != k.publisher {
				continue
			}
		}
		pub := g.owner()
		if pub == "" {
			if k, ok := g.reviewed(); ok {
				pub = k.publisher
			}
		}
		if expectedPublisher != "" && pub != "" && pub != expectedPublisher {
			continue
		}
		if expectedPublisher == "" && pub == "Aiven" {
			continue
		}
		return cand
	}
	return ""
}
