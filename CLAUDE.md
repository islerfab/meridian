# Meridian — Project Context

K8s-native declarative calendar sync (Reclaim.ai replacement, Phase 1). Go, single-tenant, OSS-aimed.

## Start here, every session

1. **`docs/DESIGN.md` is the constitution.** All 6 engine design decisions with rationale and research evidence. Do not re-litigate settled decisions casually — if a decision must change, update DESIGN.md with the new rationale and mark the old one superseded.
2. **`bd ready`** for unblocked work (this repo has its own beads DB, prefix `mer-`).
3. The homelab repo (`~/dev/personal/homelab`) owns the *deployment* side: umbrella issue hl-3y0, strategy doc `docs/plans/reclaim-replacement.md`, and (later) the ArgoCD Application in homelab-argocd. Meridian's beads track implementation only.

## Design invariants (summary — DESIGN.md has the full versions)

- **Stateless snapshot reconciliation.** Level-triggered, calendars-are-the-state via ownership markers. NO database, NO sync cursors, NO persistence layer of any kind. If a feature seems to need a DB, the design is wrong — see DESIGN.md Decision 1/1b.
- **Change detection = content hash stored in the marker**, never provider field comparison, never ETags/timestamps.
- **Server-side recurrence expansion** on both protocols; client-side (rrule-go) only as explicit per-account config opt-in, never a silent fallback. No TZ math in the engine: UTC instants + `AllDay bool`; all-day stays DATE-valued.
- **Rules**: typed filter/transform fields + CEL `when` (filters only) + Go templates (string transforms only). Transforms deliberately not expression-powered — bad filters mis-select, bad transforms corrupt calendars.
- **Adapters**: one identical five-method interface, protocol asymmetry stays internal. Last-writer-wins on shadow writes; deletes idempotent (404/410 = success). Normalized error taxonomy: NotFound/Gone, RateLimited, AuthFailed, Transient.
- **Per-rule failure isolation**; the next cycle is the retry. The five hardening guards (zombie-resurrection, mass-delete, hash change-detection, windowed orphan-GC, tombstone tolerance) are correctness requirements, not nice-to-haves — each needs tests.
- **Observability = Prometheus metrics + structured slog JSON op logs.** Guard triggers must be loud (metrics).

## Working style (current phase)

Development is **very much interactive** right now. Even in auto/autonomous mode, check in with Fabio regularly whenever there is a choice to be made — library selection, structural decisions, trade-offs — **even if you are ~80% sure of the outcome**. Present the options with your recommendation and let him decide. Batch check-ins sensibly (don't ping per trivial detail), but err on the side of asking.

## Environment

- ARM64 (aarch64) WSL2. Go 1.26.5 at `~/.local/go/bin` (on PATH via `.bashrc`; in non-login shells use the full path).
- Build tooling: Taskfile.yml, run via `go tool task <target>` (Task is a go.mod tool dep) — `go tool task ci` = vet+lint+test+build. golangci-lint + goreleaser binaries at `~/.local/bin`. CLI framework: urfave/cli v3, isolated in `internal/cli`.
- Releases: conventional commits + semver (0.x). `go tool task release` tags via svu (go.mod tool dep) and pushes; `.github/workflows/release.yml` runs GoReleaser on `v*` tags — binaries + GH release + changelog (built-in conventional-commit groups) + OCI image via integrated ko (no Dockerfile). Version info: GoReleaser ldflags into `internal/cli` vars, `debug.ReadBuildInfo` fallback for local builds. Local pipeline check: `goreleaser release --snapshot --clean --skip=ko` (no docker in WSL2).
- Module path: `github.com/islerfab/meridian`.
- Target image: ko-built on `cgr.dev/chainguard/static` (distroless-style, nonroot), linux/arm64 + amd64, pushed to `ghcr.io/islerfab/meridian`.
- Deploys eventually to the homelab Talos cluster via ArgoCD; Helm chart lives in `deploy/` in this repo.

## OSS posture (current phase)

Design for the OSS use case (no homefab-specific assumptions in code/chart), but **do not fuss over what gets checked in** — before anything goes public, history will be pruned/squashed to appear fresh. Work-log-style commits, internal notes, and homelab references are all fine for now.

## Secrets

Same discipline as homelab: never generate passwords/tokens into chat, never decrypt secrets files into context. OAuth refresh tokens land in SOPS-encrypted Secrets in homelab-argocd, not in this repo.


<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:6cd5cc61 -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/SYNC_CONCEPTS.md for details and anti-patterns.

## Agent Context Profiles

The managed Beads block is task-tracking guidance, not permission to override repository, user, or orchestrator instructions.

- **Conservative (default)**: Use `bd` for task tracking. Do not run git commits, git pushes, or Dolt remote sync unless explicitly asked. At handoff, report changed files, validation, and suggested next commands.
- **Minimal**: Keep tool instruction files as pointers to `bd prime`; use the same conservative git policy unless active instructions say otherwise.
- **Team-maintainer**: Only when the repository explicitly opts in, agents may close beads, run quality gates, commit, and push as part of session close. A current "do not commit" or "do not push" instruction still wins.

## Session Completion

This protocol applies when ending a Beads implementation workflow. It is subordinate to explicit user, repository, and orchestrator instructions.

1. **File issues for remaining work** - Create beads for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **Handle git/sync by active profile**:
   ```bash
   # Conservative/minimal/default: report status and proposed commands; wait for approval.
   git status

   # Team-maintainer opt-in only, unless current instructions forbid it:
   git pull --rebase
   git push
   git status
   ```
5. **Hand off** - Summarize changes, validation, issue status, and any blocked sync/commit/push step

**Critical rules:**
- Explicit user or orchestrator instructions override this Beads block.
- Do not commit or push without clear authority from the active profile or the current user request.
- If a required sync or push is blocked, stop and report the exact command and error.
<!-- END BEADS INTEGRATION -->
