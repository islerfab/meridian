package config

import (
	"strings"
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
	f, _, err := compileFilter(env, fc)
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

// Visibility is copy-with-override like Transparent, not override-only like
// Color: it has a real source counterpart on both protocols, so an unset
// transform mirrors it.
func TestTransformVisibility(t *testing.T) {
	ev := timed("2026-08-10T10:00:00Z", "2026-08-10T11:00:00Z")
	ev.Visibility = model.VisibilityConfidential

	tr, err := compileTransform(RuleConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if got := tr(ev); got.Visibility != model.VisibilityConfidential {
		t.Errorf("unset transform: visibility = %q, want the source's %q", got.Visibility, ev.Visibility)
	}

	forced := "private"
	tr, err = compileTransform(RuleConfig{Transform: &TransformConfig{Visibility: &forced}})
	if err != nil {
		t.Fatal(err)
	}
	if got := tr(ev); got.Visibility != model.VisibilityPrivate {
		t.Errorf("override: visibility = %q, want private", got.Visibility)
	}

	// A source that inherits its calendar default must be forceable to a
	// concrete class — that is the whole point for a privacy mirror.
	ev.Visibility = model.VisibilityDefault
	if got := tr(ev); got.Visibility != model.VisibilityPrivate {
		t.Errorf("override from default: visibility = %q, want private", got.Visibility)
	}
}

// Visibility is exposed to filter.when, so a rule can select on it without
// the transform touching anything.
func TestCELVisibility(t *testing.T) {
	env, err := newCELEnv()
	if err != nil {
		t.Fatal(err)
	}
	prg, _, err := compileWhen(env, `event.visibility == "private"`)
	if err != nil {
		t.Fatalf("compiling a visibility expression: %v", err)
	}
	ev := timed("2026-08-10T10:00:00Z", "2026-08-10T11:00:00Z")
	ev.Visibility = model.VisibilityPrivate
	out, _, err := prg.Eval(map[string]any{"event": celEventFromModel(ev)})
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if out.Value() != true {
		t.Errorf("event.visibility did not match a private event")
	}
}

func TestFilterSkipDeclined(t *testing.T) {
	f := mustFilter(t, &FilterConfig{SkipDeclined: true})
	ev := timed("2026-08-18T12:00:00Z", "2026-08-18T13:00:00Z")

	for _, rsvp := range []model.RSVP{
		model.RSVPNone, model.RSVPNeedsAction, model.RSVPAccepted, model.RSVPTentative,
	} {
		ev.RSVP = rsvp
		if !f(ev) {
			t.Errorf("rsvp %q was skipped, want matched", rsvp)
		}
	}
	ev.RSVP = model.RSVPDeclined
	if f(ev) {
		t.Error("declined event matched, want skipped")
	}
}

// skipDeclined must not become "skip anything not accepted": most events
// carry no RSVP at all and have to pass through untouched.
func TestFilterSkipDeclinedLeavesNonInvitationsAlone(t *testing.T) {
	f := mustFilter(t, &FilterConfig{SkipDeclined: true})
	ev := timed("2026-08-18T12:00:00Z", "2026-08-18T13:00:00Z")
	if !f(ev) {
		t.Error("event with no RSVP was skipped")
	}
}

func TestCELExposesRSVP(t *testing.T) {
	f := mustFilter(t, &FilterConfig{When: `event.rsvp == "accepted"`})
	ev := timed("2026-08-18T12:00:00Z", "2026-08-18T13:00:00Z")
	ev.RSVP = model.RSVPAccepted
	if !f(ev) {
		t.Error("accepted event did not match")
	}
	ev.RSVP = model.RSVPNeedsAction
	if f(ev) {
		t.Error("needsAction event matched an accepted-only expression")
	}
}

func TestSelectsEventField(t *testing.T) {
	env, err := newCELEnv()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		`event.rsvp == "declined"`:                true,
		`event.title != "" && event.rsvp != ""`:   true,
		`event.title == "rsvp"`:                   false,
		`event.status == "confirmed"`:             false,
		`event.attendees.exists(a, a == "x@y.z")`: false,
	}
	for expr, want := range cases {
		_, usesRSVP, err := compileWhen(env, expr)
		if err != nil {
			t.Fatalf("compile %q: %v", expr, err)
		}
		if usesRSVP != want {
			t.Errorf("compileWhen(%q) usesRSVP = %v, want %v", expr, usesRSVP, want)
		}
	}
}

func TestRSVPOnCalDAVWithoutIdentitiesIsStartupError(t *testing.T) {
	cfg := func(identities []string, filter *FilterConfig, from string) *Config {
		return &Config{
			Instance: "test",
			Accounts: []Account{{
				Name: "dav", Type: "caldav", Endpoint: "https://dav.example.com",
				UsernameEnv: "U", PasswordEnv: "P", Identities: identities,
				Calendars: []CalendarConfig{{Name: "main", Path: "/c/"}},
			}, {
				Name: "goog", Type: "google",
				ClientIDEnv: "I", ClientSecretEnv: "S", RefreshTokenEnv: "R",
				Calendars: []CalendarConfig{{Name: "main", ID: "primary"}},
			}},
			Rules: []RuleConfig{{
				ID: "r", From: from, To: []string{"dav/main", "goog/main"}[:1], Filter: filter,
			}},
		}
	}
	skipDeclined := func() *FilterConfig { return &FilterConfig{SkipDeclined: true} }
	viaCEL := func() *FilterConfig { return &FilterConfig{When: `event.rsvp != "declined"`} }

	cases := []struct {
		name    string
		cfg     *Config
		wantErr bool
	}{
		{"skipDeclined, caldav source, no identities", cfg(nil, skipDeclined(), "dav/main"), true},
		{"CEL rsvp, caldav source, no identities", cfg(nil, viaCEL(), "dav/main"), true},
		{"skipDeclined, caldav source, identities set", cfg([]string{"me@example.com"}, skipDeclined(), "dav/main"), false},
		// Google needs no identities: the API marks the owner's entry.
		{"skipDeclined, google source", cfg(nil, skipDeclined(), "goog/main"), false},
		{"no rsvp use at all", cfg(nil, &FilterConfig{SkipAllDay: true}, "dav/main"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CompileRules(tc.cfg)
			if tc.wantErr && err == nil {
				t.Fatal("want a startup error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("want no error, got %v", err)
			}
			if tc.wantErr && !strings.Contains(err.Error(), "identities") {
				t.Errorf("error should name identities, got %v", err)
			}
		})
	}
}

func TestTransparentForRSVP(t *testing.T) {
	transform := func(names []string) func(model.Event) model.ShadowContent {
		t.Helper()
		f, err := compileTransform(RuleConfig{
			Transform: &TransformConfig{TransparentForRSVP: names},
		})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	apply := transform([]string{"needsAction", "tentative"})

	ev := timed("2026-08-18T12:00:00Z", "2026-08-18T13:00:00Z")
	for _, tc := range []struct {
		rsvp model.RSVP
		want bool
	}{
		{model.RSVPNeedsAction, true},
		{model.RSVPTentative, true},
		{model.RSVPAccepted, false},
		{model.RSVPDeclined, false},
	} {
		ev.RSVP = tc.rsvp
		if got := apply(ev).Transparent; got != tc.want {
			t.Errorf("rsvp %q: Transparent = %v, want %v", tc.rsvp, got, tc.want)
		}
	}
}

// The field frees a slot, it never claims one: a response that isn't listed
// keeps whatever the source said rather than being forced opaque.
func TestTransparentForRSVPKeepsSourceValueWhenUnlisted(t *testing.T) {
	f, err := compileTransform(RuleConfig{
		Transform: &TransformConfig{TransparentForRSVP: []string{"needsAction"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ev := timed("2026-08-18T12:00:00Z", "2026-08-18T13:00:00Z")
	ev.Transparent = true
	ev.RSVP = model.RSVPAccepted
	if !f(ev).Transparent {
		t.Error("an accepted invitation that was transparent at the source was forced opaque")
	}
}

// Most of a calendar is not invitations; those must come through untouched.
func TestTransparentForRSVPIgnoresNonInvitations(t *testing.T) {
	f, err := compileTransform(RuleConfig{
		Transform: &TransformConfig{TransparentForRSVP: []string{"needsAction", "tentative"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ev := timed("2026-08-18T12:00:00Z", "2026-08-18T13:00:00Z")
	if f(ev).Transparent {
		t.Error("event with no RSVP was made transparent")
	}
	ev.Transparent = true
	if !f(ev).Transparent {
		t.Error("event with no RSVP lost the source's own transparency")
	}
}
