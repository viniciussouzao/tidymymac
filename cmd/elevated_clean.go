package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/viniciussouzao/tidymymac/internal/elevate"
)

// elevatedCleanPlanFile receives --plan-file: the path of the approved plan
// written by the unprivileged parent process.
var elevatedCleanPlanFile string

// elevatedCleanCmd is the root half of the privilege boundary described in
// internal/elevate. It is an internal contract with elevate.Invoke, NOT a
// public interface: the flag names, the stdout format and the exit-code
// semantics are all free to change in lockstep with that package, and nothing
// outside it may depend on them.
//
// Hidden because it is not something a user should ever type. Run by hand it
// does nothing but print an error (RunHelper requires euid 0 and a SUDO_UID
// set by sudo), and advertising it in --help would invite exactly the usage
// the whole design exists to avoid: running tidymymac as root.
//
// stdout is reserved for the Result JSON and carries nothing else -- the
// parent parses it. Progress goes to stderr, which is inherited from the
// user's terminal.
//
// Guard failures exit with elevate.HelperGuardRejectedExitCode instead of
// being returned through cobra. That exit code is the dedicated "a guard
// rejected the plan and nothing was deleted" channel of the IPC contract:
// RunHelper's error return is, by its own contract, only ever a pre-deletion
// guard rejection, whereas a generic non-zero exit is indistinguishable from
// the helper crashing or being killed halfway through deleting. The parent
// maps every non-zero exit other than this code -- including the exit 1 cobra
// produces if the Encode below fails after the clean -- to "outcome unknown",
// never to "nothing was deleted".
//
// The root command's PersistentPreRunE still runs config.Load() first. That is
// intentional: as root it succeeds only when SUDO_USER resolves, so an
// elevation that would silently lose protected_paths is refused before this
// command's own RunE is ever reached.
var elevatedCleanCmd = &cobra.Command{
	Use:          elevate.HelperCommandName,
	Short:        "Internal: execute an approved clean plan with elevated privileges",
	Hidden:       true,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := elevate.RunHelper(cmd.Context(), elevatedCleanPlanFile)
		if err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), err)
			os.Exit(elevate.HelperGuardRejectedExitCode)
		}

		// Per-category failures are data, not command failure: they are inside
		// the Result the parent is about to read, and exiting non-zero here
		// would make the parent report "nothing was deleted" for a run that
		// did in fact delete part of the plan.
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	},
}

func init() {
	elevatedCleanCmd.Flags().StringVar(&elevatedCleanPlanFile, "plan-file", "", "path to the approved clean plan written by the invoking process")
	_ = elevatedCleanCmd.MarkFlagRequired("plan-file")
	rootCmd.AddCommand(elevatedCleanCmd)
}
