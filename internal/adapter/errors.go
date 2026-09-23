package adapter

import "errors"

// Normalized error taxonomy. Adapters wrap every provider error with
// exactly one of these sentinels (errors.Is-able). Only ErrNotFound changes
// engine behaviour: an already-gone delete or update target is not a
// failure. The others differ only in the error metrics' class label; any
// failure is retried by the next cycle, and nothing backs off.
var (
	ErrNotFound    = errors.New("not found")
	ErrRateLimited = errors.New("rate limited")
	ErrAuthFailed  = errors.New("authentication failed")
	ErrTransient   = errors.New("transient error")
)
