package google

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"golang.org/x/oauth2"
	calendar "google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"

	"github.com/islerfab/meridian/internal/adapter"
	"github.com/islerfab/meridian/internal/model"
)

func TestEventFromGoogleTimed(t *testing.T) {
	item := &calendar.Event{
		Id:           "evt123",
		Summary:      "Standup",
		Description:  "daily sync",
		Location:     "Zürich",
		Transparency: "transparent",
		Status:       "tentative",
		Start:        &calendar.EventDateTime{DateTime: "2026-08-18T14:00:00+02:00"},
		End:          &calendar.EventDateTime{DateTime: "2026-08-18T15:00:00+02:00"},
		Organizer:    &calendar.EventOrganizer{Email: "boss@example.com"},
		Attendees: []*calendar.EventAttendee{
			{Email: "a@example.com"}, {Email: "b@example.com"},
		},
	}
	ev, err := eventFromGoogle(item, "work/primary")
	if err != nil {
		t.Fatal(err)
	}
	if ev.Ref != (model.EventRef{Calendar: "work/primary", UID: "evt123"}) {
		t.Errorf("ref = %+v", ev.Ref)
	}
	// Offset times must normalize to UTC instants.
	if !ev.Start.Equal(time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)) || ev.Start.Location() != time.UTC {
		t.Errorf("start = %v", ev.Start)
	}
	if !ev.Transparent || ev.Status != model.StatusTentative || ev.Organizer != "boss@example.com" {
		t.Errorf("event = %+v", ev)
	}
	if len(ev.Attendees) != 2 {
		t.Errorf("attendees = %v", ev.Attendees)
	}
	if ev.Marker != nil {
		t.Error("foreign event must have nil Marker")
	}
}

func TestEventFromGoogleRecurringInstance(t *testing.T) {
	item := &calendar.Event{
		Id:               "evt123_20260818T120000Z",
		RecurringEventId: "evt123",
		OriginalStartTime: &calendar.EventDateTime{
			DateTime: "2026-08-18T14:00:00+02:00",
		},
		Start: &calendar.EventDateTime{DateTime: "2026-08-19T10:30:00+02:00"},
		End:   &calendar.EventDateTime{DateTime: "2026-08-19T11:30:00+02:00"},
	}
	ev, err := eventFromGoogle(item, "cal")
	if err != nil {
		t.Fatal(err)
	}
	// Identity = recurring parent + original occurrence, NOT the instance
	// id (which changes when the instance moves).
	want := model.EventRef{Calendar: "cal", UID: "evt123", RecurrenceID: "2026-08-18T14:00:00+02:00"}
	if ev.Ref != want {
		t.Errorf("ref = %+v, want %+v", ev.Ref, want)
	}

	missing := &calendar.Event{
		Id:               "evt123_2",
		RecurringEventId: "evt123",
		Start:            &calendar.EventDateTime{DateTime: "2026-08-19T10:30:00Z"},
		End:              &calendar.EventDateTime{DateTime: "2026-08-19T11:30:00Z"},
	}
	if _, err := eventFromGoogle(missing, "cal"); err == nil {
		t.Error("instance without originalStartTime must error")
	}
}

func TestEventFromGoogleAllDay(t *testing.T) {
	item := &calendar.Event{
		Id:    "allday1",
		Start: &calendar.EventDateTime{Date: "2026-08-12"},
		End:   &calendar.EventDateTime{Date: "2026-08-13"},
	}
	ev, err := eventFromGoogle(item, "cal")
	if err != nil {
		t.Fatal(err)
	}
	if !ev.AllDay {
		t.Error("expected AllDay")
	}
	if !ev.Start.Equal(time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)) ||
		!ev.End.Equal(time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("start=%v end=%v", ev.Start, ev.End)
	}
}

