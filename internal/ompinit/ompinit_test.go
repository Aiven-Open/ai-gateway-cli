// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package ompinit_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/Aiven-Open/ai-gateway-cli/internal/ompinit"
)

// goldenCases are POC render.mjs outputs. Case dirs hold args, optional config.yml and state.json, and expected.*.
// Where goccy's layout differs from the POC's (flow sequences lose their padding), expected.config.yml is goccy's output and poc.config.yml the POC's, which must decode to the same data.
var goldenCases = []string{
	"01-fresh",
	"02-existing-rich",
	"03-idempotent",
	"04-reclaim-unedited",
	"05-reclaim-edited",
	"06-odd-format",
	"07-comment-only",
}

func TestRenderMatchesGolden(t *testing.T) {
	for _, name := range goldenCases {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join("testdata", name)
			res, err := ompinit.Render(readOpt(t, dir, "config.yml"), readOpt(t, dir, "state.json"), paramsFromArgs(t, dir))
			if err != nil {
				t.Fatal(err)
			}
			if got, want := string(res.Config), string(readOpt(t, dir, "expected.config.yml")); got != want {
				t.Errorf("config differs\n--- got\n%s\n--- want\n%s", got, want)
			}
			if poc := readOpt(t, dir, "poc.config.yml"); poc != nil {
				assertSameData(t, res.Config, poc)
			}
			if got, want := string(res.State), string(readOpt(t, dir, "expected.state.json")); got != want {
				t.Errorf("state differs\n--- got\n%s\n--- want\n%s", got, want)
			}
			if got, want := warningText(res.Warnings), string(readOpt(t, dir, "expected.stderr")); got != want {
				t.Errorf("warnings %q, want %q", got, want)
			}
		})
	}
}

func readOpt(t *testing.T, dir, file string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return b
}

// paramsFromArgs reads the POC renderer flags the case was generated with.
func paramsFromArgs(t *testing.T, dir string) ompinit.Params {
	t.Helper()
	f := strings.Fields(string(readOpt(t, dir, "args")))
	p := ompinit.Params{Advisor: "off"}
	for i := 0; i+1 < len(f); i += 2 {
		switch f[i] {
		case "--architect":
			p.Architect = f[i+1]
		case "--builder":
			p.Builder = f[i+1]
		case "--cross-reviewer":
			p.CrossReviewer = f[i+1]
		case "--bulk-reader":
			p.BulkReader = f[i+1]
		case "--boilerplate-writer":
			p.BoilerplateWriter = f[i+1]
		case "--advisor":
			p.Advisor = f[i+1]
		case "--advisor-model":
			p.AdvisorModel = f[i+1]
		default:
			t.Fatalf("unknown flag %s in %s/args", f[i], dir)
		}
	}
	return p
}

func warningText(ws []string) string {
	var b strings.Builder
	for _, w := range ws {
		b.WriteString("warning: " + w + "\n")
	}
	return b.String()
}

func assertSameData(t *testing.T, got, want []byte) {
	t.Helper()
	var g, w any
	if err := yaml.Unmarshal(got, &g); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(want, &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("config data differs from POC\n got %v\nwant %v", g, w)
	}
}

func baseParams() ompinit.Params {
	return ompinit.Params{
		Architect: "aiven-gateway/a", Builder: "aiven-gateway/b", CrossReviewer: "aiven-gateway/c",
		BulkReader: "aiven-gateway/d", BoilerplateWriter: "aiven-gateway/e", Advisor: "off",
		ConfigPath: "/p/.omp/config.yml", StatePath: "/p/.omp/gateway-router.state.json",
	}
}

func TestRenderRejectsInvalidYAML(t *testing.T) {
	_, err := ompinit.Render([]byte("a: [unclosed\n"), nil, baseParams())
	if err == nil {
		t.Fatal("want error")
	}
	if got := err.Error(); !strings.HasPrefix(got, "/p/.omp/config.yml is not valid YAML (") || !strings.HasSuffix(got, "). Fix or move it, then rerun.") || strings.Contains(got, "\n") {
		t.Errorf("error %q", got)
	}
}

func TestRenderRejectsNonMappingTopLevel(t *testing.T) {
	for name, in := range map[string]string{"sequence": "- a\n- b\n", "scalar": "just text\n"} {
		t.Run(name, func(t *testing.T) {
			_, err := ompinit.Render([]byte(in), nil, baseParams())
			want := "/p/.omp/config.yml must be a YAML mapping at the top level. Fix or move it, then rerun."
			if err == nil || err.Error() != want {
				t.Errorf("error %v, want %q", err, want)
			}
		})
	}
}

func TestRenderWarnsWhenStateIsUnreadableAndLeavesOptionalKeysAlone(t *testing.T) {
	existing := "modelRoles:\n  advisor: aiven-gateway/x\n"
	res, err := ompinit.Render([]byte(existing), []byte("{not json"), baseParams())
	if err != nil {
		t.Fatal(err)
	}
	want := "/p/.omp/gateway-router.state.json is unreadable; treating every optional key as hand-written and leaving it alone."
	if len(res.Warnings) != 1 || res.Warnings[0] != want {
		t.Errorf("warnings %q, want [%q]", res.Warnings, want)
	}
	if !strings.Contains(string(res.Config), "advisor: aiven-gateway/x") {
		t.Errorf("hand-written advisor was removed:\n%s", res.Config)
	}
}

