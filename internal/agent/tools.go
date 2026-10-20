package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"charm.land/fantasy"

	"vbcdd.kafo.dev/harmony/internal/command"
	"vbcdd.kafo.dev/harmony/internal/tool"
)

// NewTool adapts one declared tool to the SDK. Its schema comes from the
// declaration rather than from a Go struct, so one type serves every tool a
// profile declares.
func NewTool(name string, spec tool.Spec, runner command.Runner) (fantasy.AgentTool, error) {
	info, err := toolInfo(name, spec)
	if err != nil {
		return nil, fmt.Errorf("tool %s: %w", name, err)
	}
	return &dynamicTool{info: info, spec: spec, runner: runner}, nil
}

// dynamicTool runs a declared tool through the command runner.
type dynamicTool struct {
	info    fantasy.ToolInfo
	spec    tool.Spec
	runner  command.Runner
	options fantasy.ProviderOptions
}

// Info describes the tool to the model.
func (t *dynamicTool) Info() fantasy.ToolInfo { return t.info }

// ProviderOptions returns the endpoint-specific settings of this tool.
func (t *dynamicTool) ProviderOptions() fantasy.ProviderOptions { return t.options }

// SetProviderOptions stores the endpoint-specific settings of this tool.
func (t *dynamicTool) SetProviderOptions(options fantasy.ProviderOptions) { t.options = options }

// Run validates the call and runs the target.
//
// A rejection and a failed target both come back as an error response, which
// reaches the model as text it can act on rather than ending the turn.
func (t *dynamicTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	args, err := t.spec.Validate(json.RawMessage(call.Input))
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	out, err := t.runner.Run(ctx, t.spec.Target, args)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return fantasy.NewTextResponse(out), nil
}

// toolInfo builds the schema the model reads from a declaration.
func toolInfo(name string, spec tool.Spec) (fantasy.ToolInfo, error) {
	args, err := spec.Arguments()
	if err != nil {
		return fantasy.ToolInfo{}, err
	}
	properties := make(map[string]any, len(args))
	required := make([]string, 0, len(args))
	for _, arg := range args {
		properties[arg.Name] = map[string]any{"type": jsonType(arg.Type)}
		if !arg.Optional {
			required = append(required, arg.Name)
		}
	}
	return fantasy.ToolInfo{
		Name:        name,
		Description: spec.Description,
		Parameters: map[string]any{
			"type":       "object",
			"properties": properties,
		},
		Required: required,
	}, nil
}

// jsonType maps a declared argument type to the name JSON Schema gives it.
func jsonType(kind tool.ArgType) string {
	switch kind {
	case tool.String:
		return "string"
	case tool.Int:
		return "integer"
	case tool.Float:
		return "number"
	case tool.Bool:
		return "boolean"
	}
	return "string"
}
