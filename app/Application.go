// package app ties together all bits and pieces to start the program
package app

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/johannes-kuhfuss/calcmsfeeder/config"
	"github.com/johannes-kuhfuss/calcmsfeeder/service"
)

var (
	cfg           config.AppConfig
	calCmsService service.CalCmsService
	overwrite     bool
)

const (
	dateFormat = "2006-01-02"
)

// RunApp orchestrates the application
func RunApp() error {
	getCmdLine()
	if err := config.InitConfig(config.EnvFile, &cfg); err != nil {
		return err
	}
	wireApp()
	if err := getUserInput(); err != nil {
		return err
	}
	if err := queryCalCmsEvents(); err != nil {
		return err
	}
	confirmed, err := showStatusAndConfirm()
	if err != nil {
		return err
	}
	if confirmed {
		if err := uploadFilesToCalCms(); err != nil {
			return err
		}
	}
	return nil
}

// getCmdLine checks the command line arguments
func getCmdLine() {
	flag.StringVar(&config.EnvFile, "config.file", ".env", "Specify location of config file. Default is .env")
	flag.BoolVar(&overwrite, "overwrite", false, "Replace an active recording when an upload is already present")
	flag.Parse()
}

// wireApp initializes the services in the right order and injects the dependencies
func wireApp() {
	calCmsService = service.NewCalCmsService(&cfg)
}

// getUserInput retrieves the dates to work on
func getUserInput() error {
	if err := readStartDate(); err != nil {
		return err
	}
	return readDuration()
}

// readStartDate prompts the user for the start date
func readStartDate() error {
	var (
		dateOk    bool = false
		startDate string
		today     time.Time
	)
	scanner := bufio.NewScanner(os.Stdin)
	today = time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.Local)
	for !dateOk {
		fmt.Print("Enter start date as YYYY-MM-DD (or leave empty for today): ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("read start date: %w", err)
			}
			return fmt.Errorf("read start date: input closed")
		}
		startDate = scanner.Text()
		if startDate == "" {
			cfg.RunTime.StartDate = today
			dateOk = true
			return nil
		}
		d, err := time.ParseInLocation(dateFormat, startDate, time.Local)
		if err != nil {
			fmt.Println("Start Date must be entered as YYYY-MM-DD.")
		} else {
			if d.Before(today) {
				fmt.Println("Start Date must be today or later")
			} else {
				cfg.RunTime.StartDate = d
				dateOk = true
				return nil
			}
		}
	}
	return nil
}

// readDuration prompts the user for the duration in number of days
func readDuration() error {
	var (
		durOk    bool = false
		duration string
	)
	scanner := bufio.NewScanner(os.Stdin)
	for !durOk {
		fmt.Printf("Enter processing duration in days (1 .. %v, or leave empty for default = %v): ", cfg.CalCms.MaxDurationInDays, cfg.CalCms.DefaultDurationInDays)
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("read duration: %w", err)
			}
			return fmt.Errorf("read duration: input closed")
		}
		duration = scanner.Text()
		if duration == "" {
			cfg.RunTime.EndDate = endDateForDuration(cfg.RunTime.StartDate, cfg.CalCms.DefaultDurationInDays)
			return nil
		}
		d, err := strconv.Atoi(duration)
		if err != nil {
			fmt.Println("Duration must be a numeric value.")
		} else {
			if (d < 1) || (d > cfg.CalCms.MaxDurationInDays) {
				fmt.Printf("Duration must be between 1 and %v.\r\n", cfg.CalCms.MaxDurationInDays)
			} else {
				cfg.RunTime.EndDate = endDateForDuration(cfg.RunTime.StartDate, d)
				return nil
			}
		}
	}
	return nil
}

func endDateForDuration(start time.Time, days int) time.Time {
	return start.AddDate(0, 0, days-1)
}

func showStatusAndConfirm() (bool, error) {
	fmt.Printf("Using start date %v\r\n", cfg.RunTime.StartDate.Format(dateFormat))
	fmt.Printf("Using end date %v\r\n", cfg.RunTime.EndDate.Format(dateFormat))
	fmt.Printf("Overwrite existing recordings: %v\r\n", overwrite)
	for _, entry := range sortedSeriesKeys() {
		data := cfg.RunTime.Series[entry]
		fmt.Printf("For \"%v\" found %v entries. Will upload file \"%v\". (IDs: %v)\r\n", entry, len(data.EventIds), data.FileToUpload, data.EventIds)
	}
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("Confirm with \"y\" to continue: ")
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, fmt.Errorf("read confirmation: %w", err)
		}
		return false, fmt.Errorf("read confirmation: input closed")
	}
	decision := strings.TrimSpace(scanner.Text())
	if !strings.EqualFold(decision, "y") {
		fmt.Print("Aborting...")
		return false, nil
	}
	return true, nil
}

// queryCalCmsEvents retrieves all events for the date range from calCms and filters them to match the series configuration
func queryCalCmsEvents() error {
	if err := calCmsService.QueryEventsFromCalCms(); err != nil {
		return fmt.Errorf("query events from calCms: %w", err)
	}
	if err := calCmsService.FilterEventsFromCalCms(); err != nil {
		return fmt.Errorf("filter events from calCms: %w", err)
	}
	return nil
}

// uploadFilesToCalCms uploads the configured file to calCms for each matching event
func uploadFilesToCalCms() error {
	if eventCount() == 0 {
		fmt.Println("No matching events; nothing to upload.")
		return nil
	}
	if err := calCmsService.Login(cfg.CalCms.CmsUser, cfg.CalCms.CmsPass); err != nil {
		return fmt.Errorf("log in to calCms: %w", err)
	}
	for _, entry := range sortedSeriesKeys() {
		data := cfg.RunTime.Series[entry]
		if len(data.EventIds) == 0 {
			continue
		}
		fmt.Printf("Uploading files for \"%v\".\r\n", entry)
		for _, evId := range data.EventIds {
			hasRecording, err := calCmsService.HasRecording(evId, data.SeriesId)
			if err != nil {
				return fmt.Errorf("check existing recording for event %d: %w", evId, err)
			}
			if hasRecording && !overwrite {
				fmt.Printf("Skipping event %d: an active recording is already present (use -overwrite to replace it).\r\n", evId)
				continue
			}
			if hasRecording {
				fmt.Printf("Overwriting active recording for event %d.\r\n", evId)
			}
			if err := calCmsService.UploadFile(evId, data.SeriesId, data.FileToUpload); err != nil {
				return fmt.Errorf("upload %q for event %d: %w", data.FileToUpload, evId, err)
			}
		}
	}
	return nil
}

func sortedSeriesKeys() []string {
	keys := make([]string, 0, len(cfg.RunTime.Series))
	for key := range cfg.RunTime.Series {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func eventCount() int {
	count := 0
	for _, data := range cfg.RunTime.Series {
		count += len(data.EventIds)
	}
	return count
}
