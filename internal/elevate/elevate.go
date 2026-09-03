// Package elevate implements the privilege-escalation boundary of TidyMyMac.
//
// The design goal is that the user never has to start the whole application
// under sudo. Instead the unprivileged process scans, shows what it found and
// gets an explicit approval; only then does it re-execute *itself* under sudo
// with a single, narrow job: delete the approved things and report back.
//
// # The two-fence model
//
// A root process that is handed a list of paths and deletes them is a
// confused deputy: whoever can influence that list can delete anything on the
// machine. So the helper never trusts the plan on its own. It deletes only the
// intersection of two independent fences:
//
//	fence 1 (intent)  the approved plan -- what the human actually reviewed
//	fence 2 (domain)  a fresh Cleaner.Scan() run as root -- what that
//	                  category legitimately owns, right now
//
// Neither fence alone is sufficient and neither is trusted alone. A tampered
// plan pointing at ~/Documents survives fence 1 but can never survive fence 2,
// because no cleaner's Scan would ever return those paths. A category that
// grew new junk between approval and elevation survives fence 2 but not fence
// 1, so nothing the user did not see is ever removed. Entries that pass fence
// 1 but not fence 2 are reported as missing/skipped -- never deleted.
//
// Matching between the fences is exact FileEntry.Path string equality, and the
// entry that is actually handed to Clean is the *fresh-scan* entry, so sizes
// and attributes reflect the filesystem as it is at deletion time rather than
// whatever the (possibly stale, possibly forged) plan claimed.
//
// # What this package deliberately does not do
//
// It does not re-implement protected_paths. The intersection is assembled into
// a commands.PreparedScanResult and handed to commands.RunCleanWithPreparedScanResult,
// so config.Tag/StripProtected and the DeletesWholeDomain skip stay in their
// single canonical place. A second implementation of the safety gate is a
// second thing that can drift out of sync with the first.
//
// It also never sees the user's password. There is no askpass, no "sudo -S",
// no reading of secrets from stdin: the child's stdin and stderr are inherited
// from the terminal so sudo's own prompt runs untouched.
package elevate

import (
	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/commands"
)

// PlanSchemaVersion / ResultSchemaVersion version the IPC payloads exchanged
// between Invoke (parent) and RunHelper (root child). Both sides hard-fail on
// a mismatch instead of best-effort decoding: parent and child are normally
// the same binary, so a mismatch means something unexpected is on the other
// end of the pipe, which is exactly when a security boundary should stop.
const (
	PlanSchemaVersion = 1
	// 2: CleanCategoryResult gained partial_errors / partial_error_details /
	// partial_errors_truncated, and a partial failure now sets HasErrors.
	ResultSchemaVersion = 2
)

// Plan is the approved work handed to the elevated helper.
//
// It is a dedicated struct rather than a reused commands.ScanResult. A
// ScanResult carries scan-time totals, human-formatted sizes, a timestamp and
// per-category errors -- all of which the helper must ignore anyway, since it
// re-scans and recomputes everything from the fresh run. Shipping those fields
// across the privilege boundary would make them look authoritative when they
// are merely advisory, and every field crossing this boundary is attack
// surface that has to be justified. What the helper actually needs is only:
// which categories, which paths within them, and dry-run or not.
type Plan struct {
	Version int `json:"version"`

	// DryRun mirrors the unprivileged side's --execute state. A dry-run
	// elevation is genuinely useful: it proves the sudo path works and shows
	// what root would be able to reach, without deleting anything.
	DryRun bool `json:"dry_run"`

	Categories []PlanCategory `json:"categories"`
}

// PlanCategory is the set of approved entries for one category. Only cleaners
// reporting RequiresSudo() may appear here; RunHelper rejects the whole plan
// otherwise (see validatePlan).
type PlanCategory struct {
	Category cleaner.Category `json:"category"`

	// Entries are the entries the user reviewed and approved. Only their Path
	// is load-bearing -- every other field is re-derived from the fresh scan.
	Entries []cleaner.FileEntry `json:"entries"`
}

// Result is what the helper writes to stdout, and the only thing it writes
// there.
type Result struct {
	Version int `json:"version"`

	// Clean is the normal clean outcome, produced by the very same
	// commands.runClean the unprivileged path uses.
	Clean commands.CleanResult `json:"clean"`

	// Intersections explains, per category, how the two fences lined up, so a
	// caller can render "cleaned / skipped / failed" with a cause instead of
	// silently showing fewer files than the user approved.
	Intersections []CategoryIntersection `json:"intersections"`
}

// CategoryIntersection reports the fence-1/fence-2 arithmetic for one category.
// Approved == Matched + Missing whenever ErrMsg is empty.
type CategoryIntersection struct {
	Category cleaner.Category `json:"category"`
	Name     string           `json:"name"`

	// Approved is how many entries the plan contained for this category
	// (after de-duplication by path).
	Approved int `json:"approved"`

	// Matched is how many of those the fresh root scan also returned. These
	// are the only entries that can possibly be deleted.
	Matched int `json:"matched"`

	// Missing is Approved-Matched: approved entries the fresh scan did not
	// return. Either they are already gone, or they never belonged to this
	// cleaner's domain in the first place. Both cases are skips, not errors.
	Missing int `json:"missing"`

	// ErrMsg is set when the fresh scan for this category failed. The category
	// is then skipped and its siblings still run, mirroring how runClean
	// scopes per-category errors.
	ErrMsg string `json:"error,omitempty"`
}

// HasErrors reports whether anything in the run failed -- either a fresh scan
// or the clean itself. Callers use it to decide exit codes; per-category
// detail lives in Clean.Categories and Intersections.
func (r Result) HasErrors() bool {
	if r.Clean.HasErrors {
		return true
	}
	for _, i := range r.Intersections {
		if i.ErrMsg != "" {
			return true
		}
	}
	return false
}
