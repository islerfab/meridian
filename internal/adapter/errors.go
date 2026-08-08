package adapter

import "errors"

// Normalized error taxonomy (DESIGN.md Decision 5). Adapters wrap every
// provider error with exactly one of these sentinels (errors.Is-able); the
// engine acts on the class, never on provider-specific errors:
//
//	ErrNotFound    → treat as deleted (idempotent deletes, tombstones)
//	ErrRateLimited → back off, resume next cycle
//	ErrAuthFailed  → fatal, alert loudly (needs a human)
//	ErrTransient   → skip this rule's cycle; the next cycle is the retry
var (
	ErrNotFound    = errors.New("not found")
	ErrRateLimited = errors.New("rate limited")
	ErrAuthFailed  = errors.New("authentication failed")
	ErrTransient   = errors.New("transient error")
)
