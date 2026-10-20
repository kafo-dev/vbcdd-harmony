package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// stateFileName is the file the daemon writes below its data directory.
const stateFileName = "state.json"

// stateMode is the permission of that file, which names no secret but is the
// daemon's own.
const stateMode = 0o600

// State survives a restart: when each job next acts, and when each profile
// last handed off. Both are keyed "<profile>/<job>" and "<profile>".
type State struct {
	Jobs     map[string]time.Time `json:"jobs,omitempty"`
	Handoffs map[string]time.Time `json:"handoffs,omitempty"`
}

// stateFile reads and writes the daemon's state.
type stateFile struct {
	path string

	mu    sync.Mutex
	state State
}

// loadState reads the state, and returns an empty one when the file is absent.
func loadState(path string) (*stateFile, error) {
	file := &stateFile{
		path: path,
		state: State{
			Jobs:     map[string]time.Time{},
			Handoffs: map[string]time.Time{},
		},
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return file, nil
		}
		return nil, err
	}
	if err = json.Unmarshal(data, &file.state); err != nil {
		return nil, err
	}
	if file.state.Jobs == nil {
		file.state.Jobs = map[string]time.Time{}
	}
	if file.state.Handoffs == nil {
		file.state.Handoffs = map[string]time.Time{}
	}
	return file, nil
}

// job returns when a job next acts.
func (f *stateFile) job(key string) (time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	at, ok := f.state.Jobs[key]
	return at, ok
}

// setJob records when a job next acts and writes the state.
func (f *stateFile) setJob(key string, at time.Time) error {
	f.mu.Lock()
	f.state.Jobs[key] = at
	f.mu.Unlock()
	return f.save()
}

// handoff returns when a profile last handed off.
func (f *stateFile) handoff(key string) (time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	at, ok := f.state.Handoffs[key]
	return at, ok
}

// setHandoff records a handoff and writes the state.
func (f *stateFile) setHandoff(key string, at time.Time) error {
	f.mu.Lock()
	f.state.Handoffs[key] = at
	f.mu.Unlock()
	return f.save()
}

// save writes the state through a temporary file, so a process that dies
// mid-write leaves the previous state rather than a truncated one.
func (f *stateFile) save() error {
	f.mu.Lock()
	data, err := json.Marshal(f.state)
	f.mu.Unlock()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err = os.WriteFile(tmp, data, stateMode); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}
