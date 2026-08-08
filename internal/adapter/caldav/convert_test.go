package caldav

import (
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	"github.com/islerfab/meridian/internal/model"
)

func parseVEVENT(t *testing.T, body string) *ical.Component {
	t.Helper()
	ics := "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//test//test//EN\n" + body + "\nEND:VCALENDAR\n"
	ics = strings.ReplaceAll(ics, "\n", "\r\n")
	cal, err := ical.NewDecoder(strings.NewReader(ics)).Decode()
	if err != nil {
		t.Fatalf("parse test ICS: %v", err)
	}
	for _, comp := range cal.Children {
		if comp.Name == ical.CompEvent {
			return comp
		}
	}
	t.Fatal("no VEVENT in test ICS")
	return nil
}

func TestEventFromComponentTimed(t *testing.T) {
	comp := parseVEVENT(t, `BEGIN:VEVENT
UID:abc@example.com
DTSTAMP:20260807T120000Z
RECURRENCE-ID:20260818T120000Z
DTSTART:20260818T120000Z
DTEND:20260818T130000Z
SUMMARY:Standup
DESCRIPTION:daily sync
LOCATION:Zürich
TRANSP:TRANSPARENT
STATUS:TENTATIVE
ORGANIZER:mailto:boss@example.com
ATTENDEE:mailto:a@example.com
ATTENDEE:MAILTO:b@example.com
END:VEVENT`)

	ev, err := eventFromComponent(comp, "work/primary")
	if err != nil {
		t.Fatal(err)
	}
	want := model.Event{
		Ref:         model.EventRef{Calendar: "work/primary", UID: "abc@example.com", RecurrenceID: "20260818T120000Z"},
		Title:       "Standup",
		Description: "daily sync",
		Location:    "Zürich",
		Start:       time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
		End:         time.Date(2026, 8, 18, 13, 0, 0, 0, time.UTC),
		Transparent: true,
		Status:      model.StatusTentative,
		Organizer:   "boss@example.com",
	}
	if ev.Ref != want.Ref || ev.Title != want.Title || ev.Description != want.Description ||
		ev.Location != want.Location || !ev.Start.Equal(want.Start) || !ev.End.Equal(want.End) ||
		ev.AllDay || !ev.Transparent || ev.Status != want.Status || ev.Organizer != want.Organizer {
		t.Errorf("got %+v, want %+v", ev, want)
	}
	if len(ev.Attendees) != 2 || ev.Attendees[0] != "a@example.com" || ev.Attendees[1] != "b@example.com" {
		t.Errorf("attendees = %v", ev.Attendees)
	}
	if ev.Marker != nil {
		t.Error("foreign event must have nil Marker")
	}
}

func TestEventFromComponentAllDay(t *testing.T) {
	comp := parseVEVENT(t, `BEGIN:VEVENT
UID:allday@example.com
DTSTAMP:20260807T120000Z
DTSTART;VALUE=DATE:20260812
SUMMARY:Holiday
END:VEVENT`)

	ev, err := eventFromComponent(comp, "cal")
	if err != nil {
		t.Fatal(err)
	}
	if !ev.AllDay {
		t.Error("expected AllDay")
	}
	if !ev.Start.Equal(time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("start = %v", ev.Start)
	}
	// No DTEND on a DATE event: spans exactly one day.
	if !ev.End.Equal(time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("end = %v", ev.End)
	}
	if ev.Status != model.StatusConfirmed {
		t.Errorf("default status = %v, want confirmed", ev.Status)
	}
}

func TestEventFromComponentDuration(t *testing.T) {
	comp := parseVEVENT(t, `BEGIN:VEVENT
UID:dur@example.com
DTSTAMP:20260807T120000Z
DTSTART:20260818T120000Z
DURATION:PT45M
END:VEVENT`)
	ev, err := eventFromComponent(comp, "cal")
	if err != nil {
		t.Fatal(err)
	}
	if !ev.End.Equal(ev.Start.Add(45 * time.Minute)) {
		t.Errorf("end = %v, want start+45m", ev.End)
	}
}

func TestEventFromComponentOwned(t *testing.T) {
	comp := parseVEVENT(t, `BEGIN:VEVENT
UID:shadow@example.com
DTSTAMP:20260807T120000Z
DTSTART:20260818T120000Z
DTEND:20260818T130000Z
SUMMARY:Busy
X-MERIDIAN-SRC:cal|uid|
X-MERIDIAN-RULE:some-rule
X-MERIDIAN-HASH:deadbeef
X-MERIDIAN-INSTANCE:inst-test
X-MERIDIAN-V:1
END:VEVENT`)
	ev, err := eventFromComponent(comp, "cal")
	if err != nil {
		t.Fatal(err)
	}
	if ev.Marker == nil {
		t.Fatal("owned event must expose its marker (zombie guard input)")
	}
	if ev.Marker.Rule != "some-rule" || ev.Marker.Src.UID != "uid" {
		t.Errorf("marker = %+v", ev.Marker)
	}
}

