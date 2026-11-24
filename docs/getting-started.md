---
title: Getting started
description: Install ulgen, point it at Kayra, route it through the gateway, and connect an MCP client to every API at once.
weight: 20
---

# Getting started

This page runs ulgen in front of the platform's APIs, then connects Claude
Code to it.

You need:

- a [Kayra](https://infrapi.github.io/kayra/) with its OAuth authorization
  server enabled, for MCP clients to log in
  ([Authorization server](https://infrapi.github.io/kayra/architecture/auth/oauth2-server/));
- at least one API built on infrapi/lib that serves MCP, registered in Kayra:
  the `compute` API of [Develop an API](https://infrapi.github.io/cli/develop/develop-an-api/),
  or a tofu-api;
- a gateway in front of them (APISIX, say), where ulgen will be routed too.

`infrapi-cli`, to check what Kayra knows:
[install it](https://infrapi.github.io/cli/getting-started/#1-install).

## 1. Install

Linux archives (amd64, arm64) are on the
[releases page](https://github.com/infrapi/ulgen/releases), with
`checksums.txt`:

```bash
v=$(curl -fsSL https://api.github.com/repos/infrapi/ulgen/releases/latest | jq -r .tag_name | tr -d v)
curl -fsSLO "https://github.com/infrapi/ulgen/releases/download/v$v/ulgen_${v}_linux_amd64.tar.gz"
tar -xzf "ulgen_${v}_linux_amd64.tar.gz" ulgen ulgen.env.example
./ulgen -version
```

Or build a container image from the repository's `Dockerfile`.

## 2. Configure

ulgen reads a dotenv file, `INFRAPI_CONFIG_DOTENV_FILE`, as every infrapi/lib
service does. From [`ulgen.env.example`](https://github.com/infrapi/ulgen/blob/main/ulgen.env.example):

```bash
INFRAPI_APP_NAME="ulgen"
INFRAPI_APP_PLATFORM="sandbox"
INFRAPI_APP_REGION="eu-west-1"
INFRAPI_APP_LOCATION="dc1"
INFRAPI_APP_FQDN="ulgen.example.com"
INFRAPI_APP_URL="https://api.example.com"      # the gateway, as MCP clients reach it
INFRAPI_APP_LISTEN_ADDRESS="127.0.0.1:9700"

INFRAPI_ULGEN_API="mcp.infrapi.io"             # ulgen's endpoints: /apis/mcp.infrapi.io/v1
INFRAPI_ULGEN_AUTH_URL="https://api.example.com"   # Kayra
```

Every setting is in [Configuration](configuration.md).

## 3. Run

```bash
INFRAPI_CONFIG_DOTENV_FILE=ulgen.env ./ulgen
curl -s localhost:9700/apis/mcp.infrapi.io/v1/-/healthz
# {"status":"ok"}
```

## 4. Route it

On the gateway, route `/apis/mcp.infrapi.io/*` to ulgen, as any API.
ulgen serves under that root:

| Path | |
|---|---|
| `/apis/mcp.infrapi.io/v1/-/mcp` | the MCP endpoint, every API's tools |
| `/apis/mcp.infrapi.io/v1/-/mcp/<short_name>` | one API's tools |
| `/apis/mcp.infrapi.io/v1/-/oauth-protected-resource` | the protected resource metadata a 401 names |

ulgen is not registered in Kayra: it owns no resource, and asks Kayra
everything with the caller's token.

## 5. Check what it serves

ulgen serves the APIs Kayra lists whose contract, as Kayra last read it,
has an MCP endpoint. To see them, as ulgen does:

**infrapi-cli**

```bash
infrapi-cli apis
infrapi-cli get --raw /apis/catalog.infrapi.io/v1/apiregistrations/compute.infrapi.io/openapi.json | jq '.paths | keys[] | select(endswith("/-/mcp"))'
```

**curl**

```bash
curl -s https://api.example.com/apis -H "Authorization: Bearer $TOKEN" | jq '.[] | select(.builtin | not) | .api'
curl -s https://api.example.com/apis/catalog.infrapi.io/v1/apiregistrations/compute.infrapi.io/openapi.json \
  -H "Authorization: Bearer $TOKEN" | jq '.paths | keys[] | select(endswith("/-/mcp"))'
# "/apis/compute.infrapi.io/v1/-/mcp"
```

## 6. Connect an MCP client

```bash
claude mcp add --transport http infrapi https://api.example.com/apis/mcp.infrapi.io/v1/-/mcp
```

The client's first request gets a 401 that names ulgen's protected resource
metadata. The client logs in at Kayra (login and consent in the browser) and
retries with its token. Ask it to `list_namespaces`, then to read a VM of
one of them: `compute_get_vm`.

Each call is one `tool call` line in ulgen's log: who called which tool of
which API, in which namespace, and how it ended. See
[Audit log and metrics](observability.md).
