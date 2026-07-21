package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validTestConfig() AppConfig {
	var cfg AppConfig
	cfg.CalCms.CmsHost = "https://calendar.example"
	cfg.CalCms.CmsUser = "user"
	cfg.CalCms.CmsPass = "secret"
	cfg.CalCms.Template = "events.json"
	cfg.CalCms.ProjectID = 1
	cfg.CalCms.StudioID = 1
	cfg.CalCms.DefaultDurationInDays = 7
	cfg.CalCms.MaxDurationInDays = 30
	cfg.CalCms.SeriesFiles = map[string]string{"show": "show.stream"}
	cfg.CalCms.SeriesIds = map[string]int{"show": 42}
	return cfg
}

func TestValidateAndBuildRuntimeResolvesRelativeFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "show.stream")
	if err := os.WriteFile(file, []byte("stream contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := validTestConfig()
	if err := validateAndBuildRuntime(&cfg, dir); err != nil {
		t.Fatalf("validateAndBuildRuntime() error = %v", err)
	}
	got := cfg.RunTime.Series["show"]
	if got.SeriesId != 42 {
		t.Fatalf("series ID = %d, want 42", got.SeriesId)
	}
	want, err := filepath.EvalSymlinks(file)
	if err != nil {
		t.Fatal(err)
	}
	if got.FileToUpload != want {
		t.Fatalf("upload file = %q, want %q", got.FileToUpload, want)
	}
}

func TestValidateAndBuildRuntimeRejectsUnsafeOrIncompleteConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "show.stream"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*AppConfig)
		want   string
	}{
		{name: "insecure host", mutate: func(c *AppConfig) { c.CalCms.CmsHost = "http://calendar.example" }, want: "must use https"},
		{name: "missing credentials", mutate: func(c *AppConfig) { c.CalCms.CmsPass = "" }, want: "are required"},
		{name: "invalid duration", mutate: func(c *AppConfig) { c.CalCms.DefaultDurationInDays = 31 }, want: "1 <= default <= maximum"},
		{name: "missing series ID", mutate: func(c *AppConfig) { delete(c.CalCms.SeriesIds, "show") }, want: "positive ID"},
		{name: "missing upload file", mutate: func(c *AppConfig) { c.CalCms.SeriesFiles["show"] = "missing.stream" }, want: "invalid upload file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validTestConfig()
			tt.mutate(&cfg)
			err := validateAndBuildRuntime(&cfg, dir)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want error containing %q", err, tt.want)
			}
		})
	}
}
