package main

import "testing"

func TestDeGo(t *testing.T) {
	tests := []struct {
		name  string
		ident string
		doc   string
		want  string
	}{
		{"is-copula", "Instance", "Instance is this instance's identity.", "This instance's identity."},
		{"verb-not-copula", "MassDeleteFraction", "MassDeleteFraction triggers the guard.", "Triggers the guard."},
		{"selects", "Type", "Type selects the provider: google or caldav.", "Selects the provider: google or caldav."},
		{"type-doc", "Config", "Config is the root of rules.yaml.", "The root of rules.yaml."},
		{"multiline", "Name", "Name is unique within\nthe account.", "Unique within\nthe account."},
		{"is-inside-word-not-stripped", "Island", "Island isolates things.", "Isolates things."},
		{"already-lowercase-after-strip", "AllDay", "AllDay is true for all-day events.", "True for all-day events."},
		{"leading-code-span", "Interval", "Interval is `5m` by default.", "`5m` by default."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := deGo(tc.ident, tc.doc)
			if err != nil {
				t.Fatalf("deGo(%q, %q) errored: %v", tc.ident, tc.doc, err)
			}
			if got != tc.want {
				t.Errorf("deGo(%q, %q) = %q, want %q", tc.ident, tc.doc, got, tc.want)
			}
		})
	}
}

func TestDeGoRejects(t *testing.T) {
	tests := []struct {
		name  string
		ident string
		doc   string
	}{
		{"empty", "Instance", ""},
		{"wrong-identifier", "Instance", "The instance identity."},
		// Without a word-boundary check, "ID" would swallow the leading
		// "ID" of "IDs" and emit "s are opaque."
		{"identifier-is-prefix-of-first-word", "ID", "IDs are opaque."},
		{"nothing-beyond-identifier", "Instance", "Instance"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := deGo(tc.ident, tc.doc); err == nil {
				t.Errorf("deGo(%q, %q) = %q, want error", tc.ident, tc.doc, got)
			}
		})
	}
}

func TestCheckMarkup(t *testing.T) {
	ok := []string{
		"Plain prose with no markup at all.",
		"A placeholder in `<account>/<calendar>` form is fine.",
		"Arrows like Settings -> Integrate render literally.",
		"Flags such as `--once` stay inside code spans.",
	}
	for _, s := range ok {
		if err := checkMarkup("t", s); err != nil {
			t.Errorf("checkMarkup(%q) errored: %v", s, err)
		}
	}

	bad := []string{
		"A bare <old-id> parses as a tag and vanishes.",
		"Unbalanced `backtick run.",
		"Outside `a span` a bare <tag> is still wrong.",
	}
	for _, s := range bad {
		if err := checkMarkup("t", s); err == nil {
			t.Errorf("checkMarkup(%q) = nil, want error", s)
		}
	}
}
