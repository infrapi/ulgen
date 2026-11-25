// Package config is ulgen's settings, read from the dotenv file
// INFRAPI_CONFIG_DOTENV_FILE names (.env when unset): infrapi/lib's own
// INFRAPI_APP_* keys, and ulgen's, INFRAPI_ULGEN_*.
package config

import (
	"fmt"
	"regexp"
	"time"

	libconfig "github.com/infrapi/lib/pkg/config"
)

// Config is ulgen's settings.
type Config struct {
	// App is infrapi/lib's: the name, platform, listen address, public URL,
	// logs, CORS... (INFRAPI_APP_*). AppUrl is where MCP clients reach ulgen:
	// the resource their tokens are for.
	App *libconfig.AppConfig
	// API is ulgen's own API group: its endpoints are under
	// /apis/<API>/v1, its MCP endpoint <root>/-/mcp. INFRAPI_ULGEN_API.
	API string
	// AuthURL is Kayra's URL: the callers' tokens, the platform's APIs, the
	// callers' namespaces. INFRAPI_ULGEN_AUTH_URL.
	AuthURL string
	// AuthCacheTTL is how long a caller's identity is kept:
	// INFRAPI_ULGEN_AUTH_CACHE_TTL, 1m.
	AuthCacheTTL time.Duration
	// GatewayURL is where the APIs are reached: INFRAPI_ULGEN_GATEWAY_URL,
	// AuthURL when unset.
	GatewayURL string
	// Resource is what MCP clients ask their tokens for (the protected
	// resource metadata's resource): INFRAPI_ULGEN_RESOURCE, ulgen's own URL
	// (AppUrl and its root) when unset. ulgen passes the caller's token on to
	// the APIs, which accept it only if issued for them or a URL they are
	// under: the gateway's, a URL ulgen's root is under too.
	Resource string
	// Include and Exclude select the APIs served, a regular expression on
	// the API's whole name: INFRAPI_ULGEN_APIS_INCLUDE (every API when
	// unset) and INFRAPI_ULGEN_APIS_EXCLUDE (none when unset).
	Include, Exclude *regexp.Regexp
	// ToolsTTL is how long the APIs and their tools are kept before they are
	// listed again: INFRAPI_ULGEN_TOOLS_TTL, 5m.
	ToolsTTL time.Duration
}

// Root is ulgen's root: /apis/<API>/v1.
func (c *Config) Root() string { return "/apis/" + c.API + "/v1" }

// Load reads the settings, and fails on a missing or invalid one.
func Load() (*Config, error) {
	vars, err := libconfig.NewConfig()
	if err != nil {
		return nil, err
	}
	return FromVariables(vars)
}

// FromVariables reads the settings from vars.
func FromVariables(vars *libconfig.Variables) (*Config, error) {
	app, err := vars.GetAppConfig()
	if err != nil {
		return nil, err
	}
	c := &Config{App: app}
	if c.API, err = vars.GetString(&libconfig.VariableOpts{Key: "INFRAPI_ULGEN_API", Validator: "fqdn"}); err != nil {
		return nil, err
	}
	if c.AuthURL, err = vars.GetString(&libconfig.VariableOpts{Key: "INFRAPI_ULGEN_AUTH_URL", Validator: "url"}); err != nil {
		return nil, err
	}
	if c.AuthCacheTTL, err = vars.GetDuration(&libconfig.VariableOpts{Key: "INFRAPI_ULGEN_AUTH_CACHE_TTL", Default: "1m"}); err != nil {
		return nil, err
	}
	if c.GatewayURL, err = vars.GetString(&libconfig.VariableOpts{Key: "INFRAPI_ULGEN_GATEWAY_URL", Default: c.AuthURL, Validator: "url"}); err != nil {
		return nil, err
	}
	if c.Resource, err = vars.GetString(&libconfig.VariableOpts{Key: "INFRAPI_ULGEN_RESOURCE", Optional: true, Validator: "omitempty,url"}); err != nil {
		return nil, err
	}
	if c.ToolsTTL, err = vars.GetDuration(&libconfig.VariableOpts{Key: "INFRAPI_ULGEN_TOOLS_TTL", Default: "5m"}); err != nil {
		return nil, err
	}
	for _, re := range []struct {
		key string
		to  **regexp.Regexp
	}{{"INFRAPI_ULGEN_APIS_INCLUDE", &c.Include}, {"INFRAPI_ULGEN_APIS_EXCLUDE", &c.Exclude}} {
		s, err := vars.GetString(&libconfig.VariableOpts{Key: re.key, Optional: true})
		if err != nil {
			return nil, err
		}
		if s == "" {
			continue
		}
		if *re.to, err = regexp.Compile("^(?:" + s + ")$"); err != nil {
			return nil, fmt.Errorf("key %s: %w, dotenv located at %s", re.key, err, vars.Path)
		}
	}
	return c, nil
}

// Serves tells whether the API named name is served: not ulgen's own, kept
// by Include, not dropped by Exclude.
func (c *Config) Serves(name string) bool {
	return name != c.API && (c.Include == nil || c.Include.MatchString(name)) && (c.Exclude == nil || !c.Exclude.MatchString(name))
}
