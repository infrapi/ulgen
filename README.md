# ulgen

One MCP endpoint in front of the platform's APIs. Every API built on
[infrapi/lib](https://github.com/infrapi/lib) serves its own MCP endpoint
(`<root>/-/mcp`); ulgen finds them in [Kayra](https://github.com/infrapi/kayra)'s
catalog and serves all their tools behind one endpoint, every call made with
the caller's own token: each API checks it, as for any of its routes.

Documentation: [infrapi.github.io/products/ulgen](https://infrapi.github.io/products/ulgen/docs/), from [`docs/`](docs/index.md).

```text
MCP client ──Bearer <caller's token>──► ulgen ──► compute.infrapi.io/v1/-/mcp
                                          │   ──► grafana.infrapi.io/v1/-/mcp
                                          └─ Kayra: who calls, which APIs, which namespaces
```

- `/apis/<INFRAPI_ULGEN_API>/v1/-/mcp`: every API's tools, `<short_name>_<tool>`;
  `.../-/mcp/<short_name>`: one API's, under their own names. Each has
  `list_namespaces`, the caller's Kayra namespaces.
- A request without a valid token gets a 401 naming ulgen's protected resource
  metadata (RFC 9728): MCP clients log in at Kayra from there.
- The APIs: Kayra's `GET /apis` and the contract it last read from each, with
  the caller's token; an API whose contract has its MCP endpoint is served.
- One `tool call` audit line per call, the request id passed on to the API,
  Prometheus metrics at `<root>/-/metrics`.

An MCP server that is not an infrapi API (mcp-grafana) is served through one
that bridges it: tofu-api's `mcp.bridges`, lib's `mcp/bridge`.

## Run

```bash
cp ulgen.env.example ulgen.env    # INFRAPI_APP_* and INFRAPI_ULGEN_*
INFRAPI_CONFIG_DOTENV_FILE=ulgen.env go run ./cmd/ulgen
go run ./cmd/ulgen -version
go test -race ./...
```

Releases: on `main`, a `feat` or `fix` commit makes go-semantic-release tag the next version, and
GoReleaser publishes the Linux archives and `checksums.txt`.
