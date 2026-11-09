<!-- Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/ -->

# aiven-llm

Launcher that starts Claude Code, Codex, pi, opencode or OMP against [Aiven AI Gateway](https://aiven.io/) with per-session wiring. OMP launches need the `aiven-gateway-router` plugin, which is not published yet.

## Install

The Homebrew tap is pending. Meanwhile, with Go 1.26.2 or later (or the default `GOTOOLCHAIN=auto`):

```
go install github.com/Aiven-Open/ai-gateway-cli/cmd/aiven-llm@latest
```

## Use

Create an AI Gateway key in Aiven Console, then store it and launch a harness:

```
aiven-llm login
aiven-llm claude
```

`aiven-llm models` lists the models the key grants with prices, `aiven-llm logout` removes the key, `aiven-llm help` lists the harness commands. Harness config is generated per launch; the harness's own profile is left alone.

## Environment

| Variable | Effect |
|---|---|
| `AIVEN_LLM_BASE_URL` | Gateway URL. Defaults to `https://ai.aiven.io`; keys are stored per gateway URL. |
| `AIVEN_LLM_HOME` | Launcher state and generated config. Defaults to `~/.config/aiven-llm` on all platforms. |
| `AIVEN_LLM_KEYSTORE` | `auto` (default: OS keychain, file fallback), `keyring` or `file`. |
| `PI_CONFIG_DIR` | omp config root, joined onto the home directory (omp does this even for absolute values). Defaults to `.omp`. |
| `AIVEN_LLM_IT_KEYCHAIN` | `1` runs the macOS keychain integration test against the real login keychain. |

## Development

```
mise run test
```

Vets, checks `gofmt` and runs the tests. The tests drive `cli.Run` against a gateway double and a recording `execx.Runner`; one helper-process test covers the real `syscall.Exec`.
