# Meridian: Detailed Design

**Status**: Complete — all 6 decisions settled 2026-08-02. **This is the living copy**; the homelab repo holds a snapshot (`docs/plans/meridian-design.md`) plus the architecture-level parent plan (`docs/plans/reclaim-replacement.md`).
**Created**: 2026-08-02 (interactive design walkthrough; homelab tracking: hl-3y0, decision record hl-mjs)

## Decision 1: Sync engine model — stateless snapshot reconciliation (DECIDED 2026-08-02)

**Decision**: Every cycle, fetch **all** events in the sync window (90 days) from each source and destination, compute the desired shadow-event set from the rules, and diff against the actual shadow events on the destination (identified by ownership markers). Level-triggered reconciliation, like a k8s controller. **No sync cursors, no state DB for correctness — the calendars themselves are the state.**

Rejected: delta-driven sync (Google `syncToken` + CalDAV sync-collection with a `sync_links` DB, as sketched in the parent plan).

**Evidence** (research 2026-08-02):

- go-webdav's CalDAV client has **no sync-collection (RFC 6578) support** (only its CardDAV client does; no ctag helper either — go-webdav#148). Delta on CalDAV = hand-rolled REPORT/PROPFIND.
- Google `syncToken` **cannot be combined with `timeMin`/`timeMax`** — incremental means tracking the entire calendar, not a 90-day window.
- Quota is a non-issue: 90-day window poll every 5 min is orders of magnitude under Google defaults (600 queries/min/user).
- **Prior art**: inovex/CalendarSync (closest problem shape — Go, one-way transformed mirrors, polling) is stateless-snapshot by explicit design, markers in `extendedProperties.private`. Keeper.sh (delta + Postgres + Redis generation locks) has an issue tracker dominated by stale-state bugs and "force resync" requests — and still embeds ownership markers + orphan-GC as fallback because the DB can't be trusted. vdirsyncer (true two-way merge, the harder problem meridian avoids) still lists both sides in full every cycle. DAVx5 handles invalid sync-tokens by falling back to a full resync.

**Engine hardening requirements** (extracted from real issue trackers, all camps):

1. **Zombie-resurrection guard** — paired A→B/B→A rules: skip any source event carrying the sink's own ownership marker but absent from the sink (CalendarSync#68 fix).
2. ~~**Mass-delete guard** — empty or failed source fetch aborts the cycle instead of deleting all shadows (vdirsyncer's emptied-storage guard; watch its false-positive failure mode, vdirsyncer#694).~~ **SUPERSEDED 2026-08-08 (Fabio): faithful mirroring, detection over prevention.** An empty-but-successful source fetch empties the sinks — shadows are fully *derived* state, so a bogus empty response costs one cycle of missing busy-blocks and the next healthy cycle re-derives identical shadows (same content hashes); no information is ever lost. Freezing on empty sources (vdirsyncer-style) instead traps a sticky divergent state and blocks legitimate cleanups (vdirsyncer#694) — consistency wins over UX here. Failed fetches still abort the rule cycle (that's an error, not an empty — Decision 6 per-rule isolation). Replacement requirement: deleting more than a configurable fraction of a rule's shadows in one cycle fires `guard_triggers_total{guard=mass_delete}` **and** a notification-channel message (Discord webhook first, abstracted Notifier — mer notifier issue). Accepted residual risk: a *persistent* bogus-empty leaves destinations unblocked until a human reacts to the notification.
3. **Change detection via post-transform content hash** keyed by normalized source-ID — never ETags or updated-timestamps (they churn without content change).
4. **Orphan GC by ownership marker, restricted to the sync window** — window-edge events are a known ambiguity; never GC outside the window.
5. **Tolerate tombstoned/410 events** in fetches — treat as deleted, don't crash the cycle (CalendarSync#299).

### Decision 1b: No persistence layer at all (REVISED 2026-08-02)

~~Earlier in the walkthrough: optional pluggable `AuditSink` (Postgres default) for observability persistence.~~ **Superseded same day** after Decision 6 settled the metrics surface: with aggregates/trends/alerting covered by Prometheus metrics, the sink's only remaining value was op-level forensics — and the natural home for per-op records is **structured logs, not a database**. Every engine op emits a structured `slog` JSON record (`op`, `rule`, `src`, `reason`, hashes); log retention/queryability is a *platform* concern (see hl-zh5, VictoriaLogs evaluation — meridian op history is a good first use case), not the app's. Validation-week op volume is tiny, so size-based container log rotation retains weeks.

**Result: meridian has no database, no persistence interface, no CNPG dependency.** Footprint: one Deployment, one ConfigMap, one Secret. Observability contract for OSS users: Prometheus metrics + structured JSON logs — universally ingestible.

## Decision 2: Shadow identity & ownership marker (DECIDED 2026-08-02)