func TestRenderWarnsOnProjectEnabledModels(t *testing.T) {
	res, err := ompinit.Render([]byte("enabledModels:\n  - anthropic/claude-sonnet-4-5\n"), nil, baseParams())
	if err != nil {
		t.Fatal(err)
	}
	want := "/p/.omp/config.yml sets enabledModels. Under `aiven-llm omp` the launcher's --config overlay outranks project config, so this array is ignored there; it applies when you run a bare `omp` in this project, and then it must include \"aiven-gateway/*\" for the role models to resolve."
	if len(res.Warnings) != 1 || res.Warnings[0] != want {
		t.Errorf("warnings %q, want [%q]", res.Warnings, want)
	}
}

func TestRenderAdvisorBuilderWritesOnlyBuilderKey(t *testing.T) {
	p := baseParams()
	p.Advisor, p.AdvisorModel = "builder", "aiven-gateway/adv"
	res, err := ompinit.Render(nil, nil, p)
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(res.Config)
	if !strings.Contains(cfg, "agentAdvisor:\n    builder: aiven-gateway/adv\n") || strings.Contains(cfg, "enabled:") || strings.Contains(cfg, "  advisor:") {
		t.Errorf("config:\n%s", cfg)
	}
	if want := "{\n  \"version\": 1,\n  \"managed\": {\n    \"task.agentAdvisor.builder\": \"aiven-gateway/adv\"\n  }\n}\n"; string(res.State) != want {
		t.Errorf("state %q, want %q", res.State, want)
	}
}

func TestRenderIgnoresStateOfAnotherVersion(t *testing.T) {
	existing := "modelRoles:\n  advisor: aiven-gateway/x\n"
	state := `{"version":2,"managed":{"modelRoles.advisor":"aiven-gateway/x"}}`
	res, err := ompinit.Render([]byte(existing), []byte(state), baseParams())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 || !strings.Contains(string(res.Config), "advisor: aiven-gateway/x") {
		t.Errorf("warnings %q, config:\n%s", res.Warnings, res.Config)
	}
}

// decode parses config as YAML and fails the test when it is not valid.
func decode(t *testing.T, config []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := yaml.Unmarshal(config, &m); err != nil {
		t.Fatalf("rendered config is not valid YAML: %v\n%s", err, config)
	}
	return m
}

func lookup(t *testing.T, m map[string]any, path ...string) any {
	t.Helper()
	var cur any = m
	for _, k := range path {
		next, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("no %v in %v", path, m)
		}
		cur = next[k]
	}
	return cur
}

func TestRenderKeepsFlowMappingValid(t *testing.T) {
	for name, in := range map[string]string{
		"flow parent":    "task: {custom: keep}\n",
		"flow root":      "{custom: keep}\n",
		"nested flow":    "modelRoles: {architect: old}\ntask: {agentModelOverrides: {mine: x}}\n",
		"flow with note": "task: {custom: keep} # mine\n",
	} {
		t.Run(name, func(t *testing.T) {
			res, err := ompinit.Render([]byte(in), nil, baseParams())
			if err != nil {
				t.Fatal(err)
			}
			m := decode(t, res.Config)
			if got := lookup(t, m, "task", "agentModelOverrides", "sonic"); got != "@boilerplate-writer" {
				t.Errorf("sonic override %v\n%s", got, res.Config)
			}
			if got := lookup(t, m, "modelRoles", "architect"); got != "aiven-gateway/a" {
				t.Errorf("architect %v\n%s", got, res.Config)
			}
			again, err := ompinit.Render(res.Config, res.State, baseParams())
			if err != nil || string(again.Config) != string(res.Config) {
				t.Errorf("second run err %v, changed:\n%s", err, again.Config)
			}
		})
	}
}

func TestRenderQuotesAliasesThatAreNotPlainScalars(t *testing.T) {
	for name, alias := range map[string]string{"colon space": "aiven-gateway/x: y", "space hash": "aiven-gateway/x #y", "bool": "true", "quote": `aiven-gateway/x"y`} {
		t.Run(name, func(t *testing.T) {
			p := baseParams()
			p.Architect, p.Advisor, p.AdvisorModel = alias, "both", alias
			for _, existing := range []string{"", "modelRoles:\n  architect: old\n  advisor: old\n"} {
				res, err := ompinit.Render([]byte(existing), nil, p)
				if err != nil {
					t.Fatal(err)
				}
				m := decode(t, res.Config)
				for _, path := range [][]string{{"modelRoles", "architect"}, {"modelRoles", "advisor"}, {"task", "agentAdvisor", "builder"}} {
					if got := lookup(t, m, path...); got != alias {
						t.Errorf("%v = %#v, want %q\n%s", path, got, alias, res.Config)
					}
				}
			}
		})
	}
}

