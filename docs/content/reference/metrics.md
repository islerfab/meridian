---
title: Metrics & health
weight: 3
icon: chart-bar
description: The Prometheus metrics surface and the health endpoints.
---

Meridian's observability surface is Prometheus metrics plus structured JSON logs — no database, so no other query interface exists. All three HTTP endpoints below share one listener (`--listen`, default `[::]:8080`).

## HTTP endpoints

| Path | Meaning |
|---|---|
| `/healthz` | Liveness: the process is up. Always `200 ok` once the process is running. |
| `/readyz` | Readiness: config parsed, CEL/templates compiled, **and** every configured calendar's credentials verified via a read-only startup probe. `503` until all probes pass, then **latches `200` permanently** — later auth breakage is the `meridian_last_successful_cycle_timestamp_seconds` staleness alert's job, not a readiness flap. |
| `/metrics` | Prometheus text exposition format. |

A pod that never reaches ready has provably touched no calendar. The readiness probe never runs anything beyond a windowed, read-only listing.

## Metrics

Each description below is the metric's own Prometheus `Help` string, which is the same text Prometheus and Grafana show you.

{{% metrics-docs %}}

For which of these to alert on, see [Operate]({{% relref "guides/operate#what-to-alert-on" %}}).

## Structured op logs

Every engine operation emits one `slog` JSON record to stdout with `op`, `rule`, the source event reference, `reason`, and the relevant content hashes. There is no database to query instead, so this is the forensic and audit record.
