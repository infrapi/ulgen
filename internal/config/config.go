// Package config loads ulgen's YAML configuration: where it listens, the
// Kayra it asks, the Vault it reads tenant credentials from, and the
// upstream MCP servers it aggregates. Upstreams are configured one block
// each, never per tool: their tools are discovered.
package config

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root of ulgen's configuration file.
type Config struct {
	// Listen is the HTTP listen address, ":8080" when empty.
	Listen string `yaml:"listen"`
	// TLS, when set, serves HTTPS on Listen: for callers' tokens crossing
	// a network.
	TLS *TLSConfig `yaml:"tls"`
	// Path is where the MCP endpoint is served, "/mcp" when empty.
	Path string `yaml:"path"`
	// TokenHeader carries the caller's Kayra token, "Authorization" when
	// empty: a header of its own when a proxy in front passes the token
	// that way.
	TokenHeader string `yaml:"token_header"`
	// ResourceMetadata is the URL of the OAuth protected resource metadata
	// (RFC 9728) a 401 names in WWW-Authenticate: where an MCP client
	// finds the authorization server to log in at.
	ResourceMetadata string `yaml:"resource_metadata"`
	// AnonymousListing serves the MCP handshake and tools/list without a
	// token: a gateway's health check and UI with no user behind them. Off,
	// every request needs a token, and a client without one gets the 401
	// that starts its login.
	AnonymousListing bool `yaml:"anonymous_listing"`
	// ListTokenFile, optional, holds a Kayra token of ulgen's own, re-read
	// at each listing: what lists the tools of a passthrough upstream (one
	// that wants a token for anything, an infrapi/lib MCP) when tools/list
	// comes without a caller's token — a gateway's UI listing the tools of
	// its servers. Never used to call a tool.
	ListTokenFile string `yaml:"list_token_file"`
	// Kayra resolves callers (userinfo), decides (/check) and lists their
	// tenants (their namespaces).
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

// TLSConfig is ulgen's server certificate: PEM files, re-read when they
// change (a renewed certificate is served without a restart).
type TLSConfig struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// KayraConfig points at Kayra.
type KayraConfig struct {
	URL string `yaml:"url"`
	// CacheTTL caches a caller's resolution (userinfo), a Go duration,
	// 1m when empty. /check is never cached.
	CacheTTL string `yaml:"cache_ttl"`
}

// VaultConfig is how ulgen logs in to Vault: AppRole.
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
	// AuthorizeUlgen: ulgen asks Kayra's /check (api = Upstream.API,
	// resource "tools", verb from the tool's annotations) before calling.
	AuthorizeUlgen Authorize = "ulgen"
	// AuthorizeUpstream: the upstream decides itself, with the caller's
	// own token (credential passthrough), as every infrapi/lib app does.
	AuthorizeUpstream Authorize = "upstream"
)

// Upstream is one MCP server ulgen aggregates.
type Upstream struct {
	// Name prefixes its tools: "<name>_<tool>". Lowercase letters, digits
	// and dashes.
	Name string `yaml:"name"`
	// URL is its Streamable HTTP endpoint.
	URL string `yaml:"url"`
	// API is the Kayra api (long name) /check is asked about, with
	// authorize: ulgen and for list_tenants: the api this upstream is
	// registered as, or, with Check, an existing api whose rights it
	// borrows.
	API string `yaml:"api"`
	// Version of API, for its OpenAPI document; "v1" when empty.
	Version   string    `yaml:"version"`
	Authorize Authorize `yaml:"authorize"`
	// TenantCheck is what list_tenants asks Kayra's /check to tell whether
	// this upstream is usable in a tenant. With authorize: ulgen it is
	// resource "tools", verb "get" unless set; with authorize: upstream,
	// unset lists the upstream in every tenant of the caller.
	TenantCheck *Check `yaml:"tenant_check"`
	// Check, with authorize: ulgen, is the one right every call needs:
	// resource and verb on API, instead of resource "tools" and a verb
	// from the tool's annotations. For an upstream with no api of its own
	// in Kayra, whose rights are another api's (Grafana's read tools on a
	// telemetry api's dashboards: list). One right can't tell reads from
	// writes, so only the upstream's read-only tools are exposed. It is
	// also the default TenantCheck, and ulgen serves no OpenAPI document
	// for API: the api serves its own.
	Check      *Check     `yaml:"check"`
	Credential Credential `yaml:"credential"`
	Tenant     Tenant     `yaml:"tenant"`
	// ProtocolVersion is the MCP protocol version ulgen opens its session
	// with; the upstream may answer an older one. "2025-06-18" when empty:
	// newer versions are refused by stateful servers (mcp-grafana).
	ProtocolVersion string `yaml:"protocol_version"`
	// AllowedTools, when set, are the upstream's tools exposed, first match
	// wins: a regular expression matching the whole tool name (the
	// upstream's, not prefixed), or a rule with the resource of API its
	// calls need. Others are neither listed nor callable.
	AllowedTools []ToolRule `yaml:"allowed_tools"`

	// HeadersFromEnv are static headers sent on every request, valued
	// from environment variables (header -> variable name): the bearer
	// mcp-grafana's --server-auth-token expects, for instance.
	HeadersFromEnv map[string]string `yaml:"headers_from_env"`
}

// ToolRule is one entry of allowed_tools: Match, a regular expression on
// the whole tool name, alone (a plain string) or with, for authorize: ulgen,
// the Resource of API a matching tool's calls need: the right Kayra's /check
// is asked, its verb Verb, or the tool's annotations' (get for a read-only
// tool, create for another write, delete for a destructive one).
type ToolRule struct {
	Match    string `yaml:"match"`
	Resource string `yaml:"resource"`
	Verb     string `yaml:"verb"`
	re       *regexp.Regexp
}

