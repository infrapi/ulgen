// Package gateway is ulgen: one MCP endpoint in front of the platform's
// APIs, each of which serves its own (infrapi/lib's mcp, at <root>/-/mcp).
// The APIs are found in Kayra's catalog, with the caller's token; every
// call goes to its API with the caller's token, and the API checks it, as
// for any of its routes. ulgen adds list_namespaces, the caller's
// namespaces, an audit line per call and metrics.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/infrapi/lib/pkg/auth"
	infraerrors "github.com/infrapi/lib/pkg/errors"
	"github.com/infrapi/lib/pkg/logger"
	"github.com/infrapi/lib/pkg/server"

	"github.com/infrapi/ulgen/internal/kayra"
	"github.com/infrapi/ulgen/internal/metrics"
)

// listNamespacesTool is ulgen's own tool.
const listNamespacesTool = "list_namespaces"

// protocolVersion is the MCP version of ulgen's sessions with the APIs:
// newer ones are refused by stateful servers.
const protocolVersion = "2025-06-18"

// Catalog is what ulgen asks Kayra, as the caller.
type Catalog interface {
	APIs(ctx context.Context, token string, serves func(name string) bool) ([]kayra.API, error)
	Namespaces(ctx context.Context, token string) ([]string, error)
}

// api is an API ulgen serves the tools of.
type api struct {
	kayra.API
	client *upstreamClient
}

// Options are the Gateway's settings and dependencies.
type Options struct {
	Catalog Catalog
	// GatewayURL is where the APIs are reached.
	GatewayURL string
	// Serves selects the APIs by name.
	Serves func(name string) bool
	// ToolsTTL is how long the APIs and their tools are kept.
	ToolsTTL time.Duration
	Logger   *logger.Logger
	Metrics  *metrics.Metrics
	// Transport is the base transport to the APIs (tests).
	Transport http.RoundTripper
}

// Gateway is the ulgen MCP server.
type Gateway struct {
	opts    Options
	handler http.Handler
	now     func() time.Time

	mu sync.Mutex
	// servers: "" is the aggregate (every API, tools <short>_<tool>), then
	// one per API, by its short name, its tools under their own names.
	servers   map[string]*mcp.Server
	apis      map[string]*api // by name
	tools     map[string]*tool
	refreshed time.Time
}

type callerKey struct{}

// caller is what a request's context carries of its caller.
type caller struct {
	token, user, traceparent string
}

func callerOf(ctx context.Context) caller {
	c, _ := ctx.Value(callerKey{}).(caller)
	return c
}

// New builds the gateway. The APIs are listed on the first tools/list, with
// that caller's token.
func New(opts Options) *Gateway {
	if opts.Serves == nil {
		opts.Serves = func(string) bool { return true }
	}
	g := &Gateway{opts: opts, now: time.Now, apis: map[string]*api{}, tools: map[string]*tool{}}
	g.servers = map[string]*mcp.Server{"": g.newServer("ulgen",
		"Tools of the platform's APIs, each prefixed by its API's short name.")}
	g.handler = mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.servers[serverName(r.Context())] // nil: 404
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return g
}

type serverKey struct{}

func serverName(ctx context.Context) string {
	s, _ := ctx.Value(serverKey{}).(string)
	return s
}

// newServer is one MCP server of ulgen, with list_namespaces.
func (g *Gateway) newServer(name, intro string) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: name, Version: "v1"}, &mcp.ServerOptions{
		Instructions: intro + " A tool acting in a namespace takes a `namespace` argument: call " + listNamespacesTool +
			" first to know the namespaces you belong to, and ask the user which one when there are several.",
	})
	srv.AddTool(&mcp.Tool{
		Name:        listNamespacesTool,
		Description: "List the Kayra namespaces you belong to.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, g.listNamespaces)
	srv.AddReceivingMiddleware(g.refreshOnList)
	return srv
}

// Serve is the MCP endpoints' handler, <root>/-/mcp for every API and
// <root>/-/mcp/<short> for one, behind auth.Client.Middleware: the caller's
// token, verified, goes to the APIs.
func (g *Gateway) Serve(c *gin.Context) {
	name := c.Param("api")
	if name != "" {
		g.mu.Lock()
		_, ok := g.servers[name]
		never := g.refreshed.IsZero()
		g.mu.Unlock()
		if !ok && never {
			// right after a start: the per-API endpoints come with the first listing
			g.refresh(g.withCaller(c), false)
		}
	}
	ctx := context.WithValue(g.withCaller(c), serverKey{}, name)
	g.handler.ServeHTTP(c.Writer, c.Request.WithContext(ctx))
}

// withCaller is the request's context, with its caller.
func (g *Gateway) withCaller(c *gin.Context) context.Context {
	token, _ := strings.CutPrefix(strings.TrimSpace(c.GetHeader("Authorization")), "Bearer ")
	cl := caller{token: strings.TrimSpace(token), traceparent: c.GetHeader("Traceparent")}
	if u, ok := auth.GetUser(c); ok {
		cl.user = u.Subject
	}
	return context.WithValue(c.Request.Context(), callerKey{}, cl)
}

// Close ends the sessions with the APIs.
func (g *Gateway) Close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, a := range g.apis {
		a.client.close()
	}
}

