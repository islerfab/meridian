// Package model holds the normalized event model shared by adapters and the
// sync engine: UTC instants + AllDay flag, ownership markers, and the content
// hash used for change detection.
//
// Invariants: docs/content/design.md, "The marker" and "The copy".
package model
