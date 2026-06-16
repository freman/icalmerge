package source

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	ical "github.com/arran4/golang-ical"
)

type ICal struct {
	name    string
	url     string
	headers map[string]string
}

func NewICal(name, url string, headers map[string]string) *ICal {
	return &ICal{name: name, url: url, headers: headers}
}

func (s *ICal) Name() string { return s.name }

func (s *ICal) Fetch(ctx context.Context) (*ical.Calendar, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", s.name, err)
	}

	for k, v := range s.headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", s.name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", s.name, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.name, err)
	}

	cal, err := ical.ParseCalendarWithOptions(bytes.NewReader(body), ical.WithWindowsTimezoneMapping())
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.name, err)
	}

	return cal, nil
}
