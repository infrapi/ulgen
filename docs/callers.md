---
title: Callers
description: How ulgen resolves its callers with Kayra, the 401 that starts an MCP client's OAuth login, and the token's audience.
weight: 40
---

# Callers

Every request to an MCP endpoint carries the caller's bearer token. ulgen
resolves it with Kayra (`/oauth2/userinfo`), through infrapi/lib's
`auth.Client`, and keeps the answer for `INFRAPI_ULGEN_AUTH_CACHE_TTL`
(1 minute by default).

## Logging in from an MCP client

A request without a valid token gets a 401. Its `WWW-Authenticate` names
ulgen's protected resource metadata (RFC 9728), which names Kayra as the
authorization server:

```text
client ──► ulgen <root>/-/mcp                no token
       ◄── 401  WWW-Authenticate: Bearer resource_metadata="https://api.example.com/apis/mcp.infrapi.io/v1/-/oauth-protected-resource"
client ──► the metadata                      authorization_servers: [Kayra]
client ──► Kayra: OAuth 2.1 login, PKCE       the human logs in at their provider, consents
client ──► ulgen <root>/-/mcp  Bearer <token>
```

ulgen serves the metadata itself, at `<root>/-/oauth-protected-resource` and
`/.well-known/oauth-protected-resource<root>`: nothing to set up on the
gateway but the route to ulgen. Kayra's side is its
[authorization server](https://infrapi.github.io/kayra/architecture/auth/oauth2-server/).

## The token's audience

A token of Kayra's authorization server is issued for a resource, its
`aud`. ulgen accepts one whose `aud` is its own URL
(`INFRAPI_APP_URL` and its root) or a URL it is under; so does every
infrapi/lib API. Serve ulgen and the APIs under one host, the gateway, and
make that host one of Kayra's `oauth2.resources`: the token the client gets
for ulgen is then good for every API ulgen calls with it.

## The caller's token, everywhere

ulgen has no credential of its own. It asks Kayra which APIs exist and which
namespaces the caller belongs to with the caller's token, and calls the APIs
with it. A caller sees the APIs Kayra lists to any authenticated caller, and
uses them as far as Kayra's roles let them, API by API.
