---
title: Callers
description: How ulgen resolves its callers with Kayra, the 401 that starts an MCP client's OAuth login, anonymous listing and TLS.
weight: 45
---

# Callers

Every request carries the caller's Kayra token, in `token_header`
(`Authorization` by default). ulgen resolves it with Kayra
(`/oauth2/userinfo`), and caches the result for `kayra.cache_ttl`
(1 minute by default). `/check` is never cached.

## Logging in from an MCP client

A request without a valid token gets a 401. Its `WWW-Authenticate` names
`resource_metadata` when that is set (RFC 9728): the URL of the protected
resource metadata, where an MCP client finds the authorization server to log
in at.

```text
client ──► ulgen /mcp                          no token
       ◄── 401  WWW-Authenticate: Bearer resource_metadata="https://api.example.com/.well-known/oauth-protected-resource/mcp"
client ──► the metadata                        authorization_servers: [Kayra]
client ──► Kayra: OAuth 2.1 login, PKCE        the human logs in at their provider, consents
client ──► ulgen /mcp  Bearer <token>
```

ulgen does not serve the metadata itself. Serve it next to the MCP endpoint,
from the gateway in front or as a static document, naming Kayra in
`authorization_servers`. Kayra's side is its
[authorization server](https://infrapi.github.io/kayra/architecture/oauth2-server/).

### One token for every API

An infrapi/lib API accepts a Kayra token only when the token's `aud` is the
API, or a URL the API is under. Serve the MCP endpoint and the APIs under
one host, and make that host one of Kayra's `oauth2.resources`. The token
the client gets for the MCP endpoint is then good for every API behind it,
and ulgen can pass it on to a passthrough upstream.

## Anonymous listing

With `anonymous_listing: true`, the MCP handshake and `tools/list`
(`initialize`, `notifications/initialized`, `ping`, `tools/list`) need no
token. This is for a proxy in front whose health check or UI has no user
behind it. Tool calls still need a token.

A passthrough upstream wants a token even to list its tools. It is then
listed with `list_token_file`, a Kayra token of ulgen's own, re-read at each
listing and never used to call a tool. Without that file, it keeps the tools
that a caller's listing found.

Such a proxy may also pass the caller's token in a header of its own:
`token_header`.

## TLS

With `tls: {cert_file, key_file}`, ulgen serves HTTPS (TLS 1.2 or later).
Use it when callers' tokens cross a network. ulgen re-reads the PEM files
when they change, so a renewed certificate is served without a restart. If
a new certificate fails to load, the previous one is still served.

Behind a proxy on the same host, ulgen can listen on the loopback in plain
HTTP. In that case the proxy must send a loopback `Host` (the upstream's
own address), because the MCP SDK refuses a request whose `Host` is not
loopback to a server listening there (DNS rebinding protection).
