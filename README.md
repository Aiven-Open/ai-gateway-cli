<!-- Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/ -->

# aiven-ai-gateway

Configure and launch Claude Code, Codex, Pi, OpenCode or OMP against Aiven AI Gateway.

## Install

On macOS, once the first release is out: `brew install aiven/tap/aiven-ai-gateway` (notarized). Maintainers: see [`RELEASING.md`](RELEASING.md).

With Node 20 or later, once the npm packages are published:
`npm install -g @aiven/ai-gateway`, or `npx @aiven/ai-gateway` without
installing. See [`npm/README.md`](npm/README.md). The tools it configures read
their key without it, so they keep working whichever way it was run.

From source, once a release is tagged, with Go 1.27.3 or later (or the default `GOTOOLCHAIN=auto`):

```
go install github.com/aiven-open/ai-gateway-cli/cmd/aiven-ai-gateway@latest
```

## Use

Create a key in Aiven Console. Install the native harness,
configure it, then launch a session:

```
aiven-ai-gateway setup claude
aiven-ai-gateway launch claude
aiven-ai-gateway launch codex -- resume
aiven-ai-gateway launch pi -- --model aiven/claude-sonnet
```

Bare `aiven-ai-gateway` and `--help` display help and exit zero, even without credentials
or a valid gateway environment. The primary commands accept
`claude`, `codex`, `pi`, `opencode` or `omp`. Without a terminal, name the
harness.

`--version` prints the build's version, commit and date without reading
credentials or configuration.

Bare `setup` offers a list of tools found on `PATH`: arrow keys move, Space
marks, typing filters and Enter continues. Bare `launch` offers the installed
tools and starts the one under the cursor. Esc or Ctrl-C cancels either list
before any key is read or saved, and nothing is launched. The list is drawn
in place below the prompt, without switching to the alternate screen, and is
replaced by one line naming the choice, so the scrollback keeps a readable
record.

Inline keyboard decoding on macOS and Linux keeps arrow-key and UTF-8
fragments across reads. Escape waits up to 100 ms for a sequence suffix,
then cancels. Other platforms do not yet implement that bounded suffix
wait: use `--plain` there. Native Windows terminal behavior remains unverified.

`--plain` on `setup` or `launch`, a nonempty `NO_COLOR` or `TERM=dumb` replaces
the lists with numbered prompts that read one line each, and turns off colour
and cursor motion. Screen readers and terminals without cursor control use this
mode. End of input at any question never counts as an answer.

### Setup

`setup` never starts a harness. It validates the selected configuration, asks
whether Aiven should become the native default, and checks model access. It sends
one minimal inference request per configured protocol and model: Chat
Completions, Messages or Responses, with a one-output-token limit on each
request. These basic checks may incur usage. They do not show that a tool's
own requests, or the models its other slots and pins name, are accepted.
Tools that test the same model on the same protocol share one check; a
refusal fails only the tools that test that model. Each result names the
tested model and HTTP status.
Non-authentication refusals report a request failure and the gateway's safe
error message, rather than declaring the key rejected.
If a saved key is rejected or grants no models, setup asks before checking a
different environment key, or asks for a replacement.

Codex and Claude Code check the model their configuration selects. When the
key does not grant it, setup sends no inference request, saves neither the key
nor any tool, and names the file to change: the key is shared, and that tool
would keep requesting the model.
A tool is configured only if all its configured protocols pass. A partially
successful run writes only those tools and exits nonzero. If none pass, setup
saves no key or configuration; any existing key files remain unchanged.
Existing failed-tool files remain untouched.
Authentication failure or cancellation stops remaining checks and prevents
the run from saving configuration. Setup never retries with another protocol
or adds client compatibility settings to work around gateway rejection.

