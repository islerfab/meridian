package model

import "time"

// Status is the normalized event status (subset shared by both protocols).
type Status string

const (
	StatusConfirmed Status = "confirmed"
	StatusTentative Status = "tentative"
	StatusCancelled Status = "cancelled"
)

// EventRef identifies one source event instance across cycles. It is the
// normalized identity change detection and orphan GC key on (DESIGN.md
// Decision 2): calendar + UID + recurrence-instance ID. RecurrenceID is empty
// for non-recurring events; for expanded instances it is the provider's
// stable instance identity (CalDAV RECURRENCE-ID as UTC timestamp, Google
// instance event ID).
type EventRef struct {
	Calendar     string
	UID          string
	RecurrenceID string
}

// Event is the normalized source event shared by adapters, rules, and the
// engine: UTC instants + AllDay flag, fields matching the documented CEL
// event schema (DESIGN.md Decisions 3 and 4). Nothing protocol-specific.
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
	Status      Status
	Organizer   string
	Attendees   []string
}

// DurationMinutes is the event duration as exposed to CEL filters.
func (e Event) DurationMinutes() int {
	return int(e.End.Sub(e.Start) / time.Minute)
}
