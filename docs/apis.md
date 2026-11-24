---
title: APIs and tools
description: How ulgen finds the platform's APIs in Kayra, which tools it serves under which names, and how a call reaches its API.
weight: 30
---

# APIs and tools

## Which APIs

On a `tools/list`, at most every `INFRAPI_ULGEN_TOOLS_TTL` (5 minutes by
default), ulgen asks Kayra with the caller's token:

1. `GET /apis`: the API groups Kayra knows. Any authenticated caller may
   read them. Kayra's own groups are left out, and so is ulgen's.
2. For each, the contract Kayra last read from it:
   `GET /apis/catalog.<domain>/v1/apiregistrations/<api>/openapi.json`.
   An API whose contract has `/apis/<api>/<version>/-/mcp`, infrapi/lib's
   `server.MCPPath`, is served. An API Kayra has not read yet is left out
   until the next listing.

`INFRAPI_ULGEN_APIS_INCLUDE` and `INFRAPI_ULGEN_APIS_EXCLUDE`, regular
expressions on the API's whole name, narrow the APIs served.

ulgen then lists each API's tools at
`<INFRAPI_ULGEN_GATEWAY_URL>/apis/<api>/<version>/-/mcp`. An infrapi/lib
API's MCP endpoint answers its tool list without a token; ulgen sends the
caller's anyway, for an API that asks for one. An API that fails to list
keeps the tools it had. Kayra failing keeps every API as it was.

## Which names

| Endpoint | Tools |
|---|---|
| `<root>/-/mcp` | every API's, named `<short_name>_<tool>`: `compute_get_vm` |
| `<root>/-/mcp/<short_name>` | one API's, under their own names: `get_vm` |

`<short_name>` is the API's registration's `short_name`, or the first label
of its name when it has none. Kayra keeps short names unique. Two tools that
would still share a name are logged, and the second is left out.

Each endpoint also has `list_namespaces`: the Kayra namespaces the caller
belongs to.

A tool is served as its API lists it: its description, its input schema,
its annotations (read-only, destructive). An infrapi API's tool takes the
namespace it acts in as its `namespace` argument.

## A call

A call goes to its API's MCP endpoint, as the caller:

- `Authorization: Bearer <the caller's token>`;
- `X-Request-Id`, the request's, and `Traceparent` when the caller sent one;
- the arguments as the agent gave them.

The API checks the call as it checks any of its routes: the token, Kayra's
RBAC on the namespace, the contract. Its refusal, a 403 or a 400, comes back
to the agent as a tool error with the API's problem document. ulgen grants
nothing and refuses nothing: what an agent may do is what its user may do,
API by API.

## MCP servers that are not APIs

An MCP server with no notion of namespace or RBAC, mcp-grafana for one, is
served through an API that bridges it. Its tools are then routes of that
API, resources such as `mcp-prometheus` that Kayra's roles grant per
namespace, each call made with the namespace's own credential: tofu-api's
[`mcp.bridges`](https://infrapi.github.io/tofu-api/mcp/), lib's
[mcp/bridge](https://infrapi.github.io/lib/packages/mcp/). ulgen serves them
as any other tool of that API.
