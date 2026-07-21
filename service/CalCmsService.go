// package service implements the services and their business logic that provide the main part of the program
package service

import (
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/johannes-kuhfuss/calcmsfeeder/config"
	"github.com/johannes-kuhfuss/calcmsfeeder/domain"
)

type CalCmsService interface {
	QueryEventsFromCalCms() error
	FilterEventsFromCalCms() error
	Login(string, string) error
	HasRecording(int, int) (bool, error)
	UploadFile(int, int, string) error
}

var activeRecordingRow = regexp.MustCompile(`(?i)<tr[^>]*class\s*=\s*["'][^"']*\bactive\b[^"']*["']`)

// The calCms service handles all the communication with calCms and the necessary data transformation
type DefaultCalCmsService struct {
	Cfg    *config.AppConfig
	client *http.Client
	events domain.CalCmsPgmData
}

// NewCalCmsService creates a new calCms service and injects its dependencies
func NewCalCmsService(cfg *config.AppConfig) *DefaultCalCmsService {
	return NewCalCmsServiceWithClient(cfg, nil)
}

// NewCalCmsServiceWithClient creates a service with an injected HTTP client.
func NewCalCmsServiceWithClient(cfg *config.AppConfig, client *http.Client) *DefaultCalCmsService {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if client.Jar == nil {
		client.Jar, _ = cookiejar.New(nil)
	}
	return &DefaultCalCmsService{Cfg: cfg, client: client}
}

// getCalCmsEventData retrieves the event information from calCms
func (s *DefaultCalCmsService) getCalCmsEventData() ([]byte, error) {
	//API doc: https://github.com/rapilodev/racalmas/blob/master/docs/event-api.md
	//URL: https://programm.coloradio.org/agenda/events.cgi?from_date=2024-10-04&from_time=00:00&till_date=2024-10-05&till_time=00:00&template=event.json-p
	calUrl, err := url.Parse(s.Cfg.CalCms.CmsHost)
	if err != nil {
		return nil, fmt.Errorf("parse calCMS URL: %w", err)
	}
	calUrl = calUrl.JoinPath("agenda/events.cgi")
	query := url.Values{}
	query.Add("from_date", s.Cfg.RunTime.StartDate.Format("2006-01-02"))
	query.Add("from_time", "00:00")
	query.Add("till_date", s.Cfg.RunTime.EndDate.Format("2006-01-02"))
	query.Add("till_time", "23:55")
	query.Add("template", s.Cfg.CalCms.Template)
	calUrl.RawQuery = query.Encode()
	req, err := http.NewRequest(http.MethodGet, calUrl.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build calCMS HTTP request: %w", err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute calCMS HTTP request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("calCMS returned HTTP %d", resp.StatusCode)
	}
	eventData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read calCMS response: %w", err)
	}
	return eventData, nil
}

// QueryEventsFromCalCms retries all events from calCms and stores the resulting data for further access
func (s *DefaultCalCmsService) QueryEventsFromCalCms() error {
	data, err := s.getCalCmsEventData()
	if err != nil {
		return fmt.Errorf("get event data: %w", err)
	}
	var events domain.CalCmsPgmData
	if err := json.Unmarshal(data, &events); err != nil {
		return fmt.Errorf("decode calCMS response: %w", err)
	}
	s.events = events
	return nil
}

// FilterEventsFromCalCms extracts all events that match the configured series and stores them in the runtime configuration
func (s *DefaultCalCmsService) FilterEventsFromCalCms() error {
	for key, entry := range s.Cfg.RunTime.Series {
		entry.EventIds = nil
		s.Cfg.RunTime.Series[key] = entry
	}
	for _, event := range s.events.Events {
		if entry, ok := s.Cfg.RunTime.Series[event.Skey]; ok {
			entry.EventIds = append(entry.EventIds, event.EventID)
			s.Cfg.RunTime.Series[event.Skey] = entry
		}
	}
	return nil
}

