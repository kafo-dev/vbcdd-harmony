// Package tool declares and validates the tools a profile exposes to its
// agent. A tool is one TOML file naming a target command line; the target runs
// under the shell, receives a call's arguments as JSON on standard input and
// again as `VBCDD_ARG_<NAME>` variables, and answers on standard output.
package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// ArgType is the type an argument must carry in a call.
type ArgType string

// The types an argument may declare.
const (
	String ArgType = "string"
	Int    ArgType = "int"
	Float  ArgType = "float"
	Bool   ArgType = "bool"
)

// optionalSuffix marks a declared argument the model may omit.
const optionalSuffix = "?"

// Spec is one tool declaration, read from a TOML file below a profile's tools
// directory.
type Spec struct {
	// Description tells the model what the tool does.
	Description string `toml:"description"`
	// Args declares the arguments, as `name = "type"` pairs.
	Args map[string]string `toml:"args"`
	// Target is the command line the shell runs. It may be a pipeline or a
	// compound command, not only one executable.
	Target string `toml:"target"`
}

// Arg is one argument a tool declares.
type Arg struct {
	Name     string
	Type     ArgType
	Optional bool
}

// Parse reads a tool declaration from TOML bytes.
func Parse(data []byte) (Spec, error) {
	var spec Spec
	if err := toml.Unmarshal(data, &spec); err != nil {
		return Spec{}, err
	}
	if err := spec.check(); err != nil {
		return Spec{}, err
	}
	return spec, nil
}

// Arguments returns the declared arguments, sorted by name.
func (s Spec) Arguments() ([]Arg, error) {
	args := make([]Arg, 0, len(s.Args))
	for name, declared := range s.Args {
		arg, err := parseArg(name, declared)
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
	}
	sort.Slice(args, func(i, j int) bool { return args[i].Name < args[j].Name })
	return args, nil
}

// Validate checks a call's arguments against the declaration and returns them
// in the shape the target receives. A missing required argument, a wrong type,
// and an undeclared argument each fail before the target runs.
func (s Spec) Validate(raw json.RawMessage) (map[string]any, error) {
	call, err := decode(raw)
	if err != nil {
		return nil, err
	}
	args, err := s.Arguments()
	if err != nil {
		return nil, err
	}
	for _, arg := range args {
		value, present := call[arg.Name]
		if !present {
			if !arg.Optional {
				return nil, fmt.Errorf("argument %q is required", arg.Name)
			}
			continue
		}
		if err = arg.check(value); err != nil {
			return nil, err
		}
	}
	for _, name := range sortedKeys(call) {
		if _, declared := s.Args[name]; !declared {
			return nil, fmt.Errorf("argument %q is not declared", name)
		}
	}
	return call, nil
}

// check rejects a declaration the file itself proves wrong.
func (s Spec) check() error {
	if strings.TrimSpace(s.Target) == "" {
		return fmt.Errorf("target is empty")
	}
	if strings.TrimSpace(s.Description) == "" {
		return fmt.Errorf("description is empty")
	}
	_, err := s.Arguments()
	return err
}

// parseArg reads one `name = "type"` pair. A trailing `?` marks the argument
// optional.
func parseArg(name, declared string) (Arg, error) {
	optional := strings.HasSuffix(declared, optionalSuffix)
	kind := ArgType(strings.TrimSuffix(declared, optionalSuffix))
	switch kind {
	case String, Int, Float, Bool:
		return Arg{Name: name, Type: kind, Optional: optional}, nil
	}
	return Arg{}, fmt.Errorf("argument %q declares the unknown type %q", name, declared)
}

// check reports whether a value satisfies the declared type.
func (a Arg) check(value any) error {
	switch a.Type {
	case String:
		if _, ok := value.(string); ok {
			return nil
		}
	case Int:
		if number, ok := value.(json.Number); ok {
			if _, err := number.Int64(); err == nil {
				return nil
			}
		}
	case Float:
		if _, ok := value.(json.Number); ok {
			return nil
		}
	case Bool:
		if _, ok := value.(bool); ok {
			return nil
		}
	}
	return fmt.Errorf("argument %q must be of type %s", a.Name, a.Type)
}

// decode reads a call's arguments as an object. Numbers keep their literal
// text, so an integer reaches the target unchanged.
func decode(raw json.RawMessage) (map[string]any, error) {
	call := map[string]any{}
	if len(bytes.TrimSpace(raw)) == 0 {
		return call, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&call); err != nil {
		return nil, fmt.Errorf("arguments are not a JSON object: %w", err)
	}
	return call, nil
}

// sortedKeys returns a map's keys in a stable order, so a message about a bad
// argument does not move between runs.
func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
