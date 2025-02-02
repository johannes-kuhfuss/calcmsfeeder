// package service implements the services and their business logic that provide the main part of the program
package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/johannes-kuhfuss/calcmsfeeder/config"
	"github.com/johannes-kuhfuss/calcmsfeeder/domain"
	"github.com/johannes-kuhfuss/services_utils/logger"
)

type CalCmsService interface {
	Query() error
}

// The calCms service handles all the communication with calCms and the necessary data transformation
type DefaultCalCmsService struct {
	Cfg *config.AppConfig
}

var (
	httpCalTr     http.Transport
	httpCalClient http.Client
	CalCmsPgm     struct {
		sync.RWMutex
		data domain.CalCmsPgmData
	}
	sessionCookie *http.Cookie
)

// InitHttpCalClient sets the defaukt values for the http client used to query calCms
func InitHttpCalClient() {
	httpCalTr = http.Transport{
		DisableKeepAlives:  false,
		DisableCompression: false,
		MaxIdleConns:       0,
		IdleConnTimeout:    0,
	}
	httpCalClient = http.Client{
		Transport: &httpCalTr,
		Timeout:   5 * time.Second,
	}
}

// NewCalCmsService creates a new calCms service and injects its dependencies
func NewCalCmsService(cfg *config.AppConfig) DefaultCalCmsService {
	InitHttpCalClient()
	return DefaultCalCmsService{
		Cfg: cfg,
	}
}

// insertData inserts new calCms data in a thread-safe manner
func (s DefaultCalCmsService) insertData(data domain.CalCmsPgmData) {
	CalCmsPgm.Lock()
	CalCmsPgm.data = data
	CalCmsPgm.Unlock()
}

// getCalCmsEventData retrieves the event information from calCms
func (s DefaultCalCmsService) getCalCmsEventData() (eventData []byte, e error) {
	//API doc: https://github.com/rapilodev/racalmas/blob/master/docs/event-api.md
	//URL: https://programm.coloradio.org/agenda/events.cgi?from_date=2024-10-04&from_time=00:00&till_date=2024-10-05&till_time=00:00&template=event.json-p
	calUrl, err := url.Parse(s.Cfg.CalCms.CmsHost)
	if err != nil {
		logger.Error("Cannot parse calCMS Url", err)
		return nil, err
	}
	calUrl = calUrl.JoinPath("agenda/events.cgi")
	query := url.Values{}
	query.Add("from_date", s.Cfg.RunTime.StartDate.Format("2006-01-02"))
	query.Add("from_time", "00:00")
	query.Add("till_date", s.Cfg.RunTime.EndDate.Format("2006-01-02"))
	query.Add("till_time", "23:55")
	query.Add("template", s.Cfg.CalCms.Template)
	calUrl.RawQuery = query.Encode()
	req, err := http.NewRequest("GET", calUrl.String(), nil)
	if err != nil {
		logger.Error("Cannot build calCMS http request", err)
		return nil, err
	}
	resp, err := httpCalClient.Do(req)
	if err != nil {
		logger.Error("Cannot execute calCMS http request", err)
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		err := errors.New(resp.Status)
		logger.Errorf("Received status code %v from calCMS. %v", resp.StatusCode, err)
		return nil, err
	}
	defer resp.Body.Close()
	eventData, err = io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Cannot read response data from calCMS", err)
		return nil, err
	}
	return eventData, nil
}

func (s DefaultCalCmsService) QueryEventsFromCalCms() error {
	data, err := s.getCalCmsEventData()
	if err != nil {
		logger.Error("error getting data from calCms", err)
		return err
	}
	CalCmsPgm.Lock()
	if err := json.Unmarshal(data, &CalCmsPgm.data); err != nil {
		logger.Error("Cannot convert calCMS response data to Json", err)
		return err
	}
	CalCmsPgm.Unlock()
	return nil
}

