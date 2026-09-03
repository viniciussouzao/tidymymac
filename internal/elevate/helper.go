package elevate

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/commands"
	"github.com/viniciussouzao/tidymymac/internal/config"
)

// helperEnv holds the few pieces of ambient state RunHelper depends on, so the
// pipeline can be exercised in tests without actually being root.
//
// These seams are deliberately narrow and deliberately few. This is a security
// boundary: every injectable point is a point where a test double can differ
// from production, so the injection surface is kept to the ambient facts the
// helper cannot fake for itself (its own euid, the environment, the config, the
// registry) rather than to any of the decision logic. The guards, the plan
// validation and the intersection are not injectable and always run.
type helperEnv struct {
	geteuid     func() int
	getenv      func(string) string
	loadConfig  func() (*config.Config, error)
	newRegistry func() *cleaner.Registry

	// progress receives human-readable progress lines. It must never be
	// os.Stdout: stdout carries the Result JSON and nothing else.
	progress io.Writer
}

func defaultHelperEnv() helperEnv {
	return helperEnv{
		geteuid:    os.Geteuid,
		getenv:     os.Getenv,
		loadConfig: config.Load,
		// Safe to build as root: the sudo-requiring cleaners resolve the
		// user's home through internal/homedir, which honors SUDO_USER rather
		// than returning /var/root.
		newRegistry: cleaner.DefaultRegistry,
		progress:    os.Stderr,
	}
}

// RunHelper is the root side of the elevation. It is invoked only by Invoke,
// through the hidden "internal-elevated-clean" subcommand, and never by a user
// directly.
//
// Contract with the parent: when the helper actually ran, it returns a Result
// and a nil error even if individual categories failed -- per-category failure
// is data, and travels inside the Result. A non-nil error means a guard
// rejected the run before any deletion could happen, and the parent may rely
// on nothing having been deleted in that case.
func RunHelper(ctx context.Context, planPath string) (Result, error) {
	return runHelper(ctx, planPath, defaultHelperEnv())
}

