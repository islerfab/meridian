package model

import "time"

// ShadowRef is the provider-side identity of a shadow object on a
// destination calendar: Google event ID or CalDAV object path. It is what
// Update/Delete address; it never crosses cycles as state (the marker does).
type ShadowRef struct {
	Calendar string
	ID       string
}

// ShadowContent is the post-transform desired content of a shadow event —
// exactly the fields meridian writes to destinations. The content hash
// (DESIGN.md Decision 2) is computed over this and nothing else.
type ShadowContent struct {
	Title       string
	Description string
	Location    string

	// Same time model as Event: UTC instants; AllDay events are date-valued
	// with midnight-UTC instants and exclusive End.
	Start  time.Time
	End    time.Time
	AllDay bool

	Transparent bool

	// Reminders holds minutes-before-start reminder offsets to set on the
	// shadow (empty = none). Part of desired content: changing a rule's
	// reminders must re-write its shadows, so it participates in the hash.
	Reminders []int
}

// Shadow is one meridian-owned event on a destination calendar: desired (or
// observed) content plus the ownership marker that proves ownership and
// carries the change-detection hash.
type Shadow struct {
	Ref     ShadowRef
	Content ShadowContent
	Marker  Marker
}
