package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ical "github.com/arran4/golang-ical"
	"github.com/labstack/echo/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/freman/icalmerge/config"
	"github.com/freman/icalmerge/merge"
)

type Server struct {
	cfg     *config.Config
	sources []merge.Source

	// on-demand cache (poll_interval == 0)
	mu       sync.Mutex
	cached   []byte
	cachedAt time.Time

	// background polling (poll_interval > 0)
	active    atomic.Pointer[[]byte]
	firstPoll chan struct{}
	closeOnce sync.Once
}

func New(cfg *config.Config, sources []merge.Source) *Server {
	return &Server{
		cfg:       cfg,
		sources:   sources,
		firstPoll: make(chan struct{}),
	}
}

func (s *Server) Start() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if s.cfg.Server.PollInterval.Duration > 0 {
		s.StartPoller(ctx)
	}

	e := echo.New()

	e.GET("/calendar", s.HandleCalendar)
	e.GET("/healthz", s.HandleHealthz)

	addr := fmt.Sprintf(":%d", s.cfg.Server.Port)
	slog.Info("starting server", "addr", addr)

	if err := e.Start(addr); !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	slog.Info("server stopped")

	return nil
}

// StartPoller does an immediate fetch then ticks on PollInterval until ctx is cancelled.
func (s *Server) StartPoller(ctx context.Context) {
	slog.Info("background polling enabled", "interval", s.cfg.Server.PollInterval.Duration)

	go func() {
		s.refresh(ctx)

		ticker := time.NewTicker(s.cfg.Server.PollInterval.Duration)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				s.refresh(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (s *Server) refresh(ctx context.Context) {
	fetchCtx, cancel := context.WithTimeout(ctx, s.cfg.Server.FetchTimeout.Duration)
	defer cancel()

	result := merge.Calendars(fetchCtx, s.sources, s.cfg.Server.DaysAhead, s.cfg.Server.Parallelism, s.cfg.Server.MarkConflicts)

	for _, err := range result.Errors {
		slog.Warn("source error during poll", "err", err)
	}

	if result.Calendar == nil {
		slog.Error("all sources failed during poll - retaining previous calendar")

		return
	}

	data, err := serialize(result.Calendar)
	if err != nil {
		slog.Error("serialize failed during poll", "err", err)

		return
	}

	s.active.Store(&data)
	s.closeOnce.Do(func() { close(s.firstPoll) })
}

func (s *Server) HandleHealthz(c echo.Context) error {
	return c.String(http.StatusOK, "ok")
}

func (s *Server) HandleCalendar(c echo.Context) error {
	if !s.checkSecret(c) {
		return c.String(http.StatusUnauthorized, "unauthorized")
	}

	data, err := s.getCalendar(c.Request().Context())
	if err != nil {
		slog.Error("calendar unavailable", "err", err)

		return c.String(http.StatusInternalServerError, "calendar unavailable")
	}

	return c.Blob(http.StatusOK, "text/calendar; charset=utf-8", data)
}

func (s *Server) checkSecret(c echo.Context) bool {
	if s.cfg.Server.Secret == "" {
		return true
	}

	val := c.Request().Header.Get(s.cfg.Server.AuthHeader)
	if val == "" {
		return false
	}

	incoming := val
	if strings.EqualFold(s.cfg.Server.AuthHeader, "Authorization") {
		after, ok := strings.CutPrefix(val, "Bearer ")
		if !ok {
			return false
		}

		incoming = after
	}

	if s.cfg.SecretIsHashed() {
		return bcrypt.CompareHashAndPassword([]byte(s.cfg.Server.Secret), []byte(incoming)) == nil
	}

	return incoming == s.cfg.Server.Secret
}

func (s *Server) getCalendar(ctx context.Context) ([]byte, error) {
	if s.cfg.Server.PollInterval.Duration > 0 {
		return s.getPolled(ctx)
	}

	return s.getOnDemand()
}

// getPolled blocks until the first background poll completes, then returns the current buffer.
func (s *Server) getPolled(ctx context.Context) ([]byte, error) {
	select {
	case <-s.firstPoll:
	case <-ctx.Done():
		return nil, fmt.Errorf("request cancelled waiting for first background poll: %w", ctx.Err())
	}

	ptr := s.active.Load()
	if ptr == nil {
		return nil, fmt.Errorf("no calendar data available")
	}

	return *ptr, nil
}

func (s *Server) getOnDemand() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cached != nil && time.Since(s.cachedAt) < s.cfg.Server.CacheTTL.Duration {
		return s.cached, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.Server.FetchTimeout.Duration)
	defer cancel()

	result := merge.Calendars(ctx, s.sources, s.cfg.Server.DaysAhead, s.cfg.Server.Parallelism, s.cfg.Server.MarkConflicts)

	for _, err := range result.Errors {
		slog.Warn("source error", "err", err)
	}

	if result.Calendar == nil {
		return nil, fmt.Errorf("all %d sources failed", len(s.sources))
	}

	data, err := serialize(result.Calendar)
	if err != nil {
		return nil, fmt.Errorf("serialize calendar: %w", err)
	}

	s.cached = data
	s.cachedAt = time.Now()

	return data, nil
}

func serialize(cal *ical.Calendar) ([]byte, error) {
	var buf bytes.Buffer
	if err := cal.SerializeTo(&buf); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
