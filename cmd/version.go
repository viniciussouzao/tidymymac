package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/viniciussouzao/tidymymac/internal/buildinfo"
)

// versionCmd represents the version command
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version information",
	Long: `Print the version information of the application, including the version number,
commit hash, build date, platform, and Go version.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("version: %s\n", buildinfo.Version)
		fmt.Printf("commit: %s\n", buildinfo.Commit)
		fmt.Printf("build date: %s\n", buildinfo.BuildDate)
		fmt.Printf("platform: %s\n", buildinfo.Platform())
		fmt.Printf("go version: %s\n", buildinfo.GoVersion())
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
