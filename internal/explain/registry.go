package explain

import (
	"fmt"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
)

type ProfileDefinition struct {
	Profile      []Profile
	Description  string
	Summary      string
	CoverageNote string
	Contributors []Contributor
}

type Registry struct {
	profiles []ProfileDefinition
	byID     map[Profile]ProfileDefinition
}

func NewRegistry() *Registry {
	return &Registry{
		byID: make(map[Profile]ProfileDefinition),
	}
}

func (r *Registry) Register(def ProfileDefinition) {
	r.profiles = append(r.profiles, def)
	for _, name := range def.Profile {
		r.byID[name] = def
	}
}

func (r *Registry) Get(profile Profile) (ProfileDefinition, bool) {
	def, ok := r.byID[profile]
	return def, ok
}

func (r *Registry) All() []ProfileDefinition {
	return r.profiles
}

func DefaultRegistry(cleanerRegistry *cleaner.Registry) *Registry {
	r := NewRegistry()
	r.Register(systemDataProfile(cleanerRegistry))
	return r
}

func ResolveProfile(profile Profile, cleanerRegistry *cleaner.Registry) (ProfileDefinition, error) {
	r := DefaultRegistry(cleanerRegistry)
	def, ok := r.Get(profile)
	if !ok {
		return ProfileDefinition{}, fmt.Errorf("unknown profile %q", profile)
	}
	return def, nil
}

func systemDataProfile(registry *cleaner.Registry) ProfileDefinition {
	return ProfileDefinition{
		Profile:      []Profile{ProfileSystemData},
		Description:  "Explains what macOS groups under System Data in Storage settings.",
		Summary:      "System Data is a broad macOS storage category that often includes caches, logs, temporary files, local snapshots and update leftovers.",
		CoverageNote: "This explanation covers the System Data contributors currently detectable by TidyMyMac. macOS may include additional internal data not shown here.",
		Contributors: []Contributor{
			newContributorDetails(registry, contributorSpec{
				name:        ContributorCaches,
				description: "Cached data created to speed up apps and repeated tasks.",
				sources:     []string{"browsers", "IDEs", "media apps", "desktop applications"},
				category:    cleaner.CategoryApplicationCaches,
				context: ExplainContext{
					WhatIsThis:     "Cached data created to speed up apps and repeated tasks.",
					WhatGenerates:  "Browsers, IDEs, media apps and other desktop applications.",
					WhyItAppears:   "Cached support data is often grouped by macOS under System Data.",
					Recommendation: "Usually safe to remove; applications may rebuild these files when needed.",
					SafetyLevel:    SafetyLevelSafe,
					RelatedCommand: "tidymymac clean caches",
				},
			}),
			newContributorDetails(registry, contributorSpec{
				name:        ContributorLogs,
				description: "Logs created by applications and system components to record events and errors.",
				sources:     []string{"apps", "background services", "developer tools", "macOS"},
				category:    cleaner.CategoryLogs,
				context: ExplainContext{
					WhatIsThis:     "Logs created by applications and system components to record events and errors.",
					WhatGenerates:  "Apps, background services, developer tools and macOS itself.",
					WhyItAppears:   "Log files are commonly stored outside the categories most users see.",
					Recommendation: "Usually safe to remove older logs; new logs will continue to be generated.",
					SafetyLevel:    SafetyLevelSafe,
					RelatedCommand: "tidymymac clean logs",
				},
			}),
			newContributorDetails(registry, contributorSpec{
				name:        ContributorTempFiles,
				description: "Short-lived files left behind by apps and system processes.",
				sources:     []string{"installers", "downloads", "app workflows", "temporary processing"},
				category:    cleaner.CategoryTemp,
				context: ExplainContext{
					WhatIsThis:     "Short-lived files left behind by apps and system processes.",
					WhatGenerates:  "Installers, downloads, app workflows and temporary processing.",
					WhyItAppears:   "Temporary working data can accumulate in locations macOS later groups as System Data.",
					Recommendation: "Usually safe to review and remove when stale.",
					SafetyLevel:    SafetyLevelSafe,
					RelatedCommand: "tidymymac clean temp",
				},
			}),
			newContributorDetails(registry, contributorSpec{
				name:        ContributorTimeMachineSnapshots,
				description: "Local snapshots created by Time Machine to preserve recent file versions.",
				sources:     []string{"Time Machine local backup services"},
				category:    cleaner.CategoryTimeMachineSnapshots,
				context: ExplainContext{
					WhatIsThis:     "Local snapshots created by Time Machine to preserve recent file versions.",
					WhatGenerates:  "Time Machine local backup services.",
					WhyItAppears:   "Local snapshots are frequently counted as System Data by macOS.",
					Recommendation: "Review carefully; macOS may thin snapshots automatically when disk space is needed.",
					SafetyLevel:    SafetyLevelCaution,
					RelatedCommand: "tidymymac clean time_machine",
				},
			}),
			newContributorDetails(registry, contributorSpec{
				name:        ContributorMacOSUpdates,
				description: "Files downloaded or left behind by macOS updates.",
				sources:     []string{"Software Update", "system upgrade processes"},
				category:    cleaner.CategoryUpdates,
				context: ExplainContext{
					WhatIsThis:     "Files downloaded or left behind by macOS updates.",
					WhatGenerates:  "Software Update and system upgrade processes.",
					WhyItAppears:   "Completed or partial update files may remain outside the main user-visible categories.",
					Recommendation: "Review if you suspect unfinished or old update leftovers.",
					SafetyLevel:    SafetyLevelCaution,
					RelatedCommand: "tidymymac clean macos_updates",
				},
			}),
		},
	}
}

func missingCleanerError(spec contributorSpec) error {
	return fmt.Errorf("cleaner %q for contributor %q is not registered", spec.category, spec.name)
}

func newContributorDetails(registry *cleaner.Registry, spec contributorSpec) Contributor {
	c := scannerContributor{
		name:        spec.name,
		description: spec.description,
		sources:     append([]string(nil), spec.sources...),
		context:     spec.context,
	}

	if registry != nil {
		if underlying, ok := registry.Get(spec.category); ok {
			c.cleaner = underlying
			return c
		}
	}

	c.runtimeErr = missingCleanerError(spec)
	return c
}
