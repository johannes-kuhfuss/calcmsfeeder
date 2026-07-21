package app

import (
	"bufio"
	"bytes"
	"strings"
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
	events       []domain.CalCMSEvent
	hasRecording bool
	loginCalls   int
	checkCalls   int
	uploadCalls  int
}

func (s *recordingTestService) QueryEvents(time.Time, time.Time) ([]domain.CalCMSEvent, error) {
	return s.events, nil
}
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

func testRunner(fake *recordingTestService) *Runner {
	cfg := config.AppConfig{}
	cfg.CalCms.CmsUser = "user"
	cfg.CalCms.CmsPass = "secret"
	cfg.CalCms.DefaultDurationInDays = 7
	cfg.CalCms.MaxDurationInDays = 30
	cfg.Series = map[string]domain.SeriesInfo{
		"show": {SeriesID: 99, FileToUpload: "show.stream"},
	}
	runner := NewRunner(cfg, strings.NewReader(""), &bytes.Buffer{}, func() time.Time {
		return time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	})
	runner.Service = fake
	return runner
}

func TestUploadFilesHonorsOverwriteFlag(t *testing.T) {
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
			fake := &recordingTestService{hasRecording: true}
			runner := testRunner(fake)
			runner.Plan.Series["show"] = domain.SeriesPlan{
				SeriesInfo: domain.SeriesInfo{SeriesID: 99, FileToUpload: "show.stream"},
				EventIDs:   []int{42},
			}
			runner.Overwrite = tt.overwrite
			if err := runner.uploadFilesToCalCMS(); err != nil {
				t.Fatal(err)
			}
			if fake.loginCalls != 1 || fake.checkCalls != 1 || fake.uploadCalls != tt.wantUploads {
				t.Fatalf("calls: login=%d check=%d upload=%d, want 1, 1, %d", fake.loginCalls, fake.checkCalls, fake.uploadCalls, tt.wantUploads)
			}
		})
	}
}

func TestRunConsumesPipedInputWithOneScanner(t *testing.T) {
	fake := &recordingTestService{events: []domain.CalCMSEvent{{EventID: 42, Skey: "show"}}}
	runner := testRunner(fake)
	runner.Input = bufio.NewScanner(strings.NewReader("\n\ny\n"))

	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}
	if fake.uploadCalls != 1 {
		t.Fatalf("upload calls = %d, want 1", fake.uploadCalls)
	}
}
