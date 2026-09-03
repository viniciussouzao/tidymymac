package cleaner

import "context"

type Cleaner interface {
	// Category returns the cleaner's category identifier.
	Category() Category

	// Name returns the cleaner's name.
	Name() string

	// Description returns a brief description of the cleaner.
	Description() string

	// Scan performs the scanning process and returns a list of items to be cleaned.
	Scan(ctx context.Context, progress func(ScanProgress)) (*ScanResult, error)

	// Clean performs the cleaning process based on the provided items and returns the result.
	Clean(ctx context.Context, entries []FileEntry, dryRun bool, progress func(CleanProgress)) (*CleanResult, error)

	// RequiresSudo indicates whether the cleaner requires elevated permissions to perform its operations.
	RequiresSudo() bool

	// DeletesWholeDomain reports whether Clean may perform a destructive
	// action that is NOT scoped to the entries it was given (e.g. shelling
	// out to "brew cleanup" or "go clean -cache", which clear their entire
	// domain regardless of the entries slice). Callers must never invoke
	// Clean for such a cleaner when any entry in its category was withheld
	// (e.g. by protected_paths) -- there'd be no way to honor the omission.
	DeletesWholeDomain() bool
}

// EntryRevalidator is an optional interface implemented by cleaners whose
// FileEntry.Path is not a literal filesystem path (Docker resources, Time
// Machine snapshots). Callers that revalidate saved scan entries before a
// clean (see internal/commands) default to os.Stat, which would wrongly drop
// every such entry as "missing" -- so they must prefer this method when the
// cleaner implements it.
//
// RevalidateEntries re-checks entries against the cleaner's real backing store
// and returns the still-valid subset plus counts of entries that disappeared
// or changed type. err means revalidation itself could not be performed at all
// (daemon down, tmutil absent) and is deliberately distinct from "the entries
// are gone": callers must not treat it as an empty result.
type EntryRevalidator interface {
	RevalidateEntries(ctx context.Context, entries []FileEntry) (revalidated []FileEntry, missing int, typeChanged int, err error)
}

// PrivilegeSplitter is an optional interface for a RequiresSudo cleaner whose
// domain spans locations at different privilege levels. Not every entry a
// RequiresSudo cleaner scans actually needs root to delete -- some of it may
// be the invoking user's own files that just happen to live alongside
// something that does. Callers that build an elevate.Plan should prefer this
// interface when the cleaner implements it: NeedsSudo(entry) partitions
// entries so only the ones that truly require root are elevated, and
// everything else is cleaned directly by the unprivileged process instead of
// needlessly widening what root touches.
//
// A cleaner that does not implement this interface keeps RequiresSudo()'s
// current all-or-nothing behavior -- every entry goes to the elevated
// helper. That stays the safe, conservative default. So does a cleaner that
// also DeletesWholeDomain: it may not be handed a partial entry list at all,
// so there is nothing for a split to do.
//
// Two invariants an implementation must hold, both about classification and
// deletion agreeing on the same path:
//
//  1. NeedsSudo compares entry.Path literally -- filepath.Clean at most. The
//     path is already spelled the way the cleaner's own Scan produced it,
//     i.e. relative to a root already canonicalized by resolveScanRoots, and
//     it is what rootedRemover.locate is checked against at deletion time.
//     Re-resolving symlinks here would classify against a different path
//     than the one that gets deleted, and let a component swapped in between
//     decide which side of the privilege split an entry lands on.
//  2. The roots NeedsSudo returns true for are an exact subset of the
//     cleaner's own scan roots. If the two ever diverge, an entry could be
//     classified for a location the cleaner is not confined to at all --
//     either elevating something outside its domain, or routing a genuinely
//     privileged path to the unprivileged leg where it can only fail.
type PrivilegeSplitter interface {
	NeedsSudo(entry FileEntry) bool
}

// Registry is a struct that holds registered cleaners and provides methods to manage them.
type Registry struct {
	cleaners []Cleaner
	byID     map[Category]Cleaner
}

// Register creates an empty registry
func NewRegistry() *Registry {
	return &Registry{
		byID: make(map[Category]Cleaner),
	}
}

// Register adds a new cleaner to the registry.
func (r *Registry) Register(c Cleaner) {
	r.cleaners = append(r.cleaners, c)
	r.byID[c.Category()] = c
}

// Get retrieves a cleaner by its category identifier. It returns the cleaner and a boolean indicating whether it was found.
func (r *Registry) Get(category Category) (Cleaner, bool) {
	c, ok := r.byID[category]
	return c, ok
}

// All returns a slice of all registered cleaners.
func (r *Registry) All() []Cleaner {
	return r.cleaners
}

func DefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register(NewTempCleaner())
	r.Register(NewHomebrewCleaner())
	r.Register(NewCachesCleaner())
	r.Register(NewDevelopmentArtifactsCleaner())
	// No paths by default: project-artifacts only ever has roots when a
	// profile supplies them (see config.ResolveProfile), so a bare
	// "scan project-artifacts" is inert rather than broken.
	r.Register(NewProjectArtifactsCleaner(nil, 0, false))
	r.Register(NewLogsCleaner())
	r.Register(NewDockerCleaner())
	r.Register(NewIOSBackupsCleaner())
	r.Register(NewUpdatesCleaner())
	r.Register(NewDownloadsCleaner())
	r.Register(NewAppOrphansCleaner())
	r.Register(NewTrashCleaner())
	r.Register(NewXcodeCleaner())
	r.Register(NewTimeMachineCleaner())
	return r
}
