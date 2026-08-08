// Package config loads and validates rules.yaml (DESIGN.md Decision 4) and
// compiles it into engine inputs: sync.Rule closures, adapters, notifier.
// Strict parsing — unknown fields are errors; CEL and templates compile and
// type-check at load; every failure is a startup error. Secrets never
// appear in the file: fields reference environment variable NAMES.
package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root of rules.yaml.
type Config struct {
	// Instance is this meridian instance's identity (required; unique per
	// instance, stable, not secret — see DESIGN.md Decision 2).
	Instance string `yaml:"instance"`
	// Interval between reconciliation cycles (Go duration; default 5m).
	Interval string `yaml:"interval"`

	Notifications Notifications `yaml:"notifications"`
	Accounts      []Account     `yaml:"accounts"`
	Rules         []RuleConfig  `yaml:"rules"`

	// IntervalDuration is the parsed Interval (set by Load).
	IntervalDuration time.Duration `yaml:"-"`
}

// Notifications configures the operator notification channel.
type Notifications struct {
	// DiscordWebhookURLEnv names the env var holding the webhook URL
	// (empty = notifications disabled).
	DiscordWebhookURLEnv string `yaml:"discordWebhookURLEnv"`
	// MassDeleteFraction triggers the mass-delete detection when more than
	// this fraction of a rule's shadows is deleted in one cycle
	// (default 0.5; 0 disables).
	MassDeleteFraction *float64 `yaml:"massDeleteFraction"`
}

// Account is one provider connection. Type-specific fields are validated
// per type; setting a field of the other type is an error.
type Account struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"` // caldav | google

	// caldav
	Endpoint    string `yaml:"endpoint"`
	UsernameEnv string `yaml:"usernameEnv"`
	PasswordEnv string `yaml:"passwordEnv"`
	Expansion   string `yaml:"expansion"` // server (default) | client

	// google
	ClientIDEnv     string `yaml:"clientIDEnv"`
	ClientSecretEnv string `yaml:"clientSecretEnv"`
	RefreshTokenEnv string `yaml:"refreshTokenEnv"`

	Calendars []CalendarConfig `yaml:"calendars"`
}

// CalendarConfig names one calendar of an account. The logical reference
// used in rules is "<account>/<calendar>".
type CalendarConfig struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"` // caldav collection path
	ID   string `yaml:"id"`   // google calendar ID
}

// RuleConfig is one sync rule (compiled into sync.Rule).
type RuleConfig struct {
	ID        string           `yaml:"id"`
	From      string           `yaml:"from"`
	To        []string         `yaml:"to"`
	Filter    *FilterConfig    `yaml:"filter"`
	Transform *TransformConfig `yaml:"transform"`
}

// FilterConfig selects source events. All set fields AND together.
// weekdays+window+timezone define a recurring weekly time region; an event
// matches when it overlaps the region. All-day events are governed by
// weekdays only (their dates), never time-windowed (decided 2026-08-08).
type FilterConfig struct {
	Weekdays        []string `yaml:"weekdays"`
	Window          string   `yaml:"window"` // "HH:MM-HH:MM", must not cross midnight
	Timezone        string   `yaml:"timezone"`
	SkipTransparent bool     `yaml:"skipTransparent"`
	SkipAllDay      bool     `yaml:"skipAllDay"`
	When            string   `yaml:"when"` // CEL over the event schema
}

// TransformConfig computes shadow content. Unset fields copy the source
// (faithful mirror, decided 2026-08-08); the literal value "drop" empties a
// string field; other strings are Go templates (a plain literal is a
// trivial template).
type TransformConfig struct {
	Title       *string `yaml:"title"`
	Description *string `yaml:"description"`
	Location    *string `yaml:"location"`
	Transparent *bool   `yaml:"transparent"`
	Reminders   []int   `yaml:"reminders"`
}

// Drop is the transform keyword that empties a string field.
const Drop = "drop"

var weekdayNames = map[string]time.Weekday{
	"mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday, "sun": time.Sunday,
}

var windowRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):([0-5][0-9])-([01][0-9]|2[0-3]):([0-5][0-9])$`)

// Load reads, strictly parses, and validates the config file. It does NOT
// resolve secret env vars (BuildAdapters does) but does validate structure,
// references, windows, CEL, and templates — all failures are startup errors.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if c.Instance == "" {
		fail("instance is required (unique per meridian instance, stable — changing it orphans all shadows)")
	}
	c.IntervalDuration = 5 * time.Minute
	if c.Interval != "" {
		d, err := time.ParseDuration(c.Interval)
		if err != nil || d <= 0 {
			fail("interval %q: must be a positive Go duration", c.Interval)
		} else {
			c.IntervalDuration = d
		}
	}
	if c.Notifications.MassDeleteFraction == nil {
		half := 0.5
		c.Notifications.MassDeleteFraction = &half
	} else if f := *c.Notifications.MassDeleteFraction; f < 0 || f > 1 {
		fail("notifications.massDeleteFraction %v: must be in [0,1]", f)
	}

	calendars := map[string]bool{} // "<account>/<calendar>"
	accountNames := map[string]bool{}
	for i := range c.Accounts {
		a := &c.Accounts[i]
		where := fmt.Sprintf("accounts[%d] (%s)", i, a.Name)
		if a.Name == "" {
			fail("%s: name is required", where)
		}
		if accountNames[a.Name] {
			fail("%s: duplicate account name", where)
		}
		accountNames[a.Name] = true
		if len(a.Calendars) == 0 {
			fail("%s: at least one calendar is required", where)
		}
		switch a.Type {
		case "caldav":
			if a.Endpoint == "" || a.UsernameEnv == "" || a.PasswordEnv == "" {
				fail("%s: endpoint, usernameEnv, passwordEnv are required for caldav", where)
			}
			if a.ClientIDEnv != "" || a.ClientSecretEnv != "" || a.RefreshTokenEnv != "" {
				fail("%s: google-only fields set on caldav account", where)
			}
		case "google":
			if a.ClientIDEnv == "" || a.ClientSecretEnv == "" || a.RefreshTokenEnv == "" {
				fail("%s: clientIDEnv, clientSecretEnv, refreshTokenEnv are required for google", where)
			}
			if a.Endpoint != "" || a.UsernameEnv != "" || a.PasswordEnv != "" || a.Expansion != "" {
				fail("%s: caldav-only fields set on google account", where)
			}
		default:
			fail("%s: type must be caldav or google, got %q", where, a.Type)
		}
		seen := map[string]bool{}
		for j, cal := range a.Calendars {
			cwhere := fmt.Sprintf("%s calendars[%d] (%s)", where, j, cal.Name)
			if cal.Name == "" {
				fail("%s: name is required", cwhere)
			}
			if seen[cal.Name] {
				fail("%s: duplicate calendar name", cwhere)
			}
			seen[cal.Name] = true
			switch a.Type {
			case "caldav":
				if cal.Path == "" {
					fail("%s: path is required for caldav calendars", cwhere)
				}
				if cal.ID != "" {
					fail("%s: id is a google-only field", cwhere)
				}
			case "google":
				if cal.ID == "" {
					fail("%s: id is required for google calendars", cwhere)
				}
				if cal.Path != "" {
					fail("%s: path is a caldav-only field", cwhere)
				}
			}
			calendars[a.Name+"/"+cal.Name] = true
		}
	}

	if len(c.Rules) == 0 {
		fail("at least one rule is required")
	}
	ruleIDs := map[string]bool{}
	for i := range c.Rules {
		r := &c.Rules[i]
		where := fmt.Sprintf("rules[%d] (%s)", i, r.ID)
		if r.ID == "" {
			fail("%s: id is required", where)
		}
		if ruleIDs[r.ID] {
			fail("%s: duplicate rule id (marker collision)", where)
		}
		ruleIDs[r.ID] = true
		if !calendars[r.From] {
			fail("%s: from %q is not a configured calendar", where, r.From)
		}
		if len(r.To) == 0 {
			fail("%s: at least one destination is required", where)
		}
		for _, to := range r.To {
			if !calendars[to] {
				fail("%s: to %q is not a configured calendar", where, to)
			}
			if to == r.From {
				fail("%s: destination equals source %q", where, to)
			}
		}
		if f := r.Filter; f != nil {
			for _, wd := range f.Weekdays {
				if _, ok := weekdayNames[wd]; !ok {
					fail("%s: weekday %q (want mon..sun)", where, wd)
				}
			}
			if f.Window != "" && !windowRe.MatchString(f.Window) {
				fail("%s: window %q (want HH:MM-HH:MM, not crossing midnight)", where, f.Window)
			}
			if f.Window != "" {
				if s, e, err := parseWindow(f.Window); err == nil && e <= s {
					fail("%s: window %q: end must be after start (midnight-crossing windows are not supported)", where, f.Window)
				}
			}
			if (f.Window != "" || len(f.Weekdays) > 0) && f.Timezone == "" {
				fail("%s: timezone is required when window or weekdays are set", where)
			}
			if f.Timezone != "" {
				if _, err := time.LoadLocation(f.Timezone); err != nil {
					fail("%s: timezone %q: %v", where, f.Timezone, err)
				}
			}
		}
	}
	return errors.Join(errs...)
}

// parseWindow returns start/end as minutes since local midnight.
func parseWindow(w string) (startMin, endMin int, err error) {
	m := windowRe.FindStringSubmatch(w)
	if m == nil {
		return 0, 0, fmt.Errorf("window %q: want HH:MM-HH:MM", w)
	}
	toMin := func(h, min string) int {
		return int(h[0]-'0')*600 + int(h[1]-'0')*60 + int(min[0]-'0')*10 + int(min[1]-'0')
	}
	return toMin(m[1], m[2]), toMin(m[3], m[4]), nil
}
