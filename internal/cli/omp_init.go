// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package cli

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Aiven-Open/ai-gateway-cli/internal/config"
	"github.com/Aiven-Open/ai-gateway-cli/internal/fsutil"
	"github.com/Aiven-Open/ai-gateway-cli/internal/harness/omp"
	"github.com/Aiven-Open/ai-gateway-cli/internal/models"
	"github.com/Aiven-Open/ai-gateway-cli/internal/ompinit"
)

// ompInit writes the router's role mappings for the project in the working directory.
// Every value comes from a flag, else a prompt on a terminal, else the default.
func ompInit(ctx context.Context, args []string, env config.Env, deps Deps) int {
	flags, err := parseInitFlags(args)
	if err != nil {
		return fail(deps, err.Error())
	}
	_, table, code := grantedModels(ctx, "omp", env, deps)
	if code != 0 {
		return code
	}
	fmt.Fprint(deps.Stderr, models.Table(table), tieringText)
	p, err := chooseParams(flags, table, deps)
	if err != nil {
		return fail(deps, err.Error())
	}
	if code := writeProject(env.WorkDir, p, deps); code != 0 {
		return code
	}
	// Not a precondition: role mappings can be written before the plugin is installed.
	if profile := omp.Profile(env.GatewayScope()); !omp.PluginInstalled(env.OMPHome, env.XDGDataHome, profile) {
		fmt.Fprint(deps.Stderr, omp.PluginHint(profile))
	}
	fmt.Fprint(deps.Stderr, "\nDone. Start the router with: aiven-llm omp\n")
	return 0
}

// chooseParams settles every role and the advisor, then validates them against the table.
func chooseParams(flags initFlags, table []models.Model, deps Deps) (ompinit.Params, error) {
	var picks [5]string
	for i, role := range omp.Roles {
		if picks[i] = flags.roles[i]; picks[i] != "" {
			continue
		}
		def, err := omp.RoleDefault(table, role)
		if err != nil {
			return ompinit.Params{}, err
		}
		if picks[i], err = ask(deps, role, def, pinnable(table)); err != nil {
			return ompinit.Params{}, err
		}
	}
	advisor := flags.advisor
	if advisor == "" {
		var err error
		if advisor, err = askAdvisorMode(deps); err != nil {
			return ompinit.Params{}, err
		}
	}
	var roles [5]string
	for i, role := range omp.Roles {
		var err error
		if roles[i], err = roleAlias(table, role, picks[i]); err != nil {
			return ompinit.Params{}, err
		}
	}
	p := ompinit.Params{Architect: roles[0], Builder: roles[1], CrossReviewer: roles[2], BulkReader: roles[3], BoilerplateWriter: roles[4], Advisor: advisor}
	if advisor == "off" {
		return p, nil
	}
	model := flags.advisorModel
	if model == "" {
		var err error
		if model, err = ask(deps, "advisor model", strings.TrimPrefix(picks[2], "aiven-gateway/"), pinnable(table)); err != nil {
			return p, err
		}
	}
	var err error
	p.AdvisorModel, err = roleAlias(table, "advisor model", model)
	return p, err
}

const (
	notAliasHint = "not an alias your key grants. Pick one of the aliases listed above."
	routerHint   = models.Router + " picks a model per request, so the role would not be pinned. Pick a model."
)

// pinnable rejects with the reason an answer cannot be pinned to a role, or returns "".
func pinnable(table []models.Model) func(string) string {
	return func(a string) string {
		a = strings.TrimPrefix(a, "aiven-gateway/")
		if _, ok := models.Find(table, a); !ok {
			return notAliasHint
		}
		if a == models.Router {
			return routerHint
		}
		return ""
	}
}

// ask prompts name with default def until reject returns "" for the answer; without a terminal it returns def silently.
func ask(deps Deps, name, def string, reject func(string) string) (string, error) {
	if deps.Prompter == nil {
		return def, nil
	}
	for {
		answer, err := deps.Prompter.ReadLine(fmt.Sprintf("%s [%s]: ", name, def))
		if errors.Is(err, io.EOF) {
			return def, nil
		}
		if err != nil {
			return "", err
		}
		answer = cmp.Or(answer, def)
		reason := reject(answer)
		if reason == "" {
			return answer, nil
		}
		fmt.Fprintln(deps.Stderr, reason)
	}
}

const tieringText = `
Tiering, cheapest first. The table above is the truth; this is the reasoning.
  flash open-weight (deepseek-flash, glm-fast)  cheapest usable coding tier: builder, and the router
  full open-weight (deepseek, glm, qwen)         stronger reasoning, several times the price
  frontier (claude-fable, gpt-astra, gpt-sol)    design and hard review only
  aiven-model                                    per-request choice; never a role

Pick a cross-reviewer from a different vendor than the architect: a model reviewing its own family
finds less.
`

const advisorText = `advisor: a second model reviews transcript deltas every turn, so it bills continuously,
unlike cross-reviewer which bills once per artifact.
  off      no advisor (recommended to start)
  main     watch the router session
  builder  watch the role that writes code
  both     watch each
`

