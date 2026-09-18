---
title: Deploy on Kubernetes
weight: 1
icon: server
description: Install the chart, pick a config mode, wire up metrics, and cut over safely.
---

Meridian ships as a Helm chart in this repo (`deploy/meridian`) and an image at `ghcr.io/islerfab/meridian`, built for `linux/amd64` and `linux/arm64` on a distroless, nonroot base.

## What gets deployed

The chart renders one Deployment (single replica, `Recreate` strategy), one ConfigMap or a reference to your own, a Service, and optionally a ServiceMonitor. No Job, no CronJob.

Meridian runs as a long-running tick loop, the same idiom as any other Kubernetes controller. Readiness and the per-rule metrics in [Operate]({{% relref "guides/operate" %}}) assume a stable, scrapeable pod between cycles (see [Design]({{% relref "design" %}})). It can't scale to zero between runs; that's not on the roadmap.

## Minimal install

```bash
helm install meridian oci://ghcr.io/islerfab/charts/meridian \
  --set credentials.existingSecret=meridian-credentials \
  -f my-rules-values.yaml
```

Two things need to exist first.

**A Secret**, named by `credentials.existingSecret`, holding the environment variables your `rules.yaml` refers to by name: `usernameEnv`, `passwordEnv`, `clientIDEnv`, and friends. The chart never sees secret values, only the names of the variables holding them.

**Your rules**, supplied through one of two config modes.

### Getting the Secret into the cluster via GitOps

The chart deliberately doesn't manage the Secret itself — plaintext credentials have no business in a values file. Two common ways to keep it in Git anyway:

{{< tabs >}}
{{< tab name="SOPS" >}}
Encrypt a plain `Secret` manifest with [SOPS](https://github.com/getsops/sops) and decrypt it at apply time (e.g. via `ksops` or the [SOPS Secrets Operator](https://github.com/isindir/sops-secrets-operator)):

```bash
sops encrypt --in-place meridian-credentials.enc.yaml
```

Commit the encrypted file; the plaintext never touches Git.
{{< /tab >}}
{{< tab name="Sealed Secrets" >}}
Encrypt with [kubeseal](https://github.com/bitnami-labs/sealed-secrets) against your cluster's public key; only that cluster's controller can decrypt it back into a Secret:

```bash
kubectl create secret generic meridian-credentials \
  --dry-run=client -o yaml \
  --from-literal=MERIDIAN_GOOGLE_REFRESH_TOKEN=... \
  | kubeseal -o yaml > meridian-credentials.sealed.yaml
```

Commit the `SealedSecret`; the SealedSecrets controller reconciles it into a real Secret in-cluster.
{{< /tab >}}
{{< /tabs >}}

Either way, the result is the same: a Secret named `credentials.existingSecret` exists in the release namespace before you `helm install`.

## Config modes

{{< tabs >}}

{{< tab name="inline config.*" >}}
```yaml {filename="values.yaml"}
config:
  instance: my-instance # required, unique, and stable forever
  interval: 5m
  accounts: [...]
  rules: [...]
```

The chart renders this into a ConfigMap and puts a checksum annotation on the Deployment, so changing your rules rolls the pod. This is the simplest path, and your rules live in the Helm values file.
{{< /tab >}}

{{< tab name="existingConfigMap" >}}
```yaml {filename="values.yaml"}
existingConfigMap: my-rules-configmap
```

Use this to keep `rules.yaml` as a real file in your GitOps repo, outside the chart. A kustomize `configMapGenerator` works well, since its hash-suffixed name rolls the pod on change. `config.*` is ignored when this is set.
{{< /tab >}}

{{< /tabs >}}

## Check the rendered config before applying it

```bash
helm template meridian deploy/meridian -f my-rules-values.yaml --show-only templates/configmap.yaml \
  | meridian validate --from-configmap --config /dev/stdin
```

This runs the same strict parse and rule compilation that `run` performs at startup, without touching a single credential. It's cheap enough to wire into CI on every values change, and it catches the whole class of mistakes that would otherwise surface as a `CrashLoopBackOff`.

## Observability wiring

The pod serves `/metrics`, `/healthz` and `/readyz` on `listen.port`, default `8080`.

Set `serviceMonitor.enabled: true` if you run the Prometheus Operator. Otherwise point your own scrape config at the Service.

`service.publishNotReadyAddresses` defaults to `true`. A pod that never goes ready is failing its auth probes, and that is precisely when you want to be able to scrape it.

See [Operate]({{% relref "guides/operate" %}}) for which metrics deserve alerts.

## Cutting over safely

Meridian makes rollback cheap. Every event it writes carries an ownership marker, so undoing everything is one `meridian wipe` away. Cheap to undo is not the same as safe to skip, though, and two precautions are worth the delay.

> [!WARNING]
> **Don't run Meridian alongside another sync tool on the same calendars.** Two engines each treating the other's output as a source event to mirror will produce results that are very hard to distinguish from a bug in either one. Stage on calendars the old tool never touches, then cut over.

The useful staging trick is to split the risk: point rules at **scratch destination calendars** while reading from your **real sources**. You get the full variety of your actual data, including the recurring series and all-day oddities no synthetic test would have thought of, with nothing at stake on the destination side.

Leave it there for a day or two and watch three things:

- `meridian_last_successful_cycle_timestamp_seconds` staying fresh for every rule.
- Guard metrics staying silent.
- The op log matching what you know happened on the source calendars.

Then repoint the rules at the real destinations, watch the first few cycles directly, and let alerts take over after that.
