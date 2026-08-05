// Package sync implements stateless snapshot reconciliation (DESIGN.md
// Decision 1): per-rule level-triggered cycles that diff the desired shadow
// set against destination state, with the five hardening guards
// (zombie-resurrection, mass-delete, hash change-detection, windowed
// orphan-GC, tombstone tolerance).
package sync
