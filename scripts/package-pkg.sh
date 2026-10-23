#!/usr/bin/env bash
# package-pkg.sh builds the macOS installer package from GoReleaser's universal
# archive in DIST, writing canopy_<version>_darwin_all.pkg beside it. The
# package installs canopy to /usr/local/bin. It runs on macOS (pkgbuild).
#
# Usage: scripts/package-pkg.sh VERSION [DIST]
set -euo pipefail

version=${1:?usage: package-pkg.sh VERSION [DIST]}
dist=${2:-dist}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/root/usr/local/bin"
tar -xzf "$dist/canopy_${version}_darwin_all.tar.gz" -C "$work/root/usr/local/bin" canopy

pkgbuild --root "$work/root" \
	--identifier com.github.alphaeron.canopy \
	--version "$version" \
	--install-location / \
	"$dist/canopy_${version}_darwin_all.pkg"
