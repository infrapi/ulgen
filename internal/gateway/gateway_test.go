package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/infrapi/lib/pkg/auth"
	infraerrors "github.com/infrapi/lib/pkg/errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/infrapi/thanos/internal/config"
)

// fakeKayra: alice may read and write in acme, only read in globex; bob
// belongs nowhere.
type fakeKayra struct {
	mu     sync.Mutex
	checks []auth.AuthorizeRequest
}

func (k *fakeKayra) Verify(_ context.Context, token string) (*auth.UserInfo, error) {
	switch token {
	case "alice-token", "bob-token":
		return &auth.UserInfo{Subject: strings.TrimSuffix(token, "-token")}, nil
	}
	return nil, infraerrors.New(http.StatusUnauthorized, "test", "bad token")
}

func (k *fakeKayra) Authorize(_ context.Context, token string, req auth.AuthorizeRequest) (bool, string, error) {
	k.mu.Lock()
	k.checks = append(k.checks, req)
	k.mu.Unlock()
	if token != "alice-token" {
		return false, "you are not a member of namespace " + req.Namespace, nil
	}
	if req.Api == "telemetry.example.io" {
		if req.Namespace != "acme" {
			return false, "", infraerrors.New(http.StatusForbidden, "test", "namespace %q is not subscribed", req.Namespace)
		}
		return true, "", nil
	}
	switch {
	case req.Namespace == "acme":
		return true, "", nil
	case req.Namespace == "globex" && req.Verb == "get":
		return true, "", nil
	case req.Namespace == "globex":
		return false, "your roles grant get only", nil
	}
	return false, "", infraerrors.New(http.StatusNotFound, "test", "namespace %q not found", req.Namespace)
}

func (k *fakeKayra) Namespaces(_ context.Context, token string) ([]string, error) {
	if token != "alice-token" {
		return nil, nil
	}
	return []string{"acme", "globex"}, nil
}

type fakeCreds map[string]string

func (f fakeCreds) Credential(_ context.Context, s config.VaultSecret, tenant string) (string, error) {
	v, ok := f[tenant]
	if !ok {
		return "", errors.New("no secret")
	}
	return s.Key + ":" + v, nil
}

// echo answers a tool call with what the upstream received.
func echo(name string) mcp.ToolHandler {
	return func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		h := req.Extra.Header
		out, err := json.Marshal(map[string]any{
			"tool":          name,
			"arguments":     json.RawMessage(req.Params.Arguments),
			"authorization": h.Get("Authorization"),
			"grafana_token": h.Get("X-Grafana-Service-Account-Token"),
		})
		if err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(out)}}}, nil
	}
}

func object(props ...string) map[string]any {
	p := map[string]any{}
	for _, name := range props {
		p[name] = map[string]any{"type": "string"}
	}
	return map[string]any{"type": "object", "properties": p, "required": props}
}

func boolPtr(b bool) *bool { return &b }