func TestRenderReplacingManagedScalarKeepsAnchorAndComment(t *testing.T) {
	in := "modelRoles:\n  architect: &chosen old # why\ncustom: *chosen\n"
	res, err := ompinit.Render([]byte(in), nil, baseParams())
	if err != nil {
		t.Fatal(err)
	}
	if want := "  architect: &chosen aiven-gateway/a # why\n"; !strings.Contains(string(res.Config), want) {
		t.Errorf("config lacks %q:\n%s", want, res.Config)
	}
	if got := lookup(t, decode(t, res.Config), "custom"); got != "aiven-gateway/a" {
		t.Errorf("alias resolves to %v", got)
	}
}

func TestRenderTraversesAnchoredParentMapping(t *testing.T) {
	in := "modelRoles: &roles\n  architect: old\nother: *roles\n"
	res, err := ompinit.Render([]byte(in), nil, baseParams())
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, res.Config)
	if got := lookup(t, m, "modelRoles", "builder"); got != "aiven-gateway/b" {
		t.Errorf("builder %v\n%s", got, res.Config)
	}
	if got := lookup(t, m, "other", "architect"); got != "aiven-gateway/a" {
		t.Errorf("alias target architect %v\n%s", got, res.Config)
	}
}

func TestRenderErrorsWhenReclaimLeavesDanglingAlias(t *testing.T) {
	in := "modelRoles:\n  advisor: &a aiven-gateway/x\ncustom: *a\n"
	state := `{"version":1,"managed":{"modelRoles.advisor":"aiven-gateway/x"}}`
	res, err := ompinit.Render([]byte(in), []byte(state), baseParams())
	if err == nil || !strings.Contains(err.Error(), "/p/.omp/config.yml") || !strings.Contains(err.Error(), "alias") || strings.Contains(err.Error(), "\n") {
		t.Fatalf("error %v, result %q", err, res.Config)
	}
	if res.Config != nil || res.State != nil {
		t.Errorf("result not empty on error: %q %q", res.Config, res.State)
	}
}

func TestRenderReclaimKeepsEmptiedModelRolesAndTaskInPlace(t *testing.T) {
	in := "modelRoles:\n  advisor: aiven-gateway/x\ntask:\n  agentAdvisor:\n    builder: aiven-gateway/x\nlast: 2\n"
	state := `{"version":1,"managed":{"modelRoles.advisor":"aiven-gateway/x","task.agentAdvisor.builder":"aiven-gateway/x"}}`
	res, err := ompinit.Render([]byte(in), []byte(state), baseParams())
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(res.Config)
	if strings.Contains(cfg, "agentAdvisor") || strings.Contains(cfg, "advisor") {
		t.Errorf("advisor keys remain:\n%s", cfg)
	}
	if !strings.HasPrefix(cfg, "modelRoles:\n  architect:") || strings.Index(cfg, "task:") > strings.Index(cfg, "last:") {
		t.Errorf("modelRoles or task moved:\n%s", cfg)
	}
}

func TestRenderFillsNullParentMappings(t *testing.T) {
	for name, in := range map[string]string{
		"empty value": "keep: 1\nmodelRoles:\ntask:\n  agentAdvisor: null\n",
		"tilde":       "keep: 1\nmodelRoles: ~\nz: 2\n",
		"nested null": "keep: 1\ntask:\n  agentAdvisor: null\n",
	} {
		t.Run(name, func(t *testing.T) {
			p := baseParams()
			p.Advisor, p.AdvisorModel = "builder", "aiven-gateway/f"
			res, err := ompinit.Render([]byte(in), nil, p)
			if err != nil {
				t.Fatal(err)
			}
			m := decode(t, res.Config)
			if got := lookup(t, m, "modelRoles", "architect"); got != "aiven-gateway/a" {
				t.Errorf("architect %v\n%s", got, res.Config)
			}
			if got := lookup(t, m, "task", "agentAdvisor", "builder"); got != "aiven-gateway/f" {
				t.Errorf("agentAdvisor.builder %v\n%s", got, res.Config)
			}
			if got := lookup(t, m, "keep"); got != 1 && got != uint64(1) {
				t.Errorf("keep %v", got)
			}
		})
	}
}

func TestRenderNamesParentThatIsNotAMapping(t *testing.T) {
	for name, in := range map[string]string{
		"alias":    "base: &b\n  k: v\nmodelRoles: *b\n",
		"scalar":   "modelRoles: none\n",
		"sequence": "modelRoles: [a, b]\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ompinit.Render([]byte(in), nil, baseParams())
			want := "/p/.omp/config.yml: modelRoles (modelRoles) is "
			if err == nil || !strings.HasPrefix(err.Error(), want) || !strings.HasSuffix(err.Error(), "Fix or move it, then rerun.") || strings.Contains(err.Error(), "\n") {
				t.Errorf("error %v, want prefix %q", err, want)
			}
		})
	}
}
