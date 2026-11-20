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
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	server    *mcp.Server
	log       *slog.Logger
	toolsTTL  time.Duration
	now       func() time.Time

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

	g.server = mcp.NewServer(&mcp.Implementation{Name: "thanos", Version: "v1"}, &mcp.ServerOptions{
		Instructions: "Tools of several services, each prefixed by its service. A tool acting on a tenant takes a `tenant` argument: call " +
			listTenantsTool + " first to know the tenants you belong to, and ask the user which one when there are several.",
	})
	g.server.AddTool(&mcp.Tool{
		Name:        listTenantsTool,
		Description: "List the tenants (Kayra namespaces) you are in, with the services usable in each.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, g.listTenants)
	g.server.AddReceivingMiddleware(g.refreshOnList)
	return g, nil
}

// Close ends the upstream sessions.
func (g *Gateway) Close() {
	for _, u := range g.upstreams {
		u.client.close()
	}
}

// Handler serves MCP (Streamable HTTP, stateless) behind bearer
// authentication: every request carries a token Kayra resolves.
func (g *Gateway) Handler() http.Handler {
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return g.server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		tok = strings.TrimSpace(tok)
		if !ok || tok == "" {
			unauthorized(w, "a bearer token is required")
			return
		}
		if _, err := g.kayra.Verify(r.Context(), tok); err != nil {
			var ie *infraerrors.Error
			if errors.As(err, &ie) && ie.Code == http.StatusUnauthorized {
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

func unauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="thanos"`)
	http.Error(w, msg, http.StatusUnauthorized)
}

// refreshOnList refreshes the aggregated tools before answering a
// tools/list, at most every ToolsTTL.
func (g *Gateway) refreshOnList(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method == "tools/list" {
			g.refresh(ctx, false)
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
	for _, u := range g.upstreams {
		headers := http.Header{}
		if u.cfg.Credential.Passthrough {
			headers.Set("Authorization", "Bearer "+token(ctx))
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

	var gone []string
	for name := range g.tools {
		if _, ok := next[name]; !ok {
			gone = append(gone, name)
		}
	}
	if len(gone) > 0 {
		g.server.RemoveTools(gone...)
	}
	for _, t := range next {
		g.server.AddTool(t.exposed, g.call(t))
	}
	g.tools = next
	g.refreshed = g.now()
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

// listTenants answers list_tenants: the caller's namespaces (Kayra), each
// with the upstreams usable there: those whose tenant_check Kayra's /check
// allows, and those without one.
func (g *Gateway) listTenants(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok := token(ctx)
	namespaces, err := g.kayra.Namespaces(ctx, tok)
	if err != nil {
		g.log.Error("listing the caller's namespaces", "error", err)
		return nil, fmt.Errorf("thanos: could not list your tenants")
	}
	out := make([]tenantEntry, 0, len(namespaces))
	for _, ns := range namespaces {
		var services []string
		for _, u := range g.upstreams {
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
