package sync

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/islerfab/meridian/internal/adapter"
	"github.com/islerfab/meridian/internal/model"
)

// --- fake adapter ----------------------------------------------------------

type fakeAdapter struct {
	events     []model.Event
	shadows    map[string]model.Shadow
	nextID     int
	eventsErr  error
	shadowsErr error

	captureWindow    bool
	lastShadowWindow adapter.Window

	creates, updates, deletes int
}

func newFake() *fakeAdapter {
	return &fakeAdapter{shadows: map[string]model.Shadow{}}
}

func (f *fakeAdapter) ListEvents(_ context.Context, _ adapter.Window) ([]model.Event, error) {
	if f.eventsErr != nil {
		return nil, f.eventsErr
	}
	return f.events, nil
}

func (f *fakeAdapter) ListShadows(_ context.Context, w adapter.Window) ([]model.Shadow, error) {
	if f.captureWindow {
		f.lastShadowWindow = w
	}
	if f.shadowsErr != nil {
		return nil, f.shadowsErr
	}
	out := make([]model.Shadow, 0, len(f.shadows))
	for _, s := range f.shadows {
		out = append(out, s)
	}
	return out, nil
}

func (f *fakeAdapter) Create(_ context.Context, s model.Shadow) error {
	f.creates++
	f.nextID++
	s.Ref = model.ShadowRef{Calendar: "fake", ID: fmt.Sprintf("id-%d", f.nextID)}
	f.shadows[s.Ref.ID] = s
	return nil
}

func (f *fakeAdapter) Update(_ context.Context, s model.Shadow) error {
	f.updates++
	if _, ok := f.shadows[s.Ref.ID]; !ok {
		return fmt.Errorf("update %s: %w", s.Ref.ID, adapter.ErrNotFound)
	}
	f.shadows[s.Ref.ID] = s
	return nil
}

func (f *fakeAdapter) Delete(_ context.Context, ref model.ShadowRef) error {
	f.deletes++
	delete(f.shadows, ref.ID) // idempotent like the real ones
	return nil
}

// --- helpers ---------------------------------------------------------------

var t0 = time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)

func srcEvent(uid, title string, start time.Time) model.Event {
	return model.Event{
		Ref:   model.EventRef{Calendar: "src", UID: uid},
		Title: title,
		Start: start,
		End:   start.Add(time.Hour),
	}
}

func passAll(model.Event) bool { return true }

func busyTransform(ev model.Event) model.ShadowContent {
	return model.ShadowContent{Title: "Busy", Start: ev.Start, End: ev.End}
}

type capturingNotifier struct{ messages []string }

func (c *capturingNotifier) Notify(_ context.Context, msg string) error {
	c.messages = append(c.messages, msg)
	return nil
}

