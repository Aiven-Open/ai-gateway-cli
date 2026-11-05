# Releasing aiven-ai-gateway

`.github/workflows/release.yaml` builds, signs, notarizes and publishes on a `v*` tag on `main`. The GitHub release and the Homebrew cask (`Casks/aiven-ai-gateway.rb`) land in the public `aiven/homebrew-tap`; this repository holds no release assets. The source tag `vX.Y.Z` is released in the tap as `aiven-ai-gateway-vX.Y.Z`, created on the tap's `main`, with this repository's commit subjects since the previous tag as release notes. Prerelease tags (`-rc.N`) publish the release but not the cask.

GoReleaser builds, signs, notarizes and generates the archives, checksums, notes and cask with `--skip=publish`, because it cannot set a tag in another repository. `.github/scripts/publish-tap.sh` then publishes them: it uploads to a draft release and publishes it once every asset is there, then writes the cask through the contents API, guarded by the blob it read.

## Testing the publisher locally

With Bash, `jq`, `tar` and `shasum` installed, run the publisher cases against
synthetic release output:

```sh
bash .github/scripts/publish-tap-test.sh
```

To run the same cases against actual GoReleaser output, supply the build
directories for `v0.2.0-rc.1` and `v0.2.0`, respectively:

```sh
bash .github/scripts/publish-tap-test.sh path/to/rc-dist path/to/stable-dist
```

Their metadata must match those tags and versions; snapshot output and
symlinked build files are refused.
The tests leave the supplied directories unchanged and use a fake `gh` with
no credentials, network access or GitHub writes. They check build format,
preflight failures and retry behavior; they do not qualify GitHub permissions,
signing, notarization or installation.

## One-time setup

- Create the public repository `aiven/homebrew-tap` with a default branch.
- Create a fine-grained token (bot account) scoped to `aiven/homebrew-tap` only, with Contents read/write: `HOMEBREW_TAP_TOKEN`.
- From Apple Developer, export a Developer ID Application certificate as p12: base64 of the file is `MACOS_SIGN_P12`, its password `MACOS_SIGN_PASSWORD`.
- From App Store Connect, create an API key (p8): base64 of the p8 is `MACOS_NOTARY_KEY`, with `MACOS_NOTARY_KEY_ID` and `MACOS_NOTARY_ISSUER_ID`.
- Create the `release` environment in this repository with required reviewers, "Prevent self-review" and a deployment tag rule `v*`, and store the secrets above there, not as repository secrets.
- Add a tag ruleset on `v*` that lets only maintainers create, update or delete such tags. A tag push runs the `release.yaml` of the tagged commit, which need not be on `main`, so the ruleset and the environment reviewers are the gate.

## Cutting a release

```sh
git tag vX.Y.Z origin/main
git push origin vX.Y.Z
```

Approve the run in the `release` environment.

A published version is never replaced. A rerun accepts a tap release only when it holds exactly the new build's archives and `checksums.txt`, byte for byte, and fails otherwise; signing makes rebuilt bytes differ, so a version whose release was published but whose run failed is fixed by releasing a new version. A run that failed before publishing leaves a draft release, which has no tag yet: delete that draft, then rerun.

## First release

Tag `v0.2.0-rc.1`, then `v0.2.0`. Start with the rc. It checks what config validation cannot: that the release is created in a tap that lacks this repository's history, and that `spctl` accepts the notarized bare binary (it cannot be stapled; Gatekeeper checks it online). On a Mac:

```sh
gh release download aiven-ai-gateway-vX.Y.Z-rc.N -R aiven/homebrew-tap -p '*darwin_arm64.tar.gz' && tar xzf aiven-ai-gateway_*_darwin_arm64.tar.gz aiven-ai-gateway
codesign --verify --strict --verbose=2 ./aiven-ai-gateway
spctl --assess --type open --context context:primary-signature --verbose=4 ./aiven-ai-gateway
```

Then tag the final version and verify on a clean Mac:

```sh
brew install aiven/tap/aiven-ai-gateway
codesign --verify --strict --verbose=2 "$(which aiven-ai-gateway)"
spctl --assess --type open --context context:primary-signature --verbose=4 "$(which aiven-ai-gateway)"
aiven-ai-gateway --version
AIVEN_AI_GATEWAY_BASE_URL=<gateway URL> aiven-ai-gateway login
AIVEN_AI_GATEWAY_BASE_URL=<gateway URL> aiven-ai-gateway models
```
