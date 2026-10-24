#!/usr/bin/env bash
# package-pkg.sh builds the macOS installer package from GoReleaser's universal
# archive in DIST, writing canopy_<version>_darwin_all.pkg beside it. The
# package installs canopy to /usr/local/bin, its man pages to
# /usr/local/share/man, and its zsh, bash, and fish completions where each
# shell (macOS's own zsh, bash-completion 2, fish) looks under
# /usr/local. It runs on macOS (pkgbuild).
#
# Usage: scripts/package-pkg.sh VERSION [DIST]
set -euo pipefail

version=${1:?usage: package-pkg.sh VERSION [DIST]}
dist=${2:-dist}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/archive"
tar -xzf "$dist/canopy_${version}_darwin_all.tar.gz" -C "$work/archive"

prefix=$work/root/usr/local
install -d "$prefix/bin" "$prefix/share/zsh/site-functions" \
	"$prefix/share/bash-completion/completions" "$prefix/share/fish/vendor_completions.d"
install -m 0755 "$work/archive/canopy" "$prefix/bin/canopy"
cp -R "$work/archive/man" "$prefix/share/man"
install -m 0644 "$work/archive/completions/_canopy" "$prefix/share/zsh/site-functions/_canopy"
install -m 0644 "$work/archive/completions/canopy.bash" "$prefix/share/bash-completion/completions/canopy"
install -m 0644 "$work/archive/completions/canopy.fish" "$prefix/share/fish/vendor_completions.d/canopy.fish"

pkgbuild --root "$work/root" \
	--identifier com.github.alphaeron.canopy \
	--version "$version" \
	--install-location / \
	"$dist/canopy_${version}_darwin_all.pkg"
