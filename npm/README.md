# @aiven/ai-gateway

This package installs the `aiven-ai-gateway` command. npm selects one
version-matched native package using its `os`/`cpu` metadata; this package
contains only JavaScript and documentation. There are no install hooks,
external binary URLs, telemetry or automatic updates.

```text
npx @aiven/ai-gateway
npx @aiven/ai-gateway setup claude
npx @aiven/ai-gateway launch codex -- resume
npm install -g @aiven/ai-gateway
aiven-ai-gateway --help
```

Setup and launch need the native coding tools installed. Bare invocation
displays the help without setup or launch.

Native packages exist for Linux x64, ARM64 and ARMv7; macOS x64 and ARM64; and
Windows x64 and ARM64. Linux binaries have CGO disabled and no libc
interpreter, so they run on glibc and musl alike. ARMv6 and other OS/CPU pairs
are refused.

The wrapper resolves the installed native binary by its absolute path and
forwards every argument unchanged, with inherited streams and environment. It
never searches PATH for its own name. Ctrl-C is left to the tool, which
receives it from the terminal or console. On Unix, the wrapper forwards SIGTERM
and SIGHUP, and the tool's exit status and terminating signal propagate; a
signal Node ignores, such as SIGPIPE, becomes status 128 plus its number. On
Windows, the exit status propagates and console close is also left to the
tool. Wrapper errors do not print argv.
Use hidden input or `--key-stdin` for credentials.

Optional dependencies must be enabled. `--omit=optional`, a missing binary or
a package version or platform mismatch produces an explicit reinstall error.
Local installs, global installs and npm exec/npx use the same module resolution.
`npm exec --offline --package=@aiven/ai-gateway@<version> -- aiven-ai-gateway`
works after the same npm exec/npx invocation has populated its package and
metadata caches. A global install alone need not populate npm exec's metadata.

## Building the packages

Use the repository's pinned Go toolchain and Node 20 or later. Preparation
needs the Go modules in the module cache or reachable through the configured
proxy. It writes only to a new output directory, builds the targets one after
another and never publishes. Every tarball includes LICENSE and
THIRD_PARTY_NOTICES, and identifies the public source repository. Preparation
checks the notices against the linked modules and toolchain on all seven
targets; a dependency or toolchain change requires reviewing the notices.
Each native tarball contains one executable plus package metadata and
provenance: version, source commit and dirty state,
source timestamp, toolchain, `GOFLAGS`, target, linker flags and SHA-256. All
packages take their version from one input.

```text
npm test --prefix npm
npm run prepare-packages --prefix npm -- --out /absolute/new/output
npm run check-artifacts --prefix npm -- /absolute/new/output/artifacts
AIVEN_NPM_PREPARED=/absolute/new/output npm run test:artifacts --prefix npm
npm run prepare-packages --prefix npm -- --out /absolute/new/repeated-output
npm run check-reproducibility --prefix npm -- /absolute/new/output /absolute/new/repeated-output
npm run smoke --prefix npm -- /absolute/new/output /absolute/new/install
```

`--version <semver>` prepares that version instead of the one in
`package.json`. `SOURCE_DATE_EPOCH` overrides the source commit timestamp,
which also sets the date `--version` prints. Repeated builds with the same
source, toolchain, version, timestamp and GOFLAGS produce the same binary
bytes. CI disables saved Go environment settings, builds twice into fresh
directories and compares each binary's bytes.

To install a prepared build by hand, install the main tarball and only the
matching native tarball into an isolated prefix, with
`--offline --ignore-scripts`. No registry access is needed:

```text
npm install -g --prefix /absolute/test-prefix --offline --ignore-scripts \
  /absolute/new/output/artifacts/aiven-ai-gateway-0.0.0-dev.tgz \
  /absolute/new/output/artifacts/aiven-ai-gateway-darwin-arm64-0.0.0-dev.tgz
/absolute/test-prefix/bin/aiven-ai-gateway --help
```

The example native tarball is for macOS ARM64; choose the one for your
machine, and use an isolated HOME, npm cache and npm configuration. `smoke`
does the same for the current machine and checks help, the npm shim and a
rejected argument. It then sets up and launches each tool against a loopback
gateway with a synthetic key, with the Go fixture standing in for the tools,
and checks the files setup writes, the arguments each tool receives and the
exit status.

The artifact tests reject missing or extra binaries, invalid platform or
version data, wrong executable headers, hash mismatches, missing or changed
licenses and an incorrect source repository. The wrapper tests
use a native Go fixture and a loopback registry to check npm's package
selection, global and local installation, offline execution, argv, exit
status, streams, signals and resolution failures.

Publish the seven native packages at the exact version before this package,
so that no install of this package resolves a native package that does not
exist yet.
