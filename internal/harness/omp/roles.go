// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package omp

import (
	"errors"

	"github.com/Aiven-Open/ai-gateway-cli/internal/models"
)

// Roles are the agent roles `omp init` pins, in prompt order.
var Roles = []string{"architect", "builder", "cross-reviewer", "bulk-reader", "boilerplate-writer"}

// roleLadders are each role's preferred aliases, best first: production ids, then the POC gateway's aliases.
var roleLadders = map[string][]string{
	"architect":          {"claude-opus-5-5", "claude-fable", "claude-opus", "gpt-sol", "claude-sonnet"},
	"builder":            {"claude-sonnet-5-5", "deepseek-flash", "glm-fast", "deepseek", "claude-sonnet"},
	"cross-reviewer":     {"gpt-6-astra", "gpt-astra", "gpt-terra", "gemini-flash", "claude-opus"},
	"bulk-reader":        {"claude-haiku-5-5", "claude-haiku-5.5", "deepseek-flash", "glm-fast", "gpt-luna"},
	"boilerplate-writer": {"claude-haiku-5-5", "claude-haiku-5.5", "deepseek-flash", "glm-fast", "gpt-luna"},
}

// ErrNothingPinnable means the key grants no alias a role can be pinned to.
var ErrNothingPinnable = errors.New("your key grants no model that can be pinned to a role")

// RoleDefault is the first granted alias of the role's ladder, else the cheapest pinnable one: a role pins one model, so the router is never a default.
func RoleDefault(table []models.Model, role string) (string, error) {
	if a := models.FirstGranted(table, roleLadders[role]...); a != "" {
		return a, nil
	}
	if a := models.CheapestPinnable(table); a != "" {
		return a, nil
	}
	return "", ErrNothingPinnable
}
