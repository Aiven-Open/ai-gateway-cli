<!-- Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/ -->

# Behavioural and Configuration Reference

Detailed command contracts, protocol routing, credential handling,
configuration editing and stream rules for `aiven-ai-gateway`. For
installation and quickstart usage, see [`README.md`](../README.md).

## Terminal Interaction and Plain Mode

Bare `aiven-ai-gateway` and `--help` display help and exit zero, even
without credentials or a valid gateway environment. `--version` (or
`version`) prints the build's version, commit and date without reading
credentials or configuration. The primary commands (`setup` and `launch`)
accept `claude`, `codex`, `pi`, `opencode` or `omp`; without a terminal,
name the harness explicitly.

Bare `setup` first scans `PATH` locally. When no supported harness is
installed, it prints installation hints and `aiven-ai-gateway login` and
exits cleanly without asking for a key. Otherwise it validates the key,
lists `/v1/models`, snapshots candidate configurations read-only, and opens
an inline multi-select list of installed harnesses pre-selected with live
status badges (`new`, `configured (<model>)`, or `! ungranted: <id> in
<file>`) and a dimmed list of harnesses not on `PATH`. Arrow keys or
`j`/`k` move, `Space` marks, `a` toggles all/none, `/` enters filter mode,
`Shift+Tab` or `Esc` steps back (clearing a non-empty filter first), and
`Enter` continues. Long lists such as `Custom model…` clamp to terminal
height (`height - 1` rows) with `↑`/`↓ N more` indicators. Bare `launch`
offers the installed tools and starts the one under the cursor. `Ctrl-C`
cancels before any key or file is written, and nothing is launched. Lists
draw in place without switching to the alternate screen and collapse to
durable vertical-rail lines (`┌`, `│`, `◇`, `✓`, `└`), closing the rail
before review tables, unified diffs and copyable commands.

Inline keyboard decoding on macOS, Linux and Windows keeps arrow-key and
UTF-8 fragments across reads and bounds standalone `Esc` to 100 ms. On
Windows, virtual terminal input/output modes are enabled and restored on
exit; legacy ConHost consoles where VT modes are unavailable automatically
fall back to `--plain`. Other platforms require `--plain`.

`--plain` on `setup` or `launch`, a nonempty `NO_COLOR` or `TERM=dumb`
replaces the lists with numbered prompts that read one line each, and turns
off colour, spinners and cursor motion. Non-UTF-8 locales (`LC_ALL=C` or
`POSIX`) use ASCII markers instead of UTF-8 box-drawing glyphs. Screen
readers and terminals without cursor control use this mode. End of input at
any question never counts as an answer.

## Setup Workflow, Probes and Exit Status

`setup` never starts a harness. After local discovery, key validation and
read-only inspection of dedicated and native-default files (including
Claude Code's `~/.claude/settings.json` and project `.claude/settings*.json`
layers), interactive `setup` runs three reversible steps before review:

1. **Harnesses** (bare `setup` only): pre-selected installed tools with
   live selection count, status badges and missing-tools footer.
2. **Default model & inline conflict resolution:** offers curated presets
   (`Anthropic`, `OpenAI`, `Gemini`, `Open-weight`, filtered to granted
   models) and a searchable `Custom model…` picker. For two or more tools, a single menu
   handles `Recommended defaults`, presets, `Customize per tool…`, and
   inline resolution of ungranted Claude Code or Codex models (offering
   `Switch <tool> -> <alias> & use Recommended defaults` when
   `models.ProviderAlias` matches a wrapped Bedrock, Vertex or dated
   provider ID, or defaulting to `Leave unchanged (cancel setup)` when no
   alias matches). `Customize per tool…` opens a per-tool menu where you can
   configure any specific harness (then choose `Done — continue` or customize
   another) or pick `Customize one by one…` to step through every selected
   tool. Single-tool runs prompt directly, keeping existing granted models as
   `Keep current (<alias>)` and defaulting ungranted models to
   `Keep unchanged (refuse setup)`.
3. **Default provider:** asks *"Use Aiven AI Gateway as the default?"*,
   defaulting to `Yes` for all selected tools, with options to keep
   launcher-only (`aiven-ai-gateway launch`) or choose per tool (naming
   each config file).

