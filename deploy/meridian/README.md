# Meridian

Declarative, one-way calendar sync between Google Calendar and CalDAV, running
as a single stateless pod. Rules live in YAML next to everything else you
deploy: which calendars feed which, what gets filtered out, and how each copy
is rewritten on the way across.

The canonical example is a private calendar mirrored into a work calendar as
anonymous **Busy** blocks. Colleagues see when you're free without seeing why
you aren't.

There is no database. Each cycle reads the current state of both calendars,
works out which copies should exist, and makes the destination match. A crashed
pod, a restored backup and a rewritten config all converge the same way.

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

Pre-1.0: the rules schema, the chart values and the CLI can still change
without notice. [MIT licensed](https://github.com/islerfab/meridian/blob/main/LICENSE).
