// package app ties together all bits and pieces to start the program
package app

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/johannes-kuhfuss/calcmsfeeder/config"
	"github.com/johannes-kuhfuss/calcmsfeeder/service"
)

var (
	cfg           config.AppConfig
	calCmsService service.DefaultCalCmsService
)

// RunApp orchestrates the application
func RunApp() {
	getCmdLine()
	err := config.InitConfig(config.EnvFile, &cfg)
	if err != nil {
		panic(err)
	}
	wireApp()
	getUserInput()
	queryCalCmsEvents()
	uploadFilesToCalCms()
}

// getCmdLine checks the command line arguments
func getCmdLine() {
	flag.StringVar(&config.EnvFile, "config.file", ".env", "Specify location of config file. Default is .env")
	flag.Parse()
}

// wireApp initializes the services in the right order and injects the dependencies
func wireApp() {
	calCmsService = service.NewCalCmsService(&cfg)
}

// getUserInput retrieves the dates to work on
func getUserInput() {
	readStartDate()
	readDuration()
	fmt.Printf("Using start date %v\r\n", cfg.RunTime.StartDate.Format("2006-01-02"))
	fmt.Printf("Using end date %v\r\n", cfg.RunTime.EndDate.Format("2006-01-02"))
}

// readStartDate prompts the user for the start date
func readStartDate() {
	var (
		dateOk    bool = false
		startDate string
		today     time.Time
	)
	scanner := bufio.NewScanner(os.Stdin)
	today = time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.Local)
	for !dateOk {
		fmt.Print("Enter start date as YYYY-MM-DD (or leave empty for today): ")
		scanner.Scan()
		err := scanner.Err()
		if err != nil {
			log.Fatal(err)
		}
		startDate = scanner.Text()
		if startDate == "" {
			cfg.RunTime.StartDate = today
			dateOk = true
			return
		}
		d, err := time.Parse("2006-01-02", startDate)
		if err != nil {
			fmt.Println("Start Date must be entered as YYYY-MM-DD.")
		} else {
			if d.Before(today) {
				fmt.Println("Start Date must be today or later")
			} else {
				cfg.RunTime.StartDate = d
				dateOk = true
				return
			}
		}
	}
}

// readDuration prompts the user for the duration in number of days
func readDuration() {
	var (
		durOk    bool = false
		duration string
	)
	scanner := bufio.NewScanner(os.Stdin)
	for !durOk {
		fmt.Printf("Enter processing duration in days (1 .. 30, or leave empty for default = %v): ", cfg.CalCms.DefaultDurationInDays)
		scanner.Scan()
		err := scanner.Err()
		if err != nil {
			log.Fatal(err)
		}
		duration = scanner.Text()
		if duration == "" {
			cfg.RunTime.EndDate = cfg.RunTime.StartDate.AddDate(0, 0, cfg.CalCms.DefaultDurationInDays)
			return
		}
		d, err := strconv.Atoi(duration)
		if err != nil {
			fmt.Println("Duration must be a numeric value.")
		} else {
			if (d < 1) || (d > 30) {
				fmt.Println("Duration must be between 1 and 30.")
			} else {
				cfg.RunTime.EndDate = cfg.RunTime.StartDate.AddDate(0, 0, d)
				return
			}
		}
	}
}

// queryCalCmsEvents retrives all events for the date range from calCms and filters them to match the series configuration
func queryCalCmsEvents() {
	err := calCmsService.QueryEventsFromCalCms()
	if err != nil {
		fmt.Printf("Error while querying events from calCms: %v", err.Error())
	}
	err = calCmsService.FilterEventsFromCalCms()
	if err != nil {
		fmt.Printf("Error while filtering events from calCms: %v", err.Error())
	}
}

// uploadFilesToCalCms uploads the configured file to calCms for each matching event
func uploadFilesToCalCms() {
	for entry, data := range cfg.RunTime.Series {
		fmt.Printf("For %v found %v entries. Uploading %v.\r\n", entry, len(data.EventIds), data.FileToUpload)
		err := calCmsService.Login(cfg.CalCms.CmsUser, cfg.CalCms.CmsPass)
		if err != nil {
			fmt.Printf("Error while logging into calCms: %v", err.Error())
			return
		}
		for _, evId := range data.EventIds {
			err := calCmsService.UploadFile(evId, data.SeriesId, data.FileToUpload)
			if err != nil {
				fmt.Printf("Error uploading file to calCms: %v", err.Error())
			}
		}
	}
}
