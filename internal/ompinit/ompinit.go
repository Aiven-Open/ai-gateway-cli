// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package ompinit renders the gateway-router project files: role mappings merged into `.omp/config.yml`, the state sidecar, the router rule.
package ompinit

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
)

// Params are validated, already-qualified role selectors.
type Params struct {
	Architect, Builder, CrossReviewer, BulkReader, BoilerplateWriter string
	// Advisor is off, main, builder or both; AdvisorModel is set unless off.
	Advisor, AdvisorModel string
	// ConfigPath and StatePath only appear in warnings.
	ConfigPath, StatePath string
}

// Result is the rendered config, sidecar and warnings.
type Result struct {
	Config, State []byte
	Warnings      []string
}

type entry struct {
	path  []string
	value any
}

// bundledRoles pins OMP's bundled agents to roles, so a dispatch cannot silently run the router's own alias.
var bundledRoles = [][2]string{
	{"task", "builder"}, {"sonic", "boilerplate-writer"}, {"scout", "bulk-reader"},
	{"reviewer", "cross-reviewer"}, {"security-reviewer", "cross-reviewer"},
}

// Render merges the roles into existing (empty when the file is missing); state is the previous sidecar, if any.
func Render(existing, state []byte, p Params) (Result, error) {
	d, err := parseDoc(existing)
	if errors.Is(err, errNotMapping) {
		return Result{}, fmt.Errorf("%s must be a YAML mapping at the top level. Fix or move it, then rerun.", p.ConfigPath)
	}
	if err != nil {
		msg, _, _ := strings.Cut(err.Error(), "\n")
		return Result{}, fmt.Errorf("%s is not valid YAML (%s). Fix or move it, then rerun.", p.ConfigPath, msg)
	}
	optional := p.optional()
	managed, unreadable := readManaged(state)
	var warnings []string
	if unreadable {
		warnings = append(warnings, p.StatePath+" is unreadable; treating every optional key as hand-written and leaving it alone.")
	}
	// Required keys go first so reclaim never empties modelRoles or task, whose merge target would then lose its indent.
	if err := setAll(d, p.required(), p.ConfigPath); err != nil {
		return Result{}, err
	}
	warnings = append(warnings, reclaim(d, managed, optional)...)
	if err := setAll(d, optional, p.ConfigPath); err != nil {
		return Result{}, err
	}
	if _, ok := d.get([]string{"enabledModels"}); ok {
		warnings = append(warnings, p.ConfigPath+" sets enabledModels. Under `aiven-llm omp` the launcher's --config overlay outranks project config, so this array is ignored there; it applies when you run a bare `omp` in this project, and then it must include \"aiven-gateway/*\" for the role models to resolve.")
	}
	config := []byte(d.String())
	var decoded any
	if err := yaml.Unmarshal(config, &decoded); err != nil { // the AST edits can leave a dangling alias or flow syntax error
		msg, _, _ := strings.Cut(err.Error(), "\n")
		return Result{}, fmt.Errorf("%s would not be valid YAML after the merge (%s). Fix or move it, then rerun.", p.ConfigPath, msg)
	}
	return Result{Config: config, State: stateJSON(optional), Warnings: warnings}, nil
}

func setAll(d *doc, es []entry, configPath string) error {
	for _, e := range es {
		if err := d.set(e.path, e.value); err != nil {
			return fmt.Errorf("%s: %w. Fix or move it, then rerun.", configPath, err)
		}
	}
	return nil
}

// optionalPaths are the keys a previous run may have written and this one may not want; modelRoles.default is the launcher overlay's.
var optionalPaths = [][]string{{"modelRoles", "advisor"}, {"advisor", "enabled"}, {"task", "agentAdvisor", "builder"}}

// reclaim removes optional keys this run does not want, only where the value still matches what the sidecar recorded; edited ones stay and are reported.
func reclaim(d *doc, managed map[string]any, desired []entry) (warnings []string) {
	for _, path := range optionalPaths {
		key := strings.Join(path, ".")
		recorded, ok := managed[key]
		if !ok || slices.ContainsFunc(desired, func(e entry) bool { return slices.Equal(e.path, path) }) {
			continue
		}
		cur, found := d.get(path)
		switch {
		case !found:
		case cur == recorded:
			d.remove(path)
		default:
			warnings = append(warnings, fmt.Sprintf("left %s alone: it is %s, not the %s a previous init wrote.", key, quote(cur), quote(recorded)))
		}
	}
	return warnings
}

func quote(v any) string {
	b, _ := json.Marshal(v) // v is a string or bool read from YAML, which always marshal
	return string(b)
}

// readManaged returns the sidecar's recorded values; anything but version 1 reads as empty, and only malformed JSON is reported unreadable.
func readManaged(state []byte) (managed map[string]any, unreadable bool) {
	if state == nil {
		return nil, false
	}
	var s struct {
		Version int
		Managed map[string]any
	}
	if json.Unmarshal(state, &s) != nil {
		return nil, true
	}
	if s.Version != 1 {
		return nil, false
	}
	return s.Managed, false
}

func (p Params) required() []entry {
	es := []entry{
		{[]string{"modelRoles", "architect"}, p.Architect},
		{[]string{"modelRoles", "builder"}, p.Builder},
		{[]string{"modelRoles", "cross-reviewer"}, p.CrossReviewer},
		{[]string{"modelRoles", "bulk-reader"}, p.BulkReader},
		{[]string{"modelRoles", "boilerplate-writer"}, p.BoilerplateWriter},
	}
	for _, b := range bundledRoles {
		es = append(es, entry{[]string{"task", "agentModelOverrides", b[0]}, "@" + b[1]})
	}
	return es
}

// optional are the keys only some advisor modes write; the sidecar records them.
func (p Params) optional() []entry {
	var es []entry
	if p.Advisor == "main" || p.Advisor == "both" {
		es = append(es, entry{[]string{"modelRoles", "advisor"}, p.AdvisorModel}, entry{[]string{"advisor", "enabled"}, true})
	}
	if p.Advisor == "builder" || p.Advisor == "both" {
		es = append(es, entry{[]string{"task", "agentAdvisor", "builder"}, p.AdvisorModel})
	}
	return es
}

// stateJSON is the sidecar: version 1 and the dotted-path to value map of optional keys written.
func stateJSON(managed []entry) []byte {
	var b strings.Builder
	b.WriteString("{\n  \"version\": 1,\n  \"managed\": {")
	for i, e := range managed {
		v, _ := json.Marshal(e.value) // strings and bools always marshal
		k, _ := json.Marshal(strings.Join(e.path, "."))
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("\n    " + string(k) + ": " + string(v))
	}
	if len(managed) > 0 {
		b.WriteString("\n  ")
	}
	b.WriteString("}\n}\n")
	return []byte(b.String())
}

// Rule is the router rule written to `.omp/rules/gateway-router.md`.
//
//go:embed templates/rule.md
var Rule []byte
