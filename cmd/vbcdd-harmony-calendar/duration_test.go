package main

import (
	"testing"
	"time"
)

func TestParseICalDuration(t *testing.T) {
	t.Parallel()
	cases := map[string]time.Duration{
		"-PT30M":   -30 * time.Minute,
		"-PT10M":   -10 * time.Minute,
		"-PT2M":    -2 * time.Minute,
		"-PT1H":    -time.Hour,
		"-P1D":     -24 * time.Hour,
		"-P3D":     -3 * 24 * time.Hour,
		"-PT15H":   -15 * time.Hour,
		"P1W":      7 * 24 * time.Hour,
		"-P1DT15H": -(24 + 15) * time.Hour,
		"PT2M":     2 * time.Minute,
	}
	for input, want := range cases {
		got, err := parseICalDuration(input)
		if err != nil {
			t.Errorf("parseICalDuration(%q): %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("parseICalDuration(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestParseICalDurationRejects(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"30M", "P", "-P", "PT", "-PTX", "P1M1"} {
		if _, err := parseICalDuration(input); err == nil {
			t.Errorf("parseICalDuration(%q) accepted, want error", input)
		}
	}
}

func TestParseTriggers(t *testing.T) {
	t.Parallel()
	ical := "BEGIN:VCALENDAR\r\n" +
		"BEGIN:VEVENT\r\n" +
		"UID:x@test\r\n" +
		"BEGIN:VALARM\r\n" +
		"TRIGGER:-PT30M\r\n" +
		"ACTION:DISPLAY\r\n" +
		"END:VALARM\r\n" +
		"BEGIN:VALARM\r\n" +
		"TRIGGER;RELATED=END:-PT5M\r\n" +
		"END:VALARM\r\n" +
		"END:VEVENT\r\n" +
		"END:VCALENDAR\r\n"

	triggers := parseTriggers(ical)
	if len(triggers) != 2 {
		t.Fatalf("got %d triggers, want 2", len(triggers))
	}
	if triggers[0].Offset != -30*time.Minute || triggers[0].RelatedEnd {
		t.Errorf("first trigger = %+v", triggers[0])
	}
	if triggers[1].Offset != -5*time.Minute || !triggers[1].RelatedEnd {
		t.Errorf("second trigger = %+v", triggers[1])
	}
	if uid := itemUID(ical); uid != "x@test" {
		t.Errorf("itemUID = %q", uid)
	}
}

func TestHumanDuration(t *testing.T) {
	t.Parallel()
	cases := map[time.Duration]string{
		time.Nanosecond:       "0m",
		time.Microsecond:      "0m",
		time.Millisecond:      "0m",
		time.Second:           "0m",
		time.Minute:           "1m",
		30 * time.Minute:      "30m",
		time.Hour:             "1h",
		90 * time.Minute:      "1h30m",
		3 * 24 * time.Hour:    "3d",
		(24 + 15) * time.Hour: "1d15h",
		14 * 24 * time.Hour:   "14d",
	}
	for input, want := range cases {
		if got := humanDuration(input); got != want {
			t.Errorf("humanDuration(%v) = %q, want %q", input, got, want)
		}
	}
}

func TestClockOn(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("Etc/GMT+5")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 7, 0, 0, 0, 0, loc)
	got, ok := clockOn(day, "07:15")
	if !ok {
		t.Fatal("clockOn failed")
	}
	want := time.Date(2026, 10, 7, 7, 15, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("clockOn = %v, want %v", got, want)
	}
	if _, allDayOK := clockOn(day, "ALL-DAY"); allDayOK {
		t.Error("clockOn accepted ALL-DAY")
	}
}
