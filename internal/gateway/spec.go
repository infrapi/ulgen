package gateway

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
)

// SpecHandler serves, for every api ulgen authorizes calls on, the OpenAPI
// document Kayra reads when the api is registered:
// /apis/<api>/<version>/-/openapi.json. It names no tool: the resources
// calls are checked on (allowed_tools' rules', "tools" otherwise), each
// with the verbs a call needs — get for a read-only tool, create for
// another write, delete for a destructive one. Upstreams sharing an api
// share its document. A namespace Role grants them like any other api's:
//
//	apis: [mcp.example.io], resources: [prometheus], verbs: [get]
//
// An upstream borrowing another api's right (check) adds nothing: that api
// serves its own document.
func (g *Gateway) SpecHandler() http.Handler {
	type api struct {
		name, version string
		upstreams     []string
		resources     []string
	}
	var apis []*api
	byRoot := map[string]*api{}
	for _, u := range g.upstreams {
		resources := u.cfg.Resources()
		if len(resources) == 0 {
			continue
		}
		root := "/apis/" + u.cfg.API + "/" + u.cfg.Version
		a, ok := byRoot[root]
		if !ok {
			a = &api{name: u.cfg.API, version: u.cfg.Version}
			byRoot[root] = a
			apis = append(apis, a)
		}
		a.upstreams = append(a.upstreams, u.cfg.Name)
		for _, r := range resources {
			if !slices.Contains(a.resources, r) {
				a.resources = append(a.resources, r)
			}
		}
	}

	specs := map[string][]byte{}
	for _, a := range apis {
		root := "/apis/" + a.name + "/" + a.version
		paths := map[string]any{}
		for _, r := range a.resources {
			paths[root+"/namespaces/{namespace}/"+r+"/{tool}"] = map[string]any{
				"parameters": []any{
					map[string]any{"name": "namespace", "in": "path", "required": true, "schema": map[string]any{"type": "string"}},
					map[string]any{"name": "tool", "in": "path", "required": true, "schema": map[string]any{"type": "string"}},
				},
				"get":    operation("callReadOnly_"+r, "Call a read-only tool"),
				"post":   operation("call_"+r, "Call a tool that changes something"),
				"delete": operation("callDestructive_"+r, "Call a destructive tool"),
			}
		}
		doc := map[string]any{
			"openapi": "3.0.3",
			"info": map[string]any{
				"title":       a.name,
				"version":     a.version,
				"description": "Tools of the " + strings.Join(a.upstreams, ", ") + " MCP servers, through ulgen. Authorization only: call them over MCP.",
			},
			"paths": paths,
		}
		data, err := json.Marshal(doc)
		if err != nil {
			g.log.Error("encoding an OpenAPI document", "api", a.name, "error", err)
			continue
		}
		specs[root+"/-/openapi.json"] = data
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := specs[strings.TrimRight(r.URL.Path, "/")]
		if !ok || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(data); err != nil {
			g.log.Debug("writing an OpenAPI document", "error", err)
		}
	})
}

func operation(id, summary string) map[string]any {
	return map[string]any{
		"operationId": id,
		"summary":     summary,
		"responses":   map[string]any{"501": map[string]any{"description": "Not served over HTTP: use the MCP endpoint."}},
	}
}
