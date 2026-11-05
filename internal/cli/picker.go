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

const laterHint = "To configure a harness later, run: aiven-ai-gateway setup <pi|opencode|codex|claude|omp>"

// pickTools asks which tools to configure, offering those found on PATH, or every tool when none is. Cancelling
// returns errCancelled; choosing none returns no tools.
func pickTools(deps Deps, p presenter) ([]string, error) {
	var offered []string
	for _, t := range toolNames {
		if _, err := deps.Runner.LookPath(t); err == nil {
			offered = append(offered, t)
		}
	}
	found := "found on PATH"
	if len(offered) == 0 {
		offered, found = toolNames, "none found on PATH"
	}
	if p.inline != nil {
		c := choice{title: "Configure tools to use the gateway (" + found + ")", multi: true}
		for _, t := range offered {
			c.options = append(c.options, option{label: toolLabel(t), detail: t})
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
			p.ui.line(0, "Selected: "+c.chosen(indexes))
		}
		return picked, nil
	}
	p.ui.line(0, "Configure tools to use the gateway ("+found+"):")
	for i, t := range offered {
		p.ui.line(2, fmt.Sprintf("%d) %s (%s)", i+1, toolLabel(t), t))
	}
	for range 3 {
		answer, err := deps.Prompter.ReadLine(`Numbers or names separated by commas, "all", or Enter to skip: `)
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

// parsePick reads a menu answer: numbers into offered, any tool name, "all", or nothing (also s, skip, n, no, none) for none.
func parsePick(answer string, offered []string) ([]string, bool) {
	answer = strings.ToLower(strings.TrimSpace(answer))
	switch answer {
	case "", "s", "skip", "n", "no", "none":
		return nil, true
	case "all":
		return offered, true
	}
	var picked []string
	for field := range strings.FieldsFuncSeq(answer, func(r rune) bool { return r == ',' || r == ' ' }) {
		name := field
		if n, err := strconv.Atoi(field); err == nil && n >= 1 && n <= len(offered) {
			name = offered[n-1]
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
	var installed []string
	for _, tool := range toolNames {
		if _, err := deps.Runner.LookPath(tool); err == nil {
			installed = append(installed, tool)
		}
	}
	if len(installed) == 0 {
		return "", errors.New("no supported harness is installed; install one, then run aiven-ai-gateway setup <harness>")
	}
	if p.inline != nil {
		c := choice{title: "Launch one installed harness"}
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
		answer, err := deps.Prompter.ReadLine("Launch one installed harness (number or name), or Enter to cancel: ")
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
