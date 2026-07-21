package app

import (
	"testing"
	"time"

	"github.com/johannes-kuhfuss/calcmsfeeder/config"
	"github.com/johannes-kuhfuss/calcmsfeeder/domain"
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

type recordingTestService struct {
	hasRecording bool
	loginCalls   int
	checkCalls   int
	uploadCalls  int
}

func (s *recordingTestService) QueryEventsFromCalCms() error  { return nil }
func (s *recordingTestService) FilterEventsFromCalCms() error { return nil }
func (s *recordingTestService) Login(string, string) error {
	s.loginCalls++
	return nil
}
func (s *recordingTestService) HasRecording(int, int) (bool, error) {
	s.checkCalls++
	return s.hasRecording, nil
}
func (s *recordingTestService) UploadFile(int, int, string) error {
	s.uploadCalls++
	return nil
}

func TestUploadFilesHonorsOverwriteFlag(t *testing.T) {
	originalCfg, originalService, originalOverwrite := cfg, calCmsService, overwrite
	defer func() {
		cfg, calCmsService, overwrite = originalCfg, originalService, originalOverwrite
	}()

	tests := []struct {
		name        string
		overwrite   bool
		wantUploads int
	}{
		{name: "skip existing recording by default", overwrite: false, wantUploads: 0},
		{name: "overwrite when explicitly enabled", overwrite: true, wantUploads: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg = config.AppConfig{}
			cfg.RunTime.Series = map[string]domain.SeriesInfo{
				"show": {SeriesId: 99, FileToUpload: "show.stream", EventIds: []int{42}},
			}
			fake := &recordingTestService{hasRecording: true}
			calCmsService = fake
			overwrite = tt.overwrite
			if err := uploadFilesToCalCms(); err != nil {
				t.Fatal(err)
			}
			if fake.loginCalls != 1 || fake.checkCalls != 1 || fake.uploadCalls != tt.wantUploads {
				t.Fatalf("calls: login=%d check=%d upload=%d, want 1, 1, %d", fake.loginCalls, fake.checkCalls, fake.uploadCalls, tt.wantUploads)
			}
		})
	}
}
