// Package config loads thanos' YAML configuration: where it listens, the
// Kayra it asks, the Vault it reads tenant credentials from, and the
// upstream MCP servers it aggregates. Upstreams are configured one block
// each, never per tool: their tools are discovered.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root of thanos' configuration file.
type Config struct {
	// Listen is the HTTP listen address, ":8080" when empty.
	Listen string `yaml:"listen"`
	// Path is where the MCP endpoint is served, "/mcp" when empty.
	Path string `yaml:"path"`
	// Kayra resolves callers (userinfo), decides (/check) and lists their
	// tenants (/memberships).
	Kayra KayraConfig `yaml:"kayra"`
	// Vault holds tenant credentials. Required when an upstream reads one.
	Vault *VaultConfig `yaml:"vault"`
	// ToolsTTL is how long the aggregated tool list is kept before the
	// upstreams are listed again, a Go duration, 5m when empty.
	ToolsTTL string `yaml:"tools_ttl"`
	// CredentialTTL is how long a credential read from Vault is kept, a
	// Go duration, 1m when empty.
	CredentialTTL string     `yaml:"credential_ttl"`
	Upstreams     []Upstream `yaml:"upstreams"`
}

// KayraConfig points at Kayra.
type KayraConfig struct {
	URL string `yaml:"url"`
	// CacheTTL caches a caller's resolution (userinfo), a Go duration,
	// 1m when empty. /check is never cached.
	CacheTTL string `yaml:"cache_ttl"`
}

// VaultConfig is how thanos logs in to Vault: AppRole.
type VaultConfig struct {
	Address string `yaml:"address"`
	// RoleIDEnv / SecretIDFile: the AppRole credentials, the role id from
	// an environment variable, the secret id from a file re-read at each
	// login so a rotated one is picked up.
	RoleIDEnv    string `yaml:"role_id_env"`
	SecretIDFile string `yaml:"secret_id_file"`
	// AppRoleMount is "approle" when empty.
	AppRoleMount string `yaml:"approle_mount"`
	CACertPath   string `yaml:"ca_cert_path"`
}

// Authorize says who decides whether a caller may call an upstream's tool
// on a tenant.
type Authorize string

const (
	// AuthorizeThanos: thanos asks Kayra's /check (api = Upstream.API,
	// resource "tools", verb from the tool's annotations) before calling.
	AuthorizeThanos Authorize = "thanos"
	// AuthorizeUpstream: the upstream decides itself, with the caller's
	// own token (credential passthrough), as every infrapi/lib app does.
	AuthorizeUpstream Authorize = "upstream"
)

// Upstream is one MCP server thanos aggregates.
type Upstream struct {
	// Name prefixes its tools: "<name>_<tool>". Lowercase letters, digits
	// and dashes.
	Name string `yaml:"name"`
	// URL is its Streamable HTTP endpoint.
	URL string `yaml:"url"`
	// API is the Kayra api (long name) this upstream is registered as:
	// the /check api, with authorize: thanos and for list_tenants.
	API string `yaml:"api"`
	// Version of API, for its OpenAPI document; "v1" when empty.
	Version   string    `yaml:"version"`
	Authorize Authorize `yaml:"authorize"`
	// TenantCheck is what list_tenants asks Kayra's /check to tell whether
	// this upstream is usable in a tenant. With authorize: thanos it is
	// resource "tools", verb "get" unless set; with authorize: upstream,
	// unset lists the upstream in every tenant of the caller.
	TenantCheck *Check     `yaml:"tenant_check"`
	Credential  Credential `yaml:"credential"`
	Tenant      Tenant     `yaml:"tenant"`
	// ProtocolVersion is the MCP protocol version thanos opens its session
	// with; the upstream may answer an older one. "2025-06-18" when empty:
	// newer versions are refused by stateful servers (mcp-grafana).
	ProtocolVersion string `yaml:"protocol_version"`
	// HeadersFromEnv are static headers sent on every request, valued
	// from environment variables (header -> variable name): the bearer
	// mcp-grafana's --server-auth-token expects, for instance.
	HeadersFromEnv map[string]string `yaml:"headers_from_env"`
}

// Credential says what identifies thanos' calls to an upstream.
type Credential struct {
	// Passthrough sends the caller's own bearer token.
	Passthrough bool `yaml:"passthrough"`
	// Vault reads the tenant's credential from Vault (KV v2).
	Vault *VaultSecret `yaml:"vault"`
	// Header carries the Vault credential, e.g.
	// X-Grafana-Service-Account-Token.
	Header string `yaml:"header"`
	// Prefix is prepended to the credential, e.g. "Bearer ".
	Prefix string `yaml:"prefix"`
}

// Check is one /check question: resource and verb on Upstream.API.
type Check struct {
	Resource string `yaml:"resource"`
	Verb     string `yaml:"verb"`
}

