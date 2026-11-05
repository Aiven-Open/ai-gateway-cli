// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli_test

import "io"

// scriptedPrompter answers ReadLine from a queue and records the prompts; an exhausted queue is end of input.
type scriptedPrompter struct {
	answers []string
	prompts []string
}

func (p *scriptedPrompter) ReadSecret(string) (string, error) { return "", io.EOF }

func (p *scriptedPrompter) ReadLine(prompt string) (string, error) {
	p.prompts = append(p.prompts, prompt)
	if len(p.answers) == 0 {
		return "", io.EOF
	}
	a := p.answers[0]
	p.answers = p.answers[1:]
	return a, nil
}