Claude Code has its own menu instead of presets: `Recommended defaults` and
`Choose models`, plus `Keep current` when the key grants the configured model.
The other tools keep their preset list. `Recommended defaults` stays on Claude
models whenever Anthropic models are granted and removes any stale
`ANTHROPIC_CUSTOM_MODEL_OPTION`. A shared vendor preset in the Default model
step sets Claude Code's default model, and the custom model option when that
model is not one of the tier models. The tiers stay on granted Claude models. On
a key with no Anthropic models every tier takes the chosen model. On the
single-model path, taken by a shared vendor model or by `Recommended defaults`
when no preset fits, the chosen model replaces any non-blank
`env.ANTHROPIC_MODEL`. `Keep current`, `--yes` and the shared
`Recommended defaults` keep every tier slot the key still grants, including an
older setup with one model in all tiers. `Recommended defaults` in Claude Code's
own menu resets them.

`Choose models` lists every granted model by vendor. Tick any number of Claude
models and up to five models from other vendors; a sixth is refused. The
arrow-key list refuses it where you tick it, and plain mode refuses the answer
and asks again. A model counts as a Claude model when the gateway lists it under
Anthropic, or its publisher is missing or not one the CLI has reviewed and its
id starts with `claude-`. Then choose
the default, which setup writes to `model`. A ticked Claude model goes in the
tier its id names, and when two ticked models name the same tier the first
ticked takes it. The other picks fill `ANTHROPIC_CUSTOM_MODEL_OPTION` first,
then the tier slots `ANTHROPIC_DEFAULT_{FABLE,OPUS,SONNET,HAIKU}_MODEL`: first
tiers with no granted Claude model, then tiers holding a Claude model you did
not tick, then tiers holding one you ticked, and the haiku tier last. Order
within each group is fable, opus, sonnet, haiku. Choosing again with the same
picks gives the same slots, because an explicit choice takes tier defaults from
the gateway's catalogue and not from pins already in the file. `Keep current`
and `--yes` keep every slot the key still grants.

Claude Code sends some work to a tier by name, so a non-Claude model in a tier
slot receives that work. Setup sets
`CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1`, which lists the granted Claude
models in `/model` too, including one moved out of its tier. This discovery
behaviour was observed in Claude Code and is not documented. Every chosen model
gets at least one basic check request, so several picks mean several requests. A
chosen model that fails its check fails Claude Code only; other selected tools
still proceed. To change the picks, run setup again. Setup recomputes
context-window variables and `[1m]` for each slot. In Codex, the choice updates
`aiven.config.toml` (and canonical `config.toml` when Aiven is set as the
default provider). In Pi, OpenCode and OMP, the full granted catalogue is
written to the provider file, the chosen default alias is persisted per gateway
in `~/.config/aiven/ai-gateway-models.json` (`0600`) for `aiven-ai-gateway
launch <tool>`, and the native default is updated when Aiven is set as the
default provider.

With `--yes` or without a terminal, `setup` skips interactive model and
default-provider menus: fresh configurations use natural defaults
(falling back through the Open-weight preference order when no Anthropic or
OpenAI model is granted), existing granted models stay unchanged, and
ungranted Codex or Claude Code models (across dedicated, user and project
layers) fail before probes or writes, naming any derived gateway alias.

Next, `setup` prints one dimmed usage notice and probes each distinct
uncached `(protocol, model)` concurrently (Chat Completions, Messages or
Responses, 1 output token each) with a live spinner on styled terminals and
deterministic sorted status rows with elapsed latency. Completed probe
results are memoized across `Back` navigation so stepping back and forward
never re-sends already-verified probes. A check that times out before any response is sent once more, so a
model can receive two one-token requests; a timeout after the response has started is reported and not sent again.
These basic checks may incur usage.
They do not show that a tool's own requests, or the models its other slots
and pins name, are accepted. A refusal fails only the tools depending on
that model; authentication failure cancels remaining checks and prevents
saving any key or configuration.