**Decision**: Structured marker with desired-content hash (Option B). Every shadow event carries:

| Field | Google (`extendedProperties.private`) | CalDAV (VEVENT prop) | Content |
|---|---|---|---|
| Source ref | `meridian.src` | `X-MERIDIAN-SRC` | Readable composite: source calendar ID + source event UID + recurrence-instance ID. Not hashed — debuggability over compactness (values stay well under Google's 1024-char limit). |
| Rule ID | `meridian.rule` | `X-MERIDIAN-RULE` | The `rules.yaml` rule that created this shadow. |
| Content hash | `meridian.hash` | `X-MERIDIAN-HASH` | Hash of the **post-transform desired content**. |
| Instance ID | `meridian.instance` | `X-MERIDIAN-INSTANCE` | This meridian instance's identity (added 2026-08-08, mer-uks). Required config value; not secret, but must be unique across instances and stable for the instance's lifetime — changing it orphans every shadow the instance wrote. |
| Schema version | `meridian.v` | `X-MERIDIAN-V` | Marker format version (`1`). |

**Change detection**: compute desired content → hash → compare against the hash **stored in the marker**. The provider's returned field values are never compared, so server-side normalization (whitespace, field coercion, reordering) is structurally irrelevant — avoids the spurious-update / re-appearing-event bug class in field-compare engines (CalendarSync#167). Update iff hashes differ; a write replaces content + marker together.

**Instance+rule scoping consequences (revised 2026-08-08, mer-uks)**: ownership scope = instance ID + rule ID, globally unique. Rules targeting the same destination never fight over each other's shadows; **multiple meridian instances may feed one destination** — each instance's listing and reconciliation are scoped to its own instance ID, other instances' shadows are as invisible as foreign events. **Sweep auto-GC (decided 2026-08-08, supersedes same-day detection-only design)**: declarative to the end — every cycle, each account discovers ALL its calendars (CalDAV FindCalendars, Google CalendarList, writable only) and deletes own-instance shadows whose rule does not target that calendar in the current config (removed rule, renamed rule ID = delete+recreate churn, calendar dropped from a rule's `To`, strays on never-configured calendars). Guardrails: only own-instance markers are ever deleted; active (rule → calendar) pairings are untouched by the sweep even when their cycle aborted; sweep is windowed — past shadows remain as calendar history. Sweep deletions ≥ the mass-delete fraction notify. The crisp line: **current config + sync window = reconciled automatically; outside it = `meridian wipe`** (unbounded, incl. `--instance` override for post-rename cleanup of orphaned old-instance shadows). Duplicate rule IDs within one config = startup error.

**Google bonus**: owned shadows are server-side queryable via `privateExtendedProperty=meridian.instance=<id>` — shadow listing and wipe never fetch other instances' or foreign events at all.

Rejected: minimal marker + field-by-field compare (CalendarSync style — inherits provider normalization quirks); single JSON-blob property (opaque, messier versioning).

## Decision 3: Recurrence & timezones (DECIDED 2026-08-02)

**Decision**: Expansion to discrete instances happens **server-side** on both protocols. Google: `events.list` with `singleEvents=true`. CalDAV: `expand` in the calendar-query REPORT (go-webdav v0.7.0 `CalendarExpandRequest`; sabre/dav implements it). Expansion is only needed on sources — shadows are always discrete single events.

