package tui

import (
	"context"
	"io/fs"
	"os"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/commands"
	"github.com/viniciussouzao/tidymymac/internal/config"
	"github.com/viniciussouzao/tidymymac/internal/tui/screens"
)

// revalidateCompleteMsg carries the outcome of a revalidateCmd call. See
// handleRevalidateComplete. seq is the value App.revalidateSeq had at
// dispatch time, echoed back so a stale result (superseded by a later
// dispatch, or delivered after the user navigated away from the review
// screen entirely) can be told apart from the one currently awaited.
type revalidateCompleteMsg struct {
	seq          int
	results      map[cleaner.Category]*cleaner.ScanResult
	delta        screens.RevalidationDelta
	categoryErrs map[cleaner.Category]error
	err          error
}

// revalidateCmd runs revalidatePlan as an ordinary tea.Cmd rather than
// inline inside Update. revalidatePlan os.Stats every approved entry --
// tens of thousands for a category like Caches -- and, for Docker/Time
// Machine categories, shells out to their EntryRevalidator (docker ps/images,
// tmutil). Running that synchronously inside Update would freeze the whole
// event loop for however long it takes, exactly the anti-pattern
// startElevation's own doc comment already warns about (no key press,
// including q, could reach a.cancel() until it returned); dispatching it as
// a Cmd keeps the UI responsive and lets ctx cancellation actually reach the
// EntryRevalidator's shell-outs.
func revalidateCmd(ctx context.Context, seq int, registry *cleaner.Registry, cfg *config.Config, results map[cleaner.Category]*cleaner.ScanResult) tea.Cmd {
	return func() tea.Msg {
		revalidated, delta, categoryErrs, err := revalidatePlan(ctx, registry, cfg, results)
		return revalidateCompleteMsg{seq: seq, results: revalidated, delta: delta, categoryErrs: categoryErrs, err: err}
	}
}

// revalidatePlan re-checks results -- the exact snapshot the review screen
// was built from -- against disk and the current config, immediately before
// it is handed to cleaning. A file can vanish, change type, or become newly
// protected during however long the user sat on the review screen; nothing
// upstream of this catches that.
//
// It reuses commands.PrepareScanResultForClean, the same function
// `clean --from-file` uses to revalidate a saved scan, rather than
// reimplementing missing/type-changed/protected-path detection here: that
// gets EntryRevalidator dispatch for Docker/Time Machine for free, and keeps
// the TUI, `--from-file`, and (once its own fence-2 re-scan runs) the
// elevated sudo helper all agreeing on what "still valid" means.
//
// It never adds an entry beyond what results already approved:
// PrepareScanResultForClean's own contract is that its revalidation output
// narrows its input, but a contract is not a check, so attachFreshIdentity
// below drops anything that doesn't correspond to an originally-approved
// {category, path} pair as a second, independent guard.
//
// A category whose own revalidation failed (categoryErrs) is still included
// in the returned map (with whatever entries did survive, which may be
// none) -- the caller decides what "failed" means for the confirmation
// flow. This function itself never treats a pre-existing scan-time error
// (out.Errors, carried over from handleScanComplete) as a revalidation
// failure: those are already-known, non-fatal per-directory scan issues
// (see e.g. internal/cleaner/caches.go, app_orphans.go), and a scan that
// picked up one of those must not permanently block confirming forever
// after -- see the categoryErrs doc for the distinction.
func revalidatePlan(ctx context.Context, registry *cleaner.Registry, cfg *config.Config, results map[cleaner.Category]*cleaner.ScanResult) (map[cleaner.Category]*cleaner.ScanResult, screens.RevalidationDelta, map[cleaner.Category]error, error) {
	scan, selected, originalByKey, wasProtected := prepareRevalidationInput(results)

	if len(selected) == 0 {
		// Nothing in results actually had entries to revalidate. Calling
		// PrepareScanResultForClean with an empty selected would make it
		// default to "every category in the registry" (see resolveCleaners),
		// needlessly revalidating -- and for Docker/Time Machine, shelling
		// out for -- categories the user never scanned or reviewed at all.
		return map[cleaner.Category]*cleaner.ScanResult{}, screens.RevalidationDelta{}, nil, nil
	}

	prepared, err := commands.PrepareScanResultForClean(ctx, registry, scan, selected, cfg)
	if err != nil {
		return nil, screens.RevalidationDelta{}, nil, err
	}

	revalidated := make(map[cleaner.Category]*cleaner.ScanResult, len(results))
	categoryErrs := make(map[cleaner.Category]error)
	var beforeSize, afterSize int64
	var beforeFiles, afterFiles int
	var newlyProtected int
	var identityChanged int

	for _, orig := range originalByKey {
		if orig.Protected {
			continue
		}
		beforeSize += orig.Size
		beforeFiles++
	}

	for _, item := range prepared.Result.Categories {
		out, ok := results[item.Category]
		if !ok {
			// Every category PrepareScanResultForClean reports comes from
			// selected, which is built strictly from results' own keys
			// above -- so this only happens if the registry changed shape
			// mid-call, which dropping the category is the safe way to
			// handle rather than crash on.
			continue
		}
		if item.Err != nil {
			categoryErrs[item.Category] = item.Err
		}

		// Docker/Time Machine entries are not filesystem paths (a container
		// ID, a snapshot name); Lstat-ing them would resolve against the
		// process cwd and attach a meaningless identity. Their cleaner
		// implementing cleaner.EntryRevalidator is what makes that true,
		// independent of category -- the same signal PrepareScanResultForClean
		// itself already dispatches on above.
		skipIdentity := false
		if c, ok := registry.Get(item.Category); ok {
			_, skipIdentity = c.(cleaner.EntryRevalidator)
		}

		// Copy rather than mutate results' own *ScanResult: results still
		// belongs to the caller, and fields commands.ScanCategoryResult has
		// no equivalent for -- notably SizeKnown, used for Time Machine's
		// "unknown size" badge -- must survive the round trip unchanged.
		next := *out
		var changed int
		next.Entries, changed = attachFreshIdentity(item.Files, item.Category, originalByKey, skipIdentity)
		identityChanged += changed
		next.TotalFiles = len(next.Entries)
		next.TotalSize = 0
		for _, e := range next.Entries {
			next.TotalSize += e.Size
		}
		revalidated[item.Category] = &next

		for _, e := range next.Entries {
			if e.Protected {
				if !wasProtected[entryKey{item.Category, e.Path}] {
					newlyProtected++
				}
				continue
			}
			afterSize += e.Size
			afterFiles++
		}
	}

	delta := screens.RevalidationDelta{
		MissingFiles:     prepared.MissingFiles,
		TypeChangedFiles: prepared.TypeChangedFiles,
		NewlyProtected:   newlyProtected,
		IdentityChanged:  identityChanged,
		SizeChanged:      afterSize != beforeSize || afterFiles != beforeFiles,
		TotalSize:        afterSize,
		TotalFiles:       afterFiles,
	}

	return revalidated, delta, categoryErrs, nil
}

