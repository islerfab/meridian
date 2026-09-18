---
title: Meridian
cascade:
  type: docs
---

Declarative, Kubernetes-first calendar sync. Meridian mirrors events between Google Calendar and CalDAV calendars as rule-based, transformed copies, configured entirely as code and running as a single stateless pod.

The canonical example: every appointment on your private calendar shows up as an anonymous **Busy** block on your work calendar. Colleagues see when you're free. They don't see why you aren't.

![One source calendar on the left holding three events. Two Meridian rules in the middle. On the right, the shadows each rule produces: the first filters to events before 14:00 and rewrites them as grey Busy blocks with the location stripped, the second mirrors all three events with their titles prefixed.](images/meridian.excalidraw.svg)

> [!IMPORTANT]
> **Status: pre-alpha, running unattended in production on one deployment.** The design is settled (see [Design]({{% relref "design" %}})) and the engine handles real traffic daily. The public interface — `rules.yaml`, the Helm chart, the CLI — may still change without notice before `v1.0.0`.

## Is this for you?

Meridian does one thing. It copies events one way, from calendars you name to calendars you name, applying filters and transforms you wrote down in YAML.

**Good fit if:**

- You already run Kubernetes and want sync rules living in Git alongside everything else you deploy.
- You want one-way mirrors with transformed content, like stripping titles down to "Busy".
- You're syncing your own calendars, not running a service for other people.

**Not a fit if:**

- You want scheduling: finding meeting slots, negotiating free/busy, moving things around. Meridian copies events. It holds no opinions about how you should spend your week.
- You need a genuine two-way merge, where edits on either side flow back to the other.
- You need multi-user or multi-tenant operation from a single install.
- You need Exchange, Outlook, or Microsoft 365. Only Google Calendar and CalDAV are supported.

The full list of constraints lives on [Limitations]({{% relref "limitations" %}}), and it's worth two minutes before you invest more. The [FAQ]({{% relref "faq" %}}) answers why it's built this way.

## How it works

Meridian reconciles the way a Kubernetes controller does. Each cycle it fetches every event in a rolling window from your source and destination calendars, works out which copies *should* exist, and makes the destination match: create, update, or delete.

Those copies are called **shadows**, and every one carries an ownership marker naming the instance and rule that created it. Meridian only ever touches shadows it made itself. Your own events, and other tools' events, are invisible to it.

There's no database and no sync cursor. The calendars hold the state. That means a crashed pod, a restored backup, or a config change all converge the same way: by looking at what's there and fixing the difference.

```mermaid
flowchart LR
    A[Fetch source events] --> B[Compute desired shadows from rules]
    B --> C[List destination shadows]
    C --> D{Diff by content hash}
    D -->|new| E[Create]
    D -->|changed| F[Update]
    D -->|orphaned| G[Delete]
    E & F & G --> H[Next cycle]
    H --> A
```

## Where to go next

{{< cards >}}
  {{< refcard path="/get-started" title="New to Meridian?" subtitle="Get one rule syncing end to end on your laptop, in a few minutes." icon="play" >}}
  {{< refcard path="/guides/deploy" title="Deploying for real?" subtitle="Install the Helm chart, pick a config mode, wire up metrics." icon="server" >}}
  {{< refcard path="/guides/write-rules" title="Writing rules?" subtitle="Worked examples for filters, transforms, and bidirectional pairs." icon="adjustments" >}}
  {{< refcard path="/faq" title="Not sure yet?" subtitle="Why there's no database, whether it's safe to point at a calendar you use." >}}
  {{< refcard path="/design" title="Curious how it works?" subtitle="What each part of the engine guarantees, and what it refuses to do." icon="light-bulb" >}}
{{< /cards >}}