**No silent fallback**: client-side expansion (rrule-go) exists as an *explicit per-account config choice* (`expansion: server | client`, default `server`), never an automatic fallback. If server-side expand misbehaves, meridian fails loudly; opting into client-side (with its documented caveats: reimplements EXDATE/RDATE/override machinery, must solve non-IANA TZID resolution — go-ical#10) is a deliberate user decision.

**Why server-side**: (1) client-side expansion reimplements exactly the machinery that dominates every sync tool's bug tracker (overridden-instance pairing, EXDATE edges, RRULE-change re-materialization); (2) server-expanded instances arrive as UTC instants — no TZID parsing, dodging go-ical's IANA-only limitation (Windows TZIDs like `W. Europe Standard Time` from Exchange-imported events would fail hard); (3) RRULE changes need no special handling — snapshot reconciliation re-derives instances each cycle, stale shadows GC.

**Known risk**: sabre/dav `expand` has had historical bugs (Infomaniak's deployed version unknown) and go-webdav's client support is new (Oct 2025). Early Phase 1 acceptance testing against real Infomaniak recurring events (incl. exceptions and overridden instances) is the gate; adapter boundary isolates the swap if needed.

**Gate PASSED (2026-08-07, mer-pdn, `hack/spike-expand`)**: Infomaniak runs sabre/dav 4.3.1; expand via go-webdav v0.7.0 `CalendarExpandRequest` verified against real events — weekly+EXDATE (excluded instance absent), overridden/moved instance (original slot replaced, RECURRENCE-ID references the *original* occurrence), all-day recurring (instances stay DATE-valued, RECURRENCE-ID also DATE), DST-crossing weekly (wall-clock preserved, 07:00Z→08:00Z at CEST→CET). Timed instances arrive as UTC `Z` values with no TZID, and *every* instance (including unmodified ones) carries a unique RECURRENCE-ID — exactly the identity + UTC-instant model the engine assumes. Infomaniak quirk for adapter/docs: CalDAV login must be the internal account ID (`USERxxxxx`), not the email — email auth "succeeds" but principal lookup 404s ("Principal with name … not found"); principal = `/principals/USERxxxxx/`, home set = `/calendars/USERxxxxx/`.

**Time model requirements**:

- Internal model is absolute instants (`time.Time`, UTC) + `AllDay bool`. No TZ math in the engine.
- All-day sources produce all-day (DATE-valued) shadows — never timed conversions. All-day expansion interpreted in the source calendar's declared timezone.
- Timed shadows written as UTC instants (unambiguous, renders correctly at any display TZ).
- **Window** = `[now − 1d, now + 90d]`, membership = *overlap*, applied identically to source fetch, desired-set computation, and orphan GC on both protocols. Lookback keeps running/just-ended events stable at the edge.

## Decision 4: Rules schema — typed fields + CEL `when` + Go templates (DECIDED 2026-08-02)

**Decision**: One rule shape with typed, schema-validated `filter` and `transform` blocks (the 95% path), plus two bounded escape hatches shipped in v1:

- **`filter.when`**: an optional CEL predicate (cel-go) over a small, documented, versioned event schema (`title`, `start`, `end`, `durationMinutes`, `allDay`, `transparent`, `attendees`, `organizer`, `status`). AND-ed with the typed filter fields. Compiled + type-checked at startup — bad expressions fail the pod at config load, never mid-sync (GitOps-safe). CEL chosen because it is the k8s ecosystem's standard embedded expression language (CRD validation, ValidatingAdmissionPolicy): non-Turing-complete, sandboxed, guaranteed termination, no I/O.
- **Go templates for string transforms**: `transform.title` / `transform.description` accept literals or templates (`"[{{ .SourceCalendar }}] {{ .Title }}"`). Transforms need no expression language beyond this — the event field set is finite, so typed fields + templating spans the space; filters are where variance lives.

```yaml
rules:
  - id: private-to-work-busy
    from: private/main
    to: [work/primary]
    filter:
      weekdays: [mon, tue, wed, thu, fri]
      window: "08:00-18:00"
      timezone: Europe/Zurich        # required for time-window evaluation against UTC instants
      skipTransparent: true
      when: '!event.title.startsWith("[private]")'   # optional CEL escape hatch
    transform:
      title: "Busy"
      description: drop
      attendees: drop
      reminders: []
```

**Accepted costs**: cel-go dependency (Google-maintained, embedded in k8s itself; pulls in protobuf); the event schema becomes a public, versioned API; a `meridian rules test` dry-run subcommand is anticipated scope for debugging semantically-wrong expressions.

Rejected: transformer-pipeline config (CalendarSync-style — more machinery, invites scope creep); named rule kinds (every new use case = code change); deferring CEL post-v1 (retrofitting the event schema under pressure is how APIs get rushed).

## Decision 5: Adapter contract (DECIDED 2026-08-02)

**Decision**: Both adapters implement one identical five-method interface — no capability flags, no engine branching:

```go
type CalendarAdapter interface {
    ListEvents(ctx, window) ([]Event, error)   // expanded instances overlapping window (sources)
    ListShadows(ctx, window) ([]Shadow, error) // meridian-owned shadows overlapping window (destinations)
    Create(ctx, Shadow) error
    Update(ctx, Shadow) error                  // full replace: content + marker (new hash) in one write
    Delete(ctx, ShadowRef) error               // idempotent: 404/410 = success
}
```

- Protocol asymmetry stays internal: Google `ListShadows` uses server-side `privateExtendedProperty` query; CalDAV fetches window + filters `X-MERIDIAN-*` client-side.
- `Event` is the normalized model (UTC instants + `AllDay`, CEL-schema fields, marker struct). Nothing protocol-specific crosses the boundary.
- Updates are full-replace (`events.update`, CalDAV PUT) — replacing content+marker atomically is what keeps hash-based change detection sound.
- Adapters normalize errors to a taxonomy the engine acts on: `NotFound/Gone` → treat deleted; `RateLimited` → back off, resume next cycle; `AuthFailed` → fatal, alert loudly; `Transient` → skip cycle (feeds mass-delete guard).

**Write concurrency: last-writer-wins.** Shadows are meridian-owned (Decision 1); hand-edits are overwritten and hand-deletes recreated by design, so If-Match optimistic concurrency would protect edits for at most one cycle before reconciliation steamrolls them — an illusion of safety. Also avoids hand-rolling raw HTTP (go-webdav client can't send If-Match). Deletes are idempotent (already-gone = success).

**Transforms deliberately NOT generalized** (asymmetry with filters, decided with Decision 4/5): a bad filter mis-selects; a bad transform writes corrupt calendar data. String fields are covered by templates (already computation over the event); conditional transforms fall out of rule composition (two rules, disjoint `when`, distinct transforms — marker rule-IDs prevent fighting). Future computed transforms (e.g. `padding: 15m`) enter as new *typed* fields with engine-controlled time math, never expression hooks.

## Decision 6: Failure & observability model (DECIDED 2026-08-02)

**Failure isolation: per-rule.** Each rule is an independent reconciliation; if its source fetch or destination shadow-listing fails, that rule's cycle aborts (no writes, no GC — mass-delete guard is per-rule) while all other rules proceed. Window fetches are shared per calendar within a cycle (one fetch per calendar, rules consume from it). No retry machinery: the next cycle is the retry; rate-limits back off and resume next tick.

**Metrics** (Prometheus → VictoriaMetrics): per-rule `ops_total{op}`, `cycle_duration`, `last_successful_cycle_timestamp` (the alerting primitive), `fetch_errors_total{class}`, `guard_triggers_total{guard=mass_delete|zombie}` — guards must be loud, silent guards defeat their purpose.

**Structured op logs** (slog JSON, stdout): one record per engine op with `op`, `rule`, `src` ref, `reason`, content hashes. This is the forensic + validation-week record (see Decision 1b).

**Alerts** (VMRules): `AuthFailed` → immediate Discord (OAuth revocation needs a human; staleness compounds); `last_successful_cycle` stale >30 min per rule → warn; any guard trigger → warn.

**Health**: liveness = process up; readiness = config parsed + CEL compiled + adapters authenticated once. Broken auth at startup = never ready = visible red in ArgoCD. **Refined 2026-08-08 (Fabio): the auth probe gates the loop.** Readiness comes from an explicit read-only startup probe (tiny windowed `ListShadows` per configured calendar — no new adapter API, no writes possible), and **no reconciliation cycle runs until a full probe pass succeeds** (retrying per interval). This makes not-ready trustworthy: an instance that never became ready has provably touched no calendar. Rejected: latching ready after the first successful cycle (a "never-ready" pod could already have written/deleted — not-ready must imply untouched); dry-run first cycle (threads a write-suppression conditional through the reconciler for a startup-only concern). Readiness latches once passed — later auth breakage is the `last_successful_cycle` staleness alert's job, not readiness flapping.

**Deployment model (settled 2026-08-08 after deliberate re-examination): long-running Deployment, 1 replica, Recreate.** Meridian is a reconciliation controller, not a batch job — the internal tick loop is the k8s controller idiom (external-dns, cert-manager), not scheduler duplication — and this entire observability surface is scrape-shaped: per-rule gauges, trend metrics, and readiness-visible-in-ArgoCD all presuppose a stable long-lived target; it also keeps the door open for shorter intervals or partial event-driven (Google watch channels) later. **CronJob remains a supported, documented alternative** (statelessness makes every cycle a self-contained run): `meridian run --once` under a k8s CronJob (`concurrencyPolicy: Forbid`) or system cron/systemd timer. Its trade-offs, to be documented positively in the deployment guide (mer-yxy): alerting compresses to job exit status (per-rule isolation becomes one bit per run), no trend metrics, ArgoCD tracks the CronJob spec rather than spawned Jobs (broken deploys look green), guard loudness falls to the notification channel + logs. slog op records are identical in both modes.

**Validation measurability**: steady-state signal is per-rule *converged cycles* (zero ops); op logs provide the day-by-day audit record. Validation follows the staged plan in [TESTING.md](TESTING.md) — sandbox E2E on scratch calendars, then in-cluster soak with real sources + scratch destinations, then **hard cutover** (revised 2026-08-02: no Reclaim-parallel run — two engines on the same calendars interfere; rollback = bulk-delete-by-marker + re-enable Reclaim).

## Research references

- Capability research (2026-08-02): go-webdav v0.7.0 client verified — calendar-query+time-range ✓, multiget ✓, PUT/DELETE ✓ (no If-Match), sync-collection ✗; X-props round-trip via go-ical Props model ✓. Infomaniak = sabre/dav (verified via WWW-Authenticate realm); DAVx5-tested. Google `extendedProperties.private` persists + queryable via `privateExtendedProperty`. Refresh tokens stable (no rotation); 7-day expiry only for "Testing"-status external OAuth apps — publish app to production status.
- Architecture survey (2026-08-02): inovex/CalendarSync, Keeper.sh (AGPL — lessons only), vdirsyncer, DAVx5. Details in Decision 1.
