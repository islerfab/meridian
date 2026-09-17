# Security

Meridian asks for read and write access to calendars you actually use, so it's
reasonable to want to know what it does with that access before you hand it
over. This page covers the trust boundary and how to report a problem.

## Reporting a vulnerability

Use GitHub's private vulnerability reporting: **Security → Report a
vulnerability** on this repository. That opens a private thread visible only to
the maintainer, so please use it rather than a public issue.

This is a single-maintainer project. Expect an acknowledgement within a week
and a fix when there's a fix to make. If something is being actively exploited,
say so in the first line and it moves to the front.

## Supported versions

Pre-1.0, only the latest release gets fixes. There are no backport branches.

## What meridian does with your credentials

- **The config holds the *names* of environment variables, never secret
  values.** `rules.yaml` is meant to be committed to Git, and nothing in it is
  sensitive. The Helm chart sources the actual values from a Kubernetes Secret.
- **`meridian oauth` writes the refresh token straight to `.env`.** It is never
  printed to the terminal and never logged.
- **Meridian only touches events it created.** Every copy it writes carries an
  ownership marker naming the instance and rule behind it. Reconciliation,
  garbage collection and `wipe` all filter on that marker, so your own events
  and other tools' events are invisible to it.
- **It won't write before it can read.** A read-only probe runs against every
  configured calendar at startup, and the reconciliation loop doesn't begin
  until all of them pass. An instance that never became ready has provably
  written nothing.
- **Operation logs carry identifiers, not content.** Rule and calendar IDs,
  source event references and content hashes. Event titles, descriptions and
  locations are not logged.
- **`meridian validate` touches no credential and no provider**, which is why
  it's safe to run in CI on every config change.

The scope meridian needs is read and write on the calendars named in its
config. It requests nothing else and talks to no service other than your
configured providers.

## Out of scope

These aren't vulnerabilities in meridian, though they may still ruin your day:

- Anyone with read access to the Kubernetes Secret, or to `.env`, has your
  calendar credentials. Protect them the way you'd protect any other secret.
- A rule you wrote that points at the wrong calendar will faithfully do what
  you asked. `meridian validate` catches structural mistakes; it can't know
  which events you meant to select. Stage rules against a scratch destination
  calendar first.
- A compromised provider account. Meridian has no way to detect that and no
  way to help.

## Supply chain

Releases are built by GitHub Actions from a tag, with no manual upload step.
Binaries, archives and the container image are built by
[GoReleaser](https://goreleaser.com/); the image is produced by
[ko](https://ko.build/) on `cgr.dev/chainguard/static` and carries an SPDX
SBOM.

Artifacts are signed with [cosign](https://github.com/sigstore/cosign) in
keyless mode, so the signature is bound to this repository and workflow rather
than to a private key somebody has to keep safe. To verify the image:

```bash
cosign verify ghcr.io/islerfab/meridian:vX.Y.Z \
  --certificate-identity-regexp '^https://github\.com/islerfab/meridian/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

`go tool task vuln` runs [govulncheck](https://go.dev/blog/govulncheck) against
the module. CI runs it on every push, and a scheduled job runs it weekly so a
quiet month doesn't mean an unnoticed advisory. It's call-graph aware, so it
reports what this code actually reaches rather than everything in the
dependency tree. Dependency updates come through Dependabot.
