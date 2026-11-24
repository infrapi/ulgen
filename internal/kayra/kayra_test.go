package kayra_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/infrapi/lib/pkg/auth"
	infraerrors "github.com/infrapi/lib/pkg/errors"

	"github.com/infrapi/ulgen/internal/kayra"
)

// kayraServer answers routes, path -> status and body; the discovery
// document unless routes has it.
func kayraServer(t *testing.T, routes map[string][2]string) *kayra.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == auth.DiscoveryPath {
			if _, own := routes[auth.DiscoveryPath]; !own {
				w.Header().Set("Content-Type", "application/json")
				_, err := w.Write([]byte(`{"issuer":"x","domain":"infrapi.io","apis":{"system":"/apis/system.infrapi.io/v1","catalog":"/apis/catalog.infrapi.io/v1"},"endpoints":{"apis":"/apis","check":"/check","userinfo":"/oauth2/userinfo"}}`))
				assert.NoError(t, err)
				return
			}
		}
		if r.URL.Path != auth.DiscoveryPath {
			assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"), "as the caller")
		}
		route, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		code := http.StatusOK
		if route[0] != "" {
			code = map[string]int{"404": 404, "403": 403, "500": 500}[route[0]]
		}
		w.WriteHeader(code)
		_, err := w.Write([]byte(route[1]))
		assert.NoError(t, err)
	}))
	t.Cleanup(srv.Close)
	a, err := auth.NewClient(auth.ClientOptions{BaseURL: srv.URL})
	require.NoError(t, err)
	return kayra.New(a, srv.URL, 5*time.Second)
}

const spec = "/apis/catalog.infrapi.io/v1/apiregistrations/"

// The registered APIs serves keeps whose spec has an MCP endpoint; a short
// name and a version by default.
func TestAPIs(t *testing.T) {
	c := kayraServer(t, map[string][2]string{
		"/apis": {"", `[{"api":"system.infrapi.io","builtin":true},
			{"api":"compute.infrapi.io","short_name":"vm","version":"v2"},
			{"api":"docs.infrapi.io"},
			{"api":"plain.infrapi.io"},
			{"api":"pending.infrapi.io"},
			{"api":"skipped.infrapi.io"}]`},
		spec + "compute.infrapi.io/openapi.json": {"", `{"paths":{"/apis/compute.infrapi.io/v2/-/mcp":{}}}`},
		spec + "docs.infrapi.io/openapi.json":    {"", `{"paths":{"/apis/docs.infrapi.io/v1/-/mcp":{}}}`},
		spec + "plain.infrapi.io/openapi.json":   {"", `{"paths":{"/apis/plain.infrapi.io/v1/-/healthz":{}}}`},
	})
	apis, err := c.APIs(context.Background(), "tok", func(name string) bool { return name != "skipped.infrapi.io" })
	require.NoError(t, err)
	assert.Equal(t, []kayra.API{
		{Name: "compute.infrapi.io", Short: "vm", Version: "v2"},
		{Name: "docs.infrapi.io", Short: "docs", Version: "v1"},
	}, apis)
	assert.Equal(t, "/apis/compute.infrapi.io/v2", apis[0].Root())
}

func TestAPIsFail(t *testing.T) {
	all := func(string) bool { return true }
	for name, routes := range map[string]map[string][2]string{
		"apis refused":   {"/apis": {"403", `{}`}},
		"apis not json":  {"/apis": {"", `nope`}},
		"spec fails":     {"/apis": {"", `[{"api":"a.infrapi.io"}]`}, spec + "a.infrapi.io/openapi.json": {"500", `{}`}},
		"no catalog":     {auth.DiscoveryPath: {"", `{"apis":{},"endpoints":{"check":"/check","userinfo":"/u"}}`}},
		"discovery down": {auth.DiscoveryPath: {"500", ``}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := kayraServer(t, routes).APIs(context.Background(), "tok", all)
			require.Error(t, err)
		})
	}
	_, err := kayraServer(t, map[string][2]string{"/apis": {"403", `{}`}}).APIs(context.Background(), "tok", all)
	assert.Equal(t, http.StatusForbidden, infraerrors.ErrorCode(err), "Kayra's status")
}

func TestNamespaces(t *testing.T) {
	c := kayraServer(t, map[string][2]string{"/apis/system.infrapi.io/v1/namespaces": {"", `[{"name":"acme"},{"name":"beta"}]`}})
	ns, err := c.Namespaces(context.Background(), "tok")
	require.NoError(t, err)
	assert.Equal(t, []string{"acme", "beta"}, ns)

	_, err = kayraServer(t, map[string][2]string{"/apis/system.infrapi.io/v1/namespaces": {"403", `{}`}}).Namespaces(context.Background(), "tok")
	assert.Equal(t, http.StatusForbidden, infraerrors.ErrorCode(err))
	_, err = kayraServer(t, map[string][2]string{auth.DiscoveryPath: {"", `{"apis":{},"endpoints":{"check":"/check","userinfo":"/u"}}`}}).Namespaces(context.Background(), "tok")
	require.Error(t, err)
}
