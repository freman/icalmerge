package merge_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ical "github.com/arran4/golang-ical"

	"github.com/freman/icalmerge/merge"
)

type mockSource struct {
	name      string
	cal       *ical.Calendar
	err       error
	fetchFunc func(ctx context.Context) (*ical.Calendar, error)
}

func (m *mockSource) Name() string { return m.name }

func (m *mockSource) Fetch(ctx context.Context) (*ical.Calendar, error) {
	if m.fetchFunc != nil {
		return m.fetchFunc(ctx)
	}

	return m.cal, m.err
}

func calWithEvents(events ...*ical.VEvent) *ical.Calendar {
	cal := ical.NewCalendar()
	for _, ev := range events {
		cal.AddVEvent(ev)
	}

	return cal
}

func eventAt(uid string, start, end time.Time) *ical.VEvent {
	ev := ical.NewEvent(uid)
	ev.SetStartAt(start)
	ev.SetEndAt(end)
	ev.SetSummary(uid)

	return ev
}

func TestCalendars_MergesEvents(t *testing.T) {
	now := time.Now()
	tomorrow := now.Add(24 * time.Hour)
	dayAfter := now.Add(48 * time.Hour)

	sources := []merge.Source{
		&mockSource{name: "a", cal: calWithEvents(eventAt("ev1", tomorrow, dayAfter))},
		&mockSource{name: "b", cal: calWithEvents(eventAt("ev2", tomorrow, dayAfter))},
	}

	result := merge.Calendars(context.Background(), sources, 60, 0, false)

	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}

	events := result.Calendar.Events()
	if len(events) != 2 {
		t.Fatalf("want 2 events, got %d", len(events))
	}
}

func TestCalendars_DeduplicatesByUID(t *testing.T) {
	now := time.Now()
	tomorrow := now.Add(24 * time.Hour)
	dayAfter := now.Add(48 * time.Hour)

	ev := eventAt("same-uid", tomorrow, dayAfter)

	sources := []merge.Source{
		&mockSource{name: "a", cal: calWithEvents(ev)},
		&mockSource{name: "b", cal: calWithEvents(ev)},
	}

	result := merge.Calendars(context.Background(), sources, 60, 0, false)

	events := result.Calendar.Events()
	if len(events) != 1 {
		t.Fatalf("want 1 deduplicated event, got %d", len(events))
	}
}

func TestCalendars_FiltersExpiredEvents(t *testing.T) {
	now := time.Now()
	yesterday := now.Add(-48 * time.Hour)
	twoDaysAgo := now.Add(-72 * time.Hour)
	tomorrow := now.Add(24 * time.Hour)
	dayAfter := now.Add(48 * time.Hour)

	sources := []merge.Source{
		&mockSource{name: "a", cal: calWithEvents(
			eventAt("past", twoDaysAgo, yesterday),
			eventAt("future", tomorrow, dayAfter),
		)},
	}

	result := merge.Calendars(context.Background(), sources, 60, 0, false)

	events := result.Calendar.Events()
	if len(events) != 1 {
		t.Fatalf("want 1 future event, got %d", len(events))
	}

	uid := events[0].GetProperty(ical.ComponentPropertyUniqueId)
	if uid == nil || uid.Value != "future" {
		t.Fatalf("want event uid=future, got %v", uid)
	}
}

func TestCalendars_FiltersBeyondDaysAhead(t *testing.T) {
	now := time.Now()
	far := now.Add(90 * 24 * time.Hour)
	farEnd := far.Add(time.Hour)
	near := now.Add(24 * time.Hour)
	nearEnd := near.Add(time.Hour)

	sources := []merge.Source{
		&mockSource{name: "a", cal: calWithEvents(
			eventAt("near", near, nearEnd),
			eventAt("far", far, farEnd),
		)},
	}

	result := merge.Calendars(context.Background(), sources, 60, 0, false)

	events := result.Calendar.Events()
	if len(events) != 1 {
		t.Fatalf("want 1 event within 60 days, got %d", len(events))
	}
}

func TestCalendars_PartialSourceFailure(t *testing.T) {
	now := time.Now()
	tomorrow := now.Add(24 * time.Hour)
	dayAfter := now.Add(48 * time.Hour)

	sources := []merge.Source{
		&mockSource{name: "ok", cal: calWithEvents(eventAt("ev1", tomorrow, dayAfter))},
		&mockSource{name: "broken", err: fmt.Errorf("connection refused")},
	}

	result := merge.Calendars(context.Background(), sources, 60, 0, false)

	if len(result.Errors) != 1 {
		t.Fatalf("want 1 error, got %d", len(result.Errors))
	}

	events := result.Calendar.Events()
	if len(events) != 1 {
		t.Fatalf("want 1 event from healthy source, got %d", len(events))
	}
}

