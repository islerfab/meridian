package model

import "time"

// Status is the normalized event status (subset shared by both protocols).
type Status string

const (
	StatusConfirmed Status = "confirmed"
	StatusTentative Status = "tentative"
	StatusCancelled Status = "cancelled"
)

// Visibility is the normalized who-can-see-this classification (Google
// visibility, iCalendar CLASS). VisibilityDefault is the empty value and
// means "say nothing and let the destination calendar decide" — Google's
// literal "default" and an absent CLASS both normalize to it, since neither
// names a concrete audience.
type Visibility string

const (
	VisibilityDefault      Visibility = ""
	VisibilityPublic       Visibility = "public"
	VisibilityPrivate      Visibility = "private"
	VisibilityConfidential Visibility = "confidential"
)

// EventRef identifies one source event instance across cycles. It is the
// normalized identity change detection and orphan GC key on: calendar +
// UID + recurrence-instance ID. RecurrenceID is empty for non-recurring
// events; for expanded instances it is the provider's stable instance
// identity (CalDAV RECURRENCE-ID as UTC timestamp, Google instance event
// ID).
type EventRef struct {
	Calendar     string
	UID          string
	RecurrenceID string
}

// Event is the normalized source event shared by adapters, rules, and the
// engine: UTC instants + AllDay flag, fields matching the documented CEL
// event schema. Nothing protocol-specific.
type Event struct {
	Ref EventRef

	Title       string
	Description string
	Location    string

	// Start/End are absolute UTC instants. For AllDay events they are the
	// date at 00:00:00 UTC and the value is date-only semantically; End is
	// exclusive (iCalendar DTEND / Google end.date convention).
	Start time.Time
	End   time.Time
	// AllDay marks DATE-valued events; they must stay date-valued through
	// the pipeline (never converted to timed events).
	AllDay bool

	// Transparent is true when the event does not block time
	// (iCalendar TRANSP:TRANSPARENT, Google transparency=transparent).
	Transparent bool
	// Visibility is who the source says may see the event. Orthogonal to
	// Transparent: that one is about blocking time, this one is about
	// disclosure.
	Visibility Visibility
	Status     Status
	Organizer  string
	Attendees  []string

	// Marker is non-nil when the source event is itself meridian-owned
	// (a shadow observed as a source). The zombie-resurrection guard keys
	// on this; such events are never treated as syncable content.
	Marker *Marker
}

// DurationMinutes is the event duration as exposed to CEL filters.
func (e Event) DurationMinutes() int {
	return int(e.End.Sub(e.Start) / time.Minute)
}
