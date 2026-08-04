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