Setup announces each stage before it starts and reports its result:
retrieving the model list and its count, each test request and the model it
uses, and saving the key. Each tool's configuration preview follows. A run
ends with a summary: where the key was saved or why it was not, and for each
tool whether it was configured, unchanged, declined, not installed, failed,
partially written, cancelled or not attempted, with the files written. Only
ready tools get a launch command, the direct native command and an optional
bash/zsh alias such as
`alias codex-aiven='aiven-ai-gateway launch codex --'`. Alias names begin with
the tool name for shell completion: `pi-aiven`, `opencode-aiven`, `codex-aiven`,
`claude-aiven` and `omp-aiven`. Setup prints the aliases and never edits shell
files. Tools that need attention or were declined get the
`aiven-ai-gateway setup <harness>` command to run again. The exit status is
nonzero when any tool failed, was not installed, was partially written or the
run was cancelled; a decline alone exits zero but is not reported as complete.

Pi and OMP use Messages for `claude-*`, Responses for `gpt-*` except
`gpt-oss`/`gpt-oss-*`, and Chat Completions for every other listed model.
Both store explicit per-model protocols and base URLs. OMP uses ordinary
API-key authentication and an explicit granted model list instead of provider
discovery; rerun setup to refresh either tool's catalogue. All models returned
by the gateway are retained in listings and catalogue configurations. The gateway
controls catalogue access; the launcher applies no public-model allowlist.
Publisher and display names do not establish a protocol. OpenCode checks a
granted representative on its one protocol. Successful basic checks establish
only the tested model's route.

OMP runs as the ordinary coding harness, with its native tools. The configuration has been checked against OMP 18.15.0;
there is no runtime version gate.

Setup displays the selected gateway URL before reading or checking a key. It
uses `--key-stdin` first, otherwise the key saved for that gateway. A working
saved key offers a keep/replace choice, defaulting to keep. Without a usable
saved key, interactive setup shows the source and a masked
`AIVEN_AI_GATEWAY_KEY`, then asks whether to use it with the displayed gateway.
Only an explicit yes sends it; no or Enter asks for a key using hidden input.
`--yes` does not answer this question.

For unattended setup without a working saved key, provide the key with
`--key-stdin`, even when `AIVEN_AI_GATEWAY_KEY` is set. Add `--yes` to apply
configuration without further prompts. Later unattended runs can reuse the
saved key for that gateway. Never put a key in command arguments; clipboard
input is not supported.

```text
aiven-ai-gateway setup pi --key-stdin --yes < /path/to/gateway-key
```

After the checks, setup saves the bare key and a newline, mode `0600`, in the
key file of the configured gateway. The default gateway's is
`~/.config/aiven/ai-gateway-key`, the file the Aiven Console's instructions
write. Any other gateway's is `~/.config/aiven/ai-gateway-keys/<name>`, where
`<name>` is the gateway URL with each run of characters other than `a`-`z`,
`0`-`9`, `.` and `-` replaced by `-`, then the first 12 hex digits of the URL's
SHA-256: `https://gw.example:8443` gets `https-gw.example-8443-` and its hash.
It then shows configuration changes and their backup paths.
Pi/OpenCode catalogue previews show two model names; `v` displays the full
redacted diff. Both diff sides redact unrelated provider and MCP credentials.
Redaction goes by field name: it masks the values of fields named like a key,
token, secret, password, credential, bearer, auth, cookie or session, comments
that mention one, URL user info and key-shaped strings. A credential under
another name, under an escaped JSON field name, or in a TOML table whose header
alone names a credential (`[auth]`), is shown.

`Apply this change to <harness>? [Y/n/v]` accepts Enter or `y` to apply; `n`
declines that tool; `v` shows the full redacted diff and asks again. On a
terminal with the inline list the same three choices are offered as Apply,
Decline and Show the full redacted diff. `--yes` prints the preview and
applies on-demand configuration without prompting. Without a terminal or
`--yes`, configuration is skipped after the explicit key save.
`Make Aiven the native default for <tool>? [y/N]` accepts only an explicit
yes: Enter and `--yes` leave the native default unchanged. End of input at
this question cancels the run before any key is read. Pi and OMP each have
their own native-default question. The default choice is included in the complete
preview. The summary says Aiven is the native default only when this run
made it so; otherwise it says the default was left as it was. OMP's stored
default can be overridden by native project settings or enabled-model scope.
Setup preserves `enabledModels` and `disabledProviders`; a disabled Aiven
provider must be enabled in OMP before using it.

