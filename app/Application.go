// Package app ties together the program's configuration, user interaction, and services.
package app

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/johannes-kuhfuss/calcmsfeeder/config"
	"github.com/johannes-kuhfuss/calcmsfeeder/domain"
	"github.com/johannes-kuhfuss/calcmsfeeder/service"
)

const dateFormat = "2006-01-02"

// Runner owns the mutable state and dependencies for one application run.
type Runner struct {
	Cfg       config.AppConfig
	Plan      domain.ExecutionPlan
	Service   service.CalCmsService
	Input     *bufio.Scanner
	Output    io.Writer
	Now       func() time.Time
	Overwrite bool
}

// RunApp parses command-line options, loads configuration, and runs the CLI.
func RunApp() error {
	flags := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	envFile := flags.String("config.file", ".env", "Specify location of config file. Default is .env")
	overwrite := flags.Bool("overwrite", false, "Replace an active recording when an upload is already present")
	if err := flags.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	var cfg config.AppConfig
	if err := config.InitConfig(*envFile, &cfg); err != nil {
		return err
	}
	runner := NewRunner(cfg, os.Stdin, os.Stdout, time.Now)
	runner.Overwrite = *overwrite
	runner.Service = service.NewCalCmsService(&runner.Cfg)
	return runner.Run()
}

// NewRunner constructs a runner with a single shared input scanner.
func NewRunner(cfg config.AppConfig, input io.Reader, output io.Writer, now func() time.Time) *Runner {
	if now == nil {
		now = time.Now
	}
	plan := domain.ExecutionPlan{Series: make(map[string]domain.SeriesPlan, len(cfg.Series))}
	for key, series := range cfg.Series {
		plan.Series[key] = domain.SeriesPlan{SeriesInfo: series}
	}
	return &Runner{
		Cfg:    cfg,
		Plan:   plan,
		Input:  bufio.NewScanner(input),
		Output: output,
		Now:    now,
	}
}

// Run executes the interactive application workflow.
func (r *Runner) Run() error {
	if r.Service == nil {
		return fmt.Errorf("calCMS service is nil")
	}
	if err := r.getUserInput(); err != nil {
		return err
	}
	if err := r.queryCalCMSEvents(); err != nil {
		return err
	}
	confirmed, err := r.showStatusAndConfirm()
	if err != nil {
		return err
	}
	if confirmed {
		return r.uploadFilesToCalCMS()
	}
	return nil
}

func (r *Runner) getUserInput() error {
	if err := r.readStartDate(); err != nil {
		return err
	}
	return r.readDuration()
}

func (r *Runner) readLine(context string) (string, error) {
	if !r.Input.Scan() {
		if err := r.Input.Err(); err != nil {
			return "", fmt.Errorf("%s: %w", context, err)
		}
		return "", fmt.Errorf("%s: input closed", context)
	}
	return r.Input.Text(), nil
}

func (r *Runner) readStartDate() error {
	now := r.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for {
		fmt.Fprint(r.Output, "Enter start date as YYYY-MM-DD (or leave empty for today): ")
		startDate, err := r.readLine("read start date")
		if err != nil {
			return err
		}
		if startDate == "" {
			r.Plan.StartDate = today
			return nil
		}
		d, err := time.ParseInLocation(dateFormat, startDate, today.Location())
		if err != nil {
			fmt.Fprintln(r.Output, "Start Date must be entered as YYYY-MM-DD.")
			continue
		}
		if d.Before(today) {
			fmt.Fprintln(r.Output, "Start Date must be today or later")
			continue
		}
		r.Plan.StartDate = d
		return nil
	}
}

