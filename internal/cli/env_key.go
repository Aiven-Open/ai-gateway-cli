// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/creds"
)

// envKeyRefuse is the option both renderers start on: the environment's key names no gateway, so sending it takes a
// deliberate choice.
const envKeyRefuse = 1

// confirmEnvKey asks whether to send the environment's key to env's gateway.
func confirmEnvKey(deps Deps, p presenter, env config.Env, envKey string) (bool, error) {
	if p.inline != nil {
		return confirmEnvKeyInline(p, env, envKey)
	}
	return confirmEnvKeyPlain(deps, p, env, envKey)
}

func confirmEnvKeyInline(p presenter, env config.Env, envKey string) (bool, error) {
	c := choice{
		title:    "Use " + creds.EnvVar + " (" + maskKey(envKey) + ")?",
		subtitle: "Gateway: " + safe(env.BaseURL),
		options: []option{
			{label: "Use " + creds.EnvVar, detail: maskKey(envKey) + " with " + safe(env.BaseURL)},
			{label: "Enter a different key", detail: "paste key with hidden input"},
			{label: "Cancel", detail: "exit without reading or saving a key"},
		},
		cursor: envKeyRefuse,
		rail:   true,
		theme:  &p.ui.th,
	}
	picked, err := p.inline.Select(c)
	if err != nil {
		return false, err
	}
	if picked[0] == 2 {
		return false, errCancelled
	}
	return picked[0] == 0, nil
}

func confirmEnvKeyPlain(deps Deps, p presenter, env config.Env, envKey string) (bool, error) {
	p.ui.line(2, "Key source: "+creds.EnvVar)
	p.ui.line(2, "Key: "+maskKey(envKey))
	p.ui.line(2, "Use this key with "+safe(env.BaseURL)+"?")
	for i, label := range []string{"Use " + creds.EnvVar, "Enter a different key", "Cancel"} {
		if i == envKeyRefuse {
			label += " (default)"
		}
		p.ui.line(4, strconv.Itoa(i+1)+") "+label)
	}
	prompt := "Choose key option (Enter for " + strconv.Itoa(envKeyRefuse+1) + "): "
	for range 3 {
		answer, err := deps.Prompter.ReadLine(prompt)
		if errors.Is(err, io.EOF) || errors.Is(err, errInputEnded) {
			return false, errInputEnded
		}
		if err != nil {
			return false, err
		}
		picked, ok := envKeyAnswer(answer)
		switch {
		case !ok:
			p.ui.line(2, "Choose a number from 1 to 3.")
		case picked == 0:
			return true, nil
		case picked == 2:
			return false, errCancelled
		default:
			return false, nil
		}
	}
	return false, errors.New("no answer: expected key option number")
}

// envKeyAnswer is the option index an answer picks; a blank answer takes envKeyRefuse.
func envKeyAnswer(answer string) (int, bool) {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "":
		return envKeyRefuse, true
	case "1", "y", "yes":
		return 0, true
	case "2", "n", "no":
		return 1, true
	case "3", "c", "cancel":
		return 2, true
	}
	return 0, false
}
