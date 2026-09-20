package google

import (
	"fmt"
	"strings"
	"time"

	calendar "google.golang.org/api/calendar/v3"

	"github.com/islerfab/meridian/internal/model"
)

// eventFromGoogle normalizes one (expanded) event item into the model.
// Instance identity: expanded instances carry recurringEventId +
// originalStartTime; those form the stable EventRef (the instance's own Id
// mutates when the instance is moved, originalStartTime does not).
func eventFromGoogle(item *calendar.Event, calendarID string) (model.Event, error) {
	var ev model.Event
	if item.Id == "" {
		return ev, fmt.Errorf("event without id")
	}
	ev.Ref = model.EventRef{Calendar: calendarID, UID: item.Id}
	if item.RecurringEventId != "" {
		ev.Ref.UID = item.RecurringEventId
		if item.OriginalStartTime != nil {
			ev.Ref.RecurrenceID = firstNonEmpty(item.OriginalStartTime.DateTime, item.OriginalStartTime.Date)
		}
		if ev.Ref.RecurrenceID == "" {
			return ev, fmt.Errorf("recurring instance %s without originalStartTime", item.Id)
		}
	}

	start, allDayStart, err := parseEventDateTime(item.Start)
	if err != nil {
		return ev, fmt.Errorf("start: %w", err)
	}
	end, _, err := parseEventDateTime(item.End)
	if err != nil {
		return ev, fmt.Errorf("end: %w", err)
	}
	ev.Start, ev.End, ev.AllDay = start, end, allDayStart

	ev.Title = item.Summary
	ev.Description = item.Description
	ev.Location = item.Location
	ev.Transparent = item.Transparency == "transparent"
	ev.Visibility = visibilityFromGoogle(item.Visibility)
	switch item.Status {
	case "tentative":
		ev.Status = model.StatusTentative
	case "cancelled":
		ev.Status = model.StatusCancelled
	default:
		ev.Status = model.StatusConfirmed
	}
	if item.Organizer != nil {
		ev.Organizer = item.Organizer.Email
	}
	for _, att := range item.Attendees {
		if att.Email != "" {
			ev.Attendees = append(ev.Attendees, att.Email)
		}
		if att.Self {
			ev.RSVP = parseRSVP(att.ResponseStatus)
		}
	}

	if item.ExtendedProperties != nil {
		marker, found, err := model.ParseMarker(item.ExtendedProperties.Private, model.ProtocolGoogle)
		if err != nil {
			return ev, fmt.Errorf("meridian-owned event with malformed marker: %w", err)
		}
		if found {
			ev.Marker = &marker
		}
	}
	return ev, nil
}

// shadowFromGoogle reads a marker-matched event back as a Shadow.
func shadowFromGoogle(item *calendar.Event, calendarID string) (model.Shadow, error) {
	var shadow model.Shadow
	if item.ExtendedProperties == nil {
		return shadow, fmt.Errorf("no extended properties on marker-matched event")
	}
	marker, found, err := model.ParseMarker(item.ExtendedProperties.Private, model.ProtocolGoogle)
	if err != nil || !found {
		return shadow, fmt.Errorf("marker: found=%t: %w", found, err)
	}

	start, allDay, err := parseEventDateTime(item.Start)
	if err != nil {
		return shadow, fmt.Errorf("start: %w", err)
	}
	end, _, err := parseEventDateTime(item.End)
	if err != nil {
		return shadow, fmt.Errorf("end: %w", err)
	}
	content := model.ShadowContent{
		Title:       item.Summary,
		Description: item.Description,
		Location:    item.Location,
		Start:       start,
		End:         end,
		AllDay:      allDay,
		Transparent: item.Transparency == "transparent",
		Color:       item.ColorId,
		Visibility:  visibilityFromGoogle(item.Visibility),
	}
	if item.Reminders != nil {
		for _, o := range item.Reminders.Overrides {
			if o.Method == "popup" {
				content.Reminders = append(content.Reminders, int(o.Minutes))
			}
		}
	}
	return model.Shadow{
		Ref:     model.ShadowRef{Calendar: calendarID, ID: item.Id},
		Content: content,
		Marker:  marker,
	}, nil
}

// googleFromShadow renders the full desired event: content + marker in one
// write (atomic replace keeps hash-based change detection sound).
func googleFromShadow(shadow model.Shadow) *calendar.Event {
	c := shadow.Content
	item := &calendar.Event{
		Summary:     c.Title,
		Description: c.Description,
		Location:    c.Location,
		ColorId:     c.Color,
		Start:       toEventDateTime(c.Start, c.AllDay),
		End:         toEventDateTime(c.End, c.AllDay),
		ExtendedProperties: &calendar.EventExtendedProperties{
			Private: shadow.Marker.Properties(model.ProtocolGoogle),
		},
		// UseDefault=false suppresses calendar-default reminders; it is a
		// zero value, so it must be force-sent or Google applies defaults.
		Reminders: &calendar.EventReminders{
			UseDefault:      false,
			ForceSendFields: []string{"UseDefault"},
		},
	}
	if c.Transparent {
		item.Transparency = "transparent"
	}
	// Left unset, Google applies the calendar's default — which is exactly
	// what VisibilityDefault means, so an empty value is not sent.
	item.Visibility = string(c.Visibility)
	for _, m := range c.Reminders {
		item.Reminders.Overrides = append(item.Reminders.Overrides, &calendar.EventReminder{
			Method:  "popup",
			Minutes: int64(m),
		})
	}
	return item
}

func parseEventDateTime(edt *calendar.EventDateTime) (time.Time, bool, error) {
	if edt == nil {
		return time.Time{}, false, fmt.Errorf("missing date/time")
	}
	if edt.Date != "" {
		t, err := time.ParseInLocation("2006-01-02", edt.Date, time.UTC)
		if err != nil {
			return time.Time{}, true, err
		}
		return t, true, nil
	}
	t, err := time.Parse(time.RFC3339, edt.DateTime)
	if err != nil {
		return time.Time{}, false, err
	}
	return t.UTC(), false, nil
}

func toEventDateTime(t time.Time, allDay bool) *calendar.EventDateTime {
	if allDay {
		return &calendar.EventDateTime{Date: t.UTC().Format("2006-01-02")}
	}
	return &calendar.EventDateTime{DateTime: t.UTC().Format(time.RFC3339), TimeZone: "UTC"}
}

// visibilityFromGoogle normalizes the API's visibility field. "default" is
// Google's way of saying "inherit from the calendar", which carries no
// audience of its own and so maps to the empty value rather than to a
// fourth class the model would have to carry.
func visibilityFromGoogle(v string) model.Visibility {
	switch v {
	case "public":
		return model.VisibilityPublic
	case "private":
		return model.VisibilityPrivate
	case "confidential":
		return model.VisibilityConfidential
	default:
		return model.VisibilityDefault
	}
}

// parseRSVP normalizes attendees[].responseStatus. Google's four values are
// already the model's spelling, so anything else is a value this build
// doesn't know and maps to RSVPNone rather than being passed through into
// rules as an unrecognized string.
func parseRSVP(status string) model.RSVP {
	switch status {
	case "needsAction":
		return model.RSVPNeedsAction
	case "accepted":
		return model.RSVPAccepted
	case "declined":
		return model.RSVPDeclined
	case "tentative":
		return model.RSVPTentative
	default:
		return model.RSVPNone
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
