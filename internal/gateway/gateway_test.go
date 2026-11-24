package gateway_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/infrapi/lib/pkg/auth"
	libconfig "github.com/infrapi/lib/pkg/config"
	"github.com/infrapi/lib/pkg/logger"
	"github.com/infrapi/lib/pkg/mcp"
	"github.com/infrapi/lib/pkg/server"

	"github.com/infrapi/ulgen/internal/gateway"
	"github.com/infrapi/ulgen/internal/kayra"
	"github.com/infrapi/ulgen/internal/metrics"
)

const ulgenRoot = "/apis/mcp.infrapi.io/v1"

// fakeKayra answers what ulgen asks: its discovery, the callers (jdoe; a
// token for another resource), the APIs and their specs, the namespaces.
type fakeKayra struct {
	mu   sync.Mutex
	apis []map[string]any          // GET /apis
	spec map[string]map[string]any // api -> its spec's paths
	asks []string
	url  string
	// audience of jdoe's token
	audience string
}

func (k *fakeKayra) start(t *testing.T) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.GET(auth.DiscoveryPath, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"issuer": k.url, "domain": "infrapi.io",
			"apis":      gin.H{"system": "/apis/system.infrapi.io/v1", "catalog": "/apis/catalog.infrapi.io/v1", "authorization": "/apis/authorization.infrapi.io/v1"},
			"endpoints": gin.H{"userinfo": auth.UserinfoPath, "check": "/apis/authorization.infrapi.io/v1/check", "apis": "/apis"}})
	})
	e.POST(auth.UserinfoPath, func(c *gin.Context) {
		switch c.GetHeader("Authorization") {
		case "Bearer jdoe":
			c.JSON(http.StatusOK, gin.H{"sub": "jdoe", "aud": []string{k.audience}})
		case "Bearer elsewhere":
			c.JSON(http.StatusOK, gin.H{"sub": "jdoe", "aud": []string{"https://other.example.com"}})
		default:
			c.JSON(http.StatusUnauthorized, gin.H{"status": 401})
		}
	})
	callerOnly := func(c *gin.Context) {
		k.mu.Lock()
		k.asks = append(k.asks, c.Request.Method+" "+c.Request.URL.Path)
		k.mu.Unlock()
		if c.GetHeader("Authorization") != "Bearer jdoe" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"status": 401})
		}
	}
	e.GET("/apis", callerOnly, func(c *gin.Context) {
		k.mu.Lock()
		defer k.mu.Unlock()
		c.JSON(http.StatusOK, k.apis)
	})
	e.GET("/apis/catalog.infrapi.io/v1/apiregistrations/:name/openapi.json", callerOnly, func(c *gin.Context) {
		k.mu.Lock()
		defer k.mu.Unlock()
		paths, ok := k.spec[c.Param("name")]
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"status": 404})
			return
		}
		c.JSON(http.StatusOK, gin.H{"openapi": "3.1.0", "paths": paths})
	})
	e.GET("/apis/system.infrapi.io/v1/namespaces", callerOnly, func(c *gin.Context) {
		c.JSON(http.StatusOK, []gin.H{{"name": "beta"}, {"name": "acme"}})
	})
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	k.url = srv.URL
	return srv
}

func appConfig(t *testing.T, name, url string) *libconfig.AppConfig {
	t.Helper()
	cfg, err := libconfig.NewConfigFromMap(map[string]string{
		"INFRAPI_APP_NAME": name, "INFRAPI_APP_PLATFORM": "sandbox", "INFRAPI_APP_REGION": "eu-west-1",
		"INFRAPI_APP_LOCATION": "dc1", "INFRAPI_APP_FQDN": name + ".example.com", "INFRAPI_APP_URL": url,
	}, "test.env").GetAppConfig()
	require.NoError(t, err)
	return cfg
}

// computeAPI is an infrapi/lib API with its MCP endpoint: reading a VM, as
// jdoe only, in acme only, recording the headers it was called with.
type computeAPI struct {
	mu      sync.Mutex
	headers []http.Header
}

const computeContract = `{
  "openapi": "3.0.3",
  "info": {"title": "compute", "version": "v1"},
  "paths": {
    "/apis/compute.infrapi.io/v1/namespaces/{namespace}/vms/{vm}": {
      "parameters": [
        {"name": "namespace", "in": "path", "required": true, "schema": {"type": "string"}},
        {"name": "vm", "in": "path", "required": true, "schema": {"type": "string"}}
      ],
      "get": {"operationId": "getVm", "summary": "Read a VM", "responses": {"200": {"description": "The VM"}}}
    }
  }
}`

