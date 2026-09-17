package caldav

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"github.com/islerfab/meridian/internal/model"
)

// propColor is RFC 7986's COLOR property (CSS3 extended color keyword, e.g.
// "green"). go-ical has no named constant for it; the property name is a
// plain string like any other iCalendar property.
const propColor = "COLOR"

// eventFromComponent normalizes one (expanded) VEVENT into the model.
// Server-expanded instances arrive with UTC DATE-TIME values and a
// RECURRENCE-ID on every instance (verified against sabre/dav 4.3.1);
// all-day events stay DATE-valued.
func eventFromComponent(comp *ical.Component, calendarID string) (model.Event, error) {
	var ev model.Event

	uid, err := comp.Props.Text(ical.PropUID)
	if err != nil || uid == "" {
		return ev, fmt.Errorf("missing UID")
	}
	ev.Ref = model.EventRef{Calendar: calendarID, UID: uid}
	if rid := comp.Props.Get(ical.PropRecurrenceID); rid != nil {
		ev.Ref.RecurrenceID = rid.Value
	}

	start, end, allDay, err := parseTimes(comp)
	if err != nil {
		return ev, err
	}
	ev.Start, ev.End, ev.AllDay = start, end, allDay

	ev.Title, _ = comp.Props.Text(ical.PropSummary)
	ev.Description, _ = comp.Props.Text(ical.PropDescription)
	ev.Location, _ = comp.Props.Text(ical.PropLocation)

	if transp, _ := comp.Props.Text(ical.PropTransparency); strings.EqualFold(transp, "TRANSPARENT") {
		ev.Transparent = true
	}
	ev.Status = parseStatus(comp)
	if org := comp.Props.Get(ical.PropOrganizer); org != nil {
		ev.Organizer = stripMailto(org.Value)
	}
	for _, att := range comp.Props.Values(ical.PropAttendee) {
		ev.Attendees = append(ev.Attendees, stripMailto(att.Value))
	}

	marker, found, err := parseComponentMarker(comp)
	if err != nil {
		return ev, fmt.Errorf("meridian-owned event with malformed marker: %w", err)
	}
	if found {
		ev.Marker = &marker
	}
	return ev, nil
}

// contentFromComponent reads a shadow VEVENT back into the desired-content
// shape (used by ListShadows; the engine compares hashes, never these
// fields, so lossiness in provider round-trips is acceptable by design).
func contentFromComponent(comp *ical.Component) (model.ShadowContent, error) {
	var c model.ShadowContent
	start, end, allDay, err := parseTimes(comp)
	if err != nil {
		return c, err
	}
	c.Start, c.End, c.AllDay = start, end, allDay
	c.Title, _ = comp.Props.Text(ical.PropSummary)
	c.Description, _ = comp.Props.Text(ical.PropDescription)
	c.Location, _ = comp.Props.Text(ical.PropLocation)
	if transp, _ := comp.Props.Text(ical.PropTransparency); strings.EqualFold(transp, "TRANSPARENT") {
		c.Transparent = true
	}
	c.Color, _ = comp.Props.Text(propColor)
	for _, child := range comp.Children {
		if child.Name != ical.CompAlarm {
			continue
		}
		if m, ok := reminderMinutes(child); ok {
			c.Reminders = append(c.Reminders, m)
		}
	}
	return c, nil
}

func parseTimes(comp *ical.Component) (start, end time.Time, allDay bool, err error) {
	dtstart := comp.Props.Get(ical.PropDateTimeStart)
	if dtstart == nil {
		return start, end, false, fmt.Errorf("missing DTSTART")
	}
	allDay = dtstart.ValueType() == ical.ValueDate
	start, err = dtstart.DateTime(time.UTC)
	if err != nil {
		return start, end, allDay, fmt.Errorf("DTSTART: %w", err)
	}
	start = start.UTC()

	if dtend := comp.Props.Get(ical.PropDateTimeEnd); dtend != nil {
		end, err = dtend.DateTime(time.UTC)
		if err != nil {
			return start, end, allDay, fmt.Errorf("DTEND: %w", err)
		}
		end = end.UTC()
		return start, end, allDay, nil
	}
	if durProp := comp.Props.Get(ical.PropDuration); durProp != nil {
		dur, err := durProp.Duration()
		if err != nil {
			return start, end, allDay, fmt.Errorf("DURATION: %w", err)
		}
		return start, start.Add(dur), allDay, nil
	}
	// RFC 5545: DATE start without DTEND/DURATION spans one day; timed
	// events without either are instantaneous.
	if allDay {
		return start, start.AddDate(0, 0, 1), allDay, nil
	}
	return start, start, allDay, nil
}

