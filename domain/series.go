// package domain defines the core data structures
package domain

// SeriesInfo collects all relevant data for one series and its events
type SeriesInfo struct {
	FileToUpload string
	SeriesId     int
	EventIds     []int
}
