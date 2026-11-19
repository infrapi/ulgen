package gateway

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/infrapi/thanos/internal/config"
)

// SpecHandler serves, for every upstream thanos authorizes itself, the
// OpenAPI document Kayra reads when the api is registered:
// /apis/<api>/<version>/-/openapi.json. It names no tool: one resource,
// "tools", whose verbs are what a call needs (see verbOf) — get for a
// read-only tool, create for another write, delete for a destructive one.
// A namespace Role grants them like any other api's:
//
//	apis: [grafana.example.io], resources: [tools], verbs: [get]
func (g *Gateway) SpecHandler() http.Handler {
	specs := map[string][]byte{}
	for _, u := range g.upstreams {
		if u.cfg.Authorize != config.AuthorizeThanos {
			continue
		}
		root := "/apis/" + u.cfg.API + "/" + u.cfg.Version
		doc := map[string]any{
			"openapi": "3.0.3",
			"info": map[string]any{
				"title":       u.cfg.API,
				"version":     u.cfg.Version,
				"description": "Tools of the " + u.cfg.Name + " MCP server, through thanos. Authorization only: call them over MCP.",
			},
			"paths": map[string]any{
				root + "/namespaces/{namespace}/tools/{tool}": map[string]any{
					"parameters": []any{
						map[string]any{"name": "namespace", "in": "path", "required": true, "schema": map[string]any{"type": "string"}},
						map[string]any{"name": "tool", "in": "path", "required": true, "schema": map[string]any{"type": "string"}},
					},
					"get":    operation("callReadOnlyTool", "Call a read-only tool"),
					"post":   operation("callTool", "Call a tool that changes something"),
					"delete": operation("callDestructiveTool", "Call a destructive tool"),
				},
			},
		}
		data, err := json.Marshal(doc)
		if err != nil {
			g.log.Error("encoding an OpenAPI document", "api", u.cfg.API, "error", err)
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
