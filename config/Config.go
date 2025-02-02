// package config defines the program's configuration including the defaults
package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
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
		DefaultDurationInDays int               `envconfig:"DEFAULT_DURATION_IN_DAYS" default:"7"`
		SeriesFiles           map[string]string `envconfig:"SERIES_FILES"`
		SeriesIds             map[string]int    `envconfig:"SERIES_IDS"`
	}
	RunTime struct {
		StartDate time.Time
		EndDate   time.Time
		Series    map[string]domain.SeriesInfo
	}
}

var (
	EnvFile = ".env"
)

// InitConfig initializes the configuration and sets the defaults
func InitConfig(file string, config *AppConfig) error {
	if err := loadConfig(file); err != nil {
		return fmt.Errorf("could not load configuration from file: %v", err.Error())
	}
	if err := envconfig.Process("", config); err != nil {
		return fmt.Errorf("could not initialize configuration: %v", err.Error())
	}
	setDefaults(config)
	return nil
}

// cleanFilePath does sanity-checking on file paths
func checkFilePath(filePath *string) {
	if *filePath != "" {
		*filePath = filepath.Clean(*filePath)
		_, err := os.Stat(*filePath)
		if err == nil {
			*filePath, err = filepath.EvalSymlinks(*filePath)
			if err != nil {
				log.Printf("error checking file %v", *filePath)
			}
		}
	}
}

// setDefaults sets defaults for some configurations items
func setDefaults(config *AppConfig) {
	config.RunTime.Series = make(map[string]domain.SeriesInfo)
	for skey, file := range config.CalCms.SeriesFiles {
		checkFilePath(&file)
		seriesid := config.CalCms.SeriesIds[skey]
		config.RunTime.Series[skey] = domain.SeriesInfo{FileToUpload: file, SeriesId: seriesid}
	}
}

// loadConfig loads the configuration from file. Returns an error if loading fails
func loadConfig(file string) error {
	if err := godotenv.Load(file); err != nil {
		return err
	}
	return nil
}