// UnmarshalYAML takes a plain string (Match alone) or a mapping, its keys
// checked: a misspelt one is an error, as everywhere in the file.
func (r *ToolRule) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		return n.Decode(&r.Match)
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: an allowed_tools entry is a regular expression or {match, resource, verb}", n.Line)
	}
	for i := 0; i < len(n.Content); i += 2 {
		switch k := n.Content[i].Value; k {
		case "match", "resource", "verb":
		default:
			return fmt.Errorf("line %d: field %s not found in allowed_tools (match, resource, verb)", n.Content[i].Line, k)
		}
	}
	type plain ToolRule
	return n.Decode((*plain)(r))
}

// ToolRule is the allowed_tools rule exposing the upstream's tool name: nil
// and true without allowed_tools (every tool), the first rule matching it
// whole, or false when none does.
func (u *Upstream) ToolRule(name string) (*ToolRule, bool) {
	if len(u.AllowedTools) == 0 {
		return nil, true
	}
	for i := range u.AllowedTools {
		if u.AllowedTools[i].re.MatchString(name) {
			return &u.AllowedTools[i], true
		}
	}
	return nil, false
}

// Resources are the resources of API the upstream's calls are checked on,
// with authorize: ulgen: its rules', and "tools" for a tool no rule names a
// resource of, unless Check borrows another api's right. Its OpenAPI
// document declares them.
func (u *Upstream) Resources() []string {
	if u.Authorize != AuthorizeUlgen {
		return nil
	}
	var out []string
	fallback := len(u.AllowedTools) == 0
	for _, r := range u.AllowedTools {
		if r.Resource == "" {
			fallback = true
		} else if !slices.Contains(out, r.Resource) {
			out = append(out, r.Resource)
		}
	}
	if fallback && u.Check == nil {
		out = append(out, "tools")
	}
	return out
}

// Credential says what identifies ulgen's calls to an upstream.
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

// Tenant says how a tool names its tenant. Every tool ulgen exposes takes
// it as a "tenant" argument.
type Tenant struct {
	// Inject adds a "tenant" argument to every tool, removed before the
	// call: the upstream has no notion of tenant, its credential does.
	Inject bool `yaml:"inject"`
	// Argument is the upstream's own argument naming the tenant (e.g. an
	// infrapi/lib MCP's "namespace"): ulgen exposes it as "tenant" and
	// renames it back. A tool without it is not tenant-scoped.
	Argument string `yaml:"argument"`
}

var upstreamName = regexp.MustCompile(`^[a-z0-9-]+$`)
var resourceName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

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
	if c.TokenHeader == "" {
		c.TokenHeader = "Authorization"
	}
	if c.Kayra.URL == "" {
		return fmt.Errorf("kayra.url is required")
	}
	if c.TLS != nil && (c.TLS.CertFile == "" || c.TLS.KeyFile == "") {
		return fmt.Errorf("tls: cert_file and key_file are required")
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
		var firstResource string
		for j := range u.AllowedTools {
			r := &u.AllowedTools[j]
			re, err := regexp.Compile("^(?:" + r.Match + ")$")
			if err != nil || r.Match == "" {
				return fmt.Errorf("%s.allowed_tools[%d]: match %q is not a regular expression: %v", where, j, r.Match, err)
			}
			r.re = re
			if r.Resource == "" && r.Verb != "" {
				return fmt.Errorf("%s.allowed_tools[%d]: verb without resource", where, j)
			}
			if r.Resource == "" {
				continue
			}
			if u.Authorize != AuthorizeUlgen {
				return fmt.Errorf("%s.allowed_tools[%d]: resource only with authorize: ulgen (an upstream authorizing itself decides alone)", where, j)
			}
			if !resourceName.MatchString(r.Resource) {
				return fmt.Errorf("%s.allowed_tools[%d]: resource %q must be lowercase letters, digits, - and _", where, j, r.Resource)
			}
			if firstResource == "" {
				firstResource = r.Resource
			}
		}
		if u.Check != nil {
			if u.Authorize != AuthorizeUlgen {
				return fmt.Errorf("%s.check: only with authorize: ulgen (an upstream authorizing itself decides alone)", where)
			}
			if u.Check.Resource == "" || u.Check.Verb == "" {
				return fmt.Errorf("%s.check: needs resource and verb", where)
			}
		}
		if u.Authorize == AuthorizeUlgen && u.TenantCheck == nil {
			switch {
			case u.Check != nil:
				u.TenantCheck = &Check{Resource: u.Check.Resource, Verb: u.Check.Verb}
			case firstResource != "":
				u.TenantCheck = &Check{Resource: firstResource, Verb: "get"}
			default:
				u.TenantCheck = &Check{Resource: "tools", Verb: "get"}
			}
		}
		if u.Tenant.Inject && u.Tenant.Argument != "" {
			return fmt.Errorf("%s.tenant: inject and argument are exclusive", where)
		}
		switch u.Authorize {
		case AuthorizeUlgen:
			if u.API == "" {
				return fmt.Errorf("%s: authorize: ulgen needs api, the Kayra api /check is asked about", where)
			}
			if !u.Tenant.Inject && u.Tenant.Argument == "" {
				return fmt.Errorf("%s: authorize: ulgen needs a tenant (inject or argument) to check against", where)
			}
		case AuthorizeUpstream:
			// An upstream deciding on its own must see who calls.
			if !cred.Passthrough {
				return fmt.Errorf("%s: authorize: upstream needs credential.passthrough, so the upstream sees the caller", where)
			}
		default:
			return fmt.Errorf("%s.authorize: must be %q or %q", where, AuthorizeUlgen, AuthorizeUpstream)
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
