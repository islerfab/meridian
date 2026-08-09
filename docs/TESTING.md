# Meridian Testing & Cutover Plan

**Created**: 2026-08-02. Supersedes the "1 week alongside Reclaim" criterion from the original homelab plan.

**Why no parallel run with Reclaim**: two sync engines on the same calendars interfere — Reclaim mirrors meridian's shadows, meridian sees Reclaim's blocks as sources, and any inconsistency is ambiguous between engine bugs and engine *interaction*. Instead: staged testing on calendars Reclaim never touches, then a hard cut. The rollback that makes the hard cut safe is designed in: all meridian events carry ownership markers → back-out = one bulk-delete-by-marker sweep + re-enable Reclaim.

## Stage 0 — Unit & engine tests (continuous, in-repo)

- Each of the 5 hardening guards has dedicated tests (mer-zgf): zombie-resurrection, mass-delete abort, hash change-detection, windowed orphan GC, tombstone tolerance.
- Engine tested purely against a fake in-memory adapter; config parsing/CEL/template failures table-driven (mer-gyw).
- Marker + content-hash round-trip and hash canonicalization stability (mer-e6u).

## Stage 1 — Infomaniak expand spike (mer-pdn, early)

Real recurring events on a scratch Infomaniak calendar: weekly with EXDATE, an overridden (moved) instance, all-day recurring, DST-crossing series. Verify expanded instances, RECURRENCE-ID identity stability, UTC instants. Gate for Decision 3; failure promotes client-side expansion work.

## Stage 2 — Sandbox end-to-end (fresh scratch calendars, run locally)

Dedicated throwaway calendars on both providers, never touched by Reclaim. Synthetic event matrix + full rule set (including a bidirectional pair). Verify:

- create / update (hash-triggered) / delete / orphan GC paths
- loop detection: bidirectional rules produce no duplicates, no resurrection
- idempotence: steady state = converged cycles (zero ops), every cycle
- window edges: events entering/leaving the 90-day window, running events at the lookback edge
- crash safety: kill mid-cycle at arbitrary points → next cycle converges, no duplicates/orphans
- hand-tamper: delete shadow events manually → recreated next cycle; manual *edits* that preserve the marker **persist by design** (Decision 2 marker-trust — see tamper-edit semantics there) and must be reported by the `meridian_shadow_drift` gauge (mer-hn8); remediation = `meridian wipe rule <id>` + next cycle

## Stage 3 — Production-adjacent soak (in-cluster, real source data, scratch destinations)

Deploy to the homelab cluster. Rules read the REAL private calendar but write only to NEW scratch destination calendars (real data variety, zero risk to work calendars; Reclaim untouched). Soak 48–72h minimum:

- OAuth refresh stability, no manual reauth (original acceptance criterion, kept)
- converged-cycle metrics steady; alerts wired and quiet; op log spot-audited against actual calendar changes
- real-world event variety: recurring series with exceptions, all-day, invitations/attendee events, cancellations

## Stage 4 — Hard cutover

1. Disable Reclaim's sync features; clean its synced artifacts from the calendars.
2. Repoint rules at the real destinations (private→work busy, work→private mirror, free-blocks→work).
3. First cycles supervised via op log; then metrics/alerts carry the watch.
4. Observation window (~1 week of normal usage) with rollback armed: bulk-delete-by-marker + re-enable Reclaim.
5. Clean exit → cancel Reclaim subscription (hl-3y0 closes).
