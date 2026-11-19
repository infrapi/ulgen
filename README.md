# thanos

One MCP server in front of several, for multi-tenant use: every tool takes a
`tenant` (a [Kayra](https://github.com/infrapi/kayra) namespace), and thanos
makes sure the caller may act there before calling the upstream with the
right credential.

```
MCP client ──► (gateway: LiteLLM, ...) ──Bearer <caller's Kayra token>──► thanos ─┬─► mcp-grafana   (tenant credential from Vault)
                                                                                  └─► telemetry MCP (caller's token)
```

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
- **`list_tenants`**: the caller's tenants and the upstreams usable in each
  (Kayra `GET .../memberships?api=`).
- **Which tools a person sees** is not thanos' business: the gateway in
  front decides (LiteLLM's allowed tools).

Every request must carry a bearer token Kayra resolves (`/oauth2/userinfo`,
cached `kayra.cache_ttl`); `/check` is never cached.

## Run

```bash
go run ./cmd/thanos -config thanos.yaml     # see thanos.example.yaml
go test -race ./...
```

Upstream sessions are opened with MCP protocol `2025-06-18` by default
(`protocol_version`): stateful servers such as mcp-grafana refuse newer ones.