// grafanaUpstream mimics mcp-grafana: stateful sessions, credential per
// request, no notion of tenant, guarded by a static bearer.
func grafanaUpstream(t *testing.T) *httptest.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "grafana"}, nil)
	s.AddTool(&mcp.Tool{Name: "query_prometheus", InputSchema: object("expr"), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, echo("query_prometheus"))
	s.AddTool(&mcp.Tool{Name: "delete_dashboard", InputSchema: object("uid"), Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)}}, echo("delete_dashboard"))
	s.AddTool(&mcp.Tool{Name: "create_folder", InputSchema: object("title"), Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)}}, echo("create_folder"))
	s.AddTool(&mcp.Tool{Name: "has_tenant", InputSchema: object("tenant")}, echo("has_tenant"))
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer grafana-server-token" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// telemetryUpstream mimics an infrapi/lib MCP: stateless, "namespace" in
// the path parameters, every request authenticated with the caller's token.
func telemetryUpstream(t *testing.T) *httptest.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "telemetry"}, nil)
	s.AddTool(&mcp.Tool{Name: "list_dashboards", InputSchema: object("namespace"), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, echo("list_dashboards"))
	s.AddTool(&mcp.Tool{Name: "list_apis", InputSchema: object(), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, echo("list_apis"))
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer alice-token") && !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer bob-token") {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

type harness struct {
	thanos *httptest.Server
	kayra  *fakeKayra
	g      *Gateway
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	t.Setenv("GRAFANA_BEARER", "Bearer grafana-server-token")
	cfg := &config.Config{
		Kayra: config.KayraConfig{URL: "http://kayra.invalid"},
		Vault: &config.VaultConfig{Address: "http://vault.invalid", RoleIDEnv: "X", SecretIDFile: "/x"},
		Upstreams: []config.Upstream{
			{
				Name: "grafana", URL: grafanaUpstream(t).URL, API: "grafana.example.io", Authorize: config.AuthorizeThanos,
				Credential:     config.Credential{Vault: &config.VaultSecret{Mount: "secret", Path: "grafana/{tenant}", Key: "token"}, Header: "X-Grafana-Service-Account-Token"},
				Tenant:         config.Tenant{Inject: true},
				HeadersFromEnv: map[string]string{"Authorization": "GRAFANA_BEARER"},
			},
			{
				Name: "telemetry", URL: telemetryUpstream(t).URL, API: "telemetry.example.io", Authorize: config.AuthorizeUpstream,
				Credential:  config.Credential{Passthrough: true},
				Tenant:      config.Tenant{Argument: "namespace"},
				TenantCheck: &config.Check{Resource: "dashboards", Verb: "list"},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	k := &fakeKayra{}
	g, err := New(cfg, Options{Kayra: k, Credentials: fakeCreds{"acme": "sa-acme", "globex": "sa-globex"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(g.Close)
	mux := http.NewServeMux()
	mux.Handle("/mcp", g.Handler())
	mux.Handle("/apis/", g.SpecHandler())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &harness{thanos: srv, kayra: k, g: g}
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func (h *harness) session(t *testing.T, token string) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	s, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: h.thanos.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{token}}, MaxRetries: -1,
	}, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	})
	return s
}

func callText(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func TestToolsAreAggregatedWithATenant(t *testing.T) {
	h := newHarness(t)
	s := h.session(t, "alice-token")
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	byName := map[string]*mcp.Tool{}
	for _, tl := range res.Tools {
		byName[tl.Name] = tl
	}
	var names []string
	for n := range byName {
		names = append(names, n)
	}
	slices.Sort(names)
	want := []string{"grafana_create_folder", "grafana_delete_dashboard", "grafana_query_prometheus", "list_tenants", "telemetry_list_apis", "telemetry_list_dashboards"}
	if !slices.Equal(names, want) {
		t.Fatalf("tools = %v, want %v (grafana_has_tenant left out)", names, want)
	}
	for _, n := range []string{"grafana_query_prometheus", "telemetry_list_dashboards"} {
		schema := byName[n].InputSchema.(map[string]any)
		props := schema["properties"].(map[string]any)
		if _, ok := props["tenant"]; !ok || !slices.Contains(stringList(schema["required"]), "tenant") {
			t.Errorf("%s: no required tenant: %v", n, schema)
		}
		if _, ok := props["namespace"]; ok {
			t.Errorf("%s still exposes namespace", n)
		}
	}
	if props := byName["telemetry_list_apis"].InputSchema.(map[string]any)["properties"].(map[string]any); props["tenant"] != nil {
		t.Errorf("telemetry_list_apis is not tenant-scoped but got a tenant")
	}
}

func TestGrafanaCallsCheckAndUseTheTenantCredential(t *testing.T) {
	h := newHarness(t)
	s := h.session(t, "alice-token")
	if _, err := s.ListTools(context.Background(), nil); err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	out, isErr := callText(t, s, "grafana_query_prometheus", map[string]any{"tenant": "acme", "expr": "up"})
	if isErr || !strings.Contains(out, `"grafana_token":"token:sa-acme"`) || !strings.Contains(out, `"arguments":{"expr":"up"}`) {
		t.Errorf("acme query: %v %s", isErr, out)
	}
	if strings.Contains(out, "alice-token") {
		t.Errorf("the caller's token reached a vault-credential upstream: %s", out)
	}
	out, isErr = callText(t, s, "grafana_query_prometheus", map[string]any{"tenant": "globex", "expr": "up"})
	if isErr || !strings.Contains(out, "token:sa-globex") {
		t.Errorf("globex read: %v %s", isErr, out)
	}

	// Verbs come from the annotations: globex grants get only.
	if out, isErr := callText(t, s, "grafana_delete_dashboard", map[string]any{"tenant": "globex", "uid": "x"}); !isErr || !strings.Contains(out, "get only") {
		t.Errorf("globex delete: %v %s", isErr, out)
	}
	if _, isErr := callText(t, s, "grafana_create_folder", map[string]any{"tenant": "acme", "title": "x"}); isErr {
		t.Errorf("acme create refused")
	}
	h.kayra.mu.Lock()
	var verbs []string
	for _, c := range h.kayra.checks {
		if c.Api != "grafana.example.io" || c.Resource != "tools" {
			t.Errorf("check = %+v", c)
		}
		verbs = append(verbs, c.Verb)
	}
	h.kayra.mu.Unlock()
	if !slices.Equal(verbs, []string{"get", "get", "delete", "create"}) {
		t.Errorf("verbs checked = %v", verbs)
	}

	for desc, args := range map[string]map[string]any{
		"unknown tenant": {"tenant": "initech", "expr": "up"},
		"no tenant":      {"expr": "up"},
		"path traversal": {"tenant": "../acme", "expr": "up"},
	} {
		if out, isErr := callText(t, s, "grafana_query_prometheus", args); !isErr || strings.Contains(out, "sa-") {
			t.Errorf("%s: %v %s", desc, isErr, out)
		}
	}
}

func TestTelemetryCallsPassTheCallerThrough(t *testing.T) {
	h := newHarness(t)
	s := h.session(t, "alice-token")
	if _, err := s.ListTools(context.Background(), nil); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	out, isErr := callText(t, s, "telemetry_list_dashboards", map[string]any{"tenant": "acme"})
	if isErr || !strings.Contains(out, `"authorization":"Bearer alice-token"`) || !strings.Contains(out, `"arguments":{"namespace":"acme"}`) {
		t.Errorf("telemetry call: %v %s", isErr, out)
	}
	h.kayra.mu.Lock()
	n := len(h.kayra.checks)
	h.kayra.mu.Unlock()
	if n != 0 {
		t.Errorf("thanos checked an upstream that authorizes itself")
	}

	// Another caller, same shared upstream session: their own token.
	s2 := h.session(t, "bob-token")
	if out, _ := callText(t, s2, "telemetry_list_apis", nil); !strings.Contains(out, "Bearer bob-token") {
		t.Errorf("bob's call carried %s", out)
	}
}

func TestListTenants(t *testing.T) {
	h := newHarness(t)
	out, isErr := callText(t, h.session(t, "alice-token"), "list_tenants", nil)
	want := `{"tenants":[{"tenant":"acme","services":["grafana","telemetry"]},{"tenant":"globex","services":["grafana"]}]}`
	if isErr || out != want {
		t.Errorf("list_tenants = %s, want %s", out, want)
	}
	if out, _ := callText(t, h.session(t, "bob-token"), "list_tenants", nil); out != `{"tenants":[]}` {
		t.Errorf("bob list_tenants = %s", out)
	}
}

func TestCallersMustAuthenticate(t *testing.T) {
	h := newHarness(t)
	for _, tok := range []string{"", "forged"} {
		req, err := http.NewRequest(http.MethodPost, h.thanos.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		if err != nil {
			t.Fatal(err)
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("token %q: %d", tok, resp.StatusCode)
		}
	}
}

func TestSpec(t *testing.T) {
	h := newHarness(t)
	resp, err := http.Get(h.thanos.URL + "/apis/grafana.example.io/v1/-/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"/apis/grafana.example.io/v1/namespaces/{namespace}/tools/{tool}"`) {
		t.Errorf("spec: %d %s", resp.StatusCode, body)
	}
	if resp, err := http.Get(h.thanos.URL + "/apis/telemetry.example.io/v1/-/openapi.json"); err != nil || resp.StatusCode != http.StatusNotFound {
		t.Errorf("telemetry authorizes itself, thanos must not serve its spec")
	} else if err := resp.Body.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

func TestVerbOf(t *testing.T) {
	for want, a := range map[string]*mcp.ToolAnnotations{
		"get":    {ReadOnlyHint: true},
		"create": {DestructiveHint: boolPtr(false)},
	} {
		if got := verbOf(&mcp.Tool{Annotations: a}); got != want {
			t.Errorf("verbOf(%+v) = %s, want %s", a, got, want)
		}
	}
	for _, a := range []*mcp.ToolAnnotations{nil, {}, {DestructiveHint: boolPtr(true)}} {
		if got := verbOf(&mcp.Tool{Annotations: a}); got != "delete" {
			t.Errorf("verbOf(%+v) = %s, want delete", a, got)
		}
	}
}
