# meridian

Declarative, Kubernetes-native calendar sync. Mirror events between Google Calendar and CalDAV calendars with rule-based transforms ("show my private appointments as *Busy* on my work calendar") — configured entirely as code, deployed as a single stateless pod.

> **Status: pre-alpha, under active development.** Design is settled ([docs/DESIGN.md](docs/DESIGN.md)); implementation in progress. Not yet usable.

## Why

No k8s-native calendar sync tool exists. Existing options are SaaS products (Reclaim, Keeper) or CLI tools without a deployment story. Meridian is built for the GitOps world:

- **Rules as code**: sync rules in a ConfigMap, credentials in a Secret, chart in this repo. `git diff` shows exactly what your calendars will do.
- **Stateless by design**: no database. The calendars themselves are the state, via ownership markers on synced events. Every cycle reconciles from scratch — crash-safe, drift-proof, self-healing.
- **Single-tenant, small surface**: one pod, one ConfigMap, one Secret. Multi-user = multi-instance.

## How it works

Level-triggered reconciliation, like a Kubernetes controller: each cycle fetches all events in a rolling window from sources and destinations, computes the desired set of "shadow" events from your rules, and converges the destinations — create, update (content-hash change detection), or garbage-collect. Shadows carry ownership markers, so meridian only ever touches events it created.

See [docs/DESIGN.md](docs/DESIGN.md) for the full design and the reasoning behind it.

## License

TBD.