// refreshOnList refreshes the APIs and their tools before answering a
// tools/list, at most every ToolsTTL, and before a tools/call when they
// were never listed (a client calling from its own list, right after ulgen
// restarted).
func (g *Gateway) refreshOnList(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		switch method {
		case "tools/list":
			g.refresh(ctx, false)
		case "tools/call":
			g.mu.Lock()
			never := g.refreshed.IsZero()
			g.mu.Unlock()
			if never {
				g.refresh(ctx, false)
			}
		}
		return next(ctx, method, req)
	}
}

// result labels an answer of Kayra: ok, refused (4xx), error.
func result(err error) string {
	code := infraerrors.ErrorCode(err)
	switch {
	case err == nil:
		return "ok"
	case code >= http.StatusBadRequest && code < http.StatusInternalServerError:
		return "refused"
	default:
		return "error"
	}
}

// refresh asks Kayra for the APIs, lists their tools and replaces those
// ulgen serves. An API that fails to list keeps its previous tools; Kayra
// failing keeps every API.
func (g *Gateway) refresh(ctx context.Context, force bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !force && !g.refreshed.IsZero() && g.now().Sub(g.refreshed) < g.opts.ToolsTTL {
		return
	}
	cl := callerOf(ctx)
	log := g.opts.Logger

	start := time.Now()
	found, err := g.opts.Catalog.APIs(ctx, cl.token, g.opts.Serves)
	g.opts.Metrics.Kayra.WithLabelValues("apis", result(err)).Observe(time.Since(start).Seconds())
	if err != nil {
		log.Warn("listing the platform's APIs", logger.Err(err), logger.String("request_id", server.CorrelationID(ctx)))
		return
	}

	apis := map[string]*api{}
	for _, f := range found {
		url := strings.TrimRight(g.opts.GatewayURL, "/") + f.Root() + server.MCPPath
		if old, ok := g.apis[f.Name]; ok && old.client.url == url && old.Short == f.Short {
			apis[f.Name] = old
			continue
		}
		apis[f.Name] = &api{API: f, client: newUpstreamClient(url, protocolVersion, nil, g.opts.Transport, log)}
	}

	next := map[string]*tool{}
	for _, a := range apis {
		headers := http.Header{}
		if cl.token != "" {
			// an API's -/mcp is open unless it says otherwise: the caller's
			// token, for one that does
			headers.Set("Authorization", "Bearer "+cl.token)
		}
		listed, err := a.client.listTools(ctx, headers)
		if err != nil {
			log.Warn("listing an API's tools", logger.String("api", a.Name), logger.Err(err))
			g.opts.Metrics.ListErrors.WithLabelValues(a.Name).Inc()
			for name, t := range g.tools {
				if t.api.Name == a.Name {
					t.api = a
					next[name] = t
				}
			}
			continue
		}
		served := 0
		for _, lt := range listed {
			t, err := serve(a, lt)
			if err != nil {
				log.Warn("tool left out", logger.String("api", a.Name), logger.String("tool", lt.Name), logger.String("reason", err.Error()))
				continue
			}
			if other, taken := next[t.prefixed.Name]; taken {
				log.Warn("tool left out: its name is another API's", logger.String("api", a.Name), logger.String("tool", lt.Name),
					logger.String("other", other.api.Name))
				continue
			}
			next[t.prefixed.Name] = t
			served++
		}
		g.opts.Metrics.ToolsServed.WithLabelValues(a.Name).Set(float64(served))
	}

	// the APIs gone: their endpoint and session
	for name, old := range g.apis {
		if a, ok := apis[name]; ok && a == old {
			continue
		}
		delete(g.servers, old.Short)
		old.client.close()
		g.opts.Metrics.ToolsServed.DeleteLabelValues(name)
	}
	for _, a := range apis {
		if _, ok := g.servers[a.Short]; !ok {
			g.servers[a.Short] = g.newServer("ulgen-"+a.Short, "Tools of "+a.Name+".")
		}
	}
	agg := g.servers[""]
	for name, old := range g.tools {
		if _, ok := next[name]; !ok {
			agg.RemoveTools(name)
			if srv, ok := g.servers[old.api.Short]; ok {
				srv.RemoveTools(old.original)
			}
		}
	}
	for _, t := range next {
		agg.AddTool(t.prefixed, g.call(t))
		if t.original != listNamespacesTool { // ulgen's own on that endpoint
			g.servers[t.api.Short].AddTool(t.own(), g.call(t))
		}
	}
	g.apis, g.tools, g.refreshed = apis, next, g.now()
}

