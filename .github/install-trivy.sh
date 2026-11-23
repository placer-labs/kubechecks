#!/usr/bin/env bash
# Install the Trivy release pinned in .tool-versions, verified against a
# checksum committed here. Bumping Trivy means updating both .tool-versions and
# TRIVY_SHA256 below, so a new binary can't arrive without a reviewed commit.
set -euo pipefail

TRIVY_VERSION="$(awk '$1 == "trivy" { print $2 }' "$(dirname "$0")/../.tool-versions")"
TRIVY_SHA256="bc0fc726e017b8927a62b2d3531d7a84ee01769daf5c99ed866dced54089fdbf" # trivy_0.76.2_Linux-64bit.tar.gz

if [[ "$TRIVY_VERSION" != "0.76.2" ]]; then
  echo "install-trivy.sh pins the checksum for 0.76.2 but .tool-versions has ${TRIVY_VERSION}; update both" >&2
  exit 1
fi

tarball="trivy_${TRIVY_VERSION}_Linux-64bit.tar.gz"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

curl -fsSL -o "$tmp/$tarball" \
  "https://github.com/aquasecurity/trivy/releases/download/v${TRIVY_VERSION}/${tarball}"
echo "${TRIVY_SHA256}  $tmp/$tarball" | sha256sum --check --strict
sudo tar -xzf "$tmp/$tarball" -C /usr/local/bin trivy
trivy --version
