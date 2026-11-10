#!/usr/bin/env bash
# Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

# Runs publish-tap.sh against a fake gh on synthetic or supplied GoReleaser builds:
# archives, checksums.txt, cask, metadata.json and notes. No network and no GitHub writes.
set -uo pipefail
export LC_ALL=C
script=$(cd "$(dirname "$0")" && pwd)/publish-tap.sh
scratch=$(mktemp -d) || exit 1
trap 'rm -rf "$scratch"' EXIT
fail=0

mkdir "$scratch/bin" || exit 1
cat >"$scratch/bin/gh" <<'EOF' || exit 1
#!/usr/bin/env bash
# Logs its arguments to $GH_LOG; the tap's state comes from the STUB_* variables.
set -euo pipefail
printf '%s\n' "$*" >>"$GH_LOG"
case "$1 $2" in
"release view")
  case ${STUB_RELEASE:-none} in
  none) echo "release not found" >&2; exit 1 ;;
  draft) echo true ;;
  published) echo false ;;
  auth) echo "HTTP 401: Bad credentials" >&2; exit 1 ;;
  esac ;;
"release download") cp "$STUB_PUBLISHED"/* "${*: -1}"/ ;;
"release create" | "release edit") ;;
"api repos/aiven/homebrew-tap/contents/Casks/aiven-ai-gateway.rb?ref=main")
  if [[ -z ${STUB_CASK:-} ]]; then
    echo "gh: Not Found (HTTP 404)" >&2
    exit 1
  fi
  printf '{"sha":"blob123","content":"%s"}\n' \
    "$(base64 <"$STUB_CASK" | tr -d '\n' | fold -w 60 | sed 's/$/\\n/' | tr -d '\n')" ;;
"api -X")
  if [[ ${STUB_PUT:-ok} == conflict ]]; then
    echo "gh: Casks/aiven-ai-gateway.rb does not match blob123 (HTTP 409)" >&2
    exit 1
  fi ;;
*) echo "fake gh: unexpected $*" >&2; exit 2 ;;
esac
EOF
chmod +x "$scratch/bin/gh" || exit 1
export PATH=$scratch/bin:$PATH
if [[ $(command -v gh) != "$scratch/bin/gh" ]]; then
  echo "fake gh is not the selected executable" >&2
  exit 1
fi

# archive DIST VERSION TARGET CONTENT [NAME...]: writes the target's archive, holding the binary
# (CONTENT) and its licence texts, or only the given names.
archive() {
  local dist=$1 version=$2 target=$3 content=$4 files
  shift 4
  (($# > 0)) || set -- LICENSE THIRD_PARTY_NOTICES aiven-ai-gateway
  files=$(mktemp -d "$scratch/files.XXXX")
  printf '%s\n' "$content" >"$files/aiven-ai-gateway"
  echo "synthetic licence" >"$files/LICENSE"
  echo "synthetic notices" >"$files/THIRD_PARTY_NOTICES"
  tar -czf "$dist/aiven-ai-gateway_${version}_$target.tar.gz" -C "$files" "$@"
}

# build NAME TAG: a build of TAG, with checksums and a cask that match its archives.
build() {
  local dist=$scratch/$1 tag=$2 version=${2#v} target sum name
  local url="https://github.com/aiven/homebrew-tap/releases/download/aiven-ai-gateway-v#{version}"
  mkdir -p "$dist/homebrew/Casks"
  printf '{"tag":"%s","version":"%s"}\n' "$tag" "$version" >"$dist/metadata.json"
  echo "synthetic notes" >"$dist/CHANGELOG.md"
  for target in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64; do
    archive "$dist" "$version" "$target" "synthetic $tag $target"
  done
  (cd "$dist" && shasum -a 256 aiven-ai-gateway_*.tar.gz >checksums.txt)
  {
    echo 'cask "aiven-ai-gateway" do'
    echo "  version \"$version\""
    while read -r sum name; do
      echo "  sha256 \"$sum\""
      echo "  url \"$url/$name\""
    done <"$dist/checksums.txt"
    echo end
  } >"$dist/homebrew/Casks/aiven-ai-gateway.rb"
  echo "$dist"
}

# variant NAME DIST: a copy of DIST to break.
variant() {
  cp -R "$2" "$scratch/$1"
  echo "$scratch/$1"
}

# published NAME DIST: the assets a tap release would hold for DIST.
published() {
  mkdir "$scratch/$1"
  cp "$2"/*.tar.gz "$2/checksums.txt" "$scratch/$1"/
  echo "$scratch/$1"
}

# check NAME WANT_RC DIST REF WANT_LOG DENY_LOG [VAR=VALUE...]: runs the script, then matches
# the gh calls it made against the extended regular expressions WANT_LOG and DENY_LOG.
check() {
  local name=$1 want=$2 dist=$3 ref=$4 want_log=$5 deny_log=$6 rc ok=yes
  shift 6
  export GH_LOG
  GH_LOG=$(mktemp "$scratch/log.XXXX")
  env "$@" GITHUB_REF_NAME="$ref" bash "$script" "$dist" >/dev/null 2>"$GH_LOG.err"
  rc=$?
  [[ $rc == "$want" ]] || ok=no
  [[ -z $want_log ]] || grep -Eq -e "$want_log" "$GH_LOG" || ok=no
  [[ -z $deny_log ]] || ! grep -Eq -e "$deny_log" "$GH_LOG" || ok=no
  printf '%-4s %-52s rc=%s  %s\n' "$ok" "$name" "$rc" "$(head -c 110 "$GH_LOG.err" | tr '\n' ' ')"
  [[ $ok == yes ]] || {
    fail=1
    sed 's/^/     gh: /' "$GH_LOG"
  }
}

# input_build DIST TAG: validates supplied release output without changing it.
input_build() {
  local dist links tag=$2
  dist=$(cd "$1" && pwd -P) || return 2
  links=$(find "$dist" -type l -print) || return 2
  if [[ -n $links ]]; then
    echo "$dist must not contain symlinks" >&2
    return 2
  fi
  if ! jq -e --arg tag "$tag" '.tag == $tag and .version == ($tag | ltrimstr("v"))' \
    "$dist/metadata.json" >/dev/null; then
    echo "$dist must be a $tag release build, not a snapshot" >&2
    return 2
  fi
  echo "$dist"
}

case $# in
0)
  rc=$(build rc v0.2.0-rc.1)
  final=$(build final v0.2.0) ;;
2)
  rc=$(input_build "$1" v0.2.0-rc.1) || exit 2
  final=$(input_build "$2" v0.2.0) || exit 2 ;;
*)
  echo "usage: $0 [rc-dist stable-dist]" >&2
  exit 2 ;;
esac
same=$(published same "$final")
changed=$(published changed "$final")
printf 'x' >>"$changed/aiven-ai-gateway_0.2.0_linux_amd64.tar.gz"
missing=$(published missing "$final")
rm "$missing/aiven-ai-gateway_0.2.0_darwin_arm64.tar.gz"
extra=$(published extra "$final")
echo x >"$extra/unexpected.txt"
oldcask=$scratch/old.rb
sed 's/version "0.2.0"/version "0.1.9"/' "$final/homebrew/Casks/aiven-ai-gateway.rb" >"$oldcask"
rebuilt=$(variant rebuilt "$final")
archive "$rebuilt" 0.2.0 linux_amd64 "rebuilt after checksums.txt"
nolegal=$(variant nolegal "$final")
archive "$nolegal" 0.2.0 darwin_arm64 "synthetic v0.2.0 darwin_arm64" LICENSE aiven-ai-gateway
nocask=$(variant nocask "$final")
rm "$nocask/homebrew/Casks/aiven-ai-gateway.rb"
plaincask=$(variant plaincask "$final")
sed 's#download/aiven-ai-gateway-v#download/v#' "$final/homebrew/Casks/aiven-ai-gateway.rb" \
  >"$plaincask/homebrew/Casks/aiven-ai-gateway.rb"

writes='release (create|edit)|-X PUT'
check "missing release, prerelease: release only" 0 "$rc" v0.2.0-rc.1 \
  'release create aiven-ai-gateway-v0\.2\.0-rc\.1 .*--target main --draft --prerelease=true' 'api' \
  STUB_RELEASE=none
check "missing release, stable: release and new cask" 0 "$final" v0.2.0 \
  '-X PUT repos/aiven/homebrew-tap/contents/Casks/aiven-ai-gateway\.rb .*branch=main' 'sha=' \
  STUB_RELEASE=none
check "auth error is not 'not found'" 1 "$final" v0.2.0 '' "$writes" STUB_RELEASE=auth
check "existing identical, cask current" 0 "$final" v0.2.0 'release download' "$writes" \
  STUB_RELEASE=published STUB_PUBLISHED="$same" STUB_CASK="$final/homebrew/Casks/aiven-ai-gateway.rb"
check "existing identical, older cask: sha guard" 0 "$final" v0.2.0 '-X PUT .*sha=blob123' \
  'release (create|edit)' STUB_RELEASE=published STUB_PUBLISHED="$same" STUB_CASK="$oldcask"
check "existing with a changed archive refused" 1 "$final" v0.2.0 '' "$writes" \
  STUB_RELEASE=published STUB_PUBLISHED="$changed"
check "existing with a missing archive refused" 1 "$final" v0.2.0 '' "$writes" \
  STUB_RELEASE=published STUB_PUBLISHED="$missing"
check "existing with an extra asset refused" 1 "$final" v0.2.0 '' "$writes" \
  STUB_RELEASE=published STUB_PUBLISHED="$extra"
check "unpublished draft refused" 1 "$final" v0.2.0 '' "$writes|release download" STUB_RELEASE=draft
check "stable cask sha conflict fails" 1 "$final" v0.2.0 '-X PUT .*sha=blob123' '' \
  STUB_RELEASE=published STUB_PUBLISHED="$same" STUB_CASK="$oldcask" STUB_PUT=conflict
check "pushed tag differs from build" 1 "$final" v0.2.1 '' '.' STUB_RELEASE=none
check "archive bytes differ from checksums: no gh call" 1 "$rebuilt" v0.2.0 '' '.' STUB_RELEASE=none
check "archive without THIRD_PARTY_NOTICES: no gh call" 1 "$nolegal" v0.2.0 '' '.' STUB_RELEASE=none
check "no generated cask: no gh call" 1 "$nocask" v0.2.0 '' '.' STUB_RELEASE=none
check "cask on the plain tag: no gh call" 1 "$plaincask" v0.2.0 '' '.' STUB_RELEASE=none
exit $fail
