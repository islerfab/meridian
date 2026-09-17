package sync

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/islerfab/meridian/internal/adapter"
	"github.com/islerfab/meridian/internal/model"
)

func seedShadow(dst *fakeAdapter, id, instance, rule, uid string, start time.Time) {
	c := model.ShadowContent{Title: "Busy", Start: start, End: start.Add(time.Hour)}
	dst.shadows[id] = model.Shadow{
		Ref:     model.ShadowRef{Calendar: "dst", ID: id},
		Content: c,
		Marker:  model.NewMarker(instance, model.EventRef{Calendar: "src", UID: uid}, rule, c),
	}
}

// Multi-instance safety: same rule ID, different instance — completely
// invisible to this instance's reconciliation.
func TestForeignInstanceShadowsUntouched(t *testing.T) {
	src, dst := newFake(), newFake()
	src.events = nil // r1 desires nothing: everything of r1's would be GC'd
	seedShadow(dst, "foreign", "other-instance", "r1", "x", t0.Add(24*time.Hour))

	e := newTestEngine(t, src, dst, nil)
	e.RunCycle(context.Background())
	if _, ok := dst.shadows["foreign"]; !ok {
		t.Error("another instance's shadow (same rule ID) must never be touched")
	}
}

// fakeSweeper exposes fixed provider calendars for the sweep.
type fakeSweeper struct {
	calendars   map[string]*fakeAdapter // providerID → adapter
	discoverErr error
}

func (f *fakeSweeper) Discover(_ context.Context) ([]adapter.DiscoveredCalendar, error) {
	if f.discoverErr != nil {
		return nil, f.discoverErr
	}
	var out []adapter.DiscoveredCalendar
	for id, ad := range f.calendars {
		out = append(out, adapter.DiscoveredCalendar{ProviderID: id, Adapter: ad})
	}
	return out, nil
}

// Sweep auto-GC: own-instance shadows whose rule does not target their
// calendar are DELETED; active pairings and foreign instances are untouched
// — on configured and unconfigured calendars alike.
func TestSweepDeletesStaleShadows(t *testing.T) {
	src, dst := newFake(), newFake()
	src.events = []model.Event{srcEvent("keep", "x", t0.Add(24*time.Hour))}
	// dst is the configured destination of r1 (provider id "dst-prov").
	seedShadow(dst, "stale-rule", "inst-test", "removed-rule", "a", t0.Add(24*time.Hour))
	seedShadow(dst, "foreign", "other-instance", "removed-rule", "b", t0.Add(24*time.Hour))
	// An unconfigured calendar of the same account holds strays.
	other := newFake()
	seedShadow(other, "stray", "inst-test", "r1", "c", t0.Add(24*time.Hour))
	seedShadow(other, "their-stray", "other-instance", "r1", "d", t0.Add(24*time.Hour))
	// Out-of-window stray must survive (windowed sweep: history is kept).
	seedShadow(other, "old-stray", "inst-test", "gone", "e", t0.Add(-100*24*time.Hour))

	notifier := &capturingNotifier{}
	e := newTestEngine(t, src, dst, func(c *Config) {
		// 1 stale of 2 own per calendar = 50%; threshold is strictly
		// exceeded at 0.4.
		c.MassDeleteNotifyFraction = 0.4
		c.Notifier = notifier
		c.Sweepers = map[string]adapter.AccountSweeper{
			"acct": &fakeSweeper{calendars: map[string]*fakeAdapter{
				"dst-prov":   dst,
				"other-prov": other,
			}},
		}
		c.CalendarKeys = map[string]map[string]string{"acct": {"dst-prov": "dst"}}
	})
	e.RunCycle(context.Background())

	if _, ok := dst.shadows["stale-rule"]; ok {
		t.Error("stale-rule shadow on configured calendar must be swept")
	}
	if _, ok := dst.shadows["foreign"]; !ok {
		t.Error("foreign-instance shadow must survive the sweep")
	}
	// r1's own live shadow (created this cycle) must survive.
	live := 0
	for _, s := range dst.shadows {
		if s.Marker.Rule == "r1" && s.Marker.Instance == "inst-test" {
			live++
		}
	}
	if live != 1 {
		t.Errorf("live r1 shadows = %d, want 1", live)
	}
	// Unconfigured calendar: r1 does not target it → its r1-marked stray dies.
	if _, ok := other.shadows["stray"]; ok {
		t.Error("own-instance stray on unconfigured calendar must be swept")
	}
	if _, ok := other.shadows["their-stray"]; !ok {
		t.Error("foreign stray must survive")
	}
	if _, ok := other.shadows["old-stray"]; !ok {
		t.Error("out-of-window stray must survive (history)")
	}
	if len(notifier.messages) == 0 {
		t.Error("sweep above threshold must notify")
	}
}

