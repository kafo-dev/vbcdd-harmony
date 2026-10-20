package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	// occurrenceFields is the count of tab-separated fields the expansion
	// script prints for one occurrence.
	occurrenceFields = 7

	// clockParts is the count of fields in an `HH:MM` wall clock.
	clockParts = 2

	// scanStart and scanMax bound one line of the expander's output.
	scanStart = 64 << 10
	scanMax   = 1 << 20
)

// Item is one CalDAV item as returned by `calendula --json items list`.
type Item struct {
	ID     string    `json:"id"`
	ICAL   string    `json:"ical"`
	UID    string    `json:"-"`
	Alarms []Trigger `json:"-"`
	cal    string
}

// Occurrence is one expanded start of an item.
type Occurrence struct {
	ItemID    string
	Summary   string
	Category  string
	Transp    string
	Component string
	Start     time.Time
	End       time.Time
	AllDay    bool
	UID       string
	Alarms    []Trigger
}

// Calendar wraps the `calendula` CLI and the recurrence expander.
type Calendar struct {
	Bin       string
	Config    string
	Expand    string
	Calendars []string
	Loc       *time.Location
	StateDir  string
}

// List returns every item of every configured calendar, with its alarms.
func (c *Calendar) List(ctx context.Context) ([]Item, error) {
	var items []Item
	for _, cal := range c.Calendars {
		// The binary, its config, and the calendar name come from the config
		// file, which the owner controls.
		args := []string{"-c", c.Config, "--json", "items", "list", cal}
		cmd := exec.CommandContext(ctx, c.Bin, args...) //nolint:gosec // G204: the arguments come from the config file.
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("list %s: %w: %s", cal, err, strings.TrimSpace(stderr.String()))
		}
		var batch []Item
		if err = json.Unmarshal(out, &batch); err != nil {
			return nil, fmt.Errorf("parse %s: %w", cal, err)
		}
		for i := range batch {
			batch[i].cal = cal
		}
		items = append(items, batch...)
	}
	for i := range items {
		items[i].UID = itemUID(items[i].ICAL)
		items[i].Alarms = parseTriggers(items[i].ICAL)
	}
	return items, nil
}

// Occurrences expands every item into occurrences between `from` and `to`
// inclusive, in the configured location.
func (c *Calendar) Occurrences(ctx context.Context, from, to time.Time) ([]Occurrence, error) {
	items, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(c.StateDir, "items-*.json")
	if err != nil {
		return nil, err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(payload); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	if err = f.Close(); err != nil {
		return nil, err
	}

	// The expander script and the temporary file come from the config file
	// and the state directory.
	cmd := exec.CommandContext(ctx, c.Expand, name, //nolint:gosec // G204: the arguments come from the config file.
		"--from", from.Format("2006-01-02"),
		"--to", to.Format("2006-01-02"),
		"--tz", c.Loc.String())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("expand recurrences: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	itemsByID := make(map[string]*Item, len(items))
	for i := range items {
		itemsByID[items[i].ID] = &items[i]
	}

	var occs []Occurrence
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, scanStart), scanMax)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		if len(fields) < occurrenceFields {
			continue
		}
		if strings.HasPrefix(fields[6], "UNSUPPORTED") {
			continue
		}
		occ, ok := c.parseOccurrence(fields)
		if !ok {
			continue
		}
		if item, found := itemsByID[occ.ItemID]; found {
			occ.UID = item.UID
			occ.Alarms = item.Alarms
		}
		occs = append(occs, occ)
	}
	if err = sc.Err(); err != nil {
		return nil, err
	}
	return occs, nil
}

func (c *Calendar) parseOccurrence(fields []string) (Occurrence, bool) {
	day, err := time.ParseInLocation("2006-01-02", fields[0], c.Loc)
	if err != nil {
		return Occurrence{}, false
	}
	occ := Occurrence{
		ItemID:    fields[6],
		Summary:   fields[2],
		Category:  fields[3],
		Transp:    fields[4],
		Component: fields[5],
	}
	if fields[1] == "ALL-DAY" {
		occ.AllDay = true
		occ.Start = day
		occ.End = day.Add(dayDuration)
		return occ, true
	}
	startPart, endPart, _ := strings.Cut(fields[1], "-")
	start, startOK := clockOn(day, startPart)
	if !startOK {
		return Occurrence{}, false
	}
	occ.Start = start
	if end, endOK := clockOn(day, endPart); endOK {
		if end.Before(start) {
			end = end.Add(dayDuration)
		}
		occ.End = end
	} else {
		occ.End = start
	}
	return occ, true
}

// clockOn attaches an `HH:MM` wall clock to a day in the calendar location.
func clockOn(day time.Time, clock string) (time.Time, bool) {
	parts := strings.Split(clock, ":")
	if len(parts) != clockParts {
		return time.Time{}, false
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return time.Time{}, false
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	return day.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute), true
}
