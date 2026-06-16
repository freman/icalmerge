package merge

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	ical "github.com/arran4/golang-ical"
)

type Source interface {
	Name() string
	Fetch(ctx context.Context) (*ical.Calendar, error)
}

type Result struct {
	Calendar *ical.Calendar
	Errors   []error
}

func Calendars(ctx context.Context, sources []Source, daysAhead, parallelism int, markConflicts bool) Result {
	type fetchResult struct {
		cal *ical.Calendar
		err error
	}

	ch := make(chan fetchResult, len(sources))

	var sem chan struct{}
	if parallelism > 0 {
		sem = make(chan struct{}, parallelism)
	}

	for _, s := range sources {
		go func() {
			if sem != nil {
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-ctx.Done():
					ch <- fetchResult{err: fmt.Errorf("%s: %w", s.Name(), ctx.Err())}

					return
				}
			}

			cal, err := s.Fetch(ctx)
			ch <- fetchResult{cal, err}
		}()
	}

	var result Result
	var allEvents []*ical.VEvent

	for i := range sources {
		select {
		case r := <-ch:
			if r.err != nil {
				result.Errors = append(result.Errors, r.err)
			} else {
				allEvents = append(allEvents, r.cal.Events()...)
			}
		case <-ctx.Done():
			result.Errors = append(result.Errors, fmt.Errorf("fetch timed out after %d/%d sources responded", i, len(sources)))
			goto process
		}
	}

process:
	now := time.Now()
	cutoff := now.AddDate(0, 0, daysAhead)

	seen := make(map[string]struct{})
	var filtered []*ical.VEvent

	for _, ev := range allEvents {
		if ev == nil {
			continue
		}

		start := eventStart(ev)
		end := eventEnd(ev)

		if end.IsZero() {
			end = start
		}

		// For recurring events, use RRULE UNTIL as the effective end so
		// future occurrences aren't dropped when only the master event's
		// DTEND has already passed.
		if end.Before(now) {
			if recurEnd, ok := eventRecurEnd(ev); ok && recurEnd.After(end) {
				end = recurEnd
			}
		}

		if end.Before(now) || start.After(cutoff) {
			continue
		}

		uid := ev.GetProperty(ical.ComponentPropertyUniqueId)
		if uid == nil {
			filtered = append(filtered, ev)

			continue
		}

		if _, ok := seen[uid.Value]; ok {
			continue
		}

		seen[uid.Value] = struct{}{}

		filtered = append(filtered, ev)
	}

	sort.Slice(filtered, func(i, j int) bool {
		return eventStart(filtered[i]).Before(eventStart(filtered[j]))
	})

	if markConflicts {
		applyConflictPrefix(filtered)
	}

	out := ical.NewCalendarFor("icalmerge")
	out.SetName("merged")

	for _, ev := range filtered {
		out.AddVEvent(ev)
	}

	result.Calendar = out

	return result
}

// events must be pre-sorted by DTSTART; overlapping pairs get "CONFLICT: " prefix.
func applyConflictPrefix(events []*ical.VEvent) {
	conflicted := make([]bool, len(events))

	for i, ev := range events {
		iEnd := eventEnd(ev)
		if iEnd.IsZero() {
			iEnd = eventStart(ev)
		}

		for j := i + 1; j < len(events); j++ {
			jStart := eventStart(events[j])
			if !jStart.Before(iEnd) {
				break
			}

			conflicted[i] = true
			conflicted[j] = true
		}
	}

	for i, ev := range events {
		if !conflicted[i] {
			continue
		}

		summary := ""
		if prop := ev.GetProperty(ical.ComponentPropertySummary); prop != nil {
			summary = prop.Value
		}

		if !strings.HasPrefix(summary, "CONFLICT: ") {
			ev.SetSummary("CONFLICT: " + summary)
		}
	}
}

func eventStart(ev *ical.VEvent) time.Time {
	t, err := ev.GetStartAt()
	if err == nil {
		return t
	}

	t, err = ev.GetAllDayStartAt()
	if err == nil {
		return t
	}

	return time.Time{}
}

func eventEnd(ev *ical.VEvent) time.Time {
	t, err := ev.GetEndAt()
	if err == nil {
		return t
	}

	t, err = ev.GetAllDayEndAt()
	if err == nil {
		return t
	}

	return time.Time{}
}

// eventRecurEnd returns the effective end of a recurring event's series and
// true when the event has an RRULE. For rules with UNTIL set it returns that
// timestamp; for infinite/COUNT-based series it returns a far-future sentinel.
// Returns zero and false when no RRULE is present.
func eventRecurEnd(ev *ical.VEvent) (time.Time, bool) {
	rules, err := ev.GetRRules()
	if err != nil || len(rules) == 0 {
		return time.Time{}, false
	}

	latest := time.Time{}

	for _, rule := range rules {
		if rule.Until.IsZero() {
			return time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC), true
		}

		if rule.Until.After(latest) {
			latest = rule.Until
		}
	}

	return latest, true
}