// entryKey identifies an approved entry by both its category and path, not
// path alone: two categories can legitimately report the same path (e.g.
// Caches and App Orphans both walk ~/Library/Caches), and a bare-path map
// would let one category's entry silently answer a lookup for the other.
type entryKey struct {
	Category cleaner.Category
	Path     string
}

// prepareRevalidationInput converts results (the tui package's
// map[cleaner.Category]*cleaner.ScanResult) into the commands.ScanResult
// shape PrepareScanResultForClean expects, and returns two lookups derived
// from the pre-revalidation entries, both keyed by entryKey: originalByKey
// (every entry, used both by attachFreshIdentity's "was this approved"
// guard and to build wasProtected) and wasProtected (the subset already
// tagged Protected before this revalidation ran, so a newly-protected path
// can be told apart from one that already was).
//
// selected is built explicitly from results' own categories -- passing nil
// would make PrepareScanResultForClean default to every category in the
// registry (via resolveCleaners), which would needlessly revalidate
// categories the user never scanned or reviewed at all.
func prepareRevalidationInput(results map[cleaner.Category]*cleaner.ScanResult) (commands.ScanResult, []string, map[entryKey]cleaner.FileEntry, map[entryKey]bool) {
	scan := commands.ScanResult{}
	selected := make([]string, 0, len(results))
	originalByKey := make(map[entryKey]cleaner.FileEntry)
	wasProtected := make(map[entryKey]bool)

	for category, result := range results {
		if result == nil || result.TotalFiles == 0 {
			continue
		}
		selected = append(selected, string(category))
		scan.Categories = append(scan.Categories, commands.ScanCategoryResult{
			Category:   category,
			Name:       string(category),
			TotalFiles: result.TotalFiles,
			TotalSize:  result.TotalSize,
			Files:      result.Entries,
		})
		for _, e := range result.Entries {
			key := entryKey{category, e.Path}
			originalByKey[key] = e
			if e.Protected {
				wasProtected[key] = true
			}
		}
	}

	return scan, selected, originalByKey, wasProtected
}

