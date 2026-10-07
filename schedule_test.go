package main

import (
	"strings"
	"testing"
	"time"
)

func TestDigestText(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("Etc/GMT+5")
	if err != nil {
		t.Fatal(err)
	}
	b := &Bridge{
		cfg: &Config{loc: loc},
		occs: []Occurrence{
			{
				Summary:  "Anki (Morning)",
				Category: "STUDY",
				Start:    time.Date(2026, 10, 7, 6, 0, 0, 0, loc),
				End:      time.Date(2026, 10, 7, 7, 0, 0, 0, loc),
			},
			{
				Summary: "Holiday",
				AllDay:  true,
				Start:   time.Date(2026, 10, 7, 0, 0, 0, 0, loc),
				End:     time.Date(2026, 10, 8, 0, 0, 0, 0, loc),
			},
			{
				Summary: "Tomorrow",
				Start:   time.Date(2026, 10, 8, 6, 0, 0, 0, loc),
			},
		},
	}
	got := b.digestText(time.Date(2026, 10, 7, 9, 0, 0, 0, loc))
	for _, want := range []string{"Wednesday 7 October", "Anki (Morning)", "06:00–07:00", "Holiday", "[STUDY]"} {
		if !strings.Contains(got, want) {
			t.Errorf("digest missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Tomorrow") {
		t.Errorf("digest leaked another day:\n%s", got)
	}
}

func TestDigestTextEmpty(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("Etc/GMT+5")
	if err != nil {
		t.Fatal(err)
	}
	b := &Bridge{cfg: &Config{loc: loc}}
	got := b.digestText(time.Date(2026, 10, 7, 9, 0, 0, 0, loc))
	if !strings.Contains(got, "No events.") {
		t.Errorf("empty digest = %q", got)
	}
}

func TestFormatAlarm(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("Etc/GMT+5")
	if err != nil {
		t.Fatal(err)
	}
	got := formatAlarm(Fire{
		Occ:    Occurrence{Summary: "Study: Consolidation", Start: time.Date(2026, 10, 7, 7, 15, 0, 0, loc)},
		Offset: -30 * time.Minute,
	})
	if !strings.Contains(got, "Study: Consolidation") || !strings.Contains(got, "30m before") {
		t.Errorf("alarm = %q", got)
	}
}

func TestOccurrenceAlarms(t *testing.T) {
	t.Parallel()
	occ := Occurrence{Alarms: []Trigger{{Offset: -30 * time.Minute}}}
	got := occ.alarms()
	if len(got) != 2 || got[1].Offset != 0 || got[1].RelatedEnd {
		t.Fatalf("alarms = %#v, want a start alarm", got)
	}
	if len(occ.Alarms) != 1 {
		t.Errorf("alarms mutated its receiver: %#v", occ.Alarms)
	}

	occ.Alarms = []Trigger{{Offset: 0}}
	if alarms := occ.alarms(); len(alarms) != 1 {
		t.Errorf("start alarm duplicated: %#v", alarms)
	}

	occ.Alarms = []Trigger{{Offset: 0, RelatedEnd: true}}
	if alarms := occ.alarms(); len(alarms) != 2 {
		t.Errorf("end-relative trigger suppressed the start alarm: %#v", alarms)
	}
}

func TestFireKeyStable(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("Etc/GMT+5")
	if err != nil {
		t.Fatal(err)
	}
	occ := Occurrence{ItemID: "abc", Start: time.Date(2026, 10, 7, 7, 15, 0, 0, loc)}
	tr := Trigger{Offset: -30 * time.Minute}
	first := fireKey(occ, tr)
	if second := fireKey(occ, tr); first != second {
		t.Errorf("fireKey is not stable: %q then %q", first, second)
	}
	if fireKey(occ, tr) == fireKey(occ, Trigger{Offset: -10 * time.Minute}) {
		t.Error("fireKey ignores the offset")
	}
}
