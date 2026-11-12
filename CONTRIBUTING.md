# Contributing

Contributions are welcome. Open an issue first to discuss a larger change.

## Development

### Prerequisites

With [`mise`](https://mise.jdx.dev/), run `mise install` in the repository
root to install the pinned Go, Node, `golangci-lint` and `govulncheck`
versions from [`mise.toml`](mise.toml). Install `gitleaks` (8.30 or later)
for secret scanning, and `goreleaser` (2.18 or later) when testing snapshot
release archives.

Without `mise`, install Go 1.27.4 or later (or rely on `GOTOOLCHAIN=auto`),
Node 20 or later, `golangci-lint`, `govulncheck`, `gitleaks`, Bash, `jq`,
`tar` and `shasum`.

### Build and run locally

Build into `dist/` (ignored by Git) or run directly from source:

```sh
mise run build
./dist/aiven-ai-gateway --help

# Without mise:
CGO_ENABLED=0 go build -trimpath -o dist/aiven-ai-gateway ./cmd/aiven-ai-gateway
go run ./cmd/aiven-ai-gateway models
```

### Checks

Run `mise run check` before opening a pull request. Add or update tests with
every change in behaviour, and update [`README.md`](README.md) and
[`docs/reference.md`](docs/reference.md) when user-visible behaviour changes.

| `mise` task | Equivalent command |
| --- | --- |
| `mise run build` | `CGO_ENABLED=0 go build -trimpath -o dist/aiven-ai-gateway ./cmd/aiven-ai-gateway` |
| `mise run fmt` | `gofmt -w cmd internal npm` |
| `mise run lint` | `go vet ./...`, `gofmt -l cmd internal npm`, `golangci-lint run ./...` |
| `mise run test` | `go vet ./...`, `gofmt -l cmd internal npm`, `go test -race ./...` |
| `mise run test-npm` | `npm test --prefix npm` |
| `mise run check` | All of `lint`, `go test -race ./...`, `govulncheck ./...`, `gitleaks git --config .gitleaks.toml --no-banner --redact`, `npm test --prefix npm`, `bash .github/scripts/publish-tap-test.sh` |
| `mise run release-check` | `bash .github/scripts/publish-tap-test.sh`, `goreleaser check`, `goreleaser release --snapshot --clean` |

The Go tests drive `cli.Run` against an in-process gateway double and a
recording `execx.Runner`; helper-process tests cover the real handoff,
`syscall.Exec` on Unix and a child process on Windows. See
[`npm/README.md`](npm/README.md) for building, verifying and smoke-testing
the eight npm tarballs.

### Repository layout

- `cmd/aiven-ai-gateway`: binary entrypoint and VCS/ldflags version metadata.
- `internal/cli`: command dispatch, inline terminal lists, plain-mode
  prompts, redacted diff previews and setup/launch orchestration.
- `internal/config`: environment parsing, HTTPS origin validation and home
  directory lookup.
- `internal/creds`: `0600` per-gateway key files and shell/PowerShell key
  helpers.
- `internal/execx`: native harness process execution (`syscall.Exec` on Unix,
  child process on Windows).
- `internal/fsutil`: regular-file checks, directory-handle traversal,
  exclusive `.bak` backups and atomic file replacement.
- `internal/gateway`: `/v1/models` client and 1-token protocol checks.
- `internal/harness/{claude,codex,omp,opencode,pi}`: per-tool config
  inspection, in-place JSON/TOML/YAML editing and launch argument wiring.
- `internal/models`: protocol routing and publisher grouping
  (`internal/models/groups.go`).
- `internal/redact`: credential masking in configuration diffs.
- `npm/`: `@aiven/ai-gateway` Node wrapper, native package staging, notice
  verification and offline smoke tests.
- `docs/reference.md`: behavioural and security specification.

### Contributor notes

- **`THIRD_PARTY_NOTICES` lockstep:** `npm/scripts/notices.mjs` checks that
  [`THIRD_PARTY_NOTICES`](THIRD_PARTY_NOTICES) matches `go env GOVERSION` and
  every linked module version in `go.mod` across all seven target platforms.
  Update `THIRD_PARTY_NOTICES` in the same commit as any Go toolchain or
  dependency change.
- **HTTPS-only gateway origins:** `AIVEN_AI_GATEWAY_BASE_URL` rejects plain
  `http://` (including loopback) and requires a trusted TLS certificate;
  automated tests use in-process gateway doubles, and
  `npm run smoke --prefix npm` exercises packed binaries against a loopback
  TLS fixture.

## Commits

Each commit is one logical change that builds and passes the checks on its
own, so that it can be reviewed and reverted alone.

- The subject is `component: summary`, where the component is the package or
  area changed (`cli`, `codex`, `npm`, `ci`, `docs`). Write it in the
  imperative, lower case after the colon, without a full stop or issue key.
  Aim for 50 characters and never exceed 72.
- The body says why the change is needed, the alternative you rejected if
  there was one, and any work still needed. It does not explain the code.
  Keep it to about 120 words and wrap it at 72 columns. A change that needs
  no explanation has no body.
- A GitHub issue, when there is one, goes on its own line at the end of the
  body, as `#123`, with nothing else on that line.
- A message must make sense to someone reading `git blame` years later,
  without the pull request or issue: leave out review rounds, test runs,
  people's names and other notes on how the change was made.
- History shows the final design. Fold review fixes into the commit they fix
  rather than adding fixup commits, and do not add code that a later commit in
  the same pull request replaces. If a commit cannot build alone, add a small
  enabling commit before it rather than temporary code.

## Pull requests

The title says what the pull request changes and carries no issue key. The
description says why. Pull requests are merged with a merge commit, so the
commits stay as written.

## Security

Report vulnerabilities as described in [SECURITY.md](SECURITY.md), not in a
public issue.

## Releases

Maintainers release as described in [RELEASING.md](RELEASING.md).