func newTestEngine(t *testing.T, src, dst *fakeAdapter, mutate func(*Config)) *Engine {
	t.Helper()
	cfg := Config{
		InstanceID: "inst-test",
		Rules: []Rule{{
			ID: "r1", From: "src", To: []string{"dst"},
			Filter: passAll, Transform: busyTransform,
		}},
		Adapters: map[string]adapter.CalendarAdapter{"src": src, "dst": dst},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.now = func() time.Time { return t0 }
	return e
}

// --- core reconciliation ---------------------------------------------------

func TestConvergeAndIdempotency(t *testing.T) {
	src, dst := newFake(), newFake()
	src.events = []model.Event{
		srcEvent("a", "Standup", t0.Add(24*time.Hour)),
		srcEvent("b", "1:1", t0.Add(48*time.Hour)),
	}
	e := newTestEngine(t, src, dst, nil)

	e.RunCycle(context.Background())
	if dst.creates != 2 || len(dst.shadows) != 2 {
		t.Fatalf("creates=%d shadows=%d, want 2/2", dst.creates, len(dst.shadows))
	}
	for _, s := range dst.shadows {
		if s.Content.Title != "Busy" {
			t.Errorf("transform not applied: %+v", s.Content)
		}
		if s.Marker.Rule != "r1" || s.Marker.Hash != model.ContentHash(s.Content) {
			t.Errorf("bad marker: %+v", s.Marker)
		}
	}

	// Converged: a second cycle must be a zero-op cycle.
	e.RunCycle(context.Background())
	if dst.creates != 2 || dst.updates != 0 || dst.deletes != 0 {
		t.Errorf("second cycle not idempotent: c=%d u=%d d=%d", dst.creates, dst.updates, dst.deletes)
	}
}

// Guard 3: change detection is hash-vs-marker only, not hash-vs-observed.
// Provider-side content normalization (title mangled on the destination)
// is drift, and is healed via the bounded repair path (mer-t75) — the
// repair op carries reason="drift-repair", never diff()'s own
// "content-changed" reason. One repair attempt heals it; without fresh
// tampering the next cycle is quiet.
func TestHashChangeDetectionIgnoresProviderFields(t *testing.T) {
	src, dst := newFake(), newFake()
	src.events = []model.Event{srcEvent("a", "Standup", t0.Add(24*time.Hour))}
	e := newTestEngine(t, src, dst, nil)
	e.RunCycle(context.Background())

	// Provider "normalizes" the stored title; marker hash untouched.
	for id, s := range dst.shadows {
		s.Content.Title = "BUSY (normalized by provider)"
		dst.shadows[id] = s
	}
	e.RunCycle(context.Background())
	if dst.updates != 1 {
		t.Fatalf("updates=%d, want 1 (drift repair, not diff()'s content-changed path)", dst.updates)
	}
	for _, s := range dst.shadows {
		if s.Content.Title != "Busy" {
			t.Errorf("repair did not restore desired content: %+v", s.Content)
		}
	}

	// No fresh tampering: the repair already holds, cycle is quiet.
	e.RunCycle(context.Background())
	if dst.updates != 1 {
		t.Errorf("updates=%d, want still 1 (repair held, no further action)", dst.updates)
	}

	// A real source change must still update.
	src.events[0].Start = src.events[0].Start.Add(30 * time.Minute)
	src.events[0].End = src.events[0].End.Add(30 * time.Minute)
	e.RunCycle(context.Background())
	if dst.updates != 2 {
		t.Errorf("updates=%d, want 2 after real content change", dst.updates)
	}
}

// Drift detection + bounded repair (mer-hn8, extended mer-t75): a
// hand-tampered shadow is repaired from the already-desired content, up to
// maxRepairTries times. If it's STILL drifted after the budget is
// exhausted, repair stops and the drift becomes visible via the
// unrepairable metric instead of retrying forever (the mer-jge DST-fold
// class of bug proved unconditional retry unsafe). A real source change
// bypasses the budget entirely — it's diff()'s content-changed path, which
// always builds a fresh marker with RepairTries reset to 0.
func TestShadowDriftBoundedRepair(t *testing.T) {
	src, dst := newFake(), newFake()
	src.events = []model.Event{srcEvent("a", "Standup", t0.Add(24*time.Hour))}
	e := newTestEngine(t, src, dst, nil)
	e.RunCycle(context.Background())

	driftGauge := func() float64 {
		return testutil.ToFloat64(e.cfg.Metrics.ShadowDrift.WithLabelValues("r1", "dst"))
	}
	unrepairableGauge := func() float64 {
		return testutil.ToFloat64(e.cfg.Metrics.ShadowDriftUnrepairable.WithLabelValues("r1", "dst"))
	}
	tamper := func() {
		for id, s := range dst.shadows {
			s.Content.Title = "tampered by hand"
			dst.shadows[id] = s
		}
	}

	if g := driftGauge(); g != 0 {
		t.Fatalf("drift=%v after clean sync, want 0", g)
	}

	// Attempt 1 of maxRepairTries(=2): repaired.
	tamper()
	e.RunCycle(context.Background())
	if dst.updates != 1 {
		t.Fatalf("updates=%d, want 1 (first drift repaired)", dst.updates)
	}
	if g := driftGauge(); g != 1 {
		t.Errorf("drift=%v at repair time, want 1", g)
	}
	if g := unrepairableGauge(); g != 0 {
		t.Errorf("unrepairable=%v, want 0 (still within retry budget)", g)
	}
	for _, s := range dst.shadows {
		if s.Marker.RepairTries != 1 {
			t.Errorf("RepairTries=%d, want 1 after first repair attempt", s.Marker.RepairTries)
		}
	}

	// Simulate the repair not holding (e.g. a stable provider
	// normalization, like mer-jge) by tampering again before re-listing.
	tamper()
	e.RunCycle(context.Background()) // attempt 2 of 2
	if dst.updates != 2 {
		t.Fatalf("updates=%d, want 2 (second attempt within budget)", dst.updates)
	}

	tamper()
	e.RunCycle(context.Background()) // budget exhausted: no further repair
	if dst.updates != 2 {
		t.Errorf("updates=%d, want still 2 (retries exhausted, must not repair again)", dst.updates)
	}
	if g := unrepairableGauge(); g != 1 {
		t.Errorf("unrepairable=%v, want 1 after exhausting retries", g)
	}

	// A real source change bypasses the repair budget entirely.
	src.events[0].Start = src.events[0].Start.Add(time.Hour)
	src.events[0].End = src.events[0].End.Add(time.Hour)
	e.RunCycle(context.Background())
	if g := driftGauge(); g != 1 {
		t.Errorf("drift=%v during overwrite cycle (pre-op listing), want 1", g)
	}
	e.RunCycle(context.Background())
	if g := driftGauge(); g != 0 {
		t.Errorf("drift=%v after overwrite converged, want 0", g)
	}
	if g := unrepairableGauge(); g != 0 {
		t.Errorf("unrepairable=%v after real change converged, want 0", g)
	}
	for _, s := range dst.shadows {
		if s.Marker.RepairTries != 0 {
			t.Errorf("RepairTries=%d, want reset to 0 after a real content change", s.Marker.RepairTries)
		}
	}
}

// Guard 1 (as decided 2026-08-08): ALL meridian-owned source events are
// skipped — a shadow can never be syncable content, killing the zombie-
// resurrection class outright.
func TestZombieGuardSkipsOwnedSourceEvents(t *testing.T) {
	src, dst := newFake(), newFake()
	owned := srcEvent("zombie", "Busy", t0.Add(24*time.Hour))
	m := model.NewMarker("inst-test", model.EventRef{Calendar: "dst", UID: "orig"}, "other-rule", model.ShadowContent{})
	owned.Marker = &m
	src.events = []model.Event{owned, srcEvent("real", "Standup", t0.Add(24*time.Hour))}

	e := newTestEngine(t, src, dst, nil)
	e.RunCycle(context.Background())
	if len(dst.shadows) != 1 {
		t.Fatalf("shadows=%d, want 1 (owned source event must be skipped)", len(dst.shadows))
	}
	for _, s := range dst.shadows {
		if s.Marker.Src.UID != "real" {
			t.Errorf("wrong event mirrored: %+v", s.Marker.Src)
		}
	}
}

// Guard 2 (superseded semantics, decided 2026-08-08): an empty-but-
// successful source fetch EMPTIES the destination — faithful mirroring.
// Detection fires (metric + notification), deletes are never blocked.
func TestEmptySourceEmptiesDestinationWithNotification(t *testing.T) {
	src, dst := newFake(), newFake()
	src.events = []model.Event{
		srcEvent("a", "x", t0.Add(24*time.Hour)),
		srcEvent("b", "y", t0.Add(48*time.Hour)),
	}
	notifier := &capturingNotifier{}
	e := newTestEngine(t, src, dst, func(c *Config) {
		c.MassDeleteNotifyFraction = 0.5
		c.Notifier = notifier
	})
	e.RunCycle(context.Background())
	if len(dst.shadows) != 2 {
		t.Fatal("setup failed")
	}

	src.events = nil // source emptied (legitimately or not — indistinguishable)
	e.RunCycle(context.Background())
	if len(dst.shadows) != 0 {
		t.Errorf("shadows=%d, want 0 — empty source must empty the sink", len(dst.shadows))
	}
	if len(notifier.messages) != 1 {
		t.Fatalf("notifications=%d, want 1", len(notifier.messages))
	}
	if !strings.Contains(notifier.messages[0], "2 of 2") {
		t.Errorf("notification lacks context: %q", notifier.messages[0])
	}
}

func TestMassDeleteBelowThresholdIsSilent(t *testing.T) {
	src, dst := newFake(), newFake()
	src.events = []model.Event{
		srcEvent("a", "x", t0.Add(24*time.Hour)),
		srcEvent("b", "y", t0.Add(48*time.Hour)),
		srcEvent("c", "z", t0.Add(72*time.Hour)),
	}
	notifier := &capturingNotifier{}
	e := newTestEngine(t, src, dst, func(c *Config) {
		c.MassDeleteNotifyFraction = 0.5
		c.Notifier = notifier
	})
	e.RunCycle(context.Background())

	src.events = src.events[:2] // delete 1 of 3: 33% < 50%
	e.RunCycle(context.Background())
	if len(dst.shadows) != 2 {
		t.Errorf("shadows=%d, want 2", len(dst.shadows))
	}
	if len(notifier.messages) != 0 {
		t.Errorf("below-threshold delete must not notify: %v", notifier.messages)
	}
}

// Guard 4: orphan GC never touches shadows outside the window.
func TestWindowedGCLeavesOutOfWindowShadows(t *testing.T) {
	src, dst := newFake(), newFake()
	// A stale shadow way outside the window (e.g. left over from an old
	// window position), and no matching source event.
	old := model.ShadowContent{Title: "Busy", Start: t0.Add(-100 * 24 * time.Hour), End: t0.Add(-100 * 24 * time.Hour).Add(time.Hour)}
	dst.shadows["stale"] = model.Shadow{
		Ref:     model.ShadowRef{Calendar: "dst", ID: "stale"},
		Content: old,
		Marker:  model.NewMarker("inst-test", model.EventRef{Calendar: "src", UID: "gone"}, "r1", old),
	}
	inWindow := model.ShadowContent{Title: "Busy", Start: t0.Add(24 * time.Hour), End: t0.Add(25 * time.Hour)}
	dst.shadows["orphan"] = model.Shadow{
		Ref:     model.ShadowRef{Calendar: "dst", ID: "orphan"},
		Content: inWindow,
		Marker:  model.NewMarker("inst-test", model.EventRef{Calendar: "src", UID: "also-gone"}, "r1", inWindow),
	}
	src.events = []model.Event{srcEvent("keep", "x", t0.Add(24*time.Hour))}

	e := newTestEngine(t, src, dst, nil)
	e.RunCycle(context.Background())
	if _, ok := dst.shadows["stale"]; !ok {
		t.Error("out-of-window shadow must never be GC'd")
	}
	if _, ok := dst.shadows["orphan"]; ok {
		t.Error("in-window orphan must be GC'd")
	}
}

// Guard 5: a shadow deleted out from under us mid-cycle (tombstone) must
// not fail the rule; the next cycle recreates.
func TestTombstoneToleranceOnUpdate(t *testing.T) {
	src, dst := newFake(), newFake()
	src.events = []model.Event{srcEvent("a", "x", t0.Add(24*time.Hour))}
	e := newTestEngine(t, src, dst, nil)
	e.RunCycle(context.Background())

	// Change source, and sabotage: remove the shadow so Update 404s.
	src.events[0].Start = src.events[0].Start.Add(time.Hour)
	src.events[0].End = src.events[0].End.Add(time.Hour)
	for id := range dst.shadows {
		delete(dst.shadows, id)
	}
	e.RunCycle(context.Background()) // update hits NotFound → tolerated
	// Next cycle: recreated.
	e.RunCycle(context.Background())
	if len(dst.shadows) != 1 {
		t.Errorf("shadows=%d, want 1 (recreated after tombstoned update)", len(dst.shadows))
	}
}

// Decision 6: per-rule isolation — one rule's source failure must not
// affect another rule, and must cause no writes/GC for the failed rule.
func TestPerRuleFailureIsolation(t *testing.T) {
	srcA, srcB, dst := newFake(), newFake(), newFake()
	srcA.eventsErr = fmt.Errorf("boom: %w", adapter.ErrTransient)
	srcB.events = []model.Event{srcEvent("b1", "y", t0.Add(24*time.Hour))}

	// Rule A already has a shadow that would be orphaned if its (failed)
	// empty fetch were trusted.
	c := model.ShadowContent{Title: "Busy", Start: t0.Add(24 * time.Hour), End: t0.Add(25 * time.Hour)}
	dst.shadows["a-shadow"] = model.Shadow{
		Ref:     model.ShadowRef{Calendar: "dst", ID: "a-shadow"},
		Content: c,
		Marker:  model.NewMarker("inst-test", model.EventRef{Calendar: "srcA", UID: "a1"}, "ruleA", c),
	}

	cfg := Config{
		InstanceID: "inst-test",
		Rules: []Rule{
			{ID: "ruleA", From: "srcA", To: []string{"dst"}, Filter: passAll, Transform: busyTransform},
			{ID: "ruleB", From: "srcB", To: []string{"dst"}, Filter: passAll, Transform: busyTransform},
		},
		Adapters: map[string]adapter.CalendarAdapter{"srcA": srcA, "srcB": srcB, "dst": dst},
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.now = func() time.Time { return t0 }
	e.RunCycle(context.Background())

	if _, ok := dst.shadows["a-shadow"]; !ok {
		t.Error("failed fetch must abort rule A with no GC (its shadow was deleted)")
	}
	found := false
	for _, s := range dst.shadows {
		if s.Marker.Rule == "ruleB" {
			found = true
		}
	}
	if !found {
		t.Error("rule B must proceed despite rule A's failure")
	}
}

// Rules never touch each other's shadows; unknown-rule shadows are left
// alone entirely (mer-uks).
func TestRuleScopingAndUnknownRuleShadows(t *testing.T) {
	src, dst := newFake(), newFake()
	src.events = nil // r1 desires nothing
	c := model.ShadowContent{Title: "Busy", Start: t0.Add(24 * time.Hour), End: t0.Add(25 * time.Hour)}
	dst.shadows["foreign-rule"] = model.Shadow{
		Ref:     model.ShadowRef{Calendar: "dst", ID: "foreign-rule"},
		Content: c,
		Marker:  model.NewMarker("inst-test", model.EventRef{Calendar: "src", UID: "x"}, "removed-rule", c),
	}
	e := newTestEngine(t, src, dst, nil)
	e.RunCycle(context.Background())
	if _, ok := dst.shadows["foreign-rule"]; !ok {
		t.Error("another rule's shadow must never be GC'd by r1")
	}
}

// Duplicates (decided 2026-08-08): keep the hash-matching copy, delete the
// extras, one cycle restores the one-shadow-per-src invariant.
func TestDuplicateShadowsDeduped(t *testing.T) {
	src, dst := newFake(), newFake()
	ev := srcEvent("a", "x", t0.Add(24*time.Hour))
	src.events = []model.Event{ev}
	want := busyTransform(ev)
	srcRef := ev.Ref

	stale := model.ShadowContent{Title: "Busy", Start: ev.Start.Add(time.Hour), End: ev.End.Add(time.Hour)}
	dst.shadows["dup-stale"] = model.Shadow{
		Ref:     model.ShadowRef{Calendar: "dst", ID: "dup-stale"},
		Content: stale,
		Marker:  model.NewMarker("inst-test", srcRef, "r1", stale),
	}
	dst.shadows["dup-good"] = model.Shadow{
		Ref:     model.ShadowRef{Calendar: "dst", ID: "dup-good"},
		Content: want,
		Marker:  model.NewMarker("inst-test", srcRef, "r1", want),
	}

	e := newTestEngine(t, src, dst, nil)
	e.RunCycle(context.Background())
	if len(dst.shadows) != 1 {
		t.Fatalf("shadows=%d, want 1 after dedupe", len(dst.shadows))
	}
	if _, ok := dst.shadows["dup-good"]; !ok {
		t.Error("hash-matching duplicate must be the keeper")
	}
	if dst.updates != 0 {
		t.Error("keeper matches desired hash: no update expected")
	}
}

func TestNewValidatesWiring(t *testing.T) {
	ad := map[string]adapter.CalendarAdapter{"a": newFake(), "b": newFake()}
	cases := []Rule{
		{ID: "", From: "a", To: []string{"b"}},
		{ID: "x", From: "missing", To: []string{"b"}},
		{ID: "x", From: "a", To: nil},
		{ID: "x", From: "a", To: []string{"missing"}},
		{ID: "x", From: "a", To: []string{"a"}}, // self-sync
	}
	for i, r := range cases {
		if _, err := New(Config{InstanceID: "inst-test", Rules: []Rule{r}, Adapters: ad}); err == nil {
			t.Errorf("case %d (%+v): expected wiring error", i, r)
		}
	}
}
