<!-- Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/ -->

# aiven-ai-gateway

Configure and launch Claude Code, Codex, Pi, OpenCode or OMP against Aiven
AI Gateway.

## Install

- **macOS (Homebrew)**, once the first release is out:
  `brew install aiven/tap/aiven-ai-gateway` (notarized). Maintainers: see
  [`RELEASING.md`](RELEASING.md).
- **npm / npx** (Node 20 or later), once the npm packages are published:
  `npm install -g @aivenio/ai-gateway`, or `npx @aivenio/ai-gateway` without
  installing. See [`npm/README.md`](npm/README.md). Configured harnesses
  read their saved key file directly, so they keep working whichever way the
  launcher was run.
- **Go module**, once a release is tagged (Go 1.27.4 or later, or default
  `GOTOOLCHAIN=auto`):

  ```sh
  go install github.com/aiven-open/ai-gateway-cli/cmd/aiven-ai-gateway@latest
  ```

  Put `$(go env GOPATH)/bin` (or `GOBIN`) in `PATH`, or run
  `go run github.com/aiven-open/ai-gateway-cli/cmd/aiven-ai-gateway@latest <command>`
  without installing. The Go module path is lowercase
  `github.com/aiven-open/ai-gateway-cli`.

- **From a local clone**:

  ```sh
  go install ./cmd/aiven-ai-gateway
  ```

## Quickstart

Create an API key in Aiven Console, install the coding harness you want to
use, configure it once, then launch sessions:

```sh
aiven-ai-gateway setup claude
aiven-ai-gateway launch claude
aiven-ai-gateway launch codex resume
aiven-ai-gateway launch pi --model aiven/claude-sonnet
aiven-ai-gateway models
```

On a terminal, bare `setup` detects harnesses installed on `PATH`, checks
your key and granted models, then guides you through three reversible steps
(harnesses pre-selected with live status badges, model/preset selection with
inline ungranted-model recovery, and optional direct `<tool>` invocation)
before probing configured protocols concurrently and showing a review table.
Arrow keys or `j`/`k` move, `Space` toggles (`a` toggles all), `/` filters,
`Esc` or `Shift+Tab` steps back, and `Ctrl-C` cancels before saving any key
or file. When no supported harness is found on `PATH`, bare `setup` prints
install hints and `aiven-ai-gateway login` without asking for a key. Bare
`launch` opens a single-select list of installed harnesses. Without a
terminal, pass the harness name (`claude`, `codex`, `pi`, `opencode` or
`omp`).

Pass `--plain` on `setup` or `launch`, or set a nonempty `NO_COLOR` or
`TERM=dumb`, to use numbered line prompts without colour, spinners or cursor
motion. Non-UTF-8 locales (`LC_ALL=C`/`POSIX`) fall back to ASCII markers.

## Commands

| Command | Summary |
| --- | --- |
| `setup [harness] [--key-stdin] [-y\|--yes] [--plain]` | Discover installed harnesses, validate a key, choose a model or curated preset (resolving ungranted configured models), probe each configured protocol and model concurrently (1 output token each, which may incur usage), review redacted diffs, save the key (`0600`) and launcher model preferences, and write harness configuration with timestamped `.bak` backups. Never starts a session. |
| `launch [--plain] [harness] [--] [native args]` | Start one configured harness against the selected gateway without modifying persistent configuration. Arguments after the harness name (or after `--` when picking a harness) are forwarded literally to the native tool; an optional `--` after the harness name is also accepted. |
| `login` | Check that an explicitly supplied key can list models, save it (`0600`), and list granted models. Sends no inference request and configures no harness. |
| `logout` | Remove the saved key file for the selected gateway, leaving other gateways' key files and harness configurations intact. Does not revoke the server key or unset environment variables. |
| `models` | List granted models on stdout grouped by publisher, with the exact model IDs that harness flags take (`aiven-ai-gateway models > models.txt`). |
| `help`, `-h`, `--help` | Print usage to stdout and exit zero without reading credentials or configuration (bare `aiven-ai-gateway` does the same). |
| `version`, `--version` | Print the build's version, commit and date without reading credentials or configuration. |

