package config_test

import (
	"maps"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	libconfig "github.com/infrapi/lib/pkg/config"

	"github.com/infrapi/ulgen/internal/config"
)

var base = map[string]string{
	"INFRAPI_APP_NAME": "ulgen", "INFRAPI_APP_PLATFORM": "sandbox", "INFRAPI_APP_REGION": "eu-west-1",
	"INFRAPI_APP_LOCATION": "dc1", "INFRAPI_APP_FQDN": "ulgen.example.com", "INFRAPI_APP_URL": "https://api.example.com",
	"INFRAPI_ULGEN_API": "mcp.infrapi.io", "INFRAPI_ULGEN_AUTH_URL": "https://kayra.example.com",
}

func load(t *testing.T, extra map[string]string) (*config.Config, error) {
	t.Helper()
	v := maps.Clone(base)
	maps.Copy(v, extra)
	for k, val := range v {
		if val == "-" {
			delete(v, k)
		}
	}
	return config.FromVariables(libconfig.NewConfigFromMap(v, "ulgen.env"))
}

// The defaults: Kayra is the gateway, every API, 1m and 5m.
func TestDefaults(t *testing.T) {
	c, err := load(t, nil)
	require.NoError(t, err)
	assert.Equal(t, "/apis/mcp.infrapi.io/v1", c.Root())
	assert.Equal(t, "https://kayra.example.com", c.GatewayURL)
	assert.Equal(t, time.Minute, c.AuthCacheTTL)
	assert.Equal(t, 5*time.Minute, c.ToolsTTL)
	assert.True(t, c.Serves("compute.infrapi.io"))
	assert.False(t, c.Serves("mcp.infrapi.io"), "not its own")
}

// INFRAPI_ULGEN_APIS_INCLUDE and _EXCLUDE: whole names.
func TestServes(t *testing.T) {
	c, err := load(t, map[string]string{
		"INFRAPI_ULGEN_APIS_INCLUDE": `.*\.infrapi\.io`, "INFRAPI_ULGEN_APIS_EXCLUDE": "secret.infrapi.io",
		"INFRAPI_ULGEN_GATEWAY_URL": "https://api.example.com", "INFRAPI_ULGEN_TOOLS_TTL": "30s",
	})
	require.NoError(t, err)
	assert.True(t, c.Serves("compute.infrapi.io"))
	assert.False(t, c.Serves("secret.infrapi.io"))
	assert.False(t, c.Serves("compute.infrapi.io.evil"), "anchored")
	assert.False(t, c.Serves("other.example.com"))
	assert.Equal(t, "https://api.example.com", c.GatewayURL)
	assert.Equal(t, 30*time.Second, c.ToolsTTL)
}

func TestRefused(t *testing.T) {
	for name, extra := range map[string]map[string]string{
		"no api":          {"INFRAPI_ULGEN_API": "-"},
		"api not a name":  {"INFRAPI_ULGEN_API": "not a name"},
		"no kayra":        {"INFRAPI_ULGEN_AUTH_URL": "-"},
		"kayra not a url": {"INFRAPI_ULGEN_AUTH_URL": "kayra"},
		"bad gateway":     {"INFRAPI_ULGEN_GATEWAY_URL": "nope"},
		"bad ttl":         {"INFRAPI_ULGEN_TOOLS_TTL": "soon"},
		"bad cache ttl":   {"INFRAPI_ULGEN_AUTH_CACHE_TTL": "soon"},
		"bad include":     {"INFRAPI_ULGEN_APIS_INCLUDE": "("},
		"bad exclude":     {"INFRAPI_ULGEN_APIS_EXCLUDE": "("},
		"no app":          {"INFRAPI_APP_NAME": "-"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := load(t, extra)
			require.Error(t, err)
		})
	}
}
