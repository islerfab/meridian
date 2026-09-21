# Meridian

Declarative, one-way calendar sync between your own calendars, running as a
single stateless pod — a private calendar mirrored into a work calendar as
anonymous **Busy** blocks, say. Google Calendar and CalDAV, in any combination.
The [project README](https://github.com/islerfab/meridian) has the wider tour
and the [documentation](https://islerfab.github.io/meridian/) has everything
else; this page is about installing the chart.

What that costs you to run is the part worth knowing up front: no database, no
PersistentVolumeClaim, no StatefulSet. Each cycle reads the current state of
both calendars, works out which copies should exist, and makes the destination
match, which is why one stateless replica is enough.

## What gets deployed

One Deployment (single replica, `Recreate`), a ConfigMap holding your rules or
a reference to one you manage yourself, a Service, and optionally a
ServiceMonitor. No Job, no CronJob — Meridian runs a tick loop the way any
other controller does, and stays scrapeable between cycles.

## Install

```bash
helm install meridian oci://ghcr.io/islerfab/charts/meridian \
  --set credentials.existingSecret=meridian-credentials \
  -f my-values.yaml
```

Two things have to exist first.

**A Secret** named by `credentials.existingSecret`, holding the environment
variables your rules refer to by name (`usernameEnv`, `clientIDEnv`, and
friends). The chart handles variable *names* only — credential values never
pass through a values file. The [deploy
guide](https://islerfab.github.io/meridian/guides/deploy/) covers getting that
Secret into Git safely with SOPS or Sealed Secrets.

**Your rules**, in one of two config modes:

```yaml
# Inline: the chart renders this into a ConfigMap and annotates the Deployment
# with its checksum, so editing rules rolls the pod.
config:
  instance: home # unique per deployment, and never change it once copies exist
  interval: 5m
  accounts: [...]
  rules: [...]
```

```yaml
# Or point at a ConfigMap you manage, with a rules.yaml key. Pairs well with a
# kustomize configMapGenerator, whose hash-suffixed name rolls the pod for you.
existingConfigMap: meridian-rules
```

`meridian validate` checks a config without touching a provider or a
credential, so rules can be verified in CI before they reach a cluster.

## Values

| Key | Default | What it does |
|---|---|---|
| `config` | `{}` | Rules rendered into a ConfigMap. Ignored when `existingConfigMap` is set. |
| `existingConfigMap` | `""` | Name of a ConfigMap with a `rules.yaml` key, managed outside the chart. |
| `credentials.existingSecret` | `""` | Secret holding the env vars the rules name. Required. |
| `image.repository` | `ghcr.io/islerfab/meridian` | Image, `linux/amd64` and `linux/arm64`. |
| `image.tag` | `""` | Defaults to the chart's appVersion. |
| `extraEnv` | `[]` | Extra environment variables, e.g. `GOMEMLIMIT`. |
| `listen.port` | `8080` | Port for `/metrics`, `/healthz` and `/readyz`. |
| `service.publishNotReadyAddresses` | `true` | Keeps a never-ready pod scrapeable — its error counters matter most exactly then. |
| `serviceMonitor.enabled` | `false` | Prometheus-operator ServiceMonitor, with `interval`, `scrapeTimeout` and `labels`. |
| `resources` | 10m / 32Mi, 64Mi limit | Sized for a workload that sleeps between cycles. |
| `podSecurityContext`, `securityContext` | hardened | Nonroot 65532, read-only root filesystem, all capabilities dropped. |
| `podAnnotations`, `podLabels`, `priorityClassName`, `nodeSelector`, `tolerations`, `affinity` | empty | The usual scheduling and metadata escape hatches. |

`helm show values oci://ghcr.io/islerfab/charts/meridian` prints the file
itself, comments included.

The chart ships a `values.schema.json`, so a misspelled key is an install-time
error naming the offending path rather than a silent fall back to the default.
It covers the whole `config` block too, generated from the same Go types
meridian parses `rules.yaml` with. Requiredness and cross-field rules are not
in it — meridian validates those at startup, where the message can be a useful
one. Point an editor at the schema and the same file gets completion and
hover docs:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/islerfab/meridian/main/deploy/meridian/values.schema.json
```

## Observability

The pod exposes Prometheus metrics on `listen.port`. Per-rule counters for
creates, updates, deletes and errors, a timestamp for the last successful
cycle, and counters for the hardening guards — which fire loudly on purpose.
[Operate](https://islerfab.github.io/meridian/guides/operate/) covers what's
worth alerting on and what cleans up after itself.

## Verifying what you installed

Both the image and the chart are signed with cosign in keyless mode, so the
signature binds to this repository's release workflow rather than to a key
somebody has to keep safe:

```bash
cosign verify ghcr.io/islerfab/charts/meridian:X.Y.Z \
  --certificate-identity-regexp '^https://github\.com/islerfab/meridian/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Documentation

| | |
|---|---|
| [Get started](https://islerfab.github.io/meridian/get-started/) | One rule syncing end to end on your laptop. |
| [Deploy on Kubernetes](https://islerfab.github.io/meridian/guides/deploy/) | The long version of this page. |
| [Write sync rules](https://islerfab.github.io/meridian/guides/write-rules/) | Filters, transforms, and the traps worth knowing. |
| [Configuration reference](https://islerfab.github.io/meridian/reference/configuration/) | Every field, generated from the source. |
| [Limitations](https://islerfab.github.io/meridian/limitations/) | What it doesn't do, on purpose and otherwise. |

Dogfood: Meridian runs unattended on the maintainer's homelab, mirroring real
calendars around the clock. [MIT licensed](https://github.com/islerfab/meridian/blob/main/LICENSE).
