// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"bytes"
	"strings"

	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

// changedKeyMessage is what a run reports when the saved key it reused no longer holds what it checked.
const changedKeyMessage = "the saved key changed while setup was running; no key was written"

// obtainRunKey is obtainKey that also records what the key file held when the run reuses the saved key,
// so the commit can tell a logout or a login that happened since.
func (s *setupSession) obtainRunKey() (string, []models.Model, error) {
	before, readErr := readKeyFile(s.env)
	key, table, err := obtainKey(s.ctx, s.env, s.deps, s.p, s.opts)
	if err == nil && readErr == nil && !s.opts.fromStdin && before.existed &&
		strings.TrimSpace(string(before.data)) == key {
		s.run.reusedKey = &before
	}
	return key, table, err
}

// reusedKeyChanged reports whether the run reused the saved key and the key file no longer holds it.
func (s *setupSession) reusedKeyChanged(now keyFile) bool {
	base := s.run.reusedKey
	return base != nil && (now.existed != base.existed || !bytes.Equal(now.data, base.data))
}

// failChangedKey ends a run whose reused key changed: nothing is written, and no tool that reads the key is
// reported as ready.
func (s *setupSession) failChangedKey(approved []approvedWrite) int {
	for _, item := range approved {
		item.target.out.status = statusPending
		item.target.out.reason = "not attempted: " + changedKeyMessage
	}
	for _, o := range s.run.targets {
		if o.status == statusUnchanged {
			o.status, o.reason = statusFailed, changedKeyMessage
		}
	}
	_ = fail(s.deps, changedKeyMessage)
	s.run.credential = "not saved by this run; " + changedKeyMessage
	s.run.finish()
	return 1
}