Finally, `setup` closes the vertical rail (`└`) and presents a rail-free
review table (for multiple tools: `Apply all`, `Inspect redacted diffs`,
`Back`, `Cancel`) or diff preview (for one tool: `Apply`, `Show full
redacted diff`, `Back`, `Cancel`). Only after explicit approval (or `--yes`)
does `setup` write approved files atomically with timestamped `.bak` backups
and persist launcher model preferences for approved Pi, OpenCode and OMP
targets. With changes to write, the key is saved
before the approved changes. With nothing to write, a new key is saved only when
every selected tool is already up to date, also in unattended runs without `--yes`. If no tool ends up ready or with a
file written, the previous key is put back unless another process changed the
key file meanwhile. Setup, login and logout hold one lock file, `~/.config/aiven/ai-gateway.lock`, while they change a key file or the model preferences, so they never interleave; one that waits more than 10 seconds stops and asks you to retry. When no configuration change is approved,
`ai-gateway-models.json` is not written.

Ready tools end with a compact launch table (`COMMAND` / `MODEL`, without
shell comment markers so commands paste cleanly in `cmd.exe`, PowerShell,
`bash`, `zsh` and `fish`) and the direct native command. Tools that need attention or were declined get the
`aiven-ai-gateway setup <harness>` command to run again. The exit status is
nonzero when any tool failed, was not installed, was partially written or
the run was cancelled; a decline alone exits zero but is not reported as
complete.

### Optional Shell Shortcuts

`setup` never edits shell startup files or prints shell-specific alias
blocks. If you want short commands that forward native arguments, add
equivalents for your shell:

```sh
# bash / zsh
alias claude-aiven='aiven-ai-gateway launch claude'
alias codex-aiven='aiven-ai-gateway launch codex'
alias pi-aiven='aiven-ai-gateway launch pi'
alias opencode-aiven='aiven-ai-gateway launch opencode'
alias omp-aiven='aiven-ai-gateway launch omp'
```

```fish
# fish
abbr -a claude-aiven 'aiven-ai-gateway launch claude'
abbr -a pi-aiven 'aiven-ai-gateway launch pi'
```

```powershell
# PowerShell
function claude-aiven { aiven-ai-gateway launch claude @args }
function pi-aiven { aiven-ai-gateway launch pi @args }
```

When forwarding native arguments to Pi, OpenCode or OMP without a model,
provider, help/version flag or management subcommand, `launch` still
injects the persisted gateway default model (for OpenCode `run`, immediately
after `run`). Pass an explicit `--model` or `-m` to override it.

### Credential Input and Prompts

Setup displays the selected gateway URL before reading or checking a key. It
uses `--key-stdin` first, otherwise the key saved for that gateway. A
working saved key is reused automatically without prompting; run
`aiven-ai-gateway login` or pass `--key-stdin` to replace it. Without a
usable saved key, interactive setup shows the masked `AIVEN_AI_GATEWAY_KEY`
and asks whether to use it with the displayed gateway (via an inline menu on
styled terminals or a numbered menu in plain mode). Only an explicit approval sends
it; otherwise setup reads a key with echo disabled before prompting. `--yes`
does not answer this question.

For unattended setup without a working saved key, provide the key with
`--key-stdin`, even when `AIVEN_AI_GATEWAY_KEY` is set. Add `--yes` to apply
configuration without further prompts. Without a terminal or `--yes`,
configuration is skipped and no key is saved, unless every selected tool is already up to date: then a new key from
`--key-stdin` is saved even without `--yes`. Never put a key in command
arguments; clipboard input is not supported.

```text
aiven-ai-gateway setup pi --key-stdin --yes < /path/to/gateway-key
```

## Protocol Routing and Model Catalogue

Pi and OMP use Messages for `claude-*`, Responses for `gpt-*` except
`gpt-oss`/`gpt-oss-*`, and Chat Completions for every other listed model.
Both store explicit per-model protocols and base URLs. OMP uses ordinary
API-key authentication and an explicit granted model list instead of
provider discovery; rerun setup to refresh either tool's catalogue. All
models returned by the gateway are retained in listings and catalogue
configurations. The gateway controls catalogue access; the launcher applies
no public-model allowlist. Publisher and display names do not establish a
protocol. OpenCode checks a granted representative on its one protocol.
Successful basic checks establish only the tested model's route.

OMP runs as the ordinary coding harness, with its native tools. The
configuration has been checked against OMP 18.15.0; there is no runtime
version gate.