// VaultSecret locates one tenant's credential. Path may contain {tenant}.
type VaultSecret struct {
	Mount string `yaml:"mount"`
	Path  string `yaml:"path"`
	Key   string `yaml:"key"`
}

// Tenant says how a tool names its tenant. Every tool thanos exposes takes
// it as a "tenant" argument.
type Tenant struct {
	// Inject adds a "tenant" argument to every tool, removed before the
	// call: the upstream has no notion of tenant, its credential does.
	Inject bool `yaml:"inject"`
	// Argument is the upstream's own argument naming the tenant (e.g. an
	// infrapi/lib MCP's "namespace"): thanos exposes it as "tenant" and
	// renames it back. A tool without it is not tenant-scoped.
	Argument string `yaml:"argument"`
}

var upstreamName = regexp.MustCompile(`^[a-z0-9-]+$`)

// Load reads and validates the file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return &cfg, nil
}

// Validate checks what YAML alone can't, and fills defaults.
func (c *Config) Validate() error {
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.Path == "" {
		c.Path = "/mcp"
	}
	if c.Kayra.URL == "" {
		return fmt.Errorf("kayra.url is required")
	}
	for name, v := range map[string]string{"tools_ttl": c.ToolsTTL, "credential_ttl": c.CredentialTTL, "kayra.cache_ttl": c.Kayra.CacheTTL} {
		if v == "" {
			continue
		}
		if d, err := time.ParseDuration(v); err != nil || d <= 0 {
			return fmt.Errorf("%s: %q is not a positive duration", name, v)
		}
	}
	if len(c.Upstreams) == 0 {
		return fmt.Errorf("upstreams: at least one is required")
	}
	seen := map[string]bool{}
	needVault := false
	for i := range c.Upstreams {
		u := &c.Upstreams[i]
		where := fmt.Sprintf("upstreams[%d]", i)
		if !upstreamName.MatchString(u.Name) {
			return fmt.Errorf("%s.name: %q must be lowercase letters, digits and dashes", where, u.Name)
		}
		if seen[u.Name] {
			return fmt.Errorf("%s.name: %q is duplicated", where, u.Name)
		}
		seen[u.Name] = true
		if u.URL == "" {
			return fmt.Errorf("%s.url is required", where)
		}
		if u.Version == "" {
			u.Version = "v1"
		}
		if u.ProtocolVersion == "" {
			u.ProtocolVersion = "2025-06-18"
		}
		cred := u.Credential
		switch {
		case cred.Passthrough && cred.Vault != nil:
			return fmt.Errorf("%s.credential: passthrough and vault are exclusive", where)
		case !cred.Passthrough && cred.Vault == nil:
			return fmt.Errorf("%s.credential: one of passthrough or vault is required", where)
		case cred.Vault != nil:
			needVault = true
			if cred.Vault.Mount == "" || cred.Vault.Path == "" || cred.Vault.Key == "" || cred.Header == "" {
				return fmt.Errorf("%s.credential: vault needs mount, path and key, and header", where)
			}
		}
		if u.TenantCheck != nil && (u.TenantCheck.Resource == "" || u.TenantCheck.Verb == "" || u.API == "") {
			return fmt.Errorf("%s.tenant_check: needs resource, verb and the upstream's api", where)
		}
		if u.Authorize == AuthorizeThanos && u.TenantCheck == nil {
			u.TenantCheck = &Check{Resource: "tools", Verb: "get"}
		}
		if u.Tenant.Inject && u.Tenant.Argument != "" {
			return fmt.Errorf("%s.tenant: inject and argument are exclusive", where)
		}
		switch u.Authorize {
		case AuthorizeThanos:
			if u.API == "" {
				return fmt.Errorf("%s: authorize: thanos needs api, the Kayra api /check is asked about", where)
			}
			if !u.Tenant.Inject && u.Tenant.Argument == "" {
				return fmt.Errorf("%s: authorize: thanos needs a tenant (inject or argument) to check against", where)
			}
		case AuthorizeUpstream:
			// An upstream deciding on its own must see who calls.
			if !cred.Passthrough {
				return fmt.Errorf("%s: authorize: upstream needs credential.passthrough, so the upstream sees the caller", where)
			}
		default:
			return fmt.Errorf("%s.authorize: must be %q or %q", where, AuthorizeThanos, AuthorizeUpstream)
		}
		for header, env := range u.HeadersFromEnv {
			if os.Getenv(env) == "" {
				return fmt.Errorf("%s.headers_from_env.%s: environment variable %s is empty", where, header, env)
			}
		}
	}
	if needVault {
		if c.Vault == nil || c.Vault.Address == "" || c.Vault.RoleIDEnv == "" || c.Vault.SecretIDFile == "" {
			return fmt.Errorf("vault: address, role_id_env and secret_id_file are required when an upstream reads a credential from vault")
		}
	}
	return nil
}

// Duration parses a validated duration, def when empty.
func Duration(v string, def time.Duration) time.Duration {
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
