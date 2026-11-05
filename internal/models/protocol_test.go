// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package models_test

import (
	"testing"

	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

func TestModelProtocolGrid(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		alias    string
		protocol models.Protocol
	}{
		{"claude-haiku-5-5", models.Messages},
		{"claude-private-model", models.Messages},
		{"gpt-6.1-sol", models.Responses},
		{"gpt-private-model", models.Responses},
		{"gpt-oss", models.ChatCompletions},
		{"gpt-oss-120b", models.ChatCompletions},
		{"gpt-oss-safeguard-20b", models.ChatCompletions},
		{"gpt-oss-private-model", models.ChatCompletions},
		{"gemini-4-argon", models.ChatCompletions},
		{"deepseek-v3.2", models.ChatCompletions},
		{"aiven-model", models.ChatCompletions},
		{"private-router", models.ChatCompletions},
		{"private-customer-model", models.ChatCompletions},
	} {
		t.Run(tc.alias, func(t *testing.T) {
			t.Parallel()
			protocol := models.ModelProtocol(tc.alias)
			if protocol != tc.protocol {
				t.Fatalf("protocol=%q want=%q", protocol, tc.protocol)
			}
		})
	}
}

func TestProtocolLabelAndBaseURL(t *testing.T) {
	t.Parallel()
	const origin = "https://gateway.example"
	for _, tc := range []struct {
		protocol    models.Protocol
		label, base string
	}{
		{models.Messages, "Messages", origin},
		{models.ChatCompletions, "Chat Completions", origin + "/v1"},
		{models.Responses, "Responses", origin + "/v1"},
		{"", "Unresolved protocol", origin + "/v1"},
	} {
		if l, b := tc.protocol.Label(), tc.protocol.BaseURL(origin); l != tc.label || b != tc.base {
			t.Errorf("%q: label %q, base %q; want %q, %q", tc.protocol, l, b, tc.label, tc.base)
		}
	}
}