For unattended `setup` without a working saved key, supply the key via
`--key-stdin` and pass `--yes` to apply on-demand configuration without
interactive diff prompts (`--yes` never changes a tool's native default):

```sh
aiven-ai-gateway setup pi --key-stdin --yes < /path/to/gateway-key
```

Full behavioural details (protocol routing, credential consent, diff
redaction rules, Claude Code context-window variables, in-place config
editing and stream handling) are in [`docs/reference.md`](docs/reference.md).

## Harness Configuration

`setup` retains unrelated providers and settings in your existing
configuration files:

| Harness | On-demand configuration | Direct native invocation |
| --- | --- | --- |
| Claude Code | `~/.claude/aiven.settings.json` | `claude --settings ~/.claude/aiven.settings.json` (`claude --settings "%USERPROFILE%\.claude\aiven.settings.json"` on Windows) |
| Codex | Aiven provider in `~/.codex/config.toml`, profile in `~/.codex/aiven.config.toml` | `codex -p aiven` |
| Pi | Aiven provider in `~/.pi/agent/models.json` | `pi --model aiven/<alias>` |
| OpenCode | Aiven provider in `~/.config/opencode/opencode.json`, or `opencode.jsonc` when it exists; `$XDG_CONFIG_HOME/opencode` when set | `opencode -m aiven/<alias>` |
| OMP | Aiven provider in `~/.omp/agent/models.yml` (or existing `models.yaml`) | `omp --model aiven/<alias>` |

Setting Aiven as the default provider during `setup` also updates Claude's
`~/.claude/settings.json`, Codex's top-level model/provider, Pi's
`~/.pi/agent/settings.json`, OpenCode's `model`, or OMP's
`modelRoles.default` in `~/.omp/agent/config.yml` (or existing
`config.yaml`). Choosing launch-only keeps native defaults untouched; for Pi, OpenCode and OMP, `setup` persists your chosen default
model per gateway in `~/.config/aiven/ai-gateway-models.json` (`0600`) so
`aiven-ai-gateway launch <tool>` uses it automatically (explicit
`-- --model`/`-m` flags still override, and ungranted persisted aliases fall
back cleanly to the catalogue default).

Interactive `setup` offers curated presets (`Anthropic`, `OpenAI`,
`Gemini`, `Open-weight`, filtered to models your key grants) and a
searchable `Custom model…` picker. When an existing Claude Code (including
`~/.claude/settings.json` and project `.claude/settings*.json` layers) or
Codex configuration selects an ungranted model, `setup` badges the conflict
and offers the matching gateway alias (when known via provider-ID
normalization), available presets, or a custom model; leaving it unchanged
or running with `--yes` refuses before probes or writes.

Keys are stored with mode `0600` at `~/.config/aiven/ai-gateway-key` for the
default gateway (`https://ai.aiven.io`), and under
`~/.config/aiven/ai-gateway-keys/` for custom origins. `setup` reuses a
valid saved key automatically; run `aiven-ai-gateway login` or pass
`--key-stdin` to replace it. No key or preference file is written unless at
least one configuration change is approved.

## Environment

| Variable | Effect |
| --- | --- |
| `AIVEN_AI_GATEWAY_BASE_URL` | HTTPS gateway origin, optionally with a port. Defaults to `https://ai.aiven.io`. Custom origins use their own key files. The server certificate must be trusted, and redirects are refused. HTTP, including loopback, is rejected because it exposes bearer credentials. |
| `AIVEN_AI_GATEWAY_KEY` | Key offered by interactive `setup` when there is no `--key-stdin` or usable saved key. `setup` shows a mask and requires explicit confirmation for the selected gateway before sending it. `launch` uses the saved key for a compatible manual provider that reads this variable. |

## Troubleshooting and Limitations

- **Symlinked config or key files are refused:** `setup` and `launch` read
  and write regular files only; symlinks (for example from a dotfiles
  manager) and FIFOs are refused rather than followed. Replace the symlink
  with a regular file.
- **Custom config-directory variables are refused:** `CODEX_HOME`,
  `PI_CODING_AGENT_DIR`, `OPENCODE_CONFIG`, `OPENCODE_CONFIG_DIR`,
  `CLAUDE_CONFIG_DIR`, `OMP_PROFILE`, `PI_PROFILE`, `PI_CONFIG_DIR` and
  relative `XDG_CONFIG_HOME` values are rejected; configure redirected
  installations manually.
- **Pi `auth.json` overrides the key helper:** if `~/.pi/agent/auth.json`
  holds an `aiven` credential, `setup` and `launch` refuse until you remove
  it with `/logout` inside Pi.
- **Pi on Windows requires Git Bash:** Pi runs key commands in `bash`, so
  `setup` configures Pi on Windows only when Git Bash (or another non-WSL
  `bash` on `PATH`) is where Pi looks for it.
- **Codex hosted web search on Gemini:** `setup` disables `web_search` in
  `~/.codex/aiven.config.toml` (used by `launch codex` and
  `codex -p aiven`), because the gateway rejects hosted web search on Gemini
  models. `setup` never edits `web_search` in canonical
  `~/.codex/config.toml`, so plain `codex` against Gemini fails unless
  `web_search = "disabled"`.
- **OMP legacy JSON files:** without `models.yml`/`models.yaml`, existing
  legacy `models.json` or `settings.json` stops `setup`; run `omp` once so
  OMP migrates its files, then rerun `setup`.
- **Extended Unix ACLs and legacy consoles:** atomic backups and
  replacements preserve POSIX mode bits on Unix and DACLs on Windows, not
  extended Unix ACLs (`chmod +a`, `setfacl`). Windows terminals enable
  virtual terminal processing and bounded `Esc` waits automatically,
  falling back to `--plain` on legacy ConHost consoles where VT modes are
  unavailable; other non-Unix platforms require `--plain`.

## Development

```sh
mise run build   # builds ./dist/aiven-ai-gateway
mise run check   # vets, gofmt, lint, race tests, govulncheck, gitleaks, npm
```

See [`CONTRIBUTING.md`](CONTRIBUTING.md#development) for prerequisites, raw
`go`/`npm` commands without `mise`, repository layout and commit rules, and
[`npm/README.md`](npm/README.md) for npm package preparation and checks.

## License

Apache-2.0: see [`LICENSE`](LICENSE). Contributions:
[`CONTRIBUTING.md`](CONTRIBUTING.md).
