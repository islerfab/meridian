package config

import (
	"testing"
	"time"

	"github.com/islerfab/meridian/internal/model"
)

func mustFilter(t *testing.T, fc *FilterConfig) func(model.Event) bool {
	t.Helper()
	env, err := newCELEnv()
	if err != nil {
		t.Fatal(err)
	}
	f, err := compileFilter(env, fc)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func timed(start, end string) model.Event {
	s, _ := time.Parse(time.RFC3339, start)
	e, _ := time.Parse(time.RFC3339, end)
	return model.Event{Ref: model.EventRef{Calendar: "src", UID: "u"}, Title: "T", Start: s.UTC(), End: e.UTC()}
}

func allDay(startDate, endDate string) model.Event {
	s, _ := time.Parse("2006-01-02", startDate)
	e, _ := time.Parse("2006-01-02", endDate)
	return model.Event{Ref: model.EventRef{Calendar: "src", UID: "u"}, Title: "T", Start: s, End: e, AllDay: true}
}

// The weekly-region semantics: weekdays+window+timezone form a recurring
// region; timed events match on overlap; all-day events are
// weekday-governed only. 2026-08-10 is a Monday; Zurich is UTC+2 (CEST) in
// August.
func TestFilterWeeklyRegion(t *testing.T) {
	f := mustFilter(t, &FilterConfig{
		Weekdays: []string{"mon", "tue", "wed", "thu", "fri"},
		Window:   "08:00-18:00",
		Timezone: "Europe/Zurich",
	})
	cases := []struct {
		name string
		ev   model.Event
		want bool
	}{
		{"inside window", timed("2026-08-10T08:00:00+02:00", "2026-08-10T09:00:00+02:00"), true},
		{"straddles window start", timed("2026-08-10T07:00:00+02:00", "2026-08-10T09:00:00+02:00"), true},
		{"before window", timed("2026-08-10T06:00:00+02:00", "2026-08-10T07:30:00+02:00"), false},
		{"after window", timed("2026-08-10T19:00:00+02:00", "2026-08-10T20:00:00+02:00"), false},
		{"weekend", timed("2026-08-15T10:00:00+02:00", "2026-08-15T11:00:00+02:00"), false},
		{"sun-to-mon overnight reaches window", timed("2026-08-09T23:00:00+02:00", "2026-08-10T09:00:00+02:00"), true},
		{"sun-to-mon overnight ends before window", timed("2026-08-09T23:00:00+02:00", "2026-08-10T06:00:00+02:00"), false},
		{"multi-day spans window", timed("2026-08-09T23:00:00+02:00", "2026-08-11T09:00:00+02:00"), true},
		{"all-day on monday matches (weekday only)", allDay("2026-08-10", "2026-08-11"), true},
		{"all-day on saturday no match", allDay("2026-08-15", "2026-08-16"), false},
		{"multi-day all-day spanning weekend+monday matches", allDay("2026-08-15", "2026-08-18"), true},
	}
	for _, c := range cases {
		if got := f(c.ev); got != c.want {
			t.Errorf("%s: match = %t, want %t", c.name, got, c.want)
		}
	}
}

func TestFilterWeekdaysOnlyAndFlags(t *testing.T) {
	f := mustFilter(t, &FilterConfig{Weekdays: []string{"mon"}, Timezone: "Europe/Zurich"})
	if !f(timed("2026-08-10T22:00:00+02:00", "2026-08-10T23:00:00+02:00")) {
		t.Error("weekday-only region must match any time on the day")
	}
	if f(timed("2026-08-11T10:00:00+02:00", "2026-08-11T11:00:00+02:00")) {
		t.Error("tuesday must not match [mon]")
	}

	skip := mustFilter(t, &FilterConfig{SkipTransparent: true, SkipAllDay: true})
	ev := timed("2026-08-10T10:00:00Z", "2026-08-10T11:00:00Z")
	ev.Transparent = true
	if skip(ev) {
		t.Error("skipTransparent must exclude transparent events")
	}
	if skip(allDay("2026-08-10", "2026-08-11")) {
		t.Error("skipAllDay must exclude all-day events")
	}
	if !skip(timed("2026-08-10T10:00:00Z", "2026-08-10T11:00:00Z")) {
		t.Error("plain event must pass")
	}
}

func TestFilterCEL(t *testing.T) {
	f := mustFilter(t, &FilterConfig{When: `!event.title.startsWith("[private]") && event.durationMinutes >= 30`})
	long := timed("2026-08-10T10:00:00Z", "2026-08-10T11:00:00Z")
	if !f(long) {
		t.Error("plain 60m event must match")
	}
	private := long
	private.Title = "[private] dentist"
	if f(private) {
		t.Error("[private] title must not match")
	}
	short := timed("2026-08-10T10:00:00Z", "2026-08-10T10:15:00Z")
	if f(short) {
		t.Error("15m event must not match durationMinutes >= 30")
	}
}

func TestTransformDefaultsToFaithfulCopy(t *testing.T) {
	tr, err := compileTransform(RuleConfig{})
	if err != nil {
		t.Fatal(err)
	}
	ev := timed("2026-08-10T10:00:00Z", "2026-08-10T11:00:00Z")
	ev.Title, ev.Description, ev.Location, ev.Transparent = "Standup", "notes", "Zürich", true
	got := tr(ev)
	if got.Title != "Standup" || got.Description != "notes" || got.Location != "Zürich" ||
		!got.Transparent || !got.Start.Equal(ev.Start) || !got.End.Equal(ev.End) || len(got.Reminders) != 0 {
		t.Errorf("faithful copy violated: %+v", got)
	}
}

func TestTransformOverrides(t *testing.T) {
	title := "[{{ .SourceCalendar }}] Busy"
	drop := Drop
	opaque := false
	tr, err := compileTransform(RuleConfig{Transform: &TransformConfig{
		Title:       &title,
		Description: &drop,
		Transparent: &opaque,
		Reminders:   []int{10},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ev := timed("2026-08-10T10:00:00Z", "2026-08-10T11:00:00Z")
	ev.Title, ev.Description, ev.Location, ev.Transparent = "Secret", "secret notes", "Home", true
	got := tr(ev)
	if got.Title != "[src] Busy" {
		t.Errorf("title template: %q", got.Title)
	}
	if got.Description != "" {
		t.Errorf("description drop: %q", got.Description)
	}
	if got.Location != "Home" {
		t.Errorf("location must copy when unset: %q", got.Location)
	}
	if got.Transparent {
		t.Error("transparent override to false ignored")
	}
	if len(got.Reminders) != 1 || got.Reminders[0] != 10 {
		t.Errorf("reminders: %v", got.Reminders)
	}
}
