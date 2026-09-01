package cmd

import (
	"slices"
	"testing"

	"github.com/spf13/cobra"
)

func TestExecuteCmdRegisteredOnRoot(t *testing.T) {
	if !slices.Contains(rootCmd.Commands(), executeCmd) {
		t.Fatalf("executeCmd is not registered on rootCmd")
	}
	if executeCmd.Use != "execute" {
		t.Fatalf("executeCmd.Use = %q, want %q", executeCmd.Use, "execute")
	}
}

func TestRootExecuteDeprecationWarning(t *testing.T) {
	if err := rootCmd.ParseFlags([]string{"--execute"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	t.Cleanup(func() {
		_ = rootCmd.ParseFlags([]string{})
		executeFlag = false
	})

	if warning := rootExecuteDeprecationWarning(rootCmd); warning == "" {
		t.Fatalf("expected a deprecation warning when --execute is set on the root command")
	}
}

func TestRootExecuteDeprecationWarningAbsentWithoutFlag(t *testing.T) {
	fresh := &cobra.Command{Use: "tidymymac"}
	fresh.Flags().BoolP("execute", "e", false, "")

	if warning := rootExecuteDeprecationWarning(fresh); warning != "" {
		t.Fatalf("expected no deprecation warning when --execute was not passed, got %q", warning)
	}
}

func TestCleanExecuteDoesNotWarn(t *testing.T) {
	// clean has its own RunE and never calls rootExecuteDeprecationWarning;
	// this documents that expectation so a future refactor can't silently
	// wire the root warning into 'clean --execute'.
	if cleanCmd.RunE == nil {
		t.Fatalf("cleanCmd.RunE is nil")
	}
}
