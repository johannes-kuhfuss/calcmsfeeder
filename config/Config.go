// package config defines the program's configuration including the defaults
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johannes-kuhfuss/calcmsfeeder/domain"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

// Configuration with subsections
type AppConfig struct {
	CalCms struct {
		CmsHost               string            `envconfig:"CALCMS_HOST"`
		CmsUser               string            `envconfig:"CALCMS_USER"`
		CmsPass               string            `envconfig:"CALCMS_PASS"`
		Template              string            `envconfig:"CALCMS_TEMPLATE" default:"event.json-p"`
		ProjectID             int               `envconfig:"CALCMS_PROJECT_ID" default:"1"`
		StudioID              int               `envconfig:"CALCMS_STUDIO_ID" default:"1"`
		DefaultDurationInDays int               `envconfig:"DEFAULT_DURATION_IN_DAYS" default:"7"`
		MaxDurationInDays     int               `envconfig:"MAX_DURATION_IN_DAYS" default:"60"`
		RequestTimeout        time.Duration     `envconfig:"CALCMS_REQUEST_TIMEOUT" default:"5m"`
		SeriesFiles           map[string]string `envconfig:"SERIES_FILES"`
		SeriesIDs             map[string]int    `envconfig:"SERIES_IDS"`
	}
	Series map[string]domain.SeriesInfo `ignored:"true"`
}

// InitConfig initializes the configuration and sets the defaults
func InitConfig(file string, config *AppConfig) error {
	if err := loadConfig(file); err != nil {
		return fmt.Errorf("load configuration from file: %w", err)
	}
	if err := envconfig.Process("", config); err != nil {
		return fmt.Errorf("initialize configuration: %w", err)
	}
	return validateAndBuildSeries(config, filepath.Dir(file))
}

// checkFilePath validates and resolves an upload file path.
func checkFilePath(filePath, baseDir string) (string, error) {
	if strings.TrimSpace(filePath) == "" {
		return "", fmt.Errorf("file path is empty")
	}
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(baseDir, filePath)
	}
	filePath = filepath.Clean(filePath)
	info, err := os.Stat(filePath)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file")
	}
	resolved, err := filepath.EvalSymlinks(filePath)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

func validateAndBuildSeries(config *AppConfig, baseDir string) error {
	if config == nil {
		return fmt.Errorf("configuration is nil")
	}
	host, err := url.Parse(config.CalCms.CmsHost)
	if err != nil || host.Host == "" {
		return fmt.Errorf("CALCMS_HOST must be a valid absolute URL")
	}
	if host.Scheme != "https" {
		return fmt.Errorf("CALCMS_HOST must use https")
	}
	if config.CalCms.CmsUser == "" || config.CalCms.CmsPass == "" {
		return fmt.Errorf("CALCMS_USER and CALCMS_PASS are required")
	}
	if config.CalCms.Template == "" {
		return fmt.Errorf("CALCMS_TEMPLATE must not be empty")
	}
	if config.CalCms.ProjectID < 1 || config.CalCms.StudioID < 1 {
		return fmt.Errorf("CALCMS_PROJECT_ID and CALCMS_STUDIO_ID must be positive")
	}
	if config.CalCms.DefaultDurationInDays < 1 || config.CalCms.MaxDurationInDays < 1 || config.CalCms.DefaultDurationInDays > config.CalCms.MaxDurationInDays {
		return fmt.Errorf("duration defaults must satisfy 1 <= default <= maximum")
	}
	if config.CalCms.RequestTimeout <= 0 {
		return fmt.Errorf("CALCMS_REQUEST_TIMEOUT must be positive")
	}
	if len(config.CalCms.SeriesFiles) == 0 {
		return fmt.Errorf("SERIES_FILES must contain at least one entry")
	}
	config.Series = make(map[string]domain.SeriesInfo)
	for skey, file := range config.CalCms.SeriesFiles {
		seriesID, ok := config.CalCms.SeriesIDs[skey]
		if !ok || seriesID < 1 {
			return fmt.Errorf("SERIES_IDS must contain a positive ID for %q", skey)
		}
		file, err = checkFilePath(file, baseDir)
		if err != nil {
			return fmt.Errorf("invalid upload file for %q: %w", skey, err)
		}
		config.Series[skey] = domain.SeriesInfo{FileToUpload: file, SeriesID: seriesID}
	}
	for skey := range config.CalCms.SeriesIDs {
		if _, ok := config.CalCms.SeriesFiles[skey]; !ok {
			return fmt.Errorf("SERIES_IDS contains %q without a matching file", skey)
		}
	}
	return nil
}

// loadConfig loads the configuration from file. Returns an error if loading fails
func loadConfig(file string) error {
	if err := godotenv.Load(file); err != nil {
		return err
	}
	return nil
}