func TestEventFromComponentErrors(t *testing.T) {
	noUID := parseVEVENT(t, `BEGIN:VEVENT
DTSTAMP:20260807T120000Z
DTSTART:20260818T120000Z
END:VEVENT`)
	if _, err := eventFromComponent(noUID, "cal"); err == nil {
		t.Error("missing UID must error")
	}
	noStart := parseVEVENT(t, `BEGIN:VEVENT
UID:x
DTSTAMP:20260807T120000Z
END:VEVENT`)
	if _, err := eventFromComponent(noStart, "cal"); err == nil {
		t.Error("missing DTSTART must error")
	}
	badMarker := parseVEVENT(t, `BEGIN:VEVENT
UID:x
DTSTAMP:20260807T120000Z
DTSTART:20260818T120000Z
X-MERIDIAN-V:1
END:VEVENT`)
	if _, err := eventFromComponent(badMarker, "cal"); err == nil {
		t.Error("partial marker must error")
	}
}

// The write path must round-trip through the read path: what Create/Update
// render is exactly what ListShadows reads back.
func TestShadowRoundTrip(t *testing.T) {
	now = func() time.Time { return time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC) }
	defer func() { now = time.Now }()

	content := model.ShadowContent{
		Title:       "Busy",
		Description: "synced by meridian",
		Location:    "Zürich",
		Start:       time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
		End:         time.Date(2026, 8, 18, 13, 0, 0, 0, time.UTC),
		Transparent: true,
		Reminders:   []int{15, 30},
	}
	src := model.EventRef{Calendar: "private/main", UID: "abc@ik.me", RecurrenceID: "20260818T120000Z"}
	shadow := model.Shadow{
		Content: content,
		Marker:  model.NewMarker("inst-test", src, "private-to-work-busy", content),
	}

	cal := buildShadowCalendar("meridian-test", shadow)

	// Encode + decode: the server stores the serialized form.
	var b strings.Builder
	if err := ical.NewEncoder(&b).Encode(cal); err != nil {
		t.Fatal(err)
	}
	decoded, err := ical.NewDecoder(strings.NewReader(b.String())).Decode()
	if err != nil {
		t.Fatal(err)
	}
	var event *ical.Component
	for _, comp := range decoded.Children {
		if comp.Name == ical.CompEvent {
			event = comp
		}
	}
	if event == nil {
		t.Fatal("no VEVENT after round-trip")
	}

	marker, found, err := parseComponentMarker(event)
	if err != nil || !found {
		t.Fatalf("marker: found=%t err=%v", found, err)
	}
	if marker != shadow.Marker {
		t.Errorf("marker round-trip: got %+v, want %+v", marker, shadow.Marker)
	}
	got, err := contentFromComponent(event)
	if err != nil {
		t.Fatal(err)
	}
	// Hash equality is the real invariant: read-back content must produce
	// the same hash change detection compares against.
	if model.ContentHash(got) != model.ContentHash(content) {
		t.Errorf("content round-trip changed hash:\n got %+v\nwant %+v", got, content)
	}
}

func TestShadowRoundTripAllDay(t *testing.T) {
	content := model.ShadowContent{
		Title:  "Busy day",
		Start:  time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC),
		End:    time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC),
		AllDay: true,
	}
	shadow := model.Shadow{
		Content: content,
		Marker:  model.NewMarker("inst-test", model.EventRef{Calendar: "c", UID: "u"}, "r", content),
	}
	cal := buildShadowCalendar("meridian-test", shadow)

	var b strings.Builder
	if err := ical.NewEncoder(&b).Encode(cal); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "DTSTART;VALUE=DATE:20260812") {
		t.Errorf("all-day shadow must be DATE-valued, got:\n%s", b.String())
	}
	decoded, err := ical.NewDecoder(strings.NewReader(b.String())).Decode()
	if err != nil {
		t.Fatal(err)
	}
	for _, comp := range decoded.Children {
		if comp.Name != ical.CompEvent {
			continue
		}
		got, err := contentFromComponent(comp)
		if err != nil {
			t.Fatal(err)
		}
		if !got.AllDay || model.ContentHash(got) != model.ContentHash(content) {
			t.Errorf("all-day round-trip: got %+v", got)
		}
	}
}
