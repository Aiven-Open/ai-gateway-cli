// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// toolNames are the tools setup can configure, in menu order; each is also the name of its binary.
var toolNames = []string{"pi", "opencode", "codex", "claude", "omp"}

const laterHint = "To configure a harness later, run: " +
	"aiven-ai-gateway setup <pi|opencode|codex|claude|omp>"

// discoverTools partitions toolNames into installed (found on PATH) and missing.
func discoverTools(deps Deps) (installed, missing []string) {
	for _, t := range toolNames {
		if _, err := deps.Runner.LookPath(t); err == nil {
			installed = append(installed, t)
		} else {
			missing = append(missing, t)
		}
	}
	return installed, missing
}

// pickToolsBeat1 runs Beat 1 for bare setup: installed harnesses are pre-selected with live status badges,
// plus a dimmed missing-tools footer when some harnesses are not on PATH.
func pickToolsBeat1(
	deps Deps,
	p presenter,
	candidates []*setupTarget,
	missing []string,
	prevPicked map[string]bool,
) ([]string, error) {
	offered := make([]string, 0, len(candidates))
	for _, t := range candidates {
		offered = append(offered, t.out.tool)
	}
	if p.inline != nil {
		subtitle := ""
		if len(missing) > 0 {
			subtitle = "not on PATH: " + strings.Join(missing, ", ")
		}
		c := choice{
			title:    "Harnesses",
			subtitle: subtitle,
			multi:    true,
			rail:     true,
			theme:    &p.ui.th,
		}
		for _, t := range candidates {
			picked := true
			if prevPicked != nil {
				picked = prevPicked[t.out.tool]
			}
			detail := t.badge
			if detail == "" {
				detail = t.out.tool
			}
			c.options = append(c.options, option{
				label:  toolLabel(t.out.tool),
				detail: detail,
				picked: picked,
			})
		}
		indexes, err := p.inline.Select(c)
		if err != nil {
			return nil, err
		}
		var picked []string
		for _, i := range indexes {
			picked = append(picked, offered[i])
		}
		if len(picked) > 0 {
			p.ui.railDone("Harnesses", c.chosen(indexes))
		}
		return picked, nil
	}
	p.ui.line(0, "Configure tools to use the gateway (found on PATH):")
	for i, t := range candidates {
		entry := fmt.Sprintf("%d) %s (%s)", i+1, toolLabel(t.out.tool), t.out.tool)
		if t.badge != "" {
			entry += " — " + t.badge
		}
		p.ui.line(2, entry)
	}
	if len(missing) > 0 {
		p.ui.note(2, "Not on PATH: "+strings.Join(missing, ", "))
	}
	for range 3 {
		answer, err := deps.Prompter.ReadLine(
			`Numbers or names separated by commas, "all", or Enter to skip: `,
		)
		if errors.Is(err, io.EOF) || errors.Is(err, errInputEnded) {
			return nil, errInputEnded
		} else if err != nil {
			return nil, err
		}
		if picked, ok := parsePick(answer, offered); ok {
			return picked, nil
		}
		p.ui.line(0, "Choose from the list, for example 1,2 or pi,codex.")
	}
	return nil, errors.New("no answer: expected tool numbers or names")
}

// parsePick reads a menu answer: numbers into offered, any known tool name (unless offeredOnly is true, which
// restricts names to offered), "all", or nothing (also s, skip, n, no, none) for none.
func parsePick(answer string, offered []string) ([]string, bool) {
	return parsePickList(answer, offered, false)
}

func parsePickOffered(answer string, offered []string) ([]string, bool) {
	return parsePickList(answer, offered, true)
}

// parseModelPicks reads numbers into offered and model ids exactly as displayed: ids keep their case, and no word is a
// shortcut for the tool picker's choices.
func parseModelPicks(answer string, offered []string) ([]string, bool) {
	var picked []string
	sep := func(r rune) bool { return r == ',' || r == ' ' }
	for field := range strings.FieldsFuncSeq(answer, sep) {
		name := field
		if !slices.Contains(offered, field) {
			n, err := strconv.Atoi(field)
			if err != nil || n < 1 || n > len(offered) {
				return nil, false
			}
			name = offered[n-1]
		}
		if !slices.Contains(picked, name) {
			picked = append(picked, name)
		}
	}
	return picked, true
}

func parsePickList(answer string, offered []string, offeredOnly bool) ([]string, bool) {
	answer = strings.ToLower(strings.TrimSpace(answer))
	switch answer {
	case "", "s", "skip", "n", "no", "none":
		return nil, true
	case "all":
		return offered, true
	}
	var picked []string
	sep := func(r rune) bool { return r == ',' || r == ' ' }
	for field := range strings.FieldsFuncSeq(answer, sep) {
		name := field
		if n, err := strconv.Atoi(field); err == nil && n >= 1 && n <= len(offered) {
			name = offered[n-1]
		} else if offeredOnly && !slices.Contains(offered, field) {
			return nil, false
		} else if setupTools[field].prepare == nil {
			return nil, false
		}
		if !slices.Contains(picked, name) {
			picked = append(picked, name)
		}
	}
	return picked, true
}

// pickLaunch asks for exactly one installed harness. An empty answer or a cancellation launches nothing.
func pickLaunch(deps Deps, p presenter) (string, error) {
	installed, _ := discoverTools(deps)
	if len(installed) == 0 {
		return "", errors.New(
			"no supported harness is installed; install one, then run aiven-ai-gateway setup <harness>",
		)
	}
	if p.inline != nil {
		c := choice{title: "Launch one installed harness", theme: &p.ui.th}
		for _, t := range installed {
			c.options = append(c.options, option{label: toolLabel(t), detail: t})
		}
		indexes, err := p.inline.Select(c)
		if err != nil {
			return "", err
		}
		return installed[indexes[0]], nil
	}
	for i, t := range installed {
		p.ui.line(2, fmt.Sprintf("%d) %s (%s)", i+1, toolLabel(t), t))
	}
	for range 3 {
		answer, err := deps.Prompter.ReadLine(
			"Launch one installed harness (number or name), or Enter to cancel: ",
		)
		if errors.Is(err, io.EOF) || errors.Is(err, errInputEnded) {
			return "", errInputEnded
		}
		if err != nil {
			return "", err
		}
		picked, ok := parsePick(answer, installed)
		if ok && len(picked) == 0 {
			return "", nil
		}
		if ok && len(picked) == 1 && slices.Contains(installed, picked[0]) {
			return picked[0], nil
		}
		p.ui.line(0, "Choose exactly one installed harness from the list.")
	}
	return "", errors.New("no answer: expected one installed harness")
}
