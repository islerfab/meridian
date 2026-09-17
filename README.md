# meridian

[![CI](https://github.com/islerfab/meridian/actions/workflows/ci.yml/badge.svg)](https://github.com/islerfab/meridian/actions/workflows/ci.yml)
[![Docs](https://img.shields.io/github/v/release/islerfab/meridian?label=docs&color=0969da)](https://islerfab.github.io/meridian/)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Declarative, Kubernetes-native calendar sync. Meridian mirrors events between Google Calendar and CalDAV calendars as rule-based, transformed copies, configured entirely as code and running as a single stateless pod.

The canonical example: every appointment on your private calendar shows up as an anonymous **Busy** block on your work calendar. Colleagues see when you're free. They don't see why you aren't.

![One source calendar on the left holding three events. Two meridian rules in the middle. On the right, the shadows each rule produces: the first filters to events before 14:00 and rewrites them as grey Busy blocks with the location stripped, the second mirrors all three events with their titles prefixed.](docs/static/images/meridian.excalidraw.svg)

> **Status: pre-alpha, running unattended in production on one deployment.** The design is settled and the engine handles real traffic daily. The public interface — `rules.yaml`, the Helm chart, the CLI — may still change without notice before `v1.0.0`.

## What a config looks like

```yaml
instance: home # unique per deployment, and never change it once copies exist

accounts:
  - name: personal
    type: google
    clientIDEnv: MERIDIAN_GOOGLE_CLIENT_ID # names of env vars, never secrets
    clientSecretEnv: MERIDIAN_GOOGLE_CLIENT_SECRET
    refreshTokenEnv: MERIDIAN_GOOGLE_REFRESH_TOKEN
    calendars:
      - name: private
        id: primary
      - name: work
        id: work@example.com

rules:
  - id: private-to-work
    from: personal/private
    to: [personal/work]
    filter:
      weekdays: [mon, tue, wed, thu, fri]
      window: "08:00-18:00"
      timezone: Europe/Zurich
      skipAllDay: true
    transform:
      title: Busy
      description: drop
      location: drop
```

That's the whole surface: accounts, the calendars they expose, and rules pointing one at another. `meridian validate` checks it without touching a credential or a provider, so it runs in CI on every change.

Filters also take a [CEL](https://cel.dev/) predicate (`when: 'event.durationMinutes >= 30'`), and the three string transforms take Go templates (`title: "// {{ .Title }}"`). Worked examples are in [Write sync rules](https://islerfab.github.io/meridian/guides/write-rules/).

## Is this for you?

**Good fit if:**

- You already run Kubernetes and want sync rules living in Git alongside everything else you deploy.
- You want one-way mirrors with transformed content, like stripping titles down to "Busy".
- You're syncing your own calendars, not running a service for other people.

**Not a fit if:**

- You want scheduling: finding meeting slots, negotiating free/busy, moving things around. Meridian copies events and holds no opinions about how you should spend your week.
- You need a genuine two-way merge, where edits on either side flow back.
- You need multi-user or multi-tenant operation from one install.
- You need Exchange, Outlook, or Microsoft 365. Google Calendar and CalDAV only.

[Limitations](https://islerfab.github.io/meridian/limitations/) has the full list, and it's worth two minutes before you invest more.

## How it works

Level-triggered reconciliation, the way a Kubernetes controller works. Each cycle fetches every event in a rolling window from your sources and destinations, computes the copies the rules call for, and makes the destinations match: create, update, or delete.

- **No database.** The calendars hold the state. A crashed pod, a restored backup and a rewritten config all converge the same way.
- **Copies carry an ownership marker** naming the instance and rule that created them. Meridian only ever touches its own. Your events and other tools' events are invisible to it.
- **Change detection is a content hash** stored in that marker, so provider-side normalization can't trigger spurious updates.

## Next

| | |
|---|---|
| [Get started](https://islerfab.github.io/meridian/get-started/) | One rule syncing end to end on your laptop, in a few minutes. |
| [Deploy](https://islerfab.github.io/meridian/guides/deploy/) | Helm chart, config modes, metrics. |
| [Reference](https://islerfab.github.io/meridian/reference/) | Every config field, CLI flag and metric, generated from the source. |
| [FAQ](https://islerfab.github.io/meridian/faq/) | Why there's no database, whether it's safe to point at a calendar you use. |
| [Design](https://islerfab.github.io/meridian/design/) | What each part of the engine guarantees, and what it refuses to do. |

## Contributing

Issues and pull requests are welcome. [CONTRIBUTING.md](CONTRIBUTING.md) covers the build commands, the commit convention the release pipeline depends on, and what a change to the engine is expected to come with. Be civil about it ([code of conduct](CODE_OF_CONDUCT.md)).

Found a security problem? [SECURITY.md](SECURITY.md) has private reporting and the trust boundary — what meridian does with calendar credentials, and what it deliberately never touches.

## License

[MIT](LICENSE).