func parseStatus(comp *ical.Component) model.Status {
	status, _ := comp.Props.Text(ical.PropStatus)
	switch strings.ToUpper(status) {
	case "TENTATIVE":
		return model.StatusTentative
	case "CANCELLED":
		return model.StatusCancelled
	default:
		return model.StatusConfirmed
	}
}

func stripMailto(s string) string {
	if len(s) >= 7 && strings.EqualFold(s[:7], "mailto:") {
		return s[7:]
	}
	return s
}

// parseComponentMarker extracts X-MERIDIAN-* props into the shared codec.
func parseComponentMarker(comp *ical.Component) (model.Marker, bool, error) {
	props := map[string]string{}
	for _, key := range []string{model.CalDAVPropSrc, model.CalDAVPropRule, model.CalDAVPropHash, model.CalDAVPropInstance, model.CalDAVPropRepair, model.CalDAVPropV} {
		if p := comp.Props.Get(key); p != nil {
			props[key] = p.Value
		}
	}
	return model.ParseMarker(props, model.ProtocolCalDAV)
}

// reminderMinutes decodes a VALARM written by buildShadowCalendar:
// TRIGGER:-PT<m>M relative to start. Foreign alarm shapes are ignored.
func reminderMinutes(alarm *ical.Component) (int, bool) {
	trigger := alarm.Props.Get(ical.PropTrigger)
	if trigger == nil {
		return 0, false
	}
	dur, err := trigger.Duration()
	if err != nil || dur > 0 {
		return 0, false
	}
	return int(-dur / time.Minute), true
}

// buildShadowCalendar renders the full desired object: content + marker in
// one write (atomic replace keeps hash-based change detection sound).
func buildShadowCalendar(uid string, shadow model.Shadow) *ical.Calendar {
	event := ical.NewComponent(ical.CompEvent)
	set := func(name, value string) {
		p := ical.NewProp(name)
		p.Value = value
		event.Props.Set(p)
	}

	set(ical.PropUID, uid)
	event.Props.SetDateTime(ical.PropDateTimeStamp, now().UTC())

	c := shadow.Content
	if c.AllDay {
		event.Props.SetDate(ical.PropDateTimeStart, c.Start.UTC())
		event.Props.SetDate(ical.PropDateTimeEnd, c.End.UTC())
	} else {
		event.Props.SetDateTime(ical.PropDateTimeStart, c.Start.UTC())
		event.Props.SetDateTime(ical.PropDateTimeEnd, c.End.UTC())
	}
	if c.Title != "" {
		event.Props.SetText(ical.PropSummary, c.Title)
	}
	if c.Description != "" {
		event.Props.SetText(ical.PropDescription, c.Description)
	}
	if c.Location != "" {
		event.Props.SetText(ical.PropLocation, c.Location)
	}
	if c.Transparent {
		set(ical.PropTransparency, "TRANSPARENT")
	}
	if c.Color != "" {
		set(propColor, c.Color)
	}
	for key, value := range shadow.Marker.Properties(model.ProtocolCalDAV) {
		set(key, value)
	}
	for _, m := range c.Reminders {
		alarm := ical.NewComponent(ical.CompAlarm)
		action := ical.NewProp(ical.PropAction)
		action.Value = "DISPLAY"
		alarm.Props.Set(action)
		desc := ical.NewProp(ical.PropDescription)
		desc.SetText("Reminder")
		alarm.Props.Set(desc)
		trigger := ical.NewProp(ical.PropTrigger)
		trigger.Value = "-PT" + strconv.Itoa(m) + "M"
		alarm.Props.Set(trigger)
		event.Children = append(event.Children, alarm)
	}

	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//meridian//meridian//EN")
	cal.Children = append(cal.Children, event)
	return cal
}
