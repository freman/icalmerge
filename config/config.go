package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}

	dur, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}

	d.Duration = dur

	return nil
}

type Config struct {
	Server    Server     `yaml:"server"`
	Google    Google     `yaml:"google"`
	Calendars []Calendar `yaml:"calendars"`
	DataDir   string     `yaml:"data_dir"`
}

type Server struct {
	Port          int      `yaml:"port"`
	Secret        string   `yaml:"secret"`
	AuthHeader    string   `yaml:"auth_header"`
	CacheTTL      Duration `yaml:"cache_ttl"`
	FetchTimeout  Duration `yaml:"fetch_timeout"`
	PollInterval  Duration `yaml:"poll_interval"`
	DaysAhead         int  `yaml:"days_ahead"`
	Parallelism       int  `yaml:"parallelism"`
	MarkConflicts     bool `yaml:"mark_conflicts"`
	ExpandRecurrences bool `yaml:"expand_recurrences"`
}

type Google struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
}

type Calendar struct {
	Name       string            `yaml:"name"`
	Type       string            `yaml:"type"`
	URL        string            `yaml:"url"`
	Headers    map[string]string `yaml:"headers"`
	Account    string            `yaml:"account"`
	CalendarID string            `yaml:"calendar_id"`
}

func (c *Config) TokenDir() string {
	return filepath.Join(c.DataDir, "tokens")
}

func (c *Config) SecretIsHashed() bool {
	return strings.HasPrefix(c.Server.Secret, "$2a$") ||
		strings.HasPrefix(c.Server.Secret, "$2b$") ||
		strings.HasPrefix(c.Server.Secret, "$2y$")
}

func Load(path string) (*Config, error) {
	cfg := &Config{
		Server: Server{
			Port:         8080,
			AuthHeader:   "Authorization",
			CacheTTL:     Duration{15 * time.Minute},
			FetchTimeout: Duration{30 * time.Second},
			DaysAhead:    60,
		},
	}

	explicit := path != ""
	if !explicit {
		path = "config.yaml"
	}

	f, err := os.Open(path)
	if err != nil {
		if !explicit && errors.Is(err, os.ErrNotExist) {
			// no default config file; proceed with env vars only
		} else {
			return nil, fmt.Errorf("open config: %w", err)
		}
	} else {
		defer f.Close()

		if err := yaml.NewDecoder(f).Decode(cfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	}

	if v := os.Getenv("ICALMERGE_SECRET"); v != "" {
		cfg.Server.Secret = v
	}
	if v := os.Getenv("ICALMERGE_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("GOOGLE_CLIENT_ID"); v != "" {
		cfg.Google.ClientID = v
	}
	if v := os.Getenv("GOOGLE_CLIENT_SECRET"); v != "" {
		cfg.Google.ClientSecret = v
	}

	if cfg.DataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("find home dir: %w", err)
		}
		cfg.DataDir = filepath.Join(home, ".config", "icalmerge")
	}

	return cfg, nil
}
