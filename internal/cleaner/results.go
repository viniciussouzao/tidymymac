package cleaner

import "time"

// FileEntry represents a single file or directory found during the scanning
type FileEntry struct {
	Path     string
	Size     int64
	IsDir    bool
	ModTime  time.Time
	Category Category

	// ResourceKind classifies the entry beyond its Path, so reporting code does
	// not have to parse Path to know what it is looking at. It is Docker
	// specific for now (see the DockerResourceKind* constants in docker.go) and
	// is left empty by every other cleaner. Purely descriptive: deletion logic
	// must not depend on it.
	ResourceKind string

	// Protected is set exclusively by internal/config's tagging layer; no
	// Cleaner.Scan implementation should ever set it.
	Protected bool

	// Dev and Ino identify the filesystem object the scan actually measured,
	// so Clean can refuse to delete a *different* object that has since taken
	// this path. Confining removal to the scan root (see saferemove.go) stops
	// a swapped path component from escaping the cleaner's domain; this stops
	// it from redirecting onto another file *inside* the domain -- one the
	// user deselected, or one config.StripProtected removed from the list,
	// since protection is applied to entries and not to roots.
	//
	// json:"-" is deliberate and load-bearing. The identity is a local fact
	// established by whichever process ran the scan. It must never arrive over
	// the elevation IPC or out of a --from-file scan file, where it would be
	// attacker-supplied; the elevated helper's intersection returns the fresh
	// privileged scan's entries, so the value Clean checks is always one this
	// process observed itself. Zero means "unknown", and the check is skipped.
	//
	// Only the cleaners that can run elevated populate these.
	Dev uint64 `json:"-"`
	Ino uint64 `json:"-"`
}

// ScanProgress reports the scanning progress back to the TUI
type ScanProgress struct {
	Category   Category
	FilesFound int
	BytesFound int64
	CurrentDir string
}

// ScanResult holds the results of the scanning process
type ScanResult struct {
	Category   Category
	Entries    []FileEntry
	TotalSize  int64
	TotalFiles int
	SizeKnown  bool
	Duration   time.Duration
	Errors     []error
}

// ClenProgress reports the cleanup progress
type CleanProgress struct {
	Category     Category
	FilesDeleted int
	FilesTotal   int
	BytesDeleted int64
	BytesTotal   int64
	CurrentFile  string
}

// CleanResult is the complete result of the cleanup
type CleanResult struct {
	Category     Category
	FilesDeleted int
	BytesFreed   int64 // better name for this field?
	Errors       []error
	Duration     time.Duration
	DryRun       bool
	Skipped      bool   // true when the category was intentionally skipped (e.g. requires sudo but process is not elevated)
	SkipReason   string // human-readable reason, set whenever Skipped is true
}
