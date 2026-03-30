package buildinfo

import "runtime"

var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

// Platform returns the current platform in the format "os/arch" (e.g., "darwin/amd64").
func Platform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

// GoVersion returns the version of Go used to build the application.
func GoVersion() string {
	return runtime.Version()
}