func (a *computeAPI) engine(t *testing.T) http.Handler {
	t.Helper()
	const root = "/apis/compute.infrapi.io/v1"
	e, err := server.NewServerGin(appConfig(t, "compute", "http://gateway.example.com"), []string{root},
		server.WithOpenAPI(server.OpenAPISpec{Name: "compute v1", Path: "compute/v1.json", Content: []byte(computeContract)}))
	require.NoError(t, err)
	e.GET(root+"/namespaces/:namespace/vms/:vm", func(c *gin.Context) {
		a.mu.Lock()
		a.headers = append(a.headers, c.Request.Header.Clone())
		a.mu.Unlock()
		if c.GetHeader("Authorization") != "Bearer jdoe" || c.Param("namespace") != "acme" {
			c.JSON(http.StatusForbidden, gin.H{"title": "Forbidden", "status": 403, "detail": "not in " + c.Param("namespace")})
			return
		}
		c.JSON(http.StatusOK, gin.H{"name": c.Param("vm"), "namespace": c.Param("namespace")})
	})
	h, err := mcp.New([]byte(computeContract), e, mcp.Options{Name: "compute"})
	require.NoError(t, err)
	t.Cleanup(h.Close)
	e.Any(root+server.MCPPath, gin.WrapH(h))
	return e
}

func (a *computeAPI) last(t *testing.T) http.Header {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	require.NotEmpty(t, a.headers)
	return a.headers[len(a.headers)-1]
}

type stack struct {
	kayra   *fakeKayra
	compute *computeAPI
	ulgen   string
	reg     *prometheus.Registry
}

// newStack is Kayra, a gateway with the compute API, and ulgen, as main
// sets it up. Kayra lists compute (with its MCP), docs (without), and
// kayra's own groups.
func newStack(t *testing.T, ttl time.Duration) *stack {
	t.Helper()
	s := &stack{kayra: &fakeKayra{}, compute: &computeAPI{}, reg: prometheus.NewRegistry()}
	k := s.kayra.start(t)
	gw := httptest.NewServer(s.compute.engine(t))
	t.Cleanup(gw.Close)
	s.kayra.apis = []map[string]any{
		{"api": "system.infrapi.io", "short_name": "system", "version": "v1", "builtin": true},
		{"api": "compute.infrapi.io", "short_name": "compute", "version": "v1"},
		{"api": "docs.infrapi.io", "short_name": "", "version": "v1"},
		{"api": "pending.infrapi.io", "short_name": "pending", "version": "v1"},
	}
	s.kayra.spec = map[string]map[string]any{
		"compute.infrapi.io": {"/apis/compute.infrapi.io/v1/-/mcp": gin.H{}, "/apis/compute.infrapi.io/v1/namespaces/{namespace}/vms/{vm}": gin.H{}},
		"docs.infrapi.io":    {"/apis/docs.infrapi.io/v1/-/healthz": gin.H{}},
	}

	var ulgenURL string
	mux := http.NewServeMux()
	us := httptest.NewServer(mux)
	t.Cleanup(us.Close)
	ulgenURL = us.URL
	s.kayra.audience = ulgenURL

	client, err := auth.NewClient(auth.ClientOptions{BaseURL: k.URL, CacheTTL: -1, PublicURL: ulgenURL})
	require.NoError(t, err)
	e, err := server.NewServerGin(appConfig(t, "ulgen", ulgenURL), []string{ulgenRoot}, server.WithProtectedResource(client.AuthorizationServer))
	require.NoError(t, err)
	log, err := logger.New(logger.Options{Level: "error", Format: "json"})
	require.NoError(t, err)
	g := gateway.New(gateway.Options{
		Catalog: kayra.New(client, k.URL, 10*time.Second), GatewayURL: gw.URL, ToolsTTL: ttl, Logger: log,
		Metrics: metrics.New(s.reg, "test"),
	})
	t.Cleanup(g.Close)
	e.Any(ulgenRoot+server.MCPPath, client.Middleware(), g.Serve)
	e.Any(ulgenRoot+server.MCPPath+"/:api", client.Middleware(), g.Serve)
	mux.Handle("/", e)
	s.ulgen = ulgenURL
	return s
}

// bearer adds a token and a request id to every request.
type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	r.Header.Set("X-Request-Id", "req-42")
	return http.DefaultTransport.RoundTrip(r)
}

func (s *stack) session(t *testing.T, path, token string) *sdk.ClientSession {
	t.Helper()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "agent", Version: "v1"}, nil).Connect(context.Background(),
		&sdk.StreamableClientTransport{Endpoint: s.ulgen + path, HTTPClient: &http.Client{Transport: bearer{token}}}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, session.Close()) })
	return session
}

func names(t *testing.T, session *sdk.ClientSession) []string {
	t.Helper()
	var out []string
	for tool, err := range session.Tools(context.Background(), nil) {
		require.NoError(t, err)
		out = append(out, tool.Name)
	}
	sort.Strings(out)
	return out
}

