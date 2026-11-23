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
    api: grafana.infrapi.io
    authorize: ulgen
    credential: {vault: {mount: secret, path: "grafana/{namespace}", key: token}, header: X-Grafana-Service-Account-Token}
    namespace: {inject: true}
  - name: telemetry
    url: https://api.example.com/apis/telemetry.infrapi.io/v1/-/mcp
    api: telemetry.infrapi.io
    authorize: upstream
    credential: {passthrough: true}
    namespace: {argument: namespace}
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
	if c := cfg.Upstreams[0].NamespaceCheck; c == nil || c.Resource != "tools" || c.Verb != "get" || cfg.Upstreams[1].NamespaceCheck != nil {
		t.Errorf("namespace_check defaults: %+v %+v", cfg.Upstreams[0].NamespaceCheck, cfg.Upstreams[1].NamespaceCheck)
	}
	if cfg.Listen != ":8080" || cfg.Path != "/mcp" || cfg.Upstreams[0].Version != "v1" || cfg.Upstreams[0].ProtocolVersion != "2025-06-18" {
		t.Errorf("defaults not applied: %+v", cfg)
	}

	withRules, err := load(t, strings.Replace(base, "namespace: {inject: true}", "namespace: {inject: true}\n    allowed_tools: [\"search_.*\", {match: \"query_.*\", resource: prometheus}, {match: \"get_.*\", resource: dashboards, verb: get}]", 1))
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
	if c := g.NamespaceCheck; c == nil || c.Resource != "prometheus" || c.Verb != "get" {
		t.Errorf("namespace_check must default to the first rule's resource: %+v", c)
	}
	if res := g.Resources(); !slices.Equal(res, []string{"prometheus", "dashboards", "tools"}) {
		t.Errorf("resources = %v", res)
	}

	withCheck, err := load(t, strings.Replace(base, "namespace: {inject: true}", "namespace: {inject: true}\n    check: {resource: dashboards, verb: get}", 1))
	if err != nil {
		t.Fatalf("Load with check: %v", err)
	}
	if c := withCheck.Upstreams[0].NamespaceCheck; c == nil || c.Resource != "dashboards" || c.Verb != "get" {
		t.Errorf("namespace_check must default to check: %+v", c)
	}

	cases := map[string]struct{ from, to, want string }{
		"unknown field":                {"namespace: {inject: true}", "namespace: {inject: true}\n    typo: 1", "typo"},
		"bad name":                     {"name: grafana", "name: Grafana", "lowercase"},
		"duplicate":                    {"name: telemetry", "name: grafana", "duplicated"},
		"ulgen without api":            {"api: grafana.infrapi.io", "api: \"\"", "needs api"},
		"upstream needs caller":        {"credential: {passthrough: true}", "credential: {vault: {mount: s, path: p, key: k}, header: H}", "passthrough"},
		"both credentials":             {"credential: {passthrough: true}", "credential: {passthrough: true, vault: {mount: s, path: p, key: k}}", "exclusive"},
		"bad authorize":                {"authorize: upstream", "authorize: nobody", "authorize"},
		"no vault":                     {"vault: {address: https://vault.example.com, role_id_env: ROLE, secret_id_file: /run/secret-id}", "", "vault:"},
		"namespace_check without verb": {"namespace: {argument: namespace}", "namespace: {argument: namespace}\n    namespace_check: {resource: dashboards}", "namespace_check"},
		"missing env header":           {"namespace: {inject: true}", "namespace: {inject: true}\n    headers_from_env: {Authorization: ULGEN_TEST_UNSET}", "ULGEN_TEST_UNSET"},
		"tls without key":              {"kayra: {url: https://kayra.example.com}", "kayra: {url: https://kayra.example.com}\ntls: {cert_file: /etc/ulgen/cert.pem}", "tls: cert_file and key_file"},
		"allowed_tools typo":           {"namespace: {inject: true}", "namespace: {inject: true}\n    allowed_tools: [{match: x, resouce: y}]", "resouce"},
		"verb without resource":        {"namespace: {inject: true}", "namespace: {inject: true}\n    allowed_tools: [{match: x, verb: get}]", "verb without resource"},
		"resource on upstream":         {"namespace: {argument: namespace}", "namespace: {argument: namespace}\n    allowed_tools: [{match: x, resource: y}]", "only with authorize: ulgen"},
		"bad resource name":            {"namespace: {inject: true}", "namespace: {inject: true}\n    allowed_tools: [{match: x, resource: Prom}]", "lowercase"},
		"bad allowed_tools":            {"namespace: {inject: true}", "namespace: {inject: true}\n    allowed_tools: [\"query_(\"]", "allowed_tools"},
		"check without verb":           {"namespace: {inject: true}", "namespace: {inject: true}\n    check: {resource: dashboards}", "check: needs"},
		"check on upstream":            {"namespace: {argument: namespace}", "namespace: {argument: namespace}\n    check: {resource: dashboards, verb: get}", "only with authorize: ulgen"},
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
