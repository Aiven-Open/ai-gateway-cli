// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"slices"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
)

// resolveModels records each ready target's effective model, unchanged ones included.
func (s *setupSession) resolveModels() {
	for _, t := range s.selected {
		if t.preflightErr == nil {
			t.out.resolvedModel = resolvedToolModel(t.out.tool, s.env, s.table, t.plan, t.planSel)
		}
	}
}

func (s *setupSession) commitApproved(approved []approvedWrite) int {
	s.resolveModels()
	if len(approved) == 0 {
		return s.commitNoWrites()
	}
	prev, err := readKeyFile(s.env)
	if err != nil {
		return s.run.sharedFailure(s.deps, "reading the saved key", err)
	}
	if err := s.saveRunKey(); err != nil {
		for _, item := range approved {
			item.target.out.status = statusPending
			item.target.out.reason = "not attempted: saving the key failed"
		}
		return s.run.sharedFailure(s.deps, "saving the key", err)
	}
	for _, item := range approved {
		writeApproved(s.deps, item)
	}
	s.saveLauncherPrefs(approved)
	s.restoreKeyIfNothingReady(prev)
	return s.run.finish()
}

// restoreKeyIfNothingReady puts back the key file the run found when no selected tool ended up configured or
// unchanged: no tool reads the new key, and other tools keep the old one.
func (s *setupSession) restoreKeyIfNothingReady(prev keyFile) {
	if slices.ContainsFunc(s.run.targets, func(o *targetOutcome) bool { return o.ready() }) {
		return
	}
	if err := restoreKey(s.env, s.deps, prev); err != nil {
		_ = fail(s.deps, "putting back the previous key: "+err.Error())
		s.run.credential += "; putting back the previous key failed: " + err.Error()
		return
	}
	s.run.credential = "not saved by this run; " +
		"the new key was removed because no tool was configured"
	if prev.existed {
		s.run.credential = "not saved by this run; " +
			"the previous key was put back because no tool was configured"
	}
}

// commitNoWrites ends a run with no file to write. A new key is still saved when a selected tool is already up to
// date, since that tool reads the key file. A declined change never reaches here with an unchanged tool.
func (s *setupSession) commitNoWrites() int {
	unchanged := slices.ContainsFunc(s.run.targets, func(o *targetOutcome) bool {
		return o.status == statusUnchanged
	})
	if !unchanged {
		s.run.credential = "not saved by this run; existing key files unchanged; " +
			"no configuration changes were approved"
		return s.run.finish()
	}
	saved, err := storedKey(s.env)
	if err != nil {
		return s.run.sharedFailure(s.deps, "reading the saved key", err)
	}
	if saved == s.key {
		s.run.credential = "unchanged; existing key files unchanged"
		return s.run.finish()
	}
	if err := s.saveRunKey(); err != nil {
		return s.run.sharedFailure(s.deps, "saving the key", err)
	}
	return s.run.finish()
}

// saveRunKey saves the run's key and records where in the summary.
func (s *setupSession) saveRunKey() error {
	s.p.begin("Saving the key")
	where, err := saveKey(s.env, s.deps, s.key)
	if err != nil {
		s.p.failed("the key was not saved")
		return err
	}
	s.p.passed("saved to " + where)
	s.run.credential = "saved to " + where
	return nil
}

// saveLauncherPrefs persists the launcher model of each configured Pi, OpenCode or OMP target.
func (s *setupSession) saveLauncherPrefs(approved []approvedWrite) {
	prefUpdates := map[string]string{}
	for _, item := range approved {
		if item.target.out.status == statusConfigured && item.prefChanged && item.prefAlias != "" {
			prefUpdates[item.target.out.tool] = item.prefAlias
		}
	}
	if len(prefUpdates) == 0 {
		return
	}
	err := config.SaveModelPrefs(s.env.UserHome, s.env.BaseURL, prefUpdates, s.deps.WriteFile)
	if err == nil {
		return
	}
	_ = fail(s.deps, "saving launcher model preferences: "+err.Error())
	for _, item := range approved {
		if _, ok := prefUpdates[item.target.out.tool]; !ok {
			continue
		}
		item.target.out.status = statusFailed
		if len(item.target.out.written) > 0 {
			item.target.out.status = statusPartial
		}
		item.target.out.reason = "saving launcher model preferences failed: " + err.Error()
	}
}
