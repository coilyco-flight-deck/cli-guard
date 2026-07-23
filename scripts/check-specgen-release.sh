#!/bin/sh
# Verify checksums, package metadata, and the native specgen release binary.
set -eu

version=${1:?usage: check-specgen-release.sh VERSION [DIST_DIR]}
dist=${2:-dist}
bare=${version#v}

case "$version" in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *)
    echo "specgen release version must be a v-prefixed semantic version, got: $version" >&2
    exit 2
    ;;
esac

(
  cd "$dist"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -c SHA256SUMS
  else
    shasum -a 256 -c SHA256SUMS
  fi
)

checksum_count=$(wc -l < "$dist/SHA256SUMS" | tr -d ' ')
if [ "$checksum_count" -ne 6 ]; then
  echo "specgen release must contain six checksummed binaries" >&2
  exit 1
fi

python3 -m json.tool "$dist/specgen.json" >/dev/null
if command -v ruby >/dev/null 2>&1; then
  ruby -c "$dist/specgen.rb" >/dev/null
fi

grep -F "version \"${bare}\"" "$dist/specgen.rb" >/dev/null
grep -F "\"version\": \"${bare}\"" "$dist/specgen.json" >/dev/null

for artifact in \
  specgen-darwin-amd64 \
  specgen-darwin-arm64 \
  specgen-linux-amd64 \
  specgen-linux-arm64
do
  grep -F "$artifact" "$dist/specgen.rb" >/dev/null
done

for artifact in specgen-windows-amd64.exe specgen-windows-arm64.exe
do
  grep -F "$artifact" "$dist/specgen.json" >/dev/null
done

case "$(uname -s)/$(uname -m)" in
  Darwin/x86_64) native="$dist/specgen-darwin-amd64" ;;
  Darwin/arm64) native="$dist/specgen-darwin-arm64" ;;
  Linux/x86_64) native="$dist/specgen-linux-amd64" ;;
  Linux/aarch64 | Linux/arm64) native="$dist/specgen-linux-arm64" ;;
  *) native="" ;;
esac

if [ -n "$native" ]; then
  actual=$("$native" --version)
  expected="specgen version ${version} (cli-guard ref ${version})"
  if [ "$actual" != "$expected" ]; then
    echo "native binary reports $actual, expected $expected" >&2
    exit 1
  fi
fi

echo "verified specgen release $version"
