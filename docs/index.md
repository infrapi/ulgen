---
title: ulgen
description: One MCP endpoint in front of several MCP servers, every tool scoped to a tenant and every call authorized by Kayra.
weight: 10
---

# ulgen

ulgen is one MCP server in front of several, for multi-tenant use. Every
tool it exposes takes a `tenant` (a [Kayra](https://github.com/infrapi/kayra)
namespace). Before each call, ulgen makes sure the caller may act there. It
then calls the upstream with the right credential: the caller's own token,
or the tenant's credential, read from Vault.

```text
MCP client ──Bearer <caller's Kayra token>──► ulgen ─┬─► mcp-grafana   (tenant credential from Vault)
                                                      └─► an infrapi MCP (caller's token)
```

An AI agent connects once, logs in at Kayra once, and gets a single set of
tools:

- **One endpoint**: `/mcp` serves every upstream's tools, named
  `<upstream>_<tool>`. `/mcp/<upstream>` serves one upstream's tools under
  their own names.
- **Tenants**: an upstream with no notion of tenant, mcp-grafana for one,
  gets a `tenant` argument, and the tenant's own credential behind it. An
  infrapi MCP's `namespace` argument becomes `tenant`. `list_tenants`
  answers the caller's namespaces and the upstreams usable in each.
- **Rights in Kayra**: ulgen asks Kayra's `/check` before each call, on a
  resource per tool family (`prometheus`, `loki`, `dashboards`...). It
  serves the OpenAPI document Kayra registers the api from. Namespace roles
  then grant or withhold a family, as for any other api. An upstream built
  on infrapi/lib checks its calls itself, with the caller's token.
- **Only the tools you list**: `allowed_tools` regular expressions decide
  which upstream tools are listed and callable.
- **Traceable**: one audit line per call, with its request id, which ulgen
  passes on to the upstream. Prometheus metrics at `/metrics`.

## Where to start

| I want to... | Go to |
|---|---|
| Run ulgen and connect an MCP client | [Getting started](getting-started.md) |
| Choose which tools are exposed, and how tenants are named | [Tools and tenants](tools.md) |
| Grant or withhold tools per tenant in Kayra | [Authorization](authorization.md) |
| Understand how callers authenticate, and the 401 that starts an MCP client's login | [Callers](callers.md) |
| Give an upstream its credentials | [Credentials](credentials.md) |
| Follow a call through the logs, and scrape the metrics | [Audit log and metrics](observability.md) |
| Look up a configuration key | [Configuration](configuration.md) |