func runHelper(ctx context.Context, planPath string, env helperEnv) (Result, error) {
	// --- Guards. All of these are fatal, and all of them run before anything
	// is opened, scanned or deleted: this path fails closed. ---

	if env.geteuid() != 0 {
		// Reached when someone runs the hidden subcommand by hand. There is no
		// point degrading gracefully: without root the helper cannot do the
		// one job it exists for, and pretending otherwise would produce a
		// half-done clean the caller would mistake for a complete one.
		return Result{}, fmt.Errorf("internal command: %s must be started by tidymymac itself via sudo, not run directly", HelperCommandName)
	}

	// SUDO_UID identifies the unprivileged user whose plan this is, and is the
	// ownership we require on the plan file. If sudo did not set it we are
	// running under something we do not understand (a setuid wrapper, a
	// hand-rolled su), and we cannot tell whose plan we would be executing.
	rawUID := env.getenv("SUDO_UID")
	if rawUID == "" {
		return Result{}, fmt.Errorf("SUDO_UID is not set; %s must be started via sudo so the invoking user can be identified", HelperCommandName)
	}
	uid, err := strconv.Atoi(rawUID)
	if err != nil || uid < 0 {
		return Result{}, fmt.Errorf("SUDO_UID %q is not a valid user id", rawUID)
	}

	// config.Load hard-fails when elevated without a resolvable SUDO_USER,
	// precisely so protected_paths cannot silently stop applying under sudo.
	// We rely on that rather than re-checking it here.
	cfg, err := env.loadConfig()
	if err != nil {
		return Result{}, fmt.Errorf("loading config: %w", err)
	}

	plan, err := readPlanFile(planPath, uid)
	if err != nil {
		return Result{}, err
	}

	registry := env.newRegistry()
	if err := validatePlan(plan, registry, cfg); err != nil {
		return Result{}, err
	}

	// --- Fence 2: a fresh scan per category, intersected with the plan. ---

	result := Result{Version: ResultSchemaVersion}
	prepared := commands.PreparedScanResult{
		Result: commands.ScanResult{
			Categories: make([]commands.ScanCategoryResult, 0, len(plan.Categories)),
		},
	}
	selected := make([]string, 0, len(plan.Categories))

	for _, planCategory := range plan.Categories {
		// Presence and RequiresSudo were already verified by validatePlan.
		c, _ := registry.Get(planCategory.Category)
		name := c.Category().DisplayName()

		item := commands.ScanCategoryResult{
			Category:    c.Category(),
			Name:        name,
			RequireSudo: true,
		}
		intersection := CategoryIntersection{
			Category: c.Category(),
			Name:     name,
		}

		progressf(env.progress, "scanning %s as root\n", name)

		fresh, scanErr := c.Scan(ctx, nil)
		if scanErr != nil {
			// One category's fresh scan failing must not sink the others --
			// same per-category error philosophy runClean uses. Recording the
			// error on the prepared category makes runClean report it as that
			// category's failure too, so there is one consistent story.
			intersection.ErrMsg = scanErr.Error()
			intersection.Approved = len(dedupeByPath(planCategory.Entries))
			intersection.Missing = intersection.Approved
			item.Err = scanErr
			item.ErrMsg = scanErr.Error()
			prepared.Result.HasErrors = true

			result.Intersections = append(result.Intersections, intersection)
			prepared.Result.Categories = append(prepared.Result.Categories, item)
			// Still selected: runClean is what turns the recorded ErrMsg into a
			// reported per-category failure, and it only visits selected
			// categories. It never calls Clean for one carrying an error.
			selected = append(selected, string(c.Category()))
			continue
		}

		var freshEntries []cleaner.FileEntry
		if fresh != nil {
			freshEntries = fresh.Entries
		}
		matched, approved, missing := intersectEntries(planCategory.Entries, freshEntries, c.Category())

		intersection.Approved = approved
		intersection.Matched = len(matched)
		intersection.Missing = missing

		item.Files = matched
		item.TotalFiles = len(matched)
		for _, e := range matched {
			item.TotalSize += e.Size
		}

		prepared.RevalidatedFiles += len(matched)
		prepared.MissingFiles += missing

		result.Intersections = append(result.Intersections, intersection)

		if len(matched) == 0 {
			// Nothing survived the intersection, so this category must not
			// reach Clean AT ALL as root. runClean calls Clean(ctx, entries,
			// ...) unconditionally for every selected category, and a cleaner
			// that both RequiresSudo and DeletesWholeDomain would read an empty
			// list as "clear the whole domain" -- wiping a domain root nobody
			// approved a single entry from. No such cleaner exists today
			// (there is a registry conformance test asserting that), and this
			// keeps it from mattering if one ever does.
			//
			// It is still reported: its CategoryIntersection is recorded above
			// with Matched 0, which is exactly the "approved but nothing left"
			// story the parent renders.
			prepared.EmptyCategories++
			progressf(env.progress, "%s: none of the %d approved item(s) are still present; skipping\n", name, approved)
			continue
		}

		prepared.Result.TotalFiles += item.TotalFiles
		prepared.Result.TotalSize += item.TotalSize
		prepared.Result.Categories = append(prepared.Result.Categories, item)
		selected = append(selected, string(c.Category()))

		progressf(env.progress, "%s: %d of %d approved item(s) still present\n", name, len(matched), approved)
	}

	// An empty selection must never reach runClean: resolveCleaners reads
	// "no categories selected" as "every category", which as root would be a
	// full-registry clean nobody asked for. Every category was already
	// reported through result.Intersections, so there is nothing left to do.
	if len(selected) == 0 {
		progressf(env.progress, "nothing left to clean; every approved item is already gone\n")
		result.Clean = commands.CleanResult{CleanedAt: time.Now().UTC()}
		return result, nil
	}

	// Hand the intersection to the ordinary clean pipeline. This is the whole
	// reason the intersection is shaped as a PreparedScanResult: protected
	// path tagging/stripping and the DeletesWholeDomain skip live there, and
	// nowhere else.
	cleanResult, err := commands.RunCleanWithPreparedScanResult(ctx, registry, prepared, selected, commands.CleanerOptions{
		DryRun: plan.DryRun,
		// Detailed so the parent can report exactly which paths were acted on
		// without having to trust its own stale plan for that.
		Detailed: true,
		Config:   cfg,
	}, func(event commands.CleanEvent) {
		if event.Type == commands.CleanEventDone {
			if event.Err != nil {
				progressf(env.progress, "%s: failed: %v\n", event.Name, event.Err)
				return
			}
			progressf(env.progress, "%s: done\n", event.Name)
		}
	})
	if err != nil {
		return Result{}, fmt.Errorf("running elevated clean: %w", err)
	}

	result.Clean = cleanResult
	return result, nil
}

