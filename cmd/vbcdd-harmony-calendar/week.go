package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// weekDays is the horizon of `week`: the current day plus the next seven.
const weekDays = 8

// weekView groups the occurrences ending after `now` and starting inside the
// next `weekDays` days, one section per day. An empty day stays out of the
// view, and the all-day event of a day sorts first. An overnight event still
// running lands on the current day, not on the day it started.
func weekView(occs []Occurrence, now time.Time) string {
	today := startOfDay(now)
	limit := today.AddDate(0, 0, weekDays)
	days := map[string]*weekDay{}
	for _, occ := range occs {
		if !occ.End.After(now) || !occ.Start.Before(limit) {
			continue
		}
		day := startOfDay(occ.Start)
		if day.Before(today) {
			day = today
		}
		key := day.Format(time.DateOnly)
		group, seen := days[key]
		if !seen {
			group = &weekDay{at: day}
			days[key] = group
		}
		group.occs = append(group.occs, occ)
	}
	if len(days) == 0 {
		return fmt.Sprintf("Nothing in the next %d days.", weekDays)
	}

	keys := make([]string, 0, len(days))
	for key := range days {
		keys = append(keys, key)
	}
	sort.Strings(keys) // The keys are ISO dates, so this orders the days.

	var builder strings.Builder
	fmt.Fprintf(&builder, "📅 Next %d days", weekDays)
	for _, key := range keys {
		group := days[key]
		sort.Slice(group.occs, func(i, j int) bool {
			if group.occs[i].AllDay != group.occs[j].AllDay {
				return group.occs[i].AllDay
			}
			return group.occs[i].Start.Before(group.occs[j].Start)
		})
		fmt.Fprintf(&builder, "\n%s", dayLabel(group.at, today))
		for _, occ := range group.occs {
			builder.WriteString("\n")
			writeOccurrence(&builder, occ, "•")
		}
	}
	return builder.String()
}

// weekDay collects the events of one day of the `week` view.
type weekDay struct {
	at   time.Time
	occs []Occurrence
}

// writeOccurrence appends one event line: a marker, the time or `all day`, the
// summary, and the category when the calendar sets one.
func writeOccurrence(builder *strings.Builder, occ Occurrence, marker string) {
	if occ.AllDay {
		fmt.Fprintf(builder, "%s all day — %s", marker, occ.Summary)
	} else {
		fmt.Fprintf(builder, "%s %s–%s — %s",
			marker, occ.Start.Format("15:04"), occ.End.Format("15:04"), occ.Summary)
	}
	if occ.Category != "" && occ.Category != "-" {
		fmt.Fprintf(builder, "  [%s]", occ.Category)
	}
}

// dayLabel names a day relative to `today` where it helps: `Today` and
// `Tomorrow` carry the date too, so the label still reads as a date.
func dayLabel(day, today time.Time) string {
	switch {
	case day.Equal(today):
		return day.Format("Today, Mon 2 Jan")
	case day.Equal(today.AddDate(0, 0, 1)):
		return day.Format("Tomorrow, Mon 2 Jan")
	default:
		return day.Format("Mon 2 Jan")
	}
}
