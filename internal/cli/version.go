package cli

import "runtime/debug"

// version is the release version, set at link time by release builds (see
// .goreleaser.yaml):
//
//	-ldflags "-X github.com/alphaeron/canopy/internal/cli.version=1.2.3"
var version string

// resolveVersion reports the version `canopy --version` prints: the one set
// at link time, else the module version `go install ...@v1.2.3` records in
// the build info, else "dev" for a build from a local checkout.
func resolveVersion(linked string, info *debug.BuildInfo) string {
	if linked != "" {
		return linked
	}
	if info != nil && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func init() {
	info, _ := debug.ReadBuildInfo()
	rootCmd.Version = resolveVersion(version, info)
}
