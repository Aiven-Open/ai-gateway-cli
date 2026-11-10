// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"fmt"
	"strings"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/models"
)

// toolLabels are the harnesses' product names, for headings and summaries; commands keep the binary names.
var toolLabels = map[string]string{"claude": "Claude Code", "codex": "Codex", "pi": "Pi", "opencode": "OpenCode", "omp": "OMP"}

func toolLabel(tool string) string {
	if l := toolLabels[tool]; l != "" {
		return l
	}
	return tool
}

// presenter is how setup, login and launch report their actual stages and outcomes on stderr. It receives only
// redacted text; keys, credential files and native arguments never reach it.
type presenter struct {
	ui ui
	// inline is the terminal's choice list, or nil when plain numbered prompts are required.
	inline selector
}

func newPresenter(env config.Env, deps Deps, plain bool) presenter {
	plain = plain || plainRequested(env.Environ)
	p := presenter{ui: newUI(deps.Stderr, deps.Term.StderrTTY, deps.Term.StderrWidth, plain)}
	if sel, ok := deps.Prompter.(selector); ok && !plain && deps.Term.StdinTTY && deps.Term.StderrTTY {
		p.inline = sel
	}
	return p
}

// begin announces a request or write before it starts; passed and failed end it.
func (p presenter) begin(s string)  { p.ui.line(2, s+"…") }
func (p presenter) passed(s string) { p.ui.status(2, green, "OK", s) }
func (p presenter) failed(s string) { p.ui.status(2, red, "FAILED", s) }

type targetStatus int

const (
	// statusPending is a target setup did not reach: a shared stage failed or the run stopped first.
	statusPending targetStatus = iota
	statusConfigured
	statusUnchanged
	statusDeclined
	// statusSkipped is a change that needs --yes without a terminal.
	statusSkipped
	statusMissing
	statusFailed
	// statusPartial is a multi-file write that stopped after changing some files.
	statusPartial
	statusCancelled
)

// targetOutcome is what setup did to one harness.
type targetOutcome struct {
	tool      string
	status    targetStatus
	asDefault bool
	// reason explains a status other than configured or unchanged; written lists files changed with recovery notes.
	reason  string
	written []string
}

// ready reports whether the harness can launch on what this run left: configured now or already.
func (o targetOutcome) ready() bool {
	return o.status == statusConfigured || o.status == statusUnchanged
}

// setupRun collects one setup invocation's outcomes for the final summary.
type setupRun struct {
	env        config.Env
	p          presenter
	credential string
	table      []models.Model
	targets    []*targetOutcome
	cancelled  bool
	checks     []protocolCheck
}

func (r *setupRun) add(tool string) *targetOutcome {
	o := &targetOutcome{tool: tool}
	r.targets = append(r.targets, o)
	return o
}

// sharedFailure ends a run whose credential stage failed: no target was configured, whatever passed preflight.
func (r *setupRun) sharedFailure(deps Deps, stage string, err error) int {
	_ = failGateway(deps, err)
	for _, o := range r.targets {
		if o.status == statusPending {
			o.reason = "not attempted: " + stage + " failed"
		}
	}
	if !strings.HasPrefix(r.credential, "saved") {
		r.credential = "not saved by this run; " + stage + " failed"
	}
	r.finish()
	return 1
}

// cancel ends a run before any credential was read or saved.
func (r *setupRun) cancel(deps Deps, err error) int {
	_ = fail(deps, err.Error())
	r.cancelled = true
	for _, o := range r.targets {
		if o.status == statusPending {
			o.reason = "not attempted: setup was cancelled before an answer"
		}
	}
	r.credential = "not saved by this run; no key was read"
	r.finish()
	return 1
}