| Harness | On-demand configuration | Direct native invocation |
|---|---|---|
| Claude Code | `~/.claude/aiven.settings.json` | `claude --settings ~/.claude/aiven.settings.json` |
| Codex | Aiven provider in `~/.codex/config.toml`, profile in `~/.codex/aiven.config.toml` | `codex -p aiven` |
| Pi | Aiven provider in `~/.pi/agent/models.json` | `pi --model aiven/<alias>` |
| OpenCode | Aiven provider in `~/.config/opencode/opencode.json`, or `opencode.jsonc` when it exists; `$XDG_CONFIG_HOME/opencode` when that is set | `opencode -m aiven/<alias>` |
| OMP | Aiven provider in `~/.omp/agent/models.yml` (or existing `models.yaml`) | `omp --model aiven/<alias>` |

Other providers and settings are retained. Explicit default setup also changes
Pi's `settings.json`, OpenCode's `model`, Codex's top-level model/provider, or
Claude's canonical `settings.json`, or OMP's `modelRoles.default` in
`~/.omp/agent/config.yml` (or existing `config.yaml`).
OpenCode reads both `opencode.json` and `opencode.jsonc`, so when setup edits
`opencode.jsonc` it also removes an Aiven provider left in `opencode.json`.
Setup and launch refuse a relative `XDG_CONFIG_HOME`, which OpenCode would
resolve against the directory each session starts in.
Codex setup validates both TOML files and keeps an existing dedicated profile's
model, MCP servers and unrelated settings while updating the Aiven wiring.
Codex no longer reads profiles from `config.toml`, so setup moves the settings
of a legacy `[profiles.aiven]` table into `aiven.config.toml`, unless that file
sets them already, and removes the table and a top-level `profile = "aiven"`.
It refuses a top-level `profile` naming another profile, which Codex also
refuses: remove it and run `codex --profile <name>`, which reads
`<name>.config.toml`.
Codex setup disables hosted web search in the Aiven profile for every model,
since the gateway refuses it on Gemini models. It never changes `web_search` in
canonical `config.toml`, even when Aiven becomes the native default: plain
`codex` then keeps your own setting, or Codex's default, and its requests to
Gemini models fail unless that setting is `"disabled"`. Rerun setup to apply
this setting to existing Aiven configuration.

OMP updates the first native-consumed YAML file: `.yml` wins when it exists,
otherwise existing `.yaml` is used. It preserves unrelated YAML comments and
anchors, and refuses edits that would change unrelated alias values. The rewrite
may normalise formatting whitespace in unrelated nodes, such as spaces before a
trailing comment or padding in flow sequences. A shared or aliased owned mapping
may need to be made independent before setup.
Without consumed YAML, legacy `models.json` or `settings.json` stops setup
before credentials, network requests or writes: run `omp` once to let OMP
migrate, then rerun setup. If native migration fails, fix the legacy file first.
An `agent.db` alone does not trigger that refusal. Setup never changes the
database.

Each changed existing file gets an exclusively created timestamped backup,
such as `models.json.aiven-20261028T120000Z.bak`; collisions receive a numeric
suffix. Unchanged reruns make no config backup. JSON and TOML files are
edited in place: only the values setup changes are rewritten, so comments and
layout elsewhere are kept. A JSON file written on one line without comments
is rewritten in the standard layout. Syntax a tool's own reader rejects is
removed rather than kept: comments and trailing commas in Claude Code's
settings and Pi's `settings.json`, and block comments in Pi's `models.json`.
The preview shows those lines go. Codex's files must declare the Aiven
provider as a `[model_providers.aiven]` table; setup refuses other forms.
Setup refuses files edited since inspection, symlink targets and nonregular
targets, and writes only inside the directories it inspected. Setup and
launch read configuration and key files only when they are regular files: a
symlink, such as one a dotfiles manager made, or a FIFO is refused rather
than followed or waited on. Replace a symlink with the file it points to.
Replacements are atomic per file; an edit saved in the instant between
setup's last check and the replacement is lost. Backups and atomic
replacements preserve POSIX permission bits on Unix and the file's DACL on
Windows; extended ACLs on Unix (`chmod +a` on macOS, `setfacl` on Linux) are
not preserved. A multi-file failure can
leave earlier files configured: the error names completed files and recovery
copies. Fix the cause and rerun, or restore those copies before retrying.
Declining config does not undo an already completed key save.

