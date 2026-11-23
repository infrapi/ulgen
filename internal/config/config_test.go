package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const base = `
kayra: {url: https://kayra.example.com}
vault: {address: https://vault.example.com, role_id_env: ROLE, secret_id_file: /run/secret-id}
upstreams:
  - name: grafana
    url: http://mcp-grafana:8000/mcp
    api: grafana.example.io
    authorize: ulgen
    credential: {vault: {mount: secret, path: "grafana/{tenant}", key: token}, header: X-Grafana-Service-Account-Token}
    tenant: {inject: true}
  - name: telemetry
    url: https://api.example.com/apis/telemetry.example.io/v1/-/mcp
    api: telemetry.example.io
    authorize: upstream
    credential: {passthrough: true}
    tenant: {argument: namespace}
`

func load(t *testing.T, content string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ulgen.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestLoad(t *testing.T) {
	cfg, err := load(t, base)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c := cfg.Upstreams[0].TenantCheck; c == nil || c.Resource != "tools" || c.Verb != "get" || cfg.Upstreams[1].TenantCheck != nil {
		t.Errorf("tenant_check defaults: %+v %+v", cfg.Upstreams[0].TenantCheck, cfg.Upstreams[1].TenantCheck)
	}
	if cfg.Listen != ":8080" || cfg.Path != "/mcp" || cfg.Upstreams[0].Version != "v1" || cfg.Upstreams[0].ProtocolVersion != "2025-06-18" {
		t.Errorf("defaults not applied: %+v", cfg)
	}

	withRules, err := load(t, strings.Replace(base, "tenant: {inject: true}", "tenant: {inject: true}\n    allowed_tools: [\"search_.*\", {match: \"query_.*\", resource: prometheus}, {match: \"get_.*\", resource: dashboards, verb: get}]", 1))
	if err != nil {
		t.Fatalf("Load with rules: %v", err)
	}
	g := withRules.Upstreams[0]
	if r, ok := g.ToolRule("query_prometheus"); !ok || r.Resource != "prometheus" {
		t.Errorf("query_prometheus rule = %+v %v", r, ok)
	}
	if r, ok := g.ToolRule("search_dashboards"); !ok || r.Resource != "" {
		t.Errorf("search_dashboards rule = %+v %v", r, ok)
	}
	if _, ok := g.ToolRule("delete_dashboard"); ok {
		t.Errorf("delete_dashboard matches no rule: not exposed")
	}
	if c := g.TenantCheck; c == nil || c.Resource != "prometheus" || c.Verb != "get" {
		t.Errorf("tenant_check must default to the first rule's resource: %+v", c)
	}
	if res := g.Resources(); !slices.Equal(res, []string{"prometheus", "dashboards", "tools"}) {
		t.Errorf("resources = %v", res)
	}

	withCheck, err := load(t, strings.Replace(base, "tenant: {inject: true}", "tenant: {inject: true}\n    check: {resource: dashboards, verb: get}", 1))
	if err != nil {
		t.Fatalf("Load with check: %v", err)
	}
	if c := withCheck.Upstreams[0].TenantCheck; c == nil || c.Resource != "dashboards" || c.Verb != "get" {
		t.Errorf("tenant_check must default to check: %+v", c)
	}

	cases := map[string]struct{ from, to, want string }{
		"unknown field":             {"tenant: {inject: true}", "tenant: {inject: true}\n    typo: 1", "typo"},
		"bad name":                  {"name: grafana", "name: Grafana", "lowercase"},
		"duplicate":                 {"name: telemetry", "name: grafana", "duplicated"},
		"ulgen without api":         {"api: grafana.example.io", "api: \"\"", "needs api"},
		"upstream needs caller":     {"credential: {passthrough: true}", "credential: {vault: {mount: s, path: p, key: k}, header: H}", "passthrough"},
		"both credentials":          {"credential: {passthrough: true}", "credential: {passthrough: true, vault: {mount: s, path: p, key: k}}", "exclusive"},
		"bad authorize":             {"authorize: upstream", "authorize: nobody", "authorize"},
		"no vault":                  {"vault: {address: https://vault.example.com, role_id_env: ROLE, secret_id_file: /run/secret-id}", "", "vault:"},
		"tenant_check without verb": {"tenant: {argument: namespace}", "tenant: {argument: namespace}\n    tenant_check: {resource: dashboards}", "tenant_check"},
		"missing env header":        {"tenant: {inject: true}", "tenant: {inject: true}\n    headers_from_env: {Authorization: ULGEN_TEST_UNSET}", "ULGEN_TEST_UNSET"},
		"tls without key":           {"kayra: {url: https://kayra.example.com}", "kayra: {url: https://kayra.example.com}\ntls: {cert_file: /etc/ulgen/cert.pem}", "tls: cert_file and key_file"},
		"allowed_tools typo":        {"tenant: {inject: true}", "tenant: {inject: true}\n    allowed_tools: [{match: x, resouce: y}]", "resouce"},
		"verb without resource":     {"tenant: {inject: true}", "tenant: {inject: true}\n    allowed_tools: [{match: x, verb: get}]", "verb without resource"},
		"resource on upstream":      {"tenant: {argument: namespace}", "tenant: {argument: namespace}\n    allowed_tools: [{match: x, resource: y}]", "only with authorize: ulgen"},
		"bad resource name":         {"tenant: {inject: true}", "tenant: {inject: true}\n    allowed_tools: [{match: x, resource: Prom}]", "lowercase"},
		"bad allowed_tools":         {"tenant: {inject: true}", "tenant: {inject: true}\n    allowed_tools: [\"query_(\"]", "allowed_tools"},
		"check without verb":        {"tenant: {inject: true}", "tenant: {inject: true}\n    check: {resource: dashboards}", "check: needs"},
		"check on upstream":         {"tenant: {argument: namespace}", "tenant: {argument: namespace}\n    check: {resource: dashboards, verb: get}", "only with authorize: ulgen"},
	}
	for desc, tc := range cases {
		content := strings.Replace(base, tc.from, tc.to, 1)
		if content == base {
			t.Fatalf("%s: replacement did not apply", desc)
		}
		if _, err := load(t, content); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", desc, err, tc.want)
		}
	}
}

// The example in the repository is a valid configuration.
func TestExample(t *testing.T) {
	t.Setenv("MCP_GRAFANA_CALLER_AUTH", "Bearer example")
	cfg, err := Load(filepath.Join("..", "..", "ulgen.example.yaml"))
	if err != nil {
		t.Fatalf("ulgen.example.yaml: %v", err)
	}
	if len(cfg.Upstreams) == 0 {
		t.Error("ulgen.example.yaml: no upstream")
	}
}
