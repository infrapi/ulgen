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
`aud`: the one the protected resource metadata names, which the client asks
for. ulgen passes the caller's token on to the APIs, and an infrapi/lib API
accepts it only when its `aud` is the API's URL or a URL the API is under.
A token for ulgen's own URL (`<gateway>/apis/mcp.infrapi.io/v1`) would be
refused by every API.

So name the gateway's URL, `INFRAPI_ULGEN_RESOURCE`: the metadata then says
`"resource": "https://api.example.com"`, the client gets a token for the
whole gateway, and ulgen, under it, accepts it as every API behind it does.
Kayra issues it if that URL is one of its `oauth2.resources`. The APIs must
know their own URL as the gateway's too: their `INFRAPI_APP_URL` is the
gateway, not their node's address.

## The caller's token, everywhere

ulgen has no credential of its own. It asks Kayra which APIs exist and which
namespaces the caller belongs to with the caller's token, and calls the APIs
with it. A caller sees the APIs Kayra lists to any authenticated caller, and
uses them as far as Kayra's roles let them, API by API.
