// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package harness holds what a launch command hands to the exec step.
package harness

import (
	"encoding/json"

	"github.com/Aiven-Open/ai-gateway-cli/internal/models"
)

// Input is what the table-driven harnesses (codex, pi, opencode) need; the table is non-empty.
type Input struct {
	Table   []models.Model
	Key     string
	BaseURL string
	Home    string
	// Args are forwarded to the harness untouched.
	Args []string
}

// File is generated harness configuration. It never carries the gateway key.
type File struct {
	Path string
	Data []byte
}

// Plan is a prepared launch: files to write, then exec Argv with Env laid over the process environment.
// The key travels only in Env.
type Plan struct {
	Files []File
	Argv  []string
	Env   []string
	// Notice goes to stderr after the files are written, before the exec.
	Notice string
}

// JSON renders v as 2-space indented JSON with a trailing newline.
func JSON(v any) []byte {
	data, _ := json.MarshalIndent(v, "", "  ") // plain structs and maps cannot fail to marshal
	return append(data, '\n')
}
