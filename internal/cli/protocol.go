// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/aiven-open/ai-gateway-cli/internal/gateway"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/claude"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

// errNotGranted is a configured model the key does not grant. The tool would keep requesting it, so setup checks
// nothing else and saves neither the key nor any tool.
var errNotGranted = errors.New("which this key does not grant")

func notGranted(path, model string) error {
	return fmt.Errorf("%s selects %s, %w; choose a granted model there (aiven-ai-gateway models lists them), then rerun setup", path, model, errNotGranted)
}

type protocolCheck struct {
	protocol models.Protocol
	model    string
	status   int
	err      error
}

// same reports whether c checks the request d does.
func (c protocolCheck) same(d protocolCheck) bool {
	return c.protocol == d.protocol && c.model == d.model
}

// toolChecks derives the protocols from the configuration this run will write.
func toolChecks(tool string, table []models.Model, plan harness.Plan) ([]protocolCheck, error) {
	switch tool {
	case "pi", "omp":
		byProtocol := map[models.Protocol]string{}
		for _, m := range models.Sorted(table) {
			protocol := models.ModelProtocol(m.Alias)
			if byProtocol[protocol] == "" {
				byProtocol[protocol] = m.Alias
			}
		}
		for _, alias := range []string{models.CodexOrFirst(table), models.SonnetOrFirst(table)} {
			byProtocol[models.ModelProtocol(alias)] = alias
		}
		var checks []protocolCheck
		for _, protocol := range []models.Protocol{models.ChatCompletions, models.Messages, models.Responses} {
			if alias := byProtocol[protocol]; alias != "" {
				checks = append(checks, protocolCheck{protocol: protocol, model: alias})
			}
		}
		return checks, nil
	case "codex":
		var cfg map[string]any
		if err := toml.Unmarshal(plan.Files[len(plan.Files)-1].Data, &cfg); err != nil {
			return nil, fmt.Errorf("read proposed Codex profile: %w", err)
		}
		alias, _ := cfg["model"].(string)
		if _, granted := models.Find(table, alias); !granted {
			return nil, notGranted(plan.Files[len(plan.Files)-1].Path, alias)
		}
		return []protocolCheck{{protocol: models.Responses, model: alias}}, nil
	case "claude":
		var checks []protocolCheck
		for _, f := range plan.Files {
			alias, err := claude.Selected(f.Data)
			if err != nil {
				return nil, fmt.Errorf("read proposed Claude settings: %w", err)
			}
			if _, granted := models.Find(table, alias); !granted {
				return nil, notGranted(f.Path, alias)
			}
			if check := (protocolCheck{protocol: models.Messages, model: alias}); !slices.ContainsFunc(checks, check.same) {
				checks = append(checks, check)
			}
		}
		return checks, nil
	case "opencode":
		return []protocolCheck{{protocol: models.ChatCompletions, model: models.SonnetOrFirst(table)}}, nil
	}
	return nil, fmt.Errorf("unresolved configured protocols for %s", tool)
}

// blocked fails each pending tool whose configured model the key does not grant, leaves the other pending tools not
// attempted, and reports whether there was one. The key is shared, so saving it for the others would leave the
// refused tool sending it for a model it does not grant.
func blocked(valid []*targetOutcome, plans map[string]harness.Plan, table []models.Model) bool {
	refused := false
	for _, out := range valid {
		if out.status != statusPending {
			continue
		}
		if _, err := toolChecks(out.tool, table, plans[out.tool]); errors.Is(err, errNotGranted) {
			out.status, out.reason, refused = statusFailed, err.Error(), true
		}
	}
	for _, out := range valid {
		if refused && out.status == statusPending {
			out.reason = "the key was not saved, since another selected tool's model is not granted"
		}
	}
	return refused
}

// checkProtocols sends exactly one minimal request for each distinct configured protocol and model, and fails a tool
// only on its own checks: a model's refusal says nothing of another model on the same protocol. A successful reply
// establishes that model's served route.
func checkProtocols(ctx context.Context, gw gateway.Client, key string, valid []*targetOutcome, plans map[string]harness.Plan, table []models.Model, run *setupRun) []*targetOutcome {
	dependencies := map[string][]protocolCheck{}
	var checks []protocolCheck
	for _, out := range valid {
		if out.status != statusPending {
			continue
		}
		required, err := toolChecks(out.tool, table, plans[out.tool])
		if err != nil {
			out.status, out.reason = statusFailed, err.Error()
			continue
		}
		for _, check := range required {
			dependencies[out.tool] = append(dependencies[out.tool], check)
			if !slices.ContainsFunc(checks, check.same) {
				checks = append(checks, check)
			}
		}
	}
	slices.SortFunc(checks, func(a, b protocolCheck) int {
		return cmp.Or(strings.Compare(string(a.protocol), string(b.protocol)), strings.Compare(a.model, b.model))
	})
	authFailed := false
	for i := range checks {
		check := &checks[i]
		if authFailed || ctx.Err() != nil {
			check.err = fmt.Errorf("not attempted after authentication failure or cancellation")
			continue
		}
		run.p.begin("Basic check: " + check.protocol.Label() + " with " + check.model + "; it may incur usage")
		check.status, check.err = gw.Probe(ctx, key, check.model, check.protocol)
		text := fmt.Sprintf("%s with %s: HTTP %d", check.protocol.Label(), check.model, check.status)
		if check.status == 0 {
			text = check.protocol.Label() + " with " + check.model + ": no HTTP response"
		}
		if check.err != nil {
			run.p.failed(text + "; " + check.err.Error())
			authFailed = rejected(check.err)
		} else {
			run.p.passed(text + "; basic check passed")
		}
	}
	run.checks = checks
	var ready []*targetOutcome
	for _, out := range valid {
		if out.status != statusPending {
			continue
		}
		if err := ctx.Err(); err != nil {
			out.status, out.reason = statusFailed, "basic checks cancelled: "+err.Error()
			continue
		}
		for _, check := range checks {
			if (authFailed || slices.ContainsFunc(dependencies[out.tool], check.same)) && check.err != nil {
				out.status = statusFailed
				out.reason = check.protocol.Label() + " basic check failed: " + check.err.Error()
				break
			}
		}
		if out.status == statusPending {
			ready = append(ready, out)
		}
	}
	return ready
}
