---
title: Tools and tenants
description: The MCP endpoints, how upstream tools are discovered and filtered with allowed_tools, and how every tool gets a tenant argument.
weight: 30
---

# Tools and tenants

## Endpoints

| Path | Serves |
|---|---|
| `<path>` (`/mcp`) | every upstream's tools, named `<upstream>_<tool>` |
| `<path>/<upstream>` (`/mcp/grafana`) | one upstream's tools, under their own names |
| `/apis/<api>/<version>/-/openapi.json` | the OpenAPI document Kayra reads to register an api ulgen authorizes calls on ([Authorization](authorization.md)) |
| `/metrics` | Prometheus metrics ([Audit log and metrics](observability.md)) |
| `/healthz` | liveness |

Each MCP endpoint also has a `list_tenants` tool.

The MCP endpoints use Streamable HTTP. ulgen opens its own sessions to the
upstreams with MCP protocol `2025-06-18` by default (`protocol_version`),
because stateful servers such as mcp-grafana refuse newer versions.

## Discovery

ulgen lists each upstream's tools with `tools/list` and keeps the result for
`tools_ttl` (5 minutes by default). After that, the next listing asks the
upstreams again. A tool added upstream shows up without a restart.

## allowed_tools

`allowed_tools` decides which of an upstream's tools are exposed. The first
matching entry wins. A tool that no entry matches is neither listed nor
callable. Without `allowed_tools`, every tool is exposed.

An entry is one of:

- a regular expression, matched against the whole tool name: the
  upstream's own name, without the `<upstream>_` prefix;
- `{match, resource, verb}`, which also names the right the tool's calls
  need, for an upstream with `authorize: ulgen` ([Authorization](authorization.md)).

```yaml
allowed_tools:
  - "search_.*"                                   # exposed, checked on resource "tools"
  - {match: "query_prometheus.*|list_prometheus_.*", resource: prometheus}
  - {match: "get_dashboard.*", resource: dashboards, verb: get}
```

An invalid expression, or a key that does not exist, fails ulgen's start.

## Tenants

Every tool ulgen exposes takes a required `tenant` argument, a Kayra
namespace. Each upstream says how it names its tenant:

- `tenant: {inject: true}`: the upstream has no notion of tenant, and its
  credential sets the tenant (a tenant's Grafana service account). ulgen
  adds `tenant` to every tool and removes it before the call.
- `tenant: {argument: namespace}`: the upstream already takes the tenant as
  an argument of its own, as an infrapi/lib MCP's `namespace` does. ulgen
  exposes it as `tenant` and renames it back for the call. A tool without
  that argument is not tenant-scoped, and stays as it is.

## list_tenants

`list_tenants` answers the caller's namespaces, read from Kayra
(`GET /apis/system.<domain>/v1/namespaces`). Each comes with the upstreams
usable there, decided by Kayra's `/check` on the upstream's `tenant_check`.

The default `tenant_check`, with `authorize: ulgen`:

1. `check`, when set;
2. otherwise, the first rule's resource with `get`;
3. otherwise, `tools`/`get`.

An upstream without a `tenant_check`, one with `authorize: upstream` and
none set, is listed in every namespace.
