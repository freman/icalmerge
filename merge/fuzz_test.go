package merge_test

import (
	"bytes"
	"context"
	"testing"

	ical "github.com/arran4/golang-ical"

	"github.com/freman/icalmerge/merge"
)

// TestWindowsTimezoneRRULE_EventIncluded is a full-pipeline regression test for
// the bug where a recurring Outlook event used a Windows timezone name (which
// requires WithWindowsTimezoneMapping to parse) AND had a past DTSTART - causing
// the merge filter to drop it even though future occurrences existed.
//
// The fixture is modelled on a real Outlook calendar feed, anonymised.
func TestWindowsTimezoneRRULE_EventIncluded(t *testing.T) {
	// Weekly 15-min sync that started in the past (2026-01-06) and recurs
	// every Tuesday until 2099. Uses "New Zealand Standard Time" - a Windows
	// timezone name that requires mapping to Pacific/Auckland before Go's
	// time package can parse the DTSTART/DTEND.
	const feed = "BEGIN:VCALENDAR\r\n" +
		"VERSION:2.0\r\n" +
		"PRODID:-//Microsoft Corporation//Outlook 16.0 MIMEDIR//EN\r\n" +
		"BEGIN:VEVENT\r\n" +
		"UID:weekly-sync-windows-tz-rrule@test\r\n" +
		"SUMMARY:Weekly Sync\r\n" +
		"DTSTART;TZID=New Zealand Standard Time:20260106T150000\r\n" +
		"DTEND;TZID=New Zealand Standard Time:20260106T151500\r\n" +
		"RRULE:FREQ=WEEKLY;UNTIL=20991231T000000Z;INTERVAL=1;BYDAY=TU;WKST=MO\r\n" +
		"CLASS:PUBLIC\r\n" +
		"STATUS:CONFIRMED\r\n" +
		"TRANSP:OPAQUE\r\n" +
		"LOCATION:Meeting Room 1\r\n" +
		"X-MICROSOFT-CDO-ALLDAYEVENT:FALSE\r\n" +
		"X-MICROSOFT-CDO-INSTTYPE:1\r\n" +
		"END:VEVENT\r\n" +
		"END:VCALENDAR\r\n"

	cal, err := ical.ParseCalendarWithOptions(bytes.NewReader([]byte(feed)), ical.WithWindowsTimezoneMapping())
	if err != nil {
		t.Fatalf("ParseCalendarWithOptions: %v", err)
	}

	src := &fuzzSource{cal: cal}
	result := merge.Calendars(context.Background(), []merge.Source{src}, 60, 0, false)

	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}

	events := result.Calendar.Events()
	if len(events) != 1 {
		t.Fatalf("want 1 event (recurring series still active), got %d - "+
			"RRULE with Windows timezone and past DTSTART was incorrectly filtered", len(events))
	}
}

// FuzzCalendarsFromICal exercises the full parse -> eventStart/eventEnd -> sort
// pipeline against arbitrary bytes. A calendar feed comes from the internet so
// the parser and date-extraction code must never panic on malformed input.
func FuzzCalendarsFromICal(f *testing.F) {
	f.Add([]byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\nBEGIN:VEVENT\r\nUID:fuzz1@test\r\nSUMMARY:Test\r\nDTSTART:20240101T100000Z\r\nDTEND:20240101T110000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"))
	f.Add([]byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:fuzz2@test\r\nSUMMARY:All Day\r\nDTSTART;VALUE=DATE:20240101\r\nDTEND;VALUE=DATE:20240102\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"))
	f.Add([]byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nEND:VCALENDAR\r\n"))
	f.Add([]byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"))
	f.Add([]byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:\r\nDTSTART:\r\nDTEND:\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		cal, err := ical.ParseCalendar(bytes.NewReader(data))
		if err != nil {
			return
		}

		src := &fuzzSource{cal: cal}

		// Must not panic regardless of what the parser accepted.
		_ = merge.Calendars(context.Background(), []merge.Source{src}, 60, 0, false)
	})
}

type fuzzSource struct{ cal *ical.Calendar }

func (f *fuzzSource) Name() string                                    { return "fuzz" }
func (f *fuzzSource) Fetch(_ context.Context) (*ical.Calendar, error) { return f.cal, nil }
