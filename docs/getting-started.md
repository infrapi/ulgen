---
title: Getting started
description: Install ulgen, put two MCP servers behind it, register its api in Kayra and connect an MCP client.
weight: 20
---

# Getting started

This page puts ulgen in front of two MCP servers: mcp-grafana, which reads a
namespace's Grafana with that namespace's service account, and an infrapi MCP,
which checks every call itself. It then connects Claude Code to ulgen.

You need:

- a [Kayra](https://github.com/infrapi/kayra) server, with its OAuth
  authorization server enabled for MCP clients to log in
  ([Authorization server](https://infrapi.github.io/kayra/architecture/auth/oauth2-server/));
- a Vault holding each namespace's Grafana token (KV v2), and an AppRole for
  ulgen to read them.

## 1. Install

Linux archives (amd64, arm64) are on the
[releases page](https://github.com/infrapi/ulgen/releases), with
`checksums.txt`:

```bash
v=0.3.0
curl -fsSLO "https://github.com/infrapi/ulgen/releases/download/v$v/ulgen_${v}_linux_amd64.tar.gz"
tar -xzf "ulgen_${v}_linux_amd64.tar.gz" ulgen ulgen.example.yaml
./ulgen -version
```

Or build a container image from the repository's `Dockerfile`:

```bash
podman build -t ulgen .
podman run -v ./ulgen.yaml:/etc/ulgen/ulgen.yaml:ro,z -p 8080:8080 ulgen
```

## 2. Configure

`ulgen.yaml`, from [`ulgen.example.yaml`](https://github.com/infrapi/ulgen/blob/main/ulgen.example.yaml):

```yaml
listen: ":8080"
path: /mcp
# a 401 names it (RFC 9728): where an MCP client finds Kayra to log in at
resource_metadata: https://api.example.com/.well-known/oauth-protected-resource/mcp

kayra:
  url: https://kayra.example.com

vault:
  address: https://vault.example.com:8200
  role_id_env: ULGEN_VAULT_ROLE_ID
  secret_id_file: /run/secrets/ulgen-vault-secret-id

upstreams:
  - name: grafana
    url: http://127.0.0.1:8000/mcp
    api: mcp.infrapi.io            # the Kayra api ulgen checks calls on
    authorize: ulgen
    credential:
      vault: {mount: secret, path: "grafana/{namespace}", key: token}
      header: X-Grafana-Service-Account-Token
    namespace: {inject: true}
    allowed_tools:
      - {match: "query_prometheus.*|list_prometheus_.*", resource: prometheus}
      - {match: "query_loki.*|list_loki_.*", resource: loki}
      - {match: "search_dashboards|get_dashboard.*", resource: dashboards}

  - name: telemetry
    url: https://api.example.com/apis/telemetry.infrapi.io/v1/-/mcp
    api: telemetry.infrapi.io
    authorize: upstream
    credential: {passthrough: true}
    namespace: {argument: namespace}
```

Every key is in [Configuration](configuration.md).

## 3. Run

```bash
export ULGEN_VAULT_ROLE_ID=...
./ulgen -config ulgen.yaml
curl -s localhost:8080/healthz
```

ulgen lists each upstream's tools at start, and again every `tools_ttl`.

## 4. Register the api in Kayra

`grafana` is authorized by ulgen on `mcp.infrapi.io`. ulgen serves that
api's OpenAPI document at `/apis/mcp.infrapi.io/v1/-/openapi.json`, with one
resource per `allowed_tools` rule. Route that path to ulgen on your gateway,
then register the api and subscribe the namespaces:

```bash
infrapi-cli create apiregistrations -d '{"name": "mcp.infrapi.io", "active": true, "owner": "sre", "mode": "none"}'
infrapi-cli create subscriptions -n acme -d '{"api": "mcp.infrapi.io"}'
```

There is nothing to deploy per namespace, so the mode needs no steps.

Grant a namespace role on it, for instance Prometheus and dashboards but not
Loki:

```yaml
rules:
  - apis: [mcp.infrapi.io]
    resources: [prometheus, dashboards]
    verbs: [get]
```

See [Authorization](authorization.md).

## 5. Connect an MCP client

```bash
claude mcp add --transport http infrapi https://api.example.com/mcp
```

The client's first request gets a 401 that names the protected resource
metadata. The client then logs in at Kayra (login and consent in the
browser) and retries with its token. Ask it to `list_namespaces`, then to
query a namespace's Prometheus.

Each call is one `tool call` line in ulgen's log: who called which tool, on
which namespace, and the outcome. See [Audit log and metrics](observability.md).