func TestEventFromGoogleOwned(t *testing.T) {
	content := model.ShadowContent{Title: "Busy", Start: time.Now().UTC(), End: time.Now().UTC()}
	marker := model.NewMarker(model.EventRef{Calendar: "c", UID: "u"}, "rule-x", content)
	item := &calendar.Event{
		Id:    "shadow1",
		Start: &calendar.EventDateTime{DateTime: "2026-08-18T12:00:00Z"},
		End:   &calendar.EventDateTime{DateTime: "2026-08-18T13:00:00Z"},
		ExtendedProperties: &calendar.EventExtendedProperties{
			Private: marker.Properties(true),
		},
	}
	ev, err := eventFromGoogle(item, "cal")
	if err != nil {
		t.Fatal(err)
	}
	if ev.Marker == nil || ev.Marker.Rule != "rule-x" {
		t.Errorf("marker = %+v", ev.Marker)
	}

	item.ExtendedProperties.Private = map[string]string{model.GoogleKeyV: "1"}
	if _, err := eventFromGoogle(item, "cal"); err == nil {
		t.Error("partial marker must error")
	}
}

// What Create/Update send is exactly what ListShadows reads back — asserted
// via hash equality, the invariant change detection depends on.
func TestShadowRoundTrip(t *testing.T) {
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
	shadow := model.Shadow{Content: content, Marker: model.NewMarker(src, "rule-y", content)}

	item := googleFromShadow(shadow)
	item.Id = "created-id" // assigned server-side

	got, err := shadowFromGoogle(item, "work/primary")
	if err != nil {
		t.Fatal(err)
	}
	if got.Marker != shadow.Marker {
		t.Errorf("marker round-trip: got %+v, want %+v", got.Marker, shadow.Marker)
	}
	if model.ContentHash(got.Content) != model.ContentHash(content) {
		t.Errorf("content round-trip changed hash:\n got %+v\nwant %+v", got.Content, content)
	}
	if got.Ref.ID != "created-id" || got.Ref.Calendar != "work/primary" {
		t.Errorf("ref = %+v", got.Ref)
	}
}

func TestShadowRoundTripAllDay(t *testing.T) {
	content := model.ShadowContent{
		Title:  "Busy day",
		Start:  time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC),
		End:    time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC),
		AllDay: true,
	}
	shadow := model.Shadow{Content: content, Marker: model.NewMarker(model.EventRef{Calendar: "c", UID: "u"}, "r", content)}
	item := googleFromShadow(shadow)
	if item.Start.Date != "2026-08-12" || item.Start.DateTime != "" {
		t.Errorf("all-day must use Date field: %+v", item.Start)
	}
	item.Id = "x"
	got, err := shadowFromGoogle(item, "c")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Content.AllDay || model.ContentHash(got.Content) != model.ContentHash(content) {
		t.Errorf("round-trip: %+v", got.Content)
	}
}

func TestGoogleFromShadowSuppressesDefaultReminders(t *testing.T) {
	shadow := model.Shadow{Content: model.ShadowContent{
		Start: time.Now().UTC(), End: time.Now().UTC(),
	}}
	item := googleFromShadow(shadow)
	if item.Reminders == nil || item.Reminders.UseDefault {
		t.Fatal("reminders must be explicit, never calendar defaults")
	}
	found := false
	for _, f := range item.Reminders.ForceSendFields {
		if f == "UseDefault" {
			found = true
		}
	}
	if !found {
		t.Error("UseDefault=false is a zero value and must be force-sent")
	}
}

func TestClassify(t *testing.T) {
	g := func(code int, reasons ...string) *googleapi.Error {
		e := &googleapi.Error{Code: code}
		for _, r := range reasons {
			e.Errors = append(e.Errors, googleapi.ErrorItem{Reason: r})
		}
		return e
	}
	cases := []struct {
		err  error
		want error
	}{
		{g(404), adapter.ErrNotFound},
		{g(410), adapter.ErrNotFound},
		{g(401), adapter.ErrAuthFailed},
		{g(403, "forbidden"), adapter.ErrAuthFailed},
		{g(403, "rateLimitExceeded"), adapter.ErrRateLimited},
		{g(403, "userRateLimitExceeded"), adapter.ErrRateLimited},
		{g(429), adapter.ErrRateLimited},
		{g(500), adapter.ErrTransient},
		{g(503), adapter.ErrTransient},
		{fmt.Errorf("wrapped: %w", g(404)), adapter.ErrNotFound},
		{&oauth2.RetrieveError{}, adapter.ErrAuthFailed},
		{errors.New("dial tcp: connection refused"), adapter.ErrTransient},
	}
	for _, c := range cases {
		if got := classify(c.err); !errors.Is(got, c.want) {
			t.Errorf("classify(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}
