#!/bin/sh
# Render Homebrew and Scoop metadata from version-stamped specgen binaries.
set -eu

version=${1:?usage: render-specgen-packaging.sh VERSION [DIST_DIR]}
dist=${2:-dist}

case "$version" in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *)
    echo "specgen release version must be a v-prefixed semantic version, got: $version" >&2
    exit 2
    ;;
esac

bare=${version#v}
base="https://forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/releases/download/${version}"

sha() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

darwin_amd64=$(sha "$dist/specgen-darwin-amd64")
darwin_arm64=$(sha "$dist/specgen-darwin-arm64")
linux_amd64=$(sha "$dist/specgen-linux-amd64")
linux_arm64=$(sha "$dist/specgen-linux-arm64")
windows_amd64=$(sha "$dist/specgen-windows-amd64.exe")
windows_arm64=$(sha "$dist/specgen-windows-arm64.exe")

cat > "$dist/specgen.rb" <<EOF
class Specgen < Formula
  desc "Generate guarded CLIs from KDL policy and committed API locks"
  homepage "https://forgejo.coilysiren.me/coilyco-flight-deck/cli-guard"
  version "${bare}"
  license "MIT"

  on_macos do
    on_intel do
      url "${base}/specgen-darwin-amd64"
      sha256 "${darwin_amd64}"
    end
    on_arm do
      url "${base}/specgen-darwin-arm64"
      sha256 "${darwin_arm64}"
    end
  end
  on_linux do
    on_intel do
      url "${base}/specgen-linux-amd64"
      sha256 "${linux_amd64}"
    end
    on_arm do
      url "${base}/specgen-linux-arm64"
      sha256 "${linux_arm64}"
    end
  end

  def install
    bin.install Dir["specgen-*"].first => "specgen"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/specgen --version")
  end
end
EOF

cat > "$dist/specgen.json" <<EOF
{
    "version": "${bare}",
    "description": "Generate guarded CLIs from KDL policy and committed API locks",
    "homepage": "https://forgejo.coilysiren.me/coilyco-flight-deck/cli-guard",
    "license": "MIT",
    "architecture": {
        "64bit": {
            "url": "${base}/specgen-windows-amd64.exe",
            "hash": "${windows_amd64}",
            "bin": [["specgen-windows-amd64.exe", "specgen"]]
        },
        "arm64": {
            "url": "${base}/specgen-windows-arm64.exe",
            "hash": "${windows_arm64}",
            "bin": [["specgen-windows-arm64.exe", "specgen"]]
        }
    }
}
EOF

echo "$dist/specgen.rb"
echo "$dist/specgen.json"
