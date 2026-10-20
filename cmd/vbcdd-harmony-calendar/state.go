package main

import (
	"encoding/json"
	"os"
	"time"
)

// stateFile remembers the alarms already delivered, so a restart does not
// deliver them again.
type stateFile struct {
	path string
	// fired is the set of alarm keys already delivered.
	fired map[string]bool
}

// openState reads the state, and returns an empty one when the file is absent.
func openState(path string) (*stateFile, error) {
	file := &stateFile{path: path, fired: map[string]bool{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return file, nil
		}
		return nil, err
	}
	var stored struct {
		Fired []string `json:"fired"`
	}
	if err = json.Unmarshal(data, &stored); err != nil {
		return nil, err
	}
	for _, key := range stored.Fired {
		file.fired[key] = true
	}
	return file, nil
}

// delivered reports whether an alarm has already been handed over.
func (f *stateFile) delivered(key string) bool {
	return f.fired[key]
}

// deliver records an alarm as handed over.
func (f *stateFile) deliver(key string) {
	f.fired[key] = true
}

// prune drops the keys that no longer name an alarm of the current window, so
// the file does not grow without end.
func (f *stateFile) prune(keys map[string]bool) {
	for key := range f.fired {
		if !keys[key] {
			delete(f.fired, key)
		}
	}
}

// save writes the state.
func (f *stateFile) save() error {
	keys := make([]string, 0, len(f.fired))
	for key := range f.fired {
		keys = append(keys, key)
	}
	data, err := json.Marshal(struct {
		Fired []string `json:"fired"`
	}{Fired: keys})
	if err != nil {
		return err
	}
	return os.WriteFile(f.path, data, 0o600)
}

// now reads the clock. It is a variable so a test can hold it still.
var now = time.Now
