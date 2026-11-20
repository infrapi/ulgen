// Package gateway is thanos: one MCP server aggregating several upstream
// MCP servers, each tool taking a "tenant" (a Kayra namespace). For every
// call, thanos makes sure the caller may act in that tenant — asking
// Kayra's /check itself, or leaving it to an upstream that asks with the
// caller's own token — then calls the upstream with the right credential:
// the caller's token, or the tenant's own credential read from Vault.
//
// Nothing is configured per tool: tools are discovered from the
// upstreams, the verb /check is asked for comes from each tool's MCP
// annotations, and which tools a person sees is decided upstream of
// thanos (LiteLLM's allowed tools).
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/infrapi/lib/pkg/auth"
	infraerrors "github.com/infrapi/lib/pkg/errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/infrapi/thanos/internal/config"
	"github.com/infrapi/thanos/internal/credentials"
)

// listTenantsTool is thanos' own tool.
const listTenantsTool = "list_tenants"

type upstream struct {
	cfg    config.Upstream
	client *upstreamClient
}

// Gateway is the thanos MCP server.
type Gateway struct {
	cfg       *config.Config
	kayra     Kayra
	creds     credentials.Source
	upstreams []*upstream
	// servers: "" is the aggregate (every upstream, tools "<upstream>_<tool>"), then one per upstream, its
	// tools under their own names: a gateway in front (LiteLLM) sees one MCP server per service.
	servers  map[string]*mcp.Server
	log      *slog.Logger
	toolsTTL time.Duration
	now      func() time.Time

	mu        sync.Mutex
	tools     map[string]*tool
	refreshed time.Time
}

// Options are Gateway's dependencies.
type Options struct {
	Kayra       Kayra
	Credentials credentials.Source // may be nil when no upstream reads Vault
	Logger      *slog.Logger
	// Transport is the base transport to upstreams (tests).
	Transport http.RoundTripper
}

type tokenKey struct{}

// token is the caller's bearer token, set by Handler.
func token(ctx context.Context) string {
	t, _ := ctx.Value(tokenKey{}).(string)
	return t
}

// New builds the gateway. Upstreams are listed lazily, on the first
// tools/list (with that caller's token, which an upstream such as an
// infrapi/lib MCP requires to list anything).
func New(cfg *config.Config, opts Options) (*Gateway, error) {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	g := &Gateway{
		cfg:      cfg,
		kayra:    opts.Kayra,
		creds:    opts.Credentials,
		log:      log,
		toolsTTL: config.Duration(cfg.ToolsTTL, 5*time.Minute),
		now:      time.Now,
		tools:    map[string]*tool{},
	}
	for _, uc := range cfg.Upstreams {
		static := http.Header{}
		for header, env := range uc.HeadersFromEnv {
			static.Set(header, os.Getenv(env))
		}
		if uc.Credential.Vault != nil && g.creds == nil {
			return nil, fmt.Errorf("gateway: upstream %s reads vault, but no credential source is set", uc.Name)
		}
		g.upstreams = append(g.upstreams, &upstream{cfg: uc, client: newUpstreamClient(uc.URL, uc.ProtocolVersion, static, opts.Transport, log)})
	}

	g.servers = map[string]*mcp.Server{"": g.newServer("thanos",
		"Tools of several services, each prefixed by its service.", g.upstreams)}
	for _, u := range g.upstreams {
		g.servers[u.cfg.Name] = g.newServer("thanos-"+u.cfg.Name, "Tools of "+u.cfg.Name+".", []*upstream{u})
	}
	return g, nil
}

