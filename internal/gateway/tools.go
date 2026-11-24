package gateway

import (
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// namespaceArg is the argument an infrapi API's tools take for the
// namespace they act in.
const namespaceArg = "namespace"

// tool is one tool of an API as ulgen serves it.
type tool struct {
	api *api
	// original is the API's own name for it.
	original string
	// prefixed is the tool as the aggregate endpoint lists it:
	// <short>_<original>.
	prefixed *mcp.Tool
}

// serve builds the served form of an API's tool: its input schema as the
// API gives it, a copy of which the aggregate lists under a prefixed name.
func serve(a *api, t *mcp.Tool) (*tool, error) {
	schema, err := objectSchema(t.InputSchema)
	if err != nil {
		return nil, err
	}
	return &tool{api: a, original: t.Name, prefixed: &mcp.Tool{
		Name:         a.Short + "_" + t.Name,
		Title:        t.Title,
		Description:  t.Description,
		InputSchema:  schema,
		OutputSchema: t.OutputSchema,
		Annotations:  t.Annotations,
	}}, nil
}

// own is the tool under the API's own name, for the API's endpoint.
func (t *tool) own() *mcp.Tool {
	c := *t.prefixed
	c.Name = t.original
	return &c
}

// objectSchema returns a deep copy of an input schema as a JSON object.
func objectSchema(s any) (map[string]any, error) {
	if s == nil {
		return map[string]any{"type": "object"}, nil
	}
	data, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("its input schema doesn't encode: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil || m == nil {
		return nil, fmt.Errorf("its input schema is not a JSON object")
	}
	if t, ok := m["type"]; ok && t != "object" {
		return nil, fmt.Errorf("its input schema is not of type object")
	}
	return m, nil
}
