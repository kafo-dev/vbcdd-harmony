// Package profile loads the profiles a daemon serves. A profile is one
// directory below the profiles root: the instructions that open its
// conversation, the documentation its agent reads on demand, the tools its
// agent may call, the hooks that rewrite or answer a message before the agent
// sees it, and the timed jobs it runs.
package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"vbcdd.kafo.dev/harmony/internal/tool"
)

// The files and directories a profile directory holds.
const (
	// FileName is the file that declares a profile.
	FileName = "profile.toml"
	// HandoffName is the file that holds the handoff instruction.
	HandoffName = "handoff.md"
	// InstructionsDirName holds the instructions that open the conversation.
	InstructionsDirName = "instructions"
	// DocsDirName holds the documentation the agent reads on demand.
	DocsDirName = "docs"
	// ToolsDirName holds one TOML file per tool.
	ToolsDirName = "tools"
)

// Mode is how a hook changes a run.
type Mode string

// The ways a hook can change a run.
const (
	// Alter replaces the prompt with the hook's output; the agent still runs.
	Alter Mode = "alter"
	// Override answers instead of the agent.
	Override Mode = "override"
)

// Profile is one named configuration.
type Profile struct {
	// Name is the profile directory name, which a topic maps to.
	Name string
	// Dir is the profile directory.
	Dir string
	// Instructions opens every conversation, from the instruction files.
	Instructions string
	// Docs are the files below the docs directory, sorted by name.
	Docs []Doc
	// Tools are the tools below the tools directory, sorted by name.
	Tools []Tool
	// Hooks run before a message reaches the agent, in declaration order.
	Hooks []Hook
	// Jobs run on a timer.
	Jobs []Job
	// Handoff is where the conversation is handed off to a fresh one. Nil
	// leaves the profile without one.
	Handoff *Handoff

	enabled *bool
}

// Handoff describes when one conversation gives way to a fresh one, and what
// asks the outgoing conversation to write down for it.
type Handoff struct {
	// After hands off at the schedule's next occurrence.
	After Schedule `toml:"after"`
	// Tokens hands off once the conversation reaches this many tokens.
	Tokens int `toml:"tokens"`
	// Bytes hands off once the stored conversation reaches this many bytes.
	Bytes int64 `toml:"bytes"`
	// Prompt is the instruction, from the handoff file. It runs in the
	// outgoing conversation, and its answer is the first message of the new
	// one.
	Prompt string `toml:"-"`
}

// Due reports whether a token or byte threshold has been met. A threshold of
// zero is not set.
func (h Handoff) Due(tokens int, bytes int64) bool {
	if h.Tokens > 0 && tokens >= h.Tokens {
		return true
	}
	return h.Bytes > 0 && bytes >= h.Bytes
}

// On reports whether the profile serves its topic. A profile is on unless its
// declaration turns it off.
func (p Profile) On() bool {
	return p.enabled == nil || *p.enabled
}

// Tool is one tool a profile offers, with the name the model calls it by. The
// name comes from the file name.
type Tool struct {
	Name string
	Spec tool.Spec
}

// Doc is one documentation file a profile lists for its agent to read on
// demand. The agent sees the path and the description; the body stays on disk.
type Doc struct {
	Name        string
	Path        string
	Description string
}

// Hook is one command that runs before a message reaches the agent.
type Hook struct {
	Name    string `toml:"name"`
	Command string `toml:"command"`
	Mode    Mode   `toml:"mode"`
	// Match is a regular expression the message body must match. Empty
	// matches every message.
	Match string `toml:"match"`

	match *regexp.Regexp
}

// Matches reports whether the hook applies to a message body.
func (h Hook) Matches(body string) bool {
	if h.match == nil {
		return true
	}
	return h.match.MatchString(body)
}

// Compile returns the hook with its pattern ready to match. A hook that names
// no pattern matches every message.
func (h Hook) Compile() (Hook, error) {
	if h.Match == "" {
		return h, nil
	}
	pattern, err := regexp.Compile(h.Match)
	if err != nil {
		return Hook{}, fmt.Errorf("match %q: %w", h.Match, err)
	}
	h.match = pattern
	return h, nil
}

// Job is one timed run.
//
// Exactly one of Prompt and Command is required: a prompt calls the agent,
// while a command runs on its own and its output goes straight to the reader.
type Job struct {
	Name    string   `toml:"name"`
	Enabled *bool    `toml:"enabled"`
	Run     Schedule `toml:"run"`
	Prompt  string   `toml:"prompt"`
	Command string   `toml:"command"`
	// Next names a command that runs before the scheduled run and reports
	// when to run next, when to poll again, and optionally a prompt or a
	// command that replaces the job's own.
	Next string `toml:"next"`
}

