// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package models

import (
	"cmp"
	"slices"
	"strings"
)

const (
	// RoutingGroup holds the gateway's own model-picking alias, which no single publisher serves.
	RoutingGroup = "Aiven routing"
	// UnknownGroup holds models whose catalogue entry names no owner and that the reviewed list below omits.
	UnknownGroup = "Publisher unknown"
)

// known are reviewed exact gateway ids, so a listing without an owner still groups and names them. The
// publisher is the vendor of the model family, not the cloud serving it, spelled as the catalogue's owned_by spells
// it (Alibaba for Qwen). Names render the id readably and are not upstream display names; an empty name keeps the
// id, which is already the vendor's spelling. Only an exact id matches, never a prefix: a new id stays unknown until
// someone adds it here. A catalogue owner that disagrees with an entry wins over it; see
// reviewed.
var known = map[string]struct{ publisher, name string }{
	"claude-haiku-4-5":        {"Anthropic", "Claude Haiku 4.5"},
	"claude-haiku-5-5":        {"Anthropic", "Claude Haiku 5.5"},
	"claude-haiku-5.5":        {"Anthropic", "Claude Haiku 5.5"},
	"claude-opus-4-5":         {"Anthropic", "Claude Opus 4.5"},
	"claude-opus-4-6":         {"Anthropic", "Claude Opus 4.6"},
	"claude-opus-4-7":         {"Anthropic", "Claude Opus 4.7"},
	"claude-opus-4-8":         {"Anthropic", "Claude Opus 4.8"},
	"claude-opus-5":           {"Anthropic", "Claude Opus 5"},
	"claude-opus-5-5":         {"Anthropic", "Claude Opus 5.5"},
	"claude-sonnet-4-5":       {"Anthropic", "Claude Sonnet 4.5"},
	"claude-sonnet-4-6":       {"Anthropic", "Claude Sonnet 4.6"},
	"claude-sonnet-5":         {"Anthropic", "Claude Sonnet 5"},
	"claude-sonnet-5-5":       {"Anthropic", "Claude Sonnet 5.5"},
	"deepseek-v3.1":           {"DeepSeek", "DeepSeek V3.1"},
	"deepseek-v3.2":           {"DeepSeek", "DeepSeek V3.2"},
	"devstral-2-123b":         {"Mistral AI", "Devstral 2 123B"},
	"gemini-3.5-flash-lite":   {"Google", "Gemini 3.5 Flash-Lite"},
	"gemini-3.8-flash":        {"Google", "Gemini 3.8 Flash"},
	"gemini-4-argon":          {"Google", "Gemini 4 Argon"},
	"gemma-3-12b":             {"Google", "Gemma 3 12B"},
	"gemma-3-27b":             {"Google", "Gemma 3 27B"},
	"gemma-3-4b":              {"Google", "Gemma 3 4B"},
	"glm-4.7":                 {"Z.ai", "GLM 4.7"},
	"glm-4.7-flash":           {"Z.ai", "GLM 4.7 Flash"},
	"glm-5":                   {"Z.ai", "GLM 5"},
	"gpt-5.4":                 {"OpenAI", "GPT-5.4"},
	"gpt-5.5":                 {"OpenAI", "GPT-5.5"},
	"gpt-5.6-luna":            {"OpenAI", "GPT-5.6 Luna"},
	"gpt-5.6-sol":             {"OpenAI", "GPT-5.6 Sol"},
	"gpt-5.6-terra":           {"OpenAI", "GPT-5.6 Terra"},
	"gpt-6-astra":             {"OpenAI", "GPT-6 Astra"},
	"gpt-6-luna":              {"OpenAI", "GPT-6 Luna"},
	"gpt-6-sol":               {"OpenAI", "GPT-6 Sol"},
	"gpt-6.1-sol":             {"OpenAI", "GPT-6.1 Sol"},
	"gpt-oss-120b":            {"OpenAI", "gpt-oss 120B"},
	"gpt-oss-20b":             {"OpenAI", "gpt-oss 20B"},
	"gpt-oss-safeguard-120b":  {"OpenAI", "gpt-oss Safeguard 120B"},
	"gpt-oss-safeguard-20b":   {"OpenAI", "gpt-oss Safeguard 20B"},
	"grok-4.6":                {"xAI", "Grok 4.6"},
	"grok-4.7":                {"xAI", "Grok 4.7"},
	"kimi-k2.5":               {"Moonshot AI", "Kimi K2.5"},
	"kimi-k3":                 {"Moonshot AI", "Kimi K3"},
	"magistral-small":         {"Mistral AI", "Magistral Small"},
	"minimax-m2":              {"MiniMax", "MiniMax M2"},
	"minimax-m2.1":            {"MiniMax", "MiniMax M2.1"},
	"minimax-m2.5":            {"MiniMax", "MiniMax M2.5"},
	"ministral-3-14b":         {"Mistral AI", "Ministral 3 14B"},
	"ministral-3-3b":          {"Mistral AI", "Ministral 3 3B"},
	"ministral-3-8b":          {"Mistral AI", "Ministral 3 8B"},
	"mistral-7b":              {"Mistral AI", "Mistral 7B"},
	"mistral-large-2402":      {"Mistral AI", "Mistral Large 24.02"},
	"mistral-pixtral-large":   {"Mistral AI", "Pixtral Large"},
	"mixtral-8x7b":            {"Mistral AI", "Mixtral 8x7B"},
	"nemotron-nano-12b-v2-vl": {"NVIDIA", "Nemotron Nano 12B v2 VL"},
	"nemotron-nano-3-30b":     {"NVIDIA", "Nemotron Nano 3 30B"},
	"nemotron-nano-9b-v2":     {"NVIDIA", "Nemotron Nano 9B v2"},
	"nemotron-super-3-120b":   {"NVIDIA", "Nemotron Super 3 120B"},
	"nova-2-lite":             {"AWS", "Nova 2 Lite"},
	"nova-lite":               {"AWS", "Nova Lite"},
	"nova-micro":              {"AWS", "Nova Micro"},
	"nova-pro":                {"AWS", "Nova Pro"},
	"qwen3-235b":              {"Alibaba", "Qwen3 235B"},
	"qwen3-32b":               {"Alibaba", "Qwen3 32B"},
	"qwen3-coder-30b":         {"Alibaba", "Qwen3 Coder 30B"},
	"qwen3-coder-480b":        {"Alibaba", "Qwen3 Coder 480B"},
	"qwen3-next-80b":          {"Alibaba", "Qwen3 Next 80B"},
	"qwen3-vl-235b":           {"Alibaba", "Qwen3 VL 235B"},
	"voxtral-mini-3b":         {"Mistral AI", "Voxtral Mini 3B"},
	"voxtral-small-24b":       {"Mistral AI", "Voxtral Small 24B"},
}

