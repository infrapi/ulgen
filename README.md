# ulgen

One MCP server in front of several, for multi-tenant use: every tool takes a
`namespace` (a [Kayra](https://github.com/infrapi/kayra) namespace), and ulgen
makes sure the caller may act there before calling the upstream with the
right credential.

Documentation: [infrapi.github.io/products/ulgen](https://infrapi.github.io/products/ulgen/docs/), from [`docs/`](docs/index.md).

```
MCP client ──Bearer <caller's Kayra token>──► ulgen ─┬─► mcp-grafana   (namespace credential from Vault)
                                                      └─► telemetry MCP (caller's token)
```

Endpoints:

- `<path>` (`/mcp`): every upstream, tools named `<upstream>_<tool>`;
- `<path>/<upstream>` (`/mcp/grafana`): one upstream, its tools under their own names;
- `/apis/<api>/<version>/-/openapi.json`: the OpenAPI document Kayra reads to register an api ulgen
  authorizes calls on (below);
- `/metrics`: Prometheus metrics (below);
- `/healthz`.

Each MCP endpoint also has `list_namespaces`.

## Tools

Tools are discovered from each upstream (`tools/list`, refreshed every `tools_ttl`) and exposed with a
required `namespace` argument: added by ulgen (`namespace.inject`), or the upstream's own one renamed
(`namespace.argument`, e.g. an infrapi/lib MCP's `namespace`).

`allowed_tools` decides which of an upstream's tools are exposed, first match wins. An entry is a regular
expression matching the whole tool name (the upstream's own, not prefixed), or `{match, resource, verb}`,
which also names the right the tool's calls need (below). A tool no entry matches is neither listed nor
callable; without `allowed_tools`, every tool is exposed.

## Authorization per namespace

Each upstream is one of:

- `authorize: ulgen`: before each call, ulgen asks Kayra `POST .../check` with the caller's token,
  `{namespace: <namespace>, api: <upstream api>, resource, verb}`:
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
  # a role: apis: [mcp.infrapi.io], resources: [prometheus], verbs: [get]
  ```

  With `check: {resource, verb}`, every call of a tool no rule names a resource of asks that one right
  instead: for an upstream whose rights are an existing api's (Grafana read through a telemetry api:
  `api: telemetry.infrapi.io`, `check: {resource: dashboards, verb: list}`). One right can't tell reads
  from writes, so only read-only tools are exposed that way, and ulgen serves no document for that api.
- `authorize: upstream`: the upstream checks itself with the caller's token, as every infrapi/lib app
  does.

`list_namespaces` answers the caller's namespaces (Kayra `GET /apis/system.<domain>/v1/namespaces`), each with
the upstreams usable there: Kayra `/check` on the upstream's `namespace_check`. Its default, with
`authorize: ulgen`: `check`, else the first rule's resource with `get`, else `tools`/`get`. An upstream
without a `namespace_check` is listed in every namespace.

## Credentials

Per call: the caller's token (`credential.passthrough`), or the namespace's own credential read from Vault
(`credential.vault`, AppRole login, path with `{namespace}`, sent in `credential.header`). A Vault-credential
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

## Audit log and metrics

One `tool call` line per call, in the JSON log on stderr: the caller (`user`), `upstream`, `tool`,
`namespace`, `outcome` (`ok`; `refused` by ulgen or Kayra; `tool_error`, an error result of the upstream's;
`error`, the upstream or Kayra unreachable), `duration_ms` and `request_id`.

`request_id` is the request's `X-Request-Id`, as a proxy in front sets it; ulgen passes it on to the
upstream it calls, so one request can be followed across the components' logs.

`/metrics`, on the same listener (to scrape it from the node: ulgen usually listens on the loopback,
behind a proxy that routes no `/metrics`):

| Metric | Labels |
|---|---|
| `ulgen_tool_calls_total` | `upstream`, `tool`, `outcome` (as the audit log) |
| `ulgen_tool_call_duration_seconds` | `upstream`, `outcome` |
| `ulgen_kayra_request_duration_seconds` | `op` (`verify`, `check`, `namespaces`), `result` (`ok`, `denied`, `refused`, `error`) |
| `ulgen_credential_reads_total` | `upstream`, `result` (`ok`, `error`) |
| `ulgen_unauthenticated_requests_total` | `reason` (`no_token`, `refused_token`) |
| `ulgen_tools_exposed` | `upstream`: the tools exposed, as last listed |
| `ulgen_tools_list_errors_total` | `upstream` |
| `ulgen_build_info` | `version` |

and the Go runtime's and the process's.

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
