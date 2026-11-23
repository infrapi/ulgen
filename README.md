# ulgen

One MCP server in front of several, for multi-tenant use: every tool takes a
`tenant` (a [Kayra](https://github.com/infrapi/kayra) namespace), and ulgen
makes sure the caller may act there before calling the upstream with the
right credential.

```
MCP client ──► APISIX (OAuth metadata) ──Bearer <caller's Kayra token>──► ulgen ─┬─► mcp-grafana   (tenant credential from Vault)
                                                                                  └─► telemetry MCP (caller's token)
```

Endpoints: `<path>` (`/mcp`) serves every upstream, tools named `<upstream>_<tool>`; `<path>/<upstream>`
(`/mcp/grafana`) serves one upstream, its tools under their own names, each with `list_tenants`.

Nothing is configured per tool:

- **Tools are discovered** from each upstream (`tools/list`, refreshed every
  `tools_ttl`) and exposed as `<upstream>_<tool>`, with a required `tenant`
  argument: added by ulgen (`tenant.inject`), or the upstream's own one
  renamed (`tenant.argument`, e.g. an infrapi/lib MCP's `namespace`).
- **Authorization per tenant**, one of:
  - `authorize: ulgen`: ulgen asks Kayra `POST .../check` with the caller's
    token: `{namespace: <tenant>, api: <upstream api>, resource: "tools",
    verb}`. The verb comes from the tool's MCP annotations: `get` for a
    read-only tool, `delete` for a destructive one (or one without
    annotations), `create` for any other write. ulgen serves the matching
    OpenAPI document at `/apis/<api>/<version>/-/openapi.json` for Kayra to
    register the api: one resource, `tools`. A namespace role then reads
    `apis: [grafana.example.io], resources: [tools], verbs: [get]`.
    With `check: {resource, verb}`, every call asks that one right instead,
    on `api`: for an upstream with no api of its own in Kayra, whose rights
    are an existing api's. Grafana read through a telemetry api, say:
    `api: telemetry.example.io`, `check: {resource: dashboards, verb: list}`,
    whoever may list the tenant's dashboards may query its Grafana. One right
    can't tell reads from writes, so only read-only tools are exposed, and
    ulgen serves no OpenAPI document: nothing to register, subscribe or route.
    Finer, `allowed_tools` rules with a resource: `{match: "query_prometheus.*",
    resource: prometheus}` checks the calls of the tools it matches on
    `prometheus` of `api` (verb from the annotations, or the rule's `verb`).
    The OpenAPI document then declares those resources (and `tools` for a
    tool no rule names one of; upstreams sharing an api share it), so a role
    can grant Prometheus and not Loki:
    `apis: [mcp.example.io], resources: [prometheus], verbs: [get]`.
  - `authorize: upstream`: the upstream checks itself with the caller's
    token, as every infrapi/lib app does.
- **Credential per call**: the caller's token (`passthrough`), or the
  tenant's own credential read from Vault (`vault`, path with `{tenant}`,
  sent in `header`). A Vault-credential upstream never sees the caller's
  token.
- **`list_tenants`**: the caller's namespaces (Kayra `GET
  /apis/system.<domain>/v1/namespaces`), each with the upstreams usable there:
  Kayra `/check` on the upstream's `tenant_check` (`tools`/`get` by default
  with `authorize: ulgen`, or its `check`), every one when it has none.
- **Passing the caller's token on**: an infrapi/lib API accepts a Kayra token
  only when its `aud` is the API or a URL the API is under. Publish the MCP
  gateway and the APIs under one host and make that host Kayra's
  `oauth2.resources`: the token the client gets for the gateway is then good
  for every API behind it.
- **Which tools are exposed**: `allowed_tools`, per upstream, first match
  wins: a regular expression matching the whole tool name (the upstream's
  own), or `{match, resource, verb}` (above). Other tools are neither listed
  nor callable. None: every tool.
- **Audit log**: one `tool call` line per call, with the caller (`user`),
  `upstream`, `tool`, `tenant`, `outcome` (`ok`, `refused` by ulgen or Kayra,
  `tool_error` from the upstream, `error`) and `duration_ms`.

Every request takes the caller's token, in `token_header` (`Authorization` by default), resolved by Kayra
(`/oauth2/userinfo`, cached `kayra.cache_ttl`); `/check` is never cached. Without one, ulgen answers 401, its
`WWW-Authenticate` naming `resource_metadata` (RFC 9728) when set: an MCP client finds there the
authorization server to log in at (APISIX serves that document).

With `anonymous_listing: true`, the MCP handshake and `tools/list` (`initialize`, `notifications/initialized`,
`ping`, `tools/list`) need no token: for a gateway in front whose health check and UI have no user behind them
(LiteLLM). A passthrough upstream is then listed with `list_token_file` (a Kayra token of ulgen's own, never
used to call a tool), or keeps the tools a caller's listing found. Behind such a gateway, `token_header` can be
a dedicated header (`X-Kayra-Token`, LiteLLM's servers forwarding it with `extra_headers`).

## Run

```bash
go run ./cmd/ulgen -config ulgen.yaml     # see ulgen.example.yaml
go test -race ./...
```

With `tls: {cert_file, key_file}`, ulgen serves HTTPS (TLS 1.2 at least): the
callers' tokens cross the network from the gateway in front. The PEM files are
re-read when they change, so a renewed certificate is served without a
restart; one that fails to load keeps the previous one served.

Upstream sessions are opened with MCP protocol `2025-06-18` by default
(`protocol_version`): stateful servers such as mcp-grafana refuse newer ones.