func (r *Runner) readDuration() error {
	for {
		fmt.Fprintf(r.Output, "Enter processing duration in days (1 .. %v, or leave empty for default = %v): ", r.Cfg.CalCms.MaxDurationInDays, r.Cfg.CalCms.DefaultDurationInDays)
		duration, err := r.readLine("read duration")
		if err != nil {
			return err
		}
		if duration == "" {
			r.Plan.EndDate = endDateForDuration(r.Plan.StartDate, r.Cfg.CalCms.DefaultDurationInDays)
			return nil
		}
		days, err := strconv.Atoi(duration)
		if err != nil {
			fmt.Fprintln(r.Output, "Duration must be a numeric value.")
			continue
		}
		if days < 1 || days > r.Cfg.CalCms.MaxDurationInDays {
			fmt.Fprintf(r.Output, "Duration must be between 1 and %v.\r\n", r.Cfg.CalCms.MaxDurationInDays)
			continue
		}
		r.Plan.EndDate = endDateForDuration(r.Plan.StartDate, days)
		return nil
	}
}

func endDateForDuration(start time.Time, days int) time.Time {
	return start.AddDate(0, 0, days-1)
}

func (r *Runner) showStatusAndConfirm() (bool, error) {
	fmt.Fprintf(r.Output, "Using start date %v\r\n", r.Plan.StartDate.Format(dateFormat))
	fmt.Fprintf(r.Output, "Using end date %v\r\n", r.Plan.EndDate.Format(dateFormat))
	fmt.Fprintf(r.Output, "Overwrite existing recordings: %v\r\n", r.Overwrite)
	for _, key := range r.sortedSeriesKeys() {
		data := r.Plan.Series[key]
		fmt.Fprintf(r.Output, "For \"%v\" found %v entries. Will upload file \"%v\". (IDs: %v)\r\n", key, len(data.EventIDs), data.FileToUpload, data.EventIDs)
	}
	fmt.Fprint(r.Output, "Confirm with \"y\" to continue: ")
	decision, err := r.readLine("read confirmation")
	if err != nil {
		return false, err
	}
	if !strings.EqualFold(strings.TrimSpace(decision), "y") {
		fmt.Fprint(r.Output, "Aborting...")
		return false, nil
	}
	return true, nil
}

func (r *Runner) queryCalCMSEvents() error {
	events, err := r.Service.QueryEvents(r.Plan.StartDate, r.Plan.EndDate)
	if err != nil {
		return fmt.Errorf("query events from calCMS: %w", err)
	}
	for key, entry := range r.Plan.Series {
		entry.EventIDs = nil
		r.Plan.Series[key] = entry
	}
	for _, event := range events {
		if entry, ok := r.Plan.Series[event.Skey]; ok {
			entry.EventIDs = append(entry.EventIDs, event.EventID)
			r.Plan.Series[event.Skey] = entry
		}
	}
	return nil
}

func (r *Runner) uploadFilesToCalCMS() error {
	if r.eventCount() == 0 {
		fmt.Fprintln(r.Output, "No matching events; nothing to upload.")
		return nil
	}
	if err := r.Service.Login(r.Cfg.CalCms.CmsUser, r.Cfg.CalCms.CmsPass); err != nil {
		return fmt.Errorf("log in to calCMS: %w", err)
	}
	for _, key := range r.sortedSeriesKeys() {
		data := r.Plan.Series[key]
		if len(data.EventIDs) == 0 {
			continue
		}
		fmt.Fprintf(r.Output, "Uploading files for \"%v\".\r\n", key)
		for _, eventID := range data.EventIDs {
			hasRecording, err := r.Service.HasRecording(eventID, data.SeriesID)
			if err != nil {
				return fmt.Errorf("check existing recording for event %d: %w", eventID, err)
			}
			if hasRecording && !r.Overwrite {
				fmt.Fprintf(r.Output, "Skipping event %d: an active recording is already present (use -overwrite to replace it).\r\n", eventID)
				continue
			}
			if hasRecording {
				fmt.Fprintf(r.Output, "Overwriting active recording for event %d.\r\n", eventID)
			}
			if err := r.Service.UploadFile(eventID, data.SeriesID, data.FileToUpload); err != nil {
				return fmt.Errorf("upload %q for event %d: %w", data.FileToUpload, eventID, err)
			}
		}
	}
	return nil
}

func (r *Runner) sortedSeriesKeys() []string {
	keys := make([]string, 0, len(r.Plan.Series))
	for key := range r.Plan.Series {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (r *Runner) eventCount() int {
	count := 0
	for _, data := range r.Plan.Series {
		count += len(data.EventIDs)
	}
	return count
}
