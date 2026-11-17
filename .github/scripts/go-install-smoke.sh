#!/usr/bin/env bash
# Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

# Installs the CLI into an empty GOBIN with `go install`, then runs --version and --help, as a user
# installing with Go would. Run from the repository root on Linux, macOS or Windows (Git Bash).
set -euo pipefail

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
# Go on Windows needs a native path for GOBIN; Git Bash's pwd -W prints one.
gobin="$(cd "$tmp" && (pwd -W 2>/dev/null || pwd))"
GOBIN="$gobin" go install ./cmd/aiven-ai-gateway
"$tmp/aiven-ai-gateway" --version
"$tmp/aiven-ai-gateway" --help > /dev/null