func askAdvisorMode(deps Deps) (string, error) {
	if deps.Prompter != nil {
		fmt.Fprint(deps.Stderr, advisorText)
	}
	return ask(deps, "advisor", "off", func(a string) string {
		if slices.Contains(advisorModes, a) {
			return ""
		}
		return "pick off, main, builder or both."
	})
}

// flagError rewords package flag's errors as the launcher's: flag exposes no typed errors, so the text is matched.
func flagError(err error, args []string) error {
	msg := err.Error()
	if name, ok := strings.CutPrefix(msg, "flag needs an argument: -"); ok {
		return fmt.Errorf("--%s needs a value", name)
	}
	if name, ok := strings.CutPrefix(msg, "flag provided but not defined: -"); ok {
		return unknownFlag("--" + name)
	}
	if errors.Is(err, flag.ErrHelp) {
		// Which spelling tripped it (`-h`, `--help=true`, ...) is not reported by flag.
		i := slices.IndexFunc(args, func(a string) bool {
			name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "=")
			return name == "help" || name == "h"
		})
		return unknownFlag(args[max(i, 0)])
	}
	return err
}

func unknownFlag(arg string) error { return fmt.Errorf("unknown flag for omp init: %s", arg) }

var advisorModes = []string{"off", "main", "builder", "both"}

// initFlags are the `omp init` flags; empty means not given.
type initFlags struct {
	roles                 [5]string
	advisor, advisorModel string
}

func parseInitFlags(args []string) (initFlags, error) {
	var f initFlags
	set := flag.NewFlagSet("omp init", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	for i, role := range omp.Roles {
		set.StringVar(&f.roles[i], role, "", "")
	}
	set.StringVar(&f.advisor, "advisor", "", "")
	set.StringVar(&f.advisorModel, "advisor-model", "", "")
	if err := set.Parse(args); err != nil {
		return f, flagError(err, args)
	}
	if set.NArg() > 0 {
		return f, unknownFlag(set.Arg(0))
	}
	if f.advisor != "" && !slices.Contains(advisorModes, f.advisor) {
		return f, errors.New("--advisor must be off, main, builder or both")
	}
	return f, nil
}

// roleAlias checks value against the table and qualifies it as an OMP selector: a bare alias is ambiguous, since OMP also matches it against its built-in providers.
func roleAlias(table []models.Model, name, value string) (string, error) {
	value = strings.TrimPrefix(value, "aiven-gateway/")
	if _, ok := models.Find(table, value); !ok {
		return "", fmt.Errorf("%s: %q is not an alias your key grants. Run: aiven-llm models", name, value)
	}
	if value == models.Router {
		return "", fmt.Errorf("%s: %s", name, routerHint)
	}
	return "aiven-gateway/" + value, nil
}

// writeProject renders the project's config, sidecar and rule, then writes them; paths go to stdout.
func writeProject(project string, p ompinit.Params, deps Deps) int {
	dir := filepath.Join(project, ".omp")
	p.ConfigPath, p.StatePath = filepath.Join(dir, "config.yml"), filepath.Join(dir, "gateway-router.state.json")
	existing, err := readOptional(p.ConfigPath)
	if err != nil {
		return fail(deps, err.Error())
	}
	state, err := readOptional(p.StatePath)
	if err != nil {
		state = []byte{} // empty is not JSON, so Render warns and leaves optional keys alone, as for a corrupt sidecar
	}
	res, err := ompinit.Render(existing, state, p)
	if err != nil {
		return fail(deps, err.Error())
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(deps.Stderr, "warning: %s\n", w)
	}
	files := []projectFile{
		{path: p.ConfigPath, data: res.Config},
		{path: p.StatePath, data: res.State},
		{path: filepath.Join(dir, "rules", "gateway-router.md"), data: ompinit.Rule},
	}
	if err := os.MkdirAll(filepath.Join(dir, "rules"), 0o755); err != nil {
		return fail(deps, err.Error())
	}
	if err := writeAll(files); err != nil {
		return fail(deps, err.Error())
	}
	for _, f := range files {
		fmt.Fprintln(deps.Stdout, f.path)
	}
	return 0
}

type projectFile struct {
	path string
	data []byte
}

// writeAll writes files in order, each with its current mode or 0644 when new (project files are shared with the team, unlike
// the launcher's own 0600 files). On failure it restores the files already written, so the sidecar never disagrees with the config.
func writeAll(files []projectFile) error {
	type prior struct {
		path string
		data []byte // nil: the file did not exist
		mode fs.FileMode
	}
	var done []prior
	for _, f := range files {
		// ponytail: an unreadable old file restores as absent; the sidecar is the only one read leniently.
		old := prior{path: f.path, mode: 0o644}
		if fi, err := os.Stat(f.path); err == nil {
			old.mode = fi.Mode().Perm()
			old.data, _ = os.ReadFile(f.path)
		}
		if err := fsutil.WriteAtomic(f.path, f.data, old.mode); err != nil {
			for i := len(done) - 1; i >= 0; i-- {
				d := done[i]
				if d.data == nil {
					err = errors.Join(err, os.Remove(d.path))
				} else {
					err = errors.Join(err, fsutil.WriteAtomic(d.path, d.data, d.mode))
				}
			}
			return err
		}
		done = append(done, old)
	}
	return nil
}

// readOptional returns nil for a missing file.
func readOptional(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return b, err
}
