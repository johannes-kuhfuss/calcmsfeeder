// Package domain defines the core data structures.
package domain

import "time"

// SeriesInfo contains the validated static configuration for a series.
type SeriesInfo struct {
	FileToUpload string
	SeriesID     int
}

// SeriesPlan contains the configured series and the matching events for one run.
type SeriesPlan struct {
	SeriesInfo
	EventIDs []int
}

// ExecutionPlan contains all mutable state for one application run.
type ExecutionPlan struct {
	StartDate time.Time
	EndDate   time.Time
	Series    map[string]SeriesPlan
}
