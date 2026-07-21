package app

import (
	"testing"
	"time"
)

func TestEndDateForDurationIsInclusive(t *testing.T) {
	start := time.Date(2026, time.March, 28, 0, 0, 0, 0, time.Local)
	tests := []struct {
		name string
		days int
		want string
	}{
		{name: "one day", days: 1, want: "2026-03-28"},
		{name: "seven days", days: 7, want: "2026-04-03"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := endDateForDuration(start, tt.days).Format(dateFormat)
			if got != tt.want {
				t.Fatalf("endDateForDuration() = %s, want %s", got, tt.want)
			}
		})
	}
}
