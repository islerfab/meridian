# Agent instructions

Meridian is a Kubernetes-first, declarative calendar sync engine: one-way, rule-based mirrors between a user's own calendars, configured as YAML and running as a single stateless pod. Google Calendar and CalDAV, in any combination. Go, single-tenant, MIT.

This file is the working contract for anyone changing the code, human or agent. [CONTRIBUTING.md](CONTRIBUTING.md) covers the pull-request process and the local commands; this file covers what the project will and won't accept as a change.

## Start here

**`docs/content/design.md` is the constitution.** It is a reference organized by what the engine operates on (the cycle, the marker, the copy, the rule, the adapter, recurrence and time, the guards, health), and it doubles as the public Design page. Read it before touching engine internals, and don't re-litigate settled invariants casually.

It is not a decision log. There is no decision numbering, and reintroducing any would rebuild the thing that already broke twice. When engine behaviour changes, **edit the affected claim in place** — never append a paragraph beside the old one, never add a section per change. A claim on that page that no longer describes the code is a bug in the page.

Four package `doc.go` files name the sections that govern them (`internal/model`, `internal/sync`, `internal/adapter`, `internal/config`), so the relevant invariants are one hop from the code.

## Design invariants

The summary; `design.md` has the full versions and the reasoning.

- **Stateless snapshot reconciliation.** Level-triggered, calendars-are-the-state via ownership markers. No database, no sync cursors, no persistence layer of any kind. If a feature seems to need one, the design is wrong.
- **Change detection is a content hash stored in the marker.** Never provider field comparison, never ETags or modified-timestamps.
- **Server-side recurrence expansion on both protocols**, full stop. There is no client-side expansion and no plan for one. No timezone math in the engine either: UTC instants plus an `AllDay` flag, and all-day events stay DATE-valued.
- **Rules are typed filter and transform fields**, plus CEL `when` for filters and Go templates for string transforms. Transforms are deliberately not expression-powered: a bad filter mis-selects, a bad transform corrupts calendars.
- **Adapters share one identical five-method interface.** Protocol asymmetry stays inside the adapter. Last-writer-wins on shadow writes, deletes idempotent (404/410 counts as success), and every provider error normalizes to `NotFound`/`Gone`, `RateLimited`, `AuthFailed` or `Transient`.
- **Per-rule failure isolation**, and the next cycle is the retry. There is no backoff and no queue.
- **The five hardening guards** — zombie resurrection, mass delete, hash change detection, windowed orphan GC, tombstone tolerance — are correctness requirements rather than nice-to-haves. Each one needs tests.
- **Observability is Prometheus metrics plus structured `slog` JSON operation logs.** Guard triggers must be loud.

## Build and tooling

- Build tooling is a `Taskfile.yml` run through `go tool task <target>`; Task itself is a `go.mod` tool dependency, so Go is the only prerequisite. `go tool task ci` runs vet, lint, test, build, and the Helm and docs checks — the same thing CI runs.
- `golangci-lint` is expected on `PATH`.
- The CLI framework is urfave/cli v3, isolated in `internal/cli`.
- Module path is `github.com/islerfab/meridian`.
- Docs are a Hugo site under `docs/`, built through the Task targets. `docs:gen` regenerates `docs/data/*.json` from the real source, so anything that renders the site must go through those targets rather than invoking `hugo` directly, or it publishes stale reference pages. The same target also generates the chart's `deploy/meridian/values.schema.json` — its `config` half comes from the `internal/config` structs, so a renamed config field is a `docs:gen:check` failure rather than a chart that accepts a key meridian no longer reads.
- Releases run GoReleaser on `v*` tags: binaries, a GitHub release, and an OCI image built by integrated ko (no Dockerfile) onto `cgr.dev/chainguard/static`, pushed to `ghcr.io/islerfab/meridian` for linux/arm64 and amd64. Check the pipeline locally with `goreleaser release --snapshot --clean --skip=ko`, dropping `--skip=ko` if a container runtime is available.

## Versioning

`go tool task release` is `git tag "$(go tool svu next --v0)"` plus a push of that one tag. Running the Release workflow from the Actions tab does the same on the runner, for when you aren't at a machine with the repo checked out; GoReleaser itself only ever runs in CI either way. This is svu's real, unmodified commit-type mapping — don't invent an alternate pre-1.0 scheme:

- `fix:` → patch, always.
- `feat:` → minor, always. The minor digit climbing freely pre-1.0 is normal and expected, not something to fight.
- Breaking (`type!:` or a `BREAKING CHANGE:` footer) → major, *except* that the Taskfile passes `--v0`, which caps it at a minor bump while the major version is 0. That is the deliberate guard against an accidental `v1.0.0`, added after an ordinary `feat:` commit caused one mid-development.
- **Leaving beta is `v1.0.0`, a deliberate one-time act.** Remove `--v0` from the Taskfile, then cut a commit marked `feat!:` or carrying a `BREAKING CHANGE:` footer so `svu next` actually crosses the threshold. Until that flag is gone, no accidental commit can push a major bump.

## Comments

A comment is re-read every time the file is, so it has to earn that. Write one
only when the code can't say it itself.

- **Worth keeping:** a non-obvious invariant, a constraint imposed from outside
  the file, the reason an obvious simpler approach doesn't work, or a decision
  that someone would otherwise "fix" by reverting it.
- **Not worth keeping:** restating what the line below does, dates, "verified
  on", how a value was arrived at, or anything that reads like a commit
  message. That belongs in the commit, the PR, or the tracker.
- **The test:** if deleting it wouldn't confuse the next reader, delete it. If
  it could be wrong in a year without anyone noticing, it's a liability rather
  than documentation.

Two or three sentences of rationale on a genuinely surprising line is right.
The same length on every line is noise, and it hides the ones that matter.

## Documentation voice

Prose in `docs/content/**` and the README is meant to stay pleasant for a human to read. Four rules carry most of that:

- **Vary sentence shape.** The pattern to watch is a stated fact followed by an em dash and a justifying clause, used over and over as the only way to explain anything: "Deletes are idempotent — an already-gone shadow counts as success." One or two of those is fine. A page where every explanation takes that shape reads as machinery. Justify with a second sentence, a bullet, or a bolded lead-in instead.
- **Paragraphs cap at roughly 60 words.** Past that, split into sentences or turn it into a list.
- **Go easy on intensifiers.** "Genuinely", "deliberately", "actually" and "fully" add nothing most times they appear, and they accumulate across pages faster than they look. The "X, not Y" antithesis is the same kind of tic.
- **Dry humour is welcome, sparingly**, drawn from the project's own vocabulary: shadows, zombie resurrection, tombstones, sweeps. Roughly one touch per page, always attached to a real technical point, never decorative.

Read new or edited prose once as a reader before considering it done.

## Secrets

Never generate passwords or tokens into the conversation, and never decrypt a secrets file into context. **Never read `.env` or any other local credentials file** with a file-read tool or a shell command — its values must not enter the context. Programs load it themselves via godotenv; when running commands, rely on that rather than echoing anything.

OAuth refresh tokens belong in the deployment's own secret store, not in this repository.

## Non-interactive shell commands

`cp`, `mv` and `rm` may be aliased to `-i` on some systems, which hangs an agent waiting on a y/n prompt that nobody will answer. Always pass the explicit non-interactive flag: `cp -f`, `mv -f`, `rm -f`, `rm -rf`, `cp -rf`. Same idea elsewhere: `-o BatchMode=yes` for `ssh` and `scp`, `-y` for `apt-get`.
