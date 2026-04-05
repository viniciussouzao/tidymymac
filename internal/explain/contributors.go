package explain

import (
	"context"
	"time"
)

type ContributorName string

const (
	ContributorCaches ContributorName = "caches"

	ContributorLogs ContributorName = "logs"

	ContributorTrash ContributorName = "trash"

	ContributorTempFiles ContributorName = "temp"

	ContributorTimeMachineSnapshots ContributorName = "time_machine"

	ContributorMacOSUpdates ContributorName = "macos_updates"
)

func (c ContributorName) DisplayName() string {
	switch c {
	case ContributorCaches:
		return "Application Caches"
	case ContributorLogs:
		return "Logs"
	case ContributorTrash:
		return "Trash"
	case ContributorTempFiles:
		return "Temporary Files"
	case ContributorTimeMachineSnapshots:
		return "Time Machine Snapshots"
	case ContributorMacOSUpdates:
		return "macOS Update Residues"
	default:
		return string(c)
	}
}

type SafetyLevel string

const (
	SafetyLevelSafe SafetyLevel = "safe"

	SafetyLevelCaution SafetyLevel = "caution"

	SafetyLevelDoNotTouch SafetyLevel = "do_not_touch"
)

type ExplainContext struct {
	WhatIsThis     string
	WhatGenerates  string
	WhyItAppears   string
	Recommendation string
	SafetyLevel    SafetyLevel
	RelatedCommand string
}

type DetailedItem struct {
	Path string
	Size int64
}

type ContributorResult struct {
	Name           ContributorName
	TotalSize      int64
	TotalSizeHuman string
	TotalItems     int
	Items          []DetailedItem
	Context        ExplainContext
	HasError       bool
	ErrorMessage   string
}

type ProfileResult struct {
	Name           Profile
	Description    string
	Summary        string
	ScannedAt      time.Time
	TotalSize      int64
	TotalSizeHuman string
	Contributors   []ContributorResult
	HasErrors      bool
	CoverageNote   string
}

type Contributor interface {
	Name() ContributorName
	Description() string
	Sources() []string
	Run(ctx context.Context) (ContributorResult, error)
}
