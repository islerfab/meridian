package sync

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/islerfab/meridian/internal/adapter"
	"github.com/islerfab/meridian/internal/model"
)

// WipeTarget selects what to delete from one calendar: every shadow owned
// by the given instance, optionally narrowed to one rule ID. Backs the
// `meridian wipe calendar|rule` commands (decided 2026-08-08, mer-uks) —
// explicit operator intent replaces automatic rule-removal GC.
type WipeTarget struct {
	InstanceID string
	Rule       string // "" = all rules of the instance
}

// WipePlan lists own-instance shadows matching the target. The listing is
// UNBOUNDED (zero Window = no time filter) — wipe must catch strays outside
// the sync window too.
func WipePlan(ctx context.Context, ad adapter.CalendarAdapter, target WipeTarget) ([]model.Shadow, error) {
	if target.InstanceID == "" {
		return nil, fmt.Errorf("wipe: instance ID is required")
	}
	shadows, err := ad.ListShadows(ctx, adapter.Window{})
	if err != nil {
		return nil, fmt.Errorf("wipe: %w", err)
	}
	var matched []model.Shadow
	for _, s := range shadows {
		if s.Marker.Instance != target.InstanceID {
			continue
		}
		if target.Rule != "" && s.Marker.Rule != target.Rule {
			continue
		}
		matched = append(matched, s)
	}
	return matched, nil
}

// Wipe deletes the planned shadows. Idempotent per shadow (already-gone is
// success at the adapter layer); returns the number deleted and the first
// error alongside how far it got.
func Wipe(ctx context.Context, ad adapter.CalendarAdapter, shadows []model.Shadow, log *slog.Logger) (int, error) {
	if log == nil {
		log = slog.Default()
	}
	deleted := 0
	for _, s := range shadows {
		if err := ad.Delete(ctx, s.Ref); err != nil {
			return deleted, fmt.Errorf("wipe: delete %s: %w", s.Ref.ID, err)
		}
		deleted++
		log.Info("op", "op", "delete", "dest", s.Ref.Calendar,
			"src", s.Marker.Src.String(), "reason", "wipe", "hash", s.Marker.Hash)
	}
	return deleted, nil
}
