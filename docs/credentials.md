---
title: Credentials
description: What identifies ulgen's calls to an upstream, the caller's own token or the tenant's credential read from Vault, and static headers.
weight: 50
---

# Credentials

Each upstream's `credential` says what ulgen sends with a call.

## The caller's token

```yaml
credential: {passthrough: true}
```

The caller's own bearer token, for an upstream that checks the caller
itself (`authorize: upstream`), such as an infrapi/lib MCP. The token's
audience must be good for the upstream: see
[One token for every API](callers.md#one-token-for-every-api).

## The tenant's credential, from Vault

```yaml
credential:
  vault: {mount: secret, path: "grafana/{tenant}", key: token}
  header: X-Grafana-Service-Account-Token
  # prefix: "Bearer "               # prepended to the value
```

This is for an upstream with no notion of tenant, where each tenant has its
own credential: a Grafana service account per tenant organisation, for
instance. ulgen reads the KV v2 secret at `path`, with `{tenant}` replaced
by the call's tenant. It sends the value of `key` in `header`, after
`prefix`. The value is kept for `credential_ttl` (1 minute by default).

A Vault-credential upstream never sees the caller's token. Before each call,
ulgen checks the caller's right on the tenant with Kayra
(`authorize: ulgen`).

ulgen logs in to Vault with an AppRole:

```yaml
vault:
  address: https://vault.example.com:8200
  role_id_env: ULGEN_VAULT_ROLE_ID          # the role id, from this variable
  secret_id_file: /run/secrets/ulgen-vault-secret-id   # re-read at each login
  # approle_mount: approle
  # ca_cert_path: /etc/pki/vault-ca.pem
```

The secret id file is re-read at each login, so a rotated secret id is
picked up without a restart.

## Static headers

`headers_from_env` sends headers on every request to the upstream, valued
from environment variables (header name → variable name). A typical use is
the bearer an upstream expects from its only client:

```yaml
headers_from_env:
  Authorization: MCP_GRAFANA_CALLER_AUTH    # "Bearer <mcp-grafana --server-auth-token>"
```

A variable that is not set fails ulgen's start.
