// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/gateway"
	"github.com/aiven-open/ai-gateway-cli/internal/harness"
	"github.com/aiven-open/ai-gateway-cli/internal/harness/claude"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

const maxConcurrentProbes = 4

// errNotGranted is a configured model the key does not grant. The tool would keep requesting it, so setup checks
// nothing else and saves neither the key nor any tool.
var errNotGranted = errors.New("which this key does not grant")

func notGranted(path, model string, table []models.Model) error {
	if alias := models.ProviderAlias(table, model); alias != "" {
		return fmt.Errorf("%s selects %s, %w (try %s; aiven-ai-gateway models lists granted "+
			"models), then update it or rerun setup interactively", path, model, errNotGranted, alias)
	}
	return fmt.Errorf("%s selects %s, %w; choose a granted model there "+
		"(aiven-ai-gateway models lists them), then rerun setup", path, model, errNotGranted)
}

type probeKey struct {
	protocol models.Protocol
	model    string
}

type probeResult struct {
	status   int
	err      error
	duration time.Duration
}

type protocolCheck struct {
	protocol models.Protocol
	model    string
	status   int
	err      error
	duration time.Duration
}

// same reports whether c checks the request d does.
func (c protocolCheck) same(d protocolCheck) bool {
	return c.protocol == d.protocol && c.model == d.model
}

// toolChecks derives the protocols from the configuration this run will write.
func toolChecks(
	env config.Env,
	tool string,
	table []models.Model,
	plan harness.Plan,
	sel harness.ModelSelection,
) ([]protocolCheck, error) {
	chosen, err := sel.ResolveMain(table)
	if err != nil {
		return nil, err
	}
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
		if chosen != "" {
			byProtocol[models.ModelProtocol(chosen)] = chosen
		}
		var checks []protocolCheck
		protocols := []models.Protocol{models.ChatCompletions, models.Messages, models.Responses}
		for _, protocol := range protocols {
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
			return nil, notGranted(plan.Files[len(plan.Files)-1].Path, alias, table)
		}
		return []protocolCheck{{protocol: models.Responses, model: alias}}, nil
	case "claude":
		planned := make(map[string][]byte, len(plan.Files))
		for _, f := range plan.Files {
			alias, err := claude.Selected(f.Data)
			if err != nil {
				return nil, fmt.Errorf("read proposed Claude settings: %w", err)
			}
			if _, granted := models.Find(table, alias); !granted {
				return nil, notGranted(f.Path, alias, table)
			}
			planned[f.Path] = f.Data
		}
		path, alias, err := claude.Effective(table, env.UserHome, env.WorkDir, planned)
		if err != nil {
			return nil, fmt.Errorf("read effective Claude settings: %w", err)
		}
		if _, granted := models.Find(table, alias); !granted {
			return nil, notGranted(path, alias, table)
		}
		return []protocolCheck{{protocol: models.Messages, model: alias}}, nil
	case "opencode":
		model := cmp.Or(chosen, models.SonnetOrFirst(table))
		return []protocolCheck{{protocol: models.ChatCompletions, model: model}}, nil
	}
	return nil, fmt.Errorf("unresolved configured protocols for %s", tool)
}

// blocked fails each pending tool whose configured model the key does not grant, leaves the other pending tools not
// attempted, and reports whether there was one. The key is shared, so saving it for the others would leave the
// refused tool sending it for a model it does not grant.
func blocked(env config.Env, targets []*setupTarget, table []models.Model) bool {
	refused := false
	for _, t := range targets {
		if t.out.status != statusPending {
			continue
		}
		if _, err := toolChecks(
			env,
			t.out.tool,
			table,
			t.plan,
			t.out.selection,
		); errors.Is(
			err,
			errNotGranted,
		) {
			t.out.status, t.out.reason, refused = statusFailed, err.Error(), true
		}
	}
	for _, t := range targets {
		if refused && t.out.status == statusPending {
			t.out.reason = "the key was not saved, since another selected tool's model is not granted"
		}
	}
	return refused
}

