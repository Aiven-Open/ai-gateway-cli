// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"slices"
	"strings"

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
	unlock, err := lockState(s.env, s.deps)
	if err != nil {
		return s.run.sharedFailure(s.deps, "waiting for the saved key", err)
	}
	defer unlock()
	prev, err := readKeyFile(s.env)
	if err != nil {
		return s.run.sharedFailure(s.deps, "reading the saved key", err)
	}
	if s.reusedKeyChanged(prev) {
		return s.failChangedKey(approved)
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

// wroteNothing reports whether no target is ready and none wrote a file, so no tool reads the key this run saved.
func (r *setupRun) wroteNothing() bool {
	return !slices.ContainsFunc(r.targets, func(o *targetOutcome) bool {
		return o.ready() || len(o.written) > 0
	})
}

// restoreKeyIfNothingReady puts back the key file the run found when no tool read or wrote
// anything: no tool uses the new key, and other tools keep the old one. The caller holds
// config.LockFile, so no login or logout of this version runs in between; the file is still
// compared first, for an older launcher that takes no lock.
func (s *setupSession) restoreKeyIfNothingReady(prev keyFile) {
	if !s.run.wroteNothing() {
		return
	}
	cur, err := readKeyFile(s.env)
	if err == nil && (!cur.existed || strings.TrimSpace(string(cur.data)) != s.key) {
		s.run.credential = "not saved by this run; " +
			"the key file changed during setup so it was left alone"
		return
	}
	if err == nil {
		err = restoreKey(s.env, s.deps, prev)
	}
	if err != nil {
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

// commitNoWrites ends a run with no file to write. A new key is saved only when every selected tool is already up to
// date, since each reads the key file. Otherwise the key is not saved.
func (s *setupSession) commitNoWrites() int {
	if !s.everyToolUnchanged() {
		s.run.credential = "not saved by this run; existing key files unchanged; " +
			"no configuration changes were approved"
		s.holdUnchangedToolsWithoutUsableKey()
		return s.run.finish()
	}
	unlock, err := lockState(s.env, s.deps)
	if err != nil {
		return s.failRotation("waiting for the saved key", err)
	}
	defer unlock()
	cur, err := readKeyFile(s.env)
	if err != nil {
		return s.failRotation("reading the saved key", err)
	}
	if s.reusedKeyChanged(cur) {
		return s.failChangedKey(nil)
	}
	saved, err := storedKey(s.env)
	if err != nil {
		return s.failRotation("reading the saved key", err)
	}
	if saved == s.key {
		s.run.credential = "unchanged; existing key files unchanged"
		return s.run.finish()
	}
	if err := s.saveRunKey(); err != nil {
		return s.failRotation("saving the key", err)
	}
	return s.run.finish()
}

func (s *setupSession) everyToolUnchanged() bool {
	return len(s.run.targets) > 0 && !slices.ContainsFunc(
		s.run.targets,
		func(o *targetOutcome) bool { return o.status != statusUnchanged },
	)
}

// holdUnchangedToolsWithoutUsableKey stops reporting up-to-date tools as ready when the key file does not hold the key
// they were checked with, since a launch would not find it.
func (s *setupSession) holdUnchangedToolsWithoutUsableKey() {
	if saved, err := storedKey(s.env); err == nil && saved == s.key {
		return
	}
	for _, o := range s.run.targets {
		if o.status == statusUnchanged {
			o.status, o.reason = statusSkipped, "the key was not saved"
		}
	}
}

// failRotation ends a run whose key rotation failed: tools that were up to date are not reported as ready.
func (s *setupSession) failRotation(stage string, err error) int {
	for _, o := range s.run.targets {
		if o.status == statusUnchanged {
			o.status, o.reason = statusFailed, "the key was not updated: "+stage+" failed"
		}
	}
	return s.run.sharedFailure(s.deps, stage, err)
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
