package source

import (
	"bytes"
	"strings"
	"testing"
	"time"

	ical "github.com/arran4/golang-ical"
)

// TestWindowsToIANAAllLoadable verifies every IANA value in the map is
// recognised by time.LoadLocation. A typo here silently produces zero times
// at runtime, so catching it at test time is valuable.
func TestWindowsToIANAAllLoadable(t *testing.T) {
	for win, iana := range windowsToIANA {
		if _, err := time.LoadLocation(iana); err != nil {
			t.Errorf("windowsToIANA[%q] = %q: %v", win, iana, err)
		}
	}
}

func TestNormalizeTimezones(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "unquoted TZID parameter",
			input: "DTSTART;TZID=New Zealand Standard Time:20260407T120000",
			want:  "DTSTART;TZID=Pacific/Auckland:20260407T120000",
		},
		{
			name:  "quoted TZID parameter",
			input: `DTSTART;TZID="E. Australia Standard Time":20260429T103000`,
			want:  "DTSTART;TZID=Australia/Brisbane:20260429T103000",
		},
		{
			name:  "unknown TZID is left unchanged",
			input: "DTSTART;TZID=Made Up Standard Time:20260101T120000",
			want:  "DTSTART;TZID=Made Up Standard Time:20260101T120000",
		},
		{
			name:  "multiple replacements in one pass",
			input: "TZID=China Standard Time\nTZID=Korea Standard Time",
			want:  "TZID=Asia/Shanghai\nTZID=Asia/Seoul",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := string(normalizeTimezones([]byte(tc.input)))
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestNormalizeTimezonesRoundtrip feeds a Windows-TZID iCal through
// normalizeTimezones then the parser, and checks that GetStartAt returns
// the correct UTC time (not zero).
func TestNormalizeTimezonesRoundtrip(t *testing.T) {
	// NZST is UTC+12; DST ends first Sunday in April so 2026-04-07 is standard.
	// 12:00 NZST = 00:00 UTC.
	const feed = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\n" +
		"BEGIN:VEVENT\r\n" +
		"UID:nzst-test@test\r\n" +
		"SUMMARY:NZ meeting\r\n" +
		"DTSTART;TZID=New Zealand Standard Time:20260407T120000\r\n" +
		"DTEND;TZID=New Zealand Standard Time:20260407T123000\r\n" +
		"END:VEVENT\r\nEND:VCALENDAR\r\n"

	normalized := normalizeTimezones([]byte(feed))

	if strings.Contains(string(normalized), "New Zealand Standard Time") {
		t.Fatal("Windows timezone name was not replaced")
	}

	cal, err := ical.ParseCalendar(bytes.NewReader(normalized))
	if err != nil {
		t.Fatalf("ParseCalendar: %v", err)
	}

	events := cal.Events()
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}

	start, err := events[0].GetStartAt()
	if err != nil {
		t.Fatalf("GetStartAt: %v", err)
	}

	if start.IsZero() {
		t.Fatal("GetStartAt returned zero time - timezone normalization did not work")
	}

	want := time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC)
	if !start.UTC().Equal(want) {
		t.Errorf("want %v UTC, got %v UTC", want, start.UTC())
	}
}
