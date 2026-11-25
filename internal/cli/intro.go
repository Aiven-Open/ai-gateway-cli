// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import "strings"

// setupIntro is what every setup run prints first, before any check or prompt.
const setupIntro = "Aiven AI Gateway setup\n" +
	"Connects your coding tools to Aiven AI Gateway. Checks and saves your key,\n" +
	"then adds an Aiven profile to each tool. Nothing changes until you apply.\n" +
	"Source: https://github.com/Aiven-Open/ai-gateway-cli\n"

// printIntro prints setupIntro, its first line bold on a styled terminal, then a blank line.
func printIntro(u ui) {
	lines := strings.Split(strings.TrimSuffix(setupIntro, "\n"), "\n")
	u.printf("%s\n", u.th.bold(lines[0]))
	for _, l := range lines[1:] {
		u.line(0, l)
	}
	u.printf("\n")
}
