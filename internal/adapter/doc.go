// Package adapter defines the CalendarAdapter interface (DESIGN.md
// Decision 5) and its provider implementations (Google Calendar, CalDAV).
// Protocol asymmetry stays inside each adapter; the engine sees one
// identical five-method contract and a normalized error taxonomy.
package adapter