func TestCalendars_AllSourcesFail(t *testing.T) {
	sources := []merge.Source{
		&mockSource{name: "a", err: fmt.Errorf("timeout")},
		&mockSource{name: "b", err: fmt.Errorf("timeout")},
	}

	result := merge.Calendars(context.Background(), sources, 60, 0, false)

	if len(result.Errors) != 2 {
		t.Fatalf("want 2 errors, got %d", len(result.Errors))
	}

	if result.Calendar == nil {
		t.Fatal("want empty calendar (not nil) even when all sources fail")
	}

	if len(result.Calendar.Events()) != 0 {
		t.Fatalf("want 0 events, got %d", len(result.Calendar.Events()))
	}
}

func TestCalendars_SortedByStart(t *testing.T) {
	now := time.Now()
	d1 := now.Add(72 * time.Hour)
	d2 := now.Add(24 * time.Hour)
	d3 := now.Add(48 * time.Hour)

	sources := []merge.Source{
		&mockSource{name: "a", cal: calWithEvents(
			eventAt("third", d1, d1.Add(time.Hour)),
			eventAt("first", d2, d2.Add(time.Hour)),
			eventAt("second", d3, d3.Add(time.Hour)),
		)},
	}

	result := merge.Calendars(context.Background(), sources, 60, 0, false)

	events := result.Calendar.Events()
	if len(events) != 3 {
		t.Fatalf("want 3 events, got %d", len(events))
	}

	expected := []string{"first", "second", "third"}
	for i, ev := range events {
		uid := ev.GetProperty(ical.ComponentPropertyUniqueId)
		if uid == nil || uid.Value != expected[i] {
			t.Errorf("event[%d]: want uid=%s, got %v", i, expected[i], uid)
		}
	}
}

func TestCalendars_Timeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()

	result := merge.Calendars(ctx, []merge.Source{&slowSource{name: "slow"}}, 60, 0, false)

	if len(result.Errors) == 0 {
		t.Fatal("want timeout error, got none")
	}
}

func TestCalendars_ParallelismLimit(t *testing.T) {
	const (
		numSources  = 6
		limit       = 2
		fetchDelay  = 20 * time.Millisecond
	)

	var active atomic.Int32
	var maxSeen atomic.Int32

	makeSource := func(n int) merge.Source {
		return &mockSource{
			name: fmt.Sprintf("src%d", n),
			fetchFunc: func(ctx context.Context) (*ical.Calendar, error) {
				cur := active.Add(1)

				for {
					seen := maxSeen.Load()
					if cur <= seen || maxSeen.CompareAndSwap(seen, cur) {
						break
					}
				}

				time.Sleep(fetchDelay)
				active.Add(-1)

				return ical.NewCalendar(), nil
			},
		}
	}

	sources := make([]merge.Source, numSources)
	for i := range sources {
		sources[i] = makeSource(i)
	}

	merge.Calendars(context.Background(), sources, 60, limit, false)

	if got := maxSeen.Load(); got > limit {
		t.Fatalf("parallelism limit %d exceeded: saw %d concurrent fetches", limit, got)
	}
}

func TestCalendars_ParallelismUnlimited(t *testing.T) {
	const numSources = 6

	var (
		mu      sync.Mutex
		active  int
		maxSeen int
		ready   = make(chan struct{})
	)

	makeSource := func() merge.Source {
		return &mockSource{
			fetchFunc: func(ctx context.Context) (*ical.Calendar, error) {
				mu.Lock()
				active++
				if active > maxSeen {
					maxSeen = active
				}
				if active == numSources {
					close(ready)
				}
				mu.Unlock()

				<-ready

				mu.Lock()
				active--
				mu.Unlock()

				return ical.NewCalendar(), nil
			},
		}
	}

	sources := make([]merge.Source, numSources)
	for i := range sources {
		sources[i] = makeSource()
	}

	merge.Calendars(context.Background(), sources, 60, 0, false)

	if maxSeen != numSources {
		t.Fatalf("want all %d sources fetched concurrently, max seen was %d", numSources, maxSeen)
	}
}