// newServer is one MCP server of thanos, with list_tenants over ups.
func (g *Gateway) newServer(name, intro string, ups []*upstream) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: name, Version: "v1"}, &mcp.ServerOptions{
		Instructions: intro + " A tool acting on a tenant takes a `tenant` argument: call " + listTenantsTool +
			" first to know the tenants you belong to, and ask the user which one when there are several.",
	})
	srv.AddTool(&mcp.Tool{
		Name:        listTenantsTool,
		Description: "List the tenants (Kayra namespaces) you are in, with the services usable in each.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, g.listTenants(ups))
	srv.AddReceivingMiddleware(g.refreshOnList)
	return srv
}

// Close ends the upstream sessions.
func (g *Gateway) Close() {
	for _, u := range g.upstreams {
		u.client.close()
	}
}

// Handler serves MCP (Streamable HTTP, stateless) behind bearer
// authentication: every request carries a token Kayra resolves, in
// token_header (Authorization by default). The
// configured path serves every upstream, <path>/<upstream> one of them.
func (g *Gateway) Handler() http.Handler {
	base := strings.TrimRight(g.cfg.Path, "/")
	mcpHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		name := strings.Trim(strings.TrimPrefix(r.URL.Path, base), "/")
		return g.servers[name] // nil: 404
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.Trim(strings.TrimPrefix(r.URL.Path, base), "/")
		if _, ok := g.servers[name]; !ok || !strings.HasPrefix(r.URL.Path, base) {
			http.NotFound(w, r)
			return
		}
		raw := strings.TrimSpace(r.Header.Get(g.cfg.TokenHeader))
		tok, ok := strings.CutPrefix(raw, "Bearer ")
		if !ok && !strings.EqualFold(g.cfg.TokenHeader, "Authorization") {
			tok, ok = raw, raw != "" // a dedicated header may carry the bare token
		}
		tok = strings.TrimSpace(tok)
		if !ok || tok == "" {
			// The MCP handshake and tools/list, without a token: a gateway's
			// health check and UI (LiteLLM's, no user behind them). The tool
			// list is the same for everyone; calling a tool takes a token.
			if handshake, err := isHandshake(r); err != nil {
				http.Error(w, "unreadable request", http.StatusBadRequest)
				return
			} else if handshake {
				mcpHandler.ServeHTTP(w, r)
				return
			}
			g.log.Warn("refusing a caller without a bearer token", "path", r.URL.Path)
			unauthorized(w, "a bearer token is required")
			return
		}
		if _, err := g.kayra.Verify(r.Context(), tok); err != nil {
			var ie *infraerrors.Error
			if errors.As(err, &ie) && ie.Code == http.StatusUnauthorized {
				g.log.Warn("refusing a caller: Kayra refused the token", "path", r.URL.Path, "error", err)
				unauthorized(w, "the token was refused")
				return
			}
			g.log.Error("verifying a caller", "error", err)
			http.Error(w, "the authorization service is unavailable", http.StatusServiceUnavailable)
			return
		}
		mcpHandler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tokenKey{}, tok)))
	})
}

// handshakeMethods are the JSON-RPC messages served without a token.
var handshakeMethods = map[string]bool{
	"initialize": true, "notifications/initialized": true, "ping": true, "tools/list": true,
}

// maxHandshakeBody bounds what is read of a request without a token.
const maxHandshakeBody = 64 << 10

// isHandshake tells whether r is a POST of one handshake message, leaving
// its body readable again.
func isHandshake(r *http.Request) (bool, error) {
	if r.Method != http.MethodPost || r.Body == nil {
		return false, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxHandshakeBody+1))
	if err != nil {
		return false, err
	}
	if cerr := r.Body.Close(); cerr != nil {
		return false, cerr
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if len(body) > maxHandshakeBody {
		return false, nil
	}
	var msg struct {
		Method string `json:"method"`
	}
	if json.Unmarshal(body, &msg) != nil { // a batch, or not JSON: not a handshake
		return false, nil
	}
	return handshakeMethods[msg.Method], nil
}

func unauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="thanos"`)
	http.Error(w, msg, http.StatusUnauthorized)
}

// refreshOnList refreshes the aggregated tools before answering a
// tools/list, at most every ToolsTTL, and before a tools/call when they
// were never listed (a gateway in front calls from its own cached list,
// right after thanos restarted).
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

// refresh lists every upstream and replaces the tools thanos exposes. An
// upstream that fails to list keeps its previous tools.
func (g *Gateway) refresh(ctx context.Context, force bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !force && !g.refreshed.IsZero() && g.now().Sub(g.refreshed) < g.toolsTTL {
		return
	}

	next := map[string]*tool{}
	complete := true // every upstream listed: the next refresh can wait toolsTTL
	for _, u := range g.upstreams {
		headers := http.Header{}
		if u.cfg.Credential.Passthrough {
			tok := token(ctx)
			if tok == "" {
				tok = g.listToken()
			}
			if tok == "" {
				// no caller and no list token: keep what an earlier listing found,
				// and list again as soon as a caller's token comes
				complete = false
				for name, t := range g.tools {
					if t.upstream == u {
						next[name] = t
					}
				}
				continue
			}
			headers.Set("Authorization", "Bearer "+tok)
		}
		listed, err := u.client.listTools(ctx, headers)
		if err != nil {
			g.log.Warn("listing an upstream's tools", "upstream", u.cfg.Name, "error", err)
			for name, t := range g.tools {
				if t.upstream == u {
					next[name] = t
				}
			}
			continue
		}
		for _, lt := range listed {
			t, err := expose(u, lt)
			if err != nil {
				g.log.Warn("tool left out", "upstream", u.cfg.Name, "tool", lt.Name, "reason", err.Error())
				continue
			}
			next[t.exposed.Name] = t
		}
	}

	// The aggregate: every tool under its prefixed name; each upstream's
	// server: its own tools under their upstream names.
	for name, old := range g.tools {
		if _, ok := next[name]; !ok {
			g.servers[""].RemoveTools(name)
			if old.original != listTenantsTool {
				g.servers[old.upstream.cfg.Name].RemoveTools(old.original)
			}
		}
	}
	for _, t := range next {
		g.servers[""].AddTool(t.exposed, g.call(t))
		if t.original == listTenantsTool {
			continue // thanos' own on that server; the upstream's stays reachable on the aggregate
		}
		own := *t.exposed
		own.Name = t.original
		g.servers[t.upstream.cfg.Name].AddTool(&own, g.call(t))
	}
	g.tools = next
	if complete {
		g.refreshed = g.now()
	}
}

// listToken is list_token_file's token, "" when unset or unreadable.
func (g *Gateway) listToken() string {
	if g.cfg.ListTokenFile == "" {
		return ""
	}
	data, err := os.ReadFile(g.cfg.ListTokenFile)
	if err != nil {
		g.log.Warn("reading list_token_file", "error", err)
		return ""
	}
	return strings.TrimSpace(string(data))
}

// toolResult is a tool error the model can read and act on.
func toolError(format string, args ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}}}
}

// call is the handler of one exposed tool.
func (g *Gateway) call(t *tool) mcp.ToolHandler {
	u := t.upstream
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		tok := token(ctx)
		args := map[string]any{}
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return toolError("arguments must be a JSON object"), nil
			}
		}

		tenant := ""
		if t.scoped {
			tenant, _ = args[tenantArg].(string)
			if !credentials.ValidTenant(tenant) {
				return toolError("%q is required: the tenant to act in (list_tenants gives yours)", tenantArg), nil
			}
			delete(args, tenantArg)
			if u.cfg.Tenant.Argument != "" {
				args[u.cfg.Tenant.Argument] = tenant
			}
		}

		if u.cfg.Authorize == config.AuthorizeThanos {
			allowed, reason, err := g.kayra.Authorize(ctx, tok, auth.AuthorizeRequest{
				Namespace: tenant, Api: u.cfg.API, Resource: "tools", Verb: t.verb,
			})
			if err != nil {
				var ie *infraerrors.Error
				if errors.As(err, &ie) && ie.Code >= 400 && ie.Code < 500 {
					return toolError("not allowed on tenant %q: %s", tenant, ie.Message), nil
				}
				return nil, fmt.Errorf("thanos: authorization service: %w", err)
			}
			if !allowed {
				return toolError("not allowed on tenant %q: %s", tenant, reason), nil
			}
		}

		headers := http.Header{}
		switch {
		case u.cfg.Credential.Passthrough:
			headers.Set("Authorization", "Bearer "+tok)
		case u.cfg.Credential.Vault != nil:
			cred, err := g.creds.Credential(ctx, *u.cfg.Credential.Vault, tenant)
			if err != nil {
				g.log.Error("reading a tenant credential", "upstream", u.cfg.Name, "tenant", tenant, "error", err)
				return toolError("%s is not set up for tenant %q yet", u.cfg.Name, tenant), nil
			}
			headers.Set(u.cfg.Credential.Header, u.cfg.Credential.Prefix+cred)
		}

		res, err := u.client.callTool(ctx, headers, t.original, args)
		if err != nil {
			g.log.Error("calling an upstream tool", "upstream", u.cfg.Name, "tool", t.original, "error", err)
			return nil, fmt.Errorf("thanos: %s is unavailable", u.cfg.Name)
		}
		return res, nil
	}
}

// tenantEntry is one line of list_tenants.
type tenantEntry struct {
	Tenant   string   `json:"tenant"`
	Services []string `json:"services"`
}

// listTenants answers list_tenants over ups: the caller's namespaces
// (Kayra), each with the upstreams of ups usable there: those whose
// tenant_check Kayra's /check allows, and those without one. A namespace
// where none is usable is left out.
func (g *Gateway) listTenants(ups []*upstream) mcp.ToolHandler {
	return func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return g.tenants(ctx, ups)
	}
}

func (g *Gateway) tenants(ctx context.Context, ups []*upstream) (*mcp.CallToolResult, error) {
	tok := token(ctx)
	namespaces, err := g.kayra.Namespaces(ctx, tok)
	if err != nil {
		g.log.Error("listing the caller's namespaces", "error", err)
		return nil, fmt.Errorf("thanos: could not list your tenants")
	}
	out := make([]tenantEntry, 0, len(namespaces))
	for _, ns := range namespaces {
		var services []string
		for _, u := range ups {
			chk := u.cfg.TenantCheck
			if chk == nil {
				services = append(services, u.cfg.Name)
				continue
			}
			allowed, _, err := g.kayra.Authorize(ctx, tok, auth.AuthorizeRequest{Namespace: ns, Api: u.cfg.API, Resource: chk.Resource, Verb: chk.Verb})
			var ie *infraerrors.Error
			switch {
			case err == nil && allowed:
				services = append(services, u.cfg.Name)
			case err == nil, errors.As(err, &ie) && ie.Code >= 400 && ie.Code < 500:
				// not subscribed, no role: not usable there
			default:
				g.log.Error("checking a tenant", "tenant", ns, "upstream", u.cfg.Name, "error", err)
				return nil, fmt.Errorf("thanos: could not list your tenants")
			}
		}
		if len(services) > 0 {
			sort.Strings(services)
			out = append(out, tenantEntry{Tenant: ns, Services: services})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tenant < out[j].Tenant })

	data, err := json.Marshal(map[string]any{"tenants": out})
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(data)}},
		StructuredContent: map[string]any{"tenants": out},
	}, nil
}
