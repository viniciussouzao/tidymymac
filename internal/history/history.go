package history

import (
	"time"
)

// Record represents the history of runs, containing a slice of RunRecord.
type Record struct {
	Runs []RunRecord `json:"runs"`
}

// RunRecord represents a successful cleanup execution
type RunRecord struct {
	ID         int              `json:"id"`
	RanAt      time.Time        `json:"ran_at"`
	TotalFiles int              `json:"total_files"`
	TotalBytes int64            `json:"total_bytes"`
	DurationMs int64            `json:"duration_ms"`
	Categories []CategoryRecord `json:"categories"`
}

// CategoryRecord represents a per-category cleanup summary in a run
type CategoryRecord struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Files       int    `json:"files"`
	Bytes       int64  `json:"bytes"`
}

// AllTimeStats represents the all-time statistics of cleanup runs
type AllTimeStats struct {
	TotalRuns  int
	TotalFiles int
	TotalBytes int64
	AvgBytes   int64
	LastRunAt  time.Time
}

// Load reads the history from the default path
func Load() (Record, error) {
	p, err := path()
	if err != nil {
		return Record{}, err
	}

	return loadAtPath(p)
}

// Append adds a new run record to the history at the default path
func Append(run RunRecord) error {
	p, err := path()
	if err != nil {
		return err
	}

	return appendAtPath(p, run)
}

// Stats computes the all-time statistics from the given history record
func Stats(record Record) AllTimeStats {
	var out AllTimeStats
	for _, run := range record.Runs {
		out.TotalRuns++
		out.TotalFiles += run.TotalFiles
		out.TotalBytes += run.TotalBytes
		if run.RanAt.After(out.LastRunAt) {
			out.LastRunAt = run.RanAt
		}
	}

	if out.TotalRuns > 0 {
		out.AvgBytes = out.TotalBytes / int64(out.TotalRuns)
	}

	return out
}

func StatsByCategory(record Record, category string) AllTimeStats {
	var out AllTimeStats
	for _, run := range record.Runs {
		var matched bool
		for _, cat := range run.Categories {
			if cat.Name != category {
				continue
			}
			matched = true
			out.TotalFiles += cat.Files
			out.TotalBytes += cat.Bytes

		}
		if matched {
			out.TotalRuns++
			if run.RanAt.After(out.LastRunAt) {
				out.LastRunAt = run.RanAt
			}
		}
	}

	if out.TotalRuns > 0 {
		out.AvgBytes = out.TotalBytes / int64(out.TotalRuns)
	}

	return out
}