`login` displays the selected gateway before reading an explicitly supplied
key, checks model listing and saves the key. It does not use an environment
key, test inference or configure a harness. `models` displays the gateway on
stderr before its request. `models` and `login` after saving list the
granted models grouped by publisher. Each entry leads with a readable name
when one is known, followed by the exact id that harness flags take. The
group is the catalogue's `owned_by` when it states one, then a reviewed
table of exact gateway ids (`internal/models/groups.go`); a model with
neither is listed under "Publisher unknown". A private alias reported with
another owner, such as Aiven, stays under that owner with its id, even when
it reuses a public id. Ids are never grouped by prefix. LiteLLM's own
listing reports `owned_by: openai` for every model, so that value alone does
not place a model. A terminal wide enough shows several entries per line;
redirected output has one entry per line and no colour. Pi, OMP and OpenCode
show the same readable names in their model pickers once setup has rewritten
their configuration.

## Key Storage, Helpers and Environment Isolation

After the checks, setup saves the bare key and a newline, mode `0600`, in
the key file of the configured gateway. The default gateway's
(`https://ai.aiven.io`) is `~/.config/aiven/ai-gateway-key`, the file the
Aiven Console's instructions write. Any other gateway's is
`~/.config/aiven/ai-gateway-keys/<name>`, where `<name>` is the gateway URL
with each run of characters other than `a`-`z`, `0`-`9`, `.` and `-`
replaced by `-`, then the first 12 hex digits of the URL's SHA-256:
`https://gw.example:8443` gets `https-gw.example-8443-` and its hash.

`logout` removes the configured gateway's key file, leaving other gateways'
key files and harness configuration. It does not revoke the server key or
unset inherited environment variables.

Each configured tool reads the key file of the gateway it was set up for, so
a later login to another gateway changes no configured tool's key. Claude
Code, Codex, Pi and OMP run a helper that prints that key file and fails
without it; OpenCode reads the file when it starts. The launcher clears an
inherited `AIVEN_AI_GATEWAY_KEY` when starting these file-backed tools and
clears `ANTHROPIC_AUTH_TOKEN` and `ANTHROPIC_CUSTOM_HEADERS` for Claude
Code, so a credential held in either is not sent to the gateway. For
compatible manual configuration that reads `AIVEN_AI_GATEWAY_KEY`, launch
sets it to the selected gateway's validated saved key. Unrelated inherited
environment entries stay.

The generated helpers do not fall back to `AIVEN_AI_GATEWAY_KEY`, which
names no gateway. The Aiven Console's helper does, so launch refuses a tool
configured with it until `aiven-ai-gateway setup <tool>` rewires it. A save
replaces the key file in one rename, and a new key for a gateway reaches a
running session of a tool set up for it the next time that tool runs its
helper.

On Windows the helpers run PowerShell, except Pi's: Pi runs key commands in
bash, so setup configures Pi on Windows only when Git Bash, or another bash
on `PATH` other than the WSL launcher, is where Pi looks for it.

Pi sends a credential saved for the `aiven` provider in
`~/.pi/agent/auth.json` instead of running the helper, so setup and launch
refuse while one is there; `/logout` in Pi removes it. They read that file
and never change it.

The launcher supports the documented default config locations. It refuses
`CODEX_HOME`, `PI_CODING_AGENT_DIR`, `OPENCODE_CONFIG`,
`OPENCODE_CONFIG_DIR` or `CLAUDE_CONFIG_DIR` redirects rather than
configuring files the tool would not read. Configure redirected
installations manually. OMP additionally refuses `OMP_PROFILE`, `PI_PROFILE`
and `PI_CONFIG_DIR`.

## Diff Previews, Redaction and File Replacement

Setup shows configuration changes and their backup paths before writing. Pi,
OpenCode and OMP catalogue previews show two model names; `v` displays the
full redacted diff. Both diff sides redact unrelated provider and MCP
credentials. Redaction goes by field name: it masks the values of fields
named like a key, token, secret, password, credential, bearer, auth, cookie
or session, comments that mention one, URL user info and key-shaped strings.
A credential under another name, under an escaped JSON field name, or in a
TOML table whose header alone names a credential (`[auth]`), is shown.

