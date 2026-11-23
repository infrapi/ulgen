# ulgen

One MCP server in front of several, for multi-tenant use: every tool takes a
`tenant` (a [Kayra](https://github.com/infrapi/kayra) namespace), and ulgen
makes sure the caller may act there before calling the upstream with the
right credential.

```
MCP client ──Bearer <caller's Kayra token>──► ulgen ─┬─► mcp-grafana   (tenant credential from Vault)
                                                      └─► telemetry MCP (caller's token)
```

Endpoints:

- `<path>` (`/mcp`): every upstream, tools named `<upstream>_<tool>`;
- `<path>/<upstream>` (`/mcp/grafana`): one upstream, its tools under their own names;
- `/apis/<api>/<version>/-/openapi.json`: the OpenAPI document Kayra reads to register an api ulgen
  authorizes calls on (below);
- `/healthz`.

Each MCP endpoint also has `list_tenants`.

## Tools

Tools are discovered from each upstream (`tools/list`, refreshed every `tools_ttl`) and exposed with a
required `tenant` argument: added by ulgen (`tenant.inject`), or the upstream's own one renamed
(`tenant.argument`, e.g. an infrapi/lib MCP's `namespace`).

`allowed_tools` decides which of an upstream's tools are exposed, first match wins. An entry is a regular
expression matching the whole tool name (the upstream's own, not prefixed), or `{match, resource, verb}`,
which also names the right the tool's calls need (below). A tool no entry matches is neither listed nor
callable; without `allowed_tools`, every tool is exposed.

## Authorization per tenant

Each upstream is one of:

- `authorize: ulgen`: before each call, ulgen asks Kayra `POST .../check` with the caller's token,
  `{namespace: <tenant>, api: <upstream api>, resource, verb}`:
  - `resource` is the matching `allowed_tools` rule's, else `tools`;
  - `verb` is the rule's, else the tool's MCP annotations': `get` for a read-only tool, `delete` for a
    destructive one (or one without annotations), `create` for any other write.

  ulgen serves the api's OpenAPI document, declaring those resources with these verbs, for Kayra to
  register the api (upstreams sharing an api share it). A namespace role then grants them like any other
  api's, per tool family when the rules name one, e.g. Prometheus and not Loki:

  ```yaml
  allowed_tools:
    - {match: "query_prometheus.*|list_prometheus_.*", resource: prometheus}
    - {match: "query_loki.*|list_loki_.*", resource: loki}
  # a role: apis: [mcp.example.io], resources: [prometheus], verbs: [get]
  ```

  With `check: {resource, verb}`, every call of a tool no rule names a resource of asks that one right
  instead: for an upstream whose rights are an existing api's (Grafana read through a telemetry api:
  `api: telemetry.example.io`, `check: {resource: dashboards, verb: list}`). One right can't tell reads
  from writes, so only read-only tools are exposed that way, and ulgen serves no document for that api.
- `authorize: upstream`: the upstream checks itself with the caller's token, as every infrapi/lib app
  does.

`list_tenants` answers the caller's namespaces (Kayra `GET /apis/system.<domain>/v1/namespaces`), each with
the upstreams usable there: Kayra `/check` on the upstream's `tenant_check`. Its default, with
`authorize: ulgen`: `check`, else the first rule's resource with `get`, else `tools`/`get`. An upstream
without a `tenant_check` is listed in every namespace.

## Credentials

Per call: the caller's token (`credential.passthrough`), or the tenant's own credential read from Vault
(`credential.vault`, AppRole login, path with `{tenant}`, sent in `credential.header`). A Vault-credential
upstream never sees the caller's token.

An infrapi/lib API accepts a Kayra token only when its `aud` is the API or a URL the API is under. Serve
the MCP endpoint and the APIs under one host and make that host Kayra's `oauth2.resources`: the token the
client gets for the MCP endpoint is then good for every API behind it, and ulgen can pass it on.

## Callers

Every request takes the caller's token, in `token_header` (`Authorization` by default), resolved by Kayra
(`/oauth2/userinfo`, cached `kayra.cache_ttl`); `/check` is never cached. Without one, ulgen answers 401,
its `WWW-Authenticate` naming `resource_metadata` when set (RFC 9728): the URL of the protected resource
metadata, where an MCP client finds the authorization server to log in at.

With `anonymous_listing: true`, the MCP handshake and `tools/list` (`initialize`,
`notifications/initialized`, `ping`, `tools/list`) need no token: for a proxy in front whose health check
or UI has no user behind it. A passthrough upstream is then listed with `list_token_file` (a Kayra token of
ulgen's own, never used to call a tool), or keeps the tools a caller's listing found. Such a proxy may also
pass the token in a header of its own: `token_header`.

## Audit log

One `tool call` line per call, in the JSON log on stderr: the caller (`user`), `upstream`, `tool`,
`tenant`, `outcome` (`ok`; `refused` by ulgen or Kayra; `tool_error`, an error result of the upstream's;
`error`, the upstream or Kayra unreachable) and `duration_ms`.

## Run

```bash
go run ./cmd/ulgen -config ulgen.yaml     # see ulgen.example.yaml
go run ./cmd/ulgen -version
go test -race ./...
```

With `tls: {cert_file, key_file}`, ulgen serves HTTPS (TLS 1.2 at least), for callers' tokens crossing a
network. The PEM files are re-read when they change, so a renewed certificate is served without a restart;
one that fails to load keeps the previous one served.

Upstream sessions are opened with MCP protocol `2025-06-18` by default (`protocol_version`): stateful
servers such as mcp-grafana refuse newer ones.

Releases: on `main`, a `feat` or `fix` commit makes go-semantic-release tag the next version, and
GoReleaser publishes the Linux archives and `checksums.txt`.
