---
title: Audit log and metrics
description: One audit line per tool call, the request id passed on to the upstreams, and ulgen's Prometheus metrics.
weight: 60
---

# Audit log and metrics

## Audit log

ulgen logs JSON on stderr. Each tool call is one `tool call` line:

| Field | |
|---|---|
| `user` | the caller, as Kayra resolved them |
| `upstream`, `tool`, `tenant` | what was called, and where |
| `outcome` | `ok`; `refused` by ulgen or Kayra; `tool_error`, an error result from the upstream; `error`, the upstream or Kayra unreachable |
| `duration_ms` | the call's duration, Kayra's check included |
| `request_id` | the request's `X-Request-Id` |

```json
{"time":"2026-11-23T19:40:02.118Z","level":"INFO","msg":"tool call","user":"jdoe","upstream":"grafana","tool":"query_prometheus","tenant":"acme","outcome":"ok","duration_ms":182,"request_id":"5f0c9a7e-…"}
```

Requests refused for want of a token are logged as warnings, with their
request id.

## Following a request

`request_id` is the request's `X-Request-Id`, set by a proxy in front of
ulgen. ulgen passes it on, in the same header, to the upstream it calls.
One request can then be followed through the proxy's, ulgen's and the
upstream's logs.

## Metrics

`/metrics` is on the same listener as the MCP endpoint. To scrape it from
the node when ulgen listens on the loopback, behind a proxy that routes no
`/metrics`, point the scraper at the loopback address.

| Metric | Labels |
|---|---|
| `ulgen_tool_calls_total` | `upstream`, `tool`, `outcome` (as in the audit log) |
| `ulgen_tool_call_duration_seconds` | `upstream`, `outcome` |
| `ulgen_kayra_request_duration_seconds` | `op` (`verify`, `check`, `namespaces`), `result` (`ok`, `denied`, `refused`, `error`) |
| `ulgen_credential_reads_total` | `upstream`, `result` (`ok`, `error`) |
| `ulgen_unauthenticated_requests_total` | `reason` (`no_token`, `refused_token`) |
| `ulgen_tools_exposed` | `upstream`: the tools exposed, as last listed |
| `ulgen_tools_list_errors_total` | `upstream` |
| `ulgen_build_info` | `version` |

The Go runtime's and the process's metrics are also exported.

A few queries:

```promql
# calls per second, per upstream and outcome
sum by (upstream, outcome) (rate(ulgen_tool_calls_total[5m]))

# p95 of Kayra's /check
histogram_quantile(0.95, sum by (le) (rate(ulgen_kayra_request_duration_seconds_bucket{op="check"}[5m])))

# an upstream that stopped listing its tools
increase(ulgen_tools_list_errors_total[15m]) > 0
```
