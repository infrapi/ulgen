# thanos

One MCP server in front of several, for multi-tenant use: every tool takes a
`tenant` (a [Kayra](https://github.com/infrapi/kayra) namespace), and thanos
makes sure the caller may act there before calling the upstream with the
right credential.

```
MCP client ──► (gateway: LiteLLM, ...) ──Bearer <caller's Kayra token>──► thanos ─┬─► mcp-grafana   (tenant credential from Vault)
                                                                                  └─► telemetry MCP (caller's token)
```

Endpoints: `<path>` (`/mcp`) serves every upstream, tools named `<upstream>_<tool>`; `<path>/<upstream>`
(`/mcp/grafana`) serves one upstream, its tools under their own names, each with `list_tenants`. A gateway in
front (LiteLLM) then declares one MCP server per service, each with its own allowed tools, access groups and
limits, all of them thanos.

Nothing is configured per tool:

- **Tools are discovered** from each upstream (`tools/list`, refreshed every
  `tools_ttl`) and exposed as `<upstream>_<tool>`, with a required `tenant`
  argument: added by thanos (`tenant.inject`), or the upstream's own one
  renamed (`tenant.argument`, e.g. an infrapi/lib MCP's `namespace`).
- **Authorization per tenant**, one of:
  - `authorize: thanos`: thanos asks Kayra `POST .../check` with the caller's
    token: `{namespace: <tenant>, api: <upstream api>, resource: "tools",
    verb}`. The verb comes from the tool's MCP annotations: `get` for a
    read-only tool, `delete` for a destructive one (or one without
    annotations), `create` for any other write. thanos serves the matching
    OpenAPI document at `/apis/<api>/<version>/-/openapi.json` for Kayra to
    register the api: one resource, `tools`. A namespace role then reads
    `apis: [grafana.example.io], resources: [tools], verbs: [get]`.
  - `authorize: upstream`: the upstream checks itself with the caller's
    token, as every infrapi/lib app does.
- **Credential per call**: the caller's token (`passthrough`), or the
  tenant's own credential read from Vault (`vault`, path with `{tenant}`,
  sent in `header`). A Vault-credential upstream never sees the caller's
  token.
- **`list_tenants`**: the caller's namespaces (Kayra `GET
  /apis/system.<domain>/v1/namespaces`), each with the upstreams usable there:
  Kayra `/check` on the upstream's `tenant_check` (`tools`/`get` by default
  with `authorize: thanos`), every one when it has none.
- **Passing the caller's token on**: an infrapi/lib API accepts a Kayra token
  only when its `aud` is the API or a URL the API is under. Publish the MCP
  gateway and the APIs under one host and make that host Kayra's
  `oauth2.resources`: the token the client gets for the gateway is then good
  for every API behind it.
- **Which tools a person sees** is not thanos' business: the gateway in
  front decides (LiteLLM's allowed tools).

Every request must carry a token Kayra resolves, in `token_header` (`Authorization` by default; behind
LiteLLM, a dedicated `X-Kayra-Token` that its MCP servers forward with `extra_headers`: LiteLLM withholds the
client's `Authorization` from client-forwarded servers listed together on `/mcp`, RFC 9700), resolved by Kayra (`/oauth2/userinfo`,
cached `kayra.cache_ttl`); `/check` is never cached.

## Run

```bash
go run ./cmd/thanos -config thanos.yaml     # see thanos.example.yaml
go test -race ./...
```

Upstream sessions are opened with MCP protocol `2025-06-18` by default
(`protocol_version`): stateful servers such as mcp-grafana refuse newer ones.
