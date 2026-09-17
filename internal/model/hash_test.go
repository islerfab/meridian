package model

import (
	"testing"
	"time"
)

func baseContent() ShadowContent {
	return ShadowContent{
		Title:       "Busy",
		Description: "synced by meridian",
		Location:    "Zürich",
		Start:       time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
		End:         time.Date(2026, 8, 18, 13, 0, 0, 0, time.UTC),
		AllDay:      false,
		Transparent: false,
		Reminders:   []int{15, 30},
	}
}

// The canonical serialization is a persisted contract: this golden value
// must only ever change with a deliberate hashPrefix version bump.
func TestContentHashGolden(t *testing.T) {
	const golden = "b6ecb466daeb5611cde42b0b667ba0f0532b1d9b5bbf1d80a565fb395f47c5cc"
	if got := ContentHash(baseContent()); got != golden {
		t.Errorf("golden hash changed: got %s — if this is deliberate, bump hashPrefix and update the golden", got)
	}
}

func TestContentHashTimezoneNormalization(t *testing.T) {
	zurich, err := time.LoadLocation("Europe/Zurich")
	if err != nil {
		t.Fatal(err)
	}
	a := baseContent()
	b := baseContent()
	b.Start = a.Start.In(zurich)
	b.End = a.End.In(zurich)
	if ContentHash(a) != ContentHash(b) {
		t.Error("same instants in different zones must hash identically")
	}
}

func TestContentHashRemindersOrderInsensitive(t *testing.T) {
	a := baseContent()
	b := baseContent()
	b.Reminders = []int{30, 15}
	if ContentHash(a) != ContentHash(b) {
		t.Error("reminder order must not affect the hash")
	}
	// ...but sorting must not mutate the caller's slice.
	if b.Reminders[0] != 30 {
		t.Error("ContentHash mutated the Reminders slice")
	}
}

func TestContentHashFieldSensitivity(t *testing.T) {
	mutations := map[string]func(*ShadowContent){
		"title":       func(c *ShadowContent) { c.Title = "Free" },
		"description": func(c *ShadowContent) { c.Description = "" },
		"location":    func(c *ShadowContent) { c.Location = "Bern" },
		"start":       func(c *ShadowContent) { c.Start = c.Start.Add(time.Minute) },
		"end":         func(c *ShadowContent) { c.End = c.End.Add(time.Minute) },
		"allDay":      func(c *ShadowContent) { c.AllDay = true },
		"transparent": func(c *ShadowContent) { c.Transparent = true },
		"reminders":   func(c *ShadowContent) { c.Reminders = []int{15} },
		"color":       func(c *ShadowContent) { c.Color = "green" },
	}
	base := ContentHash(baseContent())
	for name, mutate := range mutations {
		c := baseContent()
		mutate(&c)
		if ContentHash(c) == base {
			t.Errorf("changing %s did not change the hash", name)
		}
	}
}

// A string value containing a newline must not be able to forge other
// fields' lines in the canonical form.
func TestContentHashNoFieldForgery(t *testing.T) {
	a := ShadowContent{Title: "a\ndescription=b"}
	b := ShadowContent{Title: "a", Description: "b"}
	if ContentHash(a) == ContentHash(b) {
		t.Error("embedded newline forged a field boundary")
	}
	// Escaping itself must be unambiguous: a literal `\n` in the input is
	// distinct from a real newline.
	c := ShadowContent{Title: "a\\ndescription=b"}
	if ContentHash(a) == ContentHash(c) {
		t.Error("literal backslash-n collides with real newline")
	}
}

func TestDurationMinutes(t *testing.T) {
	e := Event{
		Start: time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 8, 18, 13, 30, 0, 0, time.UTC),
	}
	if got := e.DurationMinutes(); got != 90 {
		t.Errorf("DurationMinutes = %d, want 90", got)
	}
}
