package config

import (
	"os"
	"path/filepath"
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
    authorize: thanos
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
	path := filepath.Join(t.TempDir(), "thanos.yaml")
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

	cases := map[string]struct{ from, to, want string }{
		"unknown field":         {"tenant: {inject: true}", "tenant: {inject: true}\n    typo: 1", "typo"},
		"bad name":              {"name: grafana", "name: Grafana", "lowercase"},
		"duplicate":             {"name: telemetry", "name: grafana", "duplicated"},
		"thanos without api":    {"api: grafana.example.io", "api: \"\"", "needs api"},
		"upstream needs caller": {"credential: {passthrough: true}", "credential: {vault: {mount: s, path: p, key: k}, header: H}", "passthrough"},
		"both credentials":      {"credential: {passthrough: true}", "credential: {passthrough: true, vault: {mount: s, path: p, key: k}}", "exclusive"},
		"bad authorize":         {"authorize: upstream", "authorize: nobody", "authorize"},
		"no vault":              {"vault: {address: https://vault.example.com, role_id_env: ROLE, secret_id_file: /run/secret-id}", "", "vault:"},
		"tenant_check without verb": {"tenant: {argument: namespace}", "tenant: {argument: namespace}\n    tenant_check: {resource: dashboards}", "tenant_check"},
		"missing env header":    {"tenant: {inject: true}", "tenant: {inject: true}\n    headers_from_env: {Authorization: THANOS_TEST_UNSET}", "THANOS_TEST_UNSET"},
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
