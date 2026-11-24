---
title: Configuration
description: Every setting of ulgen, infrapi/lib's INFRAPI_APP_* and its own INFRAPI_ULGEN_*, with its default.
weight: 70
---

# Configuration

```bash
INFRAPI_CONFIG_DOTENV_FILE=/etc/ulgen/ulgen.env ulgen
ulgen -version
```

ulgen reads one dotenv file, the one `INFRAPI_CONFIG_DOTENV_FILE` names
(`.env` when unset), never the process environment. A missing or invalid
setting stops it, naming the key and the file. A commented example is
[`ulgen.env.example`](https://github.com/infrapi/ulgen/blob/main/ulgen.env.example).

## infrapi/lib's

`INFRAPI_APP_*`, as for every infrapi/lib service: the name, platform,
region, listen address, logs, CORS... See lib's
[config](https://infrapi.github.io/lib/packages/config/). For ulgen:

| Key | |
|---|---|
| `INFRAPI_APP_URL` | where MCP clients reach ulgen, the gateway: the resource their tokens are for, what a 401 names |
| `INFRAPI_APP_LISTEN_ADDRESS` | `127.0.0.1:9700` behind a gateway on the same host |

## ulgen's

| Key | Default | |
|---|---|---|
| `INFRAPI_ULGEN_API` | required | ulgen's own API group: its endpoints are under `/apis/<api>/v1`, the MCP endpoint `/apis/<api>/v1/-/mcp`. Never served as one of the APIs |
| `INFRAPI_ULGEN_AUTH_URL` | required | Kayra's URL: the callers, the APIs, the namespaces |
| `INFRAPI_ULGEN_AUTH_CACHE_TTL` | `1m` | how long a caller's identity is kept |
| `INFRAPI_ULGEN_GATEWAY_URL` | `INFRAPI_ULGEN_AUTH_URL` | where the APIs are reached: `<gateway>/apis/<api>/<version>/-/mcp` |
| `INFRAPI_ULGEN_APIS_INCLUDE` | every API | a regular expression on an API's whole name: the APIs served |
| `INFRAPI_ULGEN_APIS_EXCLUDE` | none | a regular expression on an API's whole name: the APIs left out |
| `INFRAPI_ULGEN_TOOLS_TTL` | `5m` | how long the APIs and their tools are kept before they are listed again |