// Login logs into calCms and stores the session cookie for authentication of the upload request
func (s *DefaultCalCmsService) Login(user, password string) error {
	// POST to https://programm.coloradio.org/agenda/planung/calendar.cgi
	// Content-Type application/x-www-form-urlencoded
	// Form data: "user", "password", "authAction:login", "uri:"
	// Return session cookie
	calUrl, err := url.Parse(s.Cfg.CalCms.CmsHost)
	if err != nil {
		return fmt.Errorf("parse calCMS URL: %w", err)
	}
	calUrl = calUrl.JoinPath("agenda/planung/calendar.cgi")
	form := url.Values{}
	form.Add("user", user)
	form.Add("password", password)
	form.Add("authAction", "login")
	form.Add("uri", "")
	req, err := http.NewRequest(http.MethodPost, calUrl.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build calCMS HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("execute calCMS login request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("calCMS login returned HTTP %d", resp.StatusCode)
	}
	uploadURL := calUrl.ResolveReference(&url.URL{Path: "audio-recordings.cgi"})
	if len(s.client.Jar.Cookies(uploadURL)) == 0 {
		return fmt.Errorf("calCMS login returned no session cookie")
	}
	return nil
}

// HasRecording reports whether calCMS already has an active recording for an event.
func (s *DefaultCalCmsService) HasRecording(eventID, seriesID int) (bool, error) {
	calURL, err := url.Parse(s.Cfg.CalCms.CmsHost)
	if err != nil {
		return false, fmt.Errorf("parse calCMS URL: %w", err)
	}
	calURL = calURL.JoinPath("agenda/planung/audio-recordings.cgi")
	query := url.Values{}
	query.Set("project_id", strconv.Itoa(s.Cfg.CalCms.ProjectID))
	query.Set("studio_id", strconv.Itoa(s.Cfg.CalCms.StudioID))
	query.Set("series_id", strconv.Itoa(seriesID))
	query.Set("event_id", strconv.Itoa(eventID))
	calURL.RawQuery = query.Encode()
	req, err := http.NewRequest(http.MethodGet, calURL.String(), nil)
	if err != nil {
		return false, fmt.Errorf("build recording check request: %w", err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("execute recording check request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("calCMS recording check returned HTTP %d", resp.StatusCode)
	}
	if resp.Request != nil && !sameEndpoint(resp.Request.URL, calURL) {
		return false, fmt.Errorf("calCMS recording check was redirected to %q", resp.Request.URL.Path)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return false, fmt.Errorf("read recording check response: %w", err)
	}
	return activeRecordingRow.Match(body), nil
}

func sameEndpoint(left, right *url.URL) bool {
	if left == nil || right == nil {
		return false
	}
	leftPath := pathpkg.Clean("/" + strings.TrimPrefix(left.Path, "/"))
	rightPath := pathpkg.Clean("/" + strings.TrimPrefix(right.Path, "/"))
	return strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host) && leftPath == rightPath
}

// UploadFile uploads a specified file to a specified event in a series
func (s *DefaultCalCmsService) UploadFile(eventId int, seriesId int, uploadFile string) error {
	// Upload Page: https://programm.coloradio.org/agenda/planung/audio-recordings.cgi?project_id=1&studio_id=1&series_id=395&event_id=37901
	// POST request
	// Cookie set sessionID
	// Content-Type multipart/form-data (boundary)
	calUrl, err := url.Parse(s.Cfg.CalCms.CmsHost)
	if err != nil {
		return fmt.Errorf("parse calCMS URL: %w", err)
	}
	calUrl = calUrl.JoinPath("agenda/planung/audio-recordings.cgi")
	file, err := os.Open(uploadFile)
	if err != nil {
		return fmt.Errorf("open upload file: %w", err)
	}
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	req, err := http.NewRequest(http.MethodPost, calUrl.String(), reader)
	if err != nil {
		file.Close()
		reader.Close()
		writer.Close()
		return fmt.Errorf("build calCMS HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	writeDone := make(chan error, 1)
	go func() {
		defer file.Close()
		writeErr := writeMultipartUpload(multipartWriter, file, s.Cfg.CalCms.ProjectID, s.Cfg.CalCms.StudioID, eventId, seriesId, uploadFile)
		if writeErr != nil {
			writer.CloseWithError(writeErr)
		} else {
			writer.Close()
		}
		writeDone <- writeErr
	}()
	resp, err := s.client.Do(req)
	if err != nil {
		reader.CloseWithError(err)
		<-writeDone
		return fmt.Errorf("execute calCMS upload request: %w", err)
	}
	defer resp.Body.Close()
	if err := <-writeDone; err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("calCMS upload returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func writeMultipartUpload(w *multipart.Writer, file io.Reader, projectID, studioID, eventID, seriesID int, uploadFile string) error {
	fields := map[string]string{
		"project_id": strconv.Itoa(projectID),
		"studio_id":  strconv.Itoa(studioID),
		"series_id":  strconv.Itoa(seriesID),
		"event_id":   strconv.Itoa(eventID),
		"action":     "upload",
	}
	for name, value := range fields {
		if err := w.WriteField(name, value); err != nil {
			return fmt.Errorf("write multipart field %q: %w", name, err)
		}
	}
	part, err := w.CreateFormFile("upload", filepath.Base(uploadFile))
	if err != nil {
		return fmt.Errorf("create multipart file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("write multipart file: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close multipart writer: %w", err)
	}
	return nil
}
