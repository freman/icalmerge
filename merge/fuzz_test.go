package merge_test

import (
	"bytes"
	"context"
	"testing"

	ical "github.com/arran4/golang-ical"

	"github.com/freman/icalmerge/merge"
)

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
