package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/freman/icalmerge/cmd"
	"github.com/freman/icalmerge/config"
	"github.com/freman/icalmerge/merge"
	"github.com/freman/icalmerge/server"
	"github.com/freman/icalmerge/source"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		printUsage()

		return nil
	}

	configPath := os.Getenv("ICALMERGE_CONFIG")

	switch os.Args[1] {
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		fs.StringVar(&configPath, "config", configPath, "config file path")
		_ = fs.Parse(os.Args[2:])

		return runServe(configPath)

	case "auth":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: icalmerge auth <add|list|revoke> [name]")

			return nil
		}

		fs := flag.NewFlagSet("auth", flag.ExitOnError)
		fs.StringVar(&configPath, "config", configPath, "config file path")
		_ = fs.Parse(os.Args[3:])

		cfg, err := config.Load(configPath)
		if err != nil {
			return err
		}

		switch os.Args[2] {
		case "add":
			if fs.NArg() < 1 {
				return fmt.Errorf("usage: icalmerge auth add <name>")
			}

			return cmd.AuthAdd(cfg, fs.Arg(0))

		case "list":
			return cmd.AuthList(cfg)

		case "revoke":
			if fs.NArg() < 1 {
				return fmt.Errorf("usage: icalmerge auth revoke <name>")
			}

			return cmd.AuthRevoke(cfg, fs.Arg(0))

		default:
			return fmt.Errorf("unknown auth subcommand %q", os.Args[2])
		}

	case "once":
		fs := flag.NewFlagSet("once", flag.ExitOnError)
		fs.StringVar(&configPath, "config", configPath, "config file path")
		_ = fs.Parse(os.Args[2:])

		return runOnce(configPath)

	case "password":
		return runPassword()

	default:
		return fmt.Errorf("unknown command %q", os.Args[1])
	}
}

func runOnce(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	sources, err := buildSources(cfg)
	if err != nil {
		return err
	}

	if len(sources) == 0 {
		return fmt.Errorf("no calendars configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.FetchTimeout.Duration)
	defer cancel()

	result := merge.Calendars(ctx, sources, cfg.Server.DaysAhead, cfg.Server.Parallelism, cfg.Server.MarkConflicts)

	for _, e := range result.Errors {
		slog.Warn("source error", "err", e)
	}

	if result.Calendar == nil {
		return fmt.Errorf("all sources failed")
	}

	return result.Calendar.SerializeTo(os.Stdout)
}

func runPassword() error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}

	plaintext := strings.TrimSpace(scanner.Text())
	if plaintext == "" {
		return fmt.Errorf("password must not be empty")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), 12)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	fmt.Println(string(hash))

	return nil
}

func runServe(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	if cfg.Server.Secret == "" {
		slog.Warn("server.secret is not set - serving without authentication")
	} else if !cfg.SecretIsHashed() {
		slog.Warn("server.secret is stored in plaintext - run 'icalmerge password' to generate a hashed value")
	}

	sources, err := buildSources(cfg)
	if err != nil {
		return err
	}

	if len(sources) == 0 {
		return fmt.Errorf("no calendars configured")
	}

	slog.Info("configured sources", "count", len(sources))

	srv := server.New(cfg, sources)

	return srv.Start()
}

func buildSources(cfg *config.Config) ([]merge.Source, error) {
	sources := make([]merge.Source, 0, len(cfg.Calendars))

	for _, cal := range cfg.Calendars {
		switch cal.Type {
		case "ical":
			if cal.URL == "" {
				return nil, fmt.Errorf("calendar %q: url is required for type ical", cal.Name)
			}

			sources = append(sources, source.NewICal(cal.Name, cal.URL, cal.Headers))

		case "google":
			if cal.Account == "" {
				return nil, fmt.Errorf("calendar %q: account is required for type google", cal.Name)
			}

			if cal.CalendarID == "" {
				return nil, fmt.Errorf("calendar %q: calendar_id is required for type google", cal.Name)
			}

			if cfg.Google.ClientID == "" || cfg.Google.ClientSecret == "" {
				return nil, fmt.Errorf("calendar %q: google.client_id and google.client_secret must be configured", cal.Name)
			}

			tokenFile := filepath.Join(cfg.TokenDir(), cal.Account+".json")
			sources = append(sources, source.NewGoogle(
				cal.Name,
				cal.Account,
				cal.CalendarID,
				tokenFile,
				cfg.Google.ClientID,
				cfg.Google.ClientSecret,
				cfg.Server.DaysAhead,
			))

		default:
			return nil, fmt.Errorf("calendar %q: unknown type %q", cal.Name, cal.Type)
		}
	}

	return sources, nil
}

func printUsage() {
	fmt.Print(`icalmerge - merge multiple calendars into a single iCal feed

Commands:
  serve [--config <path>]          start the HTTP server
  once [--config <path>]           fetch, merge, and write iCal to stdout
  auth add <name> [--config]       authorize a Google account
  auth list [--config]             list authorized accounts
  auth revoke <name> [--config]    remove an account token
  password                         hash a password from stdin (bcrypt, cost 12)

Environment:
  ICALMERGE_CONFIG       path to config file (default: config.yaml)
  ICALMERGE_SECRET       overrides server.secret
  ICALMERGE_DATA_DIR     overrides data_dir
  GOOGLE_CLIENT_ID       overrides google.client_id
  GOOGLE_CLIENT_SECRET   overrides google.client_secret
`)
}