// A rule whose source fetch failed still protects its shadows from the
// sweep — the (rule → calendar) pairing is active regardless of cycle
// health.
func TestSweepSparesAbortedRulesShadows(t *testing.T) {
	src, dst := newFake(), newFake()
	src.eventsErr = fmt.Errorf("boom: %w", adapter.ErrTransient)
	seedShadow(dst, "r1-shadow", "inst-test", "r1", "a", t0.Add(24*time.Hour))

	e := newTestEngine(t, src, dst, func(c *Config) {
		c.Sweepers = map[string]adapter.AccountSweeper{
			"acct": &fakeSweeper{calendars: map[string]*fakeAdapter{"dst-prov": dst}},
		}
		c.CalendarKeys = map[string]map[string]string{"acct": {"dst-prov": "dst"}}
	})
	e.RunCycle(context.Background())
	if _, ok := dst.shadows["r1-shadow"]; !ok {
		t.Error("aborted rule's shadow must never be swept (rule still targets the calendar)")
	}
}

func TestWipePlanFiltersInstanceAndRule(t *testing.T) {
	dst := newFake()
	seedShadow(dst, "mine-r1", "inst-test", "r1", "a", t0)
	seedShadow(dst, "mine-r2", "inst-test", "r2", "b", t0)
	// Out-of-window stray: wipe must see it (unbounded listing).
	seedShadow(dst, "mine-r1-old", "inst-test", "r1", "c", t0.Add(-365*24*time.Hour))
	seedShadow(dst, "foreign", "other-instance", "r1", "d", t0)

	all, err := WipePlan(context.Background(), dst, WipeTarget{InstanceID: "inst-test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("instance-wide plan: %d shadows, want 3 (incl. out-of-window)", len(all))
	}

	r1, err := WipePlan(context.Background(), dst, WipeTarget{InstanceID: "inst-test", Rule: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r1) != 2 {
		t.Errorf("rule plan: %d shadows, want 2", len(r1))
	}

	if _, err := WipePlan(context.Background(), dst, WipeTarget{}); err == nil {
		t.Error("missing instance ID must error")
	}
}

func TestWipeDeletesPlannedOnly(t *testing.T) {
	dst := newFake()
	seedShadow(dst, "mine-r1", "inst-test", "r1", "a", t0)
	seedShadow(dst, "mine-r2", "inst-test", "r2", "b", t0)
	seedShadow(dst, "foreign", "other-instance", "r1", "d", t0)

	plan, err := WipePlan(context.Background(), dst, WipeTarget{InstanceID: "inst-test", Rule: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	n, err := Wipe(context.Background(), dst, plan, nil)
	if err != nil || n != 1 {
		t.Fatalf("Wipe = (%d, %v), want (1, nil)", n, err)
	}
	if _, ok := dst.shadows["mine-r1"]; ok {
		t.Error("planned shadow not deleted")
	}
	if _, ok := dst.shadows["mine-r2"]; !ok {
		t.Error("other rule's shadow deleted")
	}
	if _, ok := dst.shadows["foreign"]; !ok {
		t.Error("foreign instance's shadow deleted")
	}
}

// The fake ignores windows, so assert the contract at the type level: wipe
// passes the zero (unbounded) window.
func TestWipePlanUsesUnboundedWindow(t *testing.T) {
	dst := newFake()
	dst.captureWindow = true
	if _, err := WipePlan(context.Background(), dst, WipeTarget{InstanceID: "inst-test"}); err != nil {
		t.Fatal(err)
	}
	if !dst.lastShadowWindow.IsZero() {
		t.Errorf("wipe must list with the unbounded window, got %+v", dst.lastShadowWindow)
	}
}

var _ = adapter.Window{} // keep import if assertions change
