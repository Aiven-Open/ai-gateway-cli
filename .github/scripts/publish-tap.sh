#!/usr/bin/env bash
# Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

# Publishes a `goreleaser release --skip=publish` build to the Homebrew tap: the release, tagged
# aiven-ai-gateway-<source tag> on the tap's main, and for a final version the generated cask.
# GoReleaser cannot set a tag in another repository, so this does what its release and cask
# publishers would. A published version is never replaced: a rerun accepts a published release
# only when every asset it holds is byte-identical to this build's, and fails on anything else.
# GH_TOKEN needs Contents write on the tap; GITHUB_REF_NAME is the pushed source tag.
set -euo pipefail
export LC_ALL=C

tap=aiven/homebrew-tap
cask=Casks/aiven-ai-gateway.rb
dist=${1:-dist}

fail() {
  echo "$*" >&2
  exit 1
}

tag=$(jq -r .tag "$dist/metadata.json")
version=$(jq -r .version "$dist/metadata.json")
[[ $tag == "${GITHUB_REF_NAME:?}" ]] || fail "built $tag, but the pushed tag is $GITHUB_REF_NAME"
tap_tag=aiven-ai-gateway-$tag
prerelease=false
if [[ $version == *-* ]]; then
  prerelease=true
fi

# Check the build before writing anything: every target's archive holds the binary and its licence
# texts, checksums.txt lists exactly those archives and matches their bytes, and the cask names
# each one at the tap tag.
archives=()
for target in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64; do
  archive=$dist/aiven-ai-gateway_${version}_$target.tar.gz
  [[ -f $archive ]] || fail "missing $archive"
  listing=$(tar -tzf "$archive" | sort | paste -sd ' ' -)
  [[ $listing == "LICENSE THIRD_PARTY_NOTICES aiven-ai-gateway" ]] || fail "$archive holds: $listing"
  archives+=("$archive")
done
listed=$(awk '{ print $2 }' "$dist/checksums.txt" | sort | paste -sd ' ' -)
[[ $listed == "$(printf '%s\n' "${archives[@]##*/}" | sort | paste -sd ' ' -)" ]] ||
  fail "checksums.txt lists: $listed"
(cd "$dist" && shasum -a 256 --check --strict --quiet checksums.txt) >&2 ||
  fail "an archive's bytes differ from $dist/checksums.txt"
generated=$dist/homebrew/$cask
[[ -f $generated ]] || fail "GoReleaser generated no $generated"
grep -qF "version \"$version\"" "$generated" || fail "$generated is not version $version"
while read -r sum name; do
  grep -qF "sha256 \"$sum\"" "$generated" || fail "$generated lacks the checksum of $name"
done <"$dist/checksums.txt"
url="https://github.com/$tap/releases/download/aiven-ai-gateway-v#{version}/"
[[ $(grep -cF "url \"$url" "$generated") == "${#archives[@]}" ]] || fail "$generated does not download from $url"

assets=("${archives[@]}" "$dist/checksums.txt")
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

if gh release view "$tap_tag" --repo "$tap" --json isDraft --jq .isDraft >"$work/draft" 2>"$work/err"; then
  if [[ $(<"$work/draft") == true ]]; then
    fail "$tap_tag is an unpublished draft from an earlier run: delete the draft, which has no tag yet, and rerun"
  fi
  mkdir "$work/published"
  gh release download "$tap_tag" --repo "$tap" --dir "$work/published" ||
    fail "cannot download the assets of $tap_tag"
  for asset in "${assets[@]}"; do
    name=${asset##*/}
    [[ -f $work/published/$name ]] || fail "$tap_tag lacks $name; it is not replaced: release a new version"
    cmp -s "$work/published/$name" "$asset" ||
      fail "$tap_tag holds other bytes for $name; it is not replaced: release a new version"
  done
  held=$(find "$work/published" -type f | wc -l | tr -d ' ')
  [[ $held == "${#assets[@]}" ]] || fail "$tap_tag holds $held assets, not ${#assets[@]}"
  echo "$tap_tag is already published with identical assets"
elif grep -q 'release not found' "$work/err"; then
  # A draft gets its tag only when published, so a failed upload leaves no tag behind.
  gh release create "$tap_tag" --repo "$tap" --target main --draft --prerelease="$prerelease" \
    --title "aiven-ai-gateway $version" --notes-file "$dist/CHANGELOG.md" "${assets[@]}"
  gh release edit "$tap_tag" --repo "$tap" --draft=false
else
  cat "$work/err" >&2
  exit 1
fi

# Prerelease versions leave the cask, so brew installs only final versions.
if [[ $prerelease == true ]]; then
  exit 0
fi
api=repos/$tap/contents/$cask
sha=()
if gh api "$api?ref=main" >"$work/current.json" 2>"$work/err"; then
  jq -r .content "$work/current.json" | base64 -d >"$work/current.rb"
  if cmp -s "$work/current.rb" "$generated"; then
    echo "$cask is already current"
    exit 0
  fi
  # The blob sha makes the update fail if the cask changed since it was read.
  sha=(-f "sha=$(jq -r .sha "$work/current.json")")
elif ! grep -q 'HTTP 404' "$work/err"; then
  cat "$work/err" >&2
  exit 1
fi
gh api -X PUT "$api" -f message="aiven-ai-gateway $version" -f branch=main \
  -f content="$(base64 <"$generated" | tr -d '\n')" ${sha[@]+"${sha[@]}"} >/dev/null ||
  fail "$cask was not updated, though $tap_tag is published; a rebuild signs other bytes, so release a new version"
echo "updated $cask to $version"
