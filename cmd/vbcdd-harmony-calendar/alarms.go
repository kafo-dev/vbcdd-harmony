package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Fire is one alarm of one occurrence.
type Fire struct {
	// Key identifies the alarm across restarts.
	Key string
	// At is when it fires.
	At time.Time
	// Occ is the occurrence it belongs to.
	Occ Occurrence
	// Offset is how far before the start it fires, zero for an alarm at the
	// start.
	Offset time.Duration
}

// fires returns every alarm of the occurrences, sorted by the time it fires,
// and leaving out the ones that fired more than a moment ago.
func fires(occs []Occurrence, now time.Time) []Fire {
	cutoff := now.Add(-2 * time.Minute)
	var due []Fire
	for _, occ := range occs {
		for _, trigger := range alarmsOf(occ) {
			at := trigger.FireTime(occ.Start, occ.End)
			if at.Before(cutoff) {
				continue
			}
			due = append(due, Fire{Key: fireKey(occ, trigger), At: at, Occ: occ, Offset: trigger.Offset})
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].At.Before(due[j].At) })
	return due
}

// alarmsOf returns the triggers of an occurrence plus an alarm at its start,
// unless a trigger already fires as the event begins.
func alarmsOf(occ Occurrence) []Trigger {
	for _, trigger := range occ.Alarms {
		if !trigger.Absolute && !trigger.RelatedEnd && trigger.Offset == 0 {
			return occ.Alarms
		}
	}
	return append(append([]Trigger(nil), occ.Alarms...), Trigger{})
}

// fireKey identifies one alarm of one occurrence across restarts.
func fireKey(occ Occurrence, trigger Trigger) string {
	suffix := trigger.Offset.String()
	switch {
	case trigger.Absolute:
		suffix = "at:" + trigger.At.UTC().Format(time.RFC3339)
	case trigger.RelatedEnd:
		suffix = "end:" + suffix
	}
	return occ.ItemID + "|" + occ.Start.UTC().Format(time.RFC3339) + "|" + suffix
}

// formatAlarm turns an alarm into the one line the owner reads.
func formatAlarm(f Fire) string {
	when := f.Occ.Start.Format("Mon 2 Jan 15:04")
	if f.Occ.AllDay {
		when = f.Occ.Start.Format("Mon 2 Jan") + ", all day"
	}
	body := fmt.Sprintf("⏰ %s — %s", f.Occ.Summary, when)
	if f.Offset != 0 {
		body += fmt.Sprintf(" (alarm %s before)", humanDuration(-f.Offset))
	}
	return body
}

// humanDuration renders a positive duration as "3d", "2h30m", or "10m".
func humanDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	d = d.Round(time.Minute)
	days := d / dayDuration
	hours := d / time.Hour % hoursPerDay
	minutes := d / time.Minute % minutesPerHour

	switch {
	case days > 0 && hours > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case days > 0:
		return fmt.Sprintf("%dd", days)
	case hours > 0 && minutes > 0:
		return fmt.Sprintf("%dh%dm", hours, minutes)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}

// startOfDay returns local midnight of the day `t` falls on.
func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// upcoming lists the occurrences that end after `now` and start before local
// midnight. An all-day event sorts first; the rest sort by start.
func upcoming(occs []Occurrence, now time.Time) string {
	dayEnd := startOfDay(now).Add(dayDuration)
	var today []Occurrence
	for _, occ := range occs {
		if occ.End.After(now) && occ.Start.Before(dayEnd) {
			today = append(today, occ)
		}
	}
	if len(today) == 0 {
		return "Nothing else today."
	}
	sort.Slice(today, func(i, j int) bool {
		if today[i].AllDay != today[j].AllDay {
			return today[i].AllDay
		}
		return today[i].Start.Before(today[j].Start)
	})

	var builder strings.Builder
	for i, occ := range today {
		if i > 0 {
			builder.WriteString("\n")
		}
		marker := "•"
		if !occ.Start.After(now) && occ.End.After(now) {
			marker = "▶"
		}
		writeOccurrence(&builder, occ, marker)
	}
	return builder.String()
}
