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

`go tool task release` is `git tag -m "$(go tool svu next)"` plus a push of that one tag. The message is what makes the tag annotated, which a signed tag has to be; without it a maintainer who signs tags gets an editor rather than a release. Running the Release workflow from the Actions tab does the same on the runner, minus the signature, for when you aren't at a machine with the repo checked out; GoReleaser itself only ever runs in CI either way. This is svu's real, unmodified commit-type mapping:

- Breaking (`type!:` or a `BREAKING CHANGE:` footer) → major, always.
- `feat:` → minor, always.
- Everything else → patch. That is `always: true` in `.svu.yml`, not svu's default, which bumps for `feat:` and `fix:` only and resolves a docs-or-chore-only range back to the current tag — leaving it impossible to release. It matters because the docs site publishes from tags rather than from `main`, and because Dependabot commits arrive as `chore(deps)`, so a security bump would otherwise have to wait for an unrelated feature or be reworded by hand.

svu offers no way to map individual commit types to bump levels, so `always` is the whole vocabulary: anything that isn't a feature or a break is a patch. The cost is that `always` bumps even when **nothing** has landed, which would tag a second version on an identical tree. Both release paths therefore count commits since the last tag rather than comparing `svu next` to `svu current` — that comparison no longer means anything. Don't restore it.

**There is no longer a guard against a major bump.** The `--v0` cap came off in both the Taskfile and `release.yml` when v1.0.0 shipped, so a breaking marker on a routine change now costs a whole major version. It was added in the first place because an ordinary `feat:` commit once caused an accidental major mid-development; the protection now is commit hygiene rather than a flag.

Since 1.0.0 every change reaches `main` through a pull request closed with a merge commit, the only method the ruleset permits. Squash and rebase are both off, so commits land with their SHAs and signatures intact and **every commit subject** is what svu classifies and GoReleaser groups. The pull request title is not — it reaches only the merge commit body, which the changelog filter drops. `.github/workflows/pr.yml` rejects a commit subject that isn't a Conventional Commit and prints the version the merge would produce, so a breaking footer is visible before the merge rather than after the tag.

Required checks are strict, so a branch has to be current with `main` before it can merge. That is what makes the preview trustworthy: it is computed against the base the merge will actually use, not against whatever `main` looked like when the branch started.

Squash merging is enabled too, as the escape hatch for a contribution whose commits aren't worth preserving. It takes the title as subject and the pull request description as body (`PR_TITLE` / `PR_BODY`), so the *why* a contributor wrote down survives into the commit. Authorship survives as `Co-authored-by`; signatures do not, which is why merge is the default rather than the exception.

The two methods read different text, and inverting them is the sharp edge worth knowing before touching any of this:

| | merge commit | squash |
| --- | --- | --- |
| the title becomes | the merge commit **body** | the commit **subject** |
| the description becomes | nothing | the commit **body** |
| `feat!: …` in the title | inert | a major bump |
| `BREAKING CHANGE:` in the title | **a major bump** | inert |

The trap underneath both columns: svu matches a breaking-change marker **anywhere in a body** — not only as a trailing footer, not only at the start of a line. A mid-sentence mention with a colon after it is enough, so ordinary prose explaining that something *isn't* breaking will ship a major release. Only lowercase or a missing colon is safe.

Two mitigations, because neither alone is enough. `pr.yml` rejects a title carrying the marker, since under a merge commit that title becomes the body and `refs/pull/N/merge` gives the preview a generated message rather than the configured one, leaving it blind. And the preview simulates the squash from title *and* description, so a marker buried in prose shows up as a version number before the merge. The tidier fixes are unavailable: the API refuses `MERGE_MESSAGE` with a `BLANK` body, and promoting the title to the merge subject would duplicate every changelog entry and defeat the `^Merge pull request ` filter.

Beyond that, the squash form on GitHub is editable, so the last checkpoint is reading the message before confirming.

`pr.yml` fails only when *neither* method would produce a usable history, and prints the version each open route would produce.

If the per-commit check ever blocks a real contribution that isn't worth a tidy-up round, squash it — that is what the escape hatch is for. If that starts happening often, move the gate to the title and let squash be the default; that is the workflow the Conventional Commits FAQ recommends, and the only reason it isn't the default here is that merge commits keep SHAs and signatures intact.

## If you are an agent

You're welcome here. Most of this code arrived the same way, and everything in this file applies to you: the comment policy, the documentation voice, the design invariants that aren't up for casual re-litigation.

One condition on top. The pull request needs a human author who has read your diff, understands every line of it, and can defend it in review without consulting you. If nobody has read it, it isn't ready to open — and a maintainer reviewing it will find that out in the first exchange.

If you worked mostly on your own, with long stretches between corrections and decisions you made rather than took, quiz your human first. Pick the three lines you'd have the hardest time defending and ask them to explain each one back to you.

If the answers don't come, the change isn't ready, and the remedy is for them to read it rather than for you to write a better answer. When they were driving all along, skip the quiz: they've already done the reading.

## Pull requests

`.github/pull_request_template.md` asks for two things, why and testing, and not a summary of the diff. Keep it that way. A template that asks for more gets longer answers, and the reason a change exists is the part nobody can recover from the code a year later.

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
