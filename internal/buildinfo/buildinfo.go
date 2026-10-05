// Package buildinfo holds the application version and local build metadata.
package buildinfo

// The variables below are populated at build time via -ldflags -X.
// Their defaults keep plain `go run`/`go build` working without any
// special build flags.
var (
	// Version is the semantic version of the application (Major.Minor.Patch).
	Version     = "1.0.3"
	BuildNumber = "dev"
	GitCommit   = "unknown"
	BuildTime   = "unknown"
)

// Full returns the full version string, e.g. "1.0.0+build.137.a3f61c2d",
// with a ".dirty" suffix if the build tree had uncommitted changes, or
// "1.0.0+dev" when built without the build script's linker flags.
func Full() string {
	full := Version + "+build." + BuildNumber + "." + GitCommit

	return full
}
