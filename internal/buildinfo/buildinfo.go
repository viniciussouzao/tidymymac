package buildinfo

import "runtime"

var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

func Platform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

func GoVersion() string {
	return runtime.Version()
}
