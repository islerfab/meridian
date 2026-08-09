package sync

import (
	"context"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/islerfab/meridian/internal/adapter"
)

func TestProbeAuthAllHealthy(t *testing.T) {
	src, dst := newFake(), newFake()
	e := probeEngine(t, src, dst)
	if err := e.ProbeAuth(context.Background()); err != nil {
		t.Fatalf("ProbeAuth() = %v, want nil", err)
	}
}

func TestProbeAuthReportsEveryFailingCalendar(t *testing.T) {
	src, dst := newFake(), newFake()
	src.shadowsErr = adapter.ErrAuthFailed
	dst.shadowsErr = adapter.ErrAuthFailed
	e := probeEngine(t, src, dst)

	err := e.ProbeAuth(context.Background())
	if err == nil {
		t.Fatal("ProbeAuth() = nil, want error")
	}
	for _, cal := range []string{"a/src", "b/dst"} {
		if !strings.Contains(err.Error(), cal) {
			t.Errorf("error %q does not name failing calendar %s", err, cal)
		}
		got := testutil.ToFloat64(e.cfg.Metrics.FetchErrorsTotal.WithLabelValues(cal, "auth_failed"))
		if got != 1 {
			t.Errorf("fetch_errors_total{calendar=%s,class=auth_failed} = %v, want 1", cal, got)
		}
	}
}

func TestProbeAuthIsReadOnly(t *testing.T) {
	src, dst := newFake(), newFake()
	e := probeEngine(t, src, dst)
	if err := e.ProbeAuth(context.Background()); err != nil {
		t.Fatalf("ProbeAuth() = %v, want nil", err)
	}
	if n := dst.creates + dst.updates + dst.deletes + src.creates + src.updates + src.deletes; n != 0 {
		t.Fatalf("probe performed %d write ops, want 0", n)
	}
}

func probeEngine(t *testing.T, src, dst *fakeAdapter) *Engine {
	t.Helper()
	e, err := New(Config{
		InstanceID: "test",
		Rules: []Rule{{
			ID: "r1", From: "a/src", To: []string{"b/dst"},
			Transform: busyTransform,
		}},
		Adapters: map[string]adapter.CalendarAdapter{"a/src": src, "b/dst": dst},
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}
