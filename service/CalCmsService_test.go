package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johannes-kuhfuss/calcmsfeeder/config"
	"github.com/johannes-kuhfuss/calcmsfeeder/domain"
)

func serviceTestConfig(host string) *config.AppConfig {
	cfg := &config.AppConfig{}
	cfg.CalCms.CmsHost = host
	cfg.CalCms.Template = "events.json"
	cfg.CalCms.ProjectID = 3
	cfg.CalCms.StudioID = 4
	cfg.RunTime.StartDate = time.Date(2026, time.July, 21, 0, 0, 0, 0, time.UTC)
	cfg.RunTime.EndDate = time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC)
	cfg.RunTime.Series = map[string]domain.SeriesInfo{"show": {SeriesId: 99}}
	return cfg
}

func TestQueryAndFilterEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/agenda/events.cgi" {
			t.Errorf("path = %q", r.URL.Path)
		}
		wantQuery := map[string]string{
			"from_date": "2026-07-21",
			"from_time": "00:00",
			"till_date": "2026-07-27",
			"till_time": "23:55",
			"template":  "events.json",
		}
		for key, want := range wantQuery {
			if got := r.URL.Query().Get(key); got != want {
				t.Errorf("query %s = %q, want %q", key, got, want)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"events":[{"event_id":42,"skey":"show"},{"event_id":7,"skey":"other"}]}`)
	}))
	defer server.Close()

	cfg := serviceTestConfig(server.URL)
	svc := NewCalCmsServiceWithClient(cfg, server.Client())
	if err := svc.QueryEventsFromCalCms(); err != nil {
		t.Fatal(err)
	}
	if err := svc.FilterEventsFromCalCms(); err != nil {
		t.Fatal(err)
	}
	if got := cfg.RunTime.Series["show"].EventIds; len(got) != 1 || got[0] != 42 {
		t.Fatalf("event IDs = %v, want [42]", got)
	}
	if err := svc.FilterEventsFromCalCms(); err != nil {
		t.Fatal(err)
	}
	if got := cfg.RunTime.Series["show"].EventIds; len(got) != 1 {
		t.Fatalf("second filter duplicated events: %v", got)
	}
}

func TestMalformedJSONDoesNotPoisonSubsequentQuery(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			io.WriteString(w, `{not-json`)
			return
		}
		io.WriteString(w, `{"events":[]}`)
	}))
	defer server.Close()
	svc := NewCalCmsServiceWithClient(serviceTestConfig(server.URL), server.Client())
	if err := svc.QueryEventsFromCalCms(); err == nil {
		t.Fatal("first query unexpectedly succeeded")
	}
	if err := svc.QueryEventsFromCalCms(); err != nil {
		t.Fatalf("second query failed: %v", err)
	}
}

func TestLoginAndUploadProtocol(t *testing.T) {
	uploadFile := t.TempDir() + "/show.stream"
	if err := os.WriteFile(uploadFile, []byte("audio stream"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/agenda/planung/calendar.cgi":
			if r.Method != http.MethodPost || r.URL.RawQuery != "" {
				t.Errorf("login request = %s %s", r.Method, r.URL.String())
			}
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse login form: %v", err)
			}
			if r.Form.Get("user") != "alice" || r.Form.Get("password") != "s3cret" || r.Form.Get("authAction") != "login" {
				t.Errorf("unexpected login form: %v", r.Form)
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "csrf", Value: "xyz", Path: "/"})
		case "/agenda/planung/audio-recordings.cgi":
			cookie, err := r.Cookie("session")
			if err != nil || cookie.Value != "abc" {
				t.Errorf("session cookie = %v, %v", cookie, err)
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse upload: %v", err)
				return
			}
			wantFields := map[string]string{"project_id": "3", "studio_id": "4", "series_id": "99", "event_id": "42", "action": "upload"}
			for key, want := range wantFields {
				if got := r.FormValue(key); got != want {
					t.Errorf("field %s = %q, want %q", key, got, want)
				}
			}
			if got := r.FormValue("studio_id=1"); got != "" {
				t.Errorf("malformed studio field still present: %q", got)
			}
			file, _, err := r.FormFile("upload")
			if err != nil {
				t.Errorf("upload file: %v", err)
				return
			}
			defer file.Close()
			contents, _ := io.ReadAll(file)
			if string(contents) != "audio stream" {
				t.Errorf("upload contents = %q", contents)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	svc := NewCalCmsServiceWithClient(serviceTestConfig(server.URL), server.Client())
	if err := svc.Login("alice", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if err := svc.UploadFile(42, 99, uploadFile); err != nil {
		t.Fatal(err)
	}
}

func TestLoginRequiresSessionCookie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer server.Close()
	svc := NewCalCmsServiceWithClient(serviceTestConfig(server.URL), server.Client())
	if err := svc.Login("alice", "secret"); err == nil || !strings.Contains(err.Error(), "no session cookie") {
		t.Fatalf("Login() error = %v", err)
	}
}

type closeTrackingBody struct {
	io.Reader
	closed atomic.Bool
}

func (b *closeTrackingBody) Close() error {
	b.closed.Store(true)
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNonOKResponseBodyIsClosed(t *testing.T) {
	body := &closeTrackingBody{Reader: strings.NewReader("failure")}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadGateway, Body: body, Header: make(http.Header)}, nil
	})}
	svc := NewCalCmsServiceWithClient(serviceTestConfig("http://calendar.example"), client)
	err := svc.QueryEventsFromCalCms()
	if err == nil || !strings.Contains(err.Error(), strconv.Itoa(http.StatusBadGateway)) {
		t.Fatalf("QueryEventsFromCalCms() error = %v", err)
	}
	if !body.closed.Load() {
		t.Fatal("response body was not closed")
	}
}
