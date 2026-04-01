package cleaner

import "time"

// FileEntry represents a single file or directory found during the scanning
type FileEntry struct {
	Path     string
	Size     int64
	IsDir    bool
	ModTime  time.Time
	Category Category
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
	Skipped      bool // true when the category was intentionally skipped (e.g. requires sudo but process is not elevated)
}
