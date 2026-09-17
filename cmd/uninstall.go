package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
)

// uninstallCmd represents the `tidymymac uninstall` command: Smart Uninstall,
// the confidence-scored removal of one specific application plus the
// leftovers it scattered across ~/Library.
//
// Unlike scan/clean, this command targets exactly one application chosen by
// the user rather than a whole-system category set, so it builds its own
// throwaway cleaner.Registry per run instead of using the shared
// cleaner.DefaultRegistry() -- see docs/ARCHITECTURE.md's "Registry" section
// for why CategoryAppUninstall is deliberately excluded from the default one.
var uninstallCmd = &cobra.Command{
	Use:   "uninstall [app]",
	Short: "Remove an installed application and the leftovers it scattered across your Mac",
	Long: `Remove an installed application's .app bundle plus the support files,
caches and preferences it left under ~/Library, scored by how confident
TidyMyMac is that each item actually belongs to it.

By default this command runs in dry-run mode and only previews what would be
removed. Pass --execute to actually delete files.

Example usage:
# List every third-party application TidyMyMac can discover
$ tidymymac uninstall --list

# Preview removing an app by name (dry-run, machine-readable)
$ tidymymac uninstall Foo --output json

# Actually remove it, but only the entries TidyMyMac is fully confident about
$ tidymymac uninstall Foo --execute --output json

# Widen the removal to entries that need a human look too
$ tidymymac uninstall Foo --execute --output json --min-confidence review

# Match by bundle identifier instead of display name
$ tidymymac uninstall com.acme.foo --output json
`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Checked first, before any other flag is even read -- the exact same
		// position clean.go and root.go use, so 'sudo tidymymac uninstall
		// --execute ...' is refused before this command does anything else,
		// including a read-only --list.
		if executeFlag {
			if err := guardRootDeletion(); err != nil {
				return err
			}
		}

		list, _ := cmd.Flags().GetBool("list")
		output, _ := cmd.Flags().GetString("output")
		detailed, _ := cmd.Flags().GetBool("detailed")
		minConfidence, _ := cmd.Flags().GetString("min-confidence")

		if output != "" && output != "json" {
			return fmt.Errorf("invalid --output value %q: must be json", output)
		}
		if !validMinConfidence(minConfidence) {
			return fmt.Errorf("invalid --min-confidence value %q: must be one of %s", minConfidence, strings.Join(uninstallMinConfidenceLevels, ", "))
		}

		ctx := cmd.Context()

		if list {
			if len(args) > 0 {
				return fmt.Errorf("--list does not take an application argument")
			}
			apps, err := cleaner.DiscoverInstalledApps(ctx, nil, nil)
			if err != nil {
				return err
			}
			if output == "json" {
				return writeUninstallAppListJSON(os.Stdout, apps)
			}
			return writeUninstallAppListHuman(os.Stdout, apps)
		}

		if len(args) != 1 {
			return fmt.Errorf("uninstall requires exactly one application name or bundle id argument (or --list to see discovered applications)")
		}

		if output == "" {
			// TODO(Phase 5/7): open the interactive TUI review screen for Smart
			// Uninstall (per-item confidence badges, confirm/execute flow) once
			// it exists. Until then, --output json is the only supported
			// surface for this command. Checked before any discovery/resolution
			// work happens, like every other flag-shape validation above, so a
			// caller who forgot --output fails immediately rather than after an
			// otherwise-successful scan.
			return fmt.Errorf("interactive 'tidymymac uninstall' is not implemented yet; pass --output json (dry-run by default, --execute to delete)")
		}

		apps, err := cleaner.DiscoverInstalledApps(ctx, nil, nil)
		if err != nil {
			return err
		}
		target, err := resolveUninstallTarget(apps, args[0])
		if err != nil {
			return err
		}

		registry := cleaner.NewRegistry()
		au := cleaner.NewAppUninstaller(target)
		registry.Register(au)

		return runUninstallNonInteractive(ctx, registry, au, minConfidence, detailed, output)
	},
	SilenceUsage: true,
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
	uninstallCmd.Flags().StringP("output", "o", "", "output format for results: json (omit to preview interactively -- not yet implemented)")
	uninstallCmd.Flags().Bool("detailed", false, "include individual file paths in the result (only applies with --output json)")
	uninstallCmd.Flags().Bool("list", false, "list installed applications TidyMyMac can uninstall, instead of scanning/cleaning one")
	uninstallCmd.Flags().String("min-confidence", "safe", "lowest confidence band to remove: safe, review, or caution (default: safe)")
}
