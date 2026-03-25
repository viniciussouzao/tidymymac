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
	r.Register(NewLogsCleaner())
	r.Register(NewDockerCleaner())
	r.Register(NewIOSBackupsCleaner())
	r.Register(NewUpdatesCleaner())
	r.Register(NewTrashCleaner())
	r.Register(NewXcodeCleaner())
	return r
}