// checkProtocols sends at most one minimal request for each distinct uncached (protocol, model) concurrently under one
// stderr owner, memoizing completed results in cache across Back navigation and failing each tool only on its own
// dependencies (unless authentication fails).
func checkProtocols(
	ctx context.Context,
	gw gateway.Client,
	key string,
	targets []*setupTarget,
	table []models.Model,
	run *setupRun,
	cache map[probeKey]probeResult,
) []*setupTarget {
	dependencies := map[string][]protocolCheck{}
	var checks []protocolCheck
	for _, t := range targets {
		if t.out.status != statusPending {
			continue
		}
		required, err := toolChecks(run.env, t.out.tool, table, t.plan, t.out.selection)
		if err != nil {
			t.out.status, t.out.reason = statusFailed, err.Error()
			continue
		}
		for _, check := range required {
			dependencies[t.out.tool] = append(dependencies[t.out.tool], check)
			if !slices.ContainsFunc(checks, check.same) {
				checks = append(checks, check)
			}
		}
	}
	slices.SortFunc(checks, func(a, b protocolCheck) int {
		return cmp.Or(
			strings.Compare(string(a.protocol), string(b.protocol)),
			strings.Compare(a.model, b.model),
		)
	})

	var uncached []int
	for i := range checks {
		pk := probeKey{protocol: checks[i].protocol, model: checks[i].model}
		if res, ok := cache[pk]; ok {
			checks[i].status, checks[i].err, checks[i].duration = res.status, res.err, res.duration
		} else {
			uncached = append(uncached, i)
		}
	}

	var authFailed atomic.Bool
	for _, c := range checks {
		if rejected(c.err) {
			authFailed.Store(true)
			break
		}
	}

	if len(uncached) > 0 && !authFailed.Load() {
		run.p.ui.railNote(
			"Sending one 1-token check per configured protocol and model (may incur usage).",
		)
		probeCtx, cancelProbes := context.WithCancel(ctx)
		defer cancelProbes()
		results := make([]probeResult, len(uncached))
		sem := make(chan struct{}, maxConcurrentProbes)
		var wg sync.WaitGroup
		for slot, idx := range uncached {
			check := checks[idx]
			wg.Add(1)
			go func(slot int, proto models.Protocol, model string) {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-probeCtx.Done():
					results[slot] = probeResult{
						err: errors.New("not attempted after authentication failure or cancellation"),
					}
					return
				}
				if authFailed.Load() || probeCtx.Err() != nil {
					results[slot] = probeResult{
						err: errors.New("not attempted after authentication failure or cancellation"),
					}
					return
				}
				start := time.Now()
				status, err := gw.Probe(probeCtx, key, model, proto)
				dur := time.Since(start)
				if rejected(err) {
					authFailed.Store(true)
					cancelProbes()
				}
				results[slot] = probeResult{status: status, err: err, duration: dur}
			}(slot, check.protocol, check.model)
		}
		label := fmt.Sprintf("Checking %d protocol(s)…", len(uncached))
		if len(uncached) == 1 {
			c := checks[uncached[0]]
			label = fmt.Sprintf("Checking %s (%s)…", c.protocol.Label(), c.model)
		}
		_ = run.p.ui.runWithProgress(run.p.ui.railPrefix(), label, func() error {
			wg.Wait()
			return nil
		})
		for slot, idx := range uncached {
			res := results[slot]
			if authFailed.Load() && res.err != nil && !rejected(res.err) &&
				errors.Is(res.err, context.Canceled) {
				res.err = errors.New("not attempted after authentication failure or cancellation")
			}
			checks[idx].status = res.status
			checks[idx].err = res.err
			checks[idx].duration = res.duration
			if cache != nil && !errors.Is(res.err, context.Canceled) &&
				!authFailed.Load() {
				cache[probeKey{protocol: checks[idx].protocol, model: checks[idx].model}] = res
			}
		}
	}

	for _, check := range checks {
		printProtocolResult(run.p.ui, check)
	}
	run.checks = checks

	var ready []*setupTarget
	for _, t := range targets {
		if t.out.status != statusPending {
			continue
		}
		if err := ctx.Err(); err != nil {
			t.out.status, t.out.reason = statusFailed, "basic checks cancelled: "+err.Error()
			continue
		}
		for _, check := range checks {
			if (authFailed.Load() || slices.ContainsFunc(dependencies[t.out.tool], check.same)) &&
				check.err != nil {
				t.out.status = statusFailed
				t.out.reason = check.protocol.Label() + " basic check failed: " + check.err.Error()
				break
			}
		}
		if t.out.status == statusPending {
			ready = append(ready, t)
		}
	}
	return ready
}

func printProtocolResult(u ui, check protocolCheck) {
	ms := max(0, check.duration.Milliseconds())
	if u.color {
		statusText := fmt.Sprintf("HTTP %d (%dms)", check.status, ms)
		if check.status == 0 {
			statusText = "no HTTP response"
		}
		line := fmt.Sprintf(
			"%s %s %s %s %s",
			check.protocol.Label(),
			u.th.g.dot,
			check.model,
			u.th.g.dot,
			statusText,
		)
		if check.err != nil {
			u.railFail(line + "; " + check.err.Error())
		} else {
			u.railOK(line)
		}
		return
	}
	text := fmt.Sprintf(
		"%s with %s: HTTP %d (%dms)",
		check.protocol.Label(),
		check.model,
		check.status,
		ms,
	)
	if check.status == 0 {
		text = check.protocol.Label() + " with " + check.model + ": no HTTP response"
	}
	if check.err != nil {
		u.status(2, red, "FAILED", text+"; "+check.err.Error())
	} else {
		u.status(2, green, "OK", text+"; basic check passed")
	}
}