// owners spell the lower-case owned_by values vendors commonly publish; any other value is shown as the catalogue
// wrote it.
var owners = map[string]string{
	"aiven": "Aiven", "alibaba": "Alibaba", "amazon": "AWS", "anthropic": "Anthropic", "aws": "AWS",
	"deepseek": "DeepSeek", "google": "Google", "meta": "Meta", "minimax": "MiniMax", "mistral": "Mistral AI", "mistralai": "Mistral AI",
	"moonshotai": "Moonshot AI", "nvidia": "NVIDIA", "openai": "OpenAI", "qwen": "Alibaba", "xai": "xAI", "z-ai": "Z.ai",
	"zai": "Z.ai", "zhipu": "Z.ai",
}

// DisplayName is the catalogue's display name, else the reviewed name for a known id, else the id itself.
func (m Model) DisplayName() string {
	if m.Name != "" {
		return m.Name
	}
	if k, ok := m.reviewed(); ok && k.name != "" {
		return k.name
	}
	return m.Alias
}

// litellmOwner is the owned_by LiteLLM's own /models listing gives every model, whoever publishes it. A gateway
// that relays that listing unchanged names no publisher; a catalogue listing sends the creator, capitalised.
const litellmOwner = "openai"

// owner is the catalogue's owned_by spelled for display, or "" when the entry states none: LiteLLM's placeholder
// owner says nothing.
func (m Model) owner() string {
	if m.Owner == "" || m.Owner == litellmOwner {
		return ""
	}
	if label, ok := owners[strings.ToLower(m.Owner)]; ok {
		return label
	}
	return m.Owner
}

// reviewed is the table entry for the id, unless the catalogue names another owner: a private alias that reuses a
// public id (owned_by Aiven for claude-opus-5-5) is its owner's model, not the public one the table describes.
func (m Model) reviewed() (struct{ publisher, name string }, bool) {
	k, ok := known[m.Alias]
	if o := m.owner(); ok && o != "" && o != k.publisher {
		return k, false
	}
	return k, ok
}

// Group names who the listing files a model under: the catalogue's owner when it states one, which for private
// entries is the owner rather than the original publisher, else the reviewed publisher for a known id, else unknown.
func (m Model) Group() string {
	if m.Alias == Router {
		return RoutingGroup
	}
	if o := m.owner(); o != "" {
		return o
	}
	if k, ok := m.reviewed(); ok {
		return k.publisher
	}
	return UnknownGroup
}

// Group is one heading of the listing with its models sorted by id.
type Group struct {
	Name   string
	Models []Model
}

// Grouped orders groups by name, with routing and unknown entries last so named publishers come first.
func Grouped(ms []Model) []Group {
	byName := map[string][]Model{}
	for _, m := range Sorted(ms) {
		byName[m.Group()] = append(byName[m.Group()], m)
	}
	out := make([]Group, 0, len(byName))
	for name, list := range byName {
		out = append(out, Group{name, list})
	}
	rank := func(name string) int {
		switch name {
		case RoutingGroup:
			return 1
		case UnknownGroup:
			return 2
		}
		return 0
	}
	slices.SortFunc(out, func(a, b Group) int {
		return cmp.Or(cmp.Compare(rank(a.Name), rank(b.Name)), strings.Compare(a.Name, b.Name))
	})
	return out
}
