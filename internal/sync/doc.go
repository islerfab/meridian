// Package sync implements stateless snapshot reconciliation: per-rule
// level-triggered cycles that diff the desired shadow set against
// destination state, with the five hardening guards (zombie-resurrection,
// mass-delete, hash change-detection, windowed orphan-GC, tombstone
// tolerance).
//
// Invariants: docs/content/design.md, "The cycle" and "The guards".
package sync