// On reports whether the job runs. A job is on unless its declaration turns it
// off.
func (j Job) On() bool {
	return j.Enabled == nil || *j.Enabled
}

// Schedule is when a job runs: a time of day, with an optional weekday, or an
// interval.
type Schedule struct {
	Time            string `toml:"time"`
	Weekday         string `toml:"weekday"`
	IntervalMinutes int    `toml:"interval_minutes"`
}

// file is the shape of profile.toml.
type file struct {
	Enabled *bool    `toml:"enabled"`
	Handoff *Handoff `toml:"handoff"`
	Hooks   []Hook   `toml:"hooks"`
	Jobs    []Job    `toml:"jobs"`
}

// nameRe is the character set a profile directory name may use.
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// toolNameRe is the character set a tool file name may use.
var toolNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// clockRe is a time of day, from midnight to one minute before the next one.
// [time.Parse] accepts a single-digit hour, which reads as 5:32 in one place
// and 05:32 in another.
var clockRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):([0-5][0-9])$`)

// weekdays maps the abbreviation a schedule names to its day. [time.Parse]
// cannot do this: it takes the layout's weekday field for a date to check
// against, not for the answer, so parsing "Sun" with the layout "Mon" reports
// the weekday of the reference date.
var weekdays = map[string]time.Weekday{
	"Sun": time.Sunday,
	"Mon": time.Monday,
	"Tue": time.Tuesday,
	"Wed": time.Wednesday,
	"Thu": time.Thursday,
	"Fri": time.Friday,
	"Sat": time.Saturday,
}

// Clock reads a time of day and returns its hour and its minute.
func Clock(value string) (int, int, error) {
	parts := clockRe.FindStringSubmatch(value)
	if parts == nil {
		return 0, 0, fmt.Errorf("time %q is not HH:MM", value)
	}
	hour, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("time %q is not HH:MM", value)
	}
	minute, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, 0, fmt.Errorf("time %q is not HH:MM", value)
	}
	return hour, minute, nil
}

// Day returns the weekday an abbreviation names.
func Day(value string) (time.Weekday, error) {
	day, ok := weekdays[value]
	if !ok {
		return 0, fmt.Errorf("weekday %q is not a weekday abbreviation", value)
	}
	return day, nil
}

// LoadAll reads every profile below the profiles root, sorted by name. A
// directory whose name is not a valid profile name is ignored; one that is
// must hold a valid declaration.
func LoadAll(root string) ([]Profile, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var (
		profiles []Profile
		loaded   Profile
	)
	for _, entry := range entries {
		if !entry.IsDir() || !nameRe.MatchString(entry.Name()) {
			continue
		}
		loaded, err = Load(filepath.Join(root, entry.Name()))
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, loaded)
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	return profiles, nil
}

// Load reads one profile from its directory.
func Load(dir string) (Profile, error) {
	name := filepath.Base(dir)
	if !nameRe.MatchString(name) {
		return Profile{}, fmt.Errorf("profile %s: %q is not a valid profile name", dir, name)
	}
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		return Profile{}, err
	}
	var declared file
	if err = toml.Unmarshal(raw, &declared); err != nil {
		return Profile{}, fmt.Errorf("%s: %w", filepath.Join(dir, FileName), err)
	}
	p := Profile{
		Name:    name,
		Dir:     dir,
		enabled: declared.Enabled,
	}
	if p.Instructions, err = loadInstructions(dir); err != nil {
		return Profile{}, err
	}
	if p.Docs, err = loadDocs(dir); err != nil {
		return Profile{}, err
	}
	if p.Tools, err = loadTools(dir); err != nil {
		return Profile{}, err
	}
	if p.Hooks, err = checkHooks(dir, declared.Hooks); err != nil {
		return Profile{}, err
	}
	if p.Jobs, err = checkJobs(dir, declared.Jobs); err != nil {
		return Profile{}, err
	}
	if declared.Handoff != nil {
		if p.Handoff, err = checkHandoff(dir, declared.Handoff); err != nil {
			return Profile{}, err
		}
	}
	return p, nil
}

// loadInstructions reads the instruction files, sorted by name and joined into
// one message. A profile may carry none.
func loadInstructions(dir string) (string, error) {
	root := filepath.Join(dir, InstructionsDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var (
		parts []string
		body  []byte
	)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		if body, err = os.ReadFile(filepath.Join(root, entry.Name())); err != nil {
			return "", err
		}
		if text := strings.TrimSpace(string(body)); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

// loadDocs reads the documentation files, sorted by name so the list they build
// does not move between runs. Each file names itself to the agent through the
// description in its frontmatter.
func loadDocs(dir string) ([]Doc, error) {
	root := filepath.Join(dir, DocsDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var (
		docs []Doc
		body []byte
		note string
	)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if body, err = os.ReadFile(path); err != nil {
			return nil, err
		}
		if note, err = description(path, string(body)); err != nil {
			return nil, err
		}
		docs = append(docs, Doc{Name: entry.Name(), Path: path, Description: note})
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Name < docs[j].Name })
	return docs, nil
}

// loadTools reads the tool declarations, sorted by name.
func loadTools(dir string) ([]Tool, error) {
	root := filepath.Join(dir, ToolsDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var (
		tools []Tool
		spec  tool.Spec
		raw   []byte
	)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".toml")
		if !toolNameRe.MatchString(name) {
			return nil, fmt.Errorf("%s: %q is not a valid tool name", root, name)
		}
		path := filepath.Join(root, entry.Name())
		if raw, err = os.ReadFile(path); err != nil {
			return nil, err
		}
		if spec, err = tool.Parse(raw); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		tools = append(tools, Tool{Name: name, Spec: spec})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools, nil
}

// checkHooks validates the hook declarations and compiles their patterns.
func checkHooks(dir string, hooks []Hook) ([]Hook, error) {
	for i, hook := range hooks {
		where := fmt.Sprintf("%s: hook %d", filepath.Join(dir, FileName), i+1)
		if hook.Name != "" {
			where = fmt.Sprintf("%s: hook %q", filepath.Join(dir, FileName), hook.Name)
		}
		if strings.TrimSpace(hook.Command) == "" {
			return nil, fmt.Errorf("%s: command is empty", where)
		}
		switch hook.Mode {
		case Alter, Override:
		default:
			return nil, fmt.Errorf("%s: mode %q is not %q or %q", where, hook.Mode, Alter, Override)
		}
		compiled, err := hook.Compile()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		hooks[i] = compiled
	}
	return hooks, nil
}

// checkHandoff validates a declared handoff and reads its instruction.
func checkHandoff(dir string, declared *Handoff) (*Handoff, error) {
	where := filepath.Join(dir, FileName)
	if declared.Tokens < 0 || declared.Bytes < 0 {
		return nil, fmt.Errorf("%s: handoff thresholds cannot be negative", where)
	}
	if declared.Tokens == 0 && declared.Bytes == 0 && declared.After == (Schedule{}) {
		return nil, fmt.Errorf("%s: handoff names no threshold", where)
	}
	if declared.After != (Schedule{}) {
		if err := declared.After.check(); err != nil {
			return nil, fmt.Errorf("%s: handoff: %w", where, err)
		}
	}
	path := filepath.Join(dir, HandoffName)
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(body)) == "" {
		return nil, fmt.Errorf("%s: handoff instruction is empty", path)
	}
	declared.Prompt = string(body)
	return declared, nil
}

// checkJobs validates the job declarations.
func checkJobs(dir string, jobs []Job) ([]Job, error) {
	for i, job := range jobs {
		where := fmt.Sprintf("%s: job %d", filepath.Join(dir, FileName), i+1)
		if job.Name == "" {
			return nil, fmt.Errorf("%s: name is empty", where)
		}
		where = fmt.Sprintf("%s: job %q", filepath.Join(dir, FileName), job.Name)
		switch {
		case strings.TrimSpace(job.Prompt) == "" && strings.TrimSpace(job.Command) == "":
			return nil, fmt.Errorf("%s: one of prompt or command is required", where)
		case strings.TrimSpace(job.Prompt) != "" && strings.TrimSpace(job.Command) != "":
			return nil, fmt.Errorf("%s: prompt and command are exclusive", where)
		}
		if err := job.Run.check(); err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
	}
	return jobs, nil
}

// check reports whether a schedule names exactly one cadence.
func (s Schedule) check() error {
	switch {
	case s.IntervalMinutes > 0 && (s.Time != "" || s.Weekday != ""):
		return fmt.Errorf("run names both interval_minutes and a time")
	case s.IntervalMinutes > 0:
		return nil
	case s.Time == "":
		return fmt.Errorf("run names neither time nor interval_minutes")
	}
	if _, _, err := Clock(s.Time); err != nil {
		return err
	}
	if s.Weekday == "" {
		return nil
	}
	if _, err := Day(s.Weekday); err != nil {
		return err
	}
	return nil
}