Each changed existing file gets an exclusively created timestamped backup,
such as `models.json.aiven-20261028T120000Z.bak`; collisions receive a
numeric suffix. Unchanged reruns make no config backup. JSON and TOML files
are edited in place: only the values setup changes are rewritten, so
comments and layout elsewhere are kept. A JSON file written on one line
without comments is rewritten in the standard layout. Syntax a tool's own
reader rejects is removed rather than kept: comments and trailing commas in
Claude Code's settings and Pi's `settings.json`, and block comments in Pi's
`models.json`. The preview shows those lines go. Codex's files must declare
the Aiven provider as a `[model_providers.aiven]` table; setup refuses other
forms.

Setup refuses files edited since inspection, symlink targets and nonregular
targets, and writes only inside the directories it inspected. Setup and
launch read configuration and key files only when they are regular files: a
symlink, such as one a dotfiles manager made, or a FIFO is refused rather
than followed or waited on. Replace a symlink with the file it points to.

Replacements are atomic per file; an edit saved in the instant between
setup's last check and the replacement is lost. Backups and atomic
replacements preserve POSIX permission bits on Unix and the file's DACL on
Windows; extended ACLs on Unix (`chmod +a` on macOS, `setfacl` on Linux) are
not preserved. A multi-file failure can leave earlier files configured: the
error names completed files and recovery copies. Fix the cause and rerun, or
restore those copies before retrying. Declining or skipping all tool
configurations saves neither the key nor model preferences.

## Per-Harness Setup and Launch Rules

### Shared Launch Rules

`launch` requires compatible native Aiven setup for the target gateway.
Missing or incompatible wiring prints `aiven-ai-gateway setup <harness>` and
exits. Compatible manual configuration counts; no launcher completion marker
is used. Launch displays the selected gateway on stderr, announces its
model-access request, and selects a session profile/model without refreshing
persistent provider metadata or changing defaults. It sends no test request
and prints no model list before starting the tool. Native clients still
write their own session/history data.

Arguments after the harness name (or after `--` when picking a harness; an
optional leading `--` after the harness name is also stripped) are passed as
a literal native argument vector. Launcher flags such as `--plain` or
`--help` go before the harness name. For Pi
and OMP, `launch` prepends `--model aiven/<alias>` unless the arguments
already specify a model, provider, help/version flag or management
subcommand (`update`, `install`, `remove`, `list`, `config`, `plugin`,
`commit`). For OpenCode, `launch` injects `-m aiven/<alias>` when starting
interactively or immediately after `run` unless a model, provider or
help/version flag is already present, leaving other subcommands (`models`,
`auth`, `serve`, …) untouched. In all three tools, `<alias>` is the model
persisted by `setup` in `~/.config/aiven/ai-gateway-models.json` for that
gateway when still granted (otherwise falling back to the catalogue
default). OMP keeps all its native tools. Codex selects `-p aiven`; later
native profile/config/model options follow Codex's own precedence. Claude
adds launcher-owned inline settings; a later `--model` follows native
precedence. Use native configuration overrides deliberately: they can change
the selected provider or settings.

### Claude Code

Setup writes `~/.claude/aiven.settings.json` (and `~/.claude/settings.json`
when Aiven becomes the native default), retaining unrelated fields in
`aiven.settings.json` for the direct `claude --settings
~/.claude/aiven.settings.json` invocation. Launcher execution does not copy
those dedicated-file extras into argv; it passes only launcher-owned
settings.

Claude loads canonical user/project settings additively, preserving MCP,
hooks and project model pins. It clears conflicting gateway auth/backend
fields and wires the selected gateway/model slots for the session. Canonical
user model pins override the generated dedicated default; project settings
and then local project settings override user pins. As in Claude Code, `env`
merges apart from `model`, so an `ANTHROPIC_MODEL` from any of these files
runs over every file's `model`, and a blank one in a later file clears an
earlier one. A blank slot pin such as `ANTHROPIC_DEFAULT_OPUS_MODEL` clears
an earlier one too, leaving the launcher's default for that slot. Explicit
native `--model` follows Claude's own precedence. Setup validates each
settings file it writes and probes the effective model resolved across
these layers, refusing before any inference request or key save when the
key does not grant it.

