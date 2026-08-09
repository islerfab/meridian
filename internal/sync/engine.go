// Package sync is the reconciliation engine: stateless snapshot
// reconciliation, level-triggered like a k8s controller (DESIGN.md
// Decision 1). Every cycle re-derives the desired shadow set from sources +
// rules and converges each destination onto it. The calendars are the
// state; there is no persistence of any kind (Decision 1b).
package sync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/islerfab/meridian/internal/adapter"
	"github.com/islerfab/meridian/internal/model"
	"github.com/islerfab/meridian/internal/notify"
)

// Config assembles an Engine.
type Config struct {
	// InstanceID is this meridian instance's identity (required, not
	// secret, stable for the instance's lifetime — changing it orphans
	// every shadow the instance ever wrote). Instance+rule is the globally
	// unique ownership scope: multiple instances may feed one destination
	// as long as instance IDs differ (decided 2026-08-08, mer-uks).
	InstanceID string

	Rules    []Rule
	Adapters map[string]adapter.CalendarAdapter // by logical calendar ID

	// Sweepers enable account-wide sweep auto-GC (decided 2026-08-08):
	// every cycle, each account discovers ALL its calendars and deletes
	// own-instance shadows whose rule does not target that calendar in the
	// current config. Keyed by account name; optional.
	Sweepers map[string]adapter.AccountSweeper
	// CalendarKeys maps account name → provider calendar ID → logical
	// calendar key ("account/calendar") for configured calendars, so the
	// sweep can recognize rule-targeted calendars among discovered ones.
	CalendarKeys map[string]map[string]string

	// Window bounds relative to cycle start (Decision 3): defaults 24h back,
	// 90 days ahead.
	Lookback  time.Duration
	Lookahead time.Duration

	// MassDeleteNotifyFraction triggers the mass-delete detection (guard 2
	// as superseded 2026-08-08): deleting more than this fraction of a
	// rule+destination's shadows in one cycle fires the guard metric and a
	// notification. <= 0 disables detection (deletes still execute). The
	// deletes are NEVER blocked — faithful mirroring, detection over
	// prevention.
	MassDeleteNotifyFraction float64

	Notifier notify.Notifier
	Logger   *slog.Logger
	Metrics  *Metrics
}

// Engine reconciles all rules once per RunCycle call. The caller owns the
// tick loop; there is no retry machinery — the next cycle is the retry
// (Decision 6).
type Engine struct {
	cfg Config
	log *slog.Logger
	now func() time.Time
}

