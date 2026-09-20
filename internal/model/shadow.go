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
// exactly the fields meridian writes to destinations. The content hash is
// computed over this and nothing else.
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

	// Visibility is the shadow's disclosure class. Unlike Color it has a
	// source counterpart, so an unset transform copies the source's value
	// rather than leaving it alone; empty means write nothing and inherit
	// the destination calendar's default.
	Visibility Visibility

	// Reminders holds minutes-before-start reminder offsets to set on the
	// shadow (empty = none). Part of desired content: changing a rule's
	// reminders must re-write its shadows, so it participates in the hash.
	Reminders []int

	// Color is a destination-literal from rule config (empty = provider
	// default), not copied from the source event: format is provider-specific
	// (Google numeric colorId "1".."11"; CalDAV RFC 7986 COLOR keyword, e.g.
	// "green") and copying one provider's value into the other verbatim would
	// write garbage, so unlike Title/Description/Location there is no
	// copy-source default — same rationale as Reminders.
	Color string
}

// Shadow is one meridian-owned event on a destination calendar: desired (or
// observed) content plus the ownership marker that proves ownership and
// carries the change-detection hash.
type Shadow struct {
	Ref     ShadowRef
	Content ShadowContent
	Marker  Marker
}
