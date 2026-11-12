<!-- Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/ -->

# aiven-ai-gateway

Configure and launch Claude Code, Codex, Pi, OpenCode or OMP against Aiven
AI Gateway.

## Install

- **macOS (Homebrew)**, once the first release is out:
  `brew install aiven/tap/aiven-ai-gateway` (notarized). Maintainers: see
  [`RELEASING.md`](RELEASING.md).
- **npm / npx** (Node 20 or later), once the npm packages are published:
  `npm install -g @aiven/ai-gateway`, or `npx @aiven/ai-gateway` without
  installing. See [`npm/README.md`](npm/README.md). Configured harnesses
  read their saved key file directly, so they keep working whichever way the
  launcher was run.
- **Go module**, once a release is tagged (Go 1.27.4 or later, or default
  `GOTOOLCHAIN=auto`):

  ```sh
  go install github.com/aiven-open/ai-gateway-cli/cmd/aiven-ai-gateway@latest
  ```

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
aiven-ai-gateway launch codex -- resume
aiven-ai-gateway launch pi -- --model aiven/claude-sonnet
aiven-ai-gateway models
```

On a terminal, bare `setup` opens an inline multi-select list of harnesses
found on `PATH` (arrow keys move, Space marks, typing filters, Enter
continues) and bare `launch` opens a single-select list. `Esc` or `Ctrl-C`
cancels before any key is read or saved. Without a terminal, pass the
harness name (`claude`, `codex`, `pi`, `opencode` or `omp`).

Pass `--plain` on `setup` or `launch`, or set a nonempty `NO_COLOR` or
`TERM=dumb`, to use numbered line prompts without colour or cursor motion.

## Commands

| Command | Summary |
| --- | --- |
| `setup [harness] [--key-stdin] [-y\|--yes] [--plain]` | Validate a key, send one minimal 1-output-token request per configured protocol and model (which may incur usage), preview redacted configuration diffs (`[Y/n/v]`), save the key (`0600`), and write harness configuration with timestamped `.bak` backups. Never starts a session. |
| `launch [harness] [--plain] [-- native args]` | Start one configured harness against the selected gateway without modifying persistent configuration. Arguments after `--` are forwarded literally to the native tool. |
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
| Claude Code | `~/.claude/aiven.settings.json` | `claude --settings ~/.claude/aiven.settings.json` |
| Codex | Aiven provider in `~/.codex/config.toml`, profile in `~/.codex/aiven.config.toml` | `codex -p aiven` |
| Pi | Aiven provider in `~/.pi/agent/models.json` | `pi --model aiven/<alias>` |
| OpenCode | Aiven provider in `~/.config/opencode/opencode.json`, or `opencode.jsonc` when it exists; `$XDG_CONFIG_HOME/opencode` when set | `opencode -m aiven/<alias>` |
| OMP | Aiven provider in `~/.omp/agent/models.yml` (or existing `models.yaml`) | `omp --model aiven/<alias>` |

Answering `y` to `Make Aiven the native default for <tool>? [y/N]` also
updates Claude's `~/.claude/settings.json`, Codex's top-level
model/provider, Pi's `~/.pi/agent/settings.json`, OpenCode's `model`, or
OMP's `modelRoles.default` in `~/.omp/agent/config.yml` (or existing
`config.yaml`).

Keys are stored with mode `0600` at `~/.config/aiven/ai-gateway-key` for the
default gateway (`https://ai.aiven.io`), and under
`~/.config/aiven/ai-gateway-keys/` for custom origins.

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
- **Extended Unix ACLs and non-Unix inline keys:** atomic backups and
  replacements preserve POSIX mode bits on Unix and DACLs on Windows, not
  extended Unix ACLs (`chmod +a`, `setfacl`). Platforms other than macOS and
  Linux do not implement bounded `Esc` suffix waiting (use `--plain`);
  native Windows terminal behavior remains unverified.

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