The context window follows the model the session runs, including a native
`--model` and Claude's `opus`, `sonnet`, `haiku` and `fable` aliases. Setup
drops a `[1m]` suffix, in any case, from a kept model whose catalogue window
is under 1M, and never adds one to a model the user chose. Below 1M, setup
and launch declare it with `CLAUDE_CODE_MAX_CONTEXT_TOKENS` for a model id
Claude Code does not recognise, and with `CLAUDE_CODE_AUTO_COMPACT_WINDOW`
for one it does, since Claude Code ignores the first for its own models; the
second outranks `/autocompact`. The one not in use is set blank, which
Claude Code reads as unset, so a value left in the environment or in other
settings cannot lower the window. They are set when the session starts, so a
model picked later with `/model` keeps them.

### Codex

Codex setup writes the Aiven provider in `~/.codex/config.toml` and the
dedicated profile in `~/.codex/aiven.config.toml` (plus top-level
model/provider in `config.toml` when Aiven becomes the native default). It
validates both TOML files and keeps an existing dedicated profile's model,
MCP servers and unrelated settings while updating the Aiven wiring.

Codex no longer reads profiles from `config.toml`, so setup moves the
settings of a legacy `[profiles.aiven]` table into `aiven.config.toml`,
unless that file sets them already, and removes the table and a top-level
`profile = "aiven"`. It refuses a top-level `profile` naming another
profile, which Codex also refuses: remove it and run `codex --profile
<name>`, which reads `<name>.config.toml`.

Codex setup disables hosted web search in the Aiven profile for every model,
since the gateway refuses it on Gemini models. It never changes `web_search`
in canonical `config.toml`, even when Aiven becomes the native default:
plain `codex` then keeps your own setting, or Codex's default, and its
requests to Gemini models fail unless that setting is `"disabled"`. Rerun
setup to apply this setting to existing Aiven configuration.

### OpenCode

OpenCode setup writes the Aiven provider in
`~/.config/opencode/opencode.json`, or `opencode.jsonc` when it exists
(`$XDG_CONFIG_HOME/opencode` when set), and updates `model` when Aiven
becomes the native default. OpenCode reads both `opencode.json` and
`opencode.jsonc`, so when setup edits `opencode.jsonc` it also removes an
Aiven provider left in `opencode.json`. Setup and launch refuse a relative
`XDG_CONFIG_HOME`, which OpenCode would resolve against the directory each
session starts in.

### Pi and OMP

Pi setup updates `~/.pi/agent/models.json`, plus `~/.pi/agent/settings.json`
when Aiven becomes the native default.

OMP updates the first native-consumed YAML file: `.yml` wins when it exists
(`~/.omp/agent/models.yml`, and `modelRoles.default` in
`~/.omp/agent/config.yml` when Aiven becomes the native default), otherwise
existing `.yaml` is used. OMP's stored default can be overridden by native
project settings or enabled-model scope. Setup preserves `enabledModels` and
`disabledProviders`; a disabled Aiven provider must be enabled in OMP before
using it.

OMP rewrite preserves unrelated YAML comments and anchors, and refuses edits
that would change unrelated alias values. The rewrite may normalise
formatting whitespace in unrelated nodes, such as spaces before a trailing
comment or padding in flow sequences. A shared or aliased owned mapping may
need to be made independent before setup. Without consumed YAML, legacy
`models.json` or `settings.json` stops setup before credentials, network
requests or writes: run `omp` once to let OMP migrate, then rerun setup. If
native migration fails, fix the legacy file first. An `agent.db` alone does
not trigger that refusal. Setup never changes the database.

## Output Streams and Terminal Safety

Help and the model list go to stdout. Prompts, progress, previews, errors
and next steps go to stderr, so `aiven-ai-gateway models > models.txt`
captures only the list. Each stream is checked separately: colour and the
inline lists need a terminal on stderr (and stdin for the lists), whatever
stdout is. Help and the model list are styled only when stdout itself is a
terminal; redirected or plain help has the same words without escapes.

Catalogue and configuration text is stripped of control characters before it
is shown, so it cannot move the cursor or recolour the terminal. Copyable
commands are preserved exactly. If a native command's arguments contain
terminal controls, setup omits that command and explains how to use the
launcher instead. Saved selectors and launcher arguments keep their original
values.