// finish prints the summary and returns the exit code: nonzero when any target failed or the run was cancelled.
func (r *setupRun) finish() int {
	u := r.p.ui
	code, complete := 0, true
	for _, o := range r.targets {
		switch o.status {
		case statusMissing, statusFailed, statusPartial, statusCancelled, statusPending:
			code = 1
		}
		if !o.ready() {
			complete = false
		}
		r.cancelled = r.cancelled || o.status == statusCancelled
	}
	switch {
	case r.cancelled:
		u.heading("Setup cancelled")
	case code != 0:
		u.heading("Setup finished with errors")
	case complete:
		u.heading("Setup complete")
	default:
		u.heading("Setup finished; some tools were not configured")
	}
	labelWidth := len("Credential")
	for _, o := range r.targets {
		labelWidth = max(labelWidth, cells(toolLabel(o.tool)))
	}
	u.line(2, pad("Credential", labelWidth)+"  "+r.credential)
	if len(r.checks) > 0 {
		u.heading("Basic protocol checks")
		u.line(2, "These do not show that the tool's own requests are accepted.")
		for _, check := range r.checks {
			text := fmt.Sprintf("%s with %s: HTTP %d", check.protocol.Label(), check.model, check.status)
			if check.status == 0 {
				text = check.protocol.Label() + " with " + check.model + ": no HTTP response"
			}
			if check.err != nil {
				u.status(2, red, "FAILED", text+"; "+check.err.Error())
			} else {
				u.status(2, green, "OK", text+"; basic check passed")
			}
		}
	}
	var ready, later, attention []*targetOutcome
	for _, o := range r.targets {
		kind, word := red, "failed"
		switch o.status {
		case statusConfigured:
			kind, word = green, "configured"
		case statusUnchanged:
			kind, word = green, "unchanged"
		case statusDeclined:
			kind, word = yellow, "declined"
		case statusSkipped:
			kind, word = yellow, "not configured"
		case statusMissing:
			word = "not installed"
		case statusPartial:
			word = "partially configured"
		case statusCancelled:
			kind, word = yellow, "cancelled"
		case statusPending:
			kind, word = yellow, "not configured"
		}
		u.printf("  %s  %s", pad(toolLabel(o.tool), labelWidth), u.paint(kind, word))
		if o.reason != "" {
			u.printf("; %s", safe(o.reason))
		}
		u.printf("\n")
		indent := 2 + labelWidth + 2
		for _, w := range o.written {
			u.line(indent, "written: "+w)
		}
		if o.ready() {
			if o.asDefault && o.status == statusConfigured {
				u.line(indent, "Aiven is now the native default")
				if o.tool == "omp" {
					u.line(indent, "OMP project settings and enabled-model scope can override this stored default.")
					u.line(indent, "A disabled Aiven provider must be enabled in OMP before starting a session.")
				}
			} else {
				u.line(indent, "native default left as it was")
			}
			ready = append(ready, o)
		} else if o.status == statusDeclined {
			later = append(later, o)
		} else {
			attention = append(attention, o)
		}
	}
	if len(ready) > 0 {
		u.heading("Start a session")
		for _, o := range ready {
			u.command(2, "aiven-ai-gateway launch "+o.tool)
		}
		u.heading("Or start the native tool directly")
		for _, o := range ready {
			if !u.command(2, directCommand(o.tool, r.table, o.asDefault && o.status == statusConfigured)) {
				u.line(2, toolLabel(o.tool)+": direct command unavailable because an argument contains terminal controls.")
				u.line(2, "Use aiven-ai-gateway launch "+o.tool+" instead; its arguments remain literal.")
			}
		}
		for _, o := range ready {
			if o.tool == "claude" {
				u.line(2, "Direct claude --settings also loads extra settings kept in that file;")
				u.line(2, "launch passes only the launcher's own settings.")
			}
		}
		u.heading("Optional bash/zsh shortcuts")
		u.line(2, "Copy into your shell configuration if you want them; setup never edits it.")
		u.line(2, "Native arguments follow the shortcut, as after --.")
		var unpicked []string
		for _, o := range ready {
			if o.tool == "pi" || o.tool == "opencode" || o.tool == "omp" {
				unpicked = append(unpicked, toolLabel(o.tool))
			}
		}
		if len(unpicked) > 0 {
			u.line(2, "With native arguments, add your own model flag for "+strings.Join(unpicked, ", ")+".")
		}
		for _, o := range ready {
			u.command(2, "alias "+o.tool+"-aiven='aiven-ai-gateway launch "+o.tool+" --'")
		}
	}
	if len(later) > 0 {
		u.heading("Configure later")
		for _, o := range later {
			u.command(2, "aiven-ai-gateway setup "+o.tool)
		}
	}
	if len(attention) > 0 {
		u.heading("Needs attention")
		for _, o := range attention {
			if o.status == statusPartial {
				u.line(2, "Review the written files and recovery copies for "+toolLabel(o.tool)+", then run:")
			}
			u.command(2, "aiven-ai-gateway setup "+o.tool)
		}
	}
	return code
}

// directCommand is the native invocation that uses what setup wrote. With Aiven as the default the bare binary does.
func directCommand(tool string, table []models.Model, asDefault bool) string {
	t := setupTools[tool]
	switch {
	case asDefault && t.bin != "":
		return t.bin
	}
	return t.run(table)
}

// shellQuote makes s one literal word for bash, zsh and sh. Words of plainly safe characters stay as they are, so
// the common command reads as typed; anything else is single-quoted.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+:,./-") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