// Without a token, or with one for another resource: 401, naming the
// protected resource metadata an MCP client logs in from.
func TestUnauthenticated(t *testing.T) {
	s := newStack(t, time.Minute)
	for _, token := range []string{"", "elsewhere", "nope"} {
		req, err := http.NewRequest(http.MethodPost, s.ulgen+ulgenRoot+"/-/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		require.NoError(t, err)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, token)
		assert.Contains(t, resp.Header.Get("WWW-Authenticate"), `resource_metadata="`+s.ulgen+ulgenRoot+"/-/oauth-protected-resource", token)
	}

	resp, err := http.Get(s.ulgen + ulgenRoot + "/-/oauth-protected-resource")
	require.NoError(t, err)
	defer func() { require.NoError(t, resp.Body.Close()) }()
	var prm map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&prm))
	assert.Equal(t, s.ulgen+ulgenRoot, prm["resource"])
	assert.Equal(t, []any{s.kayra.url}, prm["authorization_servers"])
}

// The APIs Kayra knows that serve MCP: their tools prefixed by their short
// name on the aggregate, under their own names on their endpoint.
func TestTools(t *testing.T) {
	s := newStack(t, time.Minute)
	assert.Equal(t, []string{"compute_get_vm", "list_namespaces"}, names(t, s.session(t, ulgenRoot+"/-/mcp", "jdoe")))
	assert.Equal(t, []string{"get_vm", "list_namespaces"}, names(t, s.session(t, ulgenRoot+"/-/mcp/compute", "jdoe")))

	resp, err := http.Post(s.ulgen+ulgenRoot+"/-/mcp/nope", "application/json", strings.NewReader("{}"))
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "a token first")
}

// A call goes to its API with the caller's token and request id; the
// API's refusal is the agent's tool error.
func TestCall(t *testing.T) {
	s := newStack(t, time.Minute)
	session := s.session(t, ulgenRoot+"/-/mcp", "jdoe")
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: "compute_get_vm",
		Arguments: map[string]any{"namespace": "acme", "vm": "web-1"}})
	require.NoError(t, err)
	require.False(t, res.IsError, res.Content)
	assert.Equal(t, map[string]any{"name": "web-1", "namespace": "acme"}, res.StructuredContent)
	h := s.compute.last(t)
	assert.Equal(t, "Bearer jdoe", h.Get("Authorization"))
	assert.Equal(t, "req-42", h.Get("X-Request-Id"))

	res, err = session.CallTool(context.Background(), &sdk.CallToolParams{Name: "compute_get_vm",
		Arguments: map[string]any{"namespace": "beta", "vm": "web-1"}})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, res.Content[0].(*sdk.TextContent).Text, "not in beta")

	own := s.session(t, ulgenRoot+"/-/mcp/compute", "jdoe")
	res, err = own.CallTool(context.Background(), &sdk.CallToolParams{Name: "get_vm", Arguments: map[string]any{"namespace": "acme", "vm": "db"}})
	require.NoError(t, err)
	require.False(t, res.IsError, res.Content)

	n, err := s.reg.Gather()
	require.NoError(t, err)
	found := map[string]float64{}
	for _, mf := range n {
		if mf.GetName() != "ulgen_tool_calls_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			var outcome string
			for _, l := range m.GetLabel() {
				if l.GetName() == "outcome" {
					outcome = l.GetValue()
				}
			}
			found[outcome] += m.GetCounter().GetValue()
		}
	}
	assert.Equal(t, map[string]float64{"ok": 2, "tool_error": 1}, found)
}

// list_namespaces: the caller's namespaces, as Kayra lists them.
func TestListNamespaces(t *testing.T) {
	s := newStack(t, time.Minute)
	res, err := s.session(t, ulgenRoot+"/-/mcp", "jdoe").CallTool(context.Background(), &sdk.CallToolParams{Name: "list_namespaces"})
	require.NoError(t, err)
	require.False(t, res.IsError, res.Content)
	assert.Equal(t, map[string]any{"namespaces": []any{"acme", "beta"}}, res.StructuredContent)
}

// The APIs are listed again after ToolsTTL: one gone takes its tools and
// its endpoint with it.
func TestRefresh(t *testing.T) {
	s := newStack(t, 0)
	session := s.session(t, ulgenRoot+"/-/mcp", "jdoe")
	assert.Contains(t, names(t, session), "compute_get_vm")

	s.kayra.mu.Lock()
	s.kayra.apis = s.kayra.apis[:1]
	s.kayra.mu.Unlock()
	assert.Equal(t, []string{"list_namespaces"}, names(t, session))
}
