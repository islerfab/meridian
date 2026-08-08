package adapter

import (
	"context"
	"time"

	"github.com/islerfab/meridian/internal/model"
)

// Window is the sync window; membership is overlap, applied identically to
// source fetches, desired-set computation, and orphan GC (DESIGN.md
// Decision 3). The zero Window means UNBOUNDED — no time filter at all —
// used only by wipe (ListShadows); ListEvents requires real bounds
// (server-side expansion needs them).
type Window struct {
	Start time.Time
	End   time.Time
}

// IsZero reports the unbounded window.
func (w Window) IsZero() bool { return w.Start.IsZero() && w.End.IsZero() }

// Overlaps reports whether [start, end) overlaps the window. Zero-length
// events overlap when their instant lies inside the window.
func (w Window) Overlaps(start, end time.Time) bool {
	if end.After(start) {
		return start.Before(w.End) && end.After(w.Start)
	}
	return !start.Before(w.Start) && start.Before(w.End)
}

// CalendarAdapter is the single five-method contract both providers
// implement (DESIGN.md Decision 5). No capability flags: protocol asymmetry
// stays inside each adapter, and all errors are normalized to the taxonomy
// in errors.go.
type CalendarAdapter interface {
	// ListEvents returns expanded, discrete event instances overlapping the
	// window (source side). Instances of meridian-owned events carry a
	// non-nil Marker.
	ListEvents(ctx context.Context, window Window) ([]model.Event, error)
	// ListShadows returns the meridian-owned shadows overlapping the window
	// (destination side). Foreign events are never returned.
	ListShadows(ctx context.Context, window Window) ([]model.Shadow, error)
	// Create writes a new shadow (content + marker in one write).
	Create(ctx context.Context, shadow model.Shadow) error
	// Update fully replaces a shadow's content + marker in one write.
	Update(ctx context.Context, shadow model.Shadow) error
	// Delete removes a shadow. Idempotent: already-gone (404/410) is success.
	Delete(ctx context.Context, ref model.ShadowRef) error
}
