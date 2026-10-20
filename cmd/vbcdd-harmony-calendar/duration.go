package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Trigger is one `VALARM` entry of an item: either an offset from the
// occurrence start (or end), or an absolute instant.
type Trigger struct {
	Offset     time.Duration
	RelatedEnd bool
	Absolute   bool
	At         time.Time
}

// Calendar arithmetic. `time.Hour` is already a Duration, so a product of two
// Duration values would be a bug; these counts keep every product scalar.
const (
	hoursPerDay    = 24
	minutesPerHour = 60
	daysPerWeek    = 7

	// dayDuration is one wall-clock day, the length of an all-day event and
	// the step from one day to the next.
	dayDuration = hoursPerDay * time.Hour
)

// FireTime returns the instant this trigger fires for an occurrence.
func (t Trigger) FireTime(start, end time.Time) time.Time {
	if t.Absolute {
		return t.At
	}
	if t.RelatedEnd {
		return end.Add(t.Offset)
	}
	return start.Add(t.Offset)
}

// parseICalDuration parses an RFC 5545 duration, as used by `TRIGGER`, e.g.
// `-PT30M`, `-P3D`, `P1W`, `-PT15H`.
func parseICalDuration(s string) (time.Duration, error) {
	orig := s
	sign := 1
	switch {
	case strings.HasPrefix(s, "-"):
		sign = -1
		s = s[1:]
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	}
	if !strings.HasPrefix(s, "P") {
		return 0, fmt.Errorf("invalid duration %q", orig)
	}
	s = s[1:]

	var (
		total time.Duration
		num   string
		seen  bool
	)
	for _, r := range s {
		if r >= '0' && r <= '9' {
			num += string(r)
			continue
		}
		if r == 'T' {
			if num != "" {
				return 0, fmt.Errorf("invalid duration %q", orig)
			}
			continue
		}
		n, err := strconv.Atoi(num)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", orig)
		}
		seen = true
		switch r {
		case 'W':
			total += time.Duration(n*daysPerWeek*hoursPerDay) * time.Hour
		case 'D':
			total += time.Duration(n*hoursPerDay) * time.Hour
		case 'H':
			total += time.Duration(n) * time.Hour
		case 'M':
			total += time.Duration(n) * time.Minute
		case 'S':
			total += time.Duration(n) * time.Second
		default:
			return 0, fmt.Errorf("invalid duration %q", orig)
		}
		num = ""
	}
	if !seen || num != "" {
		return 0, fmt.Errorf("invalid duration %q", orig)
	}
	if sign < 0 {
		return -total, nil
	}
	return total, nil
}

var (
	alarmBlockRe  = regexp.MustCompile(`(?is)BEGIN:VALARM(.*?)END:VALARM`)
	triggerLineRe = regexp.MustCompile(`(?i)^TRIGGER([^:]*):(.*)$`)
	uidLineRe     = regexp.MustCompile(`(?im)^UID:(.*)$`)
)

// unfoldICal removes the CRLF + space line folding iCalendar uses.
func unfoldICal(s string) string {
	s = strings.ReplaceAll(s, "\r\n ", "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// parseTriggers returns every `VALARM` trigger of one item.
func parseTriggers(ical string) []Trigger {
	text := unfoldICal(ical)
	var out []Trigger
	for _, block := range alarmBlockRe.FindAllStringSubmatch(text, -1) {
		for line := range strings.SplitSeq(block[1], "\n") {
			m := triggerLineRe.FindStringSubmatch(strings.TrimRight(line, "\r"))
			if m == nil {
				continue
			}
			params := strings.ToUpper(m[1])
			value := strings.TrimSpace(m[2])
			if strings.Contains(params, "VALUE=DATE-TIME") {
				if at, err := time.Parse("20060102T150405Z", value); err == nil {
					out = append(out, Trigger{Absolute: true, At: at})
				}
				continue
			}
			offset, err := parseICalDuration(value)
			if err != nil {
				continue
			}
			out = append(out, Trigger{
				Offset:     offset,
				RelatedEnd: strings.Contains(params, "RELATED=END"),
			})
		}
	}
	return out
}

// itemUID returns the `UID` of the first component in an item, or "".
func itemUID(ical string) string {
	if m := uidLineRe.FindStringSubmatch(unfoldICal(ical)); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}
