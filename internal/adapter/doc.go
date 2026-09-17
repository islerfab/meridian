// Package adapter defines the CalendarAdapter interface and its provider
// implementations (Google Calendar, CalDAV). Protocol asymmetry stays
// inside each adapter; the engine sees one identical five-method contract
// and a normalized error taxonomy.
//
// Invariants: docs/content/design.md, "The adapter".
package adapter