### Launch

`launch` requires compatible native Aiven setup for the target gateway.
Missing or incompatible wiring prints `aiven-ai-gateway setup <harness>` and exits.
Compatible manual configuration counts; no launcher completion marker is used.
Launch displays the selected gateway on stderr, announces its model-access
request, and selects a session profile/model without refreshing persistent
provider metadata or changing defaults. It sends no test request and prints
no model list before starting the tool.
Native clients still write their own session/history data.

Arguments after `--` are passed as a literal native argument vector. With
none, Pi and OMP get `--model aiven/<SonnetOrFirst>` and OpenCode gets
`-m aiven/<SonnetOrFirst>`. With any, these three tools get them unchanged and
start on their own default model unless the arguments choose one, as in
`launch pi -- --model aiven/claude-sonnet -p hi`: where a model flag may go
depends on each tool's subcommands and options. OMP keeps all its native tools.
Codex selects `-p aiven`; later native
profile/config/model options follow Codex's own precedence. Claude adds
launcher-owned inline settings; a later `--model` follows native precedence.
Use native configuration overrides deliberately:
they can change the selected provider or settings.

Claude loads canonical user/project settings additively, preserving MCP,
hooks and project model pins. It clears conflicting gateway auth/backend
fields and wires the selected gateway/model slots for the session.
Canonical user model pins override the generated dedicated default; project
settings and then local project settings override user pins. As in Claude
Code, `env` merges apart from `model`, so an `ANTHROPIC_MODEL` from any of these
files runs over every file's `model`, and a blank one in a later file clears an
earlier one. A blank slot pin such as `ANTHROPIC_DEFAULT_OPUS_MODEL` clears an
earlier one too, leaving the launcher's default for that slot. Explicit native
`--model` follows Claude's own precedence.
The context window follows the model the session runs, including a native
`--model` and Claude's `opus`, `sonnet`, `haiku` and `fable` aliases. Setup
drops a `[1m]` suffix, in any case, from a kept model whose catalogue window is
under 1M, and never adds one to a model the user chose. Below
1M, setup and launch declare it with `CLAUDE_CODE_MAX_CONTEXT_TOKENS` for a
model id Claude Code does not recognise, and with
`CLAUDE_CODE_AUTO_COMPACT_WINDOW` for one it does, since Claude Code ignores
the first for its own models; the second outranks `/autocompact`. The one not
in use is set blank, which Claude Code reads as unset, so a value left in the
environment or in other settings cannot lower the window. They are set when the
session starts, so a model picked later with `/model` keeps them.
Setup retains unrelated fields in `aiven.settings.json` for the direct
`claude --settings` invocation. Launcher execution does not copy those
dedicated-file extras into argv; it passes only launcher-owned settings.

### Utilities and credential limits

`login` displays the selected gateway before reading an explicitly supplied
key, checks model listing and saves the key. It does not use an environment
key, test inference or configure a harness. `models` displays the gateway on
stderr before its request. `models` and `login` after saving list the granted
models grouped by publisher. Each entry leads with a readable name when one is known,
followed by the exact id that harness flags take. The group is the
catalogue's `owned_by` when it states one, then a reviewed table of exact
gateway ids (`internal/models/groups.go`); a model with neither is listed
under "Publisher unknown". A private alias reported with another owner, such
as Aiven, stays under that owner with its id, even when it reuses a public id.
Ids are never grouped by prefix. LiteLLM's own listing reports
`owned_by: openai` for every model, so that value alone does not place a
model. A terminal wide enough shows several entries per line;
redirected output has one entry per line and no colour. Pi, OMP and OpenCode show
the same readable names in their model pickers once setup has rewritten their
configuration.
`logout` removes the configured gateway's key file, leaving other gateways'
key files and harness configuration. It does not revoke the server key or
unset inherited environment variables.