// validatePlan enforces the plan-level guards. Every one of them rejects the
// ENTIRE plan rather than dropping the offending category: a plan containing
// something we did not expect is a plan we no longer understand, and partially
// executing it as root would be executing an intent nobody expressed.
// The elevated side must be strictly NARROWER than the interactive side, never
// wider: anything the unprivileged path would refuse to do, the root path must
// also refuse.
func validatePlan(plan Plan, registry *cleaner.Registry, cfg *config.Config) error {
	if plan.Version != PlanSchemaVersion {
		return fmt.Errorf("plan schema version %d is not supported (this build speaks version %d)", plan.Version, PlanSchemaVersion)
	}
	if len(plan.Categories) == 0 {
		return fmt.Errorf("plan contains no categories; nothing to elevate for")
	}

	total := 0
	seen := make(map[cleaner.Category]struct{}, len(plan.Categories))
	for _, planCategory := range plan.Categories {
		if _, dup := seen[planCategory.Category]; dup {
			return fmt.Errorf("plan lists category %q more than once", planCategory.Category)
		}
		seen[planCategory.Category] = struct{}{}

		c, ok := registry.Get(planCategory.Category)
		if !ok {
			return fmt.Errorf("plan contains unknown category %q; refusing the whole plan", planCategory.Category)
		}
		// The elevated helper exists only for work that genuinely needs root.
		// A non-sudo category showing up here means the plan was not produced
		// by the code we think produced it.
		if !c.RequiresSudo() {
			return fmt.Errorf("plan contains category %q, which does not require elevation; refusing the whole plan", planCategory.Category)
		}
		// disabled_categories is deliberately NOT checked here.
		//
		// It is documented as a soft default -- "do not include this unless I
		// ask for it" -- and an explicit selection overrides it everywhere
		// else in the CLI (see resolveCleaners). Enforcing it only for the
		// categories that happen to need root gave the tool two contradictory
		// policies separated by nothing but a privilege requirement.
		//
		// It is also not a security control, and treating it as one would be
		// misleading: the guards that bound this plan are the category being
		// known and RequiresSudo (above), and fence 2 restricting every
		// deletion to what a fresh privileged scan returns. Neither depends on
		// config. If a hard, plan-vetoing block is ever wanted, it belongs in
		// its own config concept rather than overloading this one.
		total += len(planCategory.Entries)
	}

	if total == 0 {
		return fmt.Errorf("plan contains no entries; nothing to elevate for")
	}
	return nil
}

// intersectEntries is fence 1 ∩ fence 2.
//
// It returns the FRESH scan's entries for the paths the user approved, never
// the plan's copies: the plan's sizes and mod times may be stale, and its
// Protected flag is attacker-controllable input that must never be allowed to
// short-circuit the real tagging that runClean applies. Matching is exact Path
// string equality -- no cleaning, no symlink resolution, no case folding -- so
// that "is this in the cleaner's domain" is decided solely by whether Scan
// itself produced that exact path.
//
// approved counts distinct approved paths; missing counts the approved paths
// the fresh scan did not return.
func intersectEntries(approvedEntries, freshEntries []cleaner.FileEntry, category cleaner.Category) (matched []cleaner.FileEntry, approved, missing int) {
	freshByPath := make(map[string]cleaner.FileEntry, len(freshEntries))
	for _, e := range freshEntries {
		if _, exists := freshByPath[e.Path]; !exists {
			freshByPath[e.Path] = e
		}
	}

	approvedPaths := dedupeByPath(approvedEntries)
	matched = make([]cleaner.FileEntry, 0, len(approvedPaths))
	for _, path := range approvedPaths {
		entry, ok := freshByPath[path]
		if !ok {
			missing++
			continue
		}
		// Category is normalized here because some cleaners leave it unset on
		// their entries; downstream reporting keys on it.
		entry.Category = category
		// Never inherit the plan's Protected flag: tagging is config's job and
		// happens again inside runClean.
		entry.Protected = false
		matched = append(matched, entry)
	}

	return matched, len(approvedPaths), missing
}

// dedupeByPath returns the distinct paths of entries, in first-seen order, so
// a plan that repeats a path cannot make it be counted (or handed to Clean)
// twice.
func dedupeByPath(entries []cleaner.FileEntry) []string {
	seen := make(map[string]struct{}, len(entries))
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		if _, dup := seen[e.Path]; dup {
			continue
		}
		seen[e.Path] = struct{}{}
		paths = append(paths, e.Path)
	}
	return paths
}

// progressf writes a human-readable progress line. Errors are ignored on
// purpose: progress output is cosmetic and must never be able to abort a run
// that is already deleting files.
func progressf(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	_, _ = fmt.Fprintf(w, format, args...)
}
