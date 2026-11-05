// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package models

import "strings"

// Protocol is a gateway inference interface, also used by Pi and OMP.
type Protocol string

const (
	// ChatCompletions is the OpenAI Chat Completions interface.
	ChatCompletions Protocol = "openai-completions"
	// Messages is the Anthropic Messages interface.
	Messages Protocol = "anthropic-messages"
	// Responses is the OpenAI Responses interface.
	Responses Protocol = "openai-responses"
)

// Label names the protocol in user-facing checks.
func (p Protocol) Label() string {
	switch p {
	case ChatCompletions:
		return "Chat Completions"
	case Messages:
		return "Messages"
	case Responses:
		return "Responses"
	}
	return "Unresolved protocol"
}

// BaseURL is the native client base for this protocol.
func (p Protocol) BaseURL(base string) string {
	if p == Messages {
		return base
	}
	return base + "/v1"
}

// ModelProtocol routes the granted catalogue by id family, never by publisher or display name.
func ModelProtocol(alias string) Protocol {
	switch {
	case strings.HasPrefix(alias, "claude-"):
		return Messages
	case alias == "gpt-oss" || strings.HasPrefix(alias, "gpt-oss-"):
		return ChatCompletions
	case strings.HasPrefix(alias, "gpt-"):
		return Responses
	}
	return ChatCompletions
}
