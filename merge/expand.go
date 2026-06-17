package merge

import (
	"time"

	ical "github.com/arran4/golang-ical"
	"github.com/teambition/rrule-go"
)

// expandRecurringEvents replaces master recurring events (those with RRULE and
// no RECURRENCE-ID) with individual instances within [after, before]. Exception
// events (RECURRENCE-ID) are passed through as-is; their original occurrence
// times are skipped during expansion so they don't double up.
func expandRecurringEvents(events []*ical.VEvent, after, before time.Time) []*ical.VEvent {
	type masterEntry struct {
		ev      *ical.VEvent
		exTimes map[time.Time]struct{}
	}

	masters := make(map[string]*masterEntry)
	exTimesByUID := make(map[string]map[time.Time]struct{})
	var others []*ical.VEvent

	for _, ev := range events {
		if ev == nil {
			continue
		}

		uid := eventUID(ev)
		rules, _ := ev.GetRRules()
		recID, recErr := ev.GetRecurrenceID()

		switch {
		case len(rules) > 0 && recErr != nil:
			// Master recurring event - no RECURRENCE-ID.
			if _, ok := masters[uid]; !ok {
				masters[uid] = &masterEntry{
					ev:      ev,
					exTimes: make(map[time.Time]struct{}),
				}
			}

		case recErr == nil:
			// Exception event - records which occurrence it overrides.
			if _, ok := exTimesByUID[uid]; !ok {
				exTimesByUID[uid] = make(map[time.Time]struct{})
			}

			exTimesByUID[uid][recID.UTC().Truncate(time.Second)] = struct{}{}
			others = append(others, ev)

		default:
			others = append(others, ev)
		}
	}

	// Connect collected exception times to their master entries.
	for uid, times := range exTimesByUID {
		if m, ok := masters[uid]; ok {
			for t := range times {
				m.exTimes[t] = struct{}{}
			}
		}
	}

	var expanded []*ical.VEvent

	for _, m := range masters {
		instances, err := expandEvent(m.ev, m.exTimes, after, before)
		if err != nil {
			// If expansion fails, fall back to passing the master through.
			others = append(others, m.ev)

			continue
		}

		expanded = append(expanded, instances...)
	}

	return append(expanded, others...)
}

// expandEvent generates individual VEvent instances for each occurrence of ev
// within [after, before], skipping times listed in exTimes (exception events)
// and EXDATE entries.
func expandEvent(ev *ical.VEvent, exTimes map[time.Time]struct{}, after, before time.Time) ([]*ical.VEvent, error) {
	start, err := ev.GetStartAt()
	if err != nil {
		return nil, err
	}

	end := eventEnd(ev)
	if end.IsZero() {
		end = start
	}

	duration := end.Sub(start)

	exdates, _ := ev.GetExDates()
	for _, t := range exdates {
		exTimes[t.UTC().Truncate(time.Second)] = struct{}{}
	}

	uid := eventUID(ev)
	rawRules := ev.GetProperties(ical.ComponentProperty(ical.PropertyRrule))

	var occurrences []time.Time

	for _, prop := range rawRules {
		opt, err := rrule.StrToROptionInLocation(prop.Value, start.Location())
		if err != nil {
			continue
		}

		opt.Dtstart = start

		r, err := rrule.NewRRule(*opt)
		if err != nil {
			continue
		}

		occurrences = append(occurrences, r.Between(after, before, true)...)
	}

	var instances []*ical.VEvent

	for _, occ := range occurrences {
		key := occ.UTC().Truncate(time.Second)
		if _, excluded := exTimes[key]; excluded {
			continue
		}

		instanceUID := uid + "-" + occ.UTC().Format("20060102T150405Z")
		instances = append(instances, cloneEvent(ev, instanceUID, occ, occ.Add(duration)))
	}

	return instances, nil
}

var skipOnClone = map[string]bool{
	string(ical.PropertyUid):          true,
	string(ical.PropertyDtstart):      true,
	string(ical.PropertyDtend):        true,
	string(ical.PropertyRrule):        true,
	string(ical.PropertyExdate):       true,
	string(ical.PropertyExrule):       true,
	string(ical.PropertyRdate):        true,
	string(ical.PropertyRecurrenceId): true,
}

// cloneEvent creates a new VEvent with the given uid, start, and end, copying
// all other properties from src.
func cloneEvent(src *ical.VEvent, uid string, start, end time.Time) *ical.VEvent {
	ev := ical.NewEvent(uid)

	for _, prop := range src.Properties {
		if !skipOnClone[prop.IANAToken] {
			ev.Properties = append(ev.Properties, prop)
		}
	}

	ev.SetStartAt(start)
	ev.SetEndAt(end)

	return ev
}

func eventUID(ev *ical.VEvent) string {
	if p := ev.GetProperty(ical.ComponentPropertyUniqueId); p != nil {
		return p.Value
	}

	return ""
}
