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

## npm packages

The `npm` job in the same workflow publishes `@aivenio/ai-gateway` and its seven native packages
(`npm/README.md`). It does not depend on the Homebrew job: each succeeds or fails on its own, and until
the tap and the signing secrets exist the Homebrew job fails while the `npm` job publishes.

The job prepares all eight packages at the tag's version, inspects the archives and runs the install
smoke check, which also covers the `avn-aig` shim, and only then publishes: the seven native packages first, then the wrapper, each with
provenance. A prerelease tag (`-rc.N`) is published under the `next` dist-tag and leaves `latest`
alone, so `npx @aivenio/ai-gateway@next` runs the candidate.

A published version is never replaced. On a rerun, a package whose published integrity equals the
rebuilt tarball's is skipped, so a run that failed midway is finished by rerunning it; a package
whose integrity differs fails the job, and the fix is a new version. Use "Re-run failed jobs" (or
`gh run rerun <run-id> --job <job-id>`), not "Re-run all jobs": once the Homebrew job publishes,
rerunning it fails, because signing makes its rebuilt archives differ.

One-time setup:

- Be a member of the `aivenio` organisation on npmjs.com, with two-factor authentication enabled.
- For the first release only, create a granular access token and store it as `NPM_TOKEN` in the
  `release` environment. It needs read and write under "Packages and scopes" for the `@aivenio`
  scope, "Bypass 2FA" enabled (it is off by default, and without it the publish stops at an
  interactive prompt), and a short expiry. It needs no organisation-management permission.
- After the first release, add a trusted publisher to each of the eight packages on npmjs.com
  (repository `Aiven-Open/ai-gateway-cli`, workflow `release.yaml`, environment `release`). Then
  delete `NPM_TOKEN` from the environment and revoke the token. npm 11 tries trusted publishing
  first and falls back to the token, so the job needs no change between the two states. When
  adding a trusted publisher, allow it to publish directly, not only to stage.

To see what a prepared build would publish, without publishing (it asks npmjs.com about each
package, so CI does not run it):

```sh
npm run publish-packages --prefix npm -- /absolute/new/output --dry-run
```

## Cutting a release

```sh
git tag vX.Y.Z origin/main
git push origin vX.Y.Z
```

Approve the run in the `release` environment. "Prevent self-review" means the maintainer who pushed
the tag cannot approve it; the other one must.

A published version is never replaced. A rerun accepts a tap release only when it holds exactly the new build's archives and `checksums.txt`, byte for byte, and fails otherwise; signing makes rebuilt bytes differ, so a version whose release was published but whose run failed is fixed by releasing a new version. A run that failed before publishing leaves a draft release, which has no tag yet: delete that draft, then rerun.

## First release

Tag `v0.2.0-rc.1`, then `v0.2.0`. Start with the rc. It checks what config validation cannot: that the release is created in a tap that lacks this repository's history, and that `spctl` accepts the notarized bare binary (it cannot be stapled; Gatekeeper checks it online). On a Mac:

```sh
gh release download aiven-ai-gateway-vX.Y.Z-rc.N -R aiven/homebrew-tap -p '*darwin_arm64.tar.gz' && tar xzf aiven-ai-gateway_*_darwin_arm64.tar.gz aiven-ai-gateway
codesign --verify --strict --verbose=2 ./aiven-ai-gateway
spctl --assess --type open --context context:primary-signature --verbose=4 ./aiven-ai-gateway
```

The same tags publish to npm. After the rc, check on a clean Mac, a Linux machine and Windows:

```sh
npx @aivenio/ai-gateway@next --version
```

The npm macOS binaries are not signed with the Developer ID; the rc shows whether Gatekeeper accepts them.

The registry normally makes the first version of a new package its `latest` too, even when it is
published under `next`. Check
with `npm dist-tag ls @aivenio/ai-gateway` after the rc: until the final version is published, a plain
`npm install @aivenio/ai-gateway` installs the candidate.

Then tag the final version and verify on a clean Mac:

```sh
brew install aiven/tap/aiven-ai-gateway
codesign --verify --strict --verbose=2 "$(which aiven-ai-gateway)"
spctl --assess --type open --context context:primary-signature --verbose=4 "$(which aiven-ai-gateway)"
aiven-ai-gateway --version
AIVEN_AI_GATEWAY_BASE_URL=<gateway URL> aiven-ai-gateway login
AIVEN_AI_GATEWAY_BASE_URL=<gateway URL> aiven-ai-gateway models
```
