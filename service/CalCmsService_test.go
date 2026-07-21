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
)

func serviceTestConfig(host string) *config.AppConfig {
	cfg := &config.AppConfig{}
	cfg.CalCms.CmsHost = host
	cfg.CalCms.Template = "events.json"
	cfg.CalCms.ProjectID = 3
	cfg.CalCms.StudioID = 4
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
	events, err := svc.QueryEvents(time.Date(2026, time.July, 21, 0, 0, 0, 0, time.UTC), time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].EventID != 42 || events[0].Skey != "show" {
		t.Fatalf("events = %+v", events)
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
	start, end := time.Now(), time.Now()
	if _, err := svc.QueryEvents(start, end); err == nil {
		t.Fatal("first query unexpectedly succeeded")
	}
	if _, err := svc.QueryEvents(start, end); err != nil {
		t.Fatalf("second query failed: %v", err)
	}
}

func TestQueryRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, strings.Repeat("x", (4<<20)+1))
	}))
	defer server.Close()
	svc := NewCalCmsServiceWithClient(serviceTestConfig(server.URL), server.Client())
	_, err := svc.QueryEvents(time.Now(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("QueryEvents() error = %v, want response size error", err)
	}
}

func TestConfiguredRequestTimeout(t *testing.T) {
	cfg := serviceTestConfig("https://calendar.example")
	cfg.CalCms.RequestTimeout = 17 * time.Second
	svc := NewCalCmsService(cfg)
	if svc.client.Timeout != 17*time.Second {
		t.Fatalf("client timeout = %v, want 17s", svc.client.Timeout)
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
			if r.Method == http.MethodGet {
				io.WriteString(w, `<table><tr class="active"><td>existing.stream</td></tr></table>`)
				return
			}
			if r.ContentLength <= 0 || len(r.TransferEncoding) != 0 {
				t.Errorf("upload framing: Content-Length=%d Transfer-Encoding=%v", r.ContentLength, r.TransferEncoding)
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
			io.WriteString(w, `<!-- <div class="oky" id="message">done!</div> -->`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	svc := NewCalCmsServiceWithClient(serviceTestConfig(server.URL), server.Client())
	if err := svc.Login("alice", "s3cret"); err != nil {
		t.Fatal(err)
	}
	hasRecording, err := svc.HasRecording(42, 99)
	if err != nil {
		t.Fatal(err)
	}
	if !hasRecording {
		t.Fatal("HasRecording() = false, want true")
	}
	if err := svc.UploadFile(42, 99, uploadFile); err != nil {
		t.Fatal(err)
	}
}

func TestUploadSurfacesCalCMSErrorResponse(t *testing.T) {
	uploadFile := t.TempDir() + "/show.stream"
	if err := os.WriteFile(uploadFile, []byte("audio stream"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse upload: %v", err)
		}
		io.WriteString(w, `<!-- <div class="error" id="message">Could not get file handle</div> -->`)
	}))
	defer server.Close()
	svc := NewCalCmsServiceWithClient(serviceTestConfig(server.URL), server.Client())
	err := svc.UploadFile(42, 99, uploadFile)
	if err == nil || !strings.Contains(err.Error(), "Could not get file handle") {
		t.Fatalf("UploadFile() error = %v, want server error", err)
	}
}

func TestHasRecordingRejectsAuthenticationRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			io.WriteString(w, "login")
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	}))
	defer server.Close()
	svc := NewCalCmsServiceWithClient(serviceTestConfig(server.URL), server.Client())
	_, err := svc.HasRecording(42, 99)
	if err == nil || !strings.Contains(err.Error(), "redirected") {
		t.Fatalf("HasRecording() error = %v, want redirect error", err)
	}
}

func TestUploadRejectsAuthenticationRedirect(t *testing.T) {
	uploadFile := t.TempDir() + "/show.stream"
	if err := os.WriteFile(uploadFile, []byte("audio stream"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			io.WriteString(w, "login")
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	}))
	defer server.Close()
	svc := NewCalCmsServiceWithClient(serviceTestConfig(server.URL), server.Client())
	err := svc.UploadFile(42, 99, uploadFile)
	if err == nil || !strings.Contains(err.Error(), "redirected") {
		t.Fatalf("UploadFile() error = %v, want redirect error", err)
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

func TestLoginRejectsAuthenticationRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			http.SetCookie(w, &http.Cookie{Name: "visitor", Value: "1", Path: "/"})
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	}))
	defer server.Close()
	svc := NewCalCmsServiceWithClient(serviceTestConfig(server.URL), server.Client())
	err := svc.Login("alice", "secret")
	if err == nil || !strings.Contains(err.Error(), "redirected") {
		t.Fatalf("Login() error = %v, want redirect error", err)
	}
}

func TestHasRecording(t *testing.T) {
	tests := []struct {
		name string
		html string
		want bool
	}{
		{name: "active recording", html: `<table><tr class="active"><td>recording.stream</td></tr></table>`, want: true},
		{name: "inactive recording only", html: `<table><tr class="inactive"><td>old.stream</td></tr></table>`, want: false},
		{name: "no recordings", html: `<table><tr><th>name</th></tr></table>`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/agenda/planung/audio-recordings.cgi" {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				wantQuery := map[string]string{"project_id": "3", "studio_id": "4", "series_id": "99", "event_id": "42"}
				for key, want := range wantQuery {
					if got := r.URL.Query().Get(key); got != want {
						t.Errorf("query %s = %q, want %q", key, got, want)
					}
				}
				io.WriteString(w, tt.html)
			}))
			defer server.Close()
			svc := NewCalCmsServiceWithClient(serviceTestConfig(server.URL), server.Client())
			got, err := svc.HasRecording(42, 99)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("HasRecording() = %v, want %v", got, tt.want)
			}
		})
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
	_, err := svc.QueryEvents(time.Now(), time.Now())
	if err == nil || !strings.Contains(err.Error(), strconv.Itoa(http.StatusBadGateway)) {
		t.Fatalf("QueryEvents() error = %v", err)
	}
	if !body.closed.Load() {
		t.Fatal("response body was not closed")
	}
}
