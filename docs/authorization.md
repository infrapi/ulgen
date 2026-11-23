---
title: Authorization
description: Who decides whether a caller may call a tool on a namespace, ulgen asking Kayra or the upstream itself, and the Kayra api and roles behind it.
weight: 40
---

# Authorization

Each upstream says who decides whether a caller may call one of its tools
on a namespace: `authorize: ulgen` or `authorize: upstream`.

## authorize: ulgen

Before each call, ulgen asks Kayra's `POST /apis/authorization.<domain>/v1/check`
with the caller's token:

```json
{"namespace": "<namespace>", "api": "<upstream api>", "resource": "...", "verb": "..."}
```

- `resource` comes from the `allowed_tools` rule that matches the tool.
  Without a resource in the rule, it is `tools`.
- `verb` comes from the rule. Without a verb in the rule, it comes from the
  tool's MCP annotations: `get` for a read-only tool, `create` for another
  write, `delete` for a destructive tool or a tool without annotations.

Kayra answers. A refusal is an error result naming the missing right, and
the upstream is never called. `/check` is never cached: a role taken away
applies on the next call.

### The api's OpenAPI document

Kayra registers an api from the OpenAPI document the api serves. ulgen
serves one for each api its `authorize: ulgen` upstreams name, at
`/apis/<api>/<version>/-/openapi.json` (`version` is `v1` by default). The
document declares each rule's resource with the verbs it can be checked
on. Upstreams that name the same api share its document.

Route that path to ulgen on the gateway Kayra's reconciler reads, then
register the api and subscribe the namespaces. An api with nothing to deploy
per namespace takes a mode without steps:

```bash
infrapi-cli create apiregistrations -d '{"name": "mcp.infrapi.io", "active": true, "owner": "sre", "mode": "none"}'
infrapi-cli create subscriptions -n acme -d '{"api": "mcp.infrapi.io"}'
```

### Roles per tool family

With one resource per tool family, a namespace role grants a family like
any other api's resource. For instance, Prometheus but not Loki:

```yaml
# ulgen.yaml
allowed_tools:
  - {match: "query_prometheus.*|list_prometheus_.*", resource: prometheus}
  - {match: "query_loki.*|list_loki_.*", resource: loki}
```

```yaml
# a Kayra role in the namespace's namespace
rules:
  - apis: [mcp.infrapi.io]
    resources: [prometheus]
    verbs: [get]
```

A role's `resources` are the bare names the document declares
(`prometheus`), not `prometheus.mcp.infrapi.io`. To see what a caller may
do: `infrapi-cli auth can-i get prometheus.mcp.infrapi.io -n acme`.

### Borrowing an existing api's right

`check: {resource, verb}` makes every call of a tool that no rule names a
resource for ask that one right instead. This is for an upstream whose
rights are an existing api's, such as Grafana read through a telemetry api:

```yaml
api: telemetry.infrapi.io
authorize: ulgen
check: {resource: dashboards, verb: list}
```

One right cannot tell reads from writes, so only the upstream's read-only
tools are exposed that way. ulgen serves no document for that api, because
the api serves its own. There is nothing to register.

## authorize: upstream

The upstream checks each call itself, with the caller's own token
(`credential: {passthrough: true}`), as every infrapi/lib app does. ulgen
only resolves the caller and the namespace. An upstream that checks its own
calls needs no api of ulgen's, and no `allowed_tools` resource.

`namespace_check` still tells `list_namespaces` where the upstream is usable
([Tools and namespaces](tools.md#list_namespaces)).
