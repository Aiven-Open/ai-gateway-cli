// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// diffNote follows every diff Show file changes prints.
const diffNote = "Values that look like credentials are shown as redacted placeholders."

// reviewAction is one answer to the review question.
type reviewAction int

const (
	actApply reviewAction = iota
	actShowChanges
	actBack
	actCancel
)

// reviewMenu is the review question for pending: its title, options and the action of each.
func reviewMenu(pending []pendingPreview, allowBack bool) (string, []option, []reviewAction) {
	title := "Apply these changes to " + toolLabel(pending[0].target.out.tool) + "?"
	apply := option{label: "Apply", detail: "write the files shown"}
	back := option{label: "Back", detail: "change default provider or model"}
	if len(pending) > 1 {
		title = "Apply configuration changes?"
		apply = option{
			label:  "Apply all",
			detail: fmt.Sprintf("write all %d tool configurations", len(pending)),
		}
		back.detail = "change default provider or models"
	}
	opts := []option{
		apply,
		{label: "Show file changes", detail: "print the redacted diff, then ask again"},
	}
	acts := []reviewAction{actApply, actShowChanges}
	if allowBack {
		opts, acts = append(opts, back), append(acts, actBack)
	}
	opts = append(opts, option{label: "Cancel", detail: "leave keys and files unchanged"})
	return title, opts, append(acts, actCancel)
}

// approveInteractive asks until the answer is Apply, Back or Cancel, printing the file changes when asked.
func approveInteractive(
	deps Deps,
	p presenter,
	pending []pendingPreview,
	allowBack bool,
) ([]approvedWrite, reviewOutcome) {
	for range 10 {
		act, err := askReview(deps, p, pending, allowBack)
		switch {
		case errors.Is(err, errBack), err == nil && act == actBack:
			return nil, reviewBack
		case err != nil:
			return nil, stopReview(deps, pending, err)
		case act == actApply:
			return approvedFrom(pending), reviewApproved
		case act == actCancel:
			return nil, cancelReview(pending)
		}
		if err := showChanges(p, pending); err != nil {
			return nil, failReview(deps, pending, err)
		}
	}
	return nil, failReview(deps, pending, errors.New("no answer: expected apply or cancel"))
}

func askReview(
	deps Deps,
	p presenter,
	pending []pendingPreview,
	allowBack bool,
) (reviewAction, error) {
	title, opts, acts := reviewMenu(pending, allowBack)
	p.ui.printf("\n")
	if p.inline != nil {
		picked, err := p.inline.Select(
			choice{title: title, options: opts, allowBack: allowBack, theme: &p.ui.th},
		)
		if err != nil {
			return 0, err
		}
		return acts[picked[0]], nil
	}
	p.ui.line(2, strings.TrimSuffix(title, "?")+":")
	for i, o := range opts {
		p.ui.line(4, fmt.Sprintf("%d) %s", i+1, o.label))
	}
	prompt := "Choose apply option (Enter for 1): "
	if len(pending) == 1 {
		prompt = fmt.Sprintf("Apply option for %s (Enter for 1): ", pending[0].target.out.tool)
	}
	for range 10 {
		answer, err := deps.Prompter.ReadLine(prompt)
		if err != nil {
			return 0, errInputEnded
		}
		if act, ok := reviewAnswer(strings.ToLower(strings.TrimSpace(answer)), acts); ok {
			return act, nil
		}
		p.ui.line(2, fmt.Sprintf("Choose a number from 1 to %d.", len(opts)))
	}
	return 0, errors.New("no answer: expected apply option number")
}

// reviewAnswer reads a plain answer: a number, or the words the numbered prompts have always taken.
func reviewAnswer(answer string, acts []reviewAction) (reviewAction, bool) {
	switch answer {
	case "", "y", "yes":
		return actApply, true
	case "v", "view":
		return actShowChanges, true
	case "n", "no", "c", "cancel":
		return actCancel, true
	case "b", "back":
		return actBack, slices.Contains(acts, actBack)
	}
	if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(acts) {
		return acts[n-1], true
	}
	return 0, false
}

// showChanges prints the full redacted diff of every pending tool, model lists included, then diffNote.
func showChanges(p presenter, pending []pendingPreview) error {
	for _, item := range pending {
		_, diff, err := changes(item.files, item.names)
		if err != nil {
			return err
		}
		if len(pending) > 1 {
			p.ui.heading(toolLabel(item.target.out.tool))
		}
		p.ui.printf("\n")
		p.ui.block(diff)
	}
	p.ui.note(2, diffNote)
	return nil
}

// cancelReview declines a single tool, which exits zero, and cancels several.
func cancelReview(pending []pendingPreview) reviewOutcome {
	status, outcome := statusCancelled, reviewCancelled
	if len(pending) == 1 {
		status, outcome = statusDeclined, reviewApproved
	}
	for _, item := range pending {
		item.target.out.status, item.target.out.reason = status, "no files written"
	}
	return outcome
}

func stopReview(deps Deps, pending []pendingPreview, err error) reviewOutcome {
	if len(pending) == 1 {
		err = fmt.Errorf("%s: %w", pending[0].target.out.tool, err)
	}
	_ = fail(deps, err.Error())
	for _, item := range pending {
		item.target.out.status, item.target.out.reason = statusCancelled, "no files written"
	}
	return reviewCancelled
}

func failReview(deps Deps, pending []pendingPreview, err error) reviewOutcome {
	for _, item := range pending {
		item.target.out.status, item.target.out.reason = statusFailed, err.Error()
		_ = fail(deps, item.target.out.tool+": "+err.Error())
	}
	return reviewApproved
}

func approvedFrom(pending []pendingPreview) []approvedWrite {
	approved := make([]approvedWrite, 0, len(pending))
	for _, item := range pending {
		approved = append(approved, approvedWrite{
			target:      item.target,
			write:       item.write,
			names:       item.names,
			prefAlias:   item.prefAlias,
			prefChanged: item.prefChanged,
		})
	}
	return approved
}
