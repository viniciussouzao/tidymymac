package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/config"
	"github.com/viniciussouzao/tidymymac/internal/tui/styles"
)

var profileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Manage cleanup profiles",
	Long: `Manage named cleanup profiles in ~/.tidymymac/config.yaml.

A profile bundles the categories you want to clean together with project
directories to sweep for regenerable junk (node_modules, dist, target, ...)
and oversized files. Run it with "tidymymac scan --profile <name>" or
"tidymymac clean --profile <name>".

Example:
$ tidymymac profile create dev
$ tidymymac profile add-category dev development-artifacts
$ tidymymac profile add-path dev ~/meu-projeto-js
$ tidymymac scan --profile dev
`,
}

var profileCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create an empty profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := config.CreateProfile(args[0]); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), styles.Success.Render("  profile created: ")+args[0])
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), styles.Help.Render(fmt.Sprintf("  add something to it with 'tidymymac profile add-category %s <category>' or 'tidymymac profile add-path %s <path>'", args[0], args[0])))
		return nil
	},
	SilenceUsage: true,
}

var profileDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		removed, err := config.DeleteProfile(args[0])
		if err != nil {
			return err
		}
		if !removed {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), styles.Dim.Render("  no such profile, nothing to delete: ")+args[0])
			return nil
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), styles.Success.Render("  profile deleted: ")+args[0])
		return nil
	},
	SilenceUsage: true,
}

var profileAddCategoryCmd = &cobra.Command{
	Use:   "add-category <profile> <category>",
	Short: "Add a category to a profile",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, category := args[0], args[1]
		// Checked here rather than in internal/config so a typo is caught at
		// the moment it is typed, instead of surfacing much later as
		// "unknown category" on the first scan --profile.
		if _, ok := cleaner.DefaultRegistry().Get(cleaner.Category(category)); !ok {
			return fmt.Errorf("unknown category %q; run \"tidymymac list categories\" to see the available ones", category)
		}
		if err := config.AddProfileCategory(name, category); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), styles.Success.Render("  added to "+name+": ")+category)
		return nil
	},
	SilenceUsage: true,
}

var profileRemoveCategoryCmd = &cobra.Command{
	Use:   "remove-category <profile> <category>",
	Short: "Remove a category from a profile",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, category := args[0], args[1]
		removed, err := config.RemoveProfileCategory(name, category)
		if err != nil {
			return err
		}
		if !removed {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), styles.Dim.Render("  not in "+name+", nothing to remove: ")+category)
			return nil
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), styles.Success.Render("  removed from "+name+": ")+category)
		return nil
	},
	SilenceUsage: true,
}

var profileAddPathCmd = &cobra.Command{
	Use:   "add-path <profile> <path>",
	Short: "Add a project path to a profile",
	Long: `Add a project directory to a profile. Scanning it reports regenerable junk
directories (node_modules, dist, target, ...) and files above 500MB.

Broad roots (your home directory, /, /Users, /Applications, ...) are rejected:
a profile path is meant to be a single project, not a whole-system sweep.
`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, p := args[0], args[1]
		if err := config.AddProfilePath(name, p); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), styles.Success.Render("  added to "+name+": ")+p)
		return nil
	},
	SilenceUsage: true,
}

var profileRemovePathCmd = &cobra.Command{
	Use:   "remove-path <profile> <path>",
	Short: "Remove a project path from a profile",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, p := args[0], args[1]
		removed, err := config.RemoveProfilePath(name, p)
		if err != nil {
			return err
		}
		if !removed {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), styles.Dim.Render("  not in "+name+", nothing to remove: ")+p)
			return nil
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), styles.Success.Render("  removed from "+name+": ")+p)
		return nil
	},
	SilenceUsage: true,
}

// resolveSelection turns the positional category args and an optional
// --profile name into the (categories, registry) pair the command layer
// already consumes. Without --profile it is the identity: the args as typed,
// against the default registry.
//
// includeLargeFiles only matters for clean (scan never deletes anything), so
// scan passes false.
func resolveSelection(args []string, profileName string, includeLargeFiles bool) ([]string, *cleaner.Registry, error) {
	if profileName == "" {
		return args, cleaner.DefaultRegistry(), nil
	}

	if len(args) > 0 {
		return nil, nil, fmt.Errorf(
			"--profile and explicit categories are mutually exclusive: either add %q to the profile with \"tidymymac profile add-category %s %s\", or run it on its own without --profile",
			args[0], profileName, args[0],
		)
	}

	return loadedConfig.ResolveProfile(cleaner.DefaultRegistry(), profileName, includeLargeFiles)
}

func init() {
	rootCmd.AddCommand(profileCmd)
	profileCmd.AddCommand(profileCreateCmd)
	profileCmd.AddCommand(profileDeleteCmd)
	profileCmd.AddCommand(profileAddCategoryCmd)
	profileCmd.AddCommand(profileRemoveCategoryCmd)
	profileCmd.AddCommand(profileAddPathCmd)
	profileCmd.AddCommand(profileRemovePathCmd)
}
