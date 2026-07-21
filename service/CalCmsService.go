// package service implements the services and their business logic that provide the main part of the program
package service

import (
	"encoding/json"
	"fmt"
	"html"
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
	QueryEvents(time.Time, time.Time) ([]domain.CalCMSEvent, error)
	Login(string, string) error
	HasRecording(int, int) (bool, error)
	UploadFile(int, int, string) error
}

var (
	activeRecordingRow = regexp.MustCompile(`(?i)<tr[^>]*class\s*=\s*["'][^"']*\bactive\b[^"']*["']`)
	uploadErrorRow     = regexp.MustCompile(`(?is)<div\s+class=["']error["']\s+id=["']message["'][^>]*>(.*?)</div>`)
	htmlTag            = regexp.MustCompile(`(?s)<[^>]+>`)
)

const maxResponseSize int64 = 4 << 20

// The calCms service handles all the communication with calCms and the necessary data transformation
type DefaultCalCmsService struct {
	Cfg    *config.AppConfig
	client *http.Client
}

// NewCalCmsService creates a new calCms service and injects its dependencies
func NewCalCmsService(cfg *config.AppConfig) *DefaultCalCmsService {
	return NewCalCmsServiceWithClient(cfg, nil)
}

// NewCalCmsServiceWithClient creates a service with an injected HTTP client.
func NewCalCmsServiceWithClient(cfg *config.AppConfig, client *http.Client) *DefaultCalCmsService {
	if client == nil {
		timeout := cfg.CalCms.RequestTimeout
		if timeout <= 0 {
			timeout = 5 * time.Minute
		}
		client = &http.Client{Timeout: timeout}
	}
	if client.Jar == nil {
		client.Jar, _ = cookiejar.New(nil)
	}
	return &DefaultCalCmsService{Cfg: cfg, client: client}
}

// getCalCmsEventData retrieves the event information from calCms
func (s *DefaultCalCmsService) getCalCmsEventData(startDate, endDate time.Time) ([]byte, error) {
	//API doc: https://github.com/rapilodev/racalmas/blob/master/docs/event-api.md
	//URL: https://programm.coloradio.org/agenda/events.cgi?from_date=2024-10-04&from_time=00:00&till_date=2024-10-05&till_time=00:00&template=event.json-p
	calUrl, err := url.Parse(s.Cfg.CalCms.CmsHost)
	if err != nil {
		return nil, fmt.Errorf("parse calCMS URL: %w", err)
	}
	calUrl = calUrl.JoinPath("agenda/events.cgi")
	query := url.Values{}
	query.Add("from_date", startDate.Format("2006-01-02"))
	query.Add("from_time", "00:00")
	query.Add("till_date", endDate.Format("2006-01-02"))
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
	eventData, err := readLimitedBody(resp.Body, maxResponseSize)
	if err != nil {
		return nil, fmt.Errorf("read calCMS response: %w", err)
	}
	return eventData, nil
}

// QueryEvents retrieves the events in the inclusive date range from calCMS.
func (s *DefaultCalCmsService) QueryEvents(startDate, endDate time.Time) ([]domain.CalCMSEvent, error) {
	data, err := s.getCalCmsEventData(startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("get event data: %w", err)
	}
	var events domain.CalCMSEventResponse
	if err := json.Unmarshal(data, &events); err != nil {
		return nil, fmt.Errorf("decode calCMS response: %w", err)
	}
	return events.Events, nil
}

func readLimitedBody(body io.Reader, maximum int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("response exceeds %d bytes", maximum)
	}
	return data, nil
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
	if resp.Request == nil || resp.Request.Method != http.MethodPost || !sameEndpoint(resp.Request.URL, calUrl) {
		return fmt.Errorf("calCMS login was redirected away from the login endpoint")
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
	body, err := readLimitedBody(resp.Body, maxResponseSize)
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
	fileInfo, err := file.Stat()
	if err != nil {
		file.Close()
		return fmt.Errorf("inspect upload file: %w", err)
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
	contentLength, err := multipartUploadLength(multipartWriter.Boundary(), fileInfo.Size(), s.Cfg.CalCms.ProjectID, s.Cfg.CalCms.StudioID, eventId, seriesId, uploadFile)
	if err != nil {
		file.Close()
		reader.Close()
		writer.Close()
		return fmt.Errorf("calculate upload size: %w", err)
	}
	req.ContentLength = contentLength
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
	if resp.Request == nil || resp.Request.Method != http.MethodPost || !sameEndpoint(resp.Request.URL, calUrl) {
		redirectPath := "unknown endpoint"
		if resp.Request != nil && resp.Request.URL != nil {
			redirectPath = resp.Request.URL.Path
		}
		return fmt.Errorf("calCMS upload was redirected to %q", redirectPath)
	}
	responseBody, err := readLimitedBody(resp.Body, maxResponseSize)
	if err != nil {
		return fmt.Errorf("read calCMS upload response: %w", err)
	}
	if match := uploadErrorRow.FindSubmatch(responseBody); len(match) == 2 {
		message := strings.TrimSpace(html.UnescapeString(htmlTag.ReplaceAllString(string(match[1]), " ")))
		if message == "" {
			message = "unknown server-side error"
		}
		return fmt.Errorf("calCMS rejected upload: %s", message)
	}
	return nil
}

type countingWriter struct {
	n int64
}

func (w *countingWriter) Write(data []byte) (int, error) {
	w.n += int64(len(data))
	return len(data), nil
}

func multipartUploadLength(boundary string, fileSize int64, projectID, studioID, eventID, seriesID int, uploadFile string) (int64, error) {
	counter := &countingWriter{}
	w := multipart.NewWriter(counter)
	if err := w.SetBoundary(boundary); err != nil {
		return 0, err
	}
	if err := writeMultipartUpload(w, strings.NewReader(""), projectID, studioID, eventID, seriesID, uploadFile); err != nil {
		return 0, err
	}
	return counter.n + fileSize, nil
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