// toolError is a tool error the model can read and act on.
func toolError(format string, args ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}}}
}

// call is the handler of one served tool: the call, as the caller, to the
// API, which checks it.
func (g *Gateway) call(t *tool) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (res *mcp.CallToolResult, err error) {
		cl := callerOf(ctx)
		a := t.api
		start := g.now()
		namespace := ""
		// the audit log: one line per call, who, where, what, and how it ended
		defer func() {
			outcome := "ok"
			switch {
			case err != nil:
				outcome = "error"
			case res != nil && res.IsError:
				outcome = "tool_error"
			}
			elapsed := g.now().Sub(start)
			g.opts.Logger.Info("tool call", logger.String("user", cl.user), logger.String("api", a.Name), logger.String("tool", t.original),
				logger.String("namespace", namespace), logger.String("outcome", outcome), logger.Int64("duration_ms", elapsed.Milliseconds()),
				logger.String("request_id", server.CorrelationID(ctx)))
			g.opts.Metrics.ToolCalls.WithLabelValues(a.Name, t.original, outcome).Inc()
			g.opts.Metrics.ToolCallDuration.WithLabelValues(a.Name, outcome).Observe(elapsed.Seconds())
		}()
		args := map[string]any{}
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return toolError("arguments must be a JSON object"), nil
			}
		}
		namespace, _ = args[namespaceArg].(string)

		headers := http.Header{"Authorization": {"Bearer " + cl.token}}
		if id := server.CorrelationID(ctx); id != "" {
			headers.Set(server.RequestIDHeader, id) // the API's logs say the same id
		}
		if cl.traceparent != "" {
			headers.Set("Traceparent", cl.traceparent)
		}
		out, err := a.client.callTool(ctx, headers, t.original, args)
		if err != nil {
			g.opts.Logger.Error("calling an API's tool", logger.String("api", a.Name), logger.String("tool", t.original), logger.Err(err))
			return nil, fmt.Errorf("ulgen: %s is unavailable", a.Name)
		}
		return out, nil
	}
}

// listNamespaces answers list_namespaces: the caller's namespaces.
func (g *Gateway) listNamespaces(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start := time.Now()
	namespaces, err := g.opts.Catalog.Namespaces(ctx, callerOf(ctx).token)
	g.opts.Metrics.Kayra.WithLabelValues("namespaces", result(err)).Observe(time.Since(start).Seconds())
	if err != nil {
		var ie *infraerrors.Error
		if errors.As(err, &ie) && ie.Code >= 400 && ie.Code < 500 {
			return toolError("Kayra refused to list your namespaces: %s", ie.Message), nil
		}
		g.opts.Logger.Error("listing the caller's namespaces", logger.Err(err))
		return nil, fmt.Errorf("ulgen: could not list your namespaces")
	}
	sort.Strings(namespaces)
	data, err := json.Marshal(map[string]any{"namespaces": namespaces})
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(data)}},
		StructuredContent: map[string]any{"namespaces": namespaces},
	}, nil
}
