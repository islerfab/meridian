package sync

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics is the engine's Prometheus surface (DESIGN.md Decision 6). Guard
// triggers must be loud — silent guards defeat their purpose.
type Metrics struct {
	OpsTotal            *prometheus.CounterVec // rule, op
	OpErrorsTotal       *prometheus.CounterVec // rule, op, class
	FetchErrorsTotal    *prometheus.CounterVec // calendar, class
	GuardTriggersTotal  *prometheus.CounterVec // rule, guard
	CycleDuration       prometheus.Histogram
	LastSuccessfulCycle *prometheus.GaugeVec // rule
	NotifyFailuresTotal prometheus.Counter
}

// NewMetrics builds and registers the engine collectors.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		OpsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "meridian_ops_total",
			Help: "Reconciliation operations executed, by rule and op kind.",
		}, []string{"rule", "op"}),
		OpErrorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "meridian_op_errors_total",
			Help: "Failed reconciliation operations, by rule, op kind, and error class.",
		}, []string{"rule", "op", "class"}),
		FetchErrorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "meridian_fetch_errors_total",
			Help: "Failed calendar window fetches, by calendar and error class.",
		}, []string{"calendar", "class"}),
		GuardTriggersTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "meridian_guard_triggers_total",
			Help: "Hardening guard triggers, by rule and guard.",
		}, []string{"rule", "guard"}),
		CycleDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "meridian_cycle_duration_seconds",
			Help:    "Full reconciliation cycle duration.",
			Buckets: prometheus.DefBuckets,
		}),
		LastSuccessfulCycle: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "meridian_last_successful_cycle_timestamp_seconds",
			Help: "Unix time of the last fully successful cycle per rule (the alerting primitive).",
		}, []string{"rule"}),
		NotifyFailuresTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "meridian_notify_failures_total",
			Help: "Notification deliveries that failed (best-effort channel).",
		}),
	}
	if reg != nil {
		reg.MustRegister(m.OpsTotal, m.OpErrorsTotal, m.FetchErrorsTotal,
			m.GuardTriggersTotal, m.CycleDuration, m.LastSuccessfulCycle,
			m.NotifyFailuresTotal)
	}
	return m
}
