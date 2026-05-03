package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ical "github.com/arran4/golang-ical"
	"github.com/labstack/echo/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/freman/icalmerge/config"
	"github.com/freman/icalmerge/merge"
	"github.com/freman/icalmerge/server"
)

type mockSource struct {
	cal       *ical.Calendar
	fetchFunc func(ctx context.Context) (*ical.Calendar, error)
}

func (m *mockSource) Name() string { return "mock" }

func (m *mockSource) Fetch(ctx context.Context) (*ical.Calendar, error) {
	if m.fetchFunc != nil {
		return m.fetchFunc(ctx)
	}

	return m.cal, nil
}

func emptySource() merge.Source {
	return &mockSource{cal: ical.NewCalendar()}
}

func onDemandCfg(secret string) *config.Config {
	return &config.Config{
		Server: config.Server{
			Secret:       secret,
			AuthHeader:   "Authorization",
			CacheTTL:     config.Duration{Duration: time.Minute},
			FetchTimeout: config.Duration{Duration: 5 * time.Second},
			DaysAhead:    60,
		},
	}
}

func pollCfg(secret string, interval time.Duration) *config.Config {
	return &config.Config{
		Server: config.Server{
			Secret:       secret,
			AuthHeader:   "Authorization",
			PollInterval: config.Duration{Duration: interval},
			FetchTimeout: config.Duration{Duration: 5 * time.Second},
			DaysAhead:    60,
		},
	}
}

func doRequest(srv *server.Server, header, value string) *httptest.ResponseRecorder {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/calendar", nil)

	if header != "" {
		req.Header.Set(header, value)
	}

	rec := httptest.NewRecorder()

	_ = srv.HandleCalendar(e.NewContext(req, rec))

	return rec
}

// --- auth tests ---

func TestServer_AuthBearer(t *testing.T) {
	srv := server.New(onDemandCfg("plaintext-secret"), []merge.Source{emptySource()})
	rec := doRequest(srv, "Authorization", "Bearer plaintext-secret")

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

func TestServer_AuthMissing(t *testing.T) {
	srv := server.New(onDemandCfg("plaintext-secret"), []merge.Source{emptySource()})
	rec := doRequest(srv, "", "")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestServer_AuthWrongToken(t *testing.T) {
	srv := server.New(onDemandCfg("plaintext-secret"), []merge.Source{emptySource()})
	rec := doRequest(srv, "Authorization", "Bearer wrong")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestServer_AuthNoBearerPrefix(t *testing.T) {
	srv := server.New(onDemandCfg("plaintext-secret"), []merge.Source{emptySource()})
	rec := doRequest(srv, "Authorization", "plaintext-secret")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 when Authorization header lacks Bearer prefix, got %d", rec.Code)
	}
}

func TestServer_AuthCustomHeader(t *testing.T) {
	cfg := onDemandCfg("mysecret")
	cfg.Server.AuthHeader = "X-API-Key"

	srv := server.New(cfg, []merge.Source{emptySource()})
	rec := doRequest(srv, "X-API-Key", "mysecret")

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

func TestServer_AuthCustomHeaderWrong(t *testing.T) {
	cfg := onDemandCfg("mysecret")
	cfg.Server.AuthHeader = "X-API-Key"

	srv := server.New(cfg, []merge.Source{emptySource()})
	rec := doRequest(srv, "X-API-Key", "wrongsecret")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestServer_AuthCustomHeaderNoBearerRequired(t *testing.T) {
	cfg := onDemandCfg("mysecret")
	cfg.Server.AuthHeader = "X-API-Key"

	srv := server.New(cfg, []merge.Source{emptySource()})

	// raw token value, no "Bearer " prefix - should work for non-Authorization headers
	rec := doRequest(srv, "X-API-Key", "mysecret")

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 for custom header without Bearer prefix, got %d", rec.Code)
	}
}

func TestServer_AuthHashedSecret(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("mypassword"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}

	srv := server.New(onDemandCfg(string(hash)), []merge.Source{emptySource()})
	rec := doRequest(srv, "Authorization", "Bearer mypassword")

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

func TestServer_AuthHashedSecretWrong(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("mypassword"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}

	srv := server.New(onDemandCfg(string(hash)), []merge.Source{emptySource()})
	rec := doRequest(srv, "Authorization", "Bearer wrongpassword")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestServer_CalendarContentType(t *testing.T) {
	srv := server.New(onDemandCfg("secret"), []merge.Source{emptySource()})
	rec := doRequest(srv, "Authorization", "Bearer secret")

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/calendar") {
		t.Fatalf("want text/calendar content-type, got %q", ct)
	}
}

func TestServer_Healthz(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	srv := server.New(&config.Config{}, nil)

	_ = srv.HandleHealthz(e.NewContext(req, rec))

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}

	if rec.Body.String() != "ok" {
		t.Fatalf("want body 'ok', got %q", rec.Body.String())
	}
}

// --- background polling tests ---

func TestServer_PollMode_ServesAfterFirstPoll(t *testing.T) {
	srv := server.New(pollCfg("secret", time.Hour), []merge.Source{emptySource()})

	srv.StartPoller(t.Context())

	rec := doRequest(srv, "Authorization", "Bearer secret")

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

func TestServer_PollMode_ReturnsStaleOnRequestCancel(t *testing.T) {
	srv := server.New(pollCfg("secret", time.Hour), []merge.Source{emptySource()})

	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/calendar", nil)
	req = req.WithContext(reqCtx)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()

	_ = srv.HandleCalendar(e.NewContext(req, rec))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 when request cancelled before first poll, got %d", rec.Code)
	}
}

func TestServer_PollMode_RefreshesBuffer(t *testing.T) {
	pollCount := 0

	src := &mockSource{
		fetchFunc: func(ctx context.Context) (*ical.Calendar, error) {
			pollCount++

			return ical.NewCalendar(), nil
		},
	}

	srv := server.New(pollCfg("secret", 20*time.Millisecond), []merge.Source{src})

	srv.StartPoller(t.Context())

	time.Sleep(60 * time.Millisecond)

	if pollCount < 2 {
		t.Fatalf("want at least 2 background polls, got %d", pollCount)
	}

	rec := doRequest(srv, "Authorization", "Bearer secret")

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}