func (s DefaultCalCmsService) FilterEventsFromCalCms() error {
	for _, event := range CalCmsPgm.data.Events {
		if entry, ok := s.Cfg.RunTime.Series[event.Skey]; ok {
			//logger.Infof("Event: %v, Event Id: %v, File: %v", event.Skey, event.EventID, s.Cfg.RunTime.Series[event.Skey].FileToUpload)
			entry.EventIds = append(entry.EventIds, event.EventID)
			//entry.SeriesId =
			s.Cfg.RunTime.Series[event.Skey] = entry
		}
	}
	return nil
}

func (s DefaultCalCmsService) Login(user string, password string) error {
	// POST to https://programm.coloradio.org/agenda/planung/calendar.cgi
	// Content-Type application/x-www-form-urlencoded
	// Form data: "user", "password", "authAction:login", "uri:"
	// Return session cookie
	calUrl, err := url.Parse(s.Cfg.CalCms.CmsHost)
	if err != nil {
		logger.Error("Cannot parse calCMS Url", err)
		return err
	}
	calUrl = calUrl.JoinPath("agenda/planung/calendar.cgi")
	query := url.Values{}
	query.Add("user", user)
	query.Add("password", password)
	query.Add("authAction", "login")
	query.Add("uri", "")
	calUrl.RawQuery = query.Encode()
	req, err := http.NewRequest("POST", calUrl.String(), nil)
	if err != nil {
		logger.Error("Cannot build calCMS http request", err)
		return err
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpCalClient.Do(req)
	if err != nil {
		logger.Error("Cannot execute calCMS http request", err)
		return err
	}
	if resp.StatusCode != http.StatusOK {
		err := errors.New(resp.Status)
		logger.Errorf("Received status code %v from calCMS. %v", resp.StatusCode, err)
		return err
	}
	defer resp.Body.Close()
	cookies := resp.Cookies()
	if len(cookies) != 1 {
		err := errors.New("could not receive cookie")
		logger.Error("Cannot authenticate", err)
		return err
	}
	sessionCookie = cookies[0]
	return nil
}

func (s DefaultCalCmsService) UploadFile(eventId int, seriesId int, uploadFile string) error {
	// Upload Page: https://programm.coloradio.org/agenda/planung/audio-recordings.cgi?project_id=1&studio_id=1&series_id=395&event_id=37901
	// POST request
	// Cookie set sessionID
	// Content-Type multipart/form-data (boundary)
	var (
		body bytes.Buffer
	)
	calUrl, err := url.Parse(s.Cfg.CalCms.CmsHost)
	if err != nil {
		logger.Error("Cannot parse calCMS Url", err)
		return err
	}
	calUrl = calUrl.JoinPath("agenda/planung/audio-recordings.cgi")
	file, err := os.Open(uploadFile)
	if err != nil {
		logger.Error("Cannot open upload file", err)
		return err
	}
	defer file.Close()
	fileContents, err := io.ReadAll(file)
	if err != nil {
		logger.Error("Cannot read upload file", err)
		return err
	}
	w := multipart.NewWriter(&body)
	defer w.Close()
	w.WriteField("project_id", "1")
	w.WriteField("studio_id=1", "1")
	w.WriteField("series_id", strconv.Itoa(seriesId))
	w.WriteField("event_id", strconv.Itoa(eventId))
	w.WriteField("action", "upload")
	fw, err := w.CreateFormFile("upload", filepath.Base(uploadFile))
	if err != nil {
		logger.Error("Cannot setup form file", err)
		return err
	}
	fw.Write(fileContents)
	err = w.Close()
	if err != nil {
		logger.Error("Cannot close writer", err)
		return err
	}
	req, err := http.NewRequest("POST", calUrl.String(), &body)
	if err != nil {
		logger.Error("Cannot build calCMS http request", err)
		return err
	}
	req.Header.Add("Content-Type", w.FormDataContentType())
	req.AddCookie(sessionCookie)
	resp, err := httpCalClient.Do(req)
	if err != nil {
		logger.Error("Cannot execute calCMS http request", err)
		return err
	}
	if resp.StatusCode != http.StatusOK {
		err := errors.New(resp.Status)
		logger.Errorf("Received status code %v from calCMS. %v", resp.StatusCode, err)
		return err
	}
	defer resp.Body.Close()
	return nil
}
