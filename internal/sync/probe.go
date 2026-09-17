package sync

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/islerfab/meridian/internal/adapter"
)

// ProbeAuth verifies every configured calendar's credentials with one tiny
// windowed ListShadows call per adapter — read-only, so a probe can never
// write or delete anything. This is the readiness gate: the run loop starts
// cycles only after a full probe pass, so an instance that never became
// ready has provably touched no calendar. Failures count into
// fetch_errors_total; the returned error joins one entry per failing
// calendar.
func (e *Engine) ProbeAuth(ctx context.Context) error {
	now := e.now().UTC()
	window := adapter.Window{Start: now, End: now.Add(time.Minute)}
	keys := make([]string, 0, len(e.cfg.Adapters))
	for k := range e.cfg.Adapters {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var errs []error
	for _, key := range keys {
		if _, err := e.cfg.Adapters[key].ListShadows(ctx, window); err != nil {
			e.cfg.Metrics.FetchErrorsTotal.WithLabelValues(key, errClass(err)).Inc()
			e.log.Error("auth probe failed", "calendar", key, "err", err)
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
		}
	}
	return errors.Join(errs...)
}