// New validates wiring (every rule's calendars must have adapters).
func New(cfg Config) (*Engine, error) {
	if cfg.InstanceID == "" {
		return nil, errors.New("sync: instance ID is required (unique per meridian instance; changing it orphans all shadows)")
	}
	seen := map[string]bool{}
	for _, r := range cfg.Rules {
		if r.ID == "" {
			return nil, errors.New("sync: rule with empty ID")
		}
		if seen[r.ID] {
			return nil, fmt.Errorf("sync: duplicate rule ID %q (marker collision)", r.ID)
		}
		seen[r.ID] = true
		if _, ok := cfg.Adapters[r.From]; !ok {
			return nil, fmt.Errorf("sync: rule %s: no adapter for source calendar %q", r.ID, r.From)
		}
		if len(r.To) == 0 {
			return nil, fmt.Errorf("sync: rule %s: no destinations", r.ID)
		}
		for _, dest := range r.To {
			if _, ok := cfg.Adapters[dest]; !ok {
				return nil, fmt.Errorf("sync: rule %s: no adapter for destination calendar %q", r.ID, dest)
			}
			if dest == r.From {
				return nil, fmt.Errorf("sync: rule %s: destination equals source %q", r.ID, dest)
			}
		}
	}
	if cfg.Lookback <= 0 {
		cfg.Lookback = 24 * time.Hour
	}
	if cfg.Lookahead <= 0 {
		cfg.Lookahead = 90 * 24 * time.Hour
	}
	if cfg.Notifier == nil {
		cfg.Notifier = notify.Nop{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Metrics == nil {
		cfg.Metrics = NewMetrics(nil)
	}
	return &Engine{cfg: cfg, log: cfg.Logger, now: time.Now}, nil
}

// fetchResult caches one calendar's window fetch for the cycle (fetches are
// shared per calendar across rules, Decision 6).
type fetchResult[T any] struct {
	items []T
	err   error
}

// RunCycle executes one full reconciliation cycle. Rule failures are
// isolated: a rule whose source fetch or destination shadow-listing fails
// aborts (no writes, no GC for that pairing) while all others proceed.
func (e *Engine) RunCycle(ctx context.Context) {
	started := e.now()
	window := adapter.Window{
		Start: started.Add(-e.cfg.Lookback).UTC(),
		End:   started.Add(e.cfg.Lookahead).UTC(),
	}
	defer func() {
		e.cfg.Metrics.CycleDuration.Observe(e.now().Sub(started).Seconds())
	}()

	events := make(map[string]fetchResult[model.Event])
	shadows := make(map[string]fetchResult[model.Shadow])
	for _, rule := range e.cfg.Rules {
		if _, done := events[rule.From]; !done {
			items, err := e.cfg.Adapters[rule.From].ListEvents(ctx, window)
			events[rule.From] = fetchResult[model.Event]{items, err}
			e.countFetchError(rule.From, err)
		}
		for _, dest := range rule.To {
			if _, done := shadows[dest]; !done {
				items, err := e.cfg.Adapters[dest].ListShadows(ctx, window)
				shadows[dest] = fetchResult[model.Shadow]{items, err}
				e.countFetchError(dest, err)
			}
		}
	}

	for _, rule := range e.cfg.Rules {
		e.reconcileRule(ctx, rule, window, events[rule.From], shadows)
	}
	e.sweep(ctx, window)
}

// sweep is the account-wide auto-GC (decided 2026-08-08): discover every
// calendar of every account, delete own-instance shadows (windowed) whose
// rule does not target that calendar in the current config. Guardrails:
// only own-instance markers are deleted, and shadows of an active
// (rule → calendar) pairing are never touched here — their rule's own
// reconciliation governs them, including when its cycle aborted. Past
// (out-of-window) shadows remain as history; unbounded cleanup is
// `meridian wipe`.
func (e *Engine) sweep(ctx context.Context, window adapter.Window) {
	if len(e.cfg.Sweepers) == 0 {
		return
	}
	// logical calendar key → rule IDs targeting it
	targeted := map[string]map[string]bool{}
	for _, r := range e.cfg.Rules {
		for _, dest := range r.To {
			if targeted[dest] == nil {
				targeted[dest] = map[string]bool{}
			}
			targeted[dest][r.ID] = true
		}
	}
	for account, sweeper := range e.cfg.Sweepers {
		discovered, err := sweeper.Discover(ctx)
		if err != nil {
			e.cfg.Metrics.FetchErrorsTotal.WithLabelValues("sweep:"+account, errClass(err)).Inc()
			e.log.Error("sweep: calendar discovery failed", "account", account, "err", err)
			continue
		}
		for _, dc := range discovered {
			logical := e.cfg.CalendarKeys[account][dc.ProviderID]
			allowed := targeted[logical] // nil for unconfigured calendars: nothing allowed
			shadows, err := dc.Adapter.ListShadows(ctx, window)
			if err != nil {
				e.cfg.Metrics.FetchErrorsTotal.WithLabelValues("sweep:"+account, errClass(err)).Inc()
				e.log.Error("sweep: shadow listing failed", "account", account, "calendar", dc.ProviderID, "err", err)
				continue
			}
			var stale []model.Shadow
			own := 0
			for _, s := range shadows {
				if s.Marker.Instance != e.cfg.InstanceID {
					continue // guardrail: other instances are invisible
				}
				own++
				if allowed[s.Marker.Rule] {
					continue
				}
				if !window.Overlaps(s.Content.Start, s.Content.End) {
					continue // guard 4: never GC outside the window
				}
				stale = append(stale, s)
			}
			if len(stale) == 0 {
				continue
			}
			if frac := e.cfg.MassDeleteNotifyFraction; frac > 0 && float64(len(stale))/float64(own) >= frac {
				e.cfg.Metrics.GuardTriggersTotal.WithLabelValues("sweep", "mass_delete").Inc()
				msg := fmt.Sprintf("meridian: sweep is deleting %d of %d shadows on %s (%s) — stale rules; verify this config change was intended",
					len(stale), own, dc.ProviderID, account)
				e.log.Warn("guard: sweep mass-delete detection", "guard", "mass_delete",
					"account", account, "calendar", dc.ProviderID, "deletes", len(stale), "existing", own)
				if err := e.cfg.Notifier.Notify(ctx, msg); err != nil {
					e.cfg.Metrics.NotifyFailuresTotal.Inc()
					e.log.Error("notification delivery failed", "err", err)
				}
			}
			for _, s := range stale {
				if err := dc.Adapter.Delete(ctx, s.Ref); err != nil {
					e.cfg.Metrics.OpErrorsTotal.WithLabelValues(s.Marker.Rule, string(OpDelete), errClass(err)).Inc()
					e.log.Error("sweep: delete failed", "calendar", dc.ProviderID, "src", s.Marker.Src.String(), "err", err)
					continue
				}
				e.cfg.Metrics.OpsTotal.WithLabelValues(s.Marker.Rule, string(OpDelete)).Inc()
				e.log.Info("op", "op", string(OpDelete), "dest", dc.ProviderID,
					"src", s.Marker.Src.String(), "reason", "stale-rule", "hash", s.Marker.Hash)
			}
		}
	}
}

func (e *Engine) reconcileRule(ctx context.Context, rule Rule, window adapter.Window, src fetchResult[model.Event], shadows map[string]fetchResult[model.Shadow]) {
	log := e.log.With("rule", rule.ID)
	if src.err != nil {
		log.Error("source fetch failed, rule cycle aborted", "calendar", rule.From, "err", src.err)
		return
	}

	// Desired set. Zombie guard (guard 1, decided 2026-08-08: skip ALL
	// owned events): shadows are never syncable content.
	desired := make(map[model.EventRef]model.ShadowContent)
	for _, ev := range src.items {
		if ev.Marker != nil {
			e.cfg.Metrics.GuardTriggersTotal.WithLabelValues(rule.ID, "zombie").Inc()
			log.Warn("guard: skipping meridian-owned source event",
				"guard", "zombie", "src", ev.Ref.String(), "ownerRule", ev.Marker.Rule)
			continue
		}
		if rule.Filter != nil && !rule.Filter(ev) {
			continue
		}
		desired[ev.Ref] = rule.Transform(ev)
	}

	ruleOK := true
	for _, dest := range rule.To {
		destShadows := shadows[dest]
		if destShadows.err != nil {
			log.Error("shadow listing failed, destination skipped", "dest", dest, "err", destShadows.err)
			ruleOK = false
			continue
		}
		var mine []model.Shadow
		drifted := 0
		for _, s := range destShadows.items {
			// Instance+rule scoping: adapters already filter by instance,
			// but the engine re-checks — foreign-instance shadows must be
			// untouchable even with a misconfigured adapter.
			if s.Marker.Instance == e.cfg.InstanceID && s.Marker.Rule == rule.ID {
				mine = append(mine, s)
				// Drift detection, detect-only (mer-hn8): observed content
				// no longer matches what the marker says we wrote — manual
				// edit or provider normalization. Never repaired here
				// (repair on unstable normalization = infinite update loop).
				if model.ContentHash(s.Content) != s.Marker.Hash {
					drifted++
					log.Warn("shadow drift: observed content differs from marker hash",
						"dest", dest, "src", s.Marker.Src.String(),
						"markerHash", s.Marker.Hash, "observedHash", model.ContentHash(s.Content))
				}
			}
		}
		e.cfg.Metrics.ShadowDrift.WithLabelValues(rule.ID, dest).Set(float64(drifted))
		ops := diff(dest, e.cfg.InstanceID, rule.ID, desired, mine, window)
		e.detectMassDelete(ctx, rule.ID, dest, ops, len(mine), log)
		if !e.executeOps(ctx, rule.ID, dest, ops, log) {
			ruleOK = false
		}
	}
	if ruleOK {
		e.cfg.Metrics.LastSuccessfulCycle.WithLabelValues(rule.ID).Set(float64(e.now().Unix()))
	}
}

// executeOps applies planned ops. Per-op failures are logged and counted,
// never fatal to other ops — with one tombstone nicety (guard 5): NotFound
// on update/delete means the shadow is already gone; the next cycle
// recreates it if still desired.
func (e *Engine) executeOps(ctx context.Context, rule, dest string, ops []Op, log *slog.Logger) bool {
	ad := e.cfg.Adapters[dest]
	ok := true
	for _, op := range ops {
		var err error
		switch op.Kind {
		case OpCreate:
			err = ad.Create(ctx, op.Shadow)
		case OpUpdate:
			err = ad.Update(ctx, op.Shadow)
		case OpDelete:
			err = ad.Delete(ctx, op.Shadow.Ref)
		}
		if err != nil && op.Kind == OpUpdate && errors.Is(err, adapter.ErrNotFound) {
			log.Info("op target already gone, next cycle recreates",
				"op", string(op.Kind), "dest", dest, "src", op.Shadow.Marker.Src.String())
			err = nil
		}
		if err != nil {
			ok = false
			e.cfg.Metrics.OpErrorsTotal.WithLabelValues(rule, string(op.Kind), errClass(err)).Inc()
			log.Error("op failed", "op", string(op.Kind), "dest", dest,
				"src", op.Shadow.Marker.Src.String(), "reason", op.Reason, "err", err)
			continue
		}
		e.cfg.Metrics.OpsTotal.WithLabelValues(rule, string(op.Kind)).Inc()
		// The structured op record (Decision 6/1b): forensic + validation.
		log.Info("op",
			"op", string(op.Kind), "dest", dest,
			"src", op.Shadow.Marker.Src.String(),
			"reason", op.Reason, "hash", op.Shadow.Marker.Hash)
	}
	return ok
}

// detectMassDelete fires the detection (never blocks the deletes).
func (e *Engine) detectMassDelete(ctx context.Context, rule, dest string, ops []Op, existing int, log *slog.Logger) {
	frac := e.cfg.MassDeleteNotifyFraction
	if frac <= 0 || existing == 0 {
		return
	}
	orphans := 0
	for _, op := range ops {
		if op.Kind == OpDelete && op.Reason == "orphan" {
			orphans++
		}
	}
	if float64(orphans)/float64(existing) < frac {
		return
	}
	e.cfg.Metrics.GuardTriggersTotal.WithLabelValues(rule, "mass_delete").Inc()
	msg := fmt.Sprintf("meridian: rule %s is deleting %d of %d shadows on %s this cycle (threshold %.0f%%) — verify the source calendar is intact",
		rule, orphans, existing, dest, frac*100)
	log.Warn("guard: mass-delete detection", "guard", "mass_delete",
		"dest", dest, "deletes", orphans, "existing", existing)
	if err := e.cfg.Notifier.Notify(ctx, msg); err != nil {
		e.cfg.Metrics.NotifyFailuresTotal.Inc()
		log.Error("notification delivery failed", "err", err)
	}
}

func (e *Engine) countFetchError(calendar string, err error) {
	if err != nil {
		e.cfg.Metrics.FetchErrorsTotal.WithLabelValues(calendar, errClass(err)).Inc()
	}
}

func errClass(err error) string {
	switch {
	case errors.Is(err, adapter.ErrNotFound):
		return "not_found"
	case errors.Is(err, adapter.ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, adapter.ErrAuthFailed):
		return "auth_failed"
	default:
		return "transient"
	}
}
