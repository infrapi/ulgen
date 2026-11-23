package gateway

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/infrapi/ulgen/internal/config"
)

// namespaceArg is the argument every namespace-scoped tool takes, whatever its
// upstream calls it.
const namespaceArg = "namespace"

const namespaceDescription = "The Kayra namespace to act in. The list_namespaces tool gives the ones you belong to."

// tool is one upstream tool as ulgen exposes it.
type tool struct {
	upstream *upstream
	// original is the upstream's own name.
	original string
	// exposed is the tool as listed to clients.
	exposed *mcp.Tool
	// scoped: the tool takes a namespace.
	scoped bool
	// resource and verb are what /check is asked for (authorize: ulgen).
	resource string
	verb     string
}

// verbOf maps a tool's MCP annotations to the Kayra verb its call needs
// on resource "tools": read-only tools need "get", destructive ones
// "delete", the other writes "create". MCP says a tool without
// annotations is neither read-only nor non-destructive, so it needs
// "delete": a tool nobody described gets the strictest check.
func verbOf(t *mcp.Tool) string {
	a := t.Annotations
	switch {
	case a == nil:
		return "delete"
	case a.ReadOnlyHint:
		return "get"
	case a.DestructiveHint == nil || *a.DestructiveHint:
		return "delete"
	default:
		return "create"
	}
}

// expose builds the exposed form of an upstream tool, or explains why it
// can't be exposed.
func expose(u *upstream, t *mcp.Tool, rule *config.ToolRule) (*tool, error) {
	schema, err := objectSchema(t.InputSchema)
	if err != nil {
		return nil, err
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
	}
	required := stringList(schema["required"])

	scoped := false
	switch {
	case u.cfg.Namespace.Inject:
		if _, taken := props[namespaceArg]; taken {
			return nil, fmt.Errorf("it already has a %q argument", namespaceArg)
		}
		props[namespaceArg] = map[string]any{"type": "string", "description": namespaceDescription}
		required = append(required, namespaceArg)
		scoped = true
	case u.cfg.Namespace.Argument != "":
		own, ok := props[u.cfg.Namespace.Argument]
		if !ok {
			break // not namespace-scoped: exposed as is
		}
		if _, taken := props[namespaceArg]; taken && u.cfg.Namespace.Argument != namespaceArg {
			return nil, fmt.Errorf("it has both %q and %q arguments", u.cfg.Namespace.Argument, namespaceArg)
		}
		delete(props, u.cfg.Namespace.Argument)
		arg, _ := own.(map[string]any)
		renamed := map[string]any{"type": "string"}
		for k, v := range arg {
			renamed[k] = v
		}
		renamed["description"] = namespaceDescription
		props[namespaceArg] = renamed
		required = slices.DeleteFunc(required, func(s string) bool { return s == u.cfg.Namespace.Argument })
		required = append(required, namespaceArg)
		scoped = true
	}
	if u.cfg.Authorize == config.AuthorizeUlgen && !scoped {
		return nil, fmt.Errorf("it names no namespace to check")
	}
	// the right a call needs: the rule's resource, else the borrowed check,
	// else resource "tools"; the verb from the annotations unless set
	resource, verb := "tools", verbOf(t)
	switch {
	case rule != nil && rule.Resource != "":
		resource = rule.Resource
		if rule.Verb != "" {
			verb = rule.Verb
		}
	case u.cfg.Check != nil:
		if verbOf(t) != "get" {
			return nil, fmt.Errorf("it is not read-only, and check grants every call one right (%s %s)", u.cfg.Check.Verb, u.cfg.Check.Resource)
		}
		resource, verb = u.cfg.Check.Resource, u.cfg.Check.Verb
	}

	schema["type"] = "object"
	schema["properties"] = props
	if len(required) > 0 {
		slices.Sort(required)
		schema["required"] = slices.Compact(required)
	}

	exposed := &mcp.Tool{
		Name:         u.cfg.Name + "_" + t.Name,
		Title:        t.Title,
		Description:  t.Description,
		InputSchema:  schema,
		OutputSchema: t.OutputSchema,
		Annotations:  t.Annotations,
	}
	return &tool{upstream: u, original: t.Name, exposed: exposed, scoped: scoped, resource: resource, verb: verb}, nil
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

func stringList(v any) []string {
	var out []string
	if list, ok := v.([]any); ok {
		for _, x := range list {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