// attachFreshIdentity restores each revalidated entry's Dev/Ino to the value
// captured at scan time -- the moment the user actually approved the file --
// rather than re-deriving it now. PrepareScanResultForClean's own
// revalidation deliberately never sets Dev/Ino (see revalidateEntries' doc
// comment): it exists mainly to serve `clean --from-file`, where the input
// is an untrusted scan file and inventing an identity for it would let a
// crafted entry authorize a swap. The TUI's input has no such problem --
// every entry originated from this same process's own Cleaner.Scan call --
// so leaving Dev/Ino unset here would only weaken saferemove's swap check at
// actual delete time for no reason.
//
// The identity is deliberately NOT re-captured via a fresh Lstat here: this
// revalidation pass runs at the end of the exact window (the user sitting on
// the review screen) an in-root symlink redirect could happen in (see
// internal/cleaner/saferemove.go's rootedRemover doc comment on that
// threat). Re-reading identity now would compare the fresh, possibly
// already-redirected Lstat against itself at delete time and let the swap
// through. Carrying the scan-time identity forward instead means
// saferemove's own check at delete time is comparing against the moment the
// user actually approved the file, closing that window as designed.
//
// A entry legitimately rewritten between scan and confirm (log rotation, an
// atomically-replaced cache file) gets a new inode; that case is not treated
// as a swap silently allowed through with the new identity, nor as a false
// "swap detected" surfacing as an opaque per-item failure during cleaning.
// Instead this Lstats once, purely to compare against the scan-time
// identity: a genuine mismatch drops the entry from the plan now and is
// counted so the caller can show it to the user as a delta, matching how
// missing/type-changed/newly-protected entries are already handled. An
// Lstat that fails, or an entry whose scan-time identity was never known
// (Ino == 0, e.g. revalidateEntries' own keep-on-ambiguous-stat-error path),
// is not treated as a mismatch -- the file's current state is simply
// unconfirmed here, and saferemove's own Lstat-then-compare immediately
// before unlink remains the final, fail-closed authority.
//
// skipIdentity is true for a category whose cleaner implements
// cleaner.EntryRevalidator (Docker, Time Machine): their entries are not
// filesystem paths at all (a container ID, a snapshot name), so Lstat-ing
// them would resolve against the process cwd and attach a meaningless
// identity. Their Dev/Ino is left exactly as RevalidateEntries returned it
// (normally unset).
//
// An entry that does not correspond to any originally-approved {category,
// path} pair IS dropped: PrepareScanResultForClean trusts its
// EntryRevalidator to only narrow, but that is a contract, not a check (see
// revalidatePlan's own doc comment), and this is the second, independent
// guard against a misbehaving one inventing a path the user never reviewed.
//
// A Protected entry is exempt from the identity-mismatch drop: it is never
// actually deleted (config.StripProtected removes it from the plan right
// before Clean is called), and it exists here purely as the signal a
// whole-domain cleaner's skip check depends on -- see
// config.CountProtected's callers and DeletesWholeDomain. Dropping it for an
// identity change it will never act on would remove that signal, letting
// the skip fail to fire while the protected file is still on disk (e.g. a
// protected_paths entry inside Trash/Homebrew/Development Artifacts whose
// inode happens to change between scan and confirm) -- the opposite of what
// this revalidation pass exists to guard against.
//
// Returns the filtered entries and how many were dropped for an identity
// mismatch (as opposed to for not being in originalByKey at all, or being
// exempt as Protected).
func attachFreshIdentity(entries []cleaner.FileEntry, category cleaner.Category, originalByKey map[entryKey]cleaner.FileEntry, skipIdentity bool) ([]cleaner.FileEntry, int) {
	out := make([]cleaner.FileEntry, 0, len(entries))
	var changed int
	for _, e := range entries {
		orig, ok := originalByKey[entryKey{category, e.Path}]
		if !ok {
			continue
		}
		if skipIdentity {
			out = append(out, e)
			continue
		}
		e.Dev, e.Ino = orig.Dev, orig.Ino
		if !e.Protected && orig.Ino != 0 {
			if info, err := os.Lstat(e.Path); err == nil {
				if dev, ino, ok := fileIdentity(info); ok && (dev != orig.Dev || ino != orig.Ino) {
					changed++
					continue
				}
			}
		}
		out = append(out, e)
	}
	return out, changed
}

// fileIdentity mirrors internal/cleaner's unexported helper of the same name
// (see saferemove.go's fileIdentity) -- duplicated rather than exported
// across packages for a four-line, platform-specific extraction.
func fileIdentity(info fs.FileInfo) (dev, ino uint64, ok bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return 0, 0, false
	}
	// Dev is signed on darwin; Ino is already uint64 on every supported
	// platform.
	return uint64(stat.Dev), stat.Ino, true
}
