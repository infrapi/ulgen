---
title: Configuration
description: Every key of ulgen's YAML configuration file, with its default.
weight: 70
---

# Configuration

```bash
ulgen -config /etc/ulgen/ulgen.yaml
ulgen -version
```

ulgen reads one YAML file. A key that does not exist is an error, as is a
missing required value: ulgen refuses to start and names it. A commented
example is in
[`ulgen.example.yaml`](https://github.com/infrapi/ulgen/blob/main/ulgen.example.yaml).

## Server

| Key | Default | |
|---|---|---|
| `listen` | `:8080` | the listen address |
| `path` | `/mcp` | the MCP endpoint; `<path>/<upstream>` serves one upstream |
| `tls.cert_file`, `tls.key_file` | | HTTPS, with PEM files re-read when they change ([Callers](callers.md#tls)) |
| `token_header` | `Authorization` | the header carrying the caller's Kayra token |
| `resource_metadata` | | the protected resource metadata URL a 401 names (RFC 9728) |
| `anonymous_listing` | `false` | the handshake and `tools/list` without a token ([Callers](callers.md#anonymous-listing)) |
| `list_token_file` | | a Kayra token of ulgen's own, to list passthrough upstreams without a caller |
| `tools_ttl` | `5m` | how long the upstreams' tools are kept before they are listed again |
| `credential_ttl` | `1m` | how long a credential read from Vault is kept |

## kayra

| Key | Default | |
|---|---|---|
| `kayra.url` | required | Kayra's base URL: userinfo, `/check`, the caller's namespaces |
| `kayra.cache_ttl` | `1m` | how long a caller's resolution is cached; `/check` never is |

## vault

Required when an upstream reads its credential from Vault.

| Key | Default | |
|---|---|---|
| `vault.address` | required | |
| `vault.role_id_env` | required | the environment variable holding the AppRole role id |
| `vault.secret_id_file` | required | the file holding the secret id, re-read at each login |
| `vault.approle_mount` | `approle` | |
| `vault.ca_cert_path` | | a CA bundle for Vault's certificate |

## upstreams

| Key | Default | |
|---|---|---|
| `name` | required | lowercase letters, digits and dashes; prefixes its tools (`<name>_<tool>`) |
| `url` | required | its Streamable HTTP endpoint |
| `api` | | the Kayra api its calls are checked on (`authorize: ulgen`), or the api it is registered as (`list_tenants`) |
| `version` | `v1` | the api's version, for its OpenAPI document |
| `authorize` | required | `ulgen` or `upstream` ([Authorization](authorization.md)) |
| `check` | | `{resource, verb}`: the one right every call needs, borrowed from an existing api |
| `tenant_check` | see [list_tenants](tools.md#list_tenants) | `{resource, verb}`: where `list_tenants` says the upstream is usable |
| `credential.passthrough` | | the caller's token ([Credentials](credentials.md)) |
| `credential.vault` | | `{mount, path, key}`, `{tenant}` in `path`: the tenant's credential |
| `credential.header`, `credential.prefix` | | where the Vault credential is sent, and what precedes it |
| `tenant.inject` | | add a `tenant` argument to every tool ([Tools and tenants](tools.md#tenants)) |
| `tenant.argument` | | the upstream's own tenant argument, exposed as `tenant` |
| `allowed_tools` | every tool | regular expressions, or `{match, resource, verb}` rules ([Tools and tenants](tools.md#allowed_tools)) |
| `headers_from_env` | | headers sent on every request, from environment variables |
| `protocol_version` | `2025-06-18` | the MCP protocol version of ulgen's session with it |
