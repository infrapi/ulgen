---
title: Ülgen
description: One MCP endpoint in front of the platform's APIs, found in Kayra's catalog, every call made as the caller.
weight: 10
---

# Ülgen

Ülgen is one MCP endpoint in front of the platform's APIs. Every API built
on [infrapi/lib](https://infrapi.github.io/lib/) serves its own MCP endpoint,
`<root>/-/mcp`. Ülgen finds these APIs in Kayra's catalog and serves all
their tools behind a single endpoint. An AI agent connects once, logs in at
Kayra once, and reaches every API.

```text
MCP client ──Bearer <caller's token>──► ulgen ──► compute.infrapi.io/v1/-/mcp
                                          │   ──► grafana.infrapi.io/v1/-/mcp
                                          │        (mcp-grafana bridged by tofu-api)
                                          └─ Kayra: who calls, which APIs, which namespaces
```

Ülgen decides nothing about rights. Each call goes to its API with the
caller's own token, and the API checks it as it checks any of its routes:
the token, Kayra's RBAC, the contract. Ülgen adds:

- **One endpoint**: `<root>/-/mcp` serves every API's tools, named
  `<short_name>_<tool>` (`compute_get_vm`). `<root>/-/mcp/<short_name>`
  serves one API's tools under their own names.
- **Discovery**: the APIs come from Kayra's catalog, with the caller's token.
  An API whose contract has an MCP endpoint is served. A new API shows up
  without a restart.
- **`list_namespaces`**: the Kayra namespaces the caller belongs to, the
  `namespace` argument of the APIs' tools.
- **The login**: a request without a valid token gets a 401 that names
  Ülgen's protected resource metadata (RFC 9728). An MCP client logs in at
  Kayra from there.
- **Traceable**: one audit line per call, the request id passed on to the
  API, Prometheus metrics.

Ülgen is an infrapi/lib service like the others: the same settings
(`INFRAPI_APP_*`, then its own `INFRAPI_ULGEN_*`), the same operational
endpoints (`-/healthz`, `-/metrics`...), the same errors.

An MCP server that is not an infrapi API, mcp-grafana for one, is served
through an API that bridges it: tofu-api's
[`mcp.bridges`](https://infrapi.github.io/tofu-api/mcp/), or lib's
[mcp/bridge](https://infrapi.github.io/lib/packages/mcp/). Its tools are then
resources of that API, granted per namespace by Kayra's roles.

## Where to start

| I want to... | Go to |
|---|---|
| Run Ülgen and connect an MCP client | [Getting started](getting-started.md) |
| Know which APIs and tools Ülgen serves | [APIs and tools](apis.md) |
| Understand how callers authenticate, and the 401 that starts an MCP client's login | [Callers](callers.md) |
| Follow a call through the logs, and scrape the metrics | [Audit log and metrics](observability.md) |
| Look up a setting | [Configuration](configuration.md) |