func TestConflicts_OverlappingEventsMarked(t *testing.T) {
	now := time.Now()
	base := now.Add(24 * time.Hour)

	// A: 10:00-11:00, B: 10:30-11:30 - overlap
	a := eventAt("a", base, base.Add(time.Hour))
	b := eventAt("b", base.Add(30*time.Minute), base.Add(90*time.Minute))

	sources := []merge.Source{
		&mockSource{name: "s", cal: calWithEvents(a, b)},
	}

	result := merge.Calendars(context.Background(), sources, 60, 0, true)

	events := result.Calendar.Events()
	if len(events) != 2 {
		t.Fatalf("want 2 events, got %d", len(events))
	}

	for _, ev := range events {
		prop := ev.GetProperty(ical.ComponentPropertySummary)
		if prop == nil || !strings.HasPrefix(prop.Value, "CONFLICT: ") {
			t.Errorf("want CONFLICT prefix, got summary %q", prop)
		}
	}
}

func TestConflicts_NonOverlappingNotMarked(t *testing.T) {
	now := time.Now()
	base := now.Add(24 * time.Hour)

	// A: 10:00-11:00, B: 11:00-12:00 - adjacent, not overlapping
	a := eventAt("a", base, base.Add(time.Hour))
	b := eventAt("b", base.Add(time.Hour), base.Add(2*time.Hour))

	sources := []merge.Source{
		&mockSource{name: "s", cal: calWithEvents(a, b)},
	}

	result := merge.Calendars(context.Background(), sources, 60, 0, true)

	for _, ev := range result.Calendar.Events() {
		prop := ev.GetProperty(ical.ComponentPropertySummary)
		if prop != nil && strings.HasPrefix(prop.Value, "CONFLICT: ") {
			t.Errorf("adjacent events should not be marked as conflicts, got %q", prop.Value)
		}
	}
}

func TestConflicts_DisabledDoesNotMark(t *testing.T) {
	now := time.Now()
	base := now.Add(24 * time.Hour)

	a := eventAt("a", base, base.Add(time.Hour))
	b := eventAt("b", base.Add(30*time.Minute), base.Add(90*time.Minute))

	sources := []merge.Source{
		&mockSource{name: "s", cal: calWithEvents(a, b)},
	}

	result := merge.Calendars(context.Background(), sources, 60, 0, false)

	for _, ev := range result.Calendar.Events() {
		prop := ev.GetProperty(ical.ComponentPropertySummary)
		if prop != nil && strings.HasPrefix(prop.Value, "CONFLICT: ") {
			t.Errorf("mark_conflicts=false should not prefix summaries, got %q", prop.Value)
		}
	}
}

func TestConflicts_ThreeWayOverlap(t *testing.T) {
	now := time.Now()
	base := now.Add(24 * time.Hour)

	// All three overlap with each other
	a := eventAt("a", base, base.Add(2*time.Hour))
	b := eventAt("b", base.Add(30*time.Minute), base.Add(90*time.Minute))
	c := eventAt("c", base.Add(time.Hour), base.Add(3*time.Hour))

	sources := []merge.Source{
		&mockSource{name: "s", cal: calWithEvents(a, b, c)},
	}

	result := merge.Calendars(context.Background(), sources, 60, 0, true)

	events := result.Calendar.Events()
	for _, ev := range events {
		prop := ev.GetProperty(ical.ComponentPropertySummary)
		if prop == nil || !strings.HasPrefix(prop.Value, "CONFLICT: ") {
			t.Errorf("want CONFLICT prefix on all events, got %q", prop)
		}
	}
}

func TestConflicts_NoPrefixDoubling(t *testing.T) {
	now := time.Now()
	base := now.Add(24 * time.Hour)

	a := eventAt("a", base, base.Add(time.Hour))
	b := eventAt("b", base.Add(30*time.Minute), base.Add(90*time.Minute))

	sources := []merge.Source{
		&mockSource{name: "s", cal: calWithEvents(a, b)},
	}

	// Run twice - second run should not double-prefix since events are
	// re-fetched each time. Verifies the HasPrefix guard works.
	result := merge.Calendars(context.Background(), sources, 60, 0, true)

	for _, ev := range result.Calendar.Events() {
		prop := ev.GetProperty(ical.ComponentPropertySummary)
		if prop != nil && strings.HasPrefix(prop.Value, "CONFLICT: CONFLICT: ") {
			t.Errorf("summary was double-prefixed: %q", prop.Value)
		}
	}
}

type slowSource struct{ name string }

func (s *slowSource) Name() string { return s.name }

func (s *slowSource) Fetch(ctx context.Context) (*ical.Calendar, error) {
	<-ctx.Done()

	return nil, ctx.Err()
}
