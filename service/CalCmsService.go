// package service implements the services and their business logic that provide the main part of the program
package service

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
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
	calUrl, err := url.Parse(s.Cfg.CalCms.CmsUrl)
	if err != nil {
		logger.Error("Cannot parse calCMS Url", err)
		return nil, err
	}
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
