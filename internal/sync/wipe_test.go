package sync

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

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
// invisible to this instance's reconciliation (mer-uks).
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

// Drift detection: own-instance shadows whose rule no longer targets their
// calendar are reported (gauge + warning), never deleted.
func TestDriftDetectionReportsStaleShadows(t *testing.T) {
	src, dst := newFake(), newFake()
	seedShadow(dst, "stale-1", "inst-test", "removed-rule", "a", t0.Add(24*time.Hour))
	seedShadow(dst, "stale-2", "inst-test", "removed-rule", "b", t0.Add(48*time.Hour))
	seedShadow(dst, "foreign", "other-instance", "their-rule", "c", t0.Add(24*time.Hour))

	var metrics *Metrics
	e := newTestEngine(t, src, dst, func(c *Config) {
		metrics = NewMetrics(nil)
		c.Metrics = metrics
	})
	e.RunCycle(context.Background())

	if len(dst.shadows) != 3 {
		t.Errorf("drift detection must not delete anything, shadows=%d", len(dst.shadows))
	}
	if got := testutil.ToFloat64(metrics.StaleShadows.WithLabelValues("dst", "removed-rule")); got != 2 {
		t.Errorf("stale_shadows{dst,removed-rule} = %v, want 2", got)
	}
	// Foreign-instance shadows are not ours to report on.
	if got := testutil.ToFloat64(metrics.StaleShadows.WithLabelValues("dst", "their-rule")); got != 0 {
		t.Errorf("stale_shadows{dst,their-rule} = %v, want 0", got)
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
