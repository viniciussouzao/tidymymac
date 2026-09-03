package cmd

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/viniciussouzao/tidymymac/internal/elevate"
)

// The hidden helper is reached by sudo re-executing this binary with a literal
// command name, so its registration, its name and its flag are a wire
// contract with elevate.Invoke rather than cosmetic CLI details.
func TestElevatedCleanCommandContract(t *testing.T) {
	var found *cobra.Command
	for _, c := range rootCmd.Commands() {
		if c.Name() == elevate.HelperCommandName {
			found = c
			break
		}
	}
	if found == nil {
		t.Fatalf("%q is not registered on the root command; elevate.Invoke would exec a command that does not exist", elevate.HelperCommandName)
	}

	if !found.Hidden {
		t.Errorf("%q must stay hidden: it is an internal contract, and advertising it invites running tidymymac as root", elevate.HelperCommandName)
	}
	if !found.SilenceUsage {
		t.Errorf("%q must silence usage: a usage dump would be noise on the parent's stderr", elevate.HelperCommandName)
	}

	flag := found.Flags().Lookup("plan-file")
	if flag == nil {
		t.Fatalf("%q has no --plan-file flag", elevate.HelperCommandName)
	}
	if flag.Annotations[cobra.BashCompOneRequiredFlag] == nil {
		t.Errorf("--plan-file must be required: without a plan there is nothing the helper may do")
	}
}