### Output streams

Help and the model list go to stdout. Prompts, progress, previews, errors and
next steps go to stderr, so `aiven-ai-gateway models > models.txt` captures
only the list. Each stream is checked separately: colour and the inline lists
need a terminal on stderr (and stdin for the lists), whatever stdout is. Help
and the model list are styled only when stdout itself is a terminal; redirected
or plain help has the same words without escapes.
Catalogue and configuration text is stripped of control characters before it
is shown, so it cannot move the cursor or recolour the terminal.
Copyable commands are preserved exactly. If a native command's arguments
contain terminal controls, setup omits that command and explains how to use
the launcher instead. Saved selectors and launcher arguments keep their
original values.

Each configured tool reads the key file of the gateway it was set up for, so
a later login to another gateway changes no configured tool's key. Claude
Code, Codex, Pi and OMP run a helper that prints that key file and fails
without it; OpenCode reads the file when it starts. The launcher clears an
inherited `AIVEN_AI_GATEWAY_KEY` when starting these file-backed tools and
clears `ANTHROPIC_AUTH_TOKEN` for Claude Code. For compatible manual
configuration that reads `AIVEN_AI_GATEWAY_KEY`, launch sets it to the selected
gateway's validated saved key. Unrelated inherited environment entries stay.
The generated helpers do not fall back to `AIVEN_AI_GATEWAY_KEY`, which names
no gateway. The Aiven Console's helper does, so launch refuses a tool
configured with it until
`aiven-ai-gateway setup <tool>` rewires it. A save replaces the key file in one
rename, and a new key for a gateway reaches a running session of a tool set up
for it the next time that tool runs its helper.
On Windows the helpers run PowerShell, except Pi's: Pi runs key commands in
bash, so setup configures Pi on Windows only when Git Bash, or another bash on
`PATH` other than the WSL launcher, is where Pi looks for it.
Pi sends a credential saved for the `aiven` provider in `~/.pi/agent/auth.json`
instead of running the helper, so setup and launch refuse while one is there;
`/logout` in Pi removes it. They read that file and never change it.

The launcher supports the documented default config locations. It refuses
`CODEX_HOME`, `PI_CODING_AGENT_DIR`, `OPENCODE_CONFIG`, `OPENCODE_CONFIG_DIR`
or `CLAUDE_CONFIG_DIR` redirects rather than configuring files the tool would
not read. Configure redirected installations manually.
OMP additionally refuses `OMP_PROFILE`, `PI_PROFILE` and `PI_CONFIG_DIR`.

## Environment

| Variable | Effect |
|---|---|
| `AIVEN_AI_GATEWAY_BASE_URL` | HTTPS gateway origin, optionally with a port. Defaults to `https://ai.aiven.io`. Custom origins use their own key files. The server certificate must be trusted, and redirects are refused. HTTP, including loopback, is rejected because it exposes bearer credentials. |
| `AIVEN_AI_GATEWAY_KEY` | Key offered by interactive setup when there is no `--key-stdin` or usable saved key. Setup shows a mask and requires explicit confirmation for the selected gateway before sending it. Launch uses the saved key for a compatible manual provider that reads this variable. |

## Development

```
mise run test
```

Vets, checks `gofmt` and runs the tests. The tests drive `cli.Run` against a gateway double and a recording `execx.Runner`; helper-process tests cover the real handoff, `syscall.Exec` on Unix and a child process on Windows.

Also run `go test -race ./...`, `golangci-lint run`, `govulncheck ./...` and
`npm test --prefix npm`. [`npm/README.md`](npm/README.md) covers building and
checking the npm packages.

## License

Apache-2.0: see [`LICENSE`](LICENSE). Contributions: [`CONTRIBUTING.md`](CONTRIBUTING.md).
