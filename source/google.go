package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	ical "github.com/arran4/golang-ical"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	gcal "google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

type Google struct {
	name       string
	account    string
	calendarID string
	tokenFile  string
	daysAhead  int
	oauthCfg   *oauth2.Config
}

func NewGoogle(name, account, calendarID, tokenFile, clientID, clientSecret string, daysAhead int) *Google {
	return &Google{
		name:       name,
		account:    account,
		calendarID: calendarID,
		tokenFile:  tokenFile,
		daysAhead:  daysAhead,
		oauthCfg: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Scopes:       []string{gcal.CalendarReadonlyScope},
			Endpoint:     google.Endpoint,
		},
	}
}

func (g *Google) Name() string { return g.name }

func (g *Google) loadToken() (*oauth2.Token, error) {
	f, err := os.Open(g.tokenFile)
	if err != nil {
		return nil, fmt.Errorf("open token for %s: %w", g.account, err)
	}
	defer f.Close()

	tok := new(oauth2.Token)
	if err := json.NewDecoder(f).Decode(tok); err != nil {
		return nil, fmt.Errorf("decode token for %s: %w", g.account, err)
	}

	return tok, nil
}

func (g *Google) Fetch(ctx context.Context) (*ical.Calendar, error) {
	tok, err := g.loadToken()
	if err != nil {
		return nil, err
	}

	client := g.oauthCfg.Client(ctx, tok)

	svc, err := gcal.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("create calendar service for %s: %w", g.account, err)
	}

	now := time.Now()
	timeMax := now.AddDate(0, 0, g.daysAhead).Format(time.RFC3339)

	var allItems []*gcal.Event
	pageToken := ""

	for {
		call := svc.Events.List(g.calendarID).
			Context(ctx).
			TimeMin(now.Format(time.RFC3339)).
			TimeMax(timeMax).
			SingleEvents(true).
			OrderBy("startTime")

		if pageToken != "" {
			call = call.PageToken(pageToken)
		}

		page, err := call.Do()
		if err != nil {
			if gErr, ok := errors.AsType[*googleapi.Error](err); ok && (gErr.Code == 401 || gErr.Code == 403) {
				return nil, fmt.Errorf("auth error for account %q - run 'icalmerge auth add %s' to re-authorize: %w", g.account, g.account, err)
			}

			return nil, fmt.Errorf("list events for %s/%s: %w", g.account, g.calendarID, err)
		}

		allItems = append(allItems, page.Items...)

		if page.NextPageToken == "" {
			break
		}

		pageToken = page.NextPageToken
	}

	cal := ical.NewCalendar()

	for _, item := range allItems {
		if item.Status == "cancelled" {
			continue
		}

		ev := ical.NewEvent(item.Id + "@google.com")
		ev.SetDtStampTime(time.Now())
		ev.SetSummary(item.Summary)

		if item.Description != "" {
			ev.SetDescription(item.Description)
		}

		if item.Location != "" {
			ev.SetLocation(item.Location)
		}

		if item.Start.DateTime != "" {
			start, err := time.Parse(time.RFC3339, item.Start.DateTime)
			if err == nil {
				ev.SetStartAt(start)
			}
		} else if item.Start.Date != "" {
			start, err := time.Parse("2006-01-02", item.Start.Date)
			if err == nil {
				ev.SetAllDayStartAt(start)
			}
		}

		if item.End.DateTime != "" {
			end, err := time.Parse(time.RFC3339, item.End.DateTime)
			if err == nil {
				ev.SetEndAt(end)
			}
		} else if item.End.Date != "" {
			end, err := time.Parse("2006-01-02", item.End.Date)
			if err == nil {
				ev.SetAllDayEndAt(end)
			}
		}

		if item.Updated != "" {
			updated, err := time.Parse(time.RFC3339, item.Updated)
			if err == nil {
				ev.SetLastModifiedAt(updated)
			}
		}

		cal.AddVEvent(ev)
	}

	return cal, nil
}

func OAuthConfig(clientID, clientSecret, redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes:       []string{gcal.CalendarReadonlyScope},
		Endpoint:     google.Endpoint,
		RedirectURL:  redirectURL,
	}
}

func SaveToken(path string, tok *oauth2.Token) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create token file: %w", err)
	}
	defer f.Close()

	if err := json.NewEncoder(f).Encode(tok); err != nil {
		return fmt.Errorf("write token: %w", err)
	}

	return nil
}
